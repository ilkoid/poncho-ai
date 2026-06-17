package main

import (
	"fmt"
)

// mockUpdates returns a deterministic synthetic plan. --mock mode NEVER opens a
// database connection (the pool is not created in the mock branch), so this data
// only proves the output/pipeline wiring end-to-end. It mirrors the real
// wb_data_prod shape: brand lives in cards.brand / onec_goods.brand, plus
// free-text columns. Counts are fixed — no randomness, no clock.
func mockUpdates() []Update {
	return []Update{
		{
			Target:  Target{Table: "cards", Column: "brand", Type: "text"},
			Matches: 32044,
			Samples: []Sample{{Before: "PlayToday", After: "[REDACTED]"}},
		},
		{
			Target:  Target{Table: "cards", Column: "title", Type: "text"},
			Matches: 128,
			Samples: []Sample{{Before: "Кроссовки PlayToday детские", After: "Кроссовки [REDACTED] детские"}},
		},
		{
			Target:  Target{Table: "products", Column: "brand_name", Type: "text"},
			Matches: 21781,
			Samples: []Sample{{Before: "PlayToday", After: "[REDACTED]"}},
		},
		{
			Target:  Target{Table: "onec_goods", Column: "brand", Type: "text"},
			Matches: 27366,
			Samples: []Sample{{Before: "PlayToday", After: "[REDACTED]"}},
		},
		{
			Target:  Target{Table: "feedbacks", Column: "text", Type: "text"},
			Matches: 307,
			Samples: []Sample{{Before: "Купил PlayToday, доволен", After: "Купил [REDACTED], доволен"}},
		},
	}
}

// printMock renders the synthetic plan with an unmistakable [MOCK] prefix.
func printMock(cfg *Config, selectTables []string) {
	updates := mockUpdates()
	printBanner("MOCK (no DB connection)", cfg.Schema, cfg.Read, cfg.Write)

	fmt.Fprintln(out, "[MOCK] no database connection was opened — synthetic plan only.")
	fmt.Fprintf(out, "[MOCK] table filter that WOULD apply: %s\n",
		restrictDesc(cfg.IncludeTables, selectTables, cfg.ExcludeTables))
	fmt.Fprintf(out, "[MOCK] discovered columns (synthetic): %d\n\n", len(updates))

	var total int
	for i, u := range updates {
		total += u.Matches
		fmt.Fprintf(out, "  [%d/%d] %s.%s (%s) → %d rows\n",
			i+1, len(updates), u.Target.Table, u.Target.Column, u.Target.Type, u.Matches)
		fmt.Fprintf(out, "        regexp_replace(%s, %q → %q, 'gi')\n", u.Target.Column, cfg.Read, cfg.Write)
		for _, sm := range u.Samples {
			fmt.Fprintf(out, "        sample:  %q  →  %q\n", sm.Before, sm.After)
		}
	}
	fmt.Fprintf(out, "\n[MOCK] total (synthetic): %d rows across %d columns.\n", total, len(updates))
	fmt.Fprintln(out, "[MOCK] no rows were modified (offline).")
}
