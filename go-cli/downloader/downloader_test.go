package downloader

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownload(t *testing.T) {
	t.Parallel()

	filePayload := []byte("fake parquet bytes")

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	mux.HandleFunc("/file.parquet", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(filePayload)
	})

	mux.HandleFunc("/data/ohlcv", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") != "test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("symbol"); got != "perpetual-BTC-USDT:USDT" {
			t.Fatalf("unexpected symbol %q", got)
		}
		_ = json.NewEncoder(w).Encode(aggregateDataResponse{Files: []fileInfo{{Year: 2024, Month: 1, URL: server.URL + "/file.parquet"}}})
	})

	out := t.TempDir()
	err := Download(context.Background(), server.Client(), Config{
		APIKey:        "test-key",
		Bucket:        "ohlcv",
		Timestamp:     "true",
		Interval:      "1h",
		Exchange:      "binance-futures",
		Symbol:        "perpetual-BTC-USDT:USDT",
		StartDate:     "2024-01-01",
		EndDate:       "2024-01-31",
		BaseURL:       server.URL,
		OutputDir:     out,
		MaxConcurrent: 2,
		MaxRetries:    1,
	})
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file, got %d", len(entries))
	}

	p := filepath.Join(out, entries[0].Name())
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != string(filePayload) {
		t.Fatalf("unexpected file content: %q", string(data))
	}
	if !strings.HasSuffix(entries[0].Name(), "2024-01.parquet") {
		t.Fatalf("unexpected file name: %q", entries[0].Name())
	}
}
