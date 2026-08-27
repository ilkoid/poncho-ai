package wb

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Fixture собран по примерам AcquiringReportsDetailedRes
// (13-finances.yaml:1709-1800): деньги строками, ИНН/КПП строками с нулями.
const acquiringFixture = `[{
	"rrdId": 1232610467,
	"reportId": 1234567,
	"acqDate": "2026-03-21",
	"saleDate": "2026-03-21",
	"docTypeName": "Продажа",
	"nmId": 1234567,
	"retailAmount": "367",
	"acquiringFee": "14.89",
	"acquiringFeeVat": "4.06",
	"acquiringBank": "Тинькофф",
	"tin": "010101010101",
	"taxRegistrationReasonCode": "7701123301",
	"invoiceNumber": "С/Ф 123",
	"invoiceDate": "2026-03-20",
	"shkId": 1239159661,
	"srid": "D0.r3f80c3eec6f845c6840128b4c19986f9.0.0",
	"currency": "RUB"
}]`

// TestAcquiringReportDetailedPage_DecodeAndCursor: 200 с массивом → декод и
// курсор rrdId; повторный запрос уходит с rrdId последней строки; 204 → конец.
func TestAcquiringReportDetailedPage_DecodeAndCursor(t *testing.T) {
	var (
		mu     sync.Mutex
		bodies []string
		reply  int // 0 → отдать fixture, иначе → 204
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != acquiringDetailedPath {
			t.Errorf("path = %s, want %s", r.URL.Path, acquiringDetailedPath)
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mode := reply
		reply++
		mu.Unlock()

		if mode > 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, acquiringFixture)
	}))
	defer srv.Close()

	c := New("WB_STAT")
	c.SetHTTPClient(srv.Client())

	page, err := c.AcquiringReportDetailedPage(context.Background(), srv.URL, 1, 1,
		AcquiringDetailedReq{DateFrom: "2026-08-10T00:00:00+03:00", DateTo: "2026-08-16T23:59:59+03:00"})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if !page.HasMore || len(page.Rows) != 1 {
		t.Fatalf("page 1: HasMore=%v rows=%d, want true/1", page.HasMore, len(page.Rows))
	}

	row := page.Rows[0]
	if row.RrdID != 1232610467 || row.ReportID != 1234567 || row.NmID != 1234567 {
		t.Errorf("ids = %+v", row)
	}
	if !floatEq(float64(row.AcquiringFee), 14.89) || !floatEq(float64(row.AcquiringFeeVat), 4.06) || !floatEq(float64(row.RetailAmount), 367) {
		t.Errorf("money fields = fee %v vat %v retail %v, want 14.89/4.06/367", row.AcquiringFee, row.AcquiringFeeVat, row.RetailAmount)
	}
	if row.AcquiringBank != "Тинькофф" || row.Tin != "010101010101" || row.Kpp != "7701123301" {
		t.Errorf("bank/tin/kpp = %q/%q/%q", row.AcquiringBank, row.Tin, row.Kpp)
	}
	if row.InvoiceNumber != "С/Ф 123" || row.InvoiceDate != "2026-03-20" {
		t.Errorf("invoice = %q/%q", row.InvoiceNumber, row.InvoiceDate)
	}
	if page.LastRrdID != 1232610467 {
		t.Errorf("LastRrdID = %d, want 1232610467", page.LastRrdID)
	}

	// Второй запрос — курсор rrdId из последней строки первой страницы.
	page2, err := c.AcquiringReportDetailedPage(context.Background(), srv.URL, 1, 1,
		AcquiringDetailedReq{DateFrom: "2026-08-10T00:00:00+03:00", DateTo: "2026-08-16T23:59:59+03:00", RrdID: page.LastRrdID})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if page2.HasMore || page2.Rows != nil {
		t.Fatalf("page 2 after 204: HasMore=%v rows=%v, want false/nil", page2.HasMore, page2.Rows)
	}

	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want 2", len(bodies))
	}
	var reqBody struct {
		RrdID int `json:"rrdId"`
	}
	if err := json.Unmarshal([]byte(bodies[1]), &reqBody); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if reqBody.RrdID != 1232610467 {
		t.Errorf("second request rrdId = %d, want 1232610467", reqBody.RrdID)
	}
}

// TestAcquiringReportList: путь эндпоинта, decode итогов (деньги строками),
// короткая страница (< limit) завершает пагинацию.
func TestAcquiringReportList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != acquiringListPath {
			t.Errorf("path = %s, want %s", r.URL.Path, acquiringListPath)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{
			"reportId": 307401554,
			"sellerFinanceName": "ИП Кружинин В. Р.",
			"dateFrom": "2026-03-16",
			"dateTo": "2026-03-22",
			"createDate": "2026-03-31",
			"currency": "RUB",
			"acquiringFeeSum": "258",
			"acquiringFeeVatSum": "83.79"
		}]`)
	}))
	defer srv.Close()

	c := New("WB_STAT")
	c.SetHTTPClient(srv.Client())

	reports, err := c.AcquiringReportList(context.Background(), srv.URL, 1, 1,
		"2026-08-10T00:00:00+03:00", "2026-08-16T23:59:59+03:00")
	if err != nil {
		t.Fatalf("AcquiringReportList: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(reports))
	}
	r := reports[0]
	if r.ReportID != 307401554 || r.DateFrom != "2026-03-16" || r.CreateDate != "2026-03-31" {
		t.Errorf("report meta = %+v", r)
	}
	if !floatEq(float64(r.AcquiringFeeSum), 258) || !floatEq(float64(r.AcquiringFeeVatSum), 83.79) {
		t.Errorf("sums = %v/%v, want 258/83.79", r.AcquiringFeeSum, r.AcquiringFeeVatSum)
	}
	if !strings.HasPrefix(r.SellerFinanceName, "ИП ") {
		t.Errorf("sellerFinanceName = %q", r.SellerFinanceName)
	}
}
