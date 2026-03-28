package aperiodic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_VWAP_InvalidAPIKey(t *testing.T) {
	t.Setenv("APERIODIC_API_URL", DefaultBaseURL)
	t.Setenv("APERIODIC_API_KEY", "invalid-key")

	outputDir := t.TempDir()
	_, stderr, code := runCLI(
		"vwap",
		"-exchange", "binance-futures",
		"-symbol", "perpetual-ETH-USDT:USDT",
		"-interval", "1d",
		"-start-date", "2024-01-01",
		"-end-date", "2024-02-01",
		"-output-dir", outputDir,
	)
	if code != 1 {
		t.Fatalf("expected exit code 1 for invalid API key, got %d; stderr: %s", code, stderr)
	}
}

func TestCLI_VWAP_Download(t *testing.T) {
	requireAPIKey(t)

	outputDir := t.TempDir()

	stdout, stderr, code := runCLI(
		"vwap",
		"-exchange", "binance-futures",
		"-symbol", "perpetual-ETH-USDT:USDT",
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
