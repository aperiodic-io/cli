package aperiodic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const (
	streamTestChannel = "ohlcv.binance-futures.1m"
	streamTestKey     = "test-key"
)

type fakeSubscribe struct {
	Op       string          `json:"op"`
	ID       string          `json:"id"`
	Channels []StreamChannel `json:"channels"`
}

// fakeStream is a stand-in for the stream service. Each connection runs
// script with the subscribe message it received and its 1-based number.
type fakeStream struct {
	t      *testing.T
	status int
	body   string
	script func(ctx context.Context, conn *websocket.Conn, sub fakeSubscribe, n int)

	mu      sync.Mutex
	headers []http.Header
	urls    []string
	subs    []fakeSubscribe
}

func (f *fakeStream) connections() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.headers)
}

func (f *fakeStream) recorded() ([]http.Header, []string, []fakeSubscribe) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.headers, f.urls, f.subs
}

func (f *fakeStream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.headers = append(f.headers, r.Header.Clone())
	f.urls = append(f.urls, r.URL.String())
	n := len(f.headers)
	f.mu.Unlock()

	if f.status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.body))
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		f.t.Errorf("accept: %v", err)
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()
	_, msg, err := conn.Read(ctx)
	if err != nil {
		return
	}
	var sub fakeSubscribe
	if err := json.Unmarshal(msg, &sub); err != nil {
		f.t.Errorf("subscribe is not JSON: %s", msg)
		return
	}
	f.mu.Lock()
	f.subs = append(f.subs, sub)
	f.mu.Unlock()

	f.script(ctx, conn, sub, n)
}

func startFakeStream(t *testing.T, f *fakeStream) *fakeStream {
	t.Helper()
	f.t = t
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	t.Setenv("APERIODIC_STREAM_URL", "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/stream")
	t.Setenv("APERIODIC_API_KEY", streamTestKey)
	t.Setenv("CF_ACCESS_CLIENT_ID", "")
	t.Setenv("CF_ACCESS_CLIENT_SECRET", "")

	base, ceiling := streamBackoffBase, streamBackoffMax
	streamBackoffBase, streamBackoffMax = 10*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { streamBackoffBase, streamBackoffMax = base, ceiling })
	return f
}

func send(ctx context.Context, conn *websocket.Conn, frame string) {
	_ = conn.Write(ctx, websocket.MessageText, []byte(frame))
}

func ack(ctx context.Context, conn *websocket.Conn, sub fakeSubscribe, rejected string) {
	send(ctx, conn, `{"op":"subscribed","id":"`+sub.ID+`","channels":["`+streamTestChannel+`"],"rejected":[`+rejected+`]}`)
}

func row(close string) string {
	return `{"channel":"` + streamTestChannel + `","data":{"exchange":11,"symbol":"perpetual-BTC-USDT:USDT","interval":"1m","time":1791307260000000,"close":` + close + `}}`
}

// drain answers the client's close handshake.
func drain(ctx context.Context, conn *websocket.Conn) {
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
	}
}

type streamLine struct {
	Channel  string          `json:"channel"`
	Snapshot bool            `json:"snapshot"`
	Data     json.RawMessage `json:"data"`
}

func parseStreamLines(t *testing.T, stdout string) []streamLine {
	t.Helper()
	var lines []streamLine
	for _, raw := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if raw == "" {
			continue
		}
		var line streamLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("stdout line is not JSON: %q", raw)
		}
		lines = append(lines, line)
	}
	return lines
}

func TestStream_SendsKeyHeaderAndPrintsRows(t *testing.T) {
	f := startFakeStream(t, &fakeStream{
		script: func(ctx context.Context, conn *websocket.Conn, sub fakeSubscribe, _ int) {
			ack(ctx, conn, sub, "")
			send(ctx, conn, `{"channel":"`+streamTestChannel+`","snapshot":true,"data":{"symbol":"perpetual-BTC-USDT:USDT","close":1}}`)
			send(ctx, conn, `{"op":"heartbeat","time":1791307260000000}`)
			send(ctx, conn, `{"op":"error","code":"limit_exceeded","message":"Too many messages"}`)
			send(ctx, conn, row("2"))
			drain(ctx, conn)
		},
	})

	stdout, stderr, code := runCLI(
		"stream", "ohlcv",
		"--exchange", "binance-futures",
		"--interval", "1m",
		"--symbols", "perpetual-BTC-USDT:USDT,perpetual-ETH-USDT:USDT",
		"--count", "1",
	)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}

	headers, urls, subs := f.recorded()
	header := headers[0]
	if got := header.Get("X-API-KEY"); got != streamTestKey {
		t.Errorf("expected the key in X-API-KEY, got %q", got)
	}
	if strings.Contains(urls[0], streamTestKey) {
		t.Errorf("the key must not be in the URL: %s", urls[0])
	}

	sub := subs[0]
	if sub.Op != "subscribe" || sub.ID == "" || len(sub.Channels) != 1 {
		t.Fatalf("unexpected subscribe: %+v", sub)
	}
	want := StreamChannel{Dataset: "ohlcv", Exchange: "binance-futures", Interval: "1m", Symbols: []string{"perpetual-BTC-USDT:USDT", "perpetual-ETH-USDT:USDT"}}
	got := sub.Channels[0]
	if got.Dataset != want.Dataset || got.Exchange != want.Exchange || got.Interval != want.Interval || strings.Join(got.Symbols, ",") != strings.Join(want.Symbols, ",") {
		t.Errorf("expected channel %+v, got %+v", want, got)
	}

	lines := parseStreamLines(t, stdout)
	if len(lines) != 2 {
		t.Fatalf("expected the snapshot and one live row (the snapshot does not count), got:\n%s", stdout)
	}
	if !lines[0].Snapshot || lines[1].Snapshot {
		t.Errorf("expected the snapshot row marked and the live row not, got %+v", lines)
	}
	for _, line := range lines {
		if line.Channel != streamTestChannel {
			t.Errorf("expected channel %s, got %q", streamTestChannel, line.Channel)
		}
	}
	if !strings.Contains(string(lines[1].Data), `"time":1791307260000000`) {
		t.Errorf("expected the data object verbatim, got %s", lines[1].Data)
	}

	if !strings.Contains(stderr, "Subscribed: "+streamTestChannel) {
		t.Errorf("expected the granted channel on stderr, got: %s", stderr)
	}
	if !strings.Contains(stderr, "limit_exceeded") || !strings.Contains(stderr, "Too many messages") {
		t.Errorf("expected the server error on stderr, got: %s", stderr)
	}
	if strings.Contains(stdout, "heartbeat") {
		t.Errorf("heartbeats must not reach stdout: %s", stdout)
	}
}

func TestStream_OmitsSymbolsWhenNoneGiven(t *testing.T) {
	f := startFakeStream(t, &fakeStream{
		script: func(ctx context.Context, conn *websocket.Conn, sub fakeSubscribe, _ int) {
			ack(ctx, conn, sub, "")
			send(ctx, conn, row("1"))
			drain(ctx, conn)
		},
	})

	_, stderr, code := runCLI("stream", "ohlcv", "--count", "1")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	_, _, subs := f.recorded()
	ch := subs[0].Channels[0]
	if ch.Symbols != nil {
		t.Errorf("expected no symbols, got %v", ch.Symbols)
	}
	if ch.Exchange != "binance-futures" || ch.Interval != "1m" {
		t.Errorf("expected the binance-futures 1m defaults, got %+v", ch)
	}
}

func TestStream_HandshakeRefusalsExitWithoutRetrying(t *testing.T) {
	tests := []struct {
		status  int
		body    string
		message string
	}{
		{http.StatusUnauthorized, `{"error":"Invalid API key"}`, "Invalid API key"},
		{http.StatusForbidden, `{"error":"Live data is not on your plan"}`, "Live data is not on your plan"},
		{http.StatusTooManyRequests, `{"error":"Too many connections (max 2)"}`, "Too many connections (max 2)"},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			f := startFakeStream(t, &fakeStream{status: tt.status, body: tt.body})

			stdout, stderr, code := runCLI("stream", "ohlcv", "--duration", "5s")
			if code != 1 {
				t.Fatalf("expected exit code 1, got %d; stderr: %s", code, stderr)
			}
			if !strings.Contains(stderr, tt.message) {
				t.Errorf("expected the server's message, got: %s", stderr)
			}
			if stdout != "" {
				t.Errorf("expected nothing on stdout, got: %s", stdout)
			}
			if n := f.connections(); n != 1 {
				t.Errorf("expected one attempt, got %d", n)
			}
		})
	}
}

func TestStream_ExitsWhenEveryChannelIsRejected(t *testing.T) {
	startFakeStream(t, &fakeStream{
		script: func(ctx context.Context, conn *websocket.Conn, sub fakeSubscribe, _ int) {
			send(ctx, conn, `{"op":"subscribed","id":"`+sub.ID+`","channels":[],"rejected":[{"channel":"nope.binance-futures.1m","code":"unknown_channel","message":"No such channel"}]}`)
			drain(ctx, conn)
		},
	})

	_, stderr, code := runCLI("stream", "nope", "--duration", "5s")
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "unknown_channel") || !strings.Contains(stderr, "No such channel") {
		t.Errorf("expected the rejection on stderr, got: %s", stderr)
	}
}

func TestStream_PartialRejectionWarnsAndStreams(t *testing.T) {
	startFakeStream(t, &fakeStream{
		script: func(ctx context.Context, conn *websocket.Conn, sub fakeSubscribe, _ int) {
			ack(ctx, conn, sub, `{"channel":"ohlcv.binance-futures.1m:perpetual-DOGE-USDT:USDT","code":"not_entitled","message":"Symbol not in plan"}`)
			send(ctx, conn, row("1"))
			drain(ctx, conn)
		},
	})

	stdout, stderr, code := runCLI("stream", "ohlcv", "--count", "1")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "not_entitled") || !strings.Contains(stderr, "Symbol not in plan") {
		t.Errorf("expected the rejection on stderr, got: %s", stderr)
	}
	if len(parseStreamLines(t, stdout)) != 1 {
		t.Errorf("expected one row, got: %s", stdout)
	}
}

func TestStream_ReconnectsAndResubscribesAfterADrop(t *testing.T) {
	f := startFakeStream(t, &fakeStream{
		script: func(ctx context.Context, conn *websocket.Conn, sub fakeSubscribe, n int) {
			ack(ctx, conn, sub, "")
			if n == 1 {
				send(ctx, conn, row("1"))
				conn.CloseNow()
				return
			}
			send(ctx, conn, row("2"))
			drain(ctx, conn)
		},
	})

	stdout, stderr, code := runCLI("stream", "ohlcv", "--symbols", "perpetual-BTC-USDT:USDT", "--count", "2", "--duration", "10s")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	if n := f.connections(); n != 2 {
		t.Fatalf("expected two connections, got %d", n)
	}
	_, _, subs := f.recorded()
	if len(subs) != 2 || subs[1].Channels[0].Symbols[0] != "perpetual-BTC-USDT:USDT" {
		t.Errorf("expected the subscription re-sent, got %+v", subs)
	}
	if len(parseStreamLines(t, stdout)) != 2 {
		t.Errorf("expected a row from each connection, got: %s", stdout)
	}
	if !strings.Contains(stderr, "reconnecting") {
		t.Errorf("expected the reconnect on stderr, got: %s", stderr)
	}
}

func TestStream_DoesNotReconnectAfterTerminalCloses(t *testing.T) {
	tests := []struct {
		code   websocket.StatusCode
		reason string
	}{
		{4001, "Plan lapsed"},
		{websocket.StatusPolicyViolation, "Rate limit abuse"},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			f := startFakeStream(t, &fakeStream{
				script: func(ctx context.Context, conn *websocket.Conn, sub fakeSubscribe, _ int) {
					ack(ctx, conn, sub, "")
					_ = conn.Close(tt.code, tt.reason)
				},
			})

			_, stderr, code := runCLI("stream", "ohlcv", "--duration", "5s")
			if code != 1 {
				t.Fatalf("expected exit code 1, got %d; stderr: %s", code, stderr)
			}
			if !strings.Contains(stderr, tt.reason) {
				t.Errorf("expected the close reason, got: %s", stderr)
			}
			if n := f.connections(); n != 1 {
				t.Errorf("expected no reconnect, got %d connections", n)
			}
		})
	}
}

func TestStream_DurationStopsCleanly(t *testing.T) {
	closed := make(chan websocket.StatusCode, 1)
	startFakeStream(t, &fakeStream{
		script: func(ctx context.Context, conn *websocket.Conn, sub fakeSubscribe, _ int) {
			ack(ctx, conn, sub, "")
			for {
				if _, _, err := conn.Read(ctx); err != nil {
					closed <- websocket.CloseStatus(err)
					return
				}
			}
		},
	})

	start := time.Now()
	_, stderr, code := runCLI("stream", "ohlcv", "--duration", "300ms")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("expected to stop after about 300ms, took %s", elapsed)
	}
	select {
	case status := <-closed:
		if status != websocket.StatusNormalClosure {
			t.Errorf("expected a normal close, got %v", status)
		}
	case <-time.After(2 * time.Second):
		t.Error("the server never saw the close")
	}
}

func TestStream_RequiresAKeyAndADataset(t *testing.T) {
	t.Setenv("APERIODIC_API_KEY", "")
	_, stderr, code := runCLI("stream", "ohlcv")
	if code != 1 || !strings.Contains(stderr, "APERIODIC_API_KEY") {
		t.Errorf("expected a missing-key error, got %d: %s", code, stderr)
	}

	t.Setenv("APERIODIC_API_KEY", streamTestKey)
	_, stderr, code = runCLI("stream")
	if code != 1 || !strings.Contains(stderr, "dataset") {
		t.Errorf("expected a missing-dataset error, got %d: %s", code, stderr)
	}
}

func TestStream_Help(t *testing.T) {
	stdout, _, code := runCLI("stream", "help")
	if code != 0 || !strings.Contains(stdout, "aperiodic stream <dataset>") {
		t.Errorf("expected stream usage, got %d: %s", code, stdout)
	}
}
