# Aperiodic CLI

Command-line client for [Aperiodic.io](https://aperiodic.io) — institutional-grade market microstructure, liquidity and order flow metrics with full exchange universe coverage.

## Install

**Latest release: [v1.0.1](https://github.com/aperiodic-io/cli/releases/tag/v1.0.1)**

Download the binary for your platform:

| Platform       | Architecture | Download |
|----------------|-------------|---------|
| Linux          | x86_64      | [aperiodic-linux-amd64](https://github.com/aperiodic-io/cli/releases/download/v1.0.1/aperiodic-linux-amd64) |
| Linux          | ARM64       | [aperiodic-linux-arm64](https://github.com/aperiodic-io/cli/releases/download/v1.0.1/aperiodic-linux-arm64) |
| macOS          | x86_64      | [aperiodic-darwin-amd64](https://github.com/aperiodic-io/cli/releases/download/v1.0.1/aperiodic-darwin-amd64) |
| macOS          | Apple Silicon | [aperiodic-darwin-arm64](https://github.com/aperiodic-io/cli/releases/download/v1.0.1/aperiodic-darwin-arm64) |
| Windows        | x86_64      | [aperiodic-windows-amd64.exe](https://github.com/aperiodic-io/cli/releases/download/v1.0.1/aperiodic-windows-amd64.exe) |
| Windows        | ARM64       | [aperiodic-windows-arm64.exe](https://github.com/aperiodic-io/cli/releases/download/v1.0.1/aperiodic-windows-arm64.exe) |

Or use the install script (Linux/macOS):

```bash
curl -fsSL https://raw.githubusercontent.com/aperiodic-io/cli/main/install.sh | bash
```

Or manually (Linux/macOS):

```bash
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -fsSL "https://github.com/aperiodic-io/cli/releases/latest/download/aperiodic-${OS}-${ARCH}" -o aperiodic
chmod +x aperiodic
```

## Authentication

Set your API key as an environment variable:

```bash
export APERIODIC_API_KEY=your_api_key
```

Get your API key at [aperiodic.io](https://aperiodic.io).

## Usage

```
aperiodic <metric> [flags]
aperiodic symbols [flags]
```

The first argument is the metric name. Use `symbols` to list available symbols for an exchange.

## Available Metrics

**OHLCV / VWAP**

| Metric  | Description                          |
|---------|--------------------------------------|
| `ohlcv` | Open/high/low/close/volume           |
| `vtwap` | Volume/time-weighted average price   |

**Trade metrics**

| Metric          | Description               |
|-----------------|---------------------------|
| `flow`          | Buy/sell trade flow       |
| `trade_size`    | Trade size distribution   |
| `impact`        | Price impact              |
| `range`         | Price range               |
| `updownticks`   | Up/down tick count        |
| `run_structure` | Run structure             |
| `returns`       | Returns                   |
| `slippage`      | Slippage                  |

**Order book metrics**

| Metric          | Description                |
|-----------------|----------------------------|
| `l1_price`      | L1 best bid/ask price      |
| `l1_imbalance`  | L1 order book imbalance    |
| `l1_liquidity`  | L1 liquidity               |
| `l2_imbalance`  | L2 order book imbalance    |
| `l2_liquidity`  | L2 liquidity               |

**Derivative metrics**

| Metric             | Description                |
|--------------------|----------------------------|
| `basis`            | Basis (spot vs. perp)      |
| `funding`          | Funding rates              |
| `open_interest`    | Open interest              |
| `derivative_price` | Derivative price           |

## Flags

| Flag               | Default           | Description                                   |
|--------------------|-------------------|-----------------------------------------------|
| `--exchange`       | `binance-futures` | Exchange name                                 |
| `--symbol`         |                   | Trading pair symbol (Atlas unified symbology) |
| `--interval`       | `1h`              | Aggregation interval                          |
| `--start-date`     |                   | Start date (`YYYY-MM-DD`)                     |
| `--end-date`       |                   | End date (`YYYY-MM-DD`)                       |
| `--output-dir`     |                   | Output directory for Parquet files (required) |
| `--timestamp`      | `exchange`        | Timestamp source (`exchange` or `true`)       |
| `--max-concurrent` | `10`              | Maximum concurrent downloads                  |

## Examples

**List symbols:**
```bash
aperiodic symbols --exchange binance-futures
```

**Download OHLCV data:**
```bash
aperiodic ohlcv \
  --exchange binance-futures \
  --symbol perpetual-BTC-USDT:USDT \
  --interval 1h \
  --start-date 2024-01-01 \
  --end-date 2024-03-31 \
  --output-dir ./data
```

**Download trade flow:**
```bash
aperiodic flow \
  --exchange binance-futures \
  --symbol perpetual-BTC-USDT:USDT \
  --interval 1h \
  --start-date 2024-01-01 \
  --end-date 2024-03-31 \
  --output-dir ./data
```

**Download basis:**
```bash
aperiodic basis \
  --exchange binance-futures \
  --symbol perpetual-BTC-USDT:USDT \
  --interval 1h \
  --start-date 2024-01-01 \
  --end-date 2024-03-31 \
  --output-dir ./data
```

## Supported Exchanges

| Exchange        | ID                |
|-----------------|-------------------|
| Binance Futures | `binance-futures` |
| OKX Perpetuals  | `okx-perps`       |

## Intervals

`1m`, `5m`, `15m`, `30m`, `1h`, `4h`, `1d`

## Output

All data commands download **Parquet files** to `--output-dir`. Files are fetched concurrently (tunable via `--max-concurrent`) and named by year and month.

## Build from Source

Requires Go 1.24+.

```bash
git clone https://github.com/aperiodic-io/cli.git
cd cli
go build -o aperiodic ./cmd/aperiodic
```

## License

[ISC](LICENSE)
