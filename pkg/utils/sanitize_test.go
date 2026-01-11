package utils

import (
	"strings"
	"testing"
)

func TestSanitizePLMJson(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string // Must contain these substrings
		notContains []string // Must NOT contain these substrings
	}{
		{
			name: "removes Ответственные block",
			input: `{"Реквизиты":{"Артикул":"12345"},"Ответственные":[{"НомерСтроки":1}]}`,
			contains: []string{"Артикул", "12345"},
			notContains: []string{"Ответственные", "НомерСтроки"},
		},
		{
			name: "removes Эскизы block",
			input: `{"Реквизиты":{"Артикул":"12345"},"Эскизы":[{"НомерСтроки":1}]}`,
			contains: []string{"Артикул", "12345"},
			notContains: []string{"Эскизы", "НомерСтроки"},
		},
		{
			name: "removes Миниатюра_Файл (base64 data)",
			input: `{"Реквизиты":{"Артикул":"12345","Миниатюра_Файл":"iVBORw0KGgoAAAANSUhEUg..."}}`,
			contains: []string{"Артикул", "12345"},
			notContains: []string{"Миниатюра_Файл", "iVBORw0KG"},
		},
		{
			name: "removes multiple technical fields",
			input: `{
				"Реквизиты": {
					"Артикул": "12345",
					"Наименование": "Test Product",
					"НомерСтроки": 1,
					"ИдентификаторСтроки": "abc",
					"Статус": "Active",
					"Код": 123
				}
			}`,
			contains: []string{"Артикул", "12345", "Наименование", "Test Product"},
			notContains: []string{"НомерСтроки", "ИдентификаторСтроки", "Статус", `"Код"`},
		},
		{
			name: "keeps useful product data",
			input: `{
				"Реквизиты": {
					"Артикул": "12611516",
					"Наименование": "Куртка джинсовая",
					"СезоннаяКоллекция": "Весна-Лето 2026",
					"РазмерныйРяд": "128,134,140",
					"Цвета": [{"Пантон": "Blue"}]
				}
			}`,
			contains: []string{
				"Артикул", "12611516",
				"Наименование", "Куртка джинсовая",
				"СезоннаяКоллекция", "Весна-Лето 2026",
				"РазмерныйРяд", "128,134,140",
				"Пантон", "Blue",
			},
			notContains: []string{},
		},
		{
			name: "removes empty values",
			input: `{"Реквизиты":{"Артикул":"12345","Конструкция":"","Комментарий":"   "}}`,
			contains: []string{"Артикул", "12345"},
			notContains: []string{"Конструкция", "Комментарий"},
		},
		{
			name: "removes technical fields from nested arrays",
			input: `{
				"Цвета": [
					{"НомерСтроки":1,"Пантон":"Blue","Основной":true},
					{"НомерСтроки":2,"Пантон":"Red","Основной":false}
				]
			}`,
			contains: []string{"Пантон", "Blue", "Red", "Основной"},
			notContains: []string{"НомерСтроки"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SanitizePLMJson(tt.input)
			if err != nil {
				t.Fatalf("SanitizePLMJson() error = %v", err)
			}

			// Check required substrings
			for _, mustContain := range tt.contains {
				if !strings.Contains(result, mustContain) {
					t.Errorf("Result must contain %q, but got:\n%s", mustContain, result)
				}
			}

			// Check forbidden substrings
			for _, mustNotContain := range tt.notContains {
				if strings.Contains(result, mustNotContain) {
					t.Errorf("Result must NOT contain %q, but got:\n%s", mustNotContain, result)
				}
			}
		})
	}
}

func TestSanitizePLMJsonRealExample(t *testing.T) {
	// Test with a subset of real PLM JSON structure
	input := `{
		"Реквизиты": {
			"Код": 12588,
			"Наименование": "12611516 Куртка джинсовая Мужской Tween",
			"Артикул": "12611516",
			"СезоннаяКоллекция": "Весна-Лето 2026",
			"ТоварнаяПодгруппа": "Куртка джинсовая",
			"РазмерныйРяд": "128,134,140,146,152,158,164,170,176",
			"Комментарий": "Fit Oversize",
			"Миниатюра_Файл": "iVBORw0KGgoAAAANSUhEUgAAANQAAACWCAYAAACmTqZ/AAAAIGNIUk0AA...",
			"Код": 12588
		},
		"Ответственные": [
			{"НомерСтроки":1,"Роль":"Designer","Сотрудник":"John"},
			{"НомерСтроки":2,"Роль":"Merch","Сотрудник":"Jane"}
		],
		"Эскизы": [
			{"НомерСтроки":1,"Файл":"sketch1.jpg"}
		]
	}`

	result, err := SanitizePLMJson(input)
	if err != nil {
		t.Fatalf("SanitizePLMJson() error = %v", err)
	}

	// Must keep important data
	mustContain := []string{
		"Артикул",
		"12611516",
		"Наименование",
		"Куртка джинсовая",
		"СезоннаяКоллекция",
		"Весна-Лето 2026",
		"ТоварнаяПодгруппа",
		"РазмерныйРяд",
		"128,134,140",
		"Комментарий",
		"Fit Oversize",
	}

	// Must remove technical/noise data
	mustNotContain := []string{
		"Ответственные",
		"Эскизы",
		"НомерСтроки",
		"Миниатюра_Файл",
		"iVBORw0KG",
		"Роль",
		"Сотрудник",
		"Файл",
		`"Код": 12588`, // Technical code field
	}

	for _, s := range mustContain {
		if !strings.Contains(result, s) {
			t.Errorf("Result must contain %q\nGot:\n%s", s, result)
		}
	}

	for _, s := range mustNotContain {
		if strings.Contains(result, s) {
			t.Errorf("Result must NOT contain %q\nGot:\n%s", s, result)
		}
	}

	// Check that result is significantly smaller than input
	inputLen := len(input)
	resultLen := len(result)
	if resultLen > inputLen*80/100 { // Should be at least 20% smaller
		t.Errorf("Sanitized JSON (%d bytes) should be significantly smaller than input (%d bytes)", resultLen, inputLen)
	}
}
