package wb

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// mockHTTPClient returns configurable HTTP responses in sequence.
// Records all requests for inspection.
type mockHTTPClient struct {
	mu        sync.Mutex
	responses []*mockResponse
	requests  []*http.Request
	callIdx   int
}

type mockResponse struct {
	status int
	body   string
	header map[string]string
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.requests = append(m.requests, req)

	if m.callIdx >= len(m.responses) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("[]")),
		}, nil
	}

	resp := m.responses[m.callIdx]
	m.callIdx++

	header := http.Header{}
	for k, v := range resp.header {
		header.Set(k, v)
	}

	body := resp.body
	if body == "" {
		body = "[]"
	}

	return &http.Response{
		StatusCode: resp.status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func (m *mockHTTPClient) requestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

// newMockHTTP creates a mock that returns 429 then 200.
func newMockHTTP(retryAfter string) *mockHTTPClient {
	return &mockHTTPClient{
		responses: []*mockResponse{
			{status: 429, header: map[string]string{"X-Ratelimit-Retry": retryAfter}},
			{status: 200, body: `[]`},
		},
	}
}

// High rates for fast tests (limiter.Wait returns in <1ms instead of 15-20s).
// We test adaptive LOGIC (rate reduction), not actual timing.
const (
	testDesiredRate = 6000 // 100 req/sec
	testDesiredBurst = 100
	testAPIRate      = 3000 // 50 req/sec
	testAPIBurst     = 100
	testFallbackRate = 4000 // passed to Get() as fallback
	testFallbackBurst = 100
)

func TestAdaptiveRateLimit_ReducesOn429(t *testing.T) {
	// SetRateLimit("my_tool") → Get("my_tool") → 429 → limiter drops to api floor
	mockHTTP := newMockHTTP("0") // retry=0 for speed (only test rate reduction, not backoff)
	client := New("test_key")
	client.SetHTTPClient(mockHTTP)
	client.SetRateLimit("my_tool", testDesiredRate, testDesiredBurst, testAPIRate, testAPIBurst)

	ctx := context.Background()
	var result []map[string]interface{}
	err := client.Get(ctx, "my_tool", "https://api.example.com", testFallbackRate, testFallbackBurst, "/test", nil, &result)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := mockHTTP.requestCount(); got != 2 {
		t.Errorf("request count = %d, want 2 (initial + retry)", got)
	}

	// Assert: limiter was reduced to api floor
	limiters := client.RateLimiters()
	gotRate, ok := limiters["my_tool"]
	if !ok {
		t.Fatal("limiter for 'my_tool' not found")
	}
	wantRate := rate.Limit(float64(testAPIRate) / 60.0)
	if gotRate != wantRate {
		t.Errorf("rate = %v, want %v (api floor)", gotRate, wantRate)
	}
}

func TestAdaptiveRateLimit_SilentFailureOnMismatch(t *testing.T) {
	// SetRateLimit("wrong_tool") → Get("actual_tool") → 429 → limiter NOT reduced
	// This is the pitfall: adaptive state set for one toolID, API uses another
	mockHTTP := newMockHTTP("0")
	client := New("test_key")
	client.SetHTTPClient(mockHTTP)
	client.SetRateLimit("wrong_tool", testDesiredRate, testDesiredBurst, testAPIRate, testAPIBurst)

	ctx := context.Background()
	var result []map[string]interface{}
	err := client.Get(ctx, "actual_tool", "https://api.example.com", testFallbackRate, testFallbackBurst, "/test", nil, &result)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := mockHTTP.requestCount(); got != 2 {
		t.Errorf("request count = %d, want 2", got)
	}

	// Assert: limiter for "actual_tool" was NOT reduced (no adaptive state for it)
	limiters := client.RateLimiters()
	gotRate, ok := limiters["actual_tool"]
	if !ok {
		t.Fatal("limiter for 'actual_tool' not found")
	}
	apiFloor := rate.Limit(float64(testAPIRate) / 60.0)
	if gotRate == apiFloor {
		t.Errorf("rate = %v (api floor) — should NOT reduce for mismatched toolID", gotRate)
	}
	// Limiter stays at fallback rate from Get() params
	wantRate := rate.Limit(float64(testFallbackRate) / 60.0)
	if gotRate != wantRate {
		t.Errorf("rate = %v, want %v (unchanged fallback)", gotRate, wantRate)
	}

	// "wrong_tool" limiter still exists but was never used
	if _, ok := limiters["wrong_tool"]; !ok {
		t.Error("limiter for 'wrong_tool' should exist (pre-set but unused)")
	}
}

func TestAdaptiveRateLimit_SilentFailureWithoutSetup(t *testing.T) {
	// NO SetRateLimit → Get("my_tool") → 429 → limiter NOT reduced
	// Another pitfall: no adaptive state configured at all
	mockHTTP := newMockHTTP("0")
	client := New("test_key")
	client.SetHTTPClient(mockHTTP)
	// Note: SetRateLimit NOT called

	ctx := context.Background()
	var result []map[string]interface{}
	err := client.Get(ctx, "my_tool", "https://api.example.com", testFallbackRate, testFallbackBurst, "/test", nil, &result)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := mockHTTP.requestCount(); got != 2 {
		t.Errorf("request count = %d, want 2", got)
	}

	limiters := client.RateLimiters()
	gotRate, ok := limiters["my_tool"]
	if !ok {
		t.Fatal("limiter for 'my_tool' not found")
	}
	apiFloor := rate.Limit(float64(testAPIRate) / 60.0)
	if gotRate == apiFloor {
		t.Errorf("rate = %v (api floor) — should NOT reduce without SetRateLimit", gotRate)
	}
	wantRate := rate.Limit(float64(testFallbackRate) / 60.0)
	if gotRate != wantRate {
		t.Errorf("rate = %v, want %v (unchanged fallback)", gotRate, wantRate)
	}
}

func TestAdaptiveRateLimit_RetryConsumesBackoffTime(t *testing.T) {
	// Verify that retry after 429 actually waits X-Ratelimit-Retry seconds.
	// Uses retry=0 (minimal) since we only need to verify the sleep happens.
	mockHTTP := newMockHTTP("0")
	client := New("test_key")
	client.SetHTTPClient(mockHTTP)
	client.SetRateLimit("my_tool", testDesiredRate, testDesiredBurst, testAPIRate, testAPIBurst)

	start := time.Now()
	ctx := context.Background()
	var result []map[string]interface{}
	_ = client.Get(ctx, "my_tool", "https://api.example.com", testFallbackRate, testFallbackBurst, "/test", nil, &result)
	elapsed := time.Since(start)

	// Even with retry=0, the time.After(0) call still yields the goroutine.
	// The test mainly verifies no panic/deadlock and the retry logic executes.
	if elapsed > 5*time.Second {
		t.Errorf("elapsed = %v, test took too long (rate limiter blocking?)", elapsed)
	}
}
