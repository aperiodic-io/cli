package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aperiodic-io/client/go-cli/downloader"
)

func main() {
	cfg := downloader.Config{}

	flag.StringVar(&cfg.APIKey, "api-key", "", "Aperiodic API key")
	flag.StringVar(&cfg.Bucket, "bucket", "ohlcv", "Dataset bucket/metric (e.g. ohlcv, flow, l1_price)")
	flag.StringVar(&cfg.Timestamp, "timestamp", "true", "Timestamp type: exchange|true")
	flag.StringVar(&cfg.Interval, "interval", "1h", "Interval: 1m|5m|15m|30m|1h|4h|1d")
	flag.StringVar(&cfg.Exchange, "exchange", "", "Exchange: binance-futures|binance|okx-perps")
	flag.StringVar(&cfg.Symbol, "symbol", "", "Atlas symbol, e.g. perpetual-BTC-USDT:USDT")
	flag.StringVar(&cfg.StartDate, "start-date", "", "Start date (YYYY-MM-DD)")
	flag.StringVar(&cfg.EndDate, "end-date", "", "End date (YYYY-MM-DD)")
	flag.StringVar(&cfg.BaseURL, "base-url", downloader.DefaultBaseURL, "Aperiodic API base URL")
	flag.StringVar(&cfg.OutputDir, "out", "downloads", "Output directory for parquet files")
	flag.IntVar(&cfg.MaxConcurrent, "max-concurrent", 5, "Number of concurrent file downloads")
	flag.IntVar(&cfg.MaxRetries, "max-retries", 3, "Retry attempts per file")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	client := &http.Client{Timeout: 90 * time.Second}
	if err := downloader.Download(ctx, client, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
