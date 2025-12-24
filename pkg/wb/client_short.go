//go:build short

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
    // TODO: Create rate limiter with appropriate limits
    // TODO: Initialize client with API key, base URL, HTTP client, and rate limiter
    return nil
}

// genericGet с поддержкой Rate Limit и Retries
func (c *Client) get(ctx context.Context, path string, params url.Values, dest interface{}) error {
    // TODO: Parse URL with base URL and path
    // TODO: Add query parameters if provided
    // TODO: Implement retry loop with rate limiting
    // TODO: Handle HTTP requests with proper headers
    // TODO: Handle rate limiting (429 responses) with retry-after
    // TODO: Unmarshal response JSON to destination
    // TODO: Return error if max retries exceeded
    return nil
}

// структура и метод для пинга контентного api wb (именно контентного так как для разных api свои ручки)
type PingResponse struct {
    Status string `json:"Status"`
    TS     string `json:"TS"` // Timestamp
}

// Ping проверяет связь именно с сервисом Content API
func (c *Client) Ping(ctx context.Context) error {
    // TODO: Call ping endpoint
    // TODO: Parse ping response
    // TODO: Verify status is OK
    // TODO: Return error if ping fails
    return nil
}