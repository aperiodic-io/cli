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
	streamIdleTimeout = 75 * time.Second
	streamDialTimeout = 15 * time.Second
	// A replay frame carries one whole bar: on an all-symbols channel that is
	// over a megabyte. The server caps one subscribe's replay at a few MB.
	streamMessageLimit = 16 << 20

	// How often a subscribe refused because the server is busy replaying is
	// sent again with its since, and how many truncated replays are followed
	// up, before giving up on the replay.
	streamBusyRetries     = 5
	streamMaxContinuation = 50
)

var (
	streamBackoffBase  = 500 * time.Millisecond
	streamBackoffMax   = 30 * time.Second
	streamHealthyAfter = 60 * time.Second
	// A busy refusal is retried after this, plus as much again at random.
	streamBusyRetryBase = time.Second
)

// streamReplaySteps are the intervals the server keeps bars for, with their
// length in ms. A since on any other interval is refused.
var streamReplaySteps = map[string]int64{
	"1m": 60_000, "5m": 300_000, "15m": 900_000, "30m": 1_800_000,
	"1h": 3_600_000, "4h": 14_400_000, "1d": 86_400_000,
}

// StreamChannel is one subscription; Symbols nil means every symbol the
// plan allows.
type StreamChannel struct {
	Dataset  string   `json:"dataset"`
	Exchange string   `json:"exchange"`
	Interval string   `json:"interval"`
	Symbols  []string `json:"symbols,omitempty"`
	// Since asks for the channel's bars from this time on (epoch ms) before
	// its live rows; 0 asks for none.
	Since int64 `json:"since,omitempty"`
}

func (c StreamChannel) name() string {
	return c.Dataset + "." + c.Exchange + "." + c.Interval
}

type StreamRejection struct {
	Channel string `json:"channel"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// StreamRow is a data frame, printed as one JSON line. Replay marks a row
// from the history sent before the live rows.
type StreamRow struct {
	Channel  string          `json:"channel"`
	Snapshot bool            `json:"snapshot"`
	Replay   bool            `json:"replay,omitempty"`
	Data     json.RawMessage `json:"data"`
}

// StreamReplay is the server's account of one channel's replay. Times are
// bar open times in epoch ms; Unavailable means the history could not be read.
type StreamReplay struct {
	Channel     string
	From        int64
	To          *int64
	Bars        int
	Rows        int
	Missing     []int64
	Truncated   bool
	Unavailable bool
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
	Replay   bool            `json:"replay"`
	Data     json.RawMessage `json:"data"`

	From        int64   `json:"from"`
	To          *int64  `json:"to"`
	Bars        int     `json:"bars"`
	Rows        int     `json:"rows"`
	Missing     []int64 `json:"missing"`
	Truncated   bool    `json:"truncated"`
	Unavailable bool    `json:"unavailable"`
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
	Replayed     func(StreamReplay)
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
	// Since asks for every channel's bars from this time on before its live
	// rows, on the first connect (zero: none).
	Since time.Time
	// Replay asks, on every reconnect, for the bars of each channel since the
	// newest one received, so a reconnect leaves no gap the server still holds.
	Replay bool

	// lastBar is each channel's newest bar time received (epoch ms), and
	// subscribedAt when the last session subscribed: where a reconnect's
	// replay starts.
	lastBar      map[string]int64
	subscribedAt time.Time
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

	return &Streamer{URL: url, Header: header, Channels: channels, Handlers: handlers, Replay: true}
}

// subscribeChannels is the channels to subscribe with, each with the since it
// should replay from, if any: Since on the first session, and on a reconnect
// the newest bar received (or, when none came, one bar before the last
// subscribe). Intervals the server keeps no history for get none.
func (s *Streamer) subscribeChannels(resume bool) []StreamChannel {
	channels := make([]StreamChannel, len(s.Channels))
	for i, ch := range s.Channels {
		channels[i] = ch
		step, ok := streamReplaySteps[ch.Interval]
		if !ok || ch.Since != 0 && !resume {
			continue
		}
		switch {
		case !resume && !s.Since.IsZero():
			channels[i].Since = s.Since.UnixMilli()
		case resume && s.Replay && s.lastBar[ch.name()] > 0:
			channels[i].Since = s.lastBar[ch.name()]
		case resume && s.Replay && !s.subscribedAt.IsZero():
			channels[i].Since = s.subscribedAt.UnixMilli() - step
		default:
			channels[i].Since = 0
		}
	}
	return channels
}

// noteBar records a row's bar time, so a reconnect replays from it.
func (s *Streamer) noteBar(channel string, data json.RawMessage) {
	var row struct {
		Time json.Number `json:"time"`
	}
	if json.Unmarshal(data, &row) != nil {
		return
	}
	value, err := row.Time.Float64()
	if err != nil || value <= 0 {
		return
	}
	ms := int64(value)
	switch {
	case value >= 1e17:
		ms = int64(value / 1e6)
	case value >= 1e14:
		ms = int64(value / 1e3)
	case value < 1e11:
		ms = int64(value * 1e3)
	}
	if s.lastBar == nil {
		s.lastBar = map[string]int64{}
	}
	if ms > s.lastBar[channel] {
		s.lastBar[channel] = ms
	}
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
		res := s.session(ctx, everSubscribed)
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

func (s *Streamer) session(ctx context.Context, resume bool) (res sessionResult) {
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

	channels := s.subscribeChannels(resume)
	write := func(id string, channels []StreamChannel) error {
		frame, err := json.Marshal(map[string]any{"op": "subscribe", "id": id, "channels": channels})
		if err != nil {
			return err
		}
		return conn.Write(ctx, websocket.MessageText, frame)
	}
	if err := write(streamSubscribeID, channels); err != nil {
		res.err = err
		return res
	}
	s.subscribedAt = time.Now()

	granted := map[string]bool{}
	busyRetries, continuations := 0, 0
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
		case frame.Replay && frame.Channel != "":
			var rows []json.RawMessage
			if err := json.Unmarshal(frame.Data, &rows); err != nil {
				if s.Handlers.ServerErr != nil {
					s.Handlers.ServerErr("unreadable_frame", truncate(string(msg), 200))
				}
				continue
			}
			res.healthy = true
			for _, data := range rows {
				s.noteBar(frame.Channel, data)
				if !s.Handlers.Row(StreamRow{Channel: frame.Channel, Replay: true, Data: data}) {
					_ = conn.Close(websocket.StatusNormalClosure, "")
					res.err = errStreamDone
					return res
				}
			}
		case frame.Channel != "" && frame.Data != nil:
			res.healthy = true
			if !frame.Snapshot {
				s.noteBar(frame.Channel, frame.Data)
			}
			row := StreamRow{Channel: frame.Channel, Snapshot: frame.Snapshot, Data: frame.Data}
			if !s.Handlers.Row(row) {
				_ = conn.Close(websocket.StatusNormalClosure, "")
				res.err = errStreamDone
				return res
			}
		case frame.Op == "replayed":
			if s.Handlers.Replayed != nil {
				s.Handlers.Replayed(StreamReplay{
					Channel: frame.Channel, From: frame.From, To: frame.To, Bars: frame.Bars, Rows: frame.Rows,
					Missing: frame.Missing, Truncated: frame.Truncated, Unavailable: frame.Unavailable,
				})
			}
			// The server stopped at its size cap: ask for the rest, from the
			// bar after the last one sent.
			step, replayable := streamReplaySteps[channelInterval(frame.Channel)]
			if frame.Truncated && frame.To != nil && replayable && continuations < streamMaxContinuation {
				if ch, ok := s.channelNamed(frame.Channel); ok {
					continuations++
					ch.Since = *frame.To + step
					if err := write(fmt.Sprintf("r%d", continuations), []StreamChannel{ch}); err != nil {
						res.err = err
						return res
					}
				}
			}
		case frame.Op == "error" && frame.ID == streamSubscribeID && frame.Code == "limit_exceeded" && hasSince(channels):
			// Too many replays at once on the server (as after it restarts):
			// try again after a jittered pause, then without the replay.
			busyRetries++
			if busyRetries > streamBusyRetries {
				channels = withoutSince(channels)
				if s.Handlers.ServerErr != nil {
					s.Handlers.ServerErr(frame.Code, "the server stayed too busy to replay; subscribing without it")
				}
			} else {
				select {
				case <-ctx.Done():
					res.err = ctx.Err()
					return res
				case <-time.After(streamBusyRetryBase + rand.N(streamBusyRetryBase+1)):
				}
			}
			if err := write(streamSubscribeID, channels); err != nil {
				res.err = err
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

func (s *Streamer) channelNamed(name string) (StreamChannel, bool) {
	for _, ch := range s.Channels {
		if ch.name() == name {
			return ch, true
		}
	}
	return StreamChannel{}, false
}

func channelInterval(name string) string {
	parts := strings.Split(name, ".")
	return parts[len(parts)-1]
}

func hasSince(channels []StreamChannel) bool {
	for _, ch := range channels {
		if ch.Since != 0 {
			return true
		}
	}
	return false
}

func withoutSince(channels []StreamChannel) []StreamChannel {
	out := make([]StreamChannel, len(channels))
	for i, ch := range channels {
		ch.Since = 0
		out[i] = ch
	}
	return out
}
