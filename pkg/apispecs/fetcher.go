package apispecs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxBodySize — потолок на тело ответа: крупнейшая известная спека ~4 МБ.
const maxBodySize = 64 << 20

// FileGetter — источник содержимого спеки. Реализуется HTTPFetcher и
// CDPFetcher; в тестах подставляется заглушка.
type FileGetter interface {
	Get(ctx context.Context, url string) ([]byte, error)
}

// HTTPFetcher — обычный HTTP GET с браузерным UA и ретраями.
// Челлендж не ретраится принципиально (см. ErrChallenge).
type HTTPFetcher struct {
	Client      *http.Client
	UserAgent   string
	Pause       time.Duration
	MaxAttempts int
	// BackoffBase — база экспоненциальной паузы между ретраями (2s, 4s, 8s);
	// ноль = стандартные 2s. В тестах ставят миллисекунды.
	BackoffBase time.Duration
}

// NewHTTPFetcher строит fetcher из конфига.
func NewHTTPFetcher(cfg HTTPConfig) *HTTPFetcher {
	return &HTTPFetcher{
		Client:      &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second},
		UserAgent:   cfg.UserAgent,
		Pause:       time.Duration(cfg.PauseMs) * time.Millisecond,
		MaxAttempts: cfg.Retries,
	}
}

// Get возвращает тело файла. Ретраи — только для сетевых ошибок и
// 429/5xx; ErrChallenge и прочие 4xx возвращаются сразу.
func (f *HTTPFetcher) Get(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	base := f.BackoffBase
	if base == 0 {
		base = 2 * time.Second
	}
	for attempt := 0; attempt < f.MaxAttempts; attempt++ {
		if attempt > 0 {
			backoff := base << (attempt - 1) // 2s, 4s, 8s…
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
		body, err := f.getOnce(ctx, url)
		if err == nil {
			return body, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if isPermanent(err) {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

func (f *HTTPFetcher) getOnce(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, permanent(fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("User-Agent", f.UserAgent)
	req.Header.Set("Accept", "application/yaml, application/json, */*")

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err // сетевая ошибка — transient
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, err
	}

	if challengeStatus(resp.StatusCode) || LooksLikeChallenge(resp.Header.Get("Content-Type"), body) {
		// permanent: против WAF ретраить нельзя — продлевает карантин IP.
		return nil, permanent(fmt.Errorf("%w (HTTP %d %s)", ErrChallenge, resp.StatusCode, url))
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, fmt.Errorf("HTTP %d %s", resp.StatusCode, url) // transient
	}
	if resp.StatusCode != http.StatusOK {
		return nil, permanent(fmt.Errorf("HTTP %d %s", resp.StatusCode, url))
	}
	return body, nil
}

// permanentError — ошибка, которую бессмысленно ретраить (челлендж, 4xx).
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

func permanent(err error) error { return permanentError{err: err} }

func isPermanent(err error) bool {
	var pe permanentError
	return errors.As(err, &pe)
}
