# Downloader Development Guide

Руководство по созданию новых загрузчиков данных в `cmd/data-downloaders/`.

---

## Architecture Overview

```
cmd/data-downloaders/<name>/
├── main.go           # CLI flags, config loading, orchestration
├── downloader.go     # Download logic (pagination, period splitting)
├── utils.go          # Local helpers: repeat(), maskAPIKey()
├── types.go          # API response types (optional, if not in pkg/wb)
└── config.yaml       # YAML configuration with ${ENV} expansion

Existing downloaders:
  download-wb-sales          # WB Sales/Statistics API → SQLite
  download-wb-promotion      # WB Promotion/Advertising API → SQLite
  download-wb-feedbacks      # WB Feedbacks API → SQLite
  download-wb-funnel         # WB Analytics v3 daily funnel → SQLite
  download-wb-funnel-agg     # WB Analytics v3 aggregated funnel → SQLite
  download-wb-stocks         # WB Analytics warehouse stock snapshots → SQLite
  download-wb-stock-history  # WB Analytics historical stock CSV reports → SQLite
  download-wb-cards          # WB Content API cards → SQLite (cursor-based pagination)
  download-wb-region-sales   # WB Analytics region sales → SQLite (31-day horizon)
  download-wb-prices         # WB Content API prices → SQLite

pkg/
├── config/           # LoadYAML(), shared config types (PromotionConfig, etc.)
├── storage/sqlite/   # SQLiteSalesRepository (all tables, all methods)
├── progress/         # ProgressTracker interface + CLITracker (available, unused by downloaders)
├── testing/          # Mock data generators (GenerateSalesRows, etc.)
├── utils/            # Display, DateRange helpers (available, downloaders use local copies)
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
- `StocksConfig` — warehouse stock snapshot settings (with `GetDefaults()`)
- `StockHistoryConfig` — historical stock CSV report settings (with `GetDefaults()`)

Each config type has a `GetDefaults()` method that fills zero values with sensible defaults.

**Important**: Do NOT default `Days` field in `GetDefaults()` — it would override explicit `From`/`To` dates set in config. Default Days in `main.go` only when both From and To are empty (see Common Pitfalls #8).

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
repo.SaveFunnelHistoryWithWindow(ctx, rows, refreshDays)

// Promotion methods
repo.SaveCampaigns(ctx, campaigns)
repo.SaveCampaignAppStats(ctx, appRows)
repo.SaveCampaignNmStats(ctx, nmRows)

// Feedbacks methods
repo.SaveFeedbacks(ctx, feedbacks)
repo.SaveQuestions(ctx, questions)

// Stocks methods
repo.SaveStocks(ctx, snapshotDate, items)

// Aggregated funnel methods
repo.SaveFunnelAggregatedBatch(ctx, batch)
```

**Interface vs Struct**: `SalesRepository` interface has 8 core methods (Save, Exists, Count, etc.). The `SQLiteSalesRepository` struct has 40+ public methods. Downloaders call struct methods directly — no need to add every method to the interface.

**Schema**: `initSchema()` in `repository.go` creates ALL tables + runs ALTER TABLE migrations for schema evolution. New tables should be added via `GetXxxSchemaSQL()` constants.

**Available schema functions**:
- `GetSchemaSQL()` — Main sales table (83 fields)
- `GetServiceRecordsSchemaSQL()` — Service records (logistics, deductions)
- `GetCreateViewSQL()` — FBW view
- `GetFunnelSchemaSQL()` — Funnel analytics (products, funnel_metrics_daily)
- `GetPromotionSchemaSQL()` — Campaigns (campaigns, campaign_stats_daily, campaign_products)
- `GetFunnelAggregatedSchemaSQL()` — Aggregated funnel metrics
- `GetCampaignFullstatsSchemaSQL()` — Campaign detailed stats (app, nm, booster)
- `GetFeedbacksSchemaSQL()` — Feedbacks and questions
- `GetQualitySchemaSQL()` — Product quality summary (LLM analysis)
- `GetStockHistorySchemaSQL()` — Stock history CSV reports (in `stock_history_schema.go`)
- `GetStocksWarehouseSchemaSQL()` — Warehouse stock snapshots

### pkg/progress — Progress Tracking

> **Note**: Currently unused by downloaders — all use manual `fmt.Printf` for progress output. Available for future use.

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

> **Note**: Downloaders define local `repeat()` and `maskAPIKey()` helpers in their own `utils.go` for zero-dependency simplicity. The `pkg/utils` versions are available but not imported by downloaders.

```go
import "github.com/ilkoid/poncho-ai/pkg/utils"

// Display helpers (available, but downloaders use local copies)
utils.Repeat("=", 71)        // "====..."
utils.MaskAPIKey("sk-xxx")   // "sk-xx...xxx"
utils.FormatDuration(d)      // "1m 30s"

// Date utilities (useful for period splitting in downloaders)
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

## API Key Retrieval Patterns

Two patterns exist in the codebase. Choose based on your downloader's needs:

### Pattern A: YAML Env Expansion (simpler)

Used by: `download-wb-sales`, `download-wb-funnel`, `download-wb-funnel-agg`

```yaml
# config.yaml
wb:
  api_key: ${WB_STAT}                        # Expanded by config.LoadYAML()
  analytics_api_key: ${WB_API_ANALYTICS_AND_PROMO_KEY}
```

```go
// main.go — read directly from config, detect unresolved placeholders
apiKey := cfg.WB.APIKey
if apiKey == "" || apiKey == "${WB_STAT}" {
    log.Fatal("❌ WB_STAT not set")
}
client := wb.New(apiKey)
```

**Pros**: Simple, one-liner. **Cons**: Placeholder detection is string-matching (`"${WB_STAT}"`), not generic.

### Pattern B: `getAPIKey()` Helper (explicit)

Used by: `download-wb-promotion`, `download-wb-feedbacks`, `download-wb-stocks`, `download-wb-stock-history`

```yaml
# config.yaml — api_key is empty or omitted, rely on env vars
wb:
  api_key: ""
```

```go
// main.go — explicit priority chain
func getAPIKey(cfg *Config) string {
    // Priority: primary env > fallback env > config value
    if key := os.Getenv("WB_API_ANALYTICS_AND_PROMO_KEY"); key != "" {
        return key
    }
    if key := os.Getenv("WB_API_KEY"); key != "" {
        return key
    }
    return cfg.WB.APIKey
}
```

**Pros**: Clean priority chain, no placeholder detection needed. **Cons**: More code.

**Recommendation**: Use Pattern B (`getAPIKey()`) for new downloaders. It's cleaner and avoids fragile `${...}` string checks.

**API Keys by Downloader**:

| Downloader | Primary Env Var | Fallback | Config Field |
|------------|----------------|----------|--------------|
| sales | `WB_STAT` | — | `cfg.WB.APIKey` |
| promotion | `WB_API_ANALYTICS_AND_PROMO_KEY` | `WB_API_KEY` | `cfg.WB.APIKey` |
| feedbacks | `WB_API_FEEDBACK_KEY` | `WB_API_KEY` | `cfg.WB.APIKey` |
| funnel | `WB_API_ANALYTICS_AND_PROMO_KEY` | `WB_STAT` | `cfg.WB.AnalyticsAPIKey` |
| funnel-agg | `WB_API_ANALYTICS_AND_PROMO_KEY` | `WB_STAT` | `cfg.WB.AnalyticsAPIKey` |
| stocks | configurable (`api_key_env`) | `WB_API_ANALYTICS_AND_PROMO_KEY` | `cfg.WB.APIKey` |
| stock-history | `WB_API_KEY` | — | `cfg.WB.APIKey` |

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

### 4. No Resume (download-wb-feedbacks, download-wb-funnel-agg, download-wb-stocks)

**When**: Data is append-only or fully replaceable.

**Implementation**: Just `INSERT OR IGNORE` (feedbacks) or `INSERT OR REPLACE` (funnel-agg, stocks). Rely on SQL UNIQUE constraints for deduplication.

### Resume and CLI Flags

| Downloader | Strategy | `--resume` CLI flag | Config field |
|------------|----------|---------------------|--------------|
| sales | Smart Resume | ❌ No | `download.resume` |
| promotion | Per-Entity Date | ✅ Yes | `promotion.resume` |
| stock-history | CSV Report Resume | ✅ Yes | `stock_history.resume` |
| funnel | Refresh Window | ❌ No | N/A |
| funnel-agg | No Resume | ❌ No | N/A |
| feedbacks | No Resume | ❌ No | N/A |
| stocks | No Resume | ❌ No | N/A |

**Note**: `--resume` is not universal. Only promotion and stock-history support it as a CLI flag. Sales uses config-only resume (`download.resume: true`).

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
    // NOTE: Days is NOT defaulted here — default in main.go only when From/To empty
    // See Common Pitfalls #8
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
    // 1. Parse flags (tiered: core + period + optional)
    configPath := flag.String("config", "config.yaml", "путь к конфигу")
    days := flag.Int("days", 0, "дней от сегодня (default: 7)")
    begin := flag.String("begin", "", "начало периода YYYY-MM-DD")
    end := flag.String("end", "", "конец периода YYYY-MM-DD")
    dbPath := flag.String("db", "", "путь к базе (overrides config)")
    mockMode := flag.Bool("mock", false, "mock mode (no API calls)")
    help := flag.Bool("help", false, "справка")
    flag.BoolVar(help, "h", false, "справка")
    flag.Parse()
    if *help { printHelp(); return }

    // 2. Load config with ENV expansion
    cfg, err := loadConfig(*configPath)
    if err != nil {
        log.Fatalf("❌ Failed to load config: %v", err)
    }
    defaults := cfg.MySection.GetDefaults()

    // 3. Apply CLI overrides
    if *days > 0 { defaults.Days = *days }
    if *begin != "" { defaults.From = *begin }
    if *end != "" { defaults.To = *end }
    if *dbPath != "" { defaults.DbPath = *dbPath }

    // 4. Calculate dates: CLI flags > config from/to > config days > default 7
    if defaults.From == "" || defaults.To == "" {
        if defaults.Days == 0 { defaults.Days = 7 }
        beginDate, endDate := calculateDateRange(defaults.Days)
        defaults.From = beginDate
        defaults.To = endDate
    }

    // 5. Get API key (priority: env > config, with placeholder detection)
    apiKey := getAPIKey(cfg)
    if apiKey == "" {
        log.Fatal("❌ No API key. Set WB_API_KEY or configure yaml api_key.")
    }

    // 6. Handle Ctrl+C
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    go func() { <-sigChan; fmt.Println("\n⚠️  Прервано"); cancel() }()

    // 7. Open database (schema + PRAGMAs in one call)
    repo, err := sqlite.NewSQLiteSalesRepository(defaults.DbPath)
    if err != nil { log.Fatalf("❌ Failed to open database: %v", err) }
    defer repo.Close()

    // 8. Create API client with adaptive rate limiting
    client := wb.New(apiKey)
    rl := defaults.RateLimits
    client.SetRateLimit("my_tool_id", rl.MyEndpoint, rl.MyEndpointBurst,
        rl.MyEndpointApi, rl.MyEndpointApiBurst)
    client.SetAdaptiveParams(0, defaults.AdaptiveProbeAfter, defaults.MaxBackoffSeconds)

    // 9. Download data (mock or real)
    if *mockMode {
        fmt.Println("🧪 MOCK MODE")
        // ... call mock download function ...
    } else {
        // ... call real download function ...
    }

    // 10. Print summary
    fmt.Println("Download complete!")
}

// getAPIKey retrieves API key with priority: env var > config value.
// Detects unresolved ${ENV} placeholders from YAML.
func getAPIKey(cfg *Config) string {
    if key := os.Getenv("WB_API_KEY"); key != "" {
        return key
    }
    apiKey := cfg.WB.APIKey
    if apiKey == "" || strings.HasPrefix(apiKey, "${") {
        return ""
    }
    return apiKey
}

// calculateDateRange computes date range from today.
// days=7 means last 7 complete days, excluding today.
func calculateDateRange(days int) (string, string) {
    now := time.Now()
    endDate := now.AddDate(0, 0, -1).Format("2006-01-02") // yesterday
    beginDate := now.AddDate(0, 0, -days).Format("2006-01-02")
    return beginDate, endDate
}

func loadConfig(path string) (*Config, error) {
    var cfg Config
    if err := config.LoadYAML(path, &cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}
```

**API Key Retrieval Patterns** (two approaches in the codebase):

1. **YAML env expansion** (simpler, used by funnel/funnel-agg): Set `api_key: ${WB_API_KEY}` in config.yaml. `config.LoadYAML()` calls `os.ExpandEnv()`. Read via `cfg.WB.APIKey`. Detect unresolved `${...}` placeholders.

2. **`os.Getenv()` priority chain** (explicit, used by promotion/feedbacks/stocks): Define `getAPIKey()` helper that checks env vars first, falls back to config value. Preferred for downloaders that need multiple env var fallbacks.

### 4. Create downloader.go (or loader.go)

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/ilkoid/poncho-ai/pkg/config"
    "github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
    "github.com/ilkoid/poncho-ai/pkg/wb"
)

type LoadResult struct {
    RecordsSaved int
    RequestsMade int
    Duration     time.Duration
}

type LoaderConfig struct {
    Client *wb.Client
    Repo   *sqlite.SQLiteSalesRepository
    Config config.MyDownloaderConfig
}

func DownloadMyData(ctx context.Context, cfg LoaderConfig) (*LoadResult, error) {
    start := time.Now()
    result := &LoadResult{}

    // ... period splitting, pagination, API calls, repo.SaveXxx() ...
    // Use continue on error (not return) in batch loops

    return result, nil
}
```

**Note**: File naming varies across downloaders:
- `downloader.go` (sales, promotion, feedbacks)
- `loader.go` (funnel, funnel-agg)
- `stocks_loader.go` (stocks)

Choose whichever feels natural for the download logic.

### 5. Create config.yaml

```yaml
# API key — two approaches:
#   Option A (YAML env expansion): api_key: ${WB_API_KEY}     ← simpler
#   Option B (os.Getenv in code):  api_key: ""                 ← for getAPIKey() helper
wb:
  api_key: ${WB_API_KEY}         # Option A: expanded by config.LoadYAML()
  # analytics_api_key: ${WB_API_ANALYTICS_AND_PROMO_KEY}  # For Analytics API endpoints
  timeout: 30s

my_section:
  # Period (Days is NOT defaulted in GetDefaults() — see Common Pitfalls #8)
  days: 7
  # from: "2025-03-01"    # Explicit dates override days
  # to: "2025-03-31"

  # Database
  db_path: my_data.db

  # Rate limiting (two-level adaptive: desired + api floor)
  rate_limits:
    my_endpoint: 3           # desired rate (req/min)
    my_endpoint_burst: 1     # desired burst
    my_endpoint_api: 3       # swagger-documented rate (recovery floor)
    my_endpoint_api_burst: 1 # swagger burst

  # Adaptive tuning (see dev_limits.md)
  adaptive_probe_after: 10
  max_backoff_seconds: 60
```

### 6. Add tests

```go
// e2e_test.go
package main

import (
    "context"
    "testing"

    "github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
    "github.com/ilkoid/poncho-ai/pkg/wb"
)

func TestSaveRegionSales(t *testing.T) {
    dbPath := t.TempDir() + "/test.db"
    repo, err := sqlite.NewSQLiteSalesRepository(dbPath)
    if err != nil { t.Fatal(err) }
    defer repo.Close()

    // Test with sample data (no mock client needed for save tests)
    items := []wb.MyDataType{
        {ID: 1, Name: "test", Value: 100},
    }

    saved, err := repo.SaveMyData(context.Background(), items)
    if err != nil { t.Fatalf("SaveMyData: %v", err) }
    if saved != 1 { t.Errorf("expected 1 saved, got %d", saved) }

    // Verify idempotency (re-save should succeed)
    saved2, err := repo.SaveMyData(context.Background(), items)
    if err != nil { t.Fatalf("SaveMyData (idempotent): %v", err) }
}
```

**Note**: Tests focus on repository save logic with real SQLite (in-memory). Mock HTTP clients are used sparingly — the real value is testing INSERT/UPSERT SQL logic.

---

## Checklist: Adding New Downloader

- [ ] Config type added to `pkg/config/utility.go` (with `GetDefaults()`, **no Days default**)
- [ ] Schema SQL added to `pkg/storage/sqlite/schema.go` (or new `xxx_schema.go`)
- [ ] Schema registered in `initSchema()` in `pkg/storage/sqlite/repository.go`
- [ ] Repository methods added to `pkg/storage/sqlite/` (new file)
- [ ] API types in `pkg/wb/` (or local `types.go` if downloader-specific)
- [ ] `main.go` uses `config.LoadYAML()` for config loading
- [ ] `main.go` uses `sqlite.NewSQLiteSalesRepository()` for single DB open
- [ ] API key via `getAPIKey()` helper or YAML env expansion
- [ ] Adaptive rate limiting: `wb.New(apiKey)` + `SetRateLimit()` + `SetAdaptiveParams()`
- [ ] Ctrl+C handling with `context.WithCancel`
- [ ] `config.yaml` with `${ENV}` expansion
- [ ] CLI flags:
  - **Core** (all): `--config`, `--help`/`-h`
  - **Period** (most): `--days`, `--begin`, `--end`
  - **Optional** (as needed): `--db`, `--mock`, `--resume`, `--clean`
  - **Specific**: `--funnel`, `--statuses`, `--date`, etc.
- [ ] Date calculation: `endDate = yesterday` (exception: funnel uses `today`)
- [ ] Local helpers: `repeat()` and `maskAPIKey()` in `utils.go`
- [ ] E2E test with mock client

---

## Common Pitfalls

1. **Forgetting `SetRateLimit()`**: Without it, adaptive state is created lazily with `apiFloor=0`, limiter never reduces on 429.
2. **Double DB open**: Use single `sqlite.NewSQLiteSalesRepository()` — it handles schema + PRAGMAs.
3. **ToolID mismatch**: `SetRateLimit("tool_A")` + `Get(ctx, "tool_B", ...)` creates separate limiter with no adaptive state.
4. **Missing `_ "github.com/mattn/go-sqlite3"`**: Driver registration required in main.go.
5. **Config not expanding `${ENV}`**: Use `config.LoadYAML()` which handles `os.ExpandEnv()`.
6. **Shared database between utilities**: If multiple downloaders write to the same database file (e.g., `wb-sales.db`), be aware that all data is stored together. To reset a specific utility's data, you can manually delete the relevant tables or use separate database files per utility.
7. **Ignoring 5xx errors**: The WB API may return transient 5xx errors (503, 500, 502, 504). The `wb.Client` automatically retries these with exponential backoff (5s, 10s, 15s). No manual retry logic needed in downloaders.

8. **`Days` default in `GetDefaults()` overrides explicit `from/to`**:

**❌ Wrong** — `GetDefaults()` sets Days=7, which then overrides explicit dates:
```go
// pkg/config/utility.go
func (c *MyConfig) GetDefaults() MyConfig {
    result := *c
    if result.Days == 0 { result.Days = 7 } // ← Always triggers when days not set
    return result
}

// main.go
defaults := cfg.MySection.GetDefaults() // Days is now 7
if defaults.Days > 0 {  // ← Always true now!
    begin, end := calculateDateRange(defaults.Days) // ← Overwrites from/to!
}
```

**✅ Correct** — Don't default Days in `GetDefaults()`, default it in `main.go` only when both From and To are empty:
```go
// pkg/config/utility.go — NO Days default
func (c *MyConfig) GetDefaults() MyConfig {
    result := *c
    // Days is NOT defaulted here — see main.go
    return result
}

// main.go — default Days only when no explicit dates
if defaults.From == "" || defaults.To == "" {
    if defaults.Days == 0 { defaults.Days = 7 }
    begin, end := calculateDateRange(defaults.Days)
    defaults.From = begin
    defaults.To = end
}
```

**Why**: User may set `from: "2025-01-01"` and `to: "2025-01-31"` in config but leave `days: 0`. If `GetDefaults()` sets `days: 7`, the `days > 0` check would recalculate dates, overwriting the explicit ones.

---

## Anti-Patterns: Bugs to Avoid

### 1. SQL Placeholder Mismatch

**❌ Wrong** — Too many placeholders:
```go
stmt, err := tx.PrepareContext(ctx, `
    INSERT OR REPLACE INTO funnel_metrics_daily (
        nm_id, metric_date, open_count, cart_count, order_count,
        buyout_count, add_to_wishlist, order_sum, buyout_sum,
        conversion_add_to_cart, conversion_cart_to_order, conversion_buyout
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)  // 13 placeholders!
`)
```

**✅ Correct** — Match placeholders to column count (12):
```go
stmt, err := tx.PrepareContext(ctx, `
    INSERT OR REPLACE INTO funnel_metrics_daily (
        nm_id, metric_date, open_count, cart_count, order_count,
        buyout_count, add_to_wishlist, order_sum, buyout_sum,
        conversion_add_to_cart, conversion_cart_to_order, conversion_buyout
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)  // 12 placeholders = 12 columns
`)
```

**Error symptom**: `prepare replace statement: 13 values for 12 columns`

**Prevention**:
- Count placeholders explicitly: `strings.Count(sql, "?")` must equal column count
- Always verify after schema changes
- Add unit tests for INSERT statements

---

### 2. io.Reader Cannot Be Reused on Retry

**❌ Wrong** — Reader consumed on first attempt, empty on retry:
```go
// In Post()
return c.doRequest(ctx, toolID, rateLimit, burst, httpRequest{
    method: "POST",
    url:    u.String(),
    body:   strings.NewReader(string(bodyJSON)),  // Reader gets consumed!
}, dest)

// In doRequest() retry loop
for i := 0; i < c.retryAttempts; i++ {
    httpReq, err := http.NewRequestWithContext(ctx, req.method, req.url, req.body)
    // On retry: req.body is at EOF → empty request body → 400 "Request body is empty"
}
```

**✅ Correct** — Store raw bytes, recreate Reader on each retry:
```go
// Store both Reader and raw bytes
type httpRequest struct {
    method    string
    url       string
    body      io.Reader
    bodyBytes []byte  // Store for retry
}

// In Post()
bodyJSON, err := json.Marshal(body)
return c.doRequest(ctx, toolID, rateLimit, burst, httpRequest{
    method:    "POST",
    url:       u.String(),
    body:      strings.NewReader(string(bodyJSON)),
    bodyBytes: bodyJSON,  // Save raw bytes
}, dest)

// In doRequest() retry loop
for i := 0; i < c.retryAttempts; i++ {
    // Recreate Reader from raw bytes on each attempt
    var bodyReader io.Reader
    if len(req.bodyBytes) > 0 {
        bodyReader = bytes.NewReader(req.bodyBytes)  // Fresh Reader!
    } else if req.body != nil {
        bodyReader = req.body
    }
    httpReq, err := http.NewRequestWithContext(ctx, req.method, req.url, bodyReader)
}
```

**Error symptom**: After 429 → retry → `400 Bad Request: "Request body is empty"`

**Prevention**:
- Never reuse `io.Reader` across retries
- Store raw `[]byte` payload, recreate Reader on each attempt
- Test retry logic explicitly (mock 429 response)

---

### 3. Date Calculation Off-By-One

**❌ Wrong** — Includes today (incomplete data):
```go
endDate := time.Now().Format("2006-01-02")  // Today (may be incomplete)
beginDate := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
```

**✅ Correct** — Exclude today (use yesterday as end):
```go
endDate := time.Now().AddDate(0, 0, -1).Format("2006-01-02")  // Yesterday
beginDate := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
```

**Pattern used**: `days: 7` means "last 7 complete days", excluding today.

**Exception**: `download-wb-funnel` includes today (`end = now`) because the Analytics API returns per-day data where today's partial data is still useful for trend analysis.

---

### 4. Missing Error Continuation in Batch Loops

**❌ Wrong** — Stops entire download on single batch error:
```go
for i, batch := range batches {
    productsLoaded, metricsLoaded, err := loadFunnelBatch(ctx, ...)
    if err != nil {
        return nil, err  // Stops here! Remaining batches not processed.
    }
    result.ProductsLoaded += productsLoaded
}
```

**✅ Correct** — Log error, continue with next batch:
```go
for i, batch := range batches {
    productsLoaded, metricsLoaded, err := loadFunnelBatch(ctx, ...)
    if err != nil {
        fmt.Printf("❌ Ошибка: %v\n", err)
        continue  // Skip failed batch, process remaining
    }
    result.ProductsLoaded += productsLoaded
}
```

**Trade-off**: Some batches lost, but download continues. Use `--resume` mode to retry failed batches later.

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

### 429 Rate Limiting (Adaptive + Retry)

429 responses are handled with **adaptive rate limiting + automatic retry** (see [dev_limits.md](dev_limits.md)):

1. **Immediate cooldown**: Limiter reduces to `apiFloor` (swagger-documented rate)
2. **Wait period**: Based on `X-Ratelimit-Retry` header or exponential backoff
3. **Automatic retry**: Request is retried with fresh body (POST requests recreate Reader)
4. **Permanent reduction**: Stays at reduced rate until successful request, then probes back up

**Log output**:
```
RATE_DEBUG get_wb_product_funnel_history: HTTP request at 22:59:33.911
⚠️  Adaptive rate limit: get_wb_product_funnel_history dropped to 3.0 req/min (api floor)
⚠️  429 for get_wb_product_funnel_history, cooling down 20s (server: 4s, attempt 1/3)
    429 detail: Limited by global limiter...
RATE_DEBUG get_wb_product_funnel_history: interval since last HTTP request: 20.492s
RATE_DEBUG get_wb_product_funnel_history: HTTP request at 22:59:54.404
✅ 20 товаров, 160 метрик
```

**Critical**: POST requests MUST store raw body bytes to recreate Reader on retry (see Anti-Pattern #2).

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
