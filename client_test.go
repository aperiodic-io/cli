package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") != "test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"symbols": ["perpetual-BTC-USDT:USDT", "ETH-USDT"], "exchange": "binance", "bucket": "symbols"}`))
	}))
	defer server.Close()

	client := NewAperiodicClient("test-key")
	client.BaseURL = server.URL

	symbols, err := client.GetSymbols("binance")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(symbols) != 2 || symbols[0] != "perpetual-BTC-USDT:USDT" {
		t.Errorf("unexpected symbols: %v", symbols)
	}
}

func TestFetchPresignedUrls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/data/ohlcv" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("exchange") != "binance" {
			t.Errorf("unexpected exchange: %s", q.Get("exchange"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"files": [{"year": 2024, "month": 1, "url": "http://example.com/f1"}]}`))
	}))
	defer server.Close()

	client := NewAperiodicClient("test-key")
	client.BaseURL = server.URL

	resp, err := client.FetchPresignedUrls("ohlcv", TimestampExchange, Interval1d, "binance", "perpetual-BTC-USDT:USDT", "2024-01-01", "2024-01-31")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Files) != 1 || resp.Files[0].Year != 2024 {
		t.Errorf("unexpected response: %v", resp)
	}
}

func TestDownloadWithRetry(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data"))
	}))
	defer server.Close()

	client := NewAperiodicClient("test-key")
	data, err := client.downloadWithRetry(server.URL, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(data) != "data" {
		t.Errorf("expected data, got %s", string(data))
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}
