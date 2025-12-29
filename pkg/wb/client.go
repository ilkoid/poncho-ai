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

const (
	// Дефолтные лимиты (устаревшие, используются для обратной совместимости)
	DefaultBurstLimit    = 5
	DefaultRateLimit     = 100 // запросов в минуту
	DefaultRetryAttempts = 3
	DefaultBaseURL       = "https://content-api.wildberries.ru"
)

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

// New создает новый клиент для работы с Wildberries API.
//
// Параметры:
//   - apiKey: API ключ для авторизации в Wildberries
//
// Возвращает настроенный клиент без лимитеров (создаются динамически).
// Рекомендуется использовать NewFromConfig для конфигурируемого клиента.
func New(apiKey string) *Client {
	return &Client{
		apiKey:        apiKey,
		retryAttempts: DefaultRetryAttempts,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
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
	u, err := url.Parse(baseURL + path)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if params != nil {
		u.RawQuery = params.Encode()
	}

	// Получаем или создаём limiter для этого tool
	limiter := c.getOrCreateLimiter(toolID, rateLimit, burst)

	var lastErr error

	// Retry loop
	for i := 0; i < c.retryAttempts; i++ {
		// 1. Ждем разрешения от лимитера (блокирует горутину, если превысили лимит)
		if err := limiter.Wait(ctx); err != nil {
			return fmt.Errorf("rate limiter wait: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		if err != nil {
			return err
		}

		req.Header.Set("Authorization", c.apiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
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

// get - устаревший метод для обратной совместимости.
// Используйте Get() с параметрами.
func (c *Client) get(ctx context.Context, path string, params url.Values, dest interface{}) error {
	return c.Get(ctx, "legacy", DefaultBaseURL, DefaultRateLimit, DefaultBurstLimit, path, params, dest)
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
	u, err := url.Parse(baseURL + path)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}

	// Сериализуем body
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	// Получаем или создаём limiter для этого tool
	limiter := c.getOrCreateLimiter(toolID, rateLimit, burst)

	var lastErr error

	// Retry loop
	for i := 0; i < c.retryAttempts; i++ {
		// 1. Ждем разрешения от лимитера (блокирует горутину, если превысили лимит)
		if err := limiter.Wait(ctx); err != nil {
			return fmt.Errorf("rate limiter wait: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", u.String(), strings.NewReader(string(bodyJSON)))
		if err != nil {
			return err
		}

		req.Header.Set("Authorization", c.apiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue // Сетевая ошибка, пробуем еще
		}
		defer resp.Body.Close()

		responseBody, _ := io.ReadAll(resp.Body)

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
			return fmt.Errorf("wb api error: status %d, body: %s", resp.StatusCode, string(responseBody))
		}

		if err := json.Unmarshal(responseBody, dest); err != nil {
			return fmt.Errorf("unmarshal error: %w", err)
		}

		return nil // Успех
	}

	return fmt.Errorf("max retries exceeded, last error: %v", lastErr)
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
// Возвращает ответ от API или ошибку. Полезен для диагностики:
// - проверка доступности сервиса
// - проверка валидности API ключа (401 = unauthorized)
// - определение сетевых проблем
func (c *Client) Ping(ctx context.Context) (*PingResponse, error) {
    // В документации сказано, что URL для Content: https://content-api.wildberries.ru/ping
    // Наш c.baseURL по умолчанию как раз https://content-api.wildberries.ru

    // ВАЖНО: Ping возвращает простой JSON, а не обертку APIResponse[T].
    // Поэтому используем c.get() с умом или пишем отдельный запрос, если c.get заточен под APIResponse.
    // Но наш c.get() просто делает Unmarshal в dest, так что всё ок.

    var resp PingResponse

    // Путь /ping
    // Params nil
    err := c.get(ctx, "/ping", nil, &resp)
    if err != nil {
        return nil, fmt.Errorf("ping failed: %w", err)
    }

    if resp.Status != "OK" {
        return nil, fmt.Errorf("ping status not OK: %s", resp.Status)
    }

    return &resp, nil
}
