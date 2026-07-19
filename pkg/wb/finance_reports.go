package wb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// ============================================================================
// Finance API — детализация отчётов реализации (замена reportDetailByPeriod)
// ============================================================================
// Контекст: GET /api/v5/supplier/reportDetailByPeriod отключён WB 15.07.2026
// (release-notes id=498). Замена — POST /api/finance/v1/sales-reports/detailed
// на host'е finance-api.wildberries.ru (НЕ statistics-api).
//
// Отличия от старого endpoint:
//   - POST с JSON-телом вместо GET с query-параметрами.
//   - Ответ — голый JSON-массив строк (как /api/v1/supplier/orders), а не
//     {rows:[]}; HTTP 204 = конец пагинации.
//   - Поля в camelCase; многие числовые поля возвращаются строками
//     (retailPrice: "1249", penalty: "231.35", ...). Маппер parseStrFloat
//     приводит их в float64 существующего RealizationReportRow.
//   - Поле is_legal_entity старого wire → isB2b в новом (см. mapDetailedRow).
//   - rrdId (camelCase) заменяет rrdid в теле запроса.
//
// Паритет полей: все 60 полей RealizationReportRow присутствуют в новом ответе
// (сверено по SalesReportsDetailedRes, swagger 13-finances.yaml).
// ============================================================================

// FinanceReportsBaseURL — хост финансового API.
// Передаётся явно в FinanceSalesSource; параметр baseURL интерфейса
// SalesSource (пришедший из конфига statistics-api) игнорируется.
const FinanceReportsBaseURL = "https://finance-api.wildberries.ru"

const (
	salesReportDetailedPath = "/api/finance/v1/sales-reports/detailed"
)

// SalesReportsDetailedReq — тело POST-запроса к /sales-reports/detailed.
// Имена полей — camelCase по swagger (SalesReportsDetailedReq).
type SalesReportsDetailedReq struct {
	DateFrom string   `json:"dateFrom"` // RFC3339 или YYYY-MM-DD (MSK UTC+3)
	DateTo   string   `json:"dateTo"`
	Limit    int      `json:"limit"`  // макс. 100000
	RrdID    int      `json:"rrdId"`  // пагинация: 0 при первом запросе, далее rrdId последней строки
	Period   string   `json:"period"` // "daily" | "weekly" (пусто = weekly)
	Fields   []string `json:"fields,omitempty"`
}

// salesReportDetailedRow — локальный decode-struct под camelCase wire-схему
// нового finance endpoint. WB inconsistent: одни и те же числовые поля в разных
// строках/днях может вернуть числом ("sellerPromo": 0) или строкой
// ("retailPrice": "1249"). Поэтому все числовые «суммовые» поля объявлены как
// flexFloat — он принимает оба формата JSON. Названия полей соответствуют
// SalesReportsDetailedRes (swagger 13-finances.yaml, строки 1278-1655), сверено
// по реальному ответу finance-api.wildberries.ru 2026-07-16.
type salesReportDetailedRow struct {
	ReportID     int64  `json:"reportId"`
	DateFrom     string `json:"dateFrom"`
	DateTo       string `json:"dateTo"`
	RrdID        int    `json:"rrdId"`
	GiID         int    `json:"giId"`
	SubjectName  string `json:"subjectName"`
	NmID         int    `json:"nmId"`
	BrandName    string `json:"brandName"`
	VendorCode   string `json:"vendorCode"` // = sa_name в старом wire
	Title        string `json:"title"`
	TechSize     string `json:"techSize"` // = ts_name
	SKU          string `json:"sku"`      // = barcode
	DocTypeName  string `json:"docTypeName"`
	Quantity     int    `json:"quantity"`
	OfficeName   string `json:"officeName"`
	SellerOperNm string `json:"sellerOperName"` // = supplier_oper_name
	OrderDT      string `json:"orderDt"`
	SaleDT       string `json:"saleDt"`
	RRDate       string `json:"rrDate"` // = rr_dt
	ShkID        int64  `json:"shkId"`
	GiBoxTypeNm  string `json:"giBoxTypeName"`  // = gi_box_type_name
	DeliveryMtd  string `json:"deliveryMethod"` // = delivery_method
	Srid         string `json:"srid"`
	OrderID      int64  `json:"orderId"`
	IsB2b        bool   `json:"isB2b"` // = is_legal_entity в старом wire

	// Числовые поля, которые WB возвращает строками либо числами (flexFloat):
	RetailPrice          flexFloat `json:"retailPrice"`
	RetailAmount         flexFloat `json:"retailAmount"`
	SalePercent          flexFloat `json:"salePercent"`
	CommissionPercent    flexFloat `json:"commissionPercent"`
	RetailPriceWithDisc  flexFloat `json:"retailPriceWithDisc"` // = retail_price_withdisc_rub
	ProductDiscountForR  flexFloat `json:"productDiscountForReport"`
	SellerPromo          flexFloat `json:"sellerPromo"` // = supplier_promo (приходит числом!)
	SellerPromoDiscount  flexFloat `json:"sellerPromoDiscount"`
	SPP                  flexFloat `json:"spp"`         // = ppvz_spp_prc
	KvwBase              flexFloat `json:"kvwBase"`     // = ppvz_kvw_prc_base
	Kvw                  flexFloat `json:"kvw"`         // = ppvz_kvw_prc
	SupRatingUp          flexFloat `json:"supRatingUp"` // = sup_rating_prc_up
	IsKgvp               flexFloat `json:"isKgvpV2"`    // = is_kgvp_v2 (имя поля в wire — isKgvpV2, НЕ isKgvp)
	PPVzSalesCommission  flexFloat `json:"ppvzSalesCommission"`
	ForPay               flexFloat `json:"forPay"`          // = ppvz_for_pay
	AcquiringFee         flexFloat `json:"acquiringFee"`
	AcquiringPercent     flexFloat `json:"acquiringPercent"`
	VW                   flexFloat `json:"vw"`     // = ppvz_vw
	VWNds                flexFloat `json:"vwNds"` // = ppvz_vw_nds
	Penalty              flexFloat `json:"penalty"`
	AdditionalPayment    flexFloat `json:"additionalPayment"`
	RebillLogisticCost   flexFloat `json:"rebillLogisticCost"`
	PaidStorage          flexFloat `json:"paidStorage"`     // = storage_fee
	Deduction            flexFloat `json:"deduction"`
	PaidAcceptance       flexFloat `json:"paidAcceptance"`  // = acceptance
	DeliveryService      flexFloat `json:"deliveryService"` // = delivery_rub
	WibesDiscountPercent flexFloat `json:"wibesDiscountPercent"` // = wibes_wb_discount_percent
	CashbackAmount       flexFloat `json:"cashbackAmount"`
	CashbackDiscount     flexFloat `json:"cashbackDiscount"`
	CashbackCommChange   flexFloat `json:"cashbackCommissionChange"` // = cashback_commission_change
	LoyaltyDiscount      flexFloat `json:"loyaltyDiscount"`
	SalePricePromocodeDP flexFloat `json:"salePricePromocodeDiscountPrc"` // = sale_price_promocode_discount_prc
	SalePriceAffilDP     flexFloat `json:"salePriceAffiliatedDiscountPrc"`
	SalePriceWholesaleDP flexFloat `json:"salePriceWholesaleDiscountPrc"`
	B2BCustomerTin       string    `json:"b2bCustomerTin"`
	OrderUID             string    `json:"orderUid"`
}

// SalesReportDetailedPage получает одну страницу детализации отчёта реализации.
//
// Поведение:
//   - HTTP 200 + массив строк → декодирует и возвращает rows; HasMore=true,
//     если массив не пуст (далее вызывающий код использует rrdId последней
//     строки для следующей страницы).
//   - HTTP 204 → конец пагинации (HasMore=false, rows=nil).
//   - HTTP 429 → adaptiveReduce + возврат ошибки (итератор повторит).
//
// baseURL обычно FinanceReportsBaseURL; передаётся параметром для тестируемости.
func (c *Client) SalesReportDetailedPage(
	ctx context.Context,
	baseURL string,
	rateLimit int,
	burst int,
	req SalesReportsDetailedReq,
) (*ReportDetailByPeriodPageResult, error) {
	const toolID = "finance_sales_report"

	if rateLimit <= 0 {
		rateLimit = 1
	}
	if burst <= 0 {
		burst = 1
	}
	if req.Limit <= 0 || req.Limit > 100000 {
		req.Limit = 100000
	}

	limiter := c.getOrCreateLimiter(toolID, rateLimit, burst)
	if err := limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter wait: %w", err)
	}

	// Минимальный интервал между реальными HTTP-запросами (поверх token-bucket):
	// персональный лимит finance endpoint — 1 req/min.
	minInterval := time.Duration(float64(time.Minute) / float64(rateLimit))
	c.mu.RLock()
	lastReqTime := c.lastRequestTime[toolID]
	c.mu.RUnlock()
	if !lastReqTime.IsZero() {
		if elapsed := time.Since(lastReqTime); elapsed < minInterval {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(minInterval - elapsed):
			}
		}
	}

	u, err := url.Parse(baseURL + salesReportDetailedPath)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}

	bodyJSON, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}

	// Fallback на главный токен при 401 "token scope not allowed":
	// s2s-finance отвергает statistics-scoped WB_STAT. После первой такой
	// ошибки переключаемся на financeKey (обычно WB_API_KEY) до конца
	// жизни клиента. Максимум 2 попытки: исходный ключ → fallback.
	resp, body, _, err := c.sendFinanceRequest(ctx, u.String(), bodyJSON)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// HTTP 204 = конец пагинации
	if resp.StatusCode == http.StatusNoContent {
		c.adaptiveRecoverOK(toolID)
		c.mu.Lock()
		c.lastRequestTime[toolID] = time.Now()
		c.mu.Unlock()
		return &ReportDetailByPeriodPageResult{
			Rows:       nil,
			HasMore:    false,
			LastRrdID:  req.RrdID,
			StatusCode: 204,
		}, nil
	}

	// 429 — адаптивное снижение лимита
	if resp.StatusCode == http.StatusTooManyRequests {
		serverRetrySec := 1
		if s := resp.Header.Get("X-Ratelimit-Retry"); s != "" {
			if sec, err := strconv.Atoi(s); err == nil && sec > 0 {
				serverRetrySec = sec
			}
		}
		waitDur := c.adaptiveReduce(toolID, serverRetrySec)
		fmt.Fprintf(os.Stderr, "\u26a0\ufe0f  429 for %s, cooling down %v (server: %ds)\n",
			toolID, waitDur.Truncate(time.Second), serverRetrySec)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(waitDur):
		}
		return nil, fmt.Errorf("wb api error: status 429, body: %s", string(body))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wb api error: status %d, body: %s", resp.StatusCode, string(body))
	}

	c.adaptiveRecoverOK(toolID)
	c.mu.Lock()
	c.lastRequestTime[toolID] = time.Now()
	c.mu.Unlock()

	// Ответ — голый JSON-массив строк.
	var detailed []salesReportDetailedRow
	if err := json.Unmarshal(body, &detailed); err != nil {
		return nil, fmt.Errorf("decode detailed rows: %w (body head: %s)", err, headForLog(body))
	}

	rows := make([]RealizationReportRow, 0, len(detailed))
	lastRrdID := req.RrdID
	for _, d := range detailed {
		rows = append(rows, mapDetailedRowToRealization(d))
		if d.RrdID > 0 {
			lastRrdID = d.RrdID
		}
	}

	// Пустой массив трактуем как конец пагинации (как в supplier/orders).
	hasMore := len(rows) > 0

	return &ReportDetailByPeriodPageResult{
		Rows:       rows,
		HasMore:    hasMore,
		LastRrdID:  lastRrdID,
		StatusCode: 200,
	}, nil
}

// sendFinanceRequest выполняет POST к finance endpoint с автоматическим
// переключением токена при 401 "token scope not allowed". Возвращает ответ,
// прочитанное тело и флаг usedFallback (true, если запрос был повторен с
// financeKey). Вызывающий обязан закрыть resp.Body.
//
// Логика переключения:
//   - Если useFinanceKey уже выставлен (предыдущий 401) — сразу шлём с financeKey.
//   - Иначе шлём с apiKey; при 401 scope-ошибке и непустом financeKey
//     выставляем useFinanceKey=true, логируем переключение и шлём повторно.
//   - Любой другой не-OK статус (включая повторный 401 с fallback) возвращаем
//     как есть — финальную обработку делает SalesReportDetailedPage.
func (c *Client) sendFinanceRequest(ctx context.Context, url string, bodyJSON []byte) (*http.Response, []byte, bool, error) {
	const maxAttempts = 2
	usedFallback := false

	for attempt := 0; attempt < maxAttempts; attempt++ {
		c.mu.RLock()
		useFin := c.useFinanceKey && c.financeKey != ""
		c.mu.RUnlock()

		key := c.apiKey
		if useFin {
			key = c.financeKey
			usedFallback = true
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyJSON))
		if err != nil {
			return nil, nil, false, err
		}
		httpReq.Header.Set("Authorization", key)
		httpReq.Header.Set("Accept", "application/json")
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return nil, nil, usedFallback, fmt.Errorf("http request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			resp.Body.Close()
			return nil, nil, usedFallback, fmt.Errorf("read body: %w", err)
		}

		// 401 "token scope not allowed" / origin s2s-finance — переключаем токен.
		if isFinanceScopeError(resp.StatusCode, body) && !useFin && c.financeKey != "" {
			resp.Body.Close() // тело вычитано, соединение возвращаем в пул
			c.mu.Lock()
			c.useFinanceKey = true
			c.mu.Unlock()
			fmt.Fprintf(os.Stderr,
				"⚠️  401 token scope not allowed for finance endpoint, switching to fallback API key (attempt %d)\n",
				attempt+1)
			continue
		}

		// resp.Body уже дочитан, но оставляем его открытым: вызывающий код
		// (SalesReportDetailedPage) держит defer resp.Body.Close() и читает
		// из body []byte, а не из resp.Body. Закрытие ниже после проверки 204/429
		// в вызывающем безопасно: io.ReadAll уже завершился.
		return resp, body, usedFallback, nil
	}

	// Не должно случаться: maxAttempts=2, но на всякий случай — последняя попытка.
	return nil, nil, usedFallback, fmt.Errorf("finance request: max attempts exhausted")
}

// isFinanceScopeError возвращает true для 401 ответов, где WB-шлюз явно
// указал на несовпадение scope токена и сервиса finance. Шлюз s2s-finance
// помечает это либо detail'ом "token scope not allowed", либо origin'ом
// "s2s-finance" в теле ошибки.
func isFinanceScopeError(statusCode int, body []byte) bool {
	if statusCode != http.StatusUnauthorized {
		return false
	}
	s := string(body)
	return strings.Contains(s, "token scope not allowed") ||
		strings.Contains(s, "s2s-finance")
}

// SalesReportDetailedIterator перебирает все страницы детализации за период
// [dateFrom, dateTo] (RFC3339, MSK), передавая партии строк в callback.
// Пагинация — через rrdId последней строки до получения HTTP 204 / пустой страницы.
func (c *Client) SalesReportDetailedIterator(
	ctx context.Context,
	baseURL string,
	rateLimit int,
	burst int,
	dateFrom string,
	dateTo string,
	period string,
	callback func([]RealizationReportRow) error,
) (int, error) {
	totalCount := 0
	rrdID := 0

	const maxRetries = 3
	const baseBackoff = 5 * time.Second

	for {
		var page *ReportDetailByPeriodPageResult
		var err error

		for attempt := 0; attempt < maxRetries; attempt++ {
			page, err = c.SalesReportDetailedPage(ctx, baseURL, rateLimit, burst, SalesReportsDetailedReq{
				DateFrom: dateFrom,
				DateTo:   dateTo,
				Limit:    100000,
				RrdID:    rrdID,
				Period:   period,
			})
			if err == nil {
				break
			}
			if !isRetryableError(err) || attempt == maxRetries-1 {
				return totalCount, err
			}
			backoff := baseBackoff * time.Duration(1<<attempt)
			fmt.Printf("  ⚠️  Сетевая ошибка finance, повтор #%d через %v: %v\n", attempt+1, backoff, err)
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

// mapDetailedRowToRealization приводит camelCase-строку нового finance API к
// существующему RealizationReportRow (snake_case wire). flexFloat уже содержит
// распарсенное числовое значение (принимает и число, и строку из ответа);
// пустые/невалидные → 0.0 (как omitempty в старом endpoint).
func mapDetailedRowToRealization(d salesReportDetailedRow) RealizationReportRow {
	return RealizationReportRow{
		RrdID:                       d.RrdID,
		RealizationReportID:         int(d.ReportID),
		DocTypeName:                 d.DocTypeName,
		DateFrom:                    d.DateFrom,
		DateTo:                      d.DateTo,
		SupplierArticle:             d.VendorCode,
		SubjectName:                 d.SubjectName,
		NmID:                        d.NmID,
		BrandName:                   d.BrandName,
		TechSize:                    d.TechSize,
		Barcode:                     d.SKU,
		Quantity:                    d.Quantity,
		DeliveryMethod:              d.DeliveryMtd,
		GiBoxTypeName:               d.GiBoxTypeNm,
		OfficeName:                  d.OfficeName,
		PPVzForPay:                  float64(d.ForPay),
		RetailPrice:                 float64(d.RetailPrice),
		RetailAmount:                float64(d.RetailAmount),
		SalePercent:                 float64(d.SalePercent),
		CommissionPercent:           float64(d.CommissionPercent),
		DeliveryRub:                 float64(d.DeliveryService),
		OrderDT:                     d.OrderDT,
		SaleDT:                      d.SaleDT,
		RRDT:                        d.RRDate,
		SupplierOperName:            d.SellerOperNm,
		ShkID:                       d.ShkID,
		Srid:                        d.Srid,
		RebillLogisticCost:          float64(d.RebillLogisticCost),
		PPVzVw:                      float64(d.VW),
		PPVzVwNds:                   float64(d.VWNds),
		RetailPriceWithDiscRub:      float64(d.RetailPriceWithDisc),
		PPVzSppPrc:                  float64(d.SPP),
		PPVzKvwPrcBase:              float64(d.KvwBase),
		PPVzKvwPrc:                  float64(d.Kvw),
		SupRatingPrcUp:              float64(d.SupRatingUp),
		IsKgvpV2:                    float64(d.IsKgvp),
		PPVzSalesCommission:         float64(d.PPVzSalesCommission),
		AcquiringFee:                float64(d.AcquiringFee),
		AcquiringPercent:            float64(d.AcquiringPercent),
		ProductDiscountForReport:    float64(d.ProductDiscountForR),
		SupplierPromo:               float64(d.SellerPromo),
		SellerPromoDiscount:         float64(d.SellerPromoDiscount),
		SalePricePromocodeDiscPrc:   float64(d.SalePricePromocodeDP),
		WibesWbDiscountPercent:      float64(d.WibesDiscountPercent),
		LoyaltyDiscount:             float64(d.LoyaltyDiscount),
		CashbackAmount:              float64(d.CashbackAmount),
		CashbackDiscount:            float64(d.CashbackDiscount),
		CashbackCommissionChange:    float64(d.CashbackCommChange),
		Penalty:                     float64(d.Penalty),
		Deduction:                   float64(d.Deduction),
		StorageFee:                  float64(d.PaidStorage),
		Acceptance:                  float64(d.PaidAcceptance),
		GiID:                        d.GiID,
		B2BCustomerTin:              d.B2BCustomerTin,
		OrderUID:                    d.OrderUID,
		IsLegalEntity:               d.IsB2b,
		SalePriceAffiliatedDiscountPrc: float64(d.SalePriceAffilDP),
		SalePriceWholesaleDiscountPrc:  float64(d.SalePriceWholesaleDP),
	}
}

// flexFloat принимает в JSON и число (0, 42, 4.06), и строку ("1249", "14.89",
// ""). WB inconsistent: одно и то же поле в разных строках может прийти в
// любом из форматов, поэтому декодируем устойчиво. Пустая/невалидная → 0.
type flexFloat float64

// UnmarshalJSON реализует accept-both: number или quoted-string.
func (f *flexFloat) UnmarshalJSON(data []byte) error {
	s := string(data)
	// Строка в кавычках — парсим содержимое.
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		inner := s[1 : len(s)-1]
		if inner == "" {
			*f = 0
			return nil
		}
		v, err := strconv.ParseFloat(inner, 64)
		if err != nil {
			*f = 0
			return nil // не роняем unmarshal всей строки — тихо 0
		}
		*f = flexFloat(v)
		return nil
	}
	// null → 0.
	if s == "null" {
		*f = 0
		return nil
	}
	// Число.
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0
		return nil
	}
	*f = flexFloat(v)
	return nil
}

// headForLog возвращает первые 200 байт тела для диагностического сообщения.
func headForLog(b []byte) string {
	const max = 200
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}
