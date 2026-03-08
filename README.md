## Go CLI downloader

This repo also includes a Go CLI that downloads the raw monthly parquet files returned by the same API used by the Python client.

```bash
cd go-cli
go run . \
  -api-key "$API_KEY" \
  -bucket ohlcv \
  -timestamp true \
  -interval 1h \
  -exchange binance-futures \
  -symbol 'perpetual-BTC-USDT:USDT' \
  -start-date 2024-01-01 \
  -end-date 2024-01-31 \
  -out ./downloads
```

It fetches `/data/{bucket}` pre-signed URLs, then downloads each file concurrently with retries.

### Go CLI release automation

GitHub Actions now builds Go CLI binaries for Linux, macOS, and Windows (`amd64` + `arm64`) on tagged/release builds, uploads them to the GitHub Release, and can also push them to an external package repository when these secrets are set:

- `GO_CLI_PACKAGE_REPOSITORY_URL`
- `GO_CLI_PACKAGE_REPOSITORY_TOKEN`
