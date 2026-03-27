package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/xitongsys/parquet-go-source/buffer"
	"github.com/xitongsys/parquet-go/reader"
)

type DownloadedFile struct {
	Year  int
	Month int
	Data  []byte
}

func (c *AperiodicClient) DownloadFilesConcurrently(files []FileInfo, maxConcurrent int) ([]DownloadedFile, error) {
	if len(files) == 0 {
		return nil, nil
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

			data, err := c.downloadWithRetry(f.URL, 3)
			if err != nil {
				errs[i] = fmt.Errorf("failed to download %d-%02d: %w", f.Year, f.Month, err)
				return
			}
			results[i] = DownloadedFile{
				Year:  f.Year,
				Month: f.Month,
				Data:  data,
			}
		}(i, file)
	}

	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}

	// Sort by year and month
	sort.Slice(results, func(i, j int) bool {
		if results[i].Year != results[j].Year {
			return results[i].Year < results[j].Year
		}
		return results[i].Month < results[j].Month
	})

	return results, nil
}

func (c *AperiodicClient) downloadWithRetry(url string, maxRetries int) ([]byte, error) {
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

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			lastErr = err
			continue
		}

		return data, nil
	}
	return nil, lastErr
}

func ProcessParquetFiles(downloaded []DownloadedFile, startDate, endDate time.Time) ([]map[string]interface{}, error) {
	var allRows []map[string]interface{}

	for _, df := range downloaded {
		fr := buffer.NewBufferFileFromBytes(df.Data)

		pr, err := reader.NewParquetReader(fr, nil, 4)
		if err != nil {
			fr.Close()
			return nil, fmt.Errorf("failed to create parquet reader: %w", err)
		}

		num := int(pr.GetNumRows())
		// Read all rows
		rows, err := pr.ReadByNumber(num)
		if err != nil {
			pr.ReadStop()
			fr.Close()
			return nil, fmt.Errorf("failed to read rows: %w", err)
		}

		for _, rowObj := range rows {
			rowJSON, err := json.Marshal(rowObj)
			if err != nil {
				continue
			}

			var row map[string]interface{}
			if err := json.Unmarshal(rowJSON, &row); err != nil {
				continue
			}

			// Filtering by timestamp
			var ts int64
			foundTS := false
			if tsv, ok := row["Timestamp"]; ok { // Note the uppercase if it was a struct field in test
				foundTS = true
				switch v := tsv.(type) {
				case float64:
					ts = int64(v)
				case int64:
					ts = v
				}
			} else if tsv, ok := row["timestamp"]; ok {
				foundTS = true
				switch v := tsv.(type) {
				case float64:
					ts = int64(v)
				case int64:
					ts = v
				}
			}

			if foundTS && ts > 0 {
				rowTime := time.UnixMilli(ts).UTC()
				if rowTime.Before(startDate) || rowTime.After(endDate) {
					continue
				}
				row["time"] = rowTime.Format(time.RFC3339)
			}

			allRows = append(allRows, row)
		}
		pr.ReadStop()
		fr.Close()
	}

	// Sorting by timestamp
	sort.Slice(allRows, func(i, j int) bool {
		var tsI, tsJ int64
		for _, key := range []string{"Timestamp", "timestamp"} {
			if tsv, ok := allRows[i][key]; ok {
				if v, ok := tsv.(float64); ok {
					tsI = int64(v)
				} else if v, ok := tsv.(int64); ok {
					tsI = v
				}
			}
			if tsv, ok := allRows[j][key]; ok {
				if v, ok := tsv.(float64); ok {
					tsJ = int64(v)
				} else if v, ok := tsv.(int64); ok {
					tsJ = v
				}
			}
		}
		return tsI < tsJ
	})

	return allRows, nil
}
