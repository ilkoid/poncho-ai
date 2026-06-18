package scrub

import (
	"encoding/json"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// TestReplacer_ApplySlice_ProductCard is the contract test: it exercises the
// realistic shape (a []wb.ProductCard value slice) and asserts that preventive
// scrubbing reaches every leak vector. If pkg/wb field names change, this fails.
func TestReplacer_ApplySlice_ProductCard(t *testing.T) {
	r, err := New(Rules{Rules: []Rule{
		{Find: "PlayToday", Replace: "[PlayBrand]"},
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cards := []wb.ProductCard{{
		Brand:       "PlayToday",
		Title:       "Кроссовки PLAYTODAY детские",
		Description: "лучший playtoday выбор",
		VendorCode:  "12345678", // must NOT be touched
		Tags:        []wb.CardTag{{Name: "PlayToday collection"}},
		Characteristics: []wb.CardCharacteristic{{
			Name:     "Бренд",
			ValueRaw: json.RawMessage(`"PlayToday"`),
		}},
	}}

	r.ApplySlice(cards)
	c := cards[0]

	if c.Brand != "[PlayBrand]" {
		t.Errorf("Brand = %q, want %q", c.Brand, "[PlayBrand]")
	}
	if c.Title != "Кроссовки [PlayBrand] детские" {
		t.Errorf("Title = %q, want %q", c.Title, "Кроссовки [PlayBrand] детские")
	}
	if c.Description != "лучший [PlayBrand] выбор" {
		t.Errorf("Description = %q, want %q", c.Description, "лучший [PlayBrand] выбор")
	}
	if c.VendorCode != "12345678" {
		t.Errorf("VendorCode = %q, want untouched 12345678", c.VendorCode)
	}
	// Recursion into a nested slice.
	if got := c.Tags[0].Name; got != "[PlayBrand] collection" {
		t.Errorf("Tags[0].Name = %q, want %q", got, "[PlayBrand] collection")
	}
	// json.RawMessage field — the parity gap-closer (characteristic value).
	wantRaw := `"[PlayBrand]"`
	if got := string(c.Characteristics[0].ValueRaw); got != wantRaw {
		t.Errorf("Characteristics[0].ValueRaw = %s, want %s", got, wantRaw)
	}
}

func TestReplacer_ApplyString_CaseInsensitive(t *testing.T) {
	r, _ := New(Rules{Rules: []Rule{{Find: "PlayToday", Replace: "[PlayBrand]"}}})
	cases := map[string]string{
		"":                    "",
		"PlayToday":           "[PlayBrand]",
		"PLAYTODAY":           "[PlayBrand]",
		"playtoday":           "[PlayBrand]",
		"xPlayTodayx":         "x[PlayBrand]x",
		"no brand here":       "no brand here",
		"Кроссовки PlayToday": "Кроссовки [PlayBrand]",
	}
	for in, want := range cases {
		if got := r.ApplyString(in); got != want {
			t.Errorf("ApplyString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReplacer_CaseSensitive(t *testing.T) {
	r, _ := New(Rules{Rules: []Rule{{Find: "PlayToday", Replace: "[PlayBrand]", CaseSensitive: true}}})
	if got := r.ApplyString("playtoday"); got != "playtoday" {
		t.Errorf("case-sensitive must not match 'playtoday', got %q", got)
	}
	if got := r.ApplyString("PlayToday"); got != "[PlayBrand]" {
		t.Errorf("ApplyString(\"PlayToday\") = %q, want [PlayBrand]", got)
	}
}

// TestReplacer_ReplaceLiteralVerbatim guards the ReplaceAllLiteral choice: a
// replacement containing '$'/'\' must be inserted verbatim, not interpreted.
func TestReplacer_ReplaceLiteralVerbatim(t *testing.T) {
	r, _ := New(Rules{Rules: []Rule{{Find: "X", Replace: "$1Y\\Z"}}})
	if got := r.ApplyString("aXb"); got != "a$1Y\\Zb" {
		t.Errorf("replacement not verbatim: got %q, want %q", got, "a$1Y\\Zb")
	}
}

func TestReplacer_Idempotent(t *testing.T) {
	r, _ := New(Rules{Rules: []Rule{{Find: "PlayToday", Replace: "[PlayBrand]"}}})
	cards := []wb.ProductCard{{Brand: "PlayToday", Title: "PlayToday"}}
	r.ApplySlice(cards)
	if cards[0].Brand != "[PlayBrand]" {
		t.Fatalf("first pass Brand = %q", cards[0].Brand)
	}
	r.ApplySlice(cards) // scrubbing already-scrubbed data must be a no-op
	if cards[0].Brand != "[PlayBrand]" {
		t.Errorf("not idempotent: Brand = %q", cards[0].Brand)
	}
}

func TestReplacer_EmptyRulesNoOp(t *testing.T) {
	r, _ := New(Rules{}) // no rules compiled
	cards := []wb.ProductCard{{Brand: "PlayToday"}}
	r.ApplySlice(cards) // must not panic or mutate
	if cards[0].Brand != "PlayToday" {
		t.Errorf("empty rules mutated data: %q", cards[0].Brand)
	}
	r.ApplySlice(nil) // nil safety
}

func TestReplacer_RawMessageValidity(t *testing.T) {
	// Scrubbing a JSON string value must keep the bytes valid JSON (brackets
	// are legal inside a JSON string), so downstream Values() still parses.
	r, _ := New(Rules{Rules: []Rule{{Find: "PlayToday", Replace: "[PlayBrand]"}}})
	c := []wb.CardCharacteristic{{ValueRaw: json.RawMessage(`"PlayToday"`)}}
	r.ApplySlice(c)
	var s string
	if err := json.Unmarshal(c[0].ValueRaw, &s); err != nil {
		t.Fatalf("scrubbed ValueRaw is not valid JSON: %v (bytes=%s)", err, c[0].ValueRaw)
	}
	if s != "[PlayBrand]" {
		t.Errorf("parsed value = %q, want [PlayBrand]", s)
	}
}
