package aperiodic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_VWAP_Download(t *testing.T) {
	requireAPIKey(t)

	outputDir := t.TempDir()

	stdout, stderr, code := runCLI(
		"vwap",
		"-exchange", "binance",
		"-symbol", "perpetual-BTC-USDT:USDT",
		"-interval", "1d",
		"-start-date", "2024-01-01",
		"-end-date", "2024-01-31",
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
