package aperiodic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const (
	DefaultStreamURL = "wss://stream.aperiodic.io/v1/stream"

	// streamSubscribeID is the id of the one subscribe; an op:"error" with
	// it refuses the subscription.
	streamSubscribeID = "s1"

	// The server sends a heartbeat every ~30s when idle; a silent socket
	// for longer than this is treated as dropped.
	streamIdleTimeout  = 75 * time.Second
	streamDialTimeout  = 15 * time.Second
	streamMessageLimit = 1 << 20
)

var (
	streamBackoffBase  = 500 * time.Millisecond
	streamBackoffMax   = 30 * time.Second
	streamHealthyAfter = 60 * time.Second
)

// StreamChannel is one subscription; Symbols nil means every symbol the
// plan allows.
type StreamChannel struct {
	Dataset  string   `json:"dataset"`
	Exchange string   `json:"exchange"`
	Interval string   `json:"interval"`
	Symbols  []string `json:"symbols,omitempty"`
}

type StreamRejection struct {
	Channel string `json:"channel"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// StreamRow is a data frame, printed as one JSON line.
type StreamRow struct {
	Channel  string          `json:"channel"`
	Snapshot bool            `json:"snapshot"`
	Data     json.RawMessage `json:"data"`
}

type streamFrame struct {
	Op       string            `json:"op"`
	ID       string            `json:"id"`
	Code     string            `json:"code"`
	Message  string            `json:"message"`
	Channels []string          `json:"channels"`
	Rejected []StreamRejection `json:"rejected"`

	Channel  string          `json:"channel"`
	Snapshot bool            `json:"snapshot"`
	Data     json.RawMessage `json:"data"`
}

// StreamHandshakeError is an HTTP refusal before the upgrade.
type StreamHandshakeError struct {
	StatusCode int
	Message    string
}

func (e *StreamHandshakeError) Error() string {
	return fmt.Sprintf("%d: %s", e.StatusCode, e.Message)
}

// StreamClosedError is a close the client must not reconnect after: any
// 4000-4999 code (4001 is a lapsed plan or rotated key) or 1008.
type StreamClosedError struct {
	Code   websocket.StatusCode
	Reason string
}

func (e *StreamClosedError) Error() string {
	return fmt.Sprintf("stream closed by the server (%d): %s", int(e.Code), e.Reason)
}

// StreamRejectedError means no channel is subscribed: all were rejected on
// subscribe, or the server unsubscribed the last of them.
type StreamRejectedError struct {
	Rejected []StreamRejection
}

func (e *StreamRejectedError) Error() string {
	return "no channel is subscribed"
}

// StreamSubscribeError is an op:"error" answering the subscribe itself.
type StreamSubscribeError struct {
	Code    string
	Message string
}

func (e *StreamSubscribeError) Error() string {
	return fmt.Sprintf("subscription refused: %s: %s", e.Code, e.Message)
}

// errStreamDone ends the stream because the caller has what it wanted.
var errStreamDone = errors.New("stream done")

type StreamHandlers struct {
	// Row receives each data row; returning false closes the stream.
	Row          func(StreamRow) bool
	Subscribed   func(granted []string, rejected []StreamRejection)
	Unsubscribed func(removed []string, rejected []StreamRejection)
	ServerErr    func(code, message string)
	// Reconnect reports a retry; connected says whether the failed attempt
	// had a connection to lose.
	Reconnect func(cause error, wait time.Duration, attempt int, connected bool)
}

type Streamer struct {
	URL      string
	Header   http.Header
	Channels []StreamChannel
	Handlers StreamHandlers
}

// NewStreamer reads APERIODIC_STREAM_URL and the Cloudflare Access service
// token the same way the REST client does.
func NewStreamer(apiKey string, env func(string) string, channels []StreamChannel, handlers StreamHandlers) *Streamer {
	header := make(http.Header)
	header.Set("X-API-KEY", apiKey)
	if id, secret := env("CF_ACCESS_CLIENT_ID"), env("CF_ACCESS_CLIENT_SECRET"); id != "" && secret != "" {
		header.Set("CF-Access-Client-Id", id)
		header.Set("CF-Access-Client-Secret", secret)
	}

	url := env("APERIODIC_STREAM_URL")
	if url == "" {
		url = DefaultStreamURL
	}

	return &Streamer{URL: url, Header: header, Channels: channels, Handlers: handlers}
}

// sessionResult describes how one connection went.
type sessionResult struct {
	// connected: the handshake succeeded, or at least got an HTTP response.
	connected  bool
	subscribed bool
	// healthy: the connection delivered a row or stayed up for
	// streamHealthyAfter, so the backoff starts over.
	healthy bool
	err     error
}

// Run streams until ctx ends (nil), Row asks to stop (nil), or a final error
// (see isFinal). Anything else reconnects with capped, jittered exponential
// backoff and subscribes again.
func (s *Streamer) Run(ctx context.Context) error {
	attempt := 0
	everConnected, everSubscribed := false, false
	for {
		res := s.session(ctx)
		if errors.Is(res.err, errStreamDone) || ctx.Err() != nil {
			return nil
		}
		if isFinal(res, everConnected, everSubscribed) {
			return res.err
		}
		everConnected = everConnected || res.connected
		everSubscribed = everSubscribed || res.subscribed

		if res.healthy {
			attempt = 0
		}
		attempt++
		wait := streamBackoff(attempt)
		if s.Handlers.Reconnect != nil {
			s.Handlers.Reconnect(res.err, wait, attempt, res.connected)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

// isFinal: handshake 401/403/426 and other 4xx; a 429 before any session
// was subscribed (once one was, the server may still count the dropped
// socket); a first connect that got no HTTP response at all (bad URL, DNS,
// TLS, proxy); a 4000-4999 or 1008 close; nothing left subscribed; the
// subscribe refused. Handshake 5xx, network drops, other closes (1000
// included: the server draining) and the idle timeout reconnect.
func isFinal(res sessionResult, everConnected, everSubscribed bool) bool {
	var handshake *StreamHandshakeError
	if errors.As(res.err, &handshake) {
		switch {
		case handshake.StatusCode >= 500:
			return false
		case handshake.StatusCode == http.StatusTooManyRequests:
			return !everSubscribed
		}
		return true
	}
	if !res.connected && !everConnected {
		return true
	}
	var closed *StreamClosedError
	var rejected *StreamRejectedError
	var refused *StreamSubscribeError
	return errors.As(res.err, &closed) || errors.As(res.err, &rejected) || errors.As(res.err, &refused)
}

func isFinalCloseCode(code websocket.StatusCode) bool {
	return code == websocket.StatusPolicyViolation || (code >= 4000 && code <= 4999)
}

func streamBackoff(attempt int) time.Duration {
	wait := streamBackoffMax
	if attempt < 16 {
		wait = min(streamBackoffBase<<(attempt-1), streamBackoffMax)
	}
	half := wait / 2
	return half + rand.N(half+1)
}

func (s *Streamer) session(ctx context.Context) (res sessionResult) {
	conn, err := s.dial(ctx)
	if err != nil {
		var handshake *StreamHandshakeError
		res.connected = errors.As(err, &handshake)
		res.err = err
		return res
	}
	res.connected = true
	defer conn.CloseNow()
	conn.SetReadLimit(streamMessageLimit)

	connectedAt := time.Now()
	defer func() {
		if time.Since(connectedAt) >= streamHealthyAfter {
			res.healthy = true
		}
	}()

	// Closing from here gives the server a clean close handshake while the
	// loop below is blocked in Read.
	stop := context.AfterFunc(ctx, func() {
		_ = conn.Close(websocket.StatusNormalClosure, "")
	})
	defer stop()

	subscribe, err := json.Marshal(map[string]any{"op": "subscribe", "id": streamSubscribeID, "channels": s.Channels})
	if err != nil {
		res.err = err
		return res
	}
	if err := conn.Write(ctx, websocket.MessageText, subscribe); err != nil {
		res.err = err
		return res
	}

	granted := map[string]bool{}
	for {
		msg, err := readStreamMessage(conn)
		if err != nil {
			res.err = err
			if ctx.Err() != nil {
				res.err = ctx.Err()
			} else if code := websocket.CloseStatus(err); isFinalCloseCode(code) {
				var closeErr websocket.CloseError
				errors.As(err, &closeErr)
				res.err = &StreamClosedError{Code: code, Reason: closeErr.Reason}
			}
			return res
		}

		var frame streamFrame
		if err := json.Unmarshal(msg, &frame); err != nil {
			if s.Handlers.ServerErr != nil {
				s.Handlers.ServerErr("unreadable_frame", truncate(string(msg), 200))
			}
			continue
		}

		switch {
		case frame.Channel != "" && frame.Data != nil:
			res.healthy = true
			row := StreamRow{Channel: frame.Channel, Snapshot: frame.Snapshot, Data: frame.Data}
			if !s.Handlers.Row(row) {
				_ = conn.Close(websocket.StatusNormalClosure, "")
				res.err = errStreamDone
				return res
			}
		case frame.Op == "subscribed":
			res.subscribed = true
			for _, ch := range frame.Channels {
				granted[ch] = true
			}
			if s.Handlers.Subscribed != nil {
				s.Handlers.Subscribed(frame.Channels, frame.Rejected)
			}
			if len(granted) == 0 {
				_ = conn.Close(websocket.StatusNormalClosure, "")
				res.err = &StreamRejectedError{Rejected: frame.Rejected}
				return res
			}
		case frame.Op == "unsubscribed":
			for _, ch := range frame.Channels {
				delete(granted, ch)
			}
			for _, r := range frame.Rejected {
				delete(granted, r.Channel)
			}
			if s.Handlers.Unsubscribed != nil {
				s.Handlers.Unsubscribed(frame.Channels, frame.Rejected)
			}
			if len(granted) == 0 {
				_ = conn.Close(websocket.StatusNormalClosure, "")
				res.err = &StreamRejectedError{Rejected: frame.Rejected}
				return res
			}
		case frame.Op == "error" && frame.ID == streamSubscribeID:
			_ = conn.Close(websocket.StatusNormalClosure, "")
			res.err = &StreamSubscribeError{Code: frame.Code, Message: frame.Message}
			return res
		case frame.Op == "error":
			if s.Handlers.ServerErr != nil {
				s.Handlers.ServerErr(frame.Code, frame.Message)
			}
		}
	}
}

func (s *Streamer) dial(ctx context.Context) (*websocket.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, streamDialTimeout)
	defer cancel()

	conn, resp, err := websocket.Dial(dialCtx, s.URL, &websocket.DialOptions{HTTPHeader: s.Header})
	if err == nil {
		return conn, nil
	}
	if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
		return nil, &StreamHandshakeError{StatusCode: resp.StatusCode, Message: handshakeMessage(resp)}
	}
	return nil, err
}

func handshakeMessage(resp *http.Response) string {
	var body []byte
	if resp.Body != nil {
		body, _ = io.ReadAll(resp.Body)
	}
	var errResp APIErrorResponse
	if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
		return errResp.Error
	}
	if text := strings.TrimSpace(string(body)); text != "" {
		return text
	}
	return http.StatusText(resp.StatusCode)
}

func readStreamMessage(conn *websocket.Conn) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), streamIdleTimeout)
	defer cancel()

	_, msg, err := conn.Read(ctx)
	if err != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("no message for %s", streamIdleTimeout)
	}
	return msg, err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
