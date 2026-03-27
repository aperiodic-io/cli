package aperiodic

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func requireAPIKey(t *testing.T) string {
	t.Helper()

	apiKey := os.Getenv("APERIODIC_API_KEY")
	if apiKey == "" {
		t.Skip("APERIODIC_API_KEY not set, skipping integration test")
	}
	return apiKey
}

func TestNewAperiodicClient(t *testing.T) {
	apiKey := "test-key"
	os.Setenv("APERIODIC_API_URL", "https://aperiodic.io")
	defer os.Unsetenv("APERIODIC_API_URL")

	client := NewAperiodicClient(apiKey)

	if client.APIKey != apiKey {
		t.Errorf("expected APIKey %s, got %s", apiKey, client.APIKey)
	}
	if client.BaseURL != "https://aperiodic.io" {
		t.Errorf("expected BaseURL https://aperiodic.io, got %s", client.BaseURL)
	}
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

func TestGetSymbols(t *testing.T) {
	apiKey := requireAPIKey(t)

	client := NewAperiodicClient(apiKey)

	symbols, err := client.GetSymbols("binance")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(symbols) == 0 {
		t.Fatal("expected at least one symbol, got none")
	}
}

func TestGetSymbols_Unauthorized(t *testing.T) {
	requireAPIKey(t)

	client := NewAperiodicClient("wrong-key")

	_, err := client.GetSymbols("binance")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	apiErr, ok := err.(*APIError)
	if !ok || apiErr.StatusCode != 401 {
		t.Errorf("expected 401 APIError, got %v", err)
	}
}

func TestFetchPresignedUrls(t *testing.T) {
	apiKey := requireAPIKey(t)

	client := NewAperiodicClient(apiKey)

	resp, err := client.FetchPresignedUrls("ohlcv", TimestampExchange, Interval1d, "binance", "perpetual-BTC-USDT:USDT", "2024-01-01", "2024-01-31")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Files) == 0 {
		t.Fatal("expected at least one file, got none")
	}

	for _, f := range resp.Files {
		if f.URL == "" {
			t.Error("expected non-empty URL in file info")
		}
		if f.Year == 0 {
			t.Error("expected non-zero year in file info")
		}
	}
}

func TestDownloadToFile(t *testing.T) {
	apiKey := requireAPIKey(t)

	client := NewAperiodicClient(apiKey)

	resp, err := client.FetchPresignedUrls("ohlcv", TimestampExchange, Interval1d, "binance", "perpetual-BTC-USDT:USDT", "2024-01-01", "2024-01-31")
	if err != nil {
		t.Fatalf("failed to fetch presigned urls: %v", err)
	}
	if len(resp.Files) == 0 {
		t.Fatal("no files returned to download")
	}

	tmpFile := filepath.Join(t.TempDir(), "test.parquet")
	err = client.downloadToFile(resp.Files[0].URL, tmpFile, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty file content")
	}
}
