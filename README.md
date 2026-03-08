## Go CLI downloader

This repo includes a Go CLI that downloads the raw monthly parquet files returned by the same API used by the Python client.

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

The `go-cli-release` GitHub Actions workflow now publishes:

- GitHub Release assets (`.tar.gz`, `.zip`, `checksums.txt`)
- Linux package assets (`.deb` and `.rpm`)
- Homebrew formula updates
- Scoop bucket manifest updates
- Winget manifest PRs

Publish jobs are enabled when the associated secrets are configured:

- `HOMEBREW_TAP_GITHUB_TOKEN`
- `HOMEBREW_TAP_REPOSITORY` (for example `your-org/homebrew-tap`)
- `SCOOP_BUCKET_GITHUB_TOKEN`
- `SCOOP_BUCKET_REPOSITORY` (for example `your-org/scoop-bucket`)
- `WINGET_GITHUB_TOKEN`
- `WINGET_IDENTIFIER` (for example `Aperiodic.AperiodicCLI`)

Trigger with a tag named `go-cli-v*` or a published GitHub release.
