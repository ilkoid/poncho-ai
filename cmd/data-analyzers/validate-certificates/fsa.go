// fsa.go — FSA registry API client (pub.fsa.gov.ru).
//
// Captures Bearer token via headless Chromium, then uses net/http for API calls.
// Both certificates and declarations use server-side search APIs:
//   - Certificates: POST /api/v1/rss/common/certificates/get
//   - Declarations: POST /api/v1/rds/common/declarations/get
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// FSA certificate status codes (from /api/v1/rss/common/identifiers).
const (
	StatusActive   = 6 // Действующий
	StatusResumed  = 3 // Возобновлён
	StatusArchived = 1 // Архивный (expired/terminated)
)

// statusName maps FSA status ID to human-readable name.
func statusName(id int) string {
	switch id {
	case StatusActive:
		return "Действующий"
	case StatusResumed:
		return "Возобновлён"
	case StatusArchived:
		return "Архивный"
	case 2:
		return "Приостановлен"
	case 4:
		return "Прекращён"
	case 5:
		return "Аннулирован"
	case 7:
		return "Продлён"
	case 8:
		return "Внесены изменения"
	case 10:
		return "На рассмотрении"
	case 11:
		return "Зарегистрирован"
	case 12:
		return "Выдан"
	case 14:
		return "Окончание срока"
	default:
		return fmt.Sprintf("Статус(%d)", id)
	}
}

// FSASearchResult — unified result for certificates and declarations from FSA search.
type FSASearchResult struct {
	ID         int    `json:"id"`
	Number     string `json:"number"`
	RegDate    string `json:"date"`           // "2023-05-25" or "28.12.2024"
	EndDate    string `json:"endDate"`        // "24.05.2026" (DD.MM.YYYY) or "2029-12-26" (YYYY-MM-DD)
	StatusID   int    `json:"idStatus"`
	CertType   string `json:"certType"`       // "Сертификат соответствия..." or "Декларация о соответствии..."
	ObjectType string `json:"certObjectType"` // "Серийный выпуск"
}

// FSASearchResponse — response from POST /certificates/get.
type FSASearchResponse struct {
	Items []FSASearchResult `json:"items"`
	Total int               `json:"totalCount"`
}

// FSADeclarationSearchResponse — response from POST /declarations/get.
// Declaration API returns "total" (not "totalCount") and uses "decl*" prefix for fields.
type FSADeclarationSearchResponse struct {
	Items []FSADeclarationResult `json:"items"`
	Total int                    `json:"total"`
}

// FSADeclarationResult — one declaration from FSA declaration search.
// Field names differ from certificates: declDate/declEndDate vs date/endDate.
type FSADeclarationResult struct {
	ID             int    `json:"id"`
	Number         string `json:"number"`
	DeclDate       string `json:"declDate"`      // "2024-12-28" (YYYY-MM-DD)
	DeclEndDate    string `json:"declEndDate"`    // "2029-12-26" (YYYY-MM-DD)
	StatusID       int    `json:"idStatus"`       // Same status codes as certificates
	DeclType       string `json:"declType"`       // "Декларация о соответствии..."
	DeclObjectType string `json:"declObjectType"` // "Серийный выпуск"
}

// FSAClient provides access to ФГИС РОСАКРЕДИТАЦИИ API.
type FSAClient struct {
	httpClient  *http.Client
	token       string
	allocCtx    context.Context
	allocCancel context.CancelFunc
}

// NewFSAClient launches headless Chromium, navigates to FSA website,
// captures the Bearer token from network requests.
// Browser is only needed for token capture — all searches use HTTP API.
func NewFSAClient(parentCtx context.Context, chromiumPath string) (*FSAClient, error) {
	if chromiumPath == "" {
		chromiumPath = findChromium()
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromiumPath),
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.WindowSize(1280, 800),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(parentCtx, opts...)

	tabCtx, tabCancel := chromedp.NewContext(allocCtx)
	defer tabCancel()

	// Channel to receive captured token.
	tokenCh := make(chan string, 1)

	// Listen for network requests to capture Bearer token from SPA.
	chromedp.ListenTarget(tabCtx, func(ev interface{}) {
		req, ok := ev.(*network.EventRequestWillBeSent)
		if !ok {
			return
		}
		headers := req.Request.Headers
		for _, v := range headers {
			auth, _ := v.(string)
			if strings.HasPrefix(auth, "Bearer eyJ") {
				token := strings.TrimPrefix(auth, "Bearer ")
				select {
				case tokenCh <- token:
				default:
				}
			}
		}
	})

	// Navigate to FSA certificate page — SPA loads and requests Bearer token.
	if err := chromedp.Run(tabCtx,
		network.Enable(),
		chromedp.Navigate("https://pub.fsa.gov.ru/rss/certificate"),
		chromedp.Sleep(6*time.Second),
	); err != nil {
		allocCancel()
		return nil, fmt.Errorf("navigate to FSA: %w", err)
	}

	// Wait for token with timeout.
	var token string
	select {
	case token = <-tokenCh:
	case <-time.After(15 * time.Second):
		allocCancel()
		return nil, fmt.Errorf("timeout: Bearer token not captured within 15s — FSA site may be down")
	}

	return &FSAClient{
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		token:       token,
		allocCtx:    allocCtx,
		allocCancel: allocCancel,
	}, nil
}

// SearchCertificate searches for a certificate by number via API.
// Returns (result, wasExactMatch, error).
// Endpoint: POST /api/v1/rss/common/certificates/get
// Filter uses {"column": "number", "search": "..."}.
func (c *FSAClient) SearchCertificate(ctx context.Context, number string) (*FSASearchResult, bool, error) {
	url := "https://pub.fsa.gov.ru/api/v1/rss/common/certificates/get"

	body := map[string]interface{}{
		"size": 10,
		"page": 0,
		"filter": map[string]interface{}{
			"columnsSearch": []map[string]string{
				{"column": "number", "search": number},
			},
		},
		"columnsSort": []interface{}{},
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, false, fmt.Errorf("marshal: %w", err)
	}

	respBody, err := c.doPost(ctx, url, bodyJSON)
	if err != nil {
		return nil, false, err
	}

	var searchResp FSASearchResponse
	if err := json.Unmarshal(respBody, &searchResp); err != nil {
		return nil, false, fmt.Errorf("parse JSON: %w", err)
	}

	result, exact := findExactOrFirst(searchResp.Items, number)
	return result, exact, nil
}

// SearchDeclaration searches for a declaration by number via server-side API.
// Returns (result, wasExactMatch, error).
// Endpoint: POST /api/v1/rds/common/declarations/get
// Filter uses {"name": "number", "search": "...", "type": 0} (not "column" like certificates).
func (c *FSAClient) SearchDeclaration(ctx context.Context, number string) (*FSASearchResult, bool, error) {
	url := "https://pub.fsa.gov.ru/api/v1/rds/common/declarations/get"

	body := map[string]interface{}{
		"size": 10,
		"page": 0,
		"filter": map[string]interface{}{
			"columnsSearch": []map[string]interface{}{
				{"name": "number", "search": number, "type": 0},
			},
		},
		"columnsSort": []interface{}{},
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, false, fmt.Errorf("marshal: %w", err)
	}

	respBody, err := c.doPost(ctx, url, bodyJSON)
	if err != nil {
		return nil, false, err
	}

	var searchResp FSADeclarationSearchResponse
	if err := json.Unmarshal(respBody, &searchResp); err != nil {
		return nil, false, fmt.Errorf("parse JSON: %w", err)
	}

	// Map declaration-specific fields to unified FSASearchResult.
	items := make([]FSASearchResult, len(searchResp.Items))
	for i, d := range searchResp.Items {
		items[i] = FSASearchResult{
			ID:         d.ID,
			Number:     d.Number,
			RegDate:    d.DeclDate,
			EndDate:    d.DeclEndDate,
			StatusID:   d.StatusID,
			CertType:   d.DeclType,
			ObjectType: d.DeclObjectType,
		}
	}

	result, exact := findExactOrFirst(items, number)
	return result, exact, nil
}

// doPost executes a POST request with the Bearer token and returns the response body.
func (c *FSAClient) doPost(ctx context.Context, url string, bodyJSON []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(bodyJSON)))
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		snippet := string(respBody)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("FSA API %d: %s", resp.StatusCode, snippet)
	}

	return respBody, nil
}

// findExactOrFirst returns the exact match by number, or the first item, or nil.
// Second return value indicates whether the match was exact.
func findExactOrFirst(items []FSASearchResult, number string) (*FSASearchResult, bool) {
	target := strings.TrimSpace(number)
	for i := range items {
		if strings.EqualFold(strings.TrimSpace(items[i].Number), target) {
			return &items[i], true
		}
	}
	if len(items) > 0 {
		return &items[0], false
	}
	return nil, false
}

// Close releases all resources including the browser allocator.
func (c *FSAClient) Close() {
	if c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
	if c.allocCancel != nil {
		c.allocCancel()
	}
}

// findChromium locates the Chromium binary on the system.
func findChromium() string {
	for _, path := range []string{
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
		"/snap/chromium/current/usr/lib/chromium-browser/chrome",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/google-chrome",
	} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return "chromium-browser" // fallback to PATH
}
