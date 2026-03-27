package main

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
	Filename string
}

func (c *AperiodicClient) DownloadFilesConcurrently(files []FileInfo, maxConcurrent int, outputDir string) ([]DownloadedFile, error) {
	if len(files) == 0 {
		return nil, nil
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

			filename := fmt.Sprintf("%d-%02d.parquet", f.Year, f.Month)
			destPath := filepath.Join(outputDir, filename)

			if err := c.downloadToFile(f.URL, destPath, 3); err != nil {
				errs[i] = fmt.Errorf("failed to download %d-%02d: %w", f.Year, f.Month, err)
				return
			}
			results[i] = DownloadedFile{
				Year:     f.Year,
				Month:    f.Month,
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

		resp, err := c.HTTPClient.Get(url)
		if err != nil {
			lastErr = err
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("bad status: %s", resp.Status)
			continue
		}

		out, err := os.Create(destPath)
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}
		defer out.Close()

		_, err = io.Copy(out, resp.Body)
		if err != nil {
			lastErr = err
			continue
		}

		return nil
	}
	return lastErr
}
