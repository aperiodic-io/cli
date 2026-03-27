package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadFilesConcurrently(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("parquet-content"))
	}))
	defer server.Close()

	client := NewAperiodicClient("test-key")
	client.BaseURL = server.URL

	outputDir, err := os.MkdirTemp("", "aperiodic-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(outputDir)

	files := []FileInfo{
		{Year: 2024, Month: 1, URL: server.URL},
		{Year: 2024, Month: 2, URL: server.URL},
	}

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
		if string(content) != "parquet-content" {
			t.Errorf("unexpected content in %s: %s", res.Filename, string(content))
		}
		expectedFilename := fmt.Sprintf("%d-%02d.parquet", res.Year, res.Month)
		if res.Filename != expectedFilename {
			t.Errorf("expected filename %s, got %s", expectedFilename, res.Filename)
		}
	}
}
