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
	ExchangeBinanceFutures Exchange = "binance-futures"
	ExchangeOkxPerps       Exchange = "okx-perps"
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
}

type AggregateDataResponse struct {
	Files []FileInfo `json:"files"`
}

type APIErrorResponse struct {
	Error   string   `json:"error"`
	Details []string `json:"details"`
}

type SymbolsResponse struct {
	Symbols  []string `json:"symbols"`
	Exchange string   `json:"exchange"`
	Bucket   string   `json:"bucket"`
}
