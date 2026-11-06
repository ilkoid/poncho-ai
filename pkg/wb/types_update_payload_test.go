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
