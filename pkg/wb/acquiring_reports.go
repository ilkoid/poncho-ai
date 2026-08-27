package wb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// ============================================================================
// Finance API — отчёт об издержках на приём платежей (эквайринг)
// ============================================================================
// Контекст: в детализации отчёта реализации (/sales-reports/detailed)
// платёжная комиссия приходит сводно — поле acquiringFee с маркером
// paymentProcessing («Компенсация платёжных услуг», yaml:1382-1393).
// Банковская расшифровка — кто банк-эквайер, НДС, счёт-фактура — вынесена WB
// в отдельный «Отчёт об издержках на приём платежей» (в портале продавца:
// Взаиморасчёты → Отчёты реализации → acquiring-reports):
//
//	POST /api/finance/v1/acquiring/list      — список отчётов с итогами
//	POST /api/finance/v1/acquiring/detailed  — детализация за период
//
// Swagger: docs/wb_api_swagger/13-finances.yaml:267-449 (эндпоинты),
// :1576-1800 (схемы). Лимит 1 req/min, personal/service токен, хост тот же
// finance-api.wildberries.ru.
//
// Механика запроса (лимитер + min-interval, 204, 429-cooldown) повторяет
// SalesReportDetailedPage из finance_reports.go. Сознательно НЕ вынесена в
// общий хелпер, чтобы не трогать прод-путь ночного загрузчика продаж.
// ============================================================================

const (
	acquiringListPath     = "/api/finance/v1/acquiring/list"
	acquiringDetailedPath = "/api/finance/v1/acquiring/detailed"

	// acquiringToolID — отдельный ToolID: SetRateLimit в вызывающем коде
	// обязан использовать то же имя (иначе отдельный лимитер без состояния).
	acquiringToolID = "finance_acquiring_report"
)

// AcquiringDetailedReq — тело POST /acquiring/detailed
// (AcquiringReportsDetailedReq, yaml:1661-1708). Пагинация идентична
// sales-reports/detailed: rrdId-курсор, limit ≤100000, HTTP 204 = конец.
type AcquiringDetailedReq struct {
	DateFrom string   `json:"dateFrom"` // RFC3339 или YYYY-MM-DD (MSK UTC+3)
	DateTo   string   `json:"dateTo"`
	Limit    int      `json:"limit"`
	RrdID    int      `json:"rrdId"` // 0 при первом запросе, далее rrdId последней строки
	Fields   []string `json:"fields,omitempty"`
}

// AcquiringDetailedRow — строка детализации отчёта об издержках на приём
// платежей (AcquiringReportsDetailedRes, yaml:1709-1800). Поверхность API
// новая, legacy snake_case-имени нет — camelCase-теги соответствуют wire
// напрямую (в отличие от RealizationReportRow). Денежные поля WB отдаёт
// строками — объявлены flexFloat (принимает и строку, и число).
type AcquiringDetailedRow struct {
	RrdID           int       `json:"rrdId"`           // ID строки
	ReportID        int64     `json:"reportId"`        // ID отчёта
	AcqDate         string    `json:"acqDate"`         // «Дата операции»
	SaleDate        string    `json:"saleDate"`        // «Дата продажи»
	DocTypeName     string    `json:"docTypeName"`     // «Тип документа»
	NmID            int       `json:"nmId"`            // «Артикул WB»
	RetailAmount    flexFloat `json:"retailAmount"`    // «Вайлдберриз реализовал Товар (Пр)»
	AcquiringFee    flexFloat `json:"acquiringFee"`    // комиссия за эквайринг, В ТОМ ЧИСЛЕ НДС (yaml:1777-1780)
	AcquiringFeeVat flexFloat `json:"acquiringFeeVat"` // сумма НДС (yaml:1781-1784)
	AcquiringBank   string    `json:"acquiringBank"`   // «Наименование банка-эквайера» (напр. «Тинькофф»)
	Tin             string    `json:"tin"`             // ИНН (строка — ведущие нули значимы)
	Kpp             string    `json:"taxRegistrationReasonCode"`
	InvoiceNumber   string    `json:"invoiceNumber"` // «Номер счёта-фактуры»
	InvoiceDate     string    `json:"invoiceDate"`   // «Дата счёта-фактуры»
	ShkID           int64     `json:"shkId"`         // «Штрихкод»
	Srid            string    `json:"srid"`          // ID заказа (= rid у сборочных заданий)
	Currency        string    `json:"currency"`
}

// AcquiringPageResult — одна страница детализации эквайринг-отчёта.
type AcquiringPageResult struct {
	Rows      []AcquiringDetailedRow
	HasMore   bool
	LastRrdID int
}

// AcquiringListReq — тело POST /acquiring/list
// (AcquiringReportListReq, yaml:1576-1611). Пагинация offset-ная, limit ≤1000.
type AcquiringListReq struct {
	DateFrom string `json:"dateFrom"`
	DateTo   string `json:"dateTo"`
	Limit    int    `json:"limit"`
	Offset   int    `json:"offset"`
}

// AcquiringReportSummary — отчёт из списка (AcquiringReportListRes,
// yaml:1612-1660): итоги эквайринга за отчётный период без детализации.
type AcquiringReportSummary struct {
	ReportID           int64     `json:"reportId"`
	SellerFinanceName  string    `json:"sellerFinanceName"`
	DateFrom           string    `json:"dateFrom"`
	DateTo             string    `json:"dateTo"`
	CreateDate         string    `json:"createDate"`
	Currency           string    `json:"currency"`
	AcquiringFeeSum    flexFloat `json:"acquiringFeeSum"`    // «Сумма издержек по эквайрингу»
	AcquiringFeeVatSum flexFloat `json:"acquiringFeeVatSum"` // «В том числе НДС»
}

// AcquiringReportDetailedPage получает одну страницу детализации
// эквайринг-отчёта. Поведение 204/429 — как у SalesReportDetailedPage.
func (c *Client) AcquiringReportDetailedPage(
	ctx context.Context,
	baseURL string,
	rateLimit int,
	burst int,
	req AcquiringDetailedReq,
) (*AcquiringPageResult, error) {
	if rateLimit <= 0 {
		rateLimit = 1
	}
	if burst <= 0 {
		burst = 1
	}
	if req.Limit <= 0 || req.Limit > 100000 {
		req.Limit = 100000
	}

	status, body, err := c.postAcquiringJSON(ctx, baseURL+acquiringDetailedPath, rateLimit, burst, req)
	if err != nil {
		return nil, err
	}

	if status == http.StatusNoContent {
		return &AcquiringPageResult{HasMore: false, LastRrdID: req.RrdID}, nil
	}

	var rows []AcquiringDetailedRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("decode acquiring rows: %w (body head: %s)", err, headForLog(body))
	}

	lastRrdID := req.RrdID
	for _, r := range rows {
		if r.RrdID > 0 {
			lastRrdID = r.RrdID
		}
	}
	return &AcquiringPageResult{
		Rows:      rows,
		HasMore:   len(rows) > 0,
		LastRrdID: lastRrdID,
	}, nil
}

// AcquiringReportDetailedIterator перебирает все страницы детализации
// эквайринг-отчёта за период [dateFrom, dateTo] (RFC3339, MSK), передавая
// партии строк в callback. Возвращает суммарное число строк.
func (c *Client) AcquiringReportDetailedIterator(
	ctx context.Context,
	baseURL string,
	rateLimit int,
	burst int,
	dateFrom string,
	dateTo string,
	callback func([]AcquiringDetailedRow) error,
) (int, error) {
	totalCount := 0
	rrdID := 0

	const maxRetries = 3
	const baseBackoff = 5 * time.Second

	for {
		var page *AcquiringPageResult
		var err error

		for attempt := 0; attempt < maxRetries; attempt++ {
			page, err = c.AcquiringReportDetailedPage(ctx, baseURL, rateLimit, burst, AcquiringDetailedReq{
				DateFrom: dateFrom,
				DateTo:   dateTo,
				Limit:    100000,
				RrdID:    rrdID,
			})
			if err == nil {
				break
			}
			if !isRetryableError(err) || attempt == maxRetries-1 {
				return totalCount, err
			}
			backoff := baseBackoff * time.Duration(1<<attempt)
			fmt.Printf("  ⚠️  Сетевая ошибка acquiring, повтор #%d через %v: %v\n", attempt+1, backoff, err)
			select {
			case <-ctx.Done():
				return totalCount, ctx.Err()
			case <-time.After(backoff):
			}
		}

		if !page.HasMore {
			break
		}

		if err := callback(page.Rows); err != nil {
			return totalCount, err
		}

		totalCount += len(page.Rows)
		rrdID = page.LastRrdID
	}

	return totalCount, nil
}

// AcquiringReportList возвращает все отчёты об издержках на приём платежей
// за период (offset-пагинация до пустой страницы/204).
func (c *Client) AcquiringReportList(
	ctx context.Context,
	baseURL string,
	rateLimit int,
	burst int,
	dateFrom string,
	dateTo string,
) ([]AcquiringReportSummary, error) {
	if rateLimit <= 0 {
		rateLimit = 1
	}
	if burst <= 0 {
		burst = 1
	}

	var all []AcquiringReportSummary
	const pageLimit = 1000 // максимум по yaml:1603

	for offset := 0; ; offset += pageLimit {
		status, body, err := c.postAcquiringJSON(ctx, baseURL+acquiringListPath, rateLimit, burst,
			AcquiringListReq{DateFrom: dateFrom, DateTo: dateTo, Limit: pageLimit, Offset: offset})
		if err != nil {
			return nil, err
		}
		if status == http.StatusNoContent {
			return all, nil
		}

		var page []AcquiringReportSummary
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("decode acquiring list: %w (body head: %s)", err, headForLog(body))
		}
		if len(page) == 0 {
			return all, nil
		}
		all = append(all, page...)
		if len(page) < pageLimit {
			return all, nil
		}
	}
}

// postAcquiringJSON выполняет POST к acquiring-эндпоинту с лимитом 1 req/min:
// token-bucket + минимальный интервал между реальными запросами, 429 —
// адаптивный cooldown, 204 — конец пагинации без ошибки. Тело ответа
// прочитано целиком; на 200 возвращает (200, body, nil).
func (c *Client) postAcquiringJSON(
	ctx context.Context,
	u string,
	rateLimit int,
	burst int,
	req any,
) (int, []byte, error) {
	limiter := c.getOrCreateLimiter(acquiringToolID, rateLimit, burst)
	if err := limiter.Wait(ctx); err != nil {
		return 0, nil, fmt.Errorf("rate limiter wait: %w", err)
	}

	minInterval := time.Duration(float64(time.Minute) / float64(rateLimit))
	c.mu.RLock()
	lastReqTime := c.lastRequestTime[acquiringToolID]
	c.mu.RUnlock()
	if !lastReqTime.IsZero() {
		if elapsed := time.Since(lastReqTime); elapsed < minInterval {
			select {
			case <-ctx.Done():
				return 0, nil, ctx.Err()
			case <-time.After(minInterval - elapsed):
			}
		}
	}

	parsed, err := url.Parse(u)
	if err != nil {
		return 0, nil, fmt.Errorf("invalid url: %w", err)
	}

	bodyJSON, err := json.Marshal(req)
	if err != nil {
		return 0, nil, fmt.Errorf("marshal body: %w", err)
	}

	resp, body, err := c.sendFinanceRequest(ctx, parsed.String(), bodyJSON)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		c.adaptiveRecoverOK(acquiringToolID)
		c.mu.Lock()
		c.lastRequestTime[acquiringToolID] = time.Now()
		c.mu.Unlock()
		return http.StatusNoContent, nil, nil
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		serverRetrySec := 1
		if s := resp.Header.Get("X-Ratelimit-Retry"); s != "" {
			if sec, err := strconv.Atoi(s); err == nil && sec > 0 {
				serverRetrySec = sec
			}
		}
		waitDur := c.adaptiveReduce(acquiringToolID, serverRetrySec)
		fmt.Fprintf(os.Stderr, "\u26a0\ufe0f  429 for %s, cooling down %v (server: %ds)\n",
			acquiringToolID, waitDur.Truncate(time.Second), serverRetrySec)
		select {
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		case <-time.After(waitDur):
		}
		return 0, nil, fmt.Errorf("wb api error: status 429, body: %s", string(body))
	}

	if resp.StatusCode != http.StatusOK {
		return 0, nil, fmt.Errorf("wb api error: status %d, body: %s", resp.StatusCode, string(body))
	}

	c.adaptiveRecoverOK(acquiringToolID)
	c.mu.Lock()
	c.lastRequestTime[acquiringToolID] = time.Now()
	c.mu.Unlock()
	return http.StatusOK, body, nil
}
