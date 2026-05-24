package cardupdate

import (
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func TestToUpdateItem_UnwrapsCharacteristics(t *testing.T) {
	card := FullCardData{
		NmID:        12345,
		VendorCode:  "VC001",
		Brand:       "Brand",
		Title:       "Title",
		Description: "Desc",
		Dimensions:  wb.CardDimensions{Length: 10, Width: 5, Height: 3, WeightBrutto: 0.5, IsValid: true},
		Characteristics: []CardChar{
			{CharID: 1, Value: `["синий"]`},
			{CharID: 2, Value: `[42]`},
			{CharID: 3, Value: `[2.5]`},
			{CharID: 4, Value: `[true]`},
		},
		Sizes: []wb.CardSize{
			{ChrtID: 100, TechSize: "42", WBSize: "42", Skus: []string{"sku1"}},
		},
	}

	item := ToUpdateItem(card)

	if item.NmID != 12345 {
		t.Errorf("NmID: got %d", item.NmID)
	}
	if item.VendorCode != "VC001" {
		t.Errorf("VendorCode: got %s", item.VendorCode)
	}
	if item.Brand != "Brand" {
		t.Errorf("Brand: got %s", item.Brand)
	}
	if item.Dimensions == nil || item.Dimensions.Length != 10 {
		t.Errorf("Dimensions: got %v", item.Dimensions)
	}

	if len(item.Characteristics) != 4 {
		t.Fatalf("expected 4 chars, got %d", len(item.Characteristics))
	}

	// String unwrapped from ["синий"]
	if item.Characteristics[0].Value != "синий" {
		t.Errorf("char 0: got %v (%T), want string синий", item.Characteristics[0].Value, item.Characteristics[0].Value)
	}

	// Int unwrapped from [42]
	if item.Characteristics[1].Value != 42 {
		t.Errorf("char 1: got %v (%T), want int 42", item.Characteristics[1].Value, item.Characteristics[1].Value)
	}

	// Float unwrapped from [2.5]
	if item.Characteristics[2].Value != 2.5 {
		t.Errorf("char 2: got %v (%T), want float64 2.5", item.Characteristics[2].Value, item.Characteristics[2].Value)
	}

	// Bool unwrapped from [true]
	if item.Characteristics[3].Value != true {
		t.Errorf("char 3: got %v (%T), want bool true", item.Characteristics[3].Value, item.Characteristics[3].Value)
	}

	if len(item.Sizes) != 1 || item.Sizes[0].ChrtID != 100 {
		t.Errorf("Sizes: got %v", item.Sizes)
	}
}

func TestToUpdateItem_EmptyCharacteristics(t *testing.T) {
	card := FullCardData{
		NmID:            1,
		Characteristics: nil,
	}
	item := ToUpdateItem(card)

	if item.Characteristics == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	if len(item.Characteristics) != 0 {
		t.Errorf("expected 0 chars, got %d", len(item.Characteristics))
	}
}

func TestWBUpdateConfig_Defaults(t *testing.T) {
	cfg := WBUpdateConfig{}.Defaults()

	if cfg.BatchSize != 30 {
		t.Errorf("BatchSize: got %d", cfg.BatchSize)
	}
	if cfg.RatePerMin != 8 {
		t.Errorf("RatePerMin: got %d", cfg.RatePerMin)
	}
	if cfg.RateBurst != 2 {
		t.Errorf("RateBurst: got %d", cfg.RateBurst)
	}
	if cfg.APIFloorPerMin != 5 {
		t.Errorf("APIFloorPerMin: got %d", cfg.APIFloorPerMin)
	}
	if cfg.APIFloorBurst != 1 {
		t.Errorf("APIFloorBurst: got %d", cfg.APIFloorBurst)
	}
	if cfg.IntervalSeconds != 8 {
		t.Errorf("IntervalSeconds: got %d", cfg.IntervalSeconds)
	}
}

func TestWBUpdateConfig_PartialOverride(t *testing.T) {
	cfg := WBUpdateConfig{BatchSize: 50}.Defaults()

	if cfg.BatchSize != 50 {
		t.Errorf("BatchSize should be preserved: got %d", cfg.BatchSize)
	}
	if cfg.RatePerMin != 8 {
		t.Errorf("RatePerMin should be default: got %d", cfg.RatePerMin)
	}
}
