package aperiodic

// The live stream (production, or APERIODIC_STREAM_URL): what a user running
// the CLI gets. The granted-channel test needs APERIODIC_API_KEY to be on a
// plan with live ohlcv.binance-futures.1m.

import (
	"cmp"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// useLiveStream points a test at the live stream: production, unless
// APERIODIC_STREAM_URL names another (CI sets staging before a release).
func useLiveStream(t *testing.T) {
	t.Helper()
	t.Setenv("APERIODIC_STREAM_URL", cmp.Or(os.Getenv("APERIODIC_STREAM_URL"), DefaultStreamURL))
}

func TestCLI_Stream_Live_InvalidKeyIsRefused(t *testing.T) {
	useLiveStream(t)
	t.Setenv("APERIODIC_API_KEY", "invalid-key")

	stdout, stderr, code := runCLI("stream", "ohlcv", "--duration", "30s")
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "401") {
		t.Errorf("expected a 401, got: %s", stderr)
	}
	if stdout != "" {
		t.Errorf("expected nothing on stdout, got: %s", stdout)
	}
}

func TestCLI_Stream_Live_UnknownDatasetIsRejected(t *testing.T) {
	requireAPIKey(t)
	useLiveStream(t)

	_, stderr, code := runCLI("stream", "no_such_dataset", "--exchange", "binance-futures", "--interval", "1m", "--duration", "30s")
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "Rejected: ") || !strings.Contains(stderr, "no channel is subscribed") {
		t.Errorf("expected the channel rejected, got: %s", stderr)
	}
}

// A 1m bar closes every minute; waiting 150s spans two minute boundaries.
// These tests gate every unravel-router data release against staging, so a
// single missed minute on staging's live pipeline must not block a release.
func TestCLI_Stream_Live_OHLCVRowArrives(t *testing.T) {
	requireAPIKey(t)
	useLiveStream(t)

	stdout, stderr, code := runCLI(
		"stream", "ohlcv",
		"--exchange", "binance-futures",
		"--interval", "1m",
		"--symbols", rawTestSymbol,
		"--count", "1",
		"--duration", "150s",
	)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "Subscribed: ohlcv.binance-futures.1m") {
		t.Fatalf("expected ohlcv.binance-futures.1m granted, got: %s", stderr)
	}
	if strings.Contains(stderr, "Rejected: ") {
		t.Errorf("expected no rejection, got: %s", stderr)
	}

	for _, line := range parseStreamLines(t, stdout) {
		var data struct {
			Symbol string `json:"symbol"`
		}
		if err := json.Unmarshal(line.Data, &data); err != nil {
			t.Fatalf("row data is not an object: %s", line.Data)
		}
		if !line.Snapshot && line.Channel == "ohlcv.binance-futures.1m" && data.Symbol == rawTestSymbol {
			return
		}
	}
	t.Fatalf("expected a live %s row within 150s, got:\n%s", rawTestSymbol, stdout)
}
