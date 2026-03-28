# Adaptive Rate Limiting — Developer Guide

## Concept

We use a two-level rate limiting strategy that allows exceeding documented API limits
while safely handling 429 (Too Many Requests) responses.

**Core idea**: start aggressive (`desired`), on 429 immediately drop to safe floor (`api`),
wait a calculated cooldown (`60/apiRate` seconds), then after N stable OKs probe `desired` again.

```
desired: 4 req/min     ← start here (can exceed swagger)
api:      3 req/min    ← immediate drop on 429 (swagger-documented)
cooldown: 20s          ← 60/3 = one full interval at api floor

     ┌──────────────────────────────────────────────────┐
     │  desired (4) ←─── probe after N OKs at api ────┐ │
     │    │                                             │ │
     │    ├── 200 OK ──┐                                │ │
     │    │             │                                │ │
     │    ├── 429 ─────┤                                │ │
     │    │    IMMEDIATE drop to api floor (3)          │ │
     │    │    wait 60/3 = 20s (calculated, not header) │ │
     │    │    retry (limiter has exactly 1 token)       │ │
     │    │    after N OKs at api floor → probe (4)     │ │
     │    │                                             │ │
     └──────────────────────────────────────────────────┘
```

### Why not just use the swagger limit?

- Swagger limits are often conservative. Real API throughput can be higher.
- Short bursts at higher rates complete faster when the API allows it.
- 429 responses are cheap (no data lost) — we just drop to safe floor and retry.
- The adaptive mechanism ensures we never irritate the server persistently.

### Why calculated cooldown instead of `X-Ratelimit-Retry`?

The header `X-Ratelimit-Retry` tells how long to wait, but it's **unreliable**:
- Often too short (1-6 seconds) — causes repeated 429s
- Not aligned with our rate limiter's token bucket
- Doesn't account for our desired/api rate configuration

**Our approach**: calculate cooldown as `1/apiFloor` seconds (or `60/apiRatePerMin`).

For `apiFloor=3/min` → cooldown = 60/3 = 20 seconds.

This guarantees:
1. The API's rate counter has fully reset
2. Our token bucket has exactly 1 token available for retry
3. No "token stealing" from subsequent requests
4. Predictable, server-independent behavior

```
❌ OLD (header-based):
429 → wait 6s (from X-Ratelimit-Retry) → limiter.Wait(+14s) → retry
     ↑ retry "steals" token, next request waits extra → bursts → 429s

✅ NEW (calculated):
429 → wait 20s (calculated from api floor) → limiter.Wait(0s, token ready) → retry
     ↑ retry consumes accumulated token, next request waits 20s → stable
```

---

## Architecture

### Components

| Component | File | Purpose |
|-----------|------|---------|
| `rateLimitState` | `pkg/wb/client.go` | Tracks desired/api/probe state per toolID |
| `SetRateLimit()` | `pkg/wb/client.go` | Pre-sets limiter with both desired and api rates |
| `adaptiveReduce()` | `pkg/wb/client.go` | On 429: drops limiter to api floor, returns calculated cooldown |
| `adaptiveRecoverOK()` | `pkg/wb/client.go` | After N OKs at api floor: probes desired rate |
| `doRequest()` | `pkg/wb/client.go` | HTTP retry loop with adaptive logic (for most endpoints) |
| `GetStream()` | `pkg/wb/client.go` | Streaming variant (for large responses, e.g. fullstats) |
| `PromotionRateLimits` | `pkg/config/utility.go` | Config struct with desired/api/burst per endpoint |
| `applyRateLimits()` | `cmd/.../main.go` | Wires YAML config to client |

### Data Flow

```
config.yaml                          pkg/wb/client.go                     HTTP
───────────                          ───────────────                     ────
fullstats: 4   (desired)
fullstats_api: 3 (swagger floor)
        │
        ▼
applyRateLimits()
  → SetRateLimit("get_campaign_fullstats", 4, 1, 3, 1)
      → limiters["get_campaign_fullstats"] = 4/60 req/s (desired)
      → adaptive["get_campaign_fullstats"] = {
            desiredLimit: 4/60,
            desiredBurst: 1,
            apiFloor:     3/60,     ← immediate drop target on 429
          }
        │
        ▼
GetCampaignFullstats(..., 4, 1)
  → GetStream(..., 4, 1)
      → getOrCreateLimiter() → returns pre-set limiter (4/60)
      → limiter.Wait(ctx)     ← throttling here
      → HTTP request
          │
          ├── 200 → adaptiveRecoverOK()
          │            → consecutiveOK++
          │            → if N OKs at api floor → probe desired (4/60)
          │
          └── 429 → adaptiveReduce(serverRetrySec)
                       → limiter dropped to apiFloor (3/60)
                       → wait = 1/apiFloor = 20s (calculated, not serverRetrySec)
                       → limiter.Wait() returns immediately (1 token ready)
                       → retry consumes token → success
          │
          └── next request → limiter.Wait() waits 20s for next token
```

**Key insight**: after the calculated cooldown, `limiter.Wait()` returns immediately because exactly one token has accumulated. The retry consumes it, and the next request properly waits for a new token.

---

## YAML Configuration

### Pattern

```yaml
rate_limits:
  # endpoint_name:     desired rate (req/min) — can exceed api
  # endpoint_name_burst:  desired burst
  # endpoint_name_api:   swagger-documented rate (req/min) — immediate drop target on 429
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

  # /adv/v3/fullstats (swagger: 3 req/min) — moderate exceeding
  fullstats: 4               # try 4 req/min (> swagger 3)
  fullstats_burst: 1
  fullstats_api: 3           # immediate drop to 3 req/min on 429
  fullstats_api_burst: 1

  # Adaptive tuning
  adaptive_probe_after: 15   # OKs at api floor before probing desired
  max_backoff_seconds: 90    # cap for cooldown (for very low api rates)
```

---

## How to Add Adaptive Rate Limiting to a New Endpoint

### Step 1: Add config fields to config struct

File: `pkg/config/utility.go` (or appropriate config package)

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

File: `cmd/data-downloaders/download-wb-promotion/main.go` (or similar)

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
  new_endpoint_api: 20      # swagger: 20 req/min (immediate drop on 429)
  new_endpoint_api_burst: 3
```

---

## Adaptive Behavior Details

### On 429 (Too Many Requests)

```
429 received with X-Ratelimit-Retry: 6 (logged for reference, but NOT used for wait)

1. Immediately drop limiter to api floor (e.g., 3 req/min)
2. Calculate cooldown: 1/apiFloor = 1/(3/60) = 20 seconds
3. Wait 20 seconds (capped at max_backoff_seconds if configured)
4. limiter.Wait() returns immediately (token accumulated during wait)
5. Retry request (consumes token)
6. Next request waits 20s for next token
```

**Why this works**: The cooldown duration matches the token bucket's refill rate.
After waiting `1/apiFloor` seconds, exactly one token is available. `limiter.Wait()`
returns immediately for the retry, consumes that token, and subsequent requests
properly wait for new tokens. No bursts, no token stealing, just smooth pacing.

### On Recovery (consecutive successes at api floor)

After `adaptive_probe_after` consecutive OKs at api floor, probe desired rate:

```
At api floor (3 req/min):
  Request every 20s → OK, OK, OK, ... (N OKs) → probe desired (4 req/min)
  Log: "🔄 Adaptive rate limit: tool_id probing 4.0 req/min (desired)"
  → If succeeds: stays at desired for all remaining requests
  → If 429: immediate drop to api floor again, cycle repeats
```

### Cooldown Calculation Examples

| API Floor Rate | Cooldown (`1/apiFloor`) | Token Bucket After Cooldown |
|----------------|-------------------------|----------------------------|
| 3 req/min (0.05 req/s) | 20 seconds | 1.0 token (exactly) |
| 20 req/min (0.33 req/s) | 3 seconds | 1.0 token (exactly) |
| 300 req/min (5 req/s) | 200ms | 1.0 token (exactly) |

**Alignment**: The cooldown is deliberately chosen to match the rate limiter's
token accumulation. After waiting this duration, `limiter.Wait()` will have
exactly one token available and return immediately.

---

## Constants

### Adaptive Tuning (configurable via YAML)

| YAML field | Default | Client field | Purpose |
|-----------|--------|-------------|---------|
| `adaptive_probe_after` | 10 | `adaptiveProbeAfter` | Consecutive OKs at api floor before probing desired |
| `max_backoff_seconds` | 60 | `maxBackoffSeconds` | Cap for cooldown (for very low api rates) |

Set via `client.SetAdaptiveParams()` or YAML config. Zero values keep defaults.

File: `pkg/wb/client.go`

---

## Logging Examples

### Aggressive start, 429, drop, probe

```
⚠️  429 for get_campaign_fullstats, cooling down 20s (server: 6s, attempt 1/3)...
⚠️  Adaptive rate limit: get_campaign_fullstats dropped to 3.0 req/min (api floor)
... 15 successful requests at api floor (one every ~20s, no 429) ...
🔄 Adaptive rate limit: get_campaign_fullstats probing 4.0 req/min (desired)
→ if no 429: stays at 4.0 for all remaining requests
→ if 429: dropped to 3.0 again, cycle repeats
```

**Log format breakdown**:
- `cooling down 20s` — our calculated cooldown from api floor rate
- `server: 6s` — X-Ratelimit-Retry header value (logged for reference, not used)

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

### 1. Forgetting to call `SetRateLimit()` — no adaptive state

**Symptom**: if 429 occurs, adaptive state is created lazily with zero `apiFloor`.
The limiter won't be reduced (guard `if state.apiFloor > 0`), so it stays at whatever
rate it was running. Falls back to `X-Ratelimit-Retry` header, which may be unreliable.

```
❌ WRONG — no adaptive setup:
wbClient, _ := wb.NewFromConfig(wbCfg)
// Missing: wbClient.SetRateLimit(...)
// Missing: wbClient.SetAdaptiveParams(...)
client.Post(ctx, "tool_id", url, 3, 3, path, body, &result)

✅ CORRECT:
wbClient, _ := wb.NewFromConfig(wbCfg)
wbClient.SetRateLimit("tool_id", desiredRate, desiredBurst, apiRate, apiBurst)
wbClient.SetAdaptiveParams(probeAfter, maxBackoff)
```

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
func (c *Client) getOrCreateLimiter(toolID string, rateLimit int, burst int) *rate.Limiter {
    if limiter, exists := c.limiters[toolID]; exists {
        return limiter  // ← pre-set limiter wins, params ignored
    }
    // Only creates new limiter if SetRateLimit() wasn't called (fallback)
}
```

This is by design — it allows API methods to work both with and without adaptive setup.

### 4. Config fields exist but aren't wired → dead code

Adding adaptive fields to the config struct and `GetDefaults()` cascade is only half the job.
If the downloader's `main.go` doesn't call `SetRateLimit()` with those values, the fields are dead code.

**Checklist when adding a new downloader:**
- [ ] Config struct has `desired`, `desired_burst`, `api`, `api_burst` fields
- [ ] `GetDefaults()` has cascade: api → desired, api_burst → desired_burst
- [ ] `main.go` calls `SetRateLimit(toolID, desired, desiredBurst, api, apiBurst)` **for EACH toolID used**
- [ ] **ToolIDs in `SetRateLimit()` match exactly the toolIDs in API calls** (verify with grep)
- [ ] `main.go` calls `SetAdaptiveParams(probeAfter, maxBackoff)`
- [ ] YAML config has all adaptive fields (not just `rate_limit`/`burst`)
- [ ] Displayed rate limit in header matches actual rate being used

### 5. ToolID mismatch between `SetRateLimit()` and API call

**Symptom**: Adaptive rate limiting silently fails — limiter never drops to api floor,
repeated 429s on consecutive requests despite adaptive code running.

**Root cause**: `SetRateLimit("tool_id_A", ...)` creates adaptive state for one toolID,
but API method uses `Get(ctx, "tool_id_B", ...)` — a completely different limiter
with no adaptive state (`apiFloor=0`). The check `if state.apiFloor > 0` fails silently.

```
SetRateLimit("get_campaign_fullstats", 4, 1, 3, 1)
              ↓ creates limiter + adaptive state
              limiters["get_campaign_fullstats"] = {apiFloor: 3/60}

Get(ctx, "get_wb_campaign_fullstats2", ...)
              ↓ getOrCreateLimiter() creates NEW limiter
              limiters["get_wb_campaign_fullstats2"] = {apiFloor: 0}  ← no adaptive!

On 429: adaptiveReduce("get_wb_campaign_fullstats2")
              ↓ state.apiFloor = 0
              ↓ if state.apiFloor > 0 { ... }  ← FAILS!
              ↓ fallback to server header (may be too short)
              ↓ repeated 429s
```

**Fix**: Ensure toolIDs match exactly. Verify with grep:

```bash
# Find all SetRateLimit calls
grep -rn "SetRateLimit" cmd/data-downloaders/

# Find all API call toolIDs in the SAME codebase (not service layer)
grep -rn 'client.Get\|client.Post\|client.GetStream' cmd/data-downloaders/
```

**Better fix**: Define toolID constants in one place and use them everywhere.

### 6. Deprecated `adaptive_recover_after` does nothing

**Symptom**: Changing `adaptive_recover_after` in config has no effect.

**Explanation**: This field is deprecated and accepted only for backwards compatibility.
The limiter drops to api floor **immediately** on 429, so there's no "recovery to api floor" phase.
Use `adaptive_probe_after` to control when to probe the desired rate.

### 7. Expecting instant recovery after 429

**Symptom**: "Why is it slow after 429? Shouldn't it recover immediately?"

**Explanation**: After 429, we drop to api floor and wait for a full token interval (cooldown).
For 3 req/min, that's 20 seconds. This is by design — we're being conservative to avoid
irritating the server. After `adaptive_probe_after` successful requests at api floor,
we probe the desired rate again.

**Patience is key**: the system prioritizes server stability over speed. Once it finds
a working rate (api floor or desired), it sticks to it until the next 429.
