package main

import (
	"testing"
	"time"

	"github.com/xitongsys/parquet-go-source/buffer"
	"github.com/xitongsys/parquet-go/writer"
)

type ParquetRow struct {
	Timestamp int64   `parquet:"name=timestamp, type=INT64"`
	Value     float64 `parquet:"name=value, type=DOUBLE"`
}

func createSampleParquet(rows []ParquetRow) []byte {
	fw := buffer.NewBufferFile()
	pw, _ := writer.NewParquetWriter(fw, new(ParquetRow), 4)

	for _, row := range rows {
		pw.Write(row)
	}
	pw.WriteStop()
	fw.Close()
	return fw.Bytes()
}

func TestProcessParquetFiles(t *testing.T) {
	rows1 := []ParquetRow{
		{Timestamp: 1704067200000, Value: 1.0}, // 2024-01-01
		{Timestamp: 1704153600000, Value: 2.0}, // 2024-01-02
	}
	rows2 := []ParquetRow{
		{Timestamp: 1704240000000, Value: 3.0}, // 2024-01-03
	}

	downloaded := []DownloadedFile{
		{Year: 2024, Month: 1, Data: createSampleParquet(rows1)},
		{Year: 2024, Month: 1, Data: createSampleParquet(rows2)},
	}

	startDate := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 2, 23, 59, 59, 0, time.UTC)

	results, err := ProcessParquetFiles(downloaded, startDate, endDate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d. Results: %v", len(results), results)
	}

	val, ok := results[0]["Value"].(float64)
	if !ok {
		val, ok = results[0]["value"].(float64)
	}
	if !ok || val != 2.0 {
		t.Errorf("expected value 2.0, got %v", results[0]["Value"])
	}
}

func TestProcessParquetFiles_Sorting(t *testing.T) {
	rows1 := []ParquetRow{
		{Timestamp: 1704153600000, Value: 2.0}, // 2024-01-02
	}
	rows2 := []ParquetRow{
		{Timestamp: 1704067200000, Value: 1.0}, // 2024-01-01
	}

	downloaded := []DownloadedFile{
		{Year: 2024, Month: 1, Data: createSampleParquet(rows1)},
		{Year: 2024, Month: 1, Data: createSampleParquet(rows2)},
	}

	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)

	results, err := ProcessParquetFiles(downloaded, startDate, endDate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	ts, ok := results[0]["Timestamp"].(float64)
	if !ok {
		ts, ok = results[0]["timestamp"].(float64)
	}
	if !ok || ts != 1704067200000 {
		t.Errorf("expected first result to be 2024-01-01, got %v", ts)
	}
}
