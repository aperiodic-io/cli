package aperiodic

import (
	"flag"
	"fmt"
	"io"
	"os"
)

type CLI struct {
	Stdout io.Writer
	Stderr io.Writer
	Env    func(string) string
}

func NewCLI() *CLI {
	return &CLI{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Env:    os.Getenv,
	}
}

func (c *CLI) Run(args []string) int {
	if len(args) == 0 {
		c.printUsage()
		return 0
	}

	cmd := args[0]
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		c.printUsage()
		return 0
	}

	apiKey := c.Env("APERIODIC_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(c.Stderr, "Error: APERIODIC_API_KEY environment variable not set")
		return 1
	}

	client := NewAperiodicClient(apiKey)

	fs := flag.NewFlagSet("aperiodic", flag.ContinueOnError)
	fs.SetOutput(c.Stderr)

	exchangeFlag := fs.String("exchange", "binance-futures", "Exchange name")
	symbolFlag := fs.String("symbol", "", "Trading pair symbol")
	intervalFlag := fs.String("interval", "1h", "Aggregation interval")
	startDateFlag := fs.String("start-date", "", "Start date (YYYY-MM-DD)")
	endDateFlag := fs.String("end-date", "", "End date (YYYY-MM-DD)")
	maxConcurrentFlag := fs.Int("max-concurrent", 10, "Maximum concurrent downloads")
	timestampFlag := fs.String("timestamp", "exchange", "Timestamp source (exchange, true)")
	outputDirFlag := fs.String("output-dir", "", "Output directory for Parquet files (mandatory)")

	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	if cmd == "symbols" {
		return c.handleSymbols(client, *exchangeFlag)
	}

	if *outputDirFlag == "" {
		fmt.Fprintln(c.Stderr, "Error: --output-dir is mandatory")
		return 1
	}

	return c.handleData(client, cmd, *timestampFlag, *intervalFlag, *exchangeFlag, *symbolFlag, *startDateFlag, *endDateFlag, *maxConcurrentFlag, *outputDirFlag)
}

func (c *CLI) printUsage() {
	fmt.Fprintln(c.Stdout, "Aperiodic CLI Client")
	fmt.Fprintln(c.Stdout)
	fmt.Fprintln(c.Stdout, "Usage:")
	fmt.Fprintln(c.Stdout, "  aperiodic <metric> [flags]")
	fmt.Fprintln(c.Stdout, "  aperiodic symbols [flags]")
	fmt.Fprintln(c.Stdout)
	fmt.Fprintln(c.Stdout, "Metrics:")
	fmt.Fprintln(c.Stdout, "  ohlcv             OHLCV (open/high/low/close/volume)")
	fmt.Fprintln(c.Stdout, "  vtwap             Volume/time-weighted average price")
	fmt.Fprintln(c.Stdout, "  flow              Buy/sell trade flow")
	fmt.Fprintln(c.Stdout, "  trade_size        Trade size distribution")
	fmt.Fprintln(c.Stdout, "  impact            Price impact")
	fmt.Fprintln(c.Stdout, "  range             Price range")
	fmt.Fprintln(c.Stdout, "  updownticks       Up/down tick count")
	fmt.Fprintln(c.Stdout, "  run_structure     Run structure")
	fmt.Fprintln(c.Stdout, "  returns           Returns")
	fmt.Fprintln(c.Stdout, "  slippage          Slippage")
	fmt.Fprintln(c.Stdout, "  l1_price          L1 best bid/ask price")
	fmt.Fprintln(c.Stdout, "  l1_imbalance      L1 order book imbalance")
	fmt.Fprintln(c.Stdout, "  l1_liquidity      L1 liquidity")
	fmt.Fprintln(c.Stdout, "  l2_imbalance      L2 order book imbalance")
	fmt.Fprintln(c.Stdout, "  l2_liquidity      L2 liquidity")
	fmt.Fprintln(c.Stdout, "  basis             Basis (spot vs. perp spread)")
	fmt.Fprintln(c.Stdout, "  funding           Funding rates")
	fmt.Fprintln(c.Stdout, "  open_interest     Open interest")
	fmt.Fprintln(c.Stdout, "  derivative_price  Derivative price")
	fmt.Fprintln(c.Stdout)
	fmt.Fprintln(c.Stdout, "Commands:")
	fmt.Fprintln(c.Stdout, "  symbols  List available symbols for an exchange")
	fmt.Fprintln(c.Stdout, "  help     Show this help")
	fmt.Fprintln(c.Stdout)
	fmt.Fprintln(c.Stdout, "Environment:")
	fmt.Fprintln(c.Stdout, "  APERIODIC_API_KEY  Aperiodic API key (required)")
	fmt.Fprintln(c.Stdout)
	fmt.Fprintln(c.Stdout, "Flags:")
	fmt.Fprintln(c.Stdout, "  -end-date string")
	fmt.Fprintln(c.Stdout, "        End date (YYYY-MM-DD)")
	fmt.Fprintln(c.Stdout, "  -exchange string")
	fmt.Fprintln(c.Stdout, "        Exchange name (default \"binance-futures\")")
	fmt.Fprintln(c.Stdout, "  -interval string")
	fmt.Fprintln(c.Stdout, "        Aggregation interval (default \"1h\")")
	fmt.Fprintln(c.Stdout, "  -max-concurrent int")
	fmt.Fprintln(c.Stdout, "        Maximum concurrent downloads (default 10)")
	fmt.Fprintln(c.Stdout, "  -output-dir string")
	fmt.Fprintln(c.Stdout, "        Output directory for Parquet files (mandatory)")
	fmt.Fprintln(c.Stdout, "  -start-date string")
	fmt.Fprintln(c.Stdout, "        Start date (YYYY-MM-DD)")
	fmt.Fprintln(c.Stdout, "  -symbol string")
	fmt.Fprintln(c.Stdout, "        Trading pair symbol")
	fmt.Fprintln(c.Stdout, "  -timestamp string")
	fmt.Fprintln(c.Stdout, "        Timestamp source (exchange, true) (default \"exchange\")")
}

func (c *CLI) handleSymbols(client *AperiodicClient, exchange string) int {
	symbols, err := client.GetSymbols(exchange)
	if err != nil {
		fmt.Fprintf(c.Stderr, "Error fetching symbols: %v\n", err)
		return 1
	}

	for _, s := range symbols {
		fmt.Fprintln(c.Stdout, s)
	}

	return 0
}

func (c *CLI) handleData(client *AperiodicClient, metric, timestamp, interval, exchange, symbol, startDate, endDate string, maxConcurrent int, outputDir string) int {
	if symbol == "" {
		fmt.Fprintln(c.Stderr, "Error: --symbol is required")
		return 1
	}
	if startDate == "" || endDate == "" {
		fmt.Fprintln(c.Stderr, "Error: --start-date and --end-date are required")
		return 1
	}

	resp, err := client.FetchPresignedUrls(metric, TimestampType(timestamp), Interval(interval), exchange, symbol, startDate, endDate)
	if err != nil {
		fmt.Fprintf(c.Stderr, "Error fetching file URLs: %v\n", err)
		return 1
	}

	if len(resp.Files) == 0 {
		fmt.Fprintln(c.Stdout, "No data found for the given criteria")
		return 0
	}

	fmt.Fprintf(c.Stdout, "Downloading %d Parquet files to %s...\n", len(resp.Files), outputDir)

	results, err := client.DownloadFilesConcurrently(resp.Files, maxConcurrent, outputDir)
	if err != nil {
		fmt.Fprintf(c.Stderr, "Error downloading files: %v\n", err)
		return 1
	}

	fmt.Fprintf(c.Stdout, "Successfully downloaded %d files:\n", len(results))
	for _, res := range results {
		fmt.Fprintf(c.Stdout, " - %s\n", res.Filename)
	}

	return 0
}
