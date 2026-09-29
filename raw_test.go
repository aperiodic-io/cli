package aperiodic

// Offline tests for raw data: the API and the presigned file URLs are served
// by an httptest stub, so the real code runs end to end: chunking long ranges,
// deduplicating monthly files, re-requesting a URL after a 403, streaming to
// the Hive layout and skipping files that are already there.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const rawTestSymbol = "perpetual-BTC-USDT:USDT"

func rawBody(period string) []byte {
	return []byte("parquet bytes for " + period)
}

type rawAPICall struct {
	path  string
	query url.Values
	key   string
	// hasKey distinguishes an absent X-API-KEY from an empty one.
	hasKey bool
}

// rawStub serves the raw listing, the file blobs and the coverage endpoint.
// listing is called once per listing request, numbered from 0.
type rawStub struct {
	*httptest.Server

	listing   func(call int, baseURL string) (status int, body any)
	coverage  any
	forbidden map[string]bool // ?v= versions answered with 403, like an expired URL
	failFirst map[string]int  // period → number of 500s to answer before serving it

	mu        sync.Mutex
	apiCalls  []rawAPICall
	blobCalls map[string]int
}

func newRawStub(t *testing.T, listing func(call int, baseURL string) (int, any)) *rawStub {
	t.Helper()

	stub := &rawStub{
		listing:   listing,
		forbidden: map[string]bool{},
		failFirst: map[string]int{},
		blobCalls: map[string]int{},
	}
	stub.Server = httptest.NewServer(http.HandlerFunc(stub.serve))
	t.Cleanup(stub.Close)

	t.Setenv("APERIODIC_API_URL", stub.URL)
	t.Setenv("APERIODIC_API_KEY", "key-1")

	previous := rawRetryBackoff
	rawRetryBackoff = time.Millisecond
	t.Cleanup(func() { rawRetryBackoff = previous })

	return stub
}

func (s *rawStub) serve(w http.ResponseWriter, r *http.Request) {
	if period, isBlob := strings.CutPrefix(r.URL.Path, "/blob/"); isBlob {
		s.mu.Lock()
		s.blobCalls[period]++
		fail := s.failFirst[period] > 0
		if fail {
			s.failFirst[period]--
		}
		forbidden := s.forbidden[r.URL.Query().Get("v")]
		s.mu.Unlock()

		switch {
		case forbidden:
			http.Error(w, "<Error><Code>AccessDenied</Code></Error>", http.StatusForbidden)
		case fail:
			http.Error(w, "upstream hiccup", http.StatusInternalServerError)
		default:
			_, _ = w.Write(rawBody(period))
		}
		return
	}

	key, hasKey := r.Header["X-Api-Key"]
	s.mu.Lock()
	call := len(s.apiCalls)
	s.apiCalls = append(s.apiCalls, rawAPICall{
		path:   r.URL.Path,
		query:  r.URL.Query(),
		key:    strings.Join(key, ","),
		hasKey: hasKey,
	})
	s.mu.Unlock()

	status, body := http.StatusOK, s.coverage
	if r.URL.Path != "/metadata/raw" {
		status, body = s.listing(call, s.URL)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *rawStub) calls() []rawAPICall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.apiCalls)
}

func (s *rawStub) totalBlobCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0
	for _, n := range s.blobCalls {
		total += n
	}
	return total
}

func rawListing(baseURL string, version int, periods ...string) RawFilesResponse {
	files := make([]RawFileInfo, len(periods))
	for i, p := range periods {
		files[i] = RawFileInfo{
			Period: p,
			URL:    fmt.Sprintf("%s/blob/%s?v=%d", baseURL, p, version),
			Size:   int64(len(rawBody(p))),
		}
	}
	return RawFilesResponse{
		Dataset:        "trades",
		Exchange:       "binance-futures",
		Symbol:         rawTestSymbol,
		SchemaVersion:  1,
		ExpiresIn:      3600,
		Files:          files,
		MissingPeriods: []string{},
	}
}

// staticListing answers every listing request with the same files.
func staticListing(periods ...string) func(int, string) (int, any) {
	return func(_ int, baseURL string) (int, any) {
		return http.StatusOK, rawListing(baseURL, 1, periods...)
	}
}

func rawPath(t *testing.T, dir string, dataset RawDataset, exchange Exchange, period string) string {
	t.Helper()
	path, err := RawFilePath(dir, dataset, exchange, rawTestSymbol, period)
	if err != nil {
		t.Fatalf("RawFilePath(%s): %v", period, err)
	}
	return path
}

// writtenFiles lists every file under dir, relative and slash-separated.
func writtenFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		files = append(files, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	slices.Sort(files)
	return files
}

func rawArgs(outputDir string, extra ...string) []string {
	return append([]string{
		"raw", "trades",
		"--exchange", "binance-futures",
		"--symbol", rawTestSymbol,
		"--output-dir", outputDir,
	}, extra...)
}

// ---------------------------------------------------------------------------
// Pure helpers
// ---------------------------------------------------------------------------

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(rawDateLayout, s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRawWindows(t *testing.T) {
	tests := []struct {
		name       string
		start, end string
		expected   [][2]string
	}{
		{"single day", "2025-06-01", "2025-06-01", [][2]string{{"2025-06-01", "2025-06-01"}}},
		{"exactly the limit", "2025-01-01", "2026-01-01", [][2]string{{"2025-01-01", "2026-01-01"}}},
		{
			"one day over the limit splits",
			"2025-01-01", "2026-01-02",
			[][2]string{{"2025-01-01", "2026-01-01"}, {"2026-01-02", "2026-01-02"}},
		},
		{
			// The same split the Python client's tests pin.
			"across a leap year",
			"2024-07-01", "2025-07-31",
			[][2]string{{"2024-07-01", "2025-07-01"}, {"2025-07-02", "2025-07-31"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			windows, err := rawWindows(mustDate(t, tt.start), mustDate(t, tt.end))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := make([][2]string, len(windows))
			for i, w := range windows {
				got[i] = [2]string{w.start.Format(rawDateLayout), w.end.Format(rawDateLayout)}
				if days := int(w.end.Sub(w.start).Hours()/24) + 1; days > RawMaxRangeDays {
					t.Errorf("window %d covers %d days, over the %d-day limit", i, days, RawMaxRangeDays)
				}
			}
			if !slices.Equal(got, tt.expected) {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}

	if _, err := rawWindows(mustDate(t, "2025-06-02"), mustDate(t, "2025-06-01")); err == nil {
		t.Error("expected an error when the end is before the start")
	}
}

func TestParseRawPeriod(t *testing.T) {
	tests := []struct {
		period      string
		first, last string
	}{
		{"2025-06", "2025-06-01", "2025-06-30"},
		{"2025-02", "2025-02-01", "2025-02-28"},
		{"2024-02", "2024-02-01", "2024-02-29"},
		{"2026-08-10", "2026-08-10", "2026-08-10"},
	}
	for _, tt := range tests {
		p, err := parseRawPeriod(tt.period)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tt.period, err)
		}
		if got := p.first.Format(rawDateLayout); got != tt.first {
			t.Errorf("%s: expected first day %s, got %s", tt.period, tt.first, got)
		}
		if got := p.last().Format(rawDateLayout); got != tt.last {
			t.Errorf("%s: expected last day %s, got %s", tt.period, tt.last, got)
		}
	}

	for _, bad := range []string{"", "2025", "2025-13", "2025-6", "2025/06", "2025-06-31", "../../etc"} {
		if _, err := parseRawPeriod(bad); err == nil {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
}

func TestRawFilePath(t *testing.T) {
	previous := rawEscapeColon
	t.Cleanup(func() { rawEscapeColon = previous })

	rel := func(t *testing.T, period string) string {
		t.Helper()
		path, err := RawFilePath("raw", RawQuotes, ExchangeOkxPerps, rawTestSymbol, period)
		if err != nil {
			t.Fatal(err)
		}
		return filepath.ToSlash(path)
	}

	rawEscapeColon = false
	if got, want := rel(t, "2025-06"), "raw/quotes/exchange=okx-perps/symbol=perpetual-BTC-USDT:USDT/year=2025/month=06/data.parquet"; got != want {
		t.Errorf("monthly: expected %s, got %s", want, got)
	}
	if got, want := rel(t, "2026-08-01"), "raw/quotes/exchange=okx-perps/symbol=perpetual-BTC-USDT:USDT/year=2026/month=08/day=01/data.parquet"; got != want {
		t.Errorf("daily: expected %s, got %s", want, got)
	}

	rawEscapeColon = true
	if got := rel(t, "2025-06"); !strings.Contains(got, "symbol=perpetual-BTC-USDT%3AUSDT/") {
		t.Errorf("expected the colon escaped as %%3A on Windows, got %s", got)
	}

	if _, err := RawFilePath("raw", RawQuotes, ExchangeOkxPerps, rawTestSymbol, "junk"); err == nil {
		t.Error("expected an unparseable period to be rejected")
	}
}

func TestValidateRawQuery(t *testing.T) {
	for _, dataset := range RawDatasets {
		for _, exchange := range []Exchange{ExchangeBinanceFutures, ExchangeOkxPerps} {
			if err := ValidateRawQuery(dataset, exchange); err != nil {
				t.Errorf("%s on %s: unexpected error: %v", dataset, exchange, err)
			}
		}
	}

	for _, dataset := range []RawDataset{RawTrades, RawQuotes} {
		if err := ValidateRawQuery(dataset, ExchangeHyperliquidPerps); err != nil {
			t.Errorf("%s on hyperliquid-perps: unexpected error: %v", dataset, err)
		}
	}
	for _, dataset := range []RawDataset{RawMarkPrice, RawIndexPrice, RawFundingRate, RawOpenInterest} {
		err := ValidateRawQuery(dataset, ExchangeHyperliquidPerps)
		if err == nil || !strings.Contains(err.Error(), "binance-futures, okx-perps") {
			t.Errorf("%s on hyperliquid-perps: expected an error naming the available exchanges, got %v", dataset, err)
		}
	}

	if err := ValidateRawQuery("l2_book", ExchangeBinanceFutures); err == nil || !strings.Contains(err.Error(), "open_interest") {
		t.Errorf("expected an unknown dataset to list the valid ones, got %v", err)
	}
	if err := ValidateRawQuery(RawTrades, "bybit"); err == nil || !strings.Contains(err.Error(), "okx-perps") {
		t.Errorf("expected an unknown exchange to list the valid ones, got %v", err)
	}
}

func TestHandleAPIError_ForbiddenKeepsTheReason(t *testing.T) {
	stub := newRawStub(t, func(int, string) (int, any) {
		return http.StatusForbidden, map[string]string{
			"error":       "Raw data is not included in the prime plan. It is part of Prime + Raw.",
			"code":        "raw_not_in_plan",
			"upgrade_url": "https://aperiodic.io/pricing",
		}
	})

	client := NewAperiodicClient("key-1")
	_, err := client.FetchRawFiles(RawQuery{Dataset: RawTrades, Exchange: ExchangeBinanceFutures, Symbol: rawTestSymbol}, mustDate(t, "2025-06-01"), mustDate(t, "2025-06-30"))

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T (%v)", err, err)
	}
	if apiErr.StatusCode != http.StatusForbidden || apiErr.Code != "raw_not_in_plan" || apiErr.UpgradeURL != "https://aperiodic.io/pricing" {
		t.Errorf("expected the 403's code and upgrade URL, got %+v", apiErr)
	}
	if !strings.Contains(apiErr.Message, "not included") {
		t.Errorf("expected the server's message, got %q", apiErr.Message)
	}
	if len(stub.calls()) != 1 {
		t.Errorf("expected one listing request, got %d", len(stub.calls()))
	}
}

// ---------------------------------------------------------------------------
// aperiodic raw <dataset>
// ---------------------------------------------------------------------------

func TestCLI_Raw_WritesTheHiveLayout(t *testing.T) {
	stub := newRawStub(t, staticListing("2025-06", "2025-07"))
	outputDir := t.TempDir()

	stdout, stderr, code := runCLI(rawArgs(outputDir, "--start-date", "2025-06-01", "--end-date", "2025-07-31")...)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "Successfully downloaded 2 files") {
		t.Errorf("expected two successes, got: %s", stdout)
	}

	calls := stub.calls()
	if len(calls) != 1 {
		t.Fatalf("expected one listing request, got %d", len(calls))
	}
	call := calls[0]
	if call.path != "/data/raw/trades" {
		t.Errorf("expected /data/raw/trades, got %s", call.path)
	}
	if call.key != "key-1" {
		t.Errorf("expected X-API-KEY key-1, got %q", call.key)
	}
	for param, want := range map[string]string{
		"exchange":   "binance-futures",
		"symbol":     rawTestSymbol,
		"start_date": "2025-06-01",
		"end_date":   "2025-07-31",
	} {
		if got := call.query.Get(param); got != want {
			t.Errorf("expected %s=%s, got %q", param, want, got)
		}
	}

	for _, period := range []string{"2025-06", "2025-07"} {
		path := rawPath(t, outputDir, RawTrades, ExchangeBinanceFutures, period)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("expected %s to be written: %v", path, err)
		}
		if string(body) != string(rawBody(period)) {
			t.Errorf("expected %s to hold its own period's bytes, got %q", path, body)
		}
	}

	// Nothing but the two data files: no leftover .part files.
	if got := writtenFiles(t, outputDir); len(got) != 2 {
		t.Errorf("expected exactly two files, got %v", got)
	}
}

func TestCLI_Raw_LayoutIsTheBucketLayout(t *testing.T) {
	// The on-disk layout must match the Python client's download_raw exactly,
	// so pin the literal paths (where ":" is a legal file-name character).
	if rawEscapeColon {
		t.Skip("symbols are escaped on this platform; TestRawFilePath covers it")
	}
	newRawStub(t, staticListing("2026-07", "2026-08-01", "2026-08-02"))
	outputDir := t.TempDir()

	_, stderr, code := runCLI(rawArgs(outputDir, "--start-date", "2026-07-15", "--end-date", "2026-08-02")...)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}

	prefix := "trades/exchange=binance-futures/symbol=perpetual-BTC-USDT:USDT/year=2026/"
	expected := []string{
		prefix + "month=07/data.parquet",
		prefix + "month=08/day=01/data.parquet",
		prefix + "month=08/day=02/data.parquet",
	}
	if got := writtenFiles(t, outputDir); !slices.Equal(got, expected) {
		t.Errorf("expected %v, got %v", expected, got)
	}
}

func TestCLI_Raw_SplitsLongRangesAndDedupesMonths(t *testing.T) {
	stub := newRawStub(t, func(call int, baseURL string) (int, any) {
		if call == 0 {
			return http.StatusOK, rawListing(baseURL, 1, "2025-05", "2025-06")
		}
		return http.StatusOK, rawListing(baseURL, 1, "2025-06", "2025-07")
	})
	outputDir := t.TempDir()

	stdout, stderr, code := runCLI(rawArgs(outputDir, "--start-date", "2024-07-01", "--end-date", "2025-07-31")...)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}

	var windows [][2]string
	for _, c := range stub.calls() {
		windows = append(windows, [2]string{c.query.Get("start_date"), c.query.Get("end_date")})
	}
	expected := [][2]string{{"2024-07-01", "2025-07-01"}, {"2025-07-02", "2025-07-31"}}
	if !slices.Equal(windows, expected) {
		t.Errorf("expected windows %v, got %v", expected, windows)
	}

	// 2025-06 was listed twice but downloaded once.
	if got := stub.totalBlobCalls(); got != 3 {
		t.Errorf("expected 3 downloads, got %d", got)
	}
	if !strings.Contains(stdout, "Found 3 trades files") {
		t.Errorf("expected three files found, got: %s", stdout)
	}
}

func TestCLI_Raw_SkipsFilesAlreadyPresent(t *testing.T) {
	stub := newRawStub(t, staticListing("2025-06", "2025-07"))
	outputDir := t.TempDir()

	kept := rawPath(t, outputDir, RawTrades, ExchangeBinanceFutures, "2025-06")
	stale := rawPath(t, outputDir, RawTrades, ExchangeBinanceFutures, "2025-07")
	for path, body := range map[string][]byte{kept: rawBody("2025-06"), stale: []byte("truncated")} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0644); err != nil {
			t.Fatal(err)
		}
	}

	args := rawArgs(outputDir, "--start-date", "2025-06-01", "--end-date", "2025-07-31")
	stdout, stderr, code := runCLI(args...)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	if got := stub.totalBlobCalls(); got != 1 {
		t.Errorf("expected only the wrong-sized file to be downloaded, got %d downloads", got)
	}
	if !strings.Contains(stdout, "Skipping 1 already present") {
		t.Errorf("expected the skip to be reported, got: %s", stdout)
	}
	if body, _ := os.ReadFile(stale); string(body) != string(rawBody("2025-07")) {
		t.Errorf("expected the wrong-sized file to be replaced, got %q", body)
	}

	stdout, _, code = runCLI(args...)
	if code != 0 || stub.totalBlobCalls() != 1 {
		t.Errorf("expected a re-run to download nothing, got exit %d and %d downloads", code, stub.totalBlobCalls())
	}
	if !strings.Contains(stdout, "Nothing to download") {
		t.Errorf("expected a nothing-to-do message, got: %s", stdout)
	}

	_, _, code = runCLI(append(args, "--overwrite")...)
	if code != 0 || stub.totalBlobCalls() != 3 {
		t.Errorf("expected --overwrite to download both again, got exit %d and %d downloads", code, stub.totalBlobCalls())
	}
}

func TestCLI_Raw_RefreshesAnExpiredURL(t *testing.T) {
	stub := newRawStub(t, func(call int, baseURL string) (int, any) {
		return http.StatusOK, rawListing(baseURL, call+1, "2025-06")
	})
	stub.forbidden["1"] = true
	outputDir := t.TempDir()

	_, stderr, code := runCLI(rawArgs(outputDir, "--start-date", "2025-06-15", "--end-date", "2025-06-20")...)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}

	calls := stub.calls()
	if len(calls) != 2 {
		t.Fatalf("expected the listing to be re-requested once, got %d requests", len(calls))
	}
	// The refresh asks for just that month.
	refresh := calls[1].query
	if refresh.Get("start_date") != "2025-06-01" || refresh.Get("end_date") != "2025-06-30" {
		t.Errorf("expected the refresh to cover 2025-06-01..2025-06-30, got %s..%s", refresh.Get("start_date"), refresh.Get("end_date"))
	}
	// A 403 is not retried with backoff: one try per URL.
	if got := stub.totalBlobCalls(); got != 2 {
		t.Errorf("expected two download attempts, got %d", got)
	}

	body, _ := os.ReadFile(rawPath(t, outputDir, RawTrades, ExchangeBinanceFutures, "2025-06"))
	if string(body) != string(rawBody("2025-06")) {
		t.Errorf("expected the file from the fresh URL, got %q", body)
	}
}

func TestCLI_Raw_GivesUpAfterOneRefresh(t *testing.T) {
	stub := newRawStub(t, staticListing("2025-06"))
	stub.forbidden["1"] = true
	outputDir := t.TempDir()

	_, stderr, code := runCLI(rawArgs(outputDir, "--start-date", "2025-06-01", "--end-date", "2025-06-30")...)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr, "403") || !strings.Contains(stderr, "2025-06") {
		t.Errorf("expected the 403 and the period in stderr, got: %s", stderr)
	}
	if got := stub.totalBlobCalls(); got != 2 {
		t.Errorf("expected two download attempts, got %d", got)
	}
	if got := writtenFiles(t, outputDir); len(got) != 0 {
		t.Errorf("expected nothing on disk, got %v", got)
	}
}

func TestCLI_Raw_RetriesTransientDownloadErrors(t *testing.T) {
	stub := newRawStub(t, staticListing("2025-06"))
	stub.failFirst["2025-06"] = 2
	outputDir := t.TempDir()

	_, stderr, code := runCLI(rawArgs(outputDir, "--start-date", "2025-06-01", "--end-date", "2025-06-30")...)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	if got := stub.totalBlobCalls(); got != 3 {
		t.Errorf("expected two failures then a success, got %d attempts", got)
	}
	if got := len(stub.calls()); got != 1 {
		t.Errorf("expected a 500 to be retried without re-listing, got %d listing requests", got)
	}
}

func TestCLI_Raw_NotInPlanPointsToTheUpgrade(t *testing.T) {
	newRawStub(t, func(int, string) (int, any) {
		return http.StatusForbidden, map[string]string{
			"error":       "Raw data is not included in the prime plan. It is part of Prime + Raw.",
			"code":        "raw_not_in_plan",
			"upgrade_url": "https://aperiodic.io/pricing",
		}
	})

	_, stderr, code := runCLI(rawArgs(t.TempDir(), "--start-date", "2025-06-01", "--end-date", "2025-06-30")...)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	for _, want := range []string{"not included in the prime plan", "Prime + Raw", "https://aperiodic.io/pricing", "--preview"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("expected stderr to mention %q, got: %s", want, stderr)
		}
	}
}

func TestCLI_Raw_ShowsValidationDetails(t *testing.T) {
	newRawStub(t, func(int, string) (int, any) {
		return http.StatusBadRequest, map[string]any{
			"error":   "Invalid query parameters",
			"details": []string{"symbol: Symbol must match format like perpetual-BTC-USDT:USDT"},
		}
	})

	_, stderr, code := runCLI(rawArgs(t.TempDir(), "--start-date", "2025-06-01", "--end-date", "2025-06-30")...)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr, "Invalid query parameters") || !strings.Contains(stderr, "Symbol must match") {
		t.Errorf("expected the error and its details, got: %s", stderr)
	}
}

func TestCLI_Raw_PreviewUsesTheDemoKey(t *testing.T) {
	stub := newRawStub(t, staticListing("2025-06"))
	t.Setenv("APERIODIC_API_KEY", "")
	outputDir := t.TempDir()

	_, stderr, code := runCLI(rawArgs(outputDir, "--preview")...)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}

	calls := stub.calls()
	if len(calls) != 1 {
		t.Fatalf("expected one listing request, got %d", len(calls))
	}
	if calls[0].path != "/data/raw/preview/trades" {
		t.Errorf("expected the preview path, got %s", calls[0].path)
	}
	if calls[0].key != DemoAPIKey {
		t.Errorf("expected X-API-KEY %s, got %q", DemoAPIKey, calls[0].key)
	}
	if len(calls[0].query) != 2 || calls[0].query.Get("exchange") != "binance-futures" || calls[0].query.Get("symbol") != rawTestSymbol {
		t.Errorf("expected only exchange and symbol, got %v", calls[0].query)
	}
	if _, err := os.Stat(rawPath(t, outputDir, RawTrades, ExchangeBinanceFutures, "2025-06")); err != nil {
		t.Errorf("expected the preview file on disk: %v", err)
	}
}

func TestCLI_Raw_FlagsMayFollowOrPrecedeTheDataset(t *testing.T) {
	stub := newRawStub(t, staticListing("2025-06"))
	outputDir := t.TempDir()

	_, stderr, code := runCLI(
		"raw", "--exchange", "okx-perps", "quotes",
		"--symbol", rawTestSymbol,
		"--start-date", "2025-06-01", "--end-date", "2025-06-30",
		"--output-dir", outputDir,
	)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	call := stub.calls()[0]
	if call.path != "/data/raw/quotes" || call.query.Get("exchange") != "okx-perps" {
		t.Errorf("expected quotes on okx-perps, got %s ?%s", call.path, call.query.Encode())
	}
}

func TestCLI_Raw_ReportsMissingPeriods(t *testing.T) {
	newRawStub(t, func(_ int, baseURL string) (int, any) {
		listing := rawListing(baseURL, 1, "2025-06")
		listing.MissingPeriods = []string{"2025-04", "2025-05"}
		return http.StatusOK, listing
	})

	stdout, stderr, code := runCLI(rawArgs(t.TempDir(), "--start-date", "2025-04-01", "--end-date", "2025-06-30")...)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "No file for 2 period(s) in the range: 2025-04, 2025-05") {
		t.Errorf("expected the missing periods, got: %s", stdout)
	}
}

func TestCLI_Raw_NoFiles(t *testing.T) {
	newRawStub(t, staticListing())

	stdout, stderr, code := runCLI(rawArgs(t.TempDir(), "--start-date", "2025-06-01", "--end-date", "2025-06-30")...)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "No data found") {
		t.Errorf("expected a no-data message, got: %s", stdout)
	}
}

func TestCLI_Raw_ValidatesBeforeCallingTheAPI(t *testing.T) {
	stub := newRawStub(t, staticListing("2025-06"))
	outputDir := t.TempDir()
	dates := []string{"--start-date", "2025-06-01", "--end-date", "2025-06-30"}

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no dataset", []string{"raw", "--symbol", rawTestSymbol, "--output-dir", outputDir}, "raw dataset is required"},
		{"unknown dataset", append([]string{"raw", "book", "--symbol", rawTestSymbol, "--output-dir", outputDir}, dates...), `unknown raw dataset "book"`},
		{"unknown exchange", append([]string{"raw", "trades", "--exchange", "bybit", "--symbol", rawTestSymbol, "--output-dir", outputDir}, dates...), `unknown exchange "bybit"`},
		{"derivative on hyperliquid", append([]string{"raw", "funding_rate", "--exchange", "hyperliquid-perps", "--symbol", "perpetual-BTC-USDC:USDC", "--output-dir", outputDir}, dates...), "trades and quotes only"},
		{"missing symbol", append([]string{"raw", "trades", "--output-dir", outputDir}, dates...), "--symbol is required"},
		{"missing output dir", append([]string{"raw", "trades", "--symbol", rawTestSymbol}, dates...), "--output-dir is mandatory"},
		{"missing dates", rawArgs(outputDir), "--start-date and --end-date are required"},
		{"bad date", rawArgs(outputDir, "--start-date", "2025-06-31", "--end-date", "2025-07-01"), `--start-date "2025-06-31"`},
		{"reversed dates", rawArgs(outputDir, "--start-date", "2025-07-01", "--end-date", "2025-06-01"), "on or after"},
		{"zero concurrency", rawArgs(outputDir, append(dates, "--max-concurrent", "0")...), "--max-concurrent"},
		{"extra argument", append(rawArgs(outputDir, dates...), "quotes"), "unexpected arguments: quotes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, code := runCLI(tt.args...)
			if code != 1 {
				t.Fatalf("expected exit code 1, got %d; stderr: %s", code, stderr)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Errorf("expected stderr to contain %q, got: %s", tt.want, stderr)
			}
		})
	}

	if n := len(stub.calls()); n != 0 {
		t.Errorf("expected no API calls for invalid input, got %d", n)
	}
}

func TestCLI_Raw_RequiresAKeyWithoutPreview(t *testing.T) {
	stub := newRawStub(t, staticListing("2025-06"))
	t.Setenv("APERIODIC_API_KEY", "")

	_, stderr, code := runCLI(rawArgs(t.TempDir(), "--start-date", "2025-06-01", "--end-date", "2025-06-30")...)
	if code != 1 || !strings.Contains(stderr, "APERIODIC_API_KEY") {
		t.Errorf("expected a missing-key error, got exit %d; stderr: %s", code, stderr)
	}
	if n := len(stub.calls()); n != 0 {
		t.Errorf("expected no API calls, got %d", n)
	}
}

func TestCLI_Raw_Help(t *testing.T) {
	for _, args := range [][]string{{"raw"}, {"raw", "help"}, {"raw", "--help"}, {"raw", "trades", "-h"}} {
		stdout, _, code := runCLI(args...)
		if code != 0 {
			t.Errorf("%v: expected exit code 0, got %d", args, code)
		}
		for _, want := range []string{"funding_rate", "Prime + Raw", "year=YYYY/month=MM/day=DD/data.parquet", "-overwrite"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("%v: expected help to mention %q", args, want)
			}
		}
	}

	stdout, _, _ := runCLI("help")
	if !strings.Contains(stdout, "aperiodic raw <dataset>") {
		t.Error("expected the top-level help to list the raw command")
	}
}

// ---------------------------------------------------------------------------
// aperiodic raw coverage
// ---------------------------------------------------------------------------

func rawCoverageBody() map[string]any {
	series := func(first, last string, bytes int64) map[string]any {
		return map[string]any{"first": first, "last": last, "days": 10, "missing": []string{"2020-01-02"}, "bytes": bytes}
	}
	return map[string]any{
		"schema_version":   1,
		"daily_files_from": "2026-08-01",
		"preview": map[string]any{
			"period": "2025-06",
			"symbols": map[string]string{
				"binance-futures":   "perpetual-BTC-USDT:USDT",
				"okx-perps":         "perpetual-BTC-USDT:USDT",
				"hyperliquid-perps": "perpetual-BTC-USDC:USDC",
			},
		},
		"datasets": []any{},
		"summary": []map[string]any{
			{"dataset": "trades", "exchange": "binance-futures", "symbols": 2, "first": "2019-12-01", "last": "2026-09-27", "bytes": 3 << 30},
			{"dataset": "quotes", "exchange": "okx-perps", "symbols": 1, "first": "2020-01-01", "last": "2026-09-27", "bytes": 7},
		},
		"coverage": map[string]any{
			"generated_at": "2026-09-28T03:00:00Z",
			"datasets": map[string]any{
				"trades": map[string]any{
					"binance-futures": map[string]any{
						"perpetual-ETH-USDT:USDT": series("2019-12-08", "2026-09-27", 1<<30),
						"perpetual-BTC-USDT:USDT": series("2019-12-01", "2026-09-27", 2<<30),
					},
				},
				"quotes": map[string]any{
					"okx-perps": map[string]any{
						"perpetual-BTC-USDT:USDT": series("2020-01-01", "2026-09-27", 7),
					},
				},
			},
		},
	}
}

func TestCLI_RawCoverage_Summary(t *testing.T) {
	stub := newRawStub(t, staticListing())
	stub.coverage = rawCoverageBody()
	t.Setenv("APERIODIC_API_KEY", "")

	stdout, stderr, code := runCLI("raw", "coverage")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}

	calls := stub.calls()
	if len(calls) != 1 || calls[0].path != "/metadata/raw" {
		t.Fatalf("expected one /metadata/raw request, got %+v", calls)
	}
	if calls[0].hasKey {
		t.Error("expected no X-API-KEY header on the public coverage endpoint")
	}

	for _, want := range []string{
		"as of 2026-09-28T03:00:00Z",
		"trades   binance-futures  2        2019-12-01  2026-09-27  3.0 GiB",
		"quotes   okx-perps        1        2020-01-01  2026-09-27  7 B",
		"2025-06 of binance-futures perpetual-BTC-USDT:USDT, okx-perps perpetual-BTC-USDT:USDT, hyperliquid-perps perpetual-BTC-USDC:USDC",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, stdout)
		}
	}
}

func TestCLI_RawCoverage_ListsSymbols(t *testing.T) {
	stub := newRawStub(t, staticListing())
	stub.coverage = rawCoverageBody()

	stdout, stderr, code := runCLI("raw", "coverage", "--dataset", "trades", "--exchange", "binance-futures")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected a header and two symbols, got:\n%s", stdout)
	}
	if !strings.HasPrefix(lines[1], "trades   binance-futures  perpetual-BTC-USDT:USDT  2019-12-01  2026-09-27  10    1        2.0 GiB") {
		t.Errorf("expected BTC first with its coverage, got %q", lines[1])
	}
	if !strings.Contains(lines[2], "perpetual-ETH-USDT:USDT") {
		t.Errorf("expected ETH second, got %q", lines[2])
	}

	stdout, _, _ = runCLI("raw", "coverage", "--symbol", rawTestSymbol)
	if strings.Count(stdout, rawTestSymbol) != 2 {
		t.Errorf("expected the symbol in trades and quotes, got:\n%s", stdout)
	}

	stdout, _, _ = runCLI("raw", "coverage", "--symbol", "perpetual-NOPE-USDT:USDT")
	if !strings.Contains(stdout, "No raw coverage matches") {
		t.Errorf("expected a no-match message, got:\n%s", stdout)
	}
}

func TestCLI_RawCoverage_RejectsUnknownFilters(t *testing.T) {
	stub := newRawStub(t, staticListing())

	for _, args := range [][]string{
		{"raw", "coverage", "--dataset", "book"},
		{"raw", "coverage", "--exchange", "bybit"},
		{"raw", "coverage", "--dataset", "open_interest", "--exchange", "hyperliquid-perps"},
		{"raw", "coverage", "trades"},
	} {
		if _, _, code := runCLI(args...); code != 1 {
			t.Errorf("%v: expected exit code 1, got %d", args, code)
		}
	}
	if n := len(stub.calls()); n != 0 {
		t.Errorf("expected no API calls for invalid filters, got %d", n)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := map[int64]string{
		0:         "0 B",
		1023:      "1023 B",
		1024:      "1.0 KiB",
		1536:      "1.5 KiB",
		5 << 20:   "5.0 MiB",
		3 << 30:   "3.0 GiB",
		1<<40 + 1: "1.0 TiB",
	}
	for n, want := range tests {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%d): expected %s, got %s", n, want, got)
		}
	}
}

// ---------------------------------------------------------------------------
// Transfer timeouts
// ---------------------------------------------------------------------------

func withRawStallTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	previousStall, previousBackoff := rawStallTimeout, rawRetryBackoff
	rawStallTimeout, rawRetryBackoff = d, time.Millisecond
	t.Cleanup(func() { rawStallTimeout, rawRetryBackoff = previousStall, previousBackoff })
}

func TestStreamRawFile_OutlastsTheClientTimeout(t *testing.T) {
	// A raw file can take far longer than the client's overall timeout; as
	// long as bytes keep arriving the transfer must not be cut off.
	withRawStallTimeout(t, 200*time.Millisecond)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 8; i++ {
			_, _ = w.Write([]byte("chunk "))
			w.(http.Flusher).Flush()
			time.Sleep(25 * time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)

	client := NewAperiodicClient("key-1")
	client.HTTPClient.Timeout = 50 * time.Millisecond
	dest := filepath.Join(t.TempDir(), "nested", "data.parquet")

	if err := client.streamRawFile(srv.URL, dest); err != nil {
		t.Fatalf("expected a steady transfer to finish, got: %v", err)
	}
	if body, _ := os.ReadFile(dest); string(body) != strings.Repeat("chunk ", 8) {
		t.Errorf("expected the whole body, got %q", body)
	}
}

func TestStreamRawFile_AbortsAStalledTransfer(t *testing.T) {
	withRawStallTimeout(t, 50*time.Millisecond)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	err := NewAperiodicClient("key-1").streamRawFile(srv.URL, filepath.Join(dir, "data.parquet"))
	if err == nil || !strings.Contains(err.Error(), "no data received") {
		t.Fatalf("expected a stall error, got: %v", err)
	}
	if got := writtenFiles(t, dir); len(got) != 0 {
		t.Errorf("expected no data.parquet or .part left behind, got %v", got)
	}
}
