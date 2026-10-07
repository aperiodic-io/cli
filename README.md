# Aperiodic CLI

Command-line client for [Aperiodic.io](https://aperiodic.io) — institutional-grade market microstructure, liquidity and order flow metrics with full exchange universe coverage.

## Install

**Latest release: [v1.0.3](https://github.com/aperiodic-io/cli/releases/tag/v1.2.0)**

Download the binary for your platform:

| Platform       | Architecture | Download |
|----------------|-------------|---------|
| Linux          | x86_64      | [aperiodic-linux-amd64](https://github.com/aperiodic-io/cli/releases/download/v1.2.0/aperiodic-linux-amd64) |
| Linux          | ARM64       | [aperiodic-linux-arm64](https://github.com/aperiodic-io/cli/releases/download/v1.2.0/aperiodic-linux-arm64) |
| macOS          | x86_64      | [aperiodic-darwin-amd64](https://github.com/aperiodic-io/cli/releases/download/v1.2.0/aperiodic-darwin-amd64) |
| macOS          | Apple Silicon | [aperiodic-darwin-arm64](https://github.com/aperiodic-io/cli/releases/download/v1.2.0/aperiodic-darwin-arm64) |
| Windows        | x86_64      | [aperiodic-windows-amd64.exe](https://github.com/aperiodic-io/cli/releases/download/v1.2.0/aperiodic-windows-amd64.exe) |
| Windows        | ARM64       | [aperiodic-windows-arm64.exe](https://github.com/aperiodic-io/cli/releases/download/v1.2.0/aperiodic-windows-arm64.exe) |

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

For [preview data](#preview-data) (`--preview`), no API key is required — the CLI uses the shared public demo key automatically.

[Raw data](#raw-data) needs a key on the Prime + Raw plan; `aperiodic raw coverage` and `aperiodic raw --preview` work without one.

## Usage

```
aperiodic <metric> [flags]
aperiodic symbols [flags]
aperiodic raw <dataset> [flags]
aperiodic raw coverage [flags]
aperiodic stream <dataset> [flags]
```

The first argument is the metric name. Use `symbols` to list available symbols for an exchange, `raw` for [raw data](#raw-data), and `stream` for the [live stream](#live-stream).

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
| `--preview`        | `false`           | Query the free preview dataset (no subscription; whitelisted parameters only) |

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

**Preview data (no API key required):**
```bash
aperiodic ohlcv --preview \
  --exchange binance-futures \
  --symbol perpetual-BTC-USDT:USDT \
  --interval 5m \
  --start-date 2025-05-01 \
  --end-date 2025-05-31 \
  --output-dir ./data
```

## Preview data

`--preview` fetches a free, curated slice of data from the preview endpoint — no subscription and no API key required (the shared public demo key is used automatically). Requests must match one of the whitelisted parameter combinations (exchange, symbol, interval, timestamp, date range) listed at [aperiodic.io/catalog](https://aperiodic.io/catalog#preview).

## Supported Exchanges

| Exchange              | ID                   |
|-----------------------|----------------------|
| Binance Futures       | `binance-futures`    |
| OKX Perpetuals        | `okx-perps`          |
| Hyperliquid Perpetuals | `hyperliquid-perps` |

## Intervals

`15s`, `30s`, `1m`, `5m`, `15m`, `30m`, `1h`, `4h`, `1d`

`15s` and `30s` need a Tier 3 subscription and are not available for the derivative metrics.

## Output

All metric commands download **Parquet files** to `--output-dir`, fetched concurrently (tunable via `--max-concurrent`). Raw data uses its own [file layout](#raw-file-layout).

History up to **2026-07-31** is one file per month; from **2026-08-01** onwards it is one file per day. You always ask for a date range and get back every file covering it, so a range spanning the changeover downloads the earlier months as monthly files followed by a daily file per day.

Filenames follow the granularity, zero-padded so a directory listing sorts chronologically:

```
2026-07.parquet      # monthly
2026-08-01.parquet   # daily
2026-08-02.parquet
```

## Raw data

Raw trades, top-of-book quotes and derivative ticks for Binance, OKX and Hyperliquid perpetuals: the data the metrics are built from. Raw data needs the **Prime + Raw** plan ([pricing](https://aperiodic.io/pricing)); it uses the same API key and symbols as the metrics. To try it without an account, use [`--preview`](#raw-preview).

| Dataset         | Contents                                     | `binance-futures` | `okx-perps` | `hyperliquid-perps` |
|-----------------|----------------------------------------------|:-----------------:|:-----------:|:-------------------:|
| `trades`        | Every trade: id, taker side, price, amount   | yes               | yes         | yes                 |
| `quotes`        | Top of book: best bid/ask price and amount   | yes               | yes         | yes                 |
| `mark_price`    | Mark price, one row per change               | yes               | yes         | yes                 |
| `index_price`   | Index price, one row per change              | yes               | yes         | yes                 |
| `funding_rate`  | Funding rate and next funding time           | yes               | yes         | yes                 |
| `open_interest` | Open interest, one row per change            | yes               | yes         | yes                 |

Hyperliquid's derivative feed carries no exchange time, so in its `mark_price`, `index_price`, `funding_rate` and `open_interest` files `exchange_timestamp` is modelled, and an `exchange_timestamp_kind` column (`"modelled"`) follows it. Raw L2 order books are not offered.

Every file starts with `exchange_timestamp` (the venue's time) and `local_timestamp` (when the event reached our capture machine). Timestamps are UTC.

**Download a year of trades:**
```bash
aperiodic raw trades \
  --exchange binance-futures \
  --symbol perpetual-BTC-USDT:USDT \
  --start-date 2025-01-01 \
  --end-date 2025-12-31 \
  --output-dir ./raw
```

**List what is available (no API key needed):**
```bash
aperiodic raw coverage                                           # one row per dataset and exchange
aperiodic raw coverage --dataset quotes --exchange okx-perps     # every symbol: first/last day, days, size
aperiodic raw coverage --symbol perpetual-BTC-USDT:USDT          # one symbol across datasets
```

### Raw flags

| Flag               | Default           | Description                                                                 |
|--------------------|-------------------|-----------------------------------------------------------------------------|
| `--exchange`       | `binance-futures` | Exchange name                                                               |
| `--symbol`         |                   | Trading pair symbol (Atlas unified symbology)                               |
| `--start-date`     |                   | First day, UTC (`YYYY-MM-DD`)                                               |
| `--end-date`       |                   | Last day, inclusive (`YYYY-MM-DD`); ranges over 366 days are split into several requests for you |
| `--output-dir`     |                   | Root folder for the file layout below (required)                            |
| `--max-concurrent` | `4`               | Maximum concurrent downloads                                                |
| `--overwrite`      | `false`           | Download files again even if already present with the expected size        |
| `--preview`        | `false`           | Fetch the free preview file with the shared demo key (dates are ignored)    |

`aperiodic raw coverage` takes `--dataset`, `--exchange` and `--symbol` as filters; with none it prints the summary.

### Raw file layout

Files land in the bucket's own Hive layout, the same one the Python client's `download_raw` writes, so either can top up a folder the other started. History before **2026-08-01** is one file per month; from then on it is one file per day:

```
raw/trades/exchange=binance-futures/symbol=perpetual-BTC-USDT:USDT/year=2025/month=06/data.parquet          # monthly
raw/trades/exchange=binance-futures/symbol=perpetual-BTC-USDT:USDT/year=2026/month=08/day=01/data.parquet   # daily
```

On Windows, `:` is not allowed in file names, so it is written as `%3A`, which Hive-partition readers decode back.

- Monthly files are written whole, so they can reach past the requested range: filter on `exchange_timestamp` when reading. Monthly and daily files sit at different depths, so read a folder with a recursive glob, e.g. `pl.scan_parquet("raw/trades/**/data.parquet")`.
- A file already on disk with the expected size is skipped, so running the same command again resumes an interrupted download. Each file is streamed to `data.parquet.part` and renamed once complete.
- Download URLs are valid for one hour; one that has expired is re-requested automatically.
- A key whose plan lacks raw data gets a `raw_not_in_plan` error with the link to upgrade.

### Raw preview

`--preview` needs no account and no API key (the shared public demo key is used automatically). It returns the **2025-06** file of each venue's BTC perpetual: `perpetual-BTC-USDT:USDT` on `binance-futures` and `okx-perps`, `perpetual-BTC-USDC:USDC` on `hyperliquid-perps`, for any dataset the venue serves. `--start-date` and `--end-date` are not needed.

```bash
aperiodic raw quotes --preview \
  --exchange okx-perps \
  --symbol perpetual-BTC-USDT:USDT \
  --output-dir ./raw
```

## Live stream

`aperiodic stream` subscribes to the live WebSocket feed (`wss://stream.aperiodic.io/v1/stream`) and prints **one JSON line per row** to stdout, so it pipes straight into `jq` or a file. It needs an API key on a plan with live data; symbols are the same as for the metrics.

```bash
aperiodic stream ohlcv \
  --exchange binance-futures \
  --interval 1m \
  --symbols perpetual-BTC-USDT:USDT,perpetual-ETH-USDT:USDT
```

```
{"channel":"ohlcv.binance-futures.1m","snapshot":true,"data":{"exchange":11,"symbol":"perpetual-BTC-USDT:USDT","interval":"1m","time":1791307200000000,...}}
{"channel":"ohlcv.binance-futures.1m","snapshot":false,"data":{"exchange":11,"symbol":"perpetual-BTC-USDT:USDT","interval":"1m","time":1791307260000000,...}}
```

- `data` is the server's row object, unchanged apart from whitespace (no re-escaping). On plans that include it, the latest row per symbol arrives first with `"snapshot":true`.
- Granted and rejected channels, server warnings and reconnects go to **stderr**.
- Dropped connections reconnect with capped exponential backoff and re-subscribe. This covers network drops, server restarts or drains (closes such as 1000, 1001, 1011, 1012), handshake 5xx and 75 s of silence. The backoff starts over only once a connection has delivered a row or stayed up for 60 s. Delivery is at most once: rows published while disconnected are not replayed.
- It exits 1 with the server's message, without retrying, if:
  - the key is refused (401), the plan has no live access (403), or the handshake gets 426;
  - the connection limit is reached (429) on the first connect. After a session has been subscribed, a 429 is retried, because the server may still be counting the dropped socket;
  - the first connect gets no HTTP response at all (bad URL, DNS, TLS or proxy failure);
  - every channel is rejected, the server unsubscribes the last one, or the server answers the subscribe with an error;
  - the server closes with a 4000–4999 code (4001: plan lapsed or key rotated) or 1008 (rate-limit abuse).
- Ctrl-C closes the socket cleanly and exits 130; SIGTERM exits 143.

| Flag | Description | Default |
|------|-------------|---------|
| `--exchange` | Exchange name | `binance-futures` |
| `--interval` | Aggregation interval | `1m` |
| `--symbols` | Comma-separated symbols | every symbol your plan allows |
| `--count` | Stop after this many live rows (snapshot rows are printed but not counted) | no limit |
| `--duration` | Stop after this long, e.g. `90s`, `1h` | no limit |

**Take the next closed 1m bar and stop:**
```bash
aperiodic stream ohlcv --symbols perpetual-BTC-USDT:USDT --count 1 | jq 'select(.snapshot | not) | .data'
```

Set `APERIODIC_STREAM_URL` to point at another stream endpoint.

## Build from Source

Requires Go 1.24+.

```bash
git clone https://github.com/aperiodic-io/cli.git
cd cli
go build -o aperiodic ./cmd/aperiodic
```

## License

[ISC](LICENSE)
