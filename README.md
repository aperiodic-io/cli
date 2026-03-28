# Aperiodic CLI

Command-line client for [Aperiodic.io](https://aperiodic.io) — institutional-grade market microstructure, liquidity and order flow metrics with full exchange universe coverage.

## Install

```bash
curl -fsSL https://github.com/aperiodic-io/client-go/releases/latest/download/aperiodic-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') -o aperiodic && chmod +x aperiodic && sudo mv aperiodic /usr/local/bin/
```

Alternatively, use the install script:

```bash
curl -fsSL https://raw.githubusercontent.com/aperiodic-io/cli/main/install.sh | bash
```

## Authentication

Set your API key as an environment variable:

```bash
export APERIODIC_API_KEY=your_api_key
```

Get your API key at [aperiodic.io](https://aperiodic.io).

## Usage

```
aperiodic <command> [flags]
```

### Commands

| Command      | Description                                                         |
|--------------|---------------------------------------------------------------------|
| `symbols`    | List available symbols for an exchange                              |
| `ohlcv`      | Download OHLCV (open/high/low/close/volume) data                    |
| `vwap`       | Download VWAP data                                                  |
| `twap`       | Download TWAP data                                                  |
| `metrics`    | Download trade, L1, or L2 metrics (use `--metric` for a specific one) |
| `derivative` | Download derivative metrics (use `--metric` for a specific one)     |

### Flags

| Flag               | Default             | Description                                      |
|--------------------|---------------------|--------------------------------------------------|
| `--exchange`       | `binance-futures`   | Exchange name                                    |
| `--symbol`         |                     | Trading pair symbol (Atlas unified symbology)    |
| `--interval`       | `1h`                | Aggregation interval                             |
| `--start-date`     |                     | Start date (`YYYY-MM-DD`)                        |
| `--end-date`       |                     | End date (`YYYY-MM-DD`)                          |
| `--metric`         |                     | Specific metric to fetch                         |
| `--output-dir`     |                     | Output directory for Parquet files (required)    |
| `--timestamp`      | `exchange`          | Timestamp source (`exchange` or `true`)          |
| `--max-concurrent` | `10`                | Maximum concurrent downloads                     |

## Examples

**List symbols on Binance Futures:**
```bash
aperiodic symbols --exchange binance-futures
```

**Download OHLCV data:**
```bash
aperiodic ohlcv \
  --exchange binance-futures \
  --symbol BTC-USDT-PERP \
  --interval 1h \
  --start-date 2024-01-01 \
  --end-date 2024-03-31 \
  --output-dir ./data
```

**Download trade flow metrics:**
```bash
aperiodic metrics \
  --exchange binance-futures \
  --symbol BTC-USDT-PERP \
  --metric flow \
  --interval 1h \
  --start-date 2024-01-01 \
  --end-date 2024-03-31 \
  --output-dir ./data
```

**Download derivative basis data:**
```bash
aperiodic derivative \
  --exchange binance-futures \
  --symbol BTC-USDT-PERP \
  --metric basis \
  --interval 1h \
  --start-date 2024-01-01 \
  --end-date 2024-03-31 \
  --output-dir ./data
```

## Supported Exchanges

| Exchange           | ID                  |
|--------------------|---------------------|
| Binance Futures    | `binance-futures`   |
| Binance Spot       | `binance`           |
| OKX Perpetuals     | `okx-perps`         |

## Intervals

`1m`, `5m`, `15m`, `30m`, `1h`, `4h`, `1d`

## Available Metrics

**Trade metrics** (`metrics` command)

| Metric          | Description                          |
|-----------------|--------------------------------------|
| `flow`          | Buy/sell trade flow                  |
| `vtwap`         | Volume-weighted average price        |
| `trade_size`    | Trade size distribution              |
| `impact`        | Price impact                         |
| `range`         | Price range                          |
| `updownticks`   | Up/down tick count                   |
| `run_structure` | Run structure                        |
| `returns`       | Returns                              |
| `slippage`      | Slippage                             |

**Order book metrics** (`metrics` command)

| Metric          | Description                          |
|-----------------|--------------------------------------|
| `l1_price`      | L1 best bid/ask price                |
| `l1_imbalance`  | L1 order book imbalance              |
| `l1_liquidity`  | L1 liquidity                         |
| `l2_imbalance`  | L2 order book imbalance              |
| `l2_liquidity`  | L2 liquidity                         |

**Derivative metrics** (`derivative` command)

| Metric             | Description                        |
|--------------------|------------------------------------|
| `basis`            | Basis (spot vs. perp spread)       |
| `funding`          | Funding rates                      |
| `open_interest`    | Open interest                      |
| `derivative_price` | Derivative price                   |

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
