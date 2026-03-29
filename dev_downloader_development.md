# Downloader Development Guide

Руководство по созданию новых загрузчиков данных в `cmd/data-downloaders/`.

---

## Architecture Overview

```
cmd/data-downloaders/<name>/
├── main.go           # CLI flags, config loading, orchestration
├── downloader.go     # Download logic (pagination, period splitting)
├── types.go          # API response types (optional, if not in pkg/wb)
└── config.yaml       # YAML configuration with ${ENV} expansion

pkg/
├── config/           # LoadYAML(), shared config types (PromotionConfig, etc.)
├── storage/sqlite/   # SQLiteSalesRepository (all tables, all methods)
├── progress/         # ProgressTracker interface + CLITracker
├── testing/          # Mock data generators (GenerateSalesRows, etc.)
├── utils/            # Display, DateRange helpers
└── wb/               # WB client, API types, rate limiting
```

**Rules**: Rule 6 (pkg/ has no cmd/ imports), Rule 13 (downloaders are autonomous).

---

## Shared Packages

### pkg/config — Configuration Loading

```go
import "github.com/ilkoid/poncho-ai/pkg/config"

// Load YAML with automatic ${ENV} expansion
var cfg MyConfig
if err := config.LoadYAML("config.yaml", &cfg); err != nil {
    log.Fatal(err)
}
```

**Available config types** (from `pkg/config/utility.go`):
- `WBClientConfig` — API key, rate limit, timeout
- `DownloadConfig` — sales downloader settings
- `PromotionConfig` — promotion downloader settings (with `GetDefaults()`)
- `FeedbacksConfig` — feedbacks downloader settings (with `GetDefaults()`)
- `FunnelConfig` — funnel downloader settings (with `GetDefaults()`)
- `FunnelAggregatedConfig` — aggregated funnel settings (with `GetDefaults()`)

Each config type has a `GetDefaults()` method that fills zero values with sensible defaults.

**Pattern**: Each downloader wraps shared types in a local `Config` struct:
```go
type Config struct {
    WB        config.WBClientConfig  `yaml:"wb"`
    MySection config.SomeConfig      `yaml:"my_section"`
}
```

### pkg/storage/sqlite — Database Operations

All tables are managed by `SQLiteSalesRepository`. One `*sql.DB` connection handles everything.

```go
import "github.com/ilkoid/poncho-ai/pkg/storage/sqlite"

repo, err := sqlite.NewSQLiteSalesRepository("data.db")
if err != nil {
    log.Fatal(err)
}
defer repo.Close()

// Sales methods
repo.Save(ctx, salesRows)
repo.Exists(ctx, rrdID)

// Funnel methods
repo.SaveFunnelMetrics(ctx, metrics)
repo.SaveFunnelHistoryWithWindow(ctx, rows, refreshDays)

// Promotion methods
repo.SaveCampaigns(ctx, campaigns)
repo.SaveCampaignDailyStats(ctx, stats)

// Feedbacks methods
repo.SaveFeedbacks(ctx, feedbacks)
repo.SaveQuestions(ctx, questions)
repo.CountFeedbacks(ctx)
```

**Schema**: `initSchema()` in `repository.go` creates ALL tables. New tables should be added via `GetXxxSchemaSQL()` constants in `schema.go`.

### pkg/progress — Progress Tracking

```go
import "github.com/ilkoid/poncho-ai/pkg/progress"

tracker := progress.NewCLITracker(totalItems)
for _, item := range items {
    // ... process item ...
    tracker.Update(1)
}
tracker.Done()
```

**Interface** (for testability):
```go
type ProgressTracker interface {
    Update(items int) error
    Done()
    ETA() string
    String() string
}
```

### pkg/testing — Mock Data Generators

```go
import pkgtesting "github.com/ilkoid/poncho-ai/pkg/testing"

// Generate test data for E2E tests
rows := pkgtesting.GenerateSalesRows(from, to, page, 100)
campaigns := pkgtesting.GenerateCampaigns(10, 7)
feedbacks := pkgtesting.GenerateFeedbacks(rng, 50)
questions := pkgtesting.GenerateQuestions(rng, 30)
```

### pkg/utils — Display & Date Utilities

```go
import "github.com/ilkoid/poncho-ai/pkg/utils"

// Display helpers
utils.Repeat("=", 71)        // "====..."
utils.MaskAPIKey("sk-xxx")   // "sk-xx...xxx"
utils.FormatDuration(d)      // "1m 30s"

// Date utilities
dr := utils.DateRange{From: start, To: end}
periods := dr.Split(30)              // Split into 30-day chunks
fromTs, toTs := dr.ToInt()           // YYYYMMDD format
t, err := utils.ParseDate("2025-01-01")
```

### pkg/wb — WB Client & Rate Limiting

```go
import "github.com/ilkoid/poncho-ai/pkg/wb"

client := wb.New(apiKey)

// Setup two-level adaptive rate limiting (REQUIRED before API calls)
client.SetRateLimit("tool_id", desiredRate, desiredBurst, apiRate, apiBurst)
client.SetAdaptiveParams(0, probeAfter, maxBackoff)

// Make API calls
client.Get(ctx, "tool_id", baseURL, rateLimit, burst, "/api/path", params, &response)
```

See [dev_limits.md](dev_limits.md) for full rate limiting documentation.

---

## Resume Strategies

Three different strategies are used across downloaders. Each solves a different problem:

### 1. Smart Resume with Row Filtering (download-wb-sales)

**When**: Data has stable unique IDs (e.g., `rrd_id`), resuming after interruption.

**Config**: `download.resume: true`

**Implementation**:
```go
lastDT, _ := repo.GetLastSaleDT(ctx)
firstDT, _ := repo.GetFirstSaleDT(ctx)

// Adjust period start if data exists
for _, period := range periods {
    adjusted := AdjustPeriodForResume(period, lastDT, firstDT)
    if adjusted.Skip { continue }
    // ... download adjusted range ...
}
```

Plus per-row filtering in save:
```go
exists, _ := repo.Exists(ctx, row.RrdID)
if exists { continue }
```

### 2. Per-Entity Date Resume (download-wb-promotion)

**When**: Data grouped by entity (campaigns), each with its own last date.

**Config**: `promotion.resume: true`

**Implementation**:
```go
lastDates, _ := repo.GetLastCampaignStatsDateAll(ctx)  // map[campaignID]date
for _, batch := range batches {
    earliest := findEarliestDate(batch, lastDates)
    if earliest > endDate { continue }  // skip fully loaded batch
    // ... download from earliest+1 ...
}
```

Uses `INSERT OR REPLACE` (idempotent, no per-row checks needed).

### 3. Refresh Window (download-wb-funnel)

**When**: Data may change retroactively (WB updates recent metrics).

**Config**: `storage.funnel_refresh_window: 7` (days)

**Implementation**:
```go
// No period adjustment — always load full range
// Save logic uses date-based REPLACE vs IGNORE:
repo.SaveFunnelHistoryWithWindow(ctx, rows, refreshDays)
// Internally: recent rows → REPLACE, old rows → IGNORE
```

### 4. No Resume (download-wb-feedbacks, download-wb-funnel-agg)

**When**: Data is append-only or fully replaceable.

**Implementation**: Just `INSERT OR IGNORE` (feedbacks) or `INSERT OR REPLACE` (funnel-agg). Rely on SQL UNIQUE constraints for deduplication.

---

## Creating a New Downloader: Step-by-Step

### 1. Create directory structure

```bash
mkdir -p cmd/data-downloaders/download-wb-<name>
cd cmd/data-downloaders/download-wb-<name>
```

### 2. Define config types (if not in pkg/config)

If a shared config type doesn't exist in `pkg/config/utility.go`, add it there:

```go
// pkg/config/utility.go
type MyDownloaderConfig struct {
    DbPath    string        `yaml:"db_path"`
    Days      int           `yaml:"days"`
    RateLimit int           `yaml:"rate_limit"`
}

func (c MyDownloaderConfig) GetDefaults() MyDownloaderConfig {
    if c.DbPath == "" { c.DbPath = "my_data.db" }
    if c.Days == 0 { c.Days = 7 }
    if c.RateLimit == 0 { c.RateLimit = 100 }
    return c
}
```

Then add schema SQL in `pkg/storage/sqlite/schema.go` and methods in a new file.

### 3. Create main.go

```go
package main

import (
    "context"
    "flag"
    "fmt"
    "log"
    "os"
    "os/signal"
    "strings"
    "syscall"
    "time"

    _ "github.com/mattn/go-sqlite3"
    "github.com/ilkoid/poncho-ai/pkg/config"
    "github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
    "github.com/ilkoid/poncho-ai/pkg/wb"
)

type Config struct {
    WB        config.WBClientConfig    `yaml:"wb"`
    MySection config.MyDownloaderConfig `yaml:"my_section"`
}

func main() {
    // 1. Parse flags
    configPath := flag.String("config", "config.yaml", "Path to config file")
    days := flag.Int("days", 0, "Days from today")
    help := flag.Bool("help", false, "Show help")
    flag.Parse()
    if *help { printHelp(); return }

    // 2. Load config with ENV expansion
    cfg, err := loadConfig(*configPath)
    if err != nil {
        log.Printf("Config not found, using defaults: %v", err)
        cfg = defaultConfig()
    }
    cfg.MySection = cfg.MySection.GetDefaults()
    cfg.WB = cfg.WB.GetDefaults()

    // 3. Apply CLI overrides
    if *days > 0 { cfg.MySection.Days = *days }

    // 4. Handle Ctrl+C
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    go func() { <-sigChan; fmt.Println("\nInterrupted!"); cancel() }()

    // 5. Open database (schema + PRAGMAs in one call)
    repo, err := sqlite.NewSQLiteSalesRepository(cfg.MySection.DbPath)
    if err != nil { log.Fatalf("Failed to create repository: %v", err) }
    defer repo.Close()

    // 6. Create API client with adaptive rate limiting
    apiKey := os.Getenv("WB_API_KEY")
    client := wb.New(apiKey)
    defaults := cfg.MySection.GetDefaults()
    client.SetRateLimit("my_tool", defaults.RateLimit, defaults.BurstLimit,
        defaults.RateLimitApi, defaults.BurstLimitApi)
    client.SetAdaptiveParams(0, defaults.AdaptiveProbeAfter, defaults.MaxBackoffSeconds)

    // 7. Download data
    // ... call your download function ...

    // 8. Print summary
    fmt.Println("Download complete!")
}

func loadConfig(path string) (*Config, error) {
    var cfg Config
    if err := config.LoadYAML(path, &cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}

func defaultConfig() *Config { return &Config{} }
```

### 4. Create downloader.go

```go
package main

import (
    "context"
    "fmt"

    "github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
    "github.com/ilkoid/poncho-ai/pkg/wb"
)

func DownloadMyData(
    ctx context.Context,
    client *wb.Client,
    repo *sqlite.SQLiteSalesRepository,
    dateFrom, dateTo string,
) (int, error) {
    total := 0
    // ... pagination, API calls, repo.SaveXxx() ...
    return total, nil
}
```

### 5. Create config.yaml

```yaml
wb:
  api_key: ${WB_API_KEY}
  rate_limit: 100
  burst_limit: 10
  timeout: 30s

my_section:
  db_path: my_data.db
  days: 7
  rate_limit: 3
  rate_limit_api: 3
  burst_limit: 3
  burst_limit_api: 1
```

### 6. Add tests

```go
// e2e_test.go
package main

import (
    "context"
    "testing"
    "os"

    "github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
    pkgtesting "github.com/ilkoid/poncho-ai/pkg/testing"
)

func TestBasicDownload(t *testing.T) {
    dbPath := t.TempDir() + "/test.db"
    repo, err := sqlite.NewSQLiteSalesRepository(dbPath)
    if err != nil { t.Fatal(err) }
    defer repo.Close()

    // Generate mock data
    mockClient := wb.NewMockClient()
    mockClient.AddMockData(pkgtesting.GenerateMyData(10))

    // Download
    count, err := DownloadMyData(ctx, mockClient, repo, "2025-01-01", "2025-01-31")
    if err != nil { t.Fatal(err) }
    if count == 0 { t.Error("expected some rows") }
}
```

---

## Checklist: Adding New Downloader

- [ ] Config type added to `pkg/config/utility.go` (with `GetDefaults()`)
- [ ] Schema SQL added to `pkg/storage/sqlite/schema.go`
- [ ] Repository methods added to `pkg/storage/sqlite/` (new file)
- [ ] API types in `pkg/wb/` (or local `types.go` if downloader-specific)
- [ ] `main.go` uses `config.LoadYAML()` for config loading
- [ ] `main.go` uses `sqlite.NewSQLiteSalesRepository()` for single DB open
- [ ] Adaptive rate limiting configured via `client.SetRateLimit()` + `SetAdaptiveParams()`
- [ ] CLI flags: `--config`, `--days/--begin/--end`, `--db`, `--mock`, `--help`
- [ ] Ctrl+C handling with `context.WithCancel`
- [ ] `config.yaml` with `${ENV}` expansion
- [ ] Mock mode for testing without API
- [ ] E2E test with mock client

---

## Common Pitfalls

1. **Forgetting `SetRateLimit()`**: Without it, adaptive state is created lazily with `apiFloor=0`, limiter never reduces on 429.
2. **Double DB open**: Use single `sqlite.NewSQLiteSalesRepository()` — it handles schema + PRAGMAs.
3. **ToolID mismatch**: `SetRateLimit("tool_A")` + `Get(ctx, "tool_B", ...)` creates separate limiter with no adaptive state.
4. **Missing `_ "github.com/mattn/go-sqlite3"`**: Driver registration required in main.go.
5. **Config not expanding `${ENV}`**: Use `config.LoadYAML()` which handles `os.ExpandEnv()`.
6. **Shared database between utilities**: If multiple downloaders write to the same database file (e.g., `wb-sales.db`), be aware that all data is stored together. To reset a specific utility's data, you can manually delete the relevant tables or use separate database files per utility.
6. **Ignoring 5xx errors**: The WB API may return transient 5xx errors (503, 500, 502, 504). The `wb.Client` automatically retries these with exponential backoff (5s, 10s, 15s). No manual retry logic needed in downloaders.

---

## Transient Error Handling

The `wb.Client` automatically handles transient errors from the WB API:

### 5xx Server Errors (Retried)

When the WB API returns a 5xx status code (500-599), the client automatically retries with exponential backoff:

- **Retry 1**: 5 second wait
- **Retry 2**: 10 second wait
- **Retry 3**: 15 second wait (default `retry_attempts: 3`)

**Log output**:
```
⚠️  Transient 5xx (503) for get_campaign_fullstats, retrying in 5s... (attempt 2/3)
⚠️  Transient 5xx (503) for get_campaign_fullstats, retrying in 10s... (attempt 3/3)
✅ 462 daily, 1392 app, 4567 nm, 0 booster (api 24.971s, flatten 2ms, db 504ms)
```

**Common 5xx errors from WB API**:
- `503 Service Unavailable` - Upstream service failures (e.g., `s2s-api-auth-adv`)
- `500 Internal Server Error` - RPC timeouts (e.g., `GetStatsDailyNmApp: DeadlineExceeded`)

### 429 Rate Limiting (Adaptive)

429 responses are handled with **adaptive rate limiting** (see [dev_limits.md](dev_limits.md)):
1. Limiter immediately reduces to `apiFloor` (swagger-documented rate)
2. Cooldown period based on `X-Ratelimit-Retry` header or exponential backoff
3. Stays at reduced rate permanently (no probing back to aggressive rate)

### Network Errors (Retried)

Connection failures, timeouts, and other network errors are automatically retried up to `retry_attempts` times.

### Configuration

Control retry behavior via `config.yaml`:

```yaml
wb:
  retry_attempts: 5  # Total attempts (1 initial + 4 retries)
  timeout: 30s       # Per-request timeout
```

For large downloads, consider:
- Increasing `retry_attempts` to 5 for better resilience
- Using `--resume` flag to continue after interruption
- Running during off-peak hours (2-6 AM Moscow time)
