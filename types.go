package aperiodic

type TimestampType string

const (
	TimestampExchange TimestampType = "exchange"
	TimestampTrue     TimestampType = "true"
)

type Interval string

const (
	Interval1s  Interval = "1s"
	Interval1m  Interval = "1m"
	Interval5m  Interval = "5m"
	Interval15m Interval = "15m"
	Interval30m Interval = "30m"
	Interval1h  Interval = "1h"
	Interval4h  Interval = "4h"
	Interval1d  Interval = "1d"
)

type Exchange string

const (
	ExchangeBinanceFutures   Exchange = "binance-futures"
	ExchangeOkxPerps         Exchange = "okx-perps"
	ExchangeHyperliquidPerps Exchange = "hyperliquid-perps"
)

type TradeMetric string

const (
	MetricVtwap        TradeMetric = "vtwap"
	MetricFlow         TradeMetric = "flow"
	MetricTradeSize    TradeMetric = "trade_size"
	MetricImpact       TradeMetric = "impact"
	MetricRange        TradeMetric = "range"
	MetricUpdownticks  TradeMetric = "updownticks"
	MetricRunStructure TradeMetric = "run_structure"
	MetricReturns      TradeMetric = "returns"
	MetricSlippage     TradeMetric = "slippage"
)

type L1Metric string

const (
	MetricL1Price     L1Metric = "l1_price"
	MetricL1Imbalance L1Metric = "l1_imbalance"
	MetricL1Liquidity L1Metric = "l1_liquidity"
)

type L2Metric string

const (
	MetricL2Imbalance L2Metric = "l2_imbalance"
	MetricL2Liquidity L2Metric = "l2_liquidity"
)

type DerivativeMetric string

const (
	MetricBasis           DerivativeMetric = "basis"
	MetricFunding         DerivativeMetric = "funding"
	MetricOpenInterest    DerivativeMetric = "open_interest"
	MetricDerivativePrice DerivativeMetric = "derivative_price"
)

type FileInfo struct {
	Year  int    `json:"year"`
	Month int    `json:"month"`
	URL   string `json:"url"`

	// Day is set only on daily files, which cover 2026-08-01 onwards; monthly
	// files omit the field. A pointer rather than a plain int so an absent day
	// stays distinguishable from a literal 0 — reading a stray 0 as "monthly"
	// would collide with the month's real monthly file.
	Day *int `json:"day,omitempty"`
}

type AggregateDataResponse struct {
	Files []FileInfo `json:"files"`
}

type APIErrorResponse struct {
	Error      string   `json:"error"`
	Details    []string `json:"details"`
	Code       string   `json:"code"`
	UpgradeURL string   `json:"upgrade_url"`
}

type SymbolsResponse struct {
	Symbols  []string `json:"symbols"`
	Exchange string   `json:"exchange"`
	Bucket   string   `json:"bucket"`
}

// RawDataset is a raw per-tick dataset (Prime + Raw plan).
type RawDataset string

const (
	RawTrades       RawDataset = "trades"
	RawQuotes       RawDataset = "quotes"
	RawMarkPrice    RawDataset = "mark_price"
	RawIndexPrice   RawDataset = "index_price"
	RawFundingRate  RawDataset = "funding_rate"
	RawOpenInterest RawDataset = "open_interest"
)

type RawFileInfo struct {
	// Period is "YYYY-MM" for a monthly file, "YYYY-MM-DD" for a daily one.
	Period string `json:"period"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
}

type RawFilesResponse struct {
	Dataset        string        `json:"dataset"`
	Exchange       string        `json:"exchange"`
	Symbol         string        `json:"symbol"`
	SchemaVersion  int           `json:"schema_version"`
	ExpiresIn      int           `json:"expires_in"`
	Files          []RawFileInfo `json:"files"`
	MissingPeriods []string      `json:"missing_periods"`
}

type RawColumn struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Nullable    bool   `json:"nullable"`
	Description string `json:"description"`
}

type RawDatasetInfo struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Exchanges   []string    `json:"exchanges"`
	Columns     []RawColumn `json:"columns"`
}

// RawCoverageSeries is one symbol's coverage within a dataset and exchange.
type RawCoverageSeries struct {
	First   string   `json:"first"`
	Last    string   `json:"last"`
	Days    int      `json:"days"`
	Missing []string `json:"missing"`
	Bytes   int64    `json:"bytes"`
}

// RawCoverageSummary is one row per dataset and exchange.
type RawCoverageSummary struct {
	Dataset  string `json:"dataset"`
	Exchange string `json:"exchange"`
	Symbols  int    `json:"symbols"`
	First    string `json:"first"`
	Last     string `json:"last"`
	Bytes    int64  `json:"bytes"`
}

type RawCoverageResponse struct {
	SchemaVersion  int    `json:"schema_version"`
	DailyFilesFrom string `json:"daily_files_from"`
	Preview        struct {
		Period  string            `json:"period"`
		Symbols map[string]string `json:"symbols"`
	} `json:"preview"`
	Datasets []RawDatasetInfo     `json:"datasets"`
	Summary  []RawCoverageSummary `json:"summary"`
	Coverage struct {
		GeneratedAt string `json:"generated_at"`
		// Dataset → exchange → symbol.
		Datasets map[string]map[string]map[string]RawCoverageSeries `json:"datasets"`
	} `json:"coverage"`
}
