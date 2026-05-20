package main

import (
	"encoding/json"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/utils"
)

func TestFlattenJSONString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"string value", `"белый"`, "белый"},
		{"string with spaces", `"белый с голубым"`, "белый с голубым"},
		{"array of strings", `["белый","синий"]`, "белый, синий"},
		{"array of three", `["белый","синий","красный"]`, "белый, синий, красный"},
		{"number fallback", `123`, "123"},
		{"empty string", `""`, ""},
		{"empty array", `[]`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := flattenJSONString([]byte(tt.input))
			if got != tt.want {
				t.Errorf("flattenJSONString(%s) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFlexibleMap(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]string
		wantErr bool
	}{
		{
			"string values",
			`{"цвет":"белый","покрой":"прямой"}`,
			map[string]string{"цвет": "белый", "покрой": "прямой"},
			false,
		},
		{
			"array values flattened",
			`{"цвет":["белый","синий"]}`,
			map[string]string{"цвет": "белый, синий"},
			false,
		},
		{
			"mixed string and array",
			`{"цвет":["белый","синий"],"покрой":"прямой"}`,
			map[string]string{"цвет": "белый, синий", "покрой": "прямой"},
			false,
		},
		{
			"empty object",
			`{}`,
			map[string]string{},
			false,
		},
		{
			"number values as fallback",
			`{"count":42}`,
			map[string]string{"count": "42"},
			false,
		},
		{
			"invalid JSON",
			`not json`,
			nil,
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got flexibleMap
			err := json.Unmarshal([]byte(tt.input), &got)
			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal flexibleMap: err=%v, wantErr=%v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("flexibleMap len=%d, want %d", len(got), len(tt.want))
				return
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("flexibleMap[%q]=%q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestFlexibleMapInStruct(t *testing.T) {
	input := `{
		"product_type": "платье",
		"attributes": {"цвет": ["белый","синий"], "покрой": "прямой"},
		"discrepancy": true,
		"issues": [{"field":"цвет","card_value":"синий","correct_value":"белый, синий","reason":"доминирующий белый"}],
		"summary": "цвет указан неверно"
	}`

	var result visionAnalysisResult
	if err := json.Unmarshal([]byte(input), &result); err != nil {
		t.Fatalf("Unmarshal visionAnalysisResult: %v", err)
	}

	if result.ProductType != "платье" {
		t.Errorf("ProductType=%q, want %q", result.ProductType, "платье")
	}
	if result.Attributes["цвет"] != "белый, синий" {
		t.Errorf("Attributes[цвет]=%q, want %q", result.Attributes["цвет"], "белый, синий")
	}
	if result.Attributes["покрой"] != "прямой" {
		t.Errorf("Attributes[покрой]=%q, want %q", result.Attributes["покрой"], "прямой")
	}
	if !result.Discrepancy {
		t.Error("Discrepancy=false, want true")
	}
	if len(result.Issues) != 1 {
		t.Fatalf("Issues len=%d, want 1", len(result.Issues))
	}
	if result.Issues[0].Field != "цвет" {
		t.Errorf("Issues[0].Field=%q, want %q", result.Issues[0].Field, "цвет")
	}
}

func TestFlexibleString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"string", `"белый"`, "белый", false},
		{"array", `["белый","синий"]`, "белый, синий", false},
		{"empty string", `""`, "", false},
		{"number", `42`, "42", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got flexibleString
			err := json.Unmarshal([]byte(tt.input), &got)
			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal flexibleString: err=%v, wantErr=%v", err, tt.wantErr)
				return
			}
			if string(got) != tt.want {
				t.Errorf("flexibleString=%q, want %q", string(got), tt.want)
			}
		})
	}
}

func TestFlexibleStringInCharResult(t *testing.T) {
	input := `{"characteristics": [{"charc_id": 12345, "value": ["белый","синий"]}]}`

	type charResult struct {
		CharcID int             `json:"charc_id"`
		Value   flexibleString  `json:"value"`
	}
	type charsResponse struct {
		Characteristics []charResult `json:"characteristics"`
	}

	var result charsResponse
	if err := json.Unmarshal([]byte(input), &result); err != nil {
		t.Fatalf("Unmarshal with array value: %v", err)
	}
	if len(result.Characteristics) != 1 {
		t.Fatalf("len=%d, want 1", len(result.Characteristics))
	}
	if result.Characteristics[0].Value != "белый, синий" {
		t.Errorf("Value=%q, want %q", result.Characteristics[0].Value, "белый, синий")
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain json", `{"a":1}`, `{"a":1}`},
		{"markdown json fence", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"markdown code fence", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"text before json", `Вот ответ: {"a":1}`, `{"a":1}`},
		{"text after json", `{"a":1} конец`, `{"a":1}`},
		{"no json", `no json here`, ``},
		{"nested json", `{"a":{"b":2},"c":3}`, `{"a":{"b":2},"c":3}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaned := utils.CleanJsonBlock(tt.input)
			got := utils.ExtractJSON(cleaned)
			if got != tt.want {
				t.Errorf("ExtractJSON(CleanJsonBlock())=%q, want %q", got, tt.want)
			}
		})
	}
}
