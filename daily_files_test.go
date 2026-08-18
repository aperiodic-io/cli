package aperiodic

// Tests for the daily-parquet layout.
//
// Data up to 2026-07-31 is served as one parquet per month; from 2026-08-01 the
// API returns one per day, so a single response can hold many files sharing the
// same year and month. These tests pin the property that makes that safe: every
// file in a response lands on its own path on disk.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func dayPtr(d int) *int { return &d }

func TestParquetFilename(t *testing.T) {
	// The month is zero-padded here even though the R2 object key writes it
	// unpadded. These are local filenames chosen for sortability, not keys —
	// do not "align" them with the producer's key format.
	tests := []struct {
		name     string
		file     FileInfo
		expected string
	}{
		{"monthly file omits the day", FileInfo{Year: 2026, Month: 7}, "2026-07.parquet"},
		{"daily file appends a zero-padded day", FileInfo{Year: 2026, Month: 8, Day: dayPtr(1)}, "2026-08-01.parquet"},
		{"two-digit day is unchanged", FileInfo{Year: 2026, Month: 8, Day: dayPtr(31)}, "2026-08-31.parquet"},
		{"single-digit month and day both pad", FileInfo{Year: 2027, Month: 3, Day: dayPtr(9)}, "2027-03-09.parquet"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parquetFilename(tt.file); got != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, got)
			}
		})
	}
}

func TestParquetFilename_SortsChronologically(t *testing.T) {
	// The reason for zero-padding: a directory listing is the user's index of
	// what they downloaded, so lexical order has to be date order.
	files := []FileInfo{
		{Year: 2026, Month: 8, Day: dayPtr(10)},
		{Year: 2026, Month: 8, Day: dayPtr(2)},
		{Year: 2026, Month: 9, Day: dayPtr(1)},
		{Year: 2026, Month: 8, Day: dayPtr(31)},
	}

	names := make([]string, len(files))
	for i, f := range files {
		names[i] = parquetFilename(f)
	}
	slices.Sort(names)

	expected := []string{
		"2026-08-02.parquet",
		"2026-08-10.parquet",
		"2026-08-31.parquet",
		"2026-09-01.parquet",
	}
	if !slices.Equal(names, expected) {
		t.Errorf("expected %v, got %v", expected, names)
	}
}

func TestResolveFilenames_RejectsCollisions(t *testing.T) {
	// Two files in the same month with no day is exactly the shape the CLI
	// produced before FileInfo carried one: a month of daily downloads all
	// pointed at 2026-08.parquet, raced to truncate it, and still exited 0.
	files := []FileInfo{
		{Year: 2026, Month: 8, URL: "first"},
		{Year: 2026, Month: 8, URL: "second"},
	}

	_, err := resolveFilenames(files)
	if err == nil {
		t.Fatal("expected an error when two files resolve to the same name")
	}
	if !strings.Contains(err.Error(), "2026-08.parquet") {
		t.Errorf("expected the colliding name in the error, got: %v", err)
	}
}

func TestResolveFilenames_DailyFilesDoNotCollide(t *testing.T) {
	files := make([]FileInfo, 0, 31)
	for d := 1; d <= 31; d++ {
		files = append(files, FileInfo{Year: 2026, Month: 8, Day: dayPtr(d)})
	}

	names, err := resolveFilenames(files)
	if err != nil {
		t.Fatalf("expected 31 distinct names, got error: %v", err)
	}

	unique := make(map[string]struct{}, len(names))
	for _, n := range names {
		unique[n] = struct{}{}
	}
	if len(unique) != 31 {
		t.Errorf("expected 31 distinct names, got %d", len(unique))
	}
}

func TestResolveFilenames_MonthlyAndDailyCoexist(t *testing.T) {
	// A range spanning the cutover: July monthly, then August daily. The
	// monthly name must not collide with any daily name.
	files := []FileInfo{
		{Year: 2026, Month: 7},
		{Year: 2026, Month: 8, Day: dayPtr(1)},
		{Year: 2026, Month: 8, Day: dayPtr(2)},
	}

	names, err := resolveFilenames(files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"2026-07.parquet", "2026-08-01.parquet", "2026-08-02.parquet"}
	if !slices.Equal(names, expected) {
		t.Errorf("expected %v, got %v", expected, names)
	}
}

func TestFileInfo_DecodesOptionalDay(t *testing.T) {
	const body = `{"files":[
		{"year":2026,"month":7,"url":"monthly"},
		{"year":2026,"month":8,"day":1,"url":"daily"}
	]}`

	var resp AggregateDataResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if len(resp.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(resp.Files))
	}
	if resp.Files[0].Day != nil {
		t.Errorf("expected the monthly file to have no day, got %d", *resp.Files[0].Day)
	}
	if resp.Files[1].Day == nil {
		t.Fatal("expected the daily file to carry a day")
	}
	if *resp.Files[1].Day != 1 {
		t.Errorf("expected day 1, got %d", *resp.Files[1].Day)
	}
}

// serveFiles stands up a stub API returning `files`, with each URL pointing at
// a blob endpoint on the same server whose body names the file it belongs to.
func serveFiles(t *testing.T, build func(baseURL string) []FileInfo) *httptest.Server {
	t.Helper()

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if name, isBlob := strings.CutPrefix(r.URL.Path, "/blob/"); isBlob {
			_, _ = fmt.Fprintf(w, "contents of %s", name)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AggregateDataResponse{Files: build(srv.URL)})
	}))
	t.Cleanup(srv.Close)

	return srv
}

func downloadedNames(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := filepath.Glob(filepath.Join(dir, "*.parquet"))
	if err != nil {
		t.Fatalf("failed to glob output dir: %v", err)
	}

	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = filepath.Base(e)
	}
	slices.Sort(names)

	return names
}

func TestCLI_DailyFilesWriteOnePerDay(t *testing.T) {
	// The regression this whole change exists for. Pre-fix all three days
	// resolved to 2026-08.parquet, so three goroutines truncated and rewrote
	// one file and the CLI reported three successes.
	srv := serveFiles(t, func(baseURL string) []FileInfo {
		files := make([]FileInfo, 0, 3)
		for d := 1; d <= 3; d++ {
			files = append(files, FileInfo{
				Year:  2026,
				Month: 8,
				Day:   dayPtr(d),
				URL:   fmt.Sprintf("%s/blob/day-%02d", baseURL, d),
			})
		}
		return files
	})

	t.Setenv("APERIODIC_API_URL", srv.URL)
	t.Setenv("APERIODIC_API_KEY", "test-key")
	outputDir := t.TempDir()

	stdout, stderr, code := runCLI(
		"ohlcv",
		"-exchange", "binance-futures",
		"-symbol", "perpetual-BTC-USDT:USDT",
		"-interval", "1h",
		"-start-date", "2026-08-01",
		"-end-date", "2026-08-03",
		"-output-dir", outputDir,
	)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "Successfully downloaded 3 files") {
		t.Errorf("expected three successes, got: %s", stdout)
	}

	names := downloadedNames(t, outputDir)
	expected := []string{"2026-08-01.parquet", "2026-08-02.parquet", "2026-08-03.parquet"}
	if !slices.Equal(names, expected) {
		t.Fatalf("expected %v, got %v", expected, names)
	}

	// Distinct names alone would still pass if every file held the same bytes,
	// so check each one received its own day's body.
	for d, name := range names {
		body, err := os.ReadFile(filepath.Join(outputDir, name))
		if err != nil {
			t.Fatalf("failed to read %s: %v", name, err)
		}
		want := fmt.Sprintf("contents of day-%02d", d+1)
		if string(body) != want {
			t.Errorf("expected %s to hold %q, got %q", name, want, string(body))
		}
	}
}

func TestCLI_RangeAcrossTheCutover(t *testing.T) {
	// July as a monthly file, August as daily ones — the shape a real query
	// spanning 2026-08-01 returns.
	srv := serveFiles(t, func(baseURL string) []FileInfo {
		return []FileInfo{
			{Year: 2026, Month: 7, URL: baseURL + "/blob/july"},
			{Year: 2026, Month: 8, Day: dayPtr(1), URL: baseURL + "/blob/aug-01"},
			{Year: 2026, Month: 8, Day: dayPtr(2), URL: baseURL + "/blob/aug-02"},
		}
	})

	t.Setenv("APERIODIC_API_URL", srv.URL)
	t.Setenv("APERIODIC_API_KEY", "test-key")
	outputDir := t.TempDir()

	_, stderr, code := runCLI(
		"ohlcv",
		"-exchange", "binance-futures",
		"-symbol", "perpetual-BTC-USDT:USDT",
		"-interval", "1h",
		"-start-date", "2026-07-30",
		"-end-date", "2026-08-02",
		"-output-dir", outputDir,
	)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}

	names := downloadedNames(t, outputDir)
	expected := []string{"2026-07.parquet", "2026-08-01.parquet", "2026-08-02.parquet"}
	if !slices.Equal(names, expected) {
		t.Errorf("expected %v, got %v", expected, names)
	}
}

func TestCLI_MonthlyOnlyRangeIsUnchanged(t *testing.T) {
	srv := serveFiles(t, func(baseURL string) []FileInfo {
		return []FileInfo{
			{Year: 2025, Month: 1, URL: baseURL + "/blob/jan"},
			{Year: 2025, Month: 2, URL: baseURL + "/blob/feb"},
		}
	})

	t.Setenv("APERIODIC_API_URL", srv.URL)
	t.Setenv("APERIODIC_API_KEY", "test-key")
	outputDir := t.TempDir()

	_, stderr, code := runCLI(
		"ohlcv",
		"-exchange", "binance-futures",
		"-symbol", "perpetual-BTC-USDT:USDT",
		"-interval", "1h",
		"-start-date", "2025-01-01",
		"-end-date", "2025-02-28",
		"-output-dir", outputDir,
	)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}

	names := downloadedNames(t, outputDir)
	expected := []string{"2025-01.parquet", "2025-02.parquet"}
	if !slices.Equal(names, expected) {
		t.Errorf("expected %v, got %v", expected, names)
	}
}

func TestCLI_CollidingFilesFailTheRun(t *testing.T) {
	// An API that omitted `day` on daily files would put the CLI back in the
	// silent-overwrite state. It has to fail instead of writing whichever
	// download finishes last.
	srv := serveFiles(t, func(baseURL string) []FileInfo {
		return []FileInfo{
			{Year: 2026, Month: 8, URL: baseURL + "/blob/first"},
			{Year: 2026, Month: 8, URL: baseURL + "/blob/second"},
		}
	})

	t.Setenv("APERIODIC_API_URL", srv.URL)
	t.Setenv("APERIODIC_API_KEY", "test-key")
	outputDir := t.TempDir()

	_, stderr, code := runCLI(
		"ohlcv",
		"-exchange", "binance-futures",
		"-symbol", "perpetual-BTC-USDT:USDT",
		"-interval", "1h",
		"-start-date", "2026-08-01",
		"-end-date", "2026-08-02",
		"-output-dir", outputDir,
	)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "2026-08.parquet") {
		t.Errorf("expected the colliding name in stderr, got: %s", stderr)
	}

	if names := downloadedNames(t, outputDir); len(names) != 0 {
		t.Errorf("expected nothing written when names collide, got %v", names)
	}
}
