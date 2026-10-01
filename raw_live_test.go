package aperiodic

// Raw data against the live API (production, or APERIODIC_API_URL): what a
// user running the CLI gets. The files are the June 2025 BTC perpetuals, in
// the raw buckets since the proof of concept. The paid download needs
// APERIODIC_API_KEY to be on the Prime + Raw plan.

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

var isoDay = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// requireJuneParquet checks the June 2025 file landed where RawFilePath says,
// and is Parquet rather than a storage error page.
func requireJuneParquet(t *testing.T, outputDir string, dataset RawDataset) {
	t.Helper()

	path, err := RawFilePath(outputDir, dataset, ExchangeBinanceFutures, rawTestSymbol, "2025-06")
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected the June file at %s: %v", path, err)
	}
	if !bytes.HasPrefix(content, []byte("PAR1")) || !bytes.HasSuffix(content, []byte("PAR1")) {
		t.Fatalf("%s is not a Parquet file (%d bytes)", path, len(content))
	}
}

func TestCLI_Raw_Live_CoverageListsEveryDataset(t *testing.T) {
	useLiveAPI(t)

	stdout, stderr, code := runCLI("raw", "coverage")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	for _, dataset := range RawDatasets {
		if !strings.Contains(stdout, "\n"+string(dataset)+" ") {
			t.Errorf("expected a summary row for %s, got:\n%s", dataset, stdout)
		}
	}
}

func TestCLI_Raw_Live_CoverageOfOneSymbol(t *testing.T) {
	useLiveAPI(t)

	stdout, stderr, code := runCLI(
		"raw", "coverage",
		"-dataset", "trades",
		"-exchange", "binance-futures",
		"-symbol", rawTestSymbol,
	)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected a header and one row, got:\n%s", stdout)
	}
	// DATASET EXCHANGE SYMBOL FIRST LAST DAYS ...
	row := strings.Fields(lines[1])
	if len(row) < 5 || row[2] != rawTestSymbol {
		t.Fatalf("unexpected coverage row: %q", lines[1])
	}
	first, last := row[3], row[4]
	if !isoDay.MatchString(first) || !isoDay.MatchString(last) {
		t.Fatalf("expected FIRST and LAST as YYYY-MM-DD, got %q and %q", first, last)
	}
	if first > "2025-06-01" || last < "2025-06-30" {
		t.Errorf("expected the coverage to span June 2025, got %s to %s", first, last)
	}
}

func TestCLI_Raw_Live_PreviewNeedsNoKey(t *testing.T) {
	useLiveAPI(t)
	t.Setenv("APERIODIC_API_KEY", "")

	outputDir := t.TempDir()
	stdout, stderr, code := runCLI(
		"raw", "funding_rate", "-preview",
		"-symbol", rawTestSymbol,
		"-output-dir", outputDir,
	)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "Successfully downloaded 1 files") {
		t.Errorf("expected one file downloaded, got: %s", stdout)
	}
	requireJuneParquet(t, outputDir, RawFundingRate)
}

func TestCLI_Raw_Live_DemoKeyIsRefusedOutsideThePreview(t *testing.T) {
	useLiveAPI(t)
	t.Setenv("APERIODIC_API_KEY", DemoAPIKey)

	outputDir := t.TempDir()
	_, stderr, code := runCLI(
		"raw", "trades",
		"-symbol", rawTestSymbol,
		"-start-date", "2025-06-01",
		"-end-date", "2025-06-02",
		"-output-dir", outputDir,
	)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "401") {
		t.Errorf("expected a 401, got: %s", stderr)
	}
}

// Downloads the whole June trades file (a few hundred MB): the size raw users
// stream, which the small preview file above does not exercise.
func TestCLI_Raw_Live_DownloadsAndThenSkipsTheJuneFile(t *testing.T) {
	requireAPIKey(t)

	outputDir := t.TempDir()
	args := []string{
		"raw", "trades",
		"-exchange", "binance-futures",
		"-symbol", rawTestSymbol,
		"-start-date", "2025-06-01",
		"-end-date", "2025-06-02",
		"-output-dir", outputDir,
	}

	stdout, stderr, code := runCLI(args...)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "Successfully downloaded 1 files") {
		t.Errorf("expected one file downloaded, got: %s", stdout)
	}
	requireJuneParquet(t, outputDir, RawTrades)

	stdout, stderr, code = runCLI(args...)
	if code != 0 {
		t.Fatalf("expected exit code 0 on the second run, got %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "Skipping 1 already present") {
		t.Errorf("expected the second run to skip the file, got: %s", stdout)
	}
}
