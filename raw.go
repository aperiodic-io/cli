package aperiodic

// Raw per-tick data (Prime + Raw plan): trades, quotes and derivative ticks.
//
// History before 2026-08-01 is one Parquet file per calendar month, one per
// day from then on. Files are written in the bucket's own Hive layout, the same
// one the Python client's download_raw writes, so a folder filled by either can
// be topped up by the other.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

const (
	// RawMaxRangeDays is the most days the API lists per request; longer
	// ranges are split into several requests.
	RawMaxRangeDays = 366

	// DefaultRawMaxConcurrent is lower than the metrics default: raw files run
	// to gigabytes, so a few streams already saturate most links.
	DefaultRawMaxConcurrent = 4

	// RawUpgradeURL is where a plan without raw data is pointed when the API
	// refusal carries no upgrade_url of its own.
	RawUpgradeURL = "https://aperiodic.io/pricing"

	rawDateLayout  = "2006-01-02"
	rawMonthLayout = "2006-01"
	rawMaxRetries  = 3
)

var (
	// RawDatasets lists every raw dataset in catalog order.
	RawDatasets = []RawDataset{RawTrades, RawQuotes, RawMarkPrice, RawIndexPrice, RawFundingRate, RawOpenInterest}

	// Exchanges lists every supported exchange.
	Exchanges = []Exchange{ExchangeBinanceFutures, ExchangeOkxPerps, ExchangeHyperliquidPerps}

	// Hyperliquid's derivative feed carries no exchange time, so the four
	// derivative datasets are not served for it.
	rawDerivativeExchanges = []Exchange{ExchangeBinanceFutures, ExchangeOkxPerps}

	// rawStallTimeout aborts a transfer that receives nothing for this long.
	// Raw downloads cannot use the client's overall timeout: a multi-gigabyte
	// file legitimately takes far longer than DefaultTimeout to stream.
	rawStallTimeout = DefaultTimeout

	// rawRetryBackoff is the wait before the first retry of a failed
	// transfer; it doubles on every further attempt. A var so tests can
	// shrink it.
	rawRetryBackoff = time.Second

	// ":" (in every symbol) is not a legal file-name character on Windows;
	// there it is written as "%3A", which Hive-partition readers decode back.
	// A var so tests can exercise both spellings on any platform.
	rawEscapeColon = runtime.GOOS == "windows"

	// errURLRefused marks a 403 from storage: the presigned URL expired or was
	// refused, so re-sending it cannot succeed and a fresh one is requested.
	errURLRefused = errors.New("403 Forbidden (URL expired or refused)")
)

// RawExchanges returns the exchanges a raw dataset is served for.
func RawExchanges(dataset RawDataset) []Exchange {
	switch dataset {
	case RawTrades, RawQuotes:
		return Exchanges
	default:
		return rawDerivativeExchanges
	}
}

// ValidateRawQuery rejects an unknown dataset or exchange, or a combination the
// API does not serve, naming the valid choices.
func ValidateRawQuery(dataset RawDataset, exchange Exchange) error {
	if err := validateRawDataset(dataset); err != nil {
		return err
	}
	if err := validateExchange(exchange); err != nil {
		return err
	}
	available := RawExchanges(dataset)
	if !slices.Contains(available, exchange) {
		return fmt.Errorf(
			"raw dataset %s is not available for %s (available: %s); %s serves trades and quotes only",
			dataset, exchange, joinNames(available), ExchangeHyperliquidPerps,
		)
	}
	return nil
}

func validateRawDataset(dataset RawDataset) error {
	if !slices.Contains(RawDatasets, dataset) {
		return fmt.Errorf("unknown raw dataset %q (valid: %s)", dataset, joinNames(RawDatasets))
	}
	return nil
}

func validateExchange(exchange Exchange) error {
	if !slices.Contains(Exchanges, exchange) {
		return fmt.Errorf("unknown exchange %q (valid: %s)", exchange, joinNames(Exchanges))
	}
	return nil
}

func joinNames[T ~string](names []T) string {
	return strings.Join(toStrings(names), ", ")
}

func toStrings[T ~string](names []T) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = string(n)
	}
	return out
}

// RawQuery names one raw series. With Preview set, files come from the free
// preview endpoint, which serves one fixed period and ignores any date range.
type RawQuery struct {
	Dataset  RawDataset
	Exchange Exchange
	Symbol   string
	Preview  bool
}

// RawListing is every file overlapping a range, one per period, in period
// order, plus the periods in the range the API holds no file for.
type RawListing struct {
	Files          []RawFileInfo
	MissingPeriods []string
}

// RawDownload is one listed file and where it lands on disk.
type RawDownload struct {
	File RawFileInfo
	Path string
	// Present is set when the file is already on disk with the listed size.
	Present bool
}

type rawWindow struct {
	start, end time.Time
}

// rawWindows splits an inclusive date range into windows the API accepts.
func rawWindows(start, end time.Time) ([]rawWindow, error) {
	if end.Before(start) {
		return nil, errors.New("end date must be on or after start date")
	}

	var windows []rawWindow
	for cursor := start; !cursor.After(end); {
		last := cursor.AddDate(0, 0, RawMaxRangeDays-1)
		if last.After(end) {
			last = end
		}
		windows = append(windows, rawWindow{start: cursor, end: last})
		cursor = last.AddDate(0, 0, 1)
	}
	return windows, nil
}

type rawPeriod struct {
	first time.Time
	daily bool
}

// parseRawPeriod reads a "YYYY-MM" (monthly) or "YYYY-MM-DD" (daily) period.
func parseRawPeriod(period string) (rawPeriod, error) {
	if day, err := time.Parse(rawDateLayout, period); err == nil {
		return rawPeriod{first: day, daily: true}, nil
	}
	if month, err := time.Parse(rawMonthLayout, period); err == nil {
		return rawPeriod{first: month}, nil
	}
	return rawPeriod{}, fmt.Errorf("unrecognised raw period %q (want YYYY-MM or YYYY-MM-DD)", period)
}

// last is the final day the period covers.
func (p rawPeriod) last() time.Time {
	if p.daily {
		return p.first
	}
	return p.first.AddDate(0, 1, -1)
}

// RawFilePath is where one raw file is written: the bucket's Hive layout,
// {outputDir}/{dataset}/exchange={exchange}/symbol={symbol}/year=YYYY/month=MM
// [/day=DD]/data.parquet.
func RawFilePath(outputDir string, dataset RawDataset, exchange Exchange, symbol, period string) (string, error) {
	p, err := parseRawPeriod(period)
	if err != nil {
		return "", err
	}
	if rawEscapeColon {
		symbol = strings.ReplaceAll(symbol, ":", "%3A")
	}

	parts := []string{
		outputDir,
		string(dataset),
		"exchange=" + string(exchange),
		"symbol=" + symbol,
		fmt.Sprintf("year=%d", p.first.Year()),
		fmt.Sprintf("month=%02d", int(p.first.Month())),
	}
	if p.daily {
		parts = append(parts, fmt.Sprintf("day=%02d", p.first.Day()))
	}
	return filepath.Join(append(parts, "data.parquet")...), nil
}

// FetchRawFiles lists every file overlapping [start, end] (inclusive days,
// UTC). Ranges over RawMaxRangeDays are split into several requests, and a
// monthly file listed by two of them is kept once.
func (c *AperiodicClient) FetchRawFiles(q RawQuery, start, end time.Time) (*RawListing, error) {
	if q.Preview {
		resp, err := c.fetchRawPage(q, nil)
		if err != nil {
			return nil, err
		}
		return newRawListing(resp.Files, resp.MissingPeriods), nil
	}

	windows, err := rawWindows(start, end)
	if err != nil {
		return nil, err
	}

	var files []RawFileInfo
	var missing []string
	for _, w := range windows {
		resp, err := c.fetchRawPage(q, &w)
		if err != nil {
			return nil, err
		}
		files = append(files, resp.Files...)
		missing = append(missing, resp.MissingPeriods...)
	}
	return newRawListing(files, missing), nil
}

// newRawListing keeps one file per period (the last listed, whose URL is the
// freshest) and sorts both lists; the two period formats sort chronologically
// as plain strings.
func newRawListing(files []RawFileInfo, missing []string) *RawListing {
	byPeriod := make(map[string]RawFileInfo, len(files))
	for _, f := range files {
		byPeriod[f.Period] = f
	}

	listing := &RawListing{Files: make([]RawFileInfo, 0, len(byPeriod))}
	for _, f := range byPeriod {
		listing.Files = append(listing.Files, f)
	}
	slices.SortFunc(listing.Files, func(a, b RawFileInfo) int {
		return strings.Compare(a.Period, b.Period)
	})

	for _, p := range slices.Compact(slices.Sorted(slices.Values(missing))) {
		if _, found := byPeriod[p]; !found {
			listing.MissingPeriods = append(listing.MissingPeriods, p)
		}
	}
	return listing
}

func (c *AperiodicClient) fetchRawPage(q RawQuery, window *rawWindow) (*RawFilesResponse, error) {
	dataPath := fmt.Sprintf("%s/data/raw/%s", c.BaseURL, q.Dataset)
	if q.Preview {
		dataPath = fmt.Sprintf("%s/data/raw/preview/%s", c.BaseURL, q.Dataset)
	}

	u, err := url.Parse(dataPath)
	if err != nil {
		return nil, err
	}

	query := u.Query()
	query.Set("exchange", string(q.Exchange))
	query.Set("symbol", q.Symbol)
	if window != nil {
		query.Set("start_date", window.start.Format(rawDateLayout))
		query.Set("end_date", window.end.Format(rawDateLayout))
	}
	u.RawQuery = query.Encode()

	var resp RawFilesResponse
	if err := c.getJSON(u, c.getHeaders(), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *AperiodicClient) getJSON(u *url.URL, header http.Header, out any) error {
	req := &http.Request{
		Method: http.MethodGet,
		URL:    u,
		Header: header,
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := c.handleAPIError(resp); err != nil {
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// refreshRawFile re-lists a single period to get a fresh presigned URL.
func (c *AperiodicClient) refreshRawFile(q RawQuery, period string) (RawFileInfo, error) {
	p, err := parseRawPeriod(period)
	if err != nil {
		return RawFileInfo{}, err
	}

	listing, err := c.FetchRawFiles(q, p.first, p.last())
	if err != nil {
		return RawFileInfo{}, err
	}
	for _, f := range listing.Files {
		if f.Period == period {
			return f, nil
		}
	}
	return RawFileInfo{}, fmt.Errorf("%s is no longer listed by the API", period)
}

// PlanRawDownloads resolves every file's path under outputDir and marks the
// ones already on disk with the listed size, unless overwrite is set.
func PlanRawDownloads(outputDir string, q RawQuery, files []RawFileInfo, overwrite bool) ([]RawDownload, error) {
	plan := make([]RawDownload, len(files))
	for i, f := range files {
		path, err := RawFilePath(outputDir, q.Dataset, q.Exchange, q.Symbol, f.Period)
		if err != nil {
			return nil, err
		}
		plan[i] = RawDownload{File: f, Path: path}
		if overwrite {
			continue
		}
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() == f.Size {
			plan[i].Present = true
		}
	}
	return plan, nil
}

// DownloadRawFiles streams each file to its path, at most maxConcurrent at a
// time. A URL refused with a 403 (typically expired) is re-requested once.
// onDone, if set, is called from the downloading goroutine as each file lands,
// so it must be safe for concurrent use. Files that finished before an error
// stay on disk, so running the same download again resumes it.
func (c *AperiodicClient) DownloadRawFiles(q RawQuery, downloads []RawDownload, maxConcurrent int, onDone func(RawDownload)) error {
	return runConcurrently(len(downloads), maxConcurrent, func(i int) error {
		d := downloads[i]
		if err := c.downloadRawFile(q, d); err != nil {
			return fmt.Errorf("failed to download %s: %w", d.File.Period, err)
		}
		if onDone != nil {
			onDone(d)
		}
		return nil
	})
}

func (c *AperiodicClient) downloadRawFile(q RawQuery, d RawDownload) error {
	err := c.streamRawFile(d.File.URL, d.Path)
	if !errors.Is(err, errURLRefused) {
		return err
	}

	fresh, err := c.refreshRawFile(q, d.File.Period)
	if err != nil {
		return fmt.Errorf("refreshing an expired URL: %w", err)
	}
	return c.streamRawFile(fresh.URL, d.Path)
}

// streamRawFile downloads fileURL to path via a ".part" file renamed into place
// once complete, so a glob for data.parquet never matches a half-written file.
// Transport errors and bad statuses are retried with exponential backoff; a
// 403 is returned at once as errURLRefused.
func (c *AperiodicClient) streamRawFile(fileURL, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	partial := path + ".part"

	var lastErr error
	for attempt := 0; attempt <= rawMaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(rawRetryBackoff << (attempt - 1))
		}

		retryable, err := c.streamRawOnce(fileURL, partial)
		if err == nil {
			return os.Rename(partial, path)
		}
		_ = os.Remove(partial)
		if !retryable {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// streamRawOnce makes a single attempt, like downloadOnce, but without the
// client's overall timeout: the transfer is cancelled only once it has
// received nothing for rawStallTimeout.
func (c *AperiodicClient) streamRawOnce(fileURL, destPath string) (retryable bool, err error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stall := time.AfterFunc(rawStallTimeout, cancel)
	defer stall.Stop()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return false, err
	}

	client := *c.HTTPClient
	client.Timeout = 0

	resp, err := client.Do(req)
	if err != nil {
		return true, stalledOr(ctx, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return false, errURLRefused
	}
	if resp.StatusCode != http.StatusOK {
		return true, fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return false, fmt.Errorf("failed to create file: %w", err)
	}

	_, copyErr := io.Copy(out, &stallReader{r: resp.Body, timer: stall})
	closeErr := out.Close()
	if copyErr != nil {
		return true, stalledOr(ctx, copyErr)
	}
	return true, closeErr
}

func stalledOr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("no data received for %s", rawStallTimeout)
	}
	return err
}

// stallReader pushes the stall deadline back every time data arrives.
type stallReader struct {
	r     io.Reader
	timer *time.Timer
}

func (s *stallReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.timer.Reset(rawStallTimeout)
	}
	return n, err
}

// GetRawCoverage returns the raw datasets and every symbol's coverage. The
// endpoint is public, so no API key is sent.
func (c *AperiodicClient) GetRawCoverage() (*RawCoverageResponse, error) {
	u, err := url.Parse(fmt.Sprintf("%s/metadata/raw", c.BaseURL))
	if err != nil {
		return nil, err
	}

	header := c.getHeaders()
	header.Del("X-API-KEY")

	var resp RawCoverageResponse
	if err := c.getJSON(u, header, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
