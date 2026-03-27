package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"
)

var (
	apiKeyFlag         *string
	exchangeFlag       *string
	symbolFlag         *string
	intervalFlag       *string
	startDateFlag      *string
	endDateFlag        *string
	formatFlag         *string
	maxConcurrentFlag  *int
	timestampFlag      *string
	metricFlag         *string
)

func init() {
	apiKeyFlag = flag.String("api-key", "", "Aperiodic API key")
	exchangeFlag = flag.String("exchange", "binance-futures", "Exchange name")
	symbolFlag = flag.String("symbol", "", "Trading pair symbol")
	intervalFlag = flag.String("interval", "1h", "Aggregation interval")
	startDateFlag = flag.String("start-date", "", "Start date (YYYY-MM-DD)")
	endDateFlag = flag.String("end-date", "", "End date (YYYY-MM-DD)")
	formatFlag = flag.String("format", "csv", "Output format (csv, json)")
	maxConcurrentFlag = flag.Int("max-concurrent", 10, "Maximum concurrent downloads")
	timestampFlag = flag.String("timestamp", "exchange", "Timestamp source (exchange, true)")
	metricFlag = flag.String("metric", "", "Specific metric to fetch")
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	cmd := os.Args[1]
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		printUsage()
		return
	}

	flag.CommandLine.Parse(os.Args[2:])

	apiKey := os.Getenv("APERIODIC_API_KEY")
	if *apiKeyFlag != "" {
		apiKey = *apiKeyFlag
	}

	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: API key is required (via --api-key or APERIODIC_API_KEY)")
		os.Exit(1)
	}

	client := NewAperiodicClient(apiKey)

	switch cmd {
	case "symbols":
		handleSymbols(client, *exchangeFlag)
	case "ohlcv", "vwap", "twap", "metrics", "derivative":
		handleData(client, cmd, *timestampFlag, *intervalFlag, *exchangeFlag, *symbolFlag, *startDateFlag, *endDateFlag, *maxConcurrentFlag, *formatFlag, *metricFlag)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Aperiodic CLI Client")
	fmt.Println("\nUsage:")
	fmt.Println("  aperiodic <command> [flags]")
	fmt.Println("\nCommands:")
	fmt.Println("  symbols     List available symbols for an exchange")
	fmt.Println("  ohlcv       Fetch OHLCV data")
	fmt.Println("  vwap        Fetch VWAP data")
	fmt.Println("  twap        Fetch TWAP data")
	fmt.Println("  metrics     Fetch trade/L1/L2 metrics (use --metric flag for specific metric)")
	fmt.Println("  derivative  Fetch derivative metrics (use --metric flag for specific metric)")
	fmt.Println("  help        Show this help")
	fmt.Println("\nFlags:")
	flag.PrintDefaults()
}

func handleSymbols(client *AperiodicClient, exchange string) {
	symbols, err := client.GetSymbols(exchange)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching symbols: %v\n", err)
		os.Exit(1)
	}

	for _, s := range symbols {
		fmt.Println(s)
	}
}

func handleData(client *AperiodicClient, cmd, timestamp, interval, exchange, symbol, startDate, endDate string, maxConcurrent int, format, metric string) {
	if symbol == "" {
		fmt.Fprintln(os.Stderr, "Error: --symbol is required")
		os.Exit(1)
	}
	if startDate == "" || endDate == "" {
		fmt.Fprintln(os.Stderr, "Error: --start-date and --end-date are required")
		os.Exit(1)
	}

	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing start-date: %v\n", err)
		os.Exit(1)
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing end-date: %v\n", err)
		os.Exit(1)
	}

	bucket := cmd
	if (cmd == "metrics" || cmd == "derivative") && metric != "" {
		bucket = metric
	} else if cmd == "ohlcv" {
		bucket = "ohlcv"
	} else if cmd == "vwap" || cmd == "twap" {
		bucket = "vtwap"
	}

	resp, err := client.FetchPresignedUrls(bucket, TimestampType(timestamp), Interval(interval), exchange, symbol, startDate, endDate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching file URLs: %v\n", err)
		os.Exit(1)
	}

	if len(resp.Files) == 0 {
		fmt.Fprintln(os.Stderr, "No data found for the given criteria")
		return
	}

	downloaded, err := client.DownloadFilesConcurrently(resp.Files, maxConcurrent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading files: %v\n", err)
		os.Exit(1)
	}

	rows, err := ProcessParquetFiles(downloaded, start, end)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error processing files: %v\n", err)
		os.Exit(1)
	}

	outputResults(rows, format)
}

func outputResults(rows []map[string]interface{}, format string) {
	if len(rows) == 0 {
		return
	}

	if format == "json" {
		json.NewEncoder(os.Stdout).Encode(rows)
		return
	}

	// Default to CSV
	w := csv.NewWriter(os.Stdout)
	defer w.Flush()

	// Header
	var keys []string
	for k := range rows[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	w.Write(keys)

	for _, row := range rows {
		line := make([]string, len(keys))
		for i, k := range keys {
			line[i] = fmt.Sprintf("%v", row[k])
		}
		w.Write(line)
	}
}
