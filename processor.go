package aperiodic

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type DownloadedFile struct {
	Year     int
	Month    int
	Day      *int
	Filename string
}

// parquetFilename is the on-disk name for one downloaded object.
//
// Both parts are zero-padded so a directory listing sorts chronologically. That
// is a choice about local filenames, not a mirror of the R2 key, which writes
// the month unpadded — do not "align" the two.
func parquetFilename(f FileInfo) string {
	if f.Day == nil {
		return fmt.Sprintf("%d-%02d.parquet", f.Year, f.Month)
	}
	return fmt.Sprintf("%d-%02d-%02d.parquet", f.Year, f.Month, *f.Day)
}

// resolveFilenames names every file and rejects the set if two share a name.
//
// Load-bearing, not defensive tidiness: before FileInfo carried a day, every
// daily file in a month resolved to the same name, so a month of downloads
// raced to truncate and rewrite a single path — and the CLI still exited 0
// reporting one success per file. Colliding names must fail the run rather than
// let whichever goroutine finishes last decide the contents.
func resolveFilenames(files []FileInfo) ([]string, error) {
	filenames := make([]string, len(files))
	seen := make(map[string]int, len(files))

	for i, f := range files {
		name := parquetFilename(f)
		if first, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf(
				"refusing to download: files %d and %d both resolve to %s",
				first, i, name,
			)
		}
		seen[name] = i
		filenames[i] = name
	}

	return filenames, nil
}

func (c *AperiodicClient) DownloadFilesConcurrently(files []FileInfo, maxConcurrent int, outputDir string) ([]DownloadedFile, error) {
	if len(files) == 0 {
		return nil, nil
	}

	filenames, err := resolveFilenames(files)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	results := make([]DownloadedFile, len(files))
	errs := make([]error, len(files))
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, maxConcurrent)

	for i, file := range files {
		wg.Add(1)
		go func(i int, f FileInfo) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			filename := filenames[i]
			destPath := filepath.Join(outputDir, filename)

			if err := c.downloadToFile(f.URL, destPath, 3); err != nil {
				errs[i] = fmt.Errorf("failed to download %s: %w", filename, err)
				return
			}
			results[i] = DownloadedFile{
				Year:     f.Year,
				Month:    f.Month,
				Day:      f.Day,
				Filename: filename,
			}
		}(i, file)
	}

	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}

	return results, nil
}

func (c *AperiodicClient) downloadToFile(url, destPath string, maxRetries int) error {
	var lastErr error
	for i := 0; i <= maxRetries; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * time.Second) // Simple backoff
		}

		retryable, err := c.downloadOnce(url, destPath)
		if err == nil {
			return nil
		}
		if !retryable {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// downloadOnce makes a single attempt, closing the response body and the
// destination file before it returns. It reports whether the failure is worth
// another attempt: transport and HTTP errors are, a local filesystem error is
// not.
//
// Living outside downloadToFile's loop is the point. A `defer` in a loop body
// runs only when the whole function returns, so every attempt's handles stayed
// open for the length of the retry sequence — leaving a failed attempt free to
// flush into a path a later attempt had already truncated.
func (c *AperiodicClient) downloadOnce(url, destPath string) (retryable bool, err error) {
	resp, err := c.HTTPClient.Get(url)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return true, fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return false, fmt.Errorf("failed to create file: %w", err)
	}

	_, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		return true, copyErr
	}
	return true, closeErr
}
