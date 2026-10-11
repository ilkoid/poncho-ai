package apispecs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// challengeCooldown — пауза перед повтором после карантина WB
// (их страница сама пишет «новая попытка через ~50 сек»).
const challengeCooldown = 65 * time.Second

// Capturer — CDP-перехват спеки, которую docs-сайт грузит сам (Ozon).
type Capturer interface {
	Capture(ctx context.Context, spec CaptureSpec, logf func(format string, args ...any)) ([]byte, error)
}

// CDPFetcher — браузерный канал: настоящий Chromium (Edge) с постоянным
// профилем. Проходит JS-челленджи как живой браузер и умеет перехватывать
// спеки, для которых нет прямой ссылки.
type CDPFetcher struct {
	cfg CDPConfig

	allocCtx      context.Context
	allocCancel   context.CancelFunc
	browserCtx    context.Context
	browserCancel context.CancelFunc

	visitedOrigins map[string]bool
}

// NewCDPFetcher строит fetcher (без запуска браузера — см. Start).
func NewCDPFetcher(cfg CDPConfig) *CDPFetcher {
	return &CDPFetcher{cfg: cfg, visitedOrigins: map[string]bool{}}
}

// resolveBrowserPath возвращает бинарник браузера: из конфига либо
// стандартные Edge/Chrome на macOS.
func resolveBrowserPath(configured string) (string, error) {
	candidates := []string{}
	if configured != "" {
		candidates = append(candidates, expandHome(configured))
	}
	candidates = append(candidates,
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	)
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("browser binary not found (tried: %s)", strings.Join(candidates, ", "))
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// Start запускает браузер с постоянным профилем.
func (c *CDPFetcher) Start(ctx context.Context) error {
	if c.browserCtx != nil {
		return nil
	}
	exe, err := resolveBrowserPath(c.cfg.BrowserPath)
	if err != nil {
		return err
	}
	profileDir := expandHome(c.cfg.ProfileDir)
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return fmt.Errorf("profile dir: %w", err)
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(exe),
		chromedp.UserDataDir(profileDir),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("disable-blink-features", "AutomationControlled"), // navigator.webdriver=false: часть антиботов (напр. Ozon abt) отвергает CDP-браузер по этому флагу
		chromedp.WindowSize(1440, 900),
	)
	if c.cfg.Headless {
		opts = append(opts, chromedp.Headless)
	}

	c.allocCtx, c.allocCancel = chromedp.NewExecAllocator(ctx, opts...)
	c.browserCtx, c.browserCancel = chromedp.NewContext(c.allocCtx)
	// Браузер реально стартует на первом Run — прогреваем пустым действием.
	if err := chromedp.Run(c.browserCtx); err != nil {
		c.Close()
		return fmt.Errorf("start browser (%s): %w", exe, err)
	}
	return nil
}

// Close останавливает браузер и освобождает контексты.
func (c *CDPFetcher) Close() {
	if c.browserCancel != nil {
		c.browserCancel()
		c.browserCancel = nil
	}
	if c.allocCancel != nil {
		c.allocCancel()
		c.allocCancel = nil
	}
	c.browserCtx = nil
	c.allocCtx = nil
	c.visitedOrigins = map[string]bool{}
}

// GetFile тянет fileURL через in-page fetch() после захода на originURL:
// навигация проходит JS-челлендж и оставляет clearance-куки, fetch
// переиспользует их же без всяких выгрузок браузера.
// Реализует FileGetter.
func (c *CDPFetcher) Get(ctx context.Context, url string) ([]byte, error) {
	origin := originOf(url)
	if !c.visitedOrigins[origin] {
		if err := chromedp.Run(c.browserCtx,
			chromedp.Navigate(origin),
			chromedp.WaitReady("body", chromedp.ByQuery),
		); err != nil {
			return nil, fmt.Errorf("navigate %s: %w", origin, err)
		}
		c.visitedOrigins[origin] = true
	}

	body, err := c.pageFetch(ctx, url)
	if err == nil {
		return body, nil
	}
	if !errors.Is(err, ErrChallenge) {
		return nil, err
	}
	// Карантин: их страница просит подождать ~минуту и обновляется сама.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(challengeCooldown):
	}
	return c.pageFetch(ctx, url)
}

// pageFetch выполняет синхронный XHR в контексте страницы: в отличие от
// fetch() возвращает строку сразу — chromedp.Evaluate в v0.15 не ждёт промисы.
func (c *CDPFetcher) pageFetch(ctx context.Context, url string) ([]byte, error) {
	jsLiteral, _ := json.Marshal(url)
	expr := fmt.Sprintf(
		`(() => {
			const x = new XMLHttpRequest();
			x.open('GET', %s, false);
			x.send(null);
			return (x.status >= 200 && x.status < 300)
				? x.responseText
				: 'HTTP_ERROR ' + x.status;
		})()`,
		jsLiteral,
	)
	var s string
	timeout := time.Duration(c.cfg.TimeoutSeconds) * time.Second
	runCtx, cancel := context.WithTimeout(c.browserCtx, timeout)
	defer cancel()
	if err := chromedp.Run(runCtx, chromedp.Evaluate(expr, &s)); err != nil {
		return nil, fmt.Errorf("page fetch %s: %w", url, err)
	}
	if strings.HasPrefix(s, "HTTP_ERROR") {
		return nil, fmt.Errorf("page fetch %s: %s", url, s)
	}
	if LooksLikeChallenge("", []byte(s)) {
		return nil, ErrChallenge
	}
	return []byte(s), nil
}

// Capture открывает pageURL и возвращает крупнейший JSON-ответ, похожий на
// OpenAPI-спеку (содержит `"openapi"` и не меньше MinSize байт). Все
// кандидаты логируются — по подсказкам легко скорректировать фильтр.
// Реализует Capturer.
func (c *CDPFetcher) Capture(ctx context.Context, spec CaptureSpec, logf func(format string, args ...any)) ([]byte, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}

	var mu sync.Mutex
	candidates := make(map[network.RequestID]string) // requestID → URL
	var seenAll []string                             // диагностика: всё, что грузила страница

	chromedp.ListenTarget(c.browserCtx, func(ev interface{}) {
		resp, ok := ev.(*network.EventResponseReceived)
		if !ok || resp.Response == nil {
			return
		}
		mu.Lock()
		if len(seenAll) < 200 {
			seenAll = append(seenAll, fmt.Sprintf("%d %s [%s]",
				resp.Response.Status, resp.Response.URL, resp.Response.MimeType))
		}
		mu.Unlock()
		mime := strings.ToLower(resp.Response.MimeType)
		if !strings.Contains(mime, "json") && !matchesAny(resp.Response.URL, spec.Contains) {
			return
		}
		mu.Lock()
		candidates[resp.RequestID] = resp.Response.URL
		mu.Unlock()
	})

	if err := chromedp.Run(c.browserCtx,
		network.Enable(),
		chromedp.Navigate(spec.PageURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
	); err != nil {
		return nil, fmt.Errorf("navigate %s: %w", spec.PageURL, err)
	}

	deadline := time.Duration(c.cfg.TimeoutSeconds) * time.Second
	runCtx, cancel := context.WithTimeout(c.browserCtx, deadline)
	defer cancel()
	start := time.Now()

	var best []byte
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	seen := map[network.RequestID]bool{}
	reloaded := false

	for best == nil {
		select {
		case <-runCtx.Done():
			mu.Lock()
			diag := make([]string, 0, len(seenAll))
			for i, line := range seenAll {
				if i >= 15 {
					diag = append(diag, fmt.Sprintf("… и ещё %d ответов", len(seenAll)-15))
					break
				}
				diag = append(diag, line)
			}
			mu.Unlock()
			return nil, fmt.Errorf(
				"capture timeout on %s: %d ответов-кандидатов, всего ответов %d; последние: %s",
				spec.PageURL, len(candidates), len(seenAll), strings.Join(diag, " | "))
		case <-ticker.C:
			// Полтора десятка секунд без единого кандидата — вероятно, страница
			// застряла в антибот-редиректах: один раз перезагружаем.
			mu.Lock()
			total := len(seenAll)
			mu.Unlock()
			if !reloaded && total < 5 && time.Since(start) > 15*time.Second {
				reloaded = true
				logf("apispecs: cdp capture: страница почти не грузится (%d ответов) — reload", total)
				_ = chromedp.Run(c.browserCtx, network.Enable(), chromedp.Reload())
			}
		}
		mu.Lock()
		ids := make([]network.RequestID, 0, len(candidates))
		for id := range candidates {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		mu.Unlock()

		for _, id := range ids {
			body, err := c.responseBody(runCtx, id)
			if err != nil {
				continue
			}
			mu.Lock()
			url := candidates[id]
			mu.Unlock()

			if len(body) >= 10<<10 {
				logf("apispecs: cdp capture candidate: %s (%d bytes)", url, len(body))
			}
			if int64(len(body)) >= spec.MinSize && bytes.Contains(body, []byte(`"openapi"`)) {
				if best == nil || len(body) > len(best) {
					best = body
				}
			}
		}
	}
	return best, nil
}

// responseBody достаёт тело ответа по requestID.
func (c *CDPFetcher) responseBody(ctx context.Context, id network.RequestID) ([]byte, error) {
	var body []byte
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		body, err = network.GetResponseBody(id).Do(ctx)
		return err
	}))
	return body, err
}

func originOf(rawURL string) string {
	i := strings.Index(strings.TrimPrefix(rawURL, "https://"), "/")
	if strings.HasPrefix(rawURL, "https://") && i >= 0 {
		return rawURL[:len("https://")+i]
	}
	return rawURL
}

func matchesAny(s string, subs []string) bool {
	for _, sub := range subs {
		if sub != "" && strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
