package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func TestConfigValidation(t *testing.T) {
	t.Run("missing_rules", func(t *testing.T) {
		cfg := Config{}
		cfg.applyDefaults()
		if err := cfg.validate(); err == nil {
			t.Error("expected error for empty fix_rules")
		}
	})

	t.Run("protected_in_rules", func(t *testing.T) {
		cfg := Config{
			FixRules: []FixRule{{CharID: 15000001}},
			ProtectedCharIDs: []int{15000001},
		}
		cfg.applyDefaults()
		if err := cfg.validate(); err == nil {
			t.Error("expected error for protected char_id in fix_rules")
		}
	})

	t.Run("valid_value_types", func(t *testing.T) {
		for _, vt := range []string{"string", "number", "boolean"} {
			cfg := Config{
				FixRules: []FixRule{{CharID: 123, ValueType: vt}},
			}
			cfg.applyDefaults()
			if err := cfg.validate(); err != nil {
				t.Errorf("value_type %s should be valid: %v", vt, err)
			}
		}
	})

	t.Run("invalid_value_type", func(t *testing.T) {
		cfg := Config{
			FixRules: []FixRule{{CharID: 123, ValueType: "invalid"}},
		}
		cfg.applyDefaults()
		if err := cfg.validate(); err == nil {
			t.Error("expected error for invalid value_type")
		}
	})

	t.Run("defaults_applied", func(t *testing.T) {
		cfg := Config{
			FixRules: []FixRule{{CharID: 123}},
		}
		cfg.applyDefaults()
		if cfg.DBPath != "/var/db/wb-sales.db" {
			t.Errorf("default db_path: got %s", cfg.DBPath)
		}
		if cfg.FixRules[0].ValueType != "string" {
			t.Errorf("default value_type: got %s", cfg.FixRules[0].ValueType)
		}
		if cfg.WBUpdate.BatchSize != 30 {
			t.Errorf("default batch_size: got %d", cfg.WBUpdate.BatchSize)
		}
		if cfg.WBUpdate.IntervalSeconds != 8 {
			t.Errorf("default interval_seconds: got %d", cfg.WBUpdate.IntervalSeconds)
		}
	})
}

func TestMatchesRule(t *testing.T) {
	t.Run("empty_search_matches_missing", func(t *testing.T) {
		rule := FixRule{CharID: 1, SearchValue: "", ValueType: "string"}
		if !matchesRule(rule, "", false) {
			t.Error("empty search should match missing char")
		}
	})

	t.Run("empty_search_matches_null", func(t *testing.T) {
		rule := FixRule{CharID: 1, SearchValue: "", ValueType: "string"}
		if !matchesRule(rule, "null", true) {
			t.Error("empty search should match 'null'")
		}
	})

	t.Run("empty_search_matches_empty_array", func(t *testing.T) {
		rule := FixRule{CharID: 1, SearchValue: "", ValueType: "string"}
		if !matchesRule(rule, "[]", true) {
			t.Error("empty search should match '[]'")
		}
	})

	t.Run("empty_search_no_match_nonempty", func(t *testing.T) {
		rule := FixRule{CharID: 1, SearchValue: "", ValueType: "string"}
		if matchesRule(rule, `["text"]`, true) {
			t.Error("empty search should not match non-empty value")
		}
	})

	t.Run("string_match_case_insensitive", func(t *testing.T) {
		rule := FixRule{CharID: 1, SearchValue: "Текстиль", ValueType: "string"}
		if !matchesRule(rule, `["текстиль"]`, true) {
			t.Error("string match should be case-insensitive")
		}
	})

	t.Run("number_match", func(t *testing.T) {
		rule := FixRule{CharID: 1, SearchValue: "42", ValueType: "number"}
		if !matchesRule(rule, `[42]`, true) {
			t.Error("number 42 should match [42]")
		}
		if !matchesRule(rule, `[42.0]`, true) {
			t.Error("number 42 should match [42.0]")
		}
		if matchesRule(rule, `[41]`, true) {
			t.Error("number 42 should not match [41]")
		}
	})

	t.Run("boolean_match", func(t *testing.T) {
		rule := FixRule{CharID: 1, SearchValue: "true", ValueType: "boolean"}
		if !matchesRule(rule, `[true]`, true) {
			t.Error("boolean true should match [true]")
		}
		if matchesRule(rule, `[false]`, true) {
			t.Error("boolean true should not match [false]")
		}
	})

	t.Run("nonempty_search_no_match_missing", func(t *testing.T) {
		rule := FixRule{CharID: 1, SearchValue: "text", ValueType: "string"}
		if matchesRule(rule, "", false) {
			t.Error("non-empty search should not match missing char")
		}
	})
}

func TestUnwrapValue(t *testing.T) {
	t.Run("integer_from_array", func(t *testing.T) {
		val := []any{float64(42)}
		got := unwrapValue(val)
		if got != 42 {
			t.Errorf("unwrapValue([42]) = %v, want 42", got)
		}
	})

	t.Run("float_from_array", func(t *testing.T) {
		val := []any{float64(2.5)}
		got := unwrapValue(val)
		if got != 2.5 {
			t.Errorf("unwrapValue([2.5]) = %v, want 2.5", got)
		}
	})

	t.Run("string_from_array", func(t *testing.T) {
		val := []any{"text"}
		got := unwrapValue(val)
		if got != "text" {
			t.Errorf("unwrapValue([\"text\"]) = %v, want 'text'", got)
		}
	})

	t.Run("multi_element_array_unchanged", func(t *testing.T) {
		val := []any{"a", "b"}
		got := unwrapValue(val)
		if got == nil {
			t.Error("unwrapValue([\"a\", \"b\"]) should not return nil")
		}
	})

	t.Run("non_array_unchanged", func(t *testing.T) {
		val := "plain string"
		got := unwrapValue(val)
		if got != "plain string" {
			t.Errorf("unwrapValue('plain string') = %v, want 'plain string'", got)
		}
	})
}

func TestConvertCharValue(t *testing.T) {
	t.Run("to_integer", func(t *testing.T) {
		current := `[42]`
		got := convertCharValue("123", current)
		if got != 123 {
			t.Errorf("convertCharValue('123', %s) = %v, want 123", current, got)
		}
	})

	t.Run("to_float", func(t *testing.T) {
		current := `[2.5]`
		got := convertCharValue("3.14", current)
		if got != 3.14 {
			t.Errorf("convertCharValue('3.14', %s) = %v, want 3.14", current, got)
		}
	})

	t.Run("to_string_array", func(t *testing.T) {
		current := `["a","b"]`
		got := convertCharValue("x, y", current)
		arr, ok := got.([]string)
		if !ok || len(arr) != 2 || arr[0] != "x" || arr[1] != "y" {
			t.Errorf("convertCharValue('x, y', %s) = %v, want [\"x\", \"y\"]", current, got)
		}
	})

	t.Run("invalid_current_returns_array", func(t *testing.T) {
		current := `invalid`
		got := convertCharValue("test", current)
		arr, ok := got.([]string)
		if !ok || len(arr) != 1 || arr[0] != "test" {
			t.Errorf("convertCharValue('test', 'invalid') = %v, want [\"test\"]", got)
		}
	})
}

func TestStringToCharArray(t *testing.T) {
	t.Run("single_value", func(t *testing.T) {
		got := stringToCharArray("текст")
		if len(got) != 1 || got[0] != "текст" {
			t.Errorf("stringToCharArray('текст') = %v, want [\"текст\"]", got)
		}
	})

	t.Run("comma_separated", func(t *testing.T) {
		got := stringToCharArray("a, b, c")
		if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
			t.Errorf("stringToCharArray('a, b, c') = %v, want [\"a\",\"b\",\"c\"]", got)
		}
	})

	t.Run("trims_spaces", func(t *testing.T) {
		got := stringToCharArray("  x  ,  y  ")
		if len(got) != 2 || got[0] != "x" || got[1] != "y" {
			t.Errorf("stringToCharArray('  x  ,  y  ') = %v, want [\"x\",\"y\"]", got)
		}
	})

	t.Run("empty_splits_to_original", func(t *testing.T) {
		got := stringToCharArray("")
		if len(got) != 1 || got[0] != "" {
			t.Errorf("stringToCharArray('') = %v, want [\"\"]", got)
		}
	})
}

func TestUnwrapJSONString(t *testing.T) {
	cases := []struct{ in, want string }{
		{`["text"]`, "text"},
		{`text`, "text"},
		{`[42]`, "42"},
		{`[true]`, "true"},
		{``, ""},
		{`[]`, ""},
		{`["a","b"]`, `["a","b"]`},
	}
	for _, c := range cases {
		got := unwrapJSONString(c.in)
		if got != c.want {
			t.Errorf("unwrapJSONString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUnwrapJSONNumber(t *testing.T) {
	cases := []struct{ in string; want float64; ok bool }{
		{`[42]`, 42, true},
		{`[3.14]`, 3.14, true},
		{`42`, 42, true},
		{`3.14`, 3.14, true},
		{`text`, 0, false},
		{`["a"]`, 0, false},
	}
	for _, c := range cases {
		got, err := unwrapJSONNumber(c.in)
		if (err == nil) != c.ok {
			t.Errorf("unwrapJSONNumber(%q) ok = %v, want %v (err=%v)", c.in, err == nil, c.ok, err)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("unwrapJSONNumber(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestFilterNmIDsByYear(t *testing.T) {
	// Year is extracted from positions 2-3 of vendor_code (SUBSTR(vc, 2, 2))
	entries := []config.YearEntry{
		{NmID: 1, VendorCode: "12621749"}, // year 26
		{NmID: 2, VendorCode: "23621749"}, // year 36
		{NmID: 3, VendorCode: "24621749"}, // year 46
		{NmID: 4, VendorCode: "abc"},      // too short, skipped
	}
	t.Run("filter_36_46", func(t *testing.T) {
		got := FilterNmIDsByYear(entries, []int{36, 46})
		// abc is too short and gets skipped by the logic
		if len(got) != 2 {
			t.Errorf("got %d entries, want 2", len(got))
		}
	})
	t.Run("filter_26_only", func(t *testing.T) {
		got := FilterNmIDsByYear(entries, []int{26})
		if len(got) != 1 {
			t.Errorf("got %d entries, want 1", len(got))
		}
	})
	t.Run("no_match", func(t *testing.T) {
		got := FilterNmIDsByYear(entries, []int{99})
		if len(got) != 0 {
			t.Errorf("got %d entries, want 0", len(got))
		}
	})
	t.Run("no_filter_returns_all", func(t *testing.T) {
		got := FilterNmIDsByYear(entries, nil)
		if len(got) != 4 {
			t.Errorf("got %d entries, want 4", len(got))
		}
	})
}

func TestSmartMergeLogic(t *testing.T) {
	// This is an integration test of the smart merge algorithm using the helper functions.
	t.Run("preserves_unprotected_unchanged_chars", func(t *testing.T) {
		allChars := []CardChar{
			{CharID: 1, Name: "A", Value: `["old"]`},
			{CharID: 2, Name: "B", Value: `[42]`},
			{CharID: 3, Name: "C", Value: `[true]`},
		}
		_ = []changeEntry{{CharID: 1, Old: `["old"]`, New: "new"}}
		protected := map[int]bool{999: true}

		changesMap := map[int]string{1: "new"}
		seenIDs := make(map[int]bool)
		var final []map[string]any

		for _, curr := range allChars {
			seenIDs[curr.CharID] = true
			var val any
			json.Unmarshal([]byte(curr.Value), &val)
			val = unwrapValue(val)

			if protected[curr.CharID] {
				final = append(final, map[string]any{"id": curr.CharID, "val": val})
			} else if newVal, exists := changesMap[curr.CharID]; exists {
				converted := convertCharValue(newVal, curr.Value)
				final = append(final, map[string]any{"id": curr.CharID, "val": converted})
			} else {
				final = append(final, map[string]any{"id": curr.CharID, "val": val})
			}
		}

		// CharID 1: string array -> string array conversion
		arr1, ok1 := final[0]["val"].([]string)
		if !ok1 || len(arr1) != 1 || arr1[0] != "new" {
			t.Errorf("char 1 should be ['new'], got %v (type %T)", final[0]["val"], final[0]["val"])
		}
		// CharID 2: number preserved
		if final[1]["val"] != 42 {
			t.Errorf("char 2 should be 42, got %v", final[1]["val"])
		}
		// CharID 3: bool preserved (not converted to array since current is not string)
		if final[2]["val"] != true {
			t.Errorf("char 3 should be true, got %v", final[2]["val"])
		}
	})

	t.Run("protected_char_ignored", func(t *testing.T) {
		allChars := []CardChar{
			{CharID: 1, Name: "Protected", Value: `["keep"]`},
		}
		_ = []changeEntry{{CharID: 1, Old: `["keep"]`, New: "change"}}
		protected := map[int]bool{1: true}

		_ = map[int]string{1: "change"}
		seenIDs := make(map[int]bool)
		var final any

		for _, curr := range allChars {
			seenIDs[curr.CharID] = true
			var val any
			json.Unmarshal([]byte(curr.Value), &val)
			val = unwrapValue(val)

			if protected[curr.CharID] {
				final = val // Should preserve original (unwrapped string)
			}
		}

		if final != "keep" {
			t.Errorf("protected char should keep original value, got %v", final)
		}
	})
}

// TestKizMarkedPayloadSerialization guards the 3-value logic of wb.CardUpdateItem.KizMarked:
// loadCardFields must carry an explicit *bool so an existing marking state survives the
// full-card rewrite, while NULL must stay omitted (WB then applies default false).
func TestKizMarkedPayloadSerialization(t *testing.T) {
	base := wb.CardUpdateItem{NmID: 1, VendorCode: "VC", Brand: "B", Title: "T"}

	t.Run("explicit_true_emitted", func(t *testing.T) {
		v := true
		item := base
		item.KizMarked = &v
		b, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(b, []byte(`"kizMarked":true`)) {
			t.Errorf("expected kizMarked:true in payload, got: %s", b)
		}
	})

	t.Run("explicit_false_emitted", func(t *testing.T) {
		v := false
		item := base
		item.KizMarked = &v
		b, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(b, []byte(`"kizMarked":false`)) {
			t.Errorf("expected kizMarked:false in payload, got: %s", b)
		}
	})

	t.Run("nil_omitted", func(t *testing.T) {
		item := base // KizMarked == nil
		b, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte(`kizMarked`)) {
			t.Errorf("expected kizMarked omitted when nil (cards.kiz_marked IS NULL), got: %s", b)
		}
	})
}
