package wb

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCardUpdateItemPayloadOmitsIsValid — контракт cards/update: поле
// isValid существует только в GET-ответах, в схеме update его НЕТ.
// Регрессия инцидента fill-dims (28.10–06.11): поле летело в payload с июня
// 2026 через общую структуру CardDimensions, после серверного ужесточения
// WB 27–28.10 стало кандидатом №1 на 400 «Invalid request format».
func TestCardUpdateItemPayloadOmitsIsValid(t *testing.T) {
	item := CardUpdateItem{
		NmID:       1,
		VendorCode: "10001",
		Brand:      "PlayToday",
		Title:      "Тест",
		Dimensions: &CardDimensionsUpdate{Length: 40, Width: 10, Height: 30, WeightBrutto: 0.5},
	}
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	payload := string(raw)
	if strings.Contains(strings.ToLower(payload), "isvalid") {
		t.Fatalf("payload cards/update содержит isValid: %s", payload)
	}
	for _, want := range []string{`"length":40`, `"width":10`, `"height":30`, `"weightBrutto":0.5`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload потерял габариты (%s): %s", want, payload)
		}
	}
}

// TestCardUpdateDocumentOmitsEmptyOptionalFields — родное эхо документов:
// cards/list НЕ возвращает отсутствующие productNumber/tradeName/applicant/
// endDate, значит и в cards/update этих ключей быть не должно. Пустые строки ""
// в опциональных/date-полях с 27–28.10 дают 400 «Invalid request format»
// на весь запрос — корневая причина инцидента fill-dims 28.10–08.11
// (доказано пробами 08.11: docs с "" → 400, docs в форме WB → 200).
func TestCardUpdateDocumentOmitsEmptyOptionalFields(t *testing.T) {
	doc := CardUpdateDocument{
		ID:        "7ab9e8d2-0000-0000-0000-000000000000",
		Type:      1,
		Number:    "ЕАЭС RU.Д-RU.АБ01.В.00000/0000",
		StartDate: "2026-10-20T00:00:00Z",
		IsEndless: true,
		// ProductNumber/TradeName/Applicant/EndDate не заданы — как в ответе cards/list
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	payload := string(raw)
	for _, banned := range []string{"productNumber", "tradeName", "applicant", "endDate"} {
		if strings.Contains(payload, `"`+banned+`"`) {
			t.Fatalf("пустое опциональное поле %q ушло в payload: %s", banned, payload)
		}
	}
	for _, want := range []string{`"id":"7ab9e8d2`, `"type":1`, `"number":`, `"startDate":"2026-10-20`, `"isEndless":true`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload потерял обязательное поле (%s): %s", want, payload)
		}
	}

	// Заполненные опциональные поля обязаны остаться в эхе (реальные end-даты).
	doc.EndDate = "2030-10-20T00:00:00Z"
	doc.TradeName = "PlayToday"
	raw2, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	payload2 := string(raw2)
	if !strings.Contains(payload2, `"endDate":"2030-10-20`) || !strings.Contains(payload2, `"tradeName":"PlayToday"`) {
		t.Fatalf("заполненные опциональные поля потерялись: %s", payload2)
	}
}
