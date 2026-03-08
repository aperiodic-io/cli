package downloader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const DefaultBaseURL = "https://aperiodic.io/api/v1"

type Config struct {
	APIKey        string
	Bucket        string
	Timestamp     string
	Interval      string
	Exchange      string
	Symbol        string
	StartDate     string
	EndDate       string
	BaseURL       string
	OutputDir     string
	MaxConcurrent int
	MaxRetries    int
}

type fileInfo struct {
	Year  int    `json:"year"`
	Month int    `json:"month"`
	URL   string `json:"url"`
}

type aggregateDataResponse struct {
	Files []fileInfo `json:"files"`
}

func (c *Config) validate() error {
	if c.APIKey == "" || c.Bucket == "" || c.Timestamp == "" || c.Interval == "" || c.Exchange == "" || c.Symbol == "" || c.StartDate == "" || c.EndDate == "" {
		return errors.New("api-key, bucket, timestamp, interval, exchange, symbol, start-date, and end-date are required")
	}
	if c.BaseURL == "" {
		c.BaseURL = DefaultBaseURL
	}
	if c.OutputDir == "" {
		c.OutputDir = "downloads"
	}
	if c.MaxConcurrent < 1 {
		c.MaxConcurrent = 5
	}
	if c.MaxRetries < 0 {
		c.MaxRetries = 3
	}
	return nil
}

func Download(ctx context.Context, client *http.Client, cfg Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	files, err := fetchPresignedURLs(ctx, client, cfg)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Println("no files returned by API")
		return nil
	}

	jobs := make(chan fileInfo)
	errCh := make(chan error, len(files))
	var wg sync.WaitGroup

	for i := 0; i < cfg.MaxConcurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				if err := downloadOne(ctx, client, f, cfg); err != nil {
					errCh <- err
				}
			}
		}()
	}

	for _, f := range files {
		jobs <- f
	}
	close(jobs)
	wg.Wait()
	close(errCh)

	var allErr error
	for err := range errCh {
		allErr = errors.Join(allErr, err)
	}
	return allErr
}

func fetchPresignedURLs(ctx context.Context, client *http.Client, cfg Config) ([]fileInfo, error) {
	u, err := url.Parse(fmt.Sprintf("%s/data/%s", cfg.BaseURL, cfg.Bucket))
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	q := u.Query()
	q.Set("timestamp", cfg.Timestamp)
	q.Set("interval", cfg.Interval)
	q.Set("exchange", cfg.Exchange)
	q.Set("symbol", cfg.Symbol)
	q.Set("start_date", cfg.StartDate)
	q.Set("end_date", cfg.EndDate)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("X-API-KEY", cfg.APIKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var parsed aggregateDataResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode API response: %w", err)
	}
	return parsed.Files, nil
}

func downloadOne(ctx context.Context, client *http.Client, f fileInfo, cfg Config) error {
	path := filepath.Join(cfg.OutputDir, fmt.Sprintf("%s_%s_%s_%04d-%02d.parquet", cfg.Exchange, cfg.Symbol, cfg.Bucket, f.Year, f.Month))

	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
		if err != nil {
			return fmt.Errorf("build download request for %s: %w", path, err)
		}

		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			func() {
				defer resp.Body.Close()
				out, createErr := os.Create(path)
				if createErr != nil {
					err = createErr
					return
				}
				defer out.Close()
				_, err = io.Copy(out, resp.Body)
			}()
			if err == nil {
				fmt.Printf("downloaded %s\n", path)
				return nil
			}
			lastErr = err
		} else {
			if resp != nil {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
				resp.Body.Close()
				lastErr = fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
			} else {
				lastErr = err
			}
		}

		if attempt < cfg.MaxRetries {
			jitter := time.Duration(rand.Intn(400)) * time.Millisecond
			time.Sleep((time.Duration(1<<attempt) * 500 * time.Millisecond) + jitter)
		}
	}

	return fmt.Errorf("failed %s after %d retries: %w", path, cfg.MaxRetries, lastErr)
}
