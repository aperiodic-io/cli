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

	// StatusPlanEnded is the close code for a lapsed plan or a rotated key.
	StatusPlanEnded websocket.StatusCode = 4001

	// The server sends a heartbeat every ~30s when idle; a silent socket
	// for longer than this is treated as dropped.
	streamIdleTimeout  = 75 * time.Second
	streamDialTimeout  = 15 * time.Second
	streamMessageLimit = 1 << 20
)

var (
	streamBackoffBase = 500 * time.Millisecond
	streamBackoffMax  = 30 * time.Second
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

// StreamClosedError is a close the client must not reconnect after.
type StreamClosedError struct {
	Code   websocket.StatusCode
	Reason string
}

func (e *StreamClosedError) Error() string {
	return fmt.Sprintf("stream closed by the server (%d): %s", int(e.Code), e.Reason)
}

// StreamRejectedError means the server granted none of the channels.
type StreamRejectedError struct {
	Rejected []StreamRejection
}

func (e *StreamRejectedError) Error() string {
	return "every channel was rejected"
}

// errStreamDone ends the stream because the caller has what it wanted.
var errStreamDone = errors.New("stream done")

type StreamHandlers struct {
	// Row receives each data row; returning false closes the stream.
	Row        func(StreamRow) bool
	Subscribed func(granted []string, rejected []StreamRejection)
	ServerErr  func(code, message string)
	Reconnect  func(cause error, wait time.Duration, attempt int)
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

// Run streams until ctx ends (nil), Row asks to stop (nil), or a terminal
// error: a handshake refusal, every channel rejected, or a 4001/1008 close.
// Anything else reconnects with capped, jittered exponential backoff.
func (s *Streamer) Run(ctx context.Context) error {
	attempt := 0
	for {
		subscribed, err := s.session(ctx)
		switch {
		case errors.Is(err, errStreamDone), ctx.Err() != nil:
			return nil
		case isTerminalStreamError(err):
			return err
		}

		if subscribed {
			attempt = 0
		}
		attempt++
		wait := streamBackoff(attempt)
		if s.Handlers.Reconnect != nil {
			s.Handlers.Reconnect(err, wait, attempt)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

func isTerminalStreamError(err error) bool {
	var handshake *StreamHandshakeError
	var closed *StreamClosedError
	var rejected *StreamRejectedError
	if errors.As(err, &handshake) {
		// A 5xx is the service restarting; any 4xx won't change on retry.
		return handshake.StatusCode < 500
	}
	return errors.As(err, &closed) || errors.As(err, &rejected)
}

func streamBackoff(attempt int) time.Duration {
	wait := streamBackoffMax
	if attempt < 16 {
		wait = min(streamBackoffBase<<(attempt-1), streamBackoffMax)
	}
	half := wait / 2
	return half + rand.N(half+1)
}

// session runs one connection and reports whether it got a subscription ack.
func (s *Streamer) session(ctx context.Context) (subscribed bool, err error) {
	conn, err := s.dial(ctx)
	if err != nil {
		return false, err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(streamMessageLimit)

	// Closing from here gives the server a clean close handshake while the
	// loop below is blocked in Read.
	stop := context.AfterFunc(ctx, func() {
		_ = conn.Close(websocket.StatusNormalClosure, "")
	})
	defer stop()

	subscribe, err := json.Marshal(map[string]any{"op": "subscribe", "id": "s1", "channels": s.Channels})
	if err != nil {
		return false, err
	}
	if err := conn.Write(ctx, websocket.MessageText, subscribe); err != nil {
		return false, err
	}

	for {
		msg, err := readStreamMessage(conn)
		if err != nil {
			if ctx.Err() != nil {
				return subscribed, ctx.Err()
			}
			switch code := websocket.CloseStatus(err); code {
			case StatusPlanEnded, websocket.StatusPolicyViolation:
				var closeErr websocket.CloseError
				errors.As(err, &closeErr)
				return subscribed, &StreamClosedError{Code: code, Reason: closeErr.Reason}
			}
			return subscribed, err
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
			row := StreamRow{Channel: frame.Channel, Snapshot: frame.Snapshot, Data: frame.Data}
			if !s.Handlers.Row(row) {
				_ = conn.Close(websocket.StatusNormalClosure, "")
				return subscribed, errStreamDone
			}
		case frame.Op == "subscribed":
			subscribed = true
			if s.Handlers.Subscribed != nil {
				s.Handlers.Subscribed(frame.Channels, frame.Rejected)
			}
			if len(frame.Channels) == 0 {
				_ = conn.Close(websocket.StatusNormalClosure, "")
				return subscribed, &StreamRejectedError{Rejected: frame.Rejected}
			}
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
