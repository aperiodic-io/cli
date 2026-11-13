package aperiodic

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// barMicros is the bar time the fake rows carry (row in stream_test.go), in
// the microseconds the topics use.
const barMicros int64 = 1791307260000000

type replayLine struct {
	Channel  string          `json:"channel"`
	Snapshot bool            `json:"snapshot"`
	Replay   bool            `json:"replay"`
	Data     json.RawMessage `json:"data"`
}

func parseReplayLines(t *testing.T, stdout string) []replayLine {
	t.Helper()
	var lines []replayLine
	for _, raw := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if raw == "" {
			continue
		}
		var line replayLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("stdout line is not JSON: %q", raw)
		}
		lines = append(lines, line)
	}
	return lines
}

func replayFrame(time int64, rows ...string) string {
	return `{"channel":"` + streamTestChannel + `","replay":true,"time":` + strconv.FormatInt(time, 10) + `,"data":[` + strings.Join(rows, ",") + `]}`
}

func replayedFrame(id string, from, to int64, bars, rows int, truncated bool) string {
	return `{"op":"replayed","id":"` + id + `","channel":"` + streamTestChannel + `","from":` + strconv.FormatInt(from, 10) +
		`,"to":` + strconv.FormatInt(to, 10) + `,"bars":` + strconv.Itoa(bars) + `,"rows":` + strconv.Itoa(rows) +
		`,"missing":[],"truncated":` + strconv.FormatBool(truncated) + `}`
}

func readSubscribe(ctx context.Context, t *testing.T, conn *websocket.Conn) fakeSubscribe {
	t.Helper()
	_, msg, err := conn.Read(ctx)
	if err != nil {
		t.Errorf("expected another subscribe: %v", err)
		return fakeSubscribe{}
	}
	var sub fakeSubscribe
	if err := json.Unmarshal(msg, &sub); err != nil {
		t.Errorf("subscribe is not JSON: %s", msg)
	}
	return sub
}

// A replay frame holds a whole bar, over a megabyte on an all-symbols channel:
// a client that cannot read one reconnects and asks for it again forever.
func TestStream_SinceReplaysBeforeTheLiveRows(t *testing.T) {
	from := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC).UnixMilli()
	padding := strings.Repeat("x", 2<<20)
	f := startFakeStream(t, &fakeStream{
		script: func(ctx context.Context, conn *websocket.Conn, sub fakeSubscribe, _ int) {
			ack(ctx, conn, sub, "")
			send(ctx, conn, replayFrame(from,
				`{"symbol":"perpetual-BTC-USDT:USDT","time":1791307200000000,"close":1}`,
				`{"symbol":"perpetual-ETH-USDT:USDT","time":1791307200000000,"pad":"`+padding+`"}`))
			send(ctx, conn, replayedFrame(sub.ID, from, from, 1, 2, false))
			send(ctx, conn, row("3"))
			drain(ctx, conn)
		},
	})

	stdout, stderr, code := runCLI("stream", "ohlcv", "--since", "2026-11-09T10:00:00Z", "--count", "1", "--duration", "10s")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}

	_, _, subs := f.recorded()
	if got := subs[0].Channels[0].Since; got != from {
		t.Errorf("expected since %d, got %d", from, got)
	}
	lines := parseReplayLines(t, stdout)
	if len(lines) != 3 || !lines[0].Replay || !lines[1].Replay || lines[2].Replay {
		t.Fatalf("expected two replayed rows then a live one, got: %.300s", stdout)
	}
	if !strings.Contains(stderr, "Replayed "+streamTestChannel+": 1 bars, 2 rows") {
		t.Errorf("expected the replay summary on stderr, got: %s", stderr)
	}
}

func TestStream_ReconnectReplaysFromTheNewestBar(t *testing.T) {
	f := startFakeStream(t, &fakeStream{
		script: func(ctx context.Context, conn *websocket.Conn, sub fakeSubscribe, n int) {
			ack(ctx, conn, sub, "")
			if n == 1 {
				send(ctx, conn, row("1"))
				conn.CloseNow()
				return
			}
			send(ctx, conn, replayFrame(barMicros/1000, `{"symbol":"perpetual-BTC-USDT:USDT","time":1791307320000000,"close":2}`))
			send(ctx, conn, replayedFrame(sub.ID, barMicros/1000, barMicros/1000+60_000, 1, 1, false))
			send(ctx, conn, row("3"))
			drain(ctx, conn)
		},
	})

	stdout, stderr, code := runCLI("stream", "ohlcv", "--count", "2", "--duration", "10s")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}

	_, _, subs := f.recorded()
	if len(subs) != 2 {
		t.Fatalf("expected two subscribes, got %d", len(subs))
	}
	if got := subs[0].Channels[0].Since; got != 0 {
		t.Errorf("expected no since on the first connect, got %d", got)
	}
	if got := subs[1].Channels[0].Since; got != barMicros/1000 {
		t.Errorf("expected the reconnect to replay from the newest bar %d, got %d", barMicros/1000, got)
	}
	lines := parseReplayLines(t, stdout)
	if len(lines) != 3 || lines[0].Replay || !lines[1].Replay || lines[2].Replay {
		t.Errorf("expected live, replayed, live, got: %s", stdout)
	}
}

func TestStream_NoReplayAfterAReconnectWhenTurnedOff(t *testing.T) {
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

	_, stderr, code := runCLI("stream", "ohlcv", "--replay=false", "--count", "2", "--duration", "10s")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	_, _, subs := f.recorded()
	if got := subs[1].Channels[0].Since; got != 0 {
		t.Errorf("expected no since with --replay=false, got %d", got)
	}
}

// After a deploy every client reconnects with since at once; the server
// refuses past its cap and expects a retry after a pause.
func TestStream_BusyRefusalIsSentAgain(t *testing.T) {
	base := streamBusyRetryBase
	streamBusyRetryBase = 10 * time.Millisecond
	t.Cleanup(func() { streamBusyRetryBase = base })

	retried := make(chan fakeSubscribe, 1)
	startFakeStream(t, &fakeStream{
		script: func(ctx context.Context, conn *websocket.Conn, sub fakeSubscribe, _ int) {
			send(ctx, conn, `{"op":"error","id":"`+sub.ID+`","code":"limit_exceeded","message":"Too many replays are running at once"}`)
			again := readSubscribe(ctx, t, conn)
			retried <- again
			ack(ctx, conn, again, "")
			send(ctx, conn, row("1"))
			drain(ctx, conn)
		},
	})

	_, stderr, code := runCLI("stream", "ohlcv", "--since", "30m", "--count", "1", "--duration", "10s")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	if again := <-retried; again.ID != streamSubscribeID || again.Channels[0].Since == 0 {
		t.Errorf("expected the same subscribe, since included, sent again; got %+v", again)
	}
}

// The server caps one replay; the rest comes from asking again after it.
func TestStream_TruncatedReplayIsFollowedUp(t *testing.T) {
	from := barMicros / 1000
	followUp := make(chan fakeSubscribe, 1)
	startFakeStream(t, &fakeStream{
		script: func(ctx context.Context, conn *websocket.Conn, sub fakeSubscribe, _ int) {
			ack(ctx, conn, sub, "")
			send(ctx, conn, replayFrame(from, `{"symbol":"perpetual-BTC-USDT:USDT","time":1791307260000000}`))
			send(ctx, conn, replayedFrame(sub.ID, from, from, 1, 1, true))
			next := readSubscribe(ctx, t, conn)
			followUp <- next
			send(ctx, conn, row("2"))
			drain(ctx, conn)
		},
	})

	_, stderr, code := runCLI("stream", "ohlcv", "--since", "1h", "--count", "1", "--duration", "10s")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	next := <-followUp
	if next.ID == streamSubscribeID || len(next.Channels) != 1 || next.Channels[0].Since != from+60_000 {
		t.Errorf("expected a follow-up subscribe from the next bar, got %+v", next)
	}
	if !strings.Contains(stderr, "more to come") {
		t.Errorf("expected the truncation on stderr, got: %s", stderr)
	}
}

// The server keeps no history below a minute and refuses since there.
func TestStream_SinceNeedsAMinuteOrLonger(t *testing.T) {
	t.Setenv("APERIODIC_API_KEY", streamTestKey)
	_, stderr, code := runCLI("stream", "ohlcv", "--interval", "15s", "--since", "30m")
	if code != 1 || !strings.Contains(stderr, "1m or longer") {
		t.Fatalf("expected exit 1 naming the interval, got %d: %s", code, stderr)
	}
	_, stderr, code = runCLI("stream", "ohlcv", "--since", "yesterday")
	if code != 1 || !strings.Contains(stderr, "RFC 3339") {
		t.Fatalf("expected exit 1 explaining --since, got %d: %s", code, stderr)
	}
}

func TestStream_NoSinceBelowAMinuteOnReconnect(t *testing.T) {
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

	_, stderr, code := runCLI("stream", "ohlcv", "--interval", "15s", "--count", "2", "--duration", "10s")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	_, _, subs := f.recorded()
	if got := subs[1].Channels[0].Since; got != 0 {
		t.Errorf("expected no since at 15s, got %d", got)
	}
}

func emptyTruncatedFrame(id string, from int64) string {
	return `{"op":"replayed","id":"` + id + `","channel":"` + streamTestChannel + `","from":` + strconv.FormatInt(from, 10) +
		`,"to":null,"bars":0,"rows":0,"missing":[],"truncated":true}`
}

// The size cap is shared by a subscribe's channels: one can leave another
// with no bars and no `to`. It is asked for again from `from`; empty a second
// time from the same bar, that bar is larger than the cap, and the stream
// stops asking.
func TestStream_EmptyTruncatedReplayIsAskedAgainThenGivenUp(t *testing.T) {
	from := barMicros / 1000
	followUp := make(chan fakeSubscribe, 1)
	startFakeStream(t, &fakeStream{
		script: func(ctx context.Context, conn *websocket.Conn, sub fakeSubscribe, _ int) {
			ack(ctx, conn, sub, "")
			send(ctx, conn, emptyTruncatedFrame(sub.ID, from))
			next := readSubscribe(ctx, t, conn)
			followUp <- next
			send(ctx, conn, emptyTruncatedFrame(next.ID, from))
			send(ctx, conn, row("2"))
			drain(ctx, conn)
		},
	})

	_, stderr, code := runCLI("stream", "ohlcv", "--since", "1h", "--count", "1", "--duration", "10s")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	if next := <-followUp; next.Channels[0].Since != from {
		t.Errorf("expected the follow-up from the empty replay's from %d, got %+v", from, next)
	}
	if !strings.Contains(stderr, "larger than the server's replay cap") {
		t.Errorf("expected the stop on stderr, got: %s", stderr)
	}
	if strings.Count(stderr, "more to come") != 1 {
		t.Errorf("expected more to come only for the first replay, got: %s", stderr)
	}
}

// Held live rows come before `replayed`, so the newest bar received runs
// ahead of a replay still catching up; a drop then must not skip the rest.
func TestStream_DropDuringACatchUpResumesWhereTheReplayGotTo(t *testing.T) {
	from := barMicros / 1000
	f := startFakeStream(t, &fakeStream{
		script: func(ctx context.Context, conn *websocket.Conn, sub fakeSubscribe, n int) {
			ack(ctx, conn, sub, "")
			if n == 1 {
				send(ctx, conn, replayFrame(from, `{"symbol":"perpetual-BTC-USDT:USDT","time":1791307260000000}`))
				send(ctx, conn, `{"channel":"`+streamTestChannel+`","data":{"symbol":"perpetual-BTC-USDT:USDT","time":1791307860000000,"close":1}}`)
				send(ctx, conn, replayedFrame(sub.ID, from, from, 1, 1, true))
				readSubscribe(ctx, t, conn)
				conn.CloseNow()
				return
			}
			send(ctx, conn, row("3"))
			drain(ctx, conn)
		},
	})

	_, stderr, code := runCLI("stream", "ohlcv", "--since", "1h", "--count", "2", "--duration", "10s")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	_, _, subs := f.recorded()
	if len(subs) != 2 {
		t.Fatalf("expected two subscribes, got %d", len(subs))
	}
	if got := subs[1].Channels[0].Since; got != from+60_000 {
		t.Errorf("expected the reconnect to resume the replay from %d, got %d", from+60_000, got)
	}
}

func TestStream_RefusedFollowUpIsSentAgain(t *testing.T) {
	base := streamBusyRetryBase
	streamBusyRetryBase = 10 * time.Millisecond
	t.Cleanup(func() { streamBusyRetryBase = base })

	from := barMicros / 1000
	followUps := make(chan fakeSubscribe, 2)
	startFakeStream(t, &fakeStream{
		script: func(ctx context.Context, conn *websocket.Conn, sub fakeSubscribe, _ int) {
			ack(ctx, conn, sub, "")
			send(ctx, conn, replayFrame(from, `{"symbol":"perpetual-BTC-USDT:USDT","time":1791307260000000}`))
			send(ctx, conn, replayedFrame(sub.ID, from, from, 1, 1, true))
			first := readSubscribe(ctx, t, conn)
			followUps <- first
			send(ctx, conn, `{"op":"error","id":"`+first.ID+`","code":"limit_exceeded","message":"At most 4 replays can be queued on a connection"}`)
			followUps <- readSubscribe(ctx, t, conn)
			send(ctx, conn, row("2"))
			drain(ctx, conn)
		},
	})

	_, stderr, code := runCLI("stream", "ohlcv", "--since", "1h", "--count", "1", "--duration", "10s")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	first, second := <-followUps, <-followUps
	if first.Channels[0].Since != from+60_000 || second.Channels[0].Since != from+60_000 {
		t.Errorf("expected the refused follow-up sent again from %d, got %+v then %+v", from+60_000, first, second)
	}
	if strings.Contains(stderr, "limit_exceeded") {
		t.Errorf("expected the refusal handled, not warned, got: %s", stderr)
	}
}
