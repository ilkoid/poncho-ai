# Adaptive Rate Limiting — Developer Guide

## Concept

Two-level rate limiting with **minimum interval guarantee between HTTP requests**:

**For swagger-documented endpoints:**
```
desired: 3 req/min     ← equals swagger (no aggressive starting rate)
api:      3 req/min    ← same as desired (no reduction needed)
cooldown: 20s          ← max(server hint, 60/3) = respects X-Ratelimit-Retry
min_interval: 20s      ← GUARANTEED between HTTP requests (not just limiter.Wait calls)

3 req/min ──→ 429 ──→ cooldown ──→ stay at 3 req/min
```

**For undocumented/uncertain endpoints:**
```
desired: 4 req/min     ← start aggressive (can exceed swagger)
api:      3 req/min    ← immediate drop on 429 (swagger limit)
cooldown: max(1s, 20s) ← respect X-Ratelimit-Retry, but ensure minimum interval
min_interval: computed ← 60/rateLimit, ensures pacing even after retries

desired (4) ──→ 429 ──→ api floor (3) ──→ stay forever
```

**Why minimum interval guarantee?**

The standard `rate.Limiter.Wait()` measures time from the **last `Wait()` call**, not from the **last HTTP request**. This causes issues after retries/429:

```
Request 1: Wait() (0s) → HTTP (t=0) → 429!
Cooldown: 20s
Retry:    Wait() (0s, token ready!) → HTTP (t=20) ← Only 20s from Request 1 ✓

But without cooldown:
Request 1: Wait() (0s) → HTTP (t=0) → error
Retry:    Wait() (0s) → HTTP (t=0.1) ← Only 0.1s between HTTP requests! ✗
```

**Solution:** After `limiter.Wait()`, we check `time.Since(lastHTTPRequest)` and wait additional time if needed:
- `3 req/min` → `minInterval = 20s`
- If `sinceLast < 20s`, wait `20s - sinceLast`
- This guarantees **minimum interval between actual HTTP requests**

---

## Architecture

### Components

| Component | Location | Purpose |
|-----------|----------|---------|
| `rateLimitState` | `pkg/wb/client.go:163` | Tracks desired/api rates per toolID |
| `SetRateLimit()` | `pkg/wb/client.go:623` | Pre-sets limiter with both rates |
| `adaptiveReduce()` | `pkg/wb/client.go:713` | On 429: drops to api floor, returns cooldown |
| `adaptiveRecoverOK()` | `pkg/wb/client.go:762` | Tracks successes (no probing) |
| `doRequest()` | `pkg/wb/client.go:332` | HTTP retry loop with min interval check |
| `GetStream()` | `pkg/wb/client.go:495` | Streaming variant with min interval check |

### Data Flow

```
config.yaml                     pkg/wb/client.go                    HTTP
───────────                     ───────────────                    ────
fullstats: 4   (desired)
fullstats_api: 3 (swagger floor)
        │
        ▼
applyRateLimits()
  → SetRateLimit("get_campaign_fullstats", 4, 1, 3, 1)
      → limiters["get_campaign_fullstats"] = 4/60 req/s
      → adaptive["get_campaign_fullstats"] = {
            desiredLimit: 4/60,
            apiFloor:     3/60,
          }
        │
        ▼
GetCampaignFullstats(..., 4, 1)
  → GetStream(..., 4, 1)
      → getOrCreateLimiter() → returns pre-set limiter
      → limiter.Wait(ctx)     ← throttling here
      → HTTP request
          │
          ├── 200 → adaptiveRecoverOK()
          │
          └── 429 → adaptiveReduce()
                       → limiter dropped to 3/60
                       → wait = 60/3 = 20s
                       → retry (consumes 1 token)
          │
          └── next request → limiter.Wait() waits 20s
```

---

## YAML Configuration

### Pattern

```yaml
rate_limits:
  # endpoint_name:     desired rate (req/min) — can exceed api
  # endpoint_name_api:   swagger-documented rate (req/min)
```

### Rules

1. If `desired` not set → defaults to `api`
2. If `api` not set → defaults to swagger value in code
3. Burst defaults to corresponding `*_api_burst`

### Example

```yaml
rate_limits:
  # /adv/v1/promotion/count (5 req/sec = 300 req/min)
  promotion_count: 300       # desired equals swagger for documented endpoints
  promotion_count_api: 300   # swagger: 300 req/min

  # /adv/v3/fullstats (swagger: 3 req/min, 20s interval, burst 1)
  # See docs/wb_api_swagger/08-promotion.yaml for documented limits
  fullstats: 3               # swagger-documented (no adaptive reduction needed)
  fullstats_api: 3           # same as desired for documented endpoints

  # For undocumented endpoints, you can try aggressive rates:
  # some_unknown_endpoint: 10    # try 10 req/min
  # some_unknown_endpoint_api: 5 # drop to 5 req/min on 429

  # Adaptive tuning
  max_backoff_seconds: 30    # cap for cooldown (very low rates)
```

---

## Adding Adaptive Rate Limiting to a New Endpoint

### Step 1: Add config fields

File: `pkg/config/utility.go`

```go
type YourRateLimits struct {
    // New endpoint
    NewEndpoint       int `yaml:"new_endpoint"`
    NewEndpointApi    int `yaml:"new_endpoint_api"`
}
```

### Step 2: Add defaults

File: `pkg/config/utility.go`, in `GetDefaults()`:

```go
if result.RateLimits.NewEndpointApi == 0 {
    result.RateLimits.NewEndpointApi = 100 // swagger limit
}
if result.RateLimits.NewEndpoint == 0 {
    result.RateLimits.NewEndpoint = result.RateLimits.NewEndpointApi
}
```

### Step 3: Wire to client

File: `cmd/your-downloader/main.go`

```go
func applyRateLimits(client *wb.Client, rl config.YourRateLimits) {
    client.SetRateLimit("new_endpoint", rl.NewEndpoint, 1,
                       rl.NewEndpointApi, 1)
}
```

### Step 4: Use in API method

File: `pkg/wb/your_api.go`

```go
func (c *Client) GetNewEndpoint(ctx context.Context, rateLimit, burst int) (Result, error) {
    var result Result
    err := c.GetStream(ctx, "new_endpoint", baseURL, rateLimit, burst, path, nil, &result)
    return result, err
}
```

**Critical**: `toolID` in `SetRateLimit()` must match `toolID` in `Get()` calls.

---

## Behavior Details

### Minimum Interval Between HTTP Requests

**Critical fix:** After `limiter.Wait()` returns, we check `time.Since(lastHTTPRequest)` and wait additional time if needed:

```go
minInterval := 60s / rateLimit  // 3/min → 20s
if !lastRequestTime.IsZero() {
    sinceLastReq := time.Since(lastRequestTime)
    if sinceLastReq < minInterval {
        additionalWait := minInterval - sinceLastReq
        time.Sleep(additionalWait)
    }
}
```

**Why this is needed:** `limiter.Wait()` measures time from the **last `Wait()` call**, not from the **last HTTP request**. After retries or network errors, this matters:

```
Normal flow:
  Request 1: Wait(0s) → HTTP(t=0) → success
  Request 2: Wait(20s) → HTTP(t=20) → success ✓

Without minInterval check (after error):
  Request 1: Wait(0s) → HTTP(t=0) → error
  Retry:     Wait(0s, token ready!) → HTTP(t=0.1) ← Only 0.1s between HTTP! ✗

With minInterval check:
  Request 1: Wait(0s) → HTTP(t=0) → error
  Retry:     Wait(0s) → check: sinceLast=0.1s < 20s → wait 19.9s → HTTP(t=20) ✓
```

### On 429 (Too Many Requests)

```
429 received with X-Ratelimit-Retry: 1

1. Immediately drop limiter to api floor (e.g., 3 req/min)
2. Calculate cooldown: max(server hint, 60/rate)
   - max(1, 60/3) = max(1, 20) = 20 seconds
3. Wait 20 seconds (capped at max_backoff_seconds)
4. After cooldown, limiter.Wait() may wait 0s (token ready)
5. MINIMUM INTERVAL CHECK: ensure 20s since last HTTP request
6. Retry request
7. All subsequent requests stay at api floor forever
```

**Key behavior:**
- The server's `X-Ratelimit-Retry` hint is respected, but we enforce a minimum cooldown
- The minInterval check ensures proper pacing even after retries/cooldowns
- This prevents "burst" requests that could trigger additional 429s

### On 5xx (Server Errors)

When the WB API returns a 5xx status code (500-599), the client automatically retries with exponential backoff:

```
Request: 200 OK → success
Request: 503 Service Unavailable → retry after 5s
Retry 1: 503 Service Unavailable → retry after 10s
Retry 2: 200 OK → success
```

**Backoff schedule:**
- Retry 1: 5 seconds
- Retry 2: 10 seconds
- Retry 3: 15 seconds (if `retry_attempts: 4`)

**Common 5xx errors from WB API:**
| Status | Origin | Meaning |
|--------|--------|---------|
| `503` | `s2s-api-auth-adv` | Upstream authentication service failure |
| `500` | `camp-api-public-cache` | Internal RPC timeout (e.g., `GetStatsDailyNmApp: DeadlineExceeded`) |
| `502` | Any | Bad gateway (upstream service unavailable) |
| `504` | Any | Gateway timeout |

**Log output:**
```
⚠️  Transient 5xx (503) for get_campaign_fullstats, retrying in 5s... (attempt 2/3)
⚠️  Transient 5xx (503) for get_campaign_fullstats, retrying in 10s... (attempt 3/3)
✅ 462 daily, 1392 app, 4567 nm, 0 booster (api 24.971s, flatten 2ms, db 504ms)
```

**Key differences from 429 handling:**
| Feature | 429 (Rate Limit) | 5xx (Server Error) |
|---------|------------------|-------------------|
| Action | Drop rate to `apiFloor` permanently | Retry with exponential backoff |
| Cooldown | Based on `X-Ratelimit-Retry` header | Fixed exponential backoff |
| State change | Rate limiter reduced | Rate limiter unchanged |
| Retry count | Uses `retry_attempts` | Uses `retry_attempts` |

### Cooldown Examples

| API Floor Rate | Cooldown (`60/apiRate`) |
|----------------|------------------------|
| 3 req/min | 20 seconds |
| 20 req/min | 3 seconds |
| 300 req/min | 200ms |

---

## Constants

| YAML field | Default | Purpose |
|-----------|--------|---------|
| `max_backoff_seconds` | 60 | Cap for cooldown (very low rates) |
| `adaptive_probe_after` | 10 | **Deprecated** — no longer used |

File: `pkg/wb/client.go:265`

---

## Common Pitfalls

### 1. Forgetting `SetRateLimit()` — silent failure

**Symptom**: 429 responses, rate never drops.

**Cause**: No adaptive state (`apiFloor=0`), limiter stays at current rate.

```go
// ❌ WRONG
wbClient, _ := wb.NewFromConfig(cfg)
client.Get(ctx, "tool_id", url, 3, 3, path, nil, &result)

// ✅ CORRECT
wbClient, _ := wb.NewFromConfig(cfg)
wbClient.SetRateLimit("tool_id", 4, 1, 3, 1)
client.Get(ctx, "tool_id", url, 3, 3, path, nil, &result)
```

### 2. ToolID mismatch — silent failure

**Symptom**: 429 responses, adaptive reduction never triggers.

**Cause**: `SetRateLimit("tool_A")` creates state for one toolID, `Get("tool_B")` uses a different limiter.

```go
// ❌ WRONG — toolIDs don't match
client.SetRateLimit("get_campaign_fullstats", 4, 1, 3, 1)
client.Get(ctx, "get_wb_campaign_fullstats2", ...)  // Different!

// ✅ CORRECT — toolIDs match
client.SetRateLimit("get_campaign_fullstats", 4, 1, 3, 1)
client.Get(ctx, "get_campaign_fullstats", ...)
```

**Verification**:
```bash
# Find SetRateLimit calls
grep -rn "SetRateLimit" cmd/

# Find API call toolIDs (same codebase)
grep -rn 'client.Get\|client.Post\|client.GetStream' cmd/
```

**Better**: Define toolID constants to prevent typos.

### 3. `NewFromConfig()` ignores `WBConfig.RateLimit`

**Symptom**: Config values have no effect.

**Cause**: The `WBConfig.RateLimit` field is never read by `NewFromConfig()`.

```go
wbCfg := config.WBConfig{
    RateLimit:  10,   // ← DEAD config, never used
}
wbClient, _ := wb.NewFromConfig(wbCfg)

// Actual rate limiting is via SetRateLimit() or Get() params
wbClient.SetRateLimit("tool_id", 4, 1, 3, 1)  // ← This works
```

### 4. Fallback params in API methods

`Get()` and `Post()` accept `rateLimit` and `burst` params, but if `SetRateLimit()` was called first, these params are **ignored**:

```go
func (c *Client) getOrCreateLimiter(toolID string, rateLimit int, burst int) *rate.Limiter {
    if limiter, exists := c.limiters[toolID]; exists {
        return limiter  // ← pre-set limiter wins, params ignored
    }
    // Only creates new limiter if SetRateLimit() wasn't called
}
```

This is by design — allows API methods to work with or without adaptive setup.

### 5. Config fields exist but aren't wired

**Symptom**: YAML config changes have no effect.

**Cause**: Fields added to config struct but `main.go` doesn't call `SetRateLimit()`.

**Checklist**:
- [ ] Config struct has `desired`, `api` fields
- [ ] `GetDefaults()` has cascade: api → desired
- [ ] `main.go` calls `SetRateLimit()` for each toolID
- [ ] ToolIDs in `SetRateLimit()` match API calls exactly

---

## Thread Safety

All adaptive state protected by `c.mu` (sync.RWMutex).
The rate limiter itself (`golang.org/x/time/rate.Limiter`) is also thread-safe.
Multiple goroutines can call API methods concurrently.

---

## Testing

File: `pkg/wb/client_adaptive_test.go`

```go
// SetHTTPClient() — inject mock for testing
client.SetHTTPClient(mockHTTP)

// RateLimiters() — inspect current rate for assertions
rates := client.RateLimiters()
```

Key tests:
- `TestAdaptiveRateLimit_ReducesOn429` — verifies rate drops on 429
- `TestAdaptiveRateLimit_SilentFailureOnMismatch` — toolID mismatch pitfall
- `TestAdaptiveRateLimit_SilentFailureWithoutSetup` — missing `SetRateLimit()` pitfall
