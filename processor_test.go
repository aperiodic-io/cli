package aperiodic

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadFilesConcurrently(t *testing.T) {
	apiKey := requireAPIKey(t)

	client := NewAperiodicClient(apiKey)

	resp, err := client.FetchPresignedUrls("ohlcv", TimestampExchange, Interval1d, "binance", "perpetual-BTC-USDT:USDT", "2024-01-01", "2024-02-28")
	if err != nil {
		t.Fatalf("failed to fetch presigned urls: %v", err)
	}
	if len(resp.Files) < 2 {
		t.Fatalf("expected at least 2 files, got %d", len(resp.Files))
	}

	// Use first 2 files for the test
	files := resp.Files[:2]
	outputDir := t.TempDir()

	results, err := client.DownloadFilesConcurrently(files, 2, outputDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	for _, res := range results {
		path := filepath.Join(outputDir, res.Filename)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("failed to read downloaded file %s: %v", res.Filename, err)
			continue
		}
		if len(content) == 0 {
			t.Errorf("expected non-empty content in %s", res.Filename)
		}
		expectedFilename := fmt.Sprintf("%d-%02d.parquet", res.Year, res.Month)
		if res.Filename != expectedFilename {
			t.Errorf("expected filename %s, got %s", expectedFilename, res.Filename)
		}
	}
}
