package aperiodic

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type streamFlags struct {
	exchange string
	interval string
	symbols  string
	count    int
	duration time.Duration
}

func newStreamFlagSet() (*flag.FlagSet, *streamFlags) {
	f := &streamFlags{}
	fs := flag.NewFlagSet("aperiodic stream", flag.ContinueOnError)
	fs.StringVar(&f.exchange, "exchange", string(ExchangeBinanceFutures), "Exchange name")
	fs.StringVar(&f.interval, "interval", string(Interval1m), "Aggregation interval")
	fs.StringVar(&f.symbols, "symbols", "", "Comma-separated symbols, e.g. perpetual-BTC-USDT:USDT (default: every symbol your plan allows)")
	fs.IntVar(&f.count, "count", 0, "Stop after this many live rows; snapshot rows are printed but not counted (0: no limit)")
	fs.DurationVar(&f.duration, "duration", 0, "Stop after this long, e.g. 90s or 1h (0: no limit)")
	return fs, f
}

func (c *CLI) runStream(args []string) int {
	if len(args) > 0 && isHelpArg(args[0]) {
		c.printStreamUsage()
		return 0
	}

	fs, f := newStreamFlagSet()
	positional, exitCode, ok := c.parseArgs(fs, args, c.printStreamUsage, "aperiodic stream help")
	if !ok {
		return exitCode
	}

	switch {
	case len(positional) == 0:
		fmt.Fprintln(c.Stderr, "Error: a dataset is required, e.g. 'aperiodic stream ohlcv'")
		return 1
	case len(positional) > 1:
		fmt.Fprintf(c.Stderr, "Error: unexpected arguments: %s\n", strings.Join(positional[1:], " "))
		return 1
	case f.count < 0:
		fmt.Fprintln(c.Stderr, "Error: --count must not be negative")
		return 1
	case f.duration < 0:
		fmt.Fprintln(c.Stderr, "Error: --duration must not be negative")
		return 1
	}

	apiKey := c.Env("APERIODIC_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(c.Stderr, "Error: APERIODIC_API_KEY environment variable not set")
		return 1
	}

	channel := StreamChannel{Dataset: positional[0], Exchange: f.exchange, Interval: f.interval}
	for _, symbol := range strings.Split(f.symbols, ",") {
		if symbol = strings.TrimSpace(symbol); symbol != "" {
			channel.Symbols = append(channel.Symbols, symbol)
		}
	}

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	if f.duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.duration)
		defer cancel()
	}

	live := 0
	streamer := NewStreamer(apiKey, c.Env, []StreamChannel{channel}, StreamHandlers{
		Row: func(row StreamRow) bool {
			line, err := json.Marshal(row)
			if err != nil {
				fmt.Fprintf(c.Stderr, "Warning: skipping a row: %v\n", err)
				return true
			}
			fmt.Fprintf(c.Stdout, "%s\n", line)
			if !row.Snapshot {
				live++
			}
			return f.count == 0 || live < f.count
		},
		Subscribed: func(granted []string, rejected []StreamRejection) {
			if len(granted) > 0 {
				fmt.Fprintf(c.Stderr, "Subscribed: %s\n", strings.Join(granted, ", "))
			}
			for _, r := range rejected {
				fmt.Fprintf(c.Stderr, "Rejected: %s (%s): %s\n", r.Channel, r.Code, r.Message)
			}
		},
		ServerErr: func(code, message string) {
			fmt.Fprintf(c.Stderr, "Warning: server error %s: %s\n", code, message)
		},
		Reconnect: func(cause error, wait time.Duration, attempt int) {
			fmt.Fprintf(c.Stderr, "Connection lost (%v); reconnecting in %s (attempt %d)\n", cause, wait.Round(time.Millisecond), attempt)
		},
	})

	err := streamer.Run(ctx)
	if err == nil {
		if errors.Is(context.Cause(ctx), context.Canceled) {
			// Interrupted (Ctrl-C or SIGTERM), not stopped by --count or --duration.
			return 130
		}
		return 0
	}

	var handshake *StreamHandshakeError
	var rejected *StreamRejectedError
	switch {
	case errors.As(err, &handshake):
		fmt.Fprintf(c.Stderr, "Error connecting to the stream: %v\n", handshake)
	case errors.As(err, &rejected):
		fmt.Fprintln(c.Stderr, "Error: every channel was rejected")
	default:
		fmt.Fprintf(c.Stderr, "Error: %v\n", err)
	}
	return 1
}

func (c *CLI) printStreamUsage() {
	out := c.Stdout
	fmt.Fprintln(out, "Aperiodic CLI Client: live stream")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  aperiodic stream <dataset> [flags]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Prints one JSON line per row to stdout:")
	fmt.Fprintln(out, `  {"channel":"ohlcv.binance-futures.1m","snapshot":false,"data":{...}}`)
	fmt.Fprintln(out, "Snapshot rows (the latest row per symbol, sent on subscribe on some plans)")
	fmt.Fprintln(out, "have \"snapshot\":true. Subscriptions, rejections, reconnects and warnings go")
	fmt.Fprintln(out, "to stderr. Delivery is at most once: rows missed while reconnecting are not")
	fmt.Fprintln(out, "replayed. Exits non-zero if the key is refused, the plan has no live access,")
	fmt.Fprintln(out, "the connection limit is reached, or every channel is rejected.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Environment:")
	fmt.Fprintln(out, "  APERIODIC_API_KEY     API key on a plan with live data (required)")
	fmt.Fprintln(out, "  APERIODIC_STREAM_URL  Stream endpoint (default "+DefaultStreamURL+")")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Flags:")
	fs, _ := newStreamFlagSet()
	fs.SetOutput(out)
	fs.PrintDefaults()
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Example:")
	fmt.Fprintln(out, "  aperiodic stream ohlcv --exchange binance-futures --interval 1m \\")
	fmt.Fprintln(out, "    --symbols perpetual-BTC-USDT:USDT,perpetual-ETH-USDT:USDT")
}
