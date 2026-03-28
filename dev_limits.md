# Adaptive Rate Limiting — Developer Guide

## Concept

We use a two-level rate limiting strategy that allows exceeding documented API limits
while safely handling 429 (Too Many Requests) responses.

**Core idea**: start aggressive (`desired`), auto-reduce on 429, recover to safe floor (`api`),
then periodically probe `desired` again to detect if the API limit has increased.

```
desired: 10 req/min    ← start here (can exceed swagger)
api:      3 req/min     ← recovery floor after 429 (swagger-documented)

     ┌──────────────────────────────────────────────────┐
     │  desired (10) ←─── probe after 10 OKs at api ──┐ │
     │    │                                             │ │
     │    ├── 200 OK ──┐                                │ │
     │    │             ├── 200 OK (×5) ──→ api floor (3)│ │
     │    │             │                                │ │
     │    ├── 429 ─────┤                                │ │
     │    │    reduce to 60/X-Ratelimit-Retry          │ │
     │    │    exponential backoff on repeats           │ │
     │    │                                             │ │
     │  api floor (3) ←─── recover after 5 OKs         │ │
     └──────────────────────────────────────────────────┘
```

### Why not just use the swagger limit?

- Swagger limits are often conservative. Real API throughput can be higher.
- Short bursts at higher rates complete faster when the API allows it.
- 429 responses are cheap (no data lost) — we just retry with backoff.
- The adaptive mechanism ensures we never overwhelm the API persistently.

---

## Architecture

### Components

| Component | File | Purpose |
|-----------|------|---------|
| `rateLimitState` | `pkg/wb/client.go` | Tracks desired/api/recovery state per toolID |
| `SetRateLimit()` | `pkg/wb/client.go` | Pre-sets limiter with both desired and api rates |
| `adaptiveReduce()` | `pkg/wb/client.go` | Handles 429: reduces limiter, returns backoff duration |
| `adaptiveRecoverOK()` | `pkg/wb/client.go` | Tracks successes, restores to api floor |
| `doRequest()` | `pkg/wb/client.go` | HTTP retry loop with adaptive logic (for most endpoints) |
| `GetStream()` | `pkg/wb/client.go` | Streaming variant (for large responses, e.g. fullstats) |
| `PromotionRateLimits` | `pkg/config/utility.go` | Config struct with desired/api/burst per endpoint |
| `applyRateLimits()` | `cmd/.../main.go` | Wires YAML config to client |

### Data Flow

```
config.yaml                          pkg/wb/client.go                     HTTP
───────────                          ───────────────                     ────
fullstats: 10  (desired)
fullstats_api: 3  (swagger floor)
        │
        ▼
applyRateLimits()
  → SetRateLimit("get_campaign_fullstats", 10, 1, 3, 1)
      → limiters["get_campaign_fullstats"] = 10/60 req/s (desired)
      → adaptive["get_campaign_fullstats"] = {
            desiredLimit: 10/60,
            apiFloor:     3/60,     ← recovery target
          }
        │
        ▼
GetCampaignFullstats(..., 10, 1)
  → GetStream(..., 10, 1)
      → getOrCreateLimiter() → returns pre-set limiter (10/60)
      → limiter.Wait(ctx)     ← throttling here
      → HTTP request
          │
          ├── 200 → adaptiveRecoverOK()
          │            → consecutiveOK++
          │            → if 5 OKs → restore limiter to apiFloor (3/60)
          │
          └── 429 → adaptiveReduce(retryAfterSec)
                       → limiter reduced to 60/retryAfterSec
                       → exponential backoff: retryAfter × 2^(n-1), cap 60s
                       → retry
```

---

## YAML Configuration

### Pattern

```yaml
rate_limits:
  # endpoint_name:     desired rate (req/min) — can exceed api
  # endpoint_name_burst:  desired burst
  # endpoint_name_api:   swagger-documented rate (req/min) — recovery floor
  # endpoint_name_api_burst: swagger-documented burst
```

### Rules

1. **If `desired` not set** → defaults to `api` (safe, no exceeding swagger)
2. **If `api` not set** → defaults to swagger value in `GetDefaults()`
3. **If `burst` not set** → defaults to corresponding `*_api_burst`
4. **If `*_api_burst` not set** → defaults to sensible value in `GetDefaults()`

### Example (current config)

```yaml
rate_limits:
  # /adv/v1/promotion/count (5 req/sec) — no exceeding needed
  promotion_count: 300
  promotion_count_burst: 5
  promotion_count_api: 300
  promotion_count_api_burst: 5

  # /api/advert/v2/adverts (5 req/sec) — no exceeding needed
  advert_details: 300
  advert_details_burst: 5
  advert_details_api: 300
  advert_details_api_burst: 5

  # /adv/v3/fullstats (swagger: 3 req/min) — aggressive exceeding
  fullstats: 10              # try 10 req/min (3.3× swagger)
  fullstats_burst: 1
  fullstats_api: 3           # recover to 3 req/min after 429
  fullstats_api_burst: 1
```

---

## How to Add Adaptive Rate Limiting to a New Endpoint

### Step 1: Add config fields to `PromotionRateLimits`

File: `pkg/config/utility.go`

```go
type PromotionRateLimits struct {
    // ... existing fields ...

    // New endpoint
    NewEndpoint          int `yaml:"new_endpoint"`           // desired (default: api value)
    NewEndpointBurst     int `yaml:"new_endpoint_burst"`      // desired burst
    NewEndpointApi       int `yaml:"new_endpoint_api"`        // swagger rate
    NewEndpointApiBurst  int `yaml:"new_endpoint_api_burst"`  // swagger burst
}
```

### Step 2: Add defaults in `GetDefaults()`

File: `pkg/config/utility.go`

```go
// In GetDefaults():

// New endpoint
if result.RateLimits.NewEndpointApi == 0 {
    result.RateLimits.NewEndpointApi = 100 // swagger limit
}
if result.RateLimits.NewEndpoint == 0 {
    result.RateLimits.NewEndpoint = result.RateLimits.NewEndpointApi
}
if result.RateLimits.NewEndpointApiBurst == 0 {
    result.RateLimits.NewEndpointApiBurst = 5
}
if result.RateLimits.NewEndpointBurst == 0 {
    result.RateLimits.NewEndpointBurst = result.RateLimits.NewEndpointApiBurst
}
```

### Step 3: Wire to client in `applyRateLimits()`

File: `cmd/data-downloaders/download-wb-promotion/main.go`

```go
func applyRateLimits(client *wb.Client, rl config.PromotionRateLimits) {
    // ... existing ...
    client.SetRateLimit("new_endpoint", rl.NewEndpoint, rl.NewEndpointBurst,
                       rl.NewEndpointApi, rl.NewEndpointApiBurst)
}
```

### Step 4: Use in API method

File: `pkg/wb/promotion.go` (or wherever the API method lives)

```go
func (c *Client) GetNewEndpoint(ctx context.Context, rateLimit, burst int) (Result, error) {
    // For large responses, use GetStream (avoids io.ReadAll)
    var result Result
    err := c.GetStream(ctx, "new_endpoint", endpoint, rateLimit, burst, path, nil, &result)
    return result, err
}
```

**Important**: pass `rateLimit` and `burst` from the caller. The limiter is pre-set
by `SetRateLimit` — the params here are fallback if `SetRateLimit` wasn't called.

### Step 5: Add to YAML config

```yaml
rate_limits:
  new_endpoint: 50          # desired (try 50 req/min)
  new_endpoint_burst: 5
  new_endpoint_api: 20      # swagger: 20 req/min (recovery floor)
  new_endpoint_api_burst: 3
```

---

## Adaptive Behavior Details

### On 429 (Too Many Requests)

```
429 received with X-Ratelimit-Retry: 20

1. Compute actual rate: 60/20 = 3 req/min
2. Reduce limiter if 3 < current rate
3. Exponential backoff: 20s × 2^(consecutive429-1)
   - 1st 429: wait 20s
   - 2nd 429: wait 40s
   - 3rd 429: wait 60s (capped)
   - 4th+ 429: wait 60s (capped)
4. Log: "⚠️  Adaptive rate limit: tool_id reduced to 3.0 req/min (was 10.0)"
```

### On Recovery (consecutive successes)

Two-phase recovery cycle:

```
Phase 1 — After 429: 5 consecutive OKs → restore to api floor (3 req/min)
  Log: "✅ Adaptive rate limit: tool_id restored to 3.0 req/min (api floor)"

Phase 2 — At api floor: 10 consecutive OKs → probe desired rate again (10 req/min)
  Log: "🔄 Adaptive rate limit: tool_id probing 10.0 req/min (desired)"
  → If succeeds: stays at desired (API throughput is higher than swagger!)
  → If 429 again: back to Phase 1 (reduce → recover → probe → ...)
```

### Why recovery goes to api floor first, then probes desired

Recovering directly to `desired` after 429 would immediately cause another 429.
The two-phase cycle ensures stable operation while periodically probing for higher
throughput. If the API limit changes (e.g., WB increases it), the probe succeeds
and we automatically run at the higher rate.

---

## Constants

### Adaptive Tuning (configurable via YAML)

| YAML field | Default | Client field | Purpose |
|-----------|--------|-------------|---------|
| `adaptive_recover_after` | 5 | `adaptiveRecoverAfter` | Consecutive OKs to restore to api floor after 429 |
| `adaptive_probe_after` | 10 | `adaptiveProbeAfter` | Consecutive OKs at api floor before probing desired again |
| `max_backoff_seconds` | 60 | `maxBackoffSeconds` | Cap for exponential backoff |

Set via `client.SetAdaptiveParams()` or YAML config. Zero values keep defaults.

File: `pkg/wb/client.go`

---

## Logging Examples

### Aggressive start, 429, recovery, probe

```
⚠️  429 Rate limited (20) for get_campaign_fullstats, waiting 20s (attempt 1/3)...
⚠️  Adaptive rate limit: get_campaign_fullstats reduced to 3.0 req/min (was 10.0)
✅  Adaptive rate limit: get_campaign_fullstats restored to 3.0 req/min (api floor)
... 10 successful requests at api floor ...
🔄 Adaptive rate limit: get_campaign_fullstats probing 10.0 req/min (desired)
→ if no 429: stays at 10.0 for all remaining requests
→ if 429: reduced again, cycle repeats
```

### No 429 (desired rate works)

```
(no output — desired rate is within actual API capacity)
```

---

## Thread Safety

All adaptive state is protected by `c.mu` (sync.RWMutex in `Client`).
The rate limiter itself (`golang.org/x/time/rate.Limiter`) is also thread-safe.
Multiple goroutines can call API methods concurrently.

---

## Pitfalls & Common Mistakes

### 1. Forgetting to call `SetRateLimit()` — DEADLOCK on 429

**Symptom**: program hangs forever after receiving a 429 response.

**Root cause**: if `SetRateLimit()` was never called for a toolID, the adaptive state
is created lazily in `adaptiveReduce()` with zero values (`apiFloor: 0`). When recovery
kicks in after 5 OKs, it calls `limiter.SetLimit(rate.Limit(0))` which means 0 events/sec
→ `limiter.Wait(ctx)` blocks forever.

```
❌ WRONG — no adaptive setup:
wbClient, _ := wb.NewFromConfig(wbCfg)
// Missing: wbClient.SetRateLimit(...)
// Missing: wbClient.SetAdaptiveParams(...)
client.Post(ctx, "tool_id", url, 3, 3, path, body, &result)

✅ CORRECT:
wbClient, _ := wb.NewFromConfig(wbCfg)
wbClient.SetRateLimit("tool_id", desiredRate, desiredBurst, apiRate, apiBurst)
wbClient.SetAdaptiveParams(recoverAfter, probeAfter, maxBackoff)
```

**Defense**: `adaptiveRecoverOK()` has a guard `if state.apiFloor > 0` that skips
the restore when adaptive state wasn't properly initialized. This prevents the deadlock
but means the limiter stays at the reduced rate permanently (no recovery, no probing).

### 2. `NewFromConfig()` ignores `RateLimit`/`BurstLimit` fields

The `WBConfig.RateLimit` and `WBConfig.BurstLimit` fields are **not used** by the client.
They are stored in the config struct but `NewFromConfig()` never reads them. Actual rate
limiting is done per-request via `getOrCreateLimiter()` with params passed to
`Get()`/`Post()`/`GetStream()`.

```
wbCfg := config.WBConfig{
    RateLimit:  10,   // ← this is DEAD config, never used by client
    BurstLimit: 5,    // ← this too
}
wbClient, _ := wb.NewFromConfig(wbCfg)

// Actual rate limiting happens here (or via pre-set limiter from SetRateLimit):
client.Post(ctx, "tool_id", url, 3, 3, path, body, &result)  // ← 3,3 is what matters
```

**Rule**: if you want adaptive rate limiting, always call `SetRateLimit()` explicitly.
The `WBConfig.RateLimit` field is only useful for `New()` (not `NewFromConfig`).

### 3. Hardcoded params in API methods are fallback, not actual rate

`Get()` and `Post()` accept `rateLimit` and `burst` params, but these are **fallback values**.
If `SetRateLimit()` was called first, `getOrCreateLimiter()` returns the pre-set limiter
and completely ignores the passed params:

```go
// client.go:543-559
func (c *Client) getOrCreateLimiter(toolID string, rateLimit int, burst int) *rate.Limiter {
    if limiter, exists := c.limiters[toolID]; exists {
        return limiter  // ← pre-set limiter wins, params ignored
    }
    // Only creates new limiter if SetRateLimit() wasn't called (fallback)
}
```

This is by design — it allows API methods to work both with and without adaptive setup.
But it means hardcoded values like `c.Get(ctx, "tool_id", url, 300, 5, ...)` are harmless
when `SetRateLimit()` was called before.

### 4. Config fields exist but aren't wired → dead code

Adding adaptive fields to the config struct (`FunnelRateLimit`, `FunnelRateLimitApi`, etc.)
and `GetDefaults()` cascade is only half the job. If the downloader's `main.go` doesn't
call `SetRateLimit()` with those values, the fields are dead code.

**Checklist when adding a new downloader:**
- [ ] Config struct has `desired`, `desired_burst`, `api`, `api_burst` fields
- [ ] `GetDefaults()` has cascade: api → desired, api_burst → desired_burst
- [ ] `main.go` calls `SetRateLimit(toolID, desired, desiredBurst, api, apiBurst)`
- [ ] `main.go` calls `SetAdaptiveParams(recoverAfter, probeAfter, maxBackoff)`
- [ ] YAML config has all adaptive fields (not just `rate_limit`/`burst`)
- [ ] Displayed rate limit in header matches actual rate being used

### 5. Phase 2 probe without `probed` flag — log spam every N requests

**Symptom**: "🔄 Adaptive rate limit: tool_id probing X req/min (desired)" appears
every ~10 requests even though no 429 occurred and the rate hasn't changed.

**Root cause**: Phase 2 condition `!reduced && consecutiveOK >= probeAfter && desiredLimit > apiFloor`
is always true when `desired != api`, regardless of whether the limiter is already at desired rate.
After a successful probe, `consecutiveOK` resets to 0, counts to `probeAfter` again, and
triggers another no-op probe — indefinitely.

```
❌ BEFORE (no probed flag):
desired (400) → works → 10 OKs → probe 400 (no-op, already there) → 10 OKs → probe 400 → ...

✅ AFTER (probed flag):
desired (400) → works → (no log spam)
     │
     └── 429 → reduce → 5 OKs → api floor → 10 OKs → probe (400), probed=true
                                                              │
                                                              └── works (no re-probe)
                                                                   │
                                                                   └── 429 → probed=false → cycle
```

**Fix**: `rateLimitState` has a `probed` bool field. Phase 2 sets it to `true` after probing.
`adaptiveReduce()` resets it to `false` on 429. This makes probe a one-shot event per 429 cycle.
