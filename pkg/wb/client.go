// Package wb provides a reusable SDK for Wildberries API.
//
// Architecture:
//
// This is an **API SDK**, not just a "dumb" HTTP client. It provides:
//   - HTTP client with retry, rate limiting, and error classification
//   - High-level methods that handle WB API-specific response wrappers
//   - Auto-pagination for endpoints that return partial data
//   - Client-side filtering for API limitations (workarounds)
//
// Comparison with S3 client:
//   - S3 client (pkg/s3storage) is a "dumb" client - S3 API is simple and standardized
//   - WB client is an SDK - WB API is complex with custom response formats and quirks
//
// Usage pattern:
//   - pkg/wb - reusable SDK (can be used in any project)
//   - pkg/tools/std - thin wrappers for LLM function calling
//
// Design rationale:
// Auto-pagination and filtering are NOT business logic - they are technical workarounds
// for WB API limitations. Moving them to tools would violate DRY and make code harder
// to maintain.
package wb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"golang.org/x/time/rate"
)

// Константы удалены - все параметры теперь из config.yaml
// Defaults для tools задаются в wb секции config.yaml

// ErrorType представляет тип ошибки при работе с WB API.
type ErrorType int

const (
	ErrUnknown ErrorType = iota
	ErrAuthFailed
	ErrTimeout
	ErrNetwork
	ErrRateLimit
)

// String возвращает строковое представление типа ошибки.
func (e ErrorType) String() string {
	switch e {
	case ErrAuthFailed:
		return "authentication_failed"
	case ErrTimeout:
		return "timeout"
	case ErrNetwork:
		return "network_error"
	case ErrRateLimit:
		return "rate_limit"
	default:
		return "unknown"
	}
}

// HumanMessage возвращает человекочитаемое сообщение для типа ошибки.
func (e ErrorType) HumanMessage() string {
	switch e {
	case ErrAuthFailed:
		return "API ключ недействителен или отсутствует. Проверьте WB_API_KEY в конфигурации."
	case ErrTimeout:
		return "Превышено время ожидания. Сервер WB не отвечает или проблемы с сетью."
	case ErrNetwork:
		return "Сервер WB недоступен. Проверьте подключение к интернету."
	case ErrRateLimit:
		return "Превышен лимит запросов. Подождите перед следующей попыткой."
	default:
		return "Неизвестная ошибка при подключении к WB API."
	}
}

// HTTPClient интерфейс для выполнения HTTP запросов.
//
// Позволяет мокировать HTTP клиент в тестах (Rule 9).
// Стандартный *http.Client реализует этот интерфейс.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Client struct {
	apiKey        string
	httpClient    HTTPClient // Интерфейс вместо конкретного типа для testability
	retryAttempts int        // Количество retry попыток

	mu       sync.RWMutex
	limiters map[string]*rate.Limiter // tool ID → limiter
}

// IsDemoKey проверяет что используется demo ключ (для mock режима).
func (c *Client) IsDemoKey() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apiKey == "demo_key"
}

// New создает новый клиент для работы с Wildberries API.
//
// Параметры:
//   - apiKey: API ключ для авторизации в Wildberries
//
// Возвращает настроенный клиент с дефолтными значениями из WBConfig.GetDefaults().
// Рекомендуется использовать NewFromConfig для конфигурируемого клиента.
//
// DEPRECATED: Используйте NewFromConfig для явного указания всех параметров.
func New(apiKey string) *Client {
	// Используем дефолтную конфигурацию для согласованности с NewFromConfig
	defaultCfg := config.WBConfig{
		APIKey:        apiKey,
		RateLimit:     100,  // дефолтный rate limit
		BurstLimit:    5,    // дефолтный burst
		RetryAttempts: 3,    // дефолтный retry
		Timeout:       "30s", // дефолтный timeout
	}
	cfg := defaultCfg.GetDefaults()

	// Парсим timeout (заведомо валидный, но на всякий случай)
	timeout, _ := time.ParseDuration(cfg.Timeout)

	return &Client{
		apiKey:        cfg.APIKey,
		retryAttempts: cfg.RetryAttempts,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		limiters: make(map[string]*rate.Limiter),
	}
}

// NewFromConfig создает новый клиент из конфигурации.
//
// Параметры:
//   - cfg: Конфигурация WB API с настройками timeout
//
// Возвращает настроенный клиент с параметрами из конфига.
// Лимитеры создаются динамически при вызове Get().
// Поля с нулевыми значениями используют дефолтные значения через GetDefaults().
func NewFromConfig(cfg config.WBConfig) (*Client, error) {
	// Применяем дефолтные значения
	cfg = cfg.GetDefaults()

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("wb.api_key is required")
	}

	// Парсим timeout
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("invalid wb.timeout format: %w", err)
	}

	return &Client{
		apiKey:        cfg.APIKey,
		retryAttempts: cfg.RetryAttempts,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		limiters: make(map[string]*rate.Limiter),
	}, nil
}

// ClassifyError классифицирует ошибку по типу для лучшей диагностики.
//
// Анализирует текст ошибки и возвращает соответствующий тип:
//   - ErrAuthFailed: ошибки 401, unauthorized, Forbidden
//   - ErrTimeout: timeout, deadline exceeded
//   - ErrNetwork: connection refused, no such host
//   - ErrRateLimit: ошибки 429, Too Many Requests
//   - ErrUnknown: все остальные ошибки
func (c *Client) ClassifyError(err error) ErrorType {
	if err == nil {
		return ErrUnknown
	}

	errMsg := err.Error()
	errMsgLower := strings.ToLower(errMsg)

	// Проверка ошибок авторизации
	if strings.Contains(errMsg, "401") ||
		strings.Contains(errMsgLower, "unauthorized") ||
		strings.Contains(errMsg, "Forbidden") {
		return ErrAuthFailed
	}

	// Проверка таймаутов
	if strings.Contains(errMsgLower, "timeout") ||
		strings.Contains(errMsg, "deadline exceeded") {
		return ErrTimeout
	}

	// Проверка сетевых ошибок
	if strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "no such host") {
		return ErrNetwork
	}

	// Проверка rate limiting
	if strings.Contains(errMsg, "429") ||
		strings.Contains(errMsg, "Too Many Requests") {
		return ErrRateLimit
	}

	return ErrUnknown
}

// httpRequest описывает параметры HTTP запроса.
type httpRequest struct {
	method string
	url    string
	body   io.Reader
}

// doRequest выполняет HTTP запрос с retry логикой и rate limiting.
//
// Общий метод для Get() и Post(), реализующий retry loop, rate limiting
// и обработку 429 ответов.
func (c *Client) doRequest(ctx context.Context, toolID string, rateLimit int, burst int, req httpRequest, dest interface{}) error {
	// Получаем или создаём limiter для этого tool
	limiter := c.getOrCreateLimiter(toolID, rateLimit, burst)

	var lastErr error

	// Retry loop
	for i := 0; i < c.retryAttempts; i++ {
		// 1. Ждем разрешения от лимитера (блокирует горутину, если превысили лимит)
		if err := limiter.Wait(ctx); err != nil {
			return fmt.Errorf("rate limiter wait: %w", err)
		}

		httpReq, err := http.NewRequestWithContext(ctx, req.method, req.url, req.body)
		if err != nil {
			return err
		}

		httpReq.Header.Set("Authorization", c.apiKey)
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			lastErr = err
			continue // Сетевая ошибка, пробуем еще
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		// Обработка 429 (Too Many Requests)
		if resp.StatusCode == http.StatusTooManyRequests {
			// Читаем заголовок X-Ratelimit-Retry или Retry-After
			retryAfter := 1 * time.Second // Дефолт
			if s := resp.Header.Get("X-Ratelimit-Retry"); s != "" {
				if sec, err := strconv.Atoi(s); err == nil {
					retryAfter = time.Duration(sec) * time.Second
				}
			}

			// Ждем и ретраем
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryAfter):
				continue
			}
		}

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("wb api error: status %d, body: %s", resp.StatusCode, string(body))
		}

		if err := json.Unmarshal(body, dest); err != nil {
			return fmt.Errorf("unmarshal error: %w", err)
		}

		return nil // Успех
	}

	return fmt.Errorf("max retries exceeded, last error: %v", lastErr)
}

// Get выполняет GET запрос к Wildberries API с поддержкой Rate Limit и Retries.
//
// Параметры передаются при каждом вызове, что позволяет каждому tool иметь
// свой endpoint и rate limit.
//
// Параметры:
//   - ctx: контекст для отмены
//   - toolID: идентификатор tool для выбора limiter (например, "get_wb_parent_categories")
//   - baseURL: базовый URL API (например, "https://content-api.wildberries.ru")
//   - rateLimit: лимит запросов в минуту
//   - burst: burst для rate limiter
//   - path: путь к endpoint (например, "/api/v1/directory/parent-categories")
//   - params: query параметры (может быть nil)
//   - dest: указатель на структуру для unmarshal результата
//
// Возвращает ошибку если запрос не удался.
func (c *Client) Get(ctx context.Context, toolID string, baseURL string, rateLimit int, burst int, path string, params url.Values, dest interface{}) error {
	// Валидация обязательных параметров - client "тупой", ожидает что их предоставит tool
	if baseURL == "" {
		return fmt.Errorf("baseURL is required (tool should provide value from config)")
	}
	if rateLimit <= 0 {
		return fmt.Errorf("rateLimit must be positive (tool should provide value from config)")
	}
	if burst <= 0 {
		return fmt.Errorf("burst must be positive (tool should provide value from config)")
	}

	u, err := url.Parse(baseURL + path)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if params != nil {
		u.RawQuery = params.Encode()
	}

	return c.doRequest(ctx, toolID, rateLimit, burst, httpRequest{
		method: "GET",
		url:    u.String(),
		body:   nil,
	}, dest)
}

// getOrCreateLimiter возвращает существующий limiter для toolID или создаёт новый.
//
// Параметры:
//   - toolID: идентификатор tool (ключ для map)
//   - rateLimit: запросов в минуту
//   - burst: burst для rate limiter
//
// Возвращает *rate.Limiter для этого tool.
func (c *Client) getOrCreateLimiter(toolID string, rateLimit int, burst int) *rate.Limiter {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Если limiter уже существует - возвращаем
	if limiter, exists := c.limiters[toolID]; exists {
		return limiter
	}

	// Создаём новый limiter
	// rateLimit в запросах/минуту → rate.Limit в запросах/секунду
	ratePerSec := float64(rateLimit) / 60.0
	limiter := rate.NewLimiter(rate.Limit(ratePerSec), burst)
	c.limiters[toolID] = limiter

	return limiter
}

// Post выполняет POST запрос к Wildberries API с поддержкой Rate Limit и Retries.
//
// Параметры передаются при каждом вызове, что позволяет каждому tool иметь
// свой endpoint и rate limit.
//
// Параметры:
//   - ctx: контекст для отмены
//   - toolID: идентификатор tool для выбора limiter (например, "search_wb_products")
//   - baseURL: базовый URL API (например, "https://content-api.wildberries.ru")
//   - rateLimit: лимит запросов в минуту
//   - burst: burst для rate limiter
//   - path: путь к endpoint (например, "/api/v2/list/goods")
//   - body: тело запроса (будет сериализовано в JSON)
//   - dest: указатель на структуру для unmarshal результата
//
// Возвращает ошибку если запрос не удался.
func (c *Client) Post(ctx context.Context, toolID string, baseURL string, rateLimit int, burst int, path string, body interface{}, dest interface{}) error {
	// Валидация обязательных параметров - client "тупой", ожидает что их предоставит tool
	if baseURL == "" {
		return fmt.Errorf("baseURL is required (tool should provide value from config)")
	}
	if rateLimit <= 0 {
		return fmt.Errorf("rateLimit must be positive (tool should provide value from config)")
	}
	if burst <= 0 {
		return fmt.Errorf("burst must be positive (tool should provide value from config)")
	}

	u, err := url.Parse(baseURL + path)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}

	// Сериализуем body
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	return c.doRequest(ctx, toolID, rateLimit, burst, httpRequest{
		method: "POST",
		url:    u.String(),
		body:   strings.NewReader(string(bodyJSON)),
	}, dest)
}
// PingResponse представляет ответ от ping endpoint Wildberries Content API.
//
// Поля:
//   - Status: Статус сервиса (обычно "OK" при успешном ответе)
//   - TS: Timestamp ответа сервера
type PingResponse struct {
	Status string `json:"Status"`
	TS     string `json:"TS"`
}

// Ping проверяет связь именно с сервисом Content API.
//
// Параметры:
//   - ctx: контекст для отмены
//   - baseURL: базовый URL API (например, "https://content-api.wildberries.ru")
//   - rateLimit: лимит запросов в минуту
//   - burst: burst для rate limiter
//
// Возвращает ответ от API или ошибку. Полезен для диагностики:
// - проверка доступности сервиса
// - проверка валидности API ключа (401 = unauthorized)
// - определение сетевых проблем
func (c *Client) Ping(ctx context.Context, baseURL string, rateLimit int, burst int) (*PingResponse, error) {
	var resp PingResponse

	// Ping возвращает простой JSON без обертки APIResponse[T]
	err := c.Get(ctx, "ping_wb_api", baseURL, rateLimit, burst, "/ping", nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("ping failed: %w", err)
	}

	if resp.Status != "OK" {
		return nil, fmt.Errorf("ping status not OK: %s", resp.Status)
	}

	return &resp, nil
}

// ReportDetailByPeriodPageResult представляет результат одной страницы пагинации.
type ReportDetailByPeriodPageResult struct {
	Rows        []RealizationReportRow // Строки отчета
	HasMore     bool                    // Есть ли еще данные
	LastRrdID   int                     // Последний rrd_id (для следующей страницы)
	StatusCode int                     // HTTP статус код
}

// ReportDetailByPeriodPage получает одну страницу отчета реализации.
//
// Параметры:
//   - ctx: контекст для отмены
//   - baseURL: базовый URL Statistics API
//   - rateLimit: лимит запросов в минуту
//   - burst: burst для rate limiter
//   - dateFrom: начало периода (формат: YYYY-MM-DD)
//   - dateTo: конец периода (формат: YYYY-MM-DD)
//   - rrdid: ID последней записи для пагинации (0 при первом запросе)
//
// Возвращает страницу данных или ошибку. HTTP 204 означает конец пагинации.
func (c *Client) ReportDetailByPeriodPage(
	ctx context.Context,
	baseURL string,
	rateLimit int,
	burst int,
	dateFrom int,
	dateTo int,
	rrdid int,
) (*ReportDetailByPeriodPageResult, error) {
	// Формируем параметры запроса
	// Преобразуем YYYYMMDD в YYYY-MM-DD
	dateFromStr := fmt.Sprintf("%04d-%02d-%02d", dateFrom/10000, (dateFrom%10000)/100, dateFrom%100)
	dateToStr := fmt.Sprintf("%04d-%02d-%02d", dateTo/10000, (dateTo%10000)/100, dateTo%100)

	params := url.Values{}
	params.Set("dateFrom", dateFromStr)
	params.Set("dateTo", dateToStr)
	params.Set("limit", "100000")
	if rrdid > 0 {
		params.Set("rrdid", fmt.Sprintf("%d", rrdid))
	}

	// Выполняем запрос
	var rows []RealizationReportRow

	// Создаем HTTP запрос вручную для обработки 204
	limiter := c.getOrCreateLimiter("report_detail_by_period", rateLimit, burst)
	if err := limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter wait: %w", err)
	}

	reqURL, err := url.Parse(baseURL + "/api/v5/supplier/reportDetailByPeriod")
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	reqURL.RawQuery = params.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, "GET", reqURL.String(), nil)
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Authorization", c.apiKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	// HTTP 204 = конец пагинации
	if resp.StatusCode == http.StatusNoContent {
		return &ReportDetailByPeriodPageResult{
			Rows:        nil,
			HasMore:     false,
			LastRrdID:   rrdid,
			StatusCode:  204,
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("wb api error: status %d, body: %s", resp.StatusCode, string(body))
	}

	// Парсим JSON ответ
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("unmarshal error: %w", err)
	}

	// Определяем последний rrd_id для следующей страницы
	lastRrdID := rrdid
	if len(rows) > 0 {
		lastRrdID = rows[len(rows)-1].RrdID
	}

	return &ReportDetailByPeriodPageResult{
		Rows:        rows,
		HasMore:     len(rows) > 0,
		LastRrdID:   lastRrdID,
		StatusCode:  200,
	}, nil
}

// ReportDetailByPeriodPageWithTime получает одну страницу отчета реализации с поддержкой времени.
//
// Параметры:
//   - ctx: контекст для отмены
//   - baseURL: базовый URL Statistics API
//   - rateLimit: лимит запросов в минуту
//   - burst: burst для rate limiter
//   - dateFrom: начало периода (формат RFC3339: "2026-01-25T12:00:00")
//   - dateTo: конец периода (формат RFC3339: "2026-01-25T23:59:59")
//   - rrdid: ID последней записи для пагинации (0 при первом запросе)
//   - limit: лимит строк на странице (по умолчанию 100000)
//
// Возвращает страницу данных или ошибку. HTTP 204 означает конец пагинации.
func (c *Client) ReportDetailByPeriodPageWithTime(
	ctx context.Context,
	baseURL string,
	rateLimit int,
	burst int,
	dateFrom string,
	dateTo string,
	rrdid int,
	limit int,
) (*ReportDetailByPeriodPageResult, error) {
	// Формируем параметры запроса
	params := url.Values{}
	params.Set("dateFrom", dateFrom)
	params.Set("dateTo", dateTo)
	params.Set("limit", fmt.Sprintf("%d", limit))
	params.Set("period", "daily")  // Периодичность: daily для поддержки времени
	if rrdid > 0 {
		params.Set("rrdid", fmt.Sprintf("%d", rrdid))
	}

	// Выполняем запрос
	var rows []RealizationReportRow

	// Создаем HTTP запрос вручную для обработки 204
	limiter := c.getOrCreateLimiter("report_detail_by_period_with_time", rateLimit, burst)
	if err := limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter wait: %w", err)
	}

	reqURL, err := url.Parse(baseURL + "/api/v5/supplier/reportDetailByPeriod")
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	reqURL.RawQuery = params.Encode()

	// DEBUG: логируем URL для отладки
	fmt.Printf("[DEBUG] Request URL: %s\n", reqURL.String())

	httpReq, err := http.NewRequestWithContext(ctx, "GET", reqURL.String(), nil)
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Authorization", c.apiKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	// HTTP 204 = конец пагинации
	if resp.StatusCode == http.StatusNoContent {
		return &ReportDetailByPeriodPageResult{
			Rows:        nil,
			HasMore:     false,
			LastRrdID:   rrdid,
			StatusCode:  204,
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("wb api error: status %d, body: %s", resp.StatusCode, string(body))
	}

	// Парсим JSON ответ
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("unmarshal error: %w", err)
	}

	// Определяем последний rrd_id для следующей страницы
	lastRrdID := rrdid
	if len(rows) > 0 {
		lastRrdID = rows[len(rows)-1].RrdID
	}

	return &ReportDetailByPeriodPageResult{
		Rows:        rows,
		HasMore:     len(rows) > 0,
		LastRrdID:   lastRrdID,
		StatusCode:  200,
	}, nil
}

// ReportDetailByPeriodIterator - итератор по всем страницам отчета.
// Использует callback для обработки каждой порции данных (stream processing).
//
// Параметры:
//   - ctx: контекст для отмены
//   - baseURL: базовый URL Statistics API
//   - rateLimit: лимит запросов в минуту
//   - burst: burst для rate limiter
//   - dateFrom: начало периода (формат: YYYYMMDD)
//   - dateTo: конец периода (формат: YYYYMMDD)
//   - callback: функция для обработки каждой страницы (возвращает ошибку для прерывания)
//
// Возвращает общее количество обработанных строк или ошибку.
func (c *Client) ReportDetailByPeriodIterator(
	ctx context.Context,
	baseURL string,
	rateLimit int,
	burst int,
	dateFrom int,
	dateTo int,
	callback func([]RealizationReportRow) error,
) (int, error) {
	totalCount := 0
	rrdid := 0

	for {
		page, err := c.ReportDetailByPeriodPage(ctx, baseURL, rateLimit, burst, dateFrom, dateTo, rrdid)
		if err != nil {
			return totalCount, err
		}

		// Конец пагинации
		if !page.HasMore {
			break
		}

		// Обрабатываем строки через callback (stream processing)
		if err := callback(page.Rows); err != nil {
			return totalCount, err
		}

		totalCount += len(page.Rows)
		rrdid = page.LastRrdID
	}

	return totalCount, nil
}
