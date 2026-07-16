package wb

import (
	"encoding/json"
	"math"
	"testing"
)

// TestMapDetailedRow_Parity проверяет, что все ключевые поля
// RealizationReportRow корректно восстанавливаются из camelCase ответа
// нового finance endpoint. WB inconsistent: числовые поля могут прийти
// строкой ("retailPrice":"1249") или числом ("sellerPromo":0). Образец
// смешан намеренно; основан на реальном ответе finance-api от 2026-07-16.
func TestMapDetailedRow_Parity(t *testing.T) {
	// Числовые поля — вперемешку string/number (как реально отдаёт finance API).
	raw := `[{
		"reportId": 1234567,
		"dateFrom": "2026-03-16",
		"dateTo": "2026-03-22",
		"createDate": "2026-03-23",
		"currency": "RUB",
		"reportType": 1,
		"rrdId": 1232610467,
		"giId": 123456,
		"subjectName": "Мини-печи",
		"nmId": 1234567,
		"brandName": "BlahBlah",
		"vendorCode": "MAB123",
		"title": "ДС тарелка",
		"techSize": "0",
		"sku": "1231312352310",
		"docTypeName": "Продажа",
		"quantity": 1,
		"retailPrice": "1249",
		"retailAmount": "367",
		"salePercent": 0,
		"commissionPercent": 24,
		"officeName": "Коледино",
		"sellerOperName": "Продажа",
		"orderDt": "2026-03-14T00:00:00Z",
		"saleDt": "2026-03-21T00:00:00Z",
		"rrDate": "2026-03-21",
		"shkId": 1239159661,
		"retailPriceWithDisc": "399.68",
		"giBoxTypeName": "Монопаллета",
		"deliveryMethod": "FBS, (МГТ)",
		"srid": "0f1c3999172603062979867564654dac5b702849",
		"orderId": 2816993144,
		"isB2b": false,
		"ppvzSalesCommission": "23.74",
		"forPay": "376.99",
		"acquiringFee": "14.89",
		"acquiringPercent": 4.06,
		"vw": "22.25",
		"vwNds": "4.45",
		"penalty": "231.35",
		"deduction": "6354",
		"paidStorage": "12647.29",
		"paidAcceptance": "865",
		"deliveryService": "55.5",
		"spp": 25.31,
		"kvwBase": 24.15,
		"kvw": 1.81,
		"supRatingUp": 0.5,
		"isKgvpV2": 0,
		"productDiscountForReport": 10,
		"sellerPromo": 0,
		"sellerPromoDiscount": 3,
		"wibesDiscountPercent": 1,
		"cashbackAmount": "2",
		"cashbackDiscount": "19",
		"cashbackCommissionChange": "0.2",
		"loyaltyDiscount": 5,
		"salePricePromocodeDiscountPrc": 0,
		"salePriceAffiliatedDiscountPrc": 0,
		"salePriceWholesaleDiscountPrc": 0,
		"b2bCustomerTin": "010101010101",
		"orderUid": "id375f16c4bec295d9995393af803ff7b"
	}]`

	var rows []salesReportDetailedRow
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}

	got := mapDetailedRowToRealization(rows[0])

	// Проверка всех групп полей.
	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"PPVzForPay (forPay)", got.PPVzForPay, 376.99},
		{"RetailPrice", got.RetailPrice, 1249},
		{"RetailAmount", got.RetailAmount, 367},
		{"CommissionPercent", got.CommissionPercent, 24},
		{"DeliveryRub (deliveryService)", got.DeliveryRub, 55.5},
		{"RetailPriceWithDiscRub", got.RetailPriceWithDiscRub, 399.68},
		{"PPVzSppPrc (spp)", got.PPVzSppPrc, 25.31},
		{"PPVzKvwPrcBase (kvwBase)", got.PPVzKvwPrcBase, 24.15},
		{"PPVzKvwPrc (kvw)", got.PPVzKvwPrc, 1.81},
		{"SupRatingPrcUp", got.SupRatingPrcUp, 0.5},
		{"PPVzSalesCommission", got.PPVzSalesCommission, 23.74},
		{"AcquiringFee", got.AcquiringFee, 14.89},
		{"AcquiringPercent", got.AcquiringPercent, 4.06},
		{"PPVzVw (vw)", got.PPVzVw, 22.25},
		{"PPVzVwNds (vwNds)", got.PPVzVwNds, 4.45},
		{"Penalty", got.Penalty, 231.35},
		{"Deduction", got.Deduction, 6354},
		{"StorageFee (paidStorage)", got.StorageFee, 12647.29},
		{"Acceptance (paidAcceptance)", got.Acceptance, 865},
		{"CashbackAmount", got.CashbackAmount, 2},
		{"CashbackDiscount", got.CashbackDiscount, 19},
		{"CashbackCommissionChange", got.CashbackCommissionChange, 0.2},
	}
	for _, c := range checks {
		if !floatEq(c.got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}

	// ID / строковые поля.
	if got.RrdID != 1232610467 {
		t.Errorf("RrdID = %d, want 1232610467", got.RrdID)
	}
	if got.RealizationReportID != 1234567 {
		t.Errorf("RealizationReportID = %d, want 1234567", got.RealizationReportID)
	}
	if got.NmID != 1234567 {
		t.Errorf("NmID = %d, want 1234567", got.NmID)
	}
	if got.SupplierArticle != "MAB123" {
		t.Errorf("SupplierArticle = %q (vendorCode mapping), want MAB123", got.SupplierArticle)
	}
	if got.TechSize != "0" {
		t.Errorf("TechSize = %q (techSize mapping), want 0", got.TechSize)
	}
	if got.Barcode != "1231312352310" {
		t.Errorf("Barcode = %q (sku mapping), want 1231312352310", got.Barcode)
	}
	if got.RRDT != "2026-03-21" {
		t.Errorf("RRDT = %q (rrDate mapping), want 2026-03-21", got.RRDT)
	}
	if got.Srid != "0f1c3999172603062979867564654dac5b702849" {
		t.Errorf("Srid = %q, want hex id", got.Srid)
	}
	if got.B2BCustomerTin != "010101010101" {
		t.Errorf("B2BCustomerTin = %q, want 010101010101", got.B2BCustomerTin)
	}
	if got.OrderUID != "id375f16c4bec295d9995393af803ff7b" {
		t.Errorf("OrderUID = %q (orderUid mapping), want basket id", got.OrderUID)
	}
	if got.IsLegalEntity != false {
		t.Errorf("IsLegalEntity = %v (isB2b mapping), want false", got.IsLegalEntity)
	}
}

// TestFlexFloat_NumberAndString покрывает, что flexFloat принимает оба формата
// finance API — число и строку в кавычках — и не падает на невалидных/пустых.
func TestFlexFloat_NumberAndString(t *testing.T) {
	cases := []struct {
		json string
		want float64
	}{
		{`1249`, 1249},
		{`"1249"`, 1249},
		{`14.89`, 14.89},
		{`"14.89"`, 14.89},
		{`""`, 0},
		{`"not-a-number"`, 0},
		{`0`, 0},
		{`null`, 0},
		{`-5.5`, -5.5},
		{`"-5.5"`, -5.5},
	}
	for _, c := range cases {
		var f flexFloat
		if err := json.Unmarshal([]byte(c.json), &f); err != nil {
			t.Errorf("Unmarshal(%s) unexpected error: %v", c.json, err)
			continue
		}
		if !floatEq(float64(f), c.want) {
			t.Errorf("Unmarshal(%s) = %v, want %v", c.json, float64(f), c.want)
		}
	}
}

func floatEq(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}
