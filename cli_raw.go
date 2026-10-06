package aperiodic

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

var rawDatasetDescriptions = map[RawDataset]string{
	RawTrades:       "Every trade: id, taker side, price, amount",
	RawQuotes:       "Top of book: best bid/ask price and amount",
	RawMarkPrice:    "Mark price, one row per change",
	RawIndexPrice:   "Index price, one row per change",
	RawFundingRate:  "Funding rate",
	RawOpenInterest: "Open interest, one row per change",
}

type rawDownloadFlags struct {
	exchange      string
	symbol        string
	startDate     string
	endDate       string
	outputDir     string
	maxConcurrent int
	overwrite     bool
	preview       bool
}

func newRawDownloadFlagSet() (*flag.FlagSet, *rawDownloadFlags) {
	f := &rawDownloadFlags{}
	fs := flag.NewFlagSet("aperiodic raw", flag.ContinueOnError)
	fs.StringVar(&f.exchange, "exchange", string(ExchangeBinanceFutures), "Exchange name")
	fs.StringVar(&f.symbol, "symbol", "", "Trading pair symbol, e.g. perpetual-BTC-USDT:USDT")
	fs.StringVar(&f.startDate, "start-date", "", "First day, UTC (YYYY-MM-DD)")
	fs.StringVar(&f.endDate, "end-date", "", "Last day, inclusive (YYYY-MM-DD); ranges over 366 days are split for you")
	fs.StringVar(&f.outputDir, "output-dir", "", "Root folder for the Hive layout (mandatory)")
	fs.IntVar(&f.maxConcurrent, "max-concurrent", DefaultRawMaxConcurrent, "Maximum concurrent downloads")
	fs.BoolVar(&f.overwrite, "overwrite", false, "Download files again even if already present with the expected size")
	fs.BoolVar(&f.preview, "preview", false, "Fetch the free preview file (2025-06, each venue's BTC perpetual) with the shared demo key; dates are ignored")
	return fs, f
}

type rawCoverageFlags struct {
	dataset  string
	exchange string
	symbol   string
}

func newRawCoverageFlagSet() (*flag.FlagSet, *rawCoverageFlags) {
	f := &rawCoverageFlags{}
	fs := flag.NewFlagSet("aperiodic raw coverage", flag.ContinueOnError)
	fs.StringVar(&f.dataset, "dataset", "", "List the symbols of this raw dataset")
	fs.StringVar(&f.exchange, "exchange", "", "List the symbols on this exchange")
	fs.StringVar(&f.symbol, "symbol", "", "Show this symbol's coverage")
	return fs, f
}

func (c *CLI) runRaw(args []string) int {
	if len(args) == 0 || isHelpArg(args[0]) {
		c.printRawUsage()
		return 0
	}
	if args[0] == "coverage" {
		return c.runRawCoverage(args[1:])
	}
	return c.runRawDownload(args)
}

// parseRawArgs parses flags wherever they sit among the positional arguments;
// the flag package alone stops at the first positional one. On failure it
// returns the exit code, 0 when the user asked for help.
func (c *CLI) parseRawArgs(fs *flag.FlagSet, args []string) (positional []string, exitCode int, ok bool) {
	fs.SetOutput(c.Stderr)
	fs.Usage = func() {}

	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				c.printRawUsage()
				return nil, 0, false
			}
			fmt.Fprintln(c.Stderr, "Run 'aperiodic raw help' for usage.")
			return nil, 2, false
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, 0, true
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

func (c *CLI) runRawDownload(args []string) int {
	fs, f := newRawDownloadFlagSet()
	positional, exitCode, ok := c.parseRawArgs(fs, args)
	if !ok {
		return exitCode
	}

	switch {
	case len(positional) == 0:
		fmt.Fprintf(c.Stderr, "Error: a raw dataset is required (%s)\n", joinNames(RawDatasets))
		return 1
	case len(positional) > 1:
		fmt.Fprintf(c.Stderr, "Error: unexpected arguments: %s\n", strings.Join(positional[1:], " "))
		return 1
	}

	query := RawQuery{
		Dataset:  RawDataset(positional[0]),
		Exchange: Exchange(f.exchange),
		Symbol:   f.symbol,
		Preview:  f.preview,
	}
	if err := ValidateRawQuery(query.Dataset, query.Exchange); err != nil {
		fmt.Fprintf(c.Stderr, "Error: %v\n", err)
		return 1
	}
	if query.Symbol == "" {
		fmt.Fprintln(c.Stderr, "Error: --symbol is required")
		return 1
	}
	if f.outputDir == "" {
		fmt.Fprintln(c.Stderr, "Error: --output-dir is mandatory")
		return 1
	}
	if f.maxConcurrent < 1 {
		fmt.Fprintln(c.Stderr, "Error: --max-concurrent must be at least 1")
		return 1
	}

	var start, end time.Time
	if query.Preview {
		if f.startDate != "" || f.endDate != "" {
			fmt.Fprintln(c.Stderr, "Note: --preview ignores --start-date and --end-date; the preview is a fixed file")
		}
	} else {
		var err error
		if start, end, err = parseRawDateRange(f.startDate, f.endDate); err != nil {
			fmt.Fprintf(c.Stderr, "Error: %v\n", err)
			return 1
		}
	}

	apiKey, ok := c.resolveAPIKey(query.Preview)
	if !ok {
		return 1
	}
	client := NewAperiodicClient(apiKey)

	listing, err := client.FetchRawFiles(query, start, end)
	if err != nil {
		c.reportRawAPIError("Error fetching raw file URLs", err, query.Preview)
		return 1
	}

	if len(listing.Files) == 0 {
		c.printMissingPeriods(listing.MissingPeriods)
		fmt.Fprintln(c.Stdout, "No data found for the given criteria")
		return 0
	}

	plan, err := PlanRawDownloads(f.outputDir, query, listing.Files, f.overwrite)
	if err != nil {
		fmt.Fprintf(c.Stderr, "Error: %v\n", err)
		return 1
	}

	pending := slices.DeleteFunc(slices.Clone(plan), func(d RawDownload) bool { return d.Present })
	present := len(plan) - len(pending)

	fmt.Fprintf(c.Stdout, "Found %d %s files (%s) for %s on %s\n", len(plan), query.Dataset, formatBytes(totalRawBytes(plan)), query.Symbol, query.Exchange)
	c.printMissingPeriods(listing.MissingPeriods)
	if present > 0 {
		fmt.Fprintf(c.Stdout, "Skipping %d already present with the expected size (pass --overwrite to download them again)\n", present)
	}
	if len(pending) == 0 {
		fmt.Fprintf(c.Stdout, "Nothing to download: every file is already in %s\n", f.outputDir)
		return 0
	}

	fmt.Fprintf(c.Stdout, "Downloading %d files (%s) to %s...\n", len(pending), formatBytes(totalRawBytes(pending)), f.outputDir)

	var mu sync.Mutex
	done := 0
	err = client.DownloadRawFiles(query, pending, f.maxConcurrent, func(d RawDownload) {
		mu.Lock()
		defer mu.Unlock()
		done++
		fmt.Fprintf(c.Stdout, " [%d/%d] %s (%s)\n", done, len(pending), d.Path, formatBytes(d.File.Size))
	})
	if err != nil {
		fmt.Fprintf(c.Stderr, "Error downloading files: %v\n", err)
		if done > 0 {
			fmt.Fprintf(c.Stderr, "%d files finished and are kept; run the same command again to resume.\n", done)
		}
		return 1
	}

	fmt.Fprintf(c.Stdout, "Successfully downloaded %d files\n", len(pending))
	return 0
}

func (c *CLI) printMissingPeriods(periods []string) {
	if len(periods) > 0 {
		fmt.Fprintf(c.Stdout, "No file for %d period(s) in the range: %s\n", len(periods), summarizePeriods(periods))
	}
}

func parseRawDateRange(startDate, endDate string) (start, end time.Time, err error) {
	if startDate == "" || endDate == "" {
		return start, end, errors.New("--start-date and --end-date are required (or pass --preview)")
	}
	if start, err = time.Parse(rawDateLayout, startDate); err != nil {
		return start, end, fmt.Errorf("--start-date %q is not a YYYY-MM-DD date", startDate)
	}
	if end, err = time.Parse(rawDateLayout, endDate); err != nil {
		return start, end, fmt.Errorf("--end-date %q is not a YYYY-MM-DD date", endDate)
	}
	if end.Before(start) {
		return start, end, errors.New("--end-date must be on or after --start-date")
	}
	return start, end, nil
}

func (c *CLI) reportRawAPIError(prefix string, err error, preview bool) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		fmt.Fprintf(c.Stderr, "%s: %v\n", prefix, err)
		return
	}

	if apiErr.Code == "raw_not_in_plan" {
		fmt.Fprintf(c.Stderr, "Error: %s\n", apiErr.Message)
		fmt.Fprintf(c.Stderr, "Raw data needs the Prime + Raw plan: %s\n", cmp.Or(apiErr.UpgradeURL, RawUpgradeURL))
		fmt.Fprintln(c.Stderr, "To try it without an account, add --preview for the free 2025-06 file of each venue's BTC perpetual.")
		return
	}

	fmt.Fprintf(c.Stderr, "%s: %v\n", prefix, err)
	for _, detail := range apiErr.Details {
		fmt.Fprintf(c.Stderr, "  - %s\n", detail)
	}
	if preview && apiErr.StatusCode == http.StatusBadRequest {
		fmt.Fprintln(c.Stderr, "The raw preview serves only the 2025-06 file of each venue's BTC perpetual; 'aperiodic raw coverage' lists the preview symbols.")
	}
}

func (c *CLI) runRawCoverage(args []string) int {
	fs, f := newRawCoverageFlagSet()
	positional, exitCode, ok := c.parseRawArgs(fs, args)
	if !ok {
		return exitCode
	}
	if len(positional) > 0 {
		fmt.Fprintf(c.Stderr, "Error: unexpected arguments: %s (filter with --dataset, --exchange or --symbol)\n", strings.Join(positional, " "))
		return 1
	}

	if err := validateRawCoverageFilters(RawDataset(f.dataset), Exchange(f.exchange)); err != nil {
		fmt.Fprintf(c.Stderr, "Error: %v\n", err)
		return 1
	}

	// Coverage is public; the key, if any, is not sent.
	coverage, err := NewAperiodicClient("").GetRawCoverage()
	if err != nil {
		c.reportRawAPIError("Error fetching raw coverage", err, false)
		return 1
	}

	if f.dataset == "" && f.exchange == "" && f.symbol == "" {
		c.printRawCoverageSummary(coverage)
		return 0
	}
	return c.printRawCoverageSeries(coverage, f)
}

// validateRawCoverageFilters checks whichever of the two filters are set.
func validateRawCoverageFilters(dataset RawDataset, exchange Exchange) error {
	switch {
	case dataset != "" && exchange != "":
		return ValidateRawQuery(dataset, exchange)
	case dataset != "":
		return validateRawDataset(dataset)
	case exchange != "":
		return validateExchange(exchange)
	}
	return nil
}

func (c *CLI) printRawCoverageSummary(coverage *RawCoverageResponse) {
	if coverage.Coverage.GeneratedAt != "" {
		fmt.Fprintf(c.Stdout, "Raw coverage as of %s\n\n", coverage.Coverage.GeneratedAt)
	}

	w := tabwriter.NewWriter(c.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DATASET\tEXCHANGE\tSYMBOLS\tFIRST\tLAST\tSIZE")
	for _, row := range coverage.Summary {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n", row.Dataset, row.Exchange, row.Symbols, row.First, row.Last, formatBytes(row.Bytes))
	}
	w.Flush()

	if len(coverage.Preview.Symbols) > 0 {
		previews := make([]string, 0, len(coverage.Preview.Symbols))
		for _, exchange := range orderedKeys(coverage.Preview.Symbols, toStrings(Exchanges)) {
			previews = append(previews, exchange+" "+coverage.Preview.Symbols[exchange])
		}
		fmt.Fprintf(c.Stdout, "\nFree preview (--preview, no account): %s of %s\n", coverage.Preview.Period, strings.Join(previews, ", "))
	}
	fmt.Fprintln(c.Stdout, "Filter with --dataset, --exchange or --symbol to list symbols.")
}

func (c *CLI) printRawCoverageSeries(coverage *RawCoverageResponse, f *rawCoverageFlags) int {
	w := tabwriter.NewWriter(c.Stdout, 0, 0, 2, ' ', 0)
	rows := 0

	byDataset := coverage.Coverage.Datasets
	for _, dataset := range orderedKeys(byDataset, toStrings(RawDatasets)) {
		if f.dataset != "" && dataset != f.dataset {
			continue
		}
		byExchange := byDataset[dataset]
		for _, exchange := range orderedKeys(byExchange, toStrings(Exchanges)) {
			if f.exchange != "" && exchange != f.exchange {
				continue
			}
			bySymbol := byExchange[exchange]
			for _, symbol := range orderedKeys(bySymbol, nil) {
				if f.symbol != "" && symbol != f.symbol {
					continue
				}
				if rows == 0 {
					fmt.Fprintln(w, "DATASET\tEXCHANGE\tSYMBOL\tFIRST\tLAST\tDAYS\tMISSING\tSIZE")
				}
				s := bySymbol[symbol]
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n", dataset, exchange, symbol, s.First, s.Last, s.Days, len(s.Missing), formatBytes(s.Bytes))
				rows++
			}
		}
	}
	w.Flush()

	if rows == 0 {
		fmt.Fprintln(c.Stdout, "No raw coverage matches the given filters")
	}
	return 0
}

// orderedKeys returns m's keys with those in known first, in known's order,
// then the rest sorted.
func orderedKeys[V any](m map[string]V, known []string) []string {
	keys := make([]string, 0, len(m))
	for _, k := range known {
		if _, ok := m[k]; ok {
			keys = append(keys, k)
		}
	}

	var rest []string
	for k := range m {
		if !slices.Contains(known, k) {
			rest = append(rest, k)
		}
	}
	slices.Sort(rest)
	return append(keys, rest...)
}

func totalRawBytes(downloads []RawDownload) int64 {
	var total int64
	for _, d := range downloads {
		total += d.File.Size
	}
	return total
}

// formatBytes renders a size in binary units, e.g. "1.5 GiB".
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// summarizePeriods lists the first few periods and counts the rest.
func summarizePeriods(periods []string) string {
	const shown = 5
	if len(periods) <= shown {
		return strings.Join(periods, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(periods[:shown], ", "), len(periods)-shown)
}

func (c *CLI) printRawUsage() {
	out := c.Stdout
	fmt.Fprintln(out, "Aperiodic CLI Client: raw data (Prime + Raw plan)")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  aperiodic raw <dataset> [flags]   Download raw Parquet files")
	fmt.Fprintln(out, "  aperiodic raw coverage [flags]    Show which symbols and days are available (no API key needed)")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Datasets:")
	dw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, dataset := range RawDatasets {
		fmt.Fprintf(dw, "  %s\t%s\t(%s)\n", dataset, rawDatasetDescriptions[dataset], joinNames(RawExchanges(dataset)))
	}
	dw.Flush()
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Files land in the bucket's own Hive layout, one per month before 2026-08-01")
	fmt.Fprintln(out, "and one per day from then on:")
	fmt.Fprintln(out, "  <output-dir>/<dataset>/exchange=<exchange>/symbol=<symbol>/year=YYYY/month=MM/data.parquet")
	fmt.Fprintln(out, "  <output-dir>/<dataset>/exchange=<exchange>/symbol=<symbol>/year=YYYY/month=MM/day=DD/data.parquet")
	fmt.Fprintln(out, "Monthly files are written whole, so filter on exchange_timestamp when reading.")
	fmt.Fprintln(out, "Files already present with the expected size are skipped, so re-running resumes.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Environment:")
	fmt.Fprintln(out, "  APERIODIC_API_KEY  API key on the Prime + Raw plan (not needed with --preview or for coverage)")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Download flags:")
	downloadFlags, _ := newRawDownloadFlagSet()
	downloadFlags.SetOutput(out)
	downloadFlags.PrintDefaults()
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Coverage flags:")
	coverageFlags, _ := newRawCoverageFlagSet()
	coverageFlags.SetOutput(out)
	coverageFlags.PrintDefaults()
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Example:")
	fmt.Fprintln(out, "  aperiodic raw trades --exchange binance-futures --symbol perpetual-BTC-USDT:USDT \\")
	fmt.Fprintln(out, "    --start-date 2025-01-01 --end-date 2025-12-31 --output-dir raw")
}
