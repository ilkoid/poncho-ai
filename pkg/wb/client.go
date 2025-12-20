package wb

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "strconv"
    "time"

    "golang.org/x/time/rate" // <--- Добавь этот импорт
)

const (
    // Лимиты для Content API (согласно документации)
    BurstLimit    = 5
    RateLimit     = 100 // запросов в минуту
    RetryAttempts = 3
	DefaultBaseURL = "https://content-api.wildberries.ru"
)

type Client struct {
    apiKey     string
    baseURL    string
    httpClient *http.Client
    limiter    *rate.Limiter // <--- Лимитер
}

func New(apiKey string) *Client {
    // 100 req/min = 1.66 req/sec
    // Но лучше быть чуть консервативнее, скажем 1.5 rps
    r := rate.Limit(1.6) 
    
    return &Client{
        apiKey:  apiKey,
        baseURL: DefaultBaseURL,
        httpClient: &http.Client{
            Timeout: 30 * time.Second,
        },
        // Burst=5, Rate=1.6 req/s
        limiter: rate.NewLimiter(r, BurstLimit),
    }
}

// genericGet с поддержкой Rate Limit и Retries
func (c *Client) get(ctx context.Context, path string, params url.Values, dest interface{}) error {
    u, err := url.Parse(c.baseURL + path)
    if err != nil {
        return fmt.Errorf("invalid url: %w", err)
    }
    if params != nil {
        u.RawQuery = params.Encode()
    }

    var lastErr error

    // Retry loop
    for i := 0; i < RetryAttempts; i++ {
        // 1. Ждем разрешения от лимитера (блокирует горутину, если превысили лимит)
        if err := c.limiter.Wait(ctx); err != nil {
            return fmt.Errorf("rate limiter wait: %w", err)
        }

        req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
        if err != nil {
            return err
        }

        req.Header.Set("Authorization", c.apiKey)
        req.Header.Set("Content-Type", "application/json")
        req.Header.Set("Accept", "application/json")
        // Можно добавить локаль, если нужно
        // req.Header.Set("Accept-Language", "ru")

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
// структура и метод для пинга контентного api wb (именно контентного так как для разных api свои ручки)
type PingResponse struct {
    Status string `json:"Status"`
    TS     string `json:"TS"` // Timestamp
}

// Ping проверяет связь именно с сервисом Content API
func (c *Client) Ping(ctx context.Context) error {
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
        return fmt.Errorf("ping failed: %w", err)
    }

    if resp.Status != "OK" {
        return fmt.Errorf("ping status not OK: %s", resp.Status)
    }

    return nil
}
