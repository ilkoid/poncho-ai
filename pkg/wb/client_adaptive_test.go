package wb

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"

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

func Test429CooldownFlat_NoMultiplier(t *testing.T) {
	// Cooldown при 429 плоский: подсказка X-Ratelimit-Retry + ε (Set429RetryEpsilon,
	// тут 10мс), без домножения на номер попытки. Три 429 с hint=2 дают ТРИ паузы
	// по ~2с ≈ 6с (пауза выполняется и после последней попытки, перед выходом из
	// цикла); прежний множитель ×2/×3 дал бы 2+4+6=12с и тест падает (граница 9с).
	mockHTTP := &mockHTTPClient{
		responses: []*mockResponse{
			{status: 429, header: map[string]string{"X-Ratelimit-Retry": "2"}},
			{status: 429, header: map[string]string{"X-Ratelimit-Retry": "2"}},
			{status: 429, header: map[string]string{"X-Ratelimit-Retry": "2"}},
		},
	}
	client := New("test_key")
	client.SetHTTPClient(mockHTTP)
	client.SetRateLimit("my_tool", testDesiredRate, testDesiredBurst, testAPIRate, testAPIBurst)
	client.Set429RetryEpsilon(10 * time.Millisecond)

	start := time.Now()
	ctx := context.Background()
	var result []map[string]interface{}
	err := client.Get(ctx, "my_tool", "https://api.example.com", testFallbackRate, testFallbackBurst, "/test", nil, &result)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error after 3 consecutive 429s")
	}
	if got := mockHTTP.requestCount(); got != 3 {
		t.Errorf("request count = %d, want 3", got)
	}
	if elapsed < 5500*time.Millisecond {
		t.Errorf("elapsed = %v, want ≥ 5.5s (три паузы по ~2с)", elapsed)
	}
	if elapsed > 9*time.Second {
		t.Errorf("elapsed = %v, cooldown не плоский (множитель на попытку вернулся?)", elapsed)
	}
}

func Test429Cooldown_NoHint_FloorInterval(t *testing.T) {
	// Без подсказки (заголовок "0"/отсутствует) ждём один интервал apiFloor —
	// период генерации токена. apiRate=300/мин → интервал 0.2с.
	mockHTTP := newMockHTTP("0")
	client := New("test_key")
	client.SetHTTPClient(mockHTTP)
	client.SetRateLimit("my_tool", testDesiredRate, testDesiredBurst, 300, 100)

	start := time.Now()
	ctx := context.Background()
	var result []map[string]interface{}
	_ = client.Get(ctx, "my_tool", "https://api.example.com", testFallbackRate, testFallbackBurst, "/test", nil, &result)
	elapsed := time.Since(start)

	if got := mockHTTP.requestCount(); got != 2 {
		t.Errorf("request count = %d, want 2 (429 + retry)", got)
	}
	if elapsed < 150*time.Millisecond {
		t.Errorf("elapsed = %v, want ≥ 0.15с (интервал floor 0.2с)", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Errorf("elapsed = %v, пауза не соответствует интервалу floor", elapsed)
	}
}

func Test429Cooldown_HintPlusEpsilon(t *testing.T) {
	// Новая семантика: при живой подсказке пауза = подсказка + ε, БЕЗ override
	// флором. hint=2с, ε=100мс → ~2.1с; прежний max(hint, интервал floor) дал бы
	// то же тут, но при коротких подсказках (1–5с) флор перегонял момент раздачи.
	mockHTTP := newMockHTTP("2")
	client := New("test_key")
	client.SetHTTPClient(mockHTTP)
	client.SetRateLimit("my_tool", testDesiredRate, testDesiredBurst, testAPIRate, testAPIBurst)
	client.Set429RetryEpsilon(100 * time.Millisecond)

	start := time.Now()
	ctx := context.Background()
	var result []map[string]interface{}
	_ = client.Get(ctx, "my_tool", "https://api.example.com", testFallbackRate, testFallbackBurst, "/test", nil, &result)
	elapsed := time.Since(start)

	if got := mockHTTP.requestCount(); got != 2 {
		t.Errorf("request count = %d, want 2 (429 + retry)", got)
	}
	if elapsed < 1900*time.Millisecond {
		t.Errorf("elapsed = %v, want ≥ 1.9с (подсказка 2с + ε 100мс)", elapsed)
	}
	if elapsed > 4*time.Second {
		t.Errorf("elapsed = %v, пауза больше подсказки+ε", elapsed)
	}
}

func Test429RetryEpsilon_DefaultAndSetter(t *testing.T) {
	client := New("test_key")
	if client.retry429Epsilon != 200*time.Millisecond {
		t.Errorf("дефолт ε = %v, want 200ms", client.retry429Epsilon)
	}
	client.Set429RetryEpsilon(300 * time.Millisecond)
	if client.retry429Epsilon != 300*time.Millisecond {
		t.Errorf("ε после сеттера = %v, want 300ms", client.retry429Epsilon)
	}
	client.Set429RetryEpsilon(0) // <= 0 игнорируется
	if client.retry429Epsilon != 300*time.Millisecond {
		t.Errorf("ε после игнорируемого сеттера = %v, want 300ms", client.retry429Epsilon)
	}
}

// TestNewFromConfig_AppliesRetryEpsilon: wb.retry_epsilon_ms из конфига
// (каноническое место настройки ε) применяется конструктором; 0 = дефолт 200мс.
func TestNewFromConfig_AppliesRetryEpsilon(t *testing.T) {
	client, err := NewFromConfig(config.WBConfig{
		APIKey:         "test_key",
		RetryAttempts:  10,
		RetryEpsilonMs: 110,
	})
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	if client.retry429Epsilon != 110*time.Millisecond {
		t.Errorf("ε из конфига = %v, want 110ms", client.retry429Epsilon)
	}
	if client.retryAttempts != 10 {
		t.Errorf("retryAttempts = %d, want 10", client.retryAttempts)
	}

	// 0/отсутствие ключа = дефолт клиента 200мс
	client2, err := NewFromConfig(config.WBConfig{APIKey: "test_key"})
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	if client2.retry429Epsilon != 200*time.Millisecond {
		t.Errorf("ε без ключа = %v, want дефолт 200ms", client2.retry429Epsilon)
	}
}

// TestRetryScheme: строка для шапки лога — фактические значения клиента.
func TestRetryScheme(t *testing.T) {
	client, _ := NewFromConfig(config.WBConfig{
		APIKey:         "test_key",
		RetryAttempts:  10,
		RetryEpsilonMs: 110,
	})
	want := "hint+eps 110ms · retries 10"
	if got := client.RetryScheme(); got != want {
		t.Errorf("RetryScheme() = %q, want %q", got, want)
	}
}

// Test429HintRetry_SkipsLocalPacing: повтор после ЖИВОЙ подсказки сервера не
// проходит локальный pacing (limiter.Wait + jitter-гард) — иначе выстрел
// сдвигается на 0,5–2с мимо раздачи токена (замер 16.10: интервалы попыток
// 20/21с при сне «подсказка+ε» 18,2/19,2с — лишнее ждал лимитер).
//
// Ловушка именно пейсинга: лимитер 12/мин burst 1 — первую попытку берёт
// единственный токен, и без фикса повтор ждал бы следующий до ~5с.
func Test429HintRetry_SkipsLocalPacing(t *testing.T) {
	mockHTTP := newMockHTTP("1") // 429 (hint 1с) → 200
	client := New("test_key")
	client.SetHTTPClient(mockHTTP)
	client.Set429RetryEpsilon(100 * time.Millisecond)
	// lastRequestTime в прошлом: изолируем лимитер от jitter-гарда (он тоже
	// часть пейсинга, но недетерминирован случайным джиттером).
	client.mu.Lock()
	client.lastRequestTime["my_tool"] = time.Now().Add(-time.Minute)
	client.mu.Unlock()

	start := time.Now()
	ctx := context.Background()
	var result []map[string]interface{}
	_ = client.Get(ctx, "my_tool", "https://api.example.com", 12, 1, "/test", nil, &result)
	elapsed := time.Since(start)

	if got := mockHTTP.requestCount(); got != 2 {
		t.Errorf("request count = %d, want 2 (429 + retry)", got)
	}
	if elapsed < 1*time.Second {
		t.Errorf("elapsed = %v, want ≥ 1с (сон «подсказка 1с + ε»)", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Errorf("elapsed = %v, повтор ждал локальный pacing (лимитер 12/мин burst 1 "+
			"держал бы токен до ~5с), want < 3с", elapsed)
	}
}

// Test429HintRetry_GetStreamSkipsPacing: симметрия фикса doRequest — повтор
// после живой подсказки в GetStream-петле (campaigns fullstats) тоже идёт без
// локального пейсинга. Лимитер 12/мин burst 1: без фикса повтор ждал бы токен
// до ~5с, с фиксом elapsed ≈ 1,1с (чистый сон «подсказка 1с + ε»).
func Test429HintRetry_GetStreamSkipsPacing(t *testing.T) {
	mockHTTP := newMockHTTP("1") // 429 (hint 1с) → 200
	client := New("test_key")
	client.SetHTTPClient(mockHTTP)
	client.Set429RetryEpsilon(100 * time.Millisecond)
	client.mu.Lock()
	client.lastRequestTime["my_tool"] = time.Now().Add(-time.Minute)
	client.mu.Unlock()

	start := time.Now()
	ctx := context.Background()
	var result []map[string]interface{}
	_ = client.GetStream(ctx, "my_tool", "https://api.example.com", 12, 1, "/test", nil, &result)
	elapsed := time.Since(start)

	if got := mockHTTP.requestCount(); got != 2 {
		t.Errorf("request count = %d, want 2 (429 + retry)", got)
	}
	if elapsed < 1*time.Second {
		t.Errorf("elapsed = %v, want ≥ 1с (сон «подсказка 1с + ε»)", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Errorf("elapsed = %v, GetStream-повтор ждал локальный pacing (лимитер 12/мин burst 1), want < 3с", elapsed)
	}
}

// Test429Storm_NextRequestSkipsQueue: шторм-режим — первая попытка СЛЕДУЮЩЕГО
// запроса после 429 идёт без очереди в лимитере. Запрос 1: 429 (hint 1с) +
// победа повтором; запрос 2 должен выстрелить мгновенно (лимитер 12/мин
// burst 1 без шторм-флага держал бы токен до ~5с).
func Test429Storm_NextRequestSkipsQueue(t *testing.T) {
	mockHTTP := &mockHTTPClient{responses: []*mockResponse{
		{status: 429, header: map[string]string{"X-Ratelimit-Retry": "1"}},
		{status: 200, body: `[]`},
		{status: 200, body: `[]`},
	}}
	client := New("test_key")
	client.SetHTTPClient(mockHTTP)
	client.Set429RetryEpsilon(100 * time.Millisecond)
	client.mu.Lock()
	client.lastRequestTime["my_tool"] = time.Now().Add(-time.Minute)
	client.mu.Unlock()

	ctx := context.Background()
	var r1, r2 []map[string]interface{}
	_ = client.Get(ctx, "my_tool", "https://api.example.com", 12, 1, "/t", nil, &r1) // 429 + гонка

	start := time.Now()
	_ = client.Get(ctx, "my_tool", "https://api.example.com", 12, 1, "/t", nil, &r2) // шторм-скип
	elapsed := time.Since(start)

	if got := mockHTTP.requestCount(); got != 3 {
		t.Errorf("request count = %d, want 3 (429 + retry + мгновенный)", got)
	}
	if elapsed > 2*time.Second {
		t.Errorf("elapsed = %v, want < 2с (шторм-режим: без очереди; без флага лимитер ждал бы ~4с)", elapsed)
	}
}

// Test429Storm_CalmSuccessRestoresQueueing: спокойный успех гасит шторм-флаг —
// запрос после НЕ-429 успеха снова честно ждёт слот (спокойная фаза не меняется).
func Test429Storm_CalmSuccessRestoresQueueing(t *testing.T) {
	mockHTTP := &mockHTTPClient{responses: []*mockResponse{
		{status: 429, header: map[string]string{"X-Ratelimit-Retry": "1"}},
		{status: 200, body: `[]`}, // гонка-победа (запрос 1)
		{status: 200, body: `[]`}, // мгновенный успех по шторм-флагу (запрос 2)
		{status: 200, body: `[]`}, // запрос 3: снова через очередь
	}}
	client := New("test_key")
	client.SetHTTPClient(mockHTTP)
	client.Set429RetryEpsilon(100 * time.Millisecond)
	client.mu.Lock()
	client.lastRequestTime["my_tool"] = time.Now().Add(-time.Minute)
	client.mu.Unlock()

	ctx := context.Background()
	var r []map[string]interface{}
	_ = client.Get(ctx, "my_tool", "https://api.example.com", 12, 1, "/t", nil, &r) // 429 + гонка
	_ = client.Get(ctx, "my_tool", "https://api.example.com", 12, 1, "/t", nil, &r) // шторм-скип → успех без 429

	start := time.Now()
	_ = client.Get(ctx, "my_tool", "https://api.example.com", 12, 1, "/t", nil, &r) // очередь восстановлена
	elapsed := time.Since(start)

	if got := mockHTTP.requestCount(); got != 4 {
		t.Errorf("request count = %d, want 4", got)
	}
	if elapsed < 3*time.Second {
		t.Errorf("elapsed = %v, want ≥ 3с (после спокойного успеха очередь вернулась: лимитер 12/мин + jitter)", elapsed)
	}
}
