package aperiodic

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func requireAPIKey(t *testing.T) string {
	t.Helper()

	apiKey := os.Getenv("APERIODIC_API_KEY")
	if apiKey == "" {
		t.Fatal("APERIODIC_API_KEY environment variable not set")
	}

	// Force production base URL for all integration tests
	t.Setenv("APERIODIC_API_URL", DefaultBaseURL)

	return apiKey
}

func TestHandleAPIError(t *testing.T) {
	client := NewAperiodicClient("test-key")

	tests := []struct {
		statusCode int
		body       string
		expected   string
	}{
		{http.StatusUnauthorized, "", "Unauthorized"},
		{http.StatusForbidden, "", "Forbidden"},
		{http.StatusNotFound, "", "Not Found"},
		{http.StatusTooManyRequests, "", "Too Many Requests"},
		{http.StatusBadRequest, `{"error": "bad request", "details": ["detail"]}`, "bad request"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("Status%d", tt.statusCode), func(t *testing.T) {
			var body io.ReadCloser
			if tt.body != "" {
				body = io.NopCloser(bytes.NewReader([]byte(tt.body)))
			} else {
				body = io.NopCloser(bytes.NewReader([]byte{}))
			}
			resp := &http.Response{
				StatusCode: tt.statusCode,
				Body:       body,
			}

			err := client.handleAPIError(resp)
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			apiErr, ok := err.(*APIError)
			if !ok {
				t.Fatalf("expected *APIError, got %T", err)
			}

			if apiErr.Message != tt.expected {
				t.Errorf("expected message %s, got %s", tt.expected, apiErr.Message)
			}
		})
	}
}

func runCLI(args ...string) (stdout, stderr string, exitCode int) {
	var outBuf, errBuf bytes.Buffer
	cli := &CLI{
		Stdout: &outBuf,
		Stderr: &errBuf,
		Env:    os.Getenv,
	}
	code := cli.Run(args)
	return outBuf.String(), errBuf.String(), code
}

func TestCLI_Help(t *testing.T) {
	stdout, _, code := runCLI("help")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !strings.Contains(stdout, "Aperiodic CLI Client") {
		t.Error("expected help output to contain 'Aperiodic CLI Client'")
	}
	if !strings.Contains(stdout, "symbols") {
		t.Error("expected help output to list 'symbols' command")
	}
}

func TestCLI_NoArgs(t *testing.T) {
	stdout, _, code := runCLI()
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Error("expected usage output")
	}
}

func TestCLI_MissingAPIKey(t *testing.T) {
	cli := &CLI{
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		Env:    func(string) string { return "" },
	}
	code := cli.Run([]string{"symbols"})
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
}

func TestCLI_UnknownCommand(t *testing.T) {
	_, stderr, code := runCLI("bogus", "-api-key", "fake")
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr, "Unknown command") {
		t.Errorf("expected 'Unknown command' in stderr, got: %s", stderr)
	}
}

// Tests that always run (no API key needed) — match Python client pattern
// where invalid-key tests run unconditionally and assert specific error codes.

func TestCLI_Symbols_InvalidAPIKey(t *testing.T) {
	t.Setenv("APERIODIC_API_URL", DefaultBaseURL)

	cli := &CLI{
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		Env:    func(string) string { return "" },
	}
	code := cli.Run([]string{"symbols", "-api-key", "invalid-key", "-exchange", "binance-futures"})
	if code != 1 {
		t.Fatalf("expected exit code 1 for invalid API key, got %d", code)
	}
}

func TestCLI_OHLCV_InvalidAPIKey(t *testing.T) {
	t.Setenv("APERIODIC_API_URL", DefaultBaseURL)

	outputDir := t.TempDir()
	cli := &CLI{
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		Env:    func(string) string { return "" },
	}
	code := cli.Run([]string{
		"ohlcv",
		"-api-key", "invalid-key",
		"-exchange", "binance-futures",
		"-symbol", "perpetual-BTC-USDT:USDT",
		"-interval", "1d",
		"-start-date", "2024-01-01",
		"-end-date", "2024-02-01",
		"-output-dir", outputDir,
	})
	if code != 1 {
		t.Fatalf("expected exit code 1 for invalid API key, got %d", code)
	}
}

func TestCLI_OHLCV_MissingFlags(t *testing.T) {
	// Missing --output-dir
	_, stderr, code := runCLI("ohlcv", "-api-key", "fake", "-symbol", "perpetual-BTC-USDT:USDT", "-start-date", "2024-01-01", "-end-date", "2024-01-31")
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr, "output-dir") {
		t.Errorf("expected error about output-dir, got: %s", stderr)
	}

	// Missing --symbol
	outputDir := t.TempDir()
	_, stderr, code = runCLI("ohlcv", "-api-key", "fake", "-start-date", "2024-01-01", "-end-date", "2024-01-31", "-output-dir", outputDir)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr, "symbol") {
		t.Errorf("expected error about symbol, got: %s", stderr)
	}
}

// Tests that require a valid API key — skip when APERIODIC_API_KEY is not set.

func TestCLI_Symbols(t *testing.T) {
	requireAPIKey(t)

	stdout, stderr, code := runCLI("symbols", "-exchange", "binance-futures")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) == 0 {
		t.Fatal("expected at least one symbol in output")
	}

	// Verify known symbol is present
	found := false
	for _, line := range lines {
		if line == "perpetual-BTC-USDT:USDT" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected perpetual-BTC-USDT:USDT in symbols output")
	}
}

func TestCLI_OHLCV_Download(t *testing.T) {
	requireAPIKey(t)

	outputDir := t.TempDir()

	stdout, stderr, code := runCLI(
		"ohlcv",
		"-exchange", "binance-futures",
		"-symbol", "perpetual-BTC-USDT:USDT",
		"-interval", "1d",
		"-start-date", "2024-01-01",
		"-end-date", "2024-02-01",
		"-output-dir", outputDir,
	)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}

	if !strings.Contains(stdout, "Successfully downloaded") {
		t.Errorf("expected success message, got: %s", stdout)
	}

	files, err := filepath.Glob(filepath.Join(outputDir, "*.parquet"))
	if err != nil {
		t.Fatalf("failed to glob output dir: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one parquet file in output dir")
	}

	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			t.Errorf("failed to stat %s: %v", f, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("expected non-empty file %s", f)
		}
	}
}
