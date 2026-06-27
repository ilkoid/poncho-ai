package main

import (
	"strings"
	"testing"
)

// TestClassifyFix covers the direction-aware staging decision with the real-world
// shapes seen in wb_data_prod (verified by the SQL classification in the plan):
// over-declared cards must be skipped (no penalty risk, never shrink), only genuine
// under-declarations become pending. No DB, no WB — pure function over 6 floats.
func TestClassifyFix(t *testing.T) {
	cases := []struct {
		name             string
		oldL, oldW, oldH float64
		newL, newW, newH float64
		wantStatus       string
		wantReason       string // substring of the returned reason
	}{
		{
			name: "14293833 Шапка — card 30×31×7 ≥ meas 29×30×6 (over-declared, the bug case)",
			oldL: 30, oldW: 31, oldH: 7,
			newL: 29, newW: 30, newH: 6,
			wantStatus: "skipped",
			wantReason: "over-declared",
		},
		{
			name: "740051798 Сумки — card 35×30×12 < meas 43×42×12 (real under-declaration, real fix)",
			oldL: 35, oldW: 30, oldH: 12,
			newL: 43, newW: 42, newH: 12,
			wantStatus: "pending",
			wantReason: "under-declared",
		},
		{
			name: "212887194 Бейсболка — under on height only (28×28×8 → 28×28×10)",
			oldL: 28, oldW: 28, oldH: 8,
			newL: 28, newW: 28, newH: 10,
			wantStatus: "pending",
			wantReason: "under-declared",
		},
		{
			name: "exact match — already fixed, idempotent skip",
			oldL: 30, oldW: 31, oldH: 7,
			newL: 30, newW: 31, newH: 7,
			wantStatus: "skipped",
			wantReason: "== measurement",
		},
		{
			name: "swapped axes, equal volume (30×28×5 vs 28×30×5) — skip via volume equality",
			oldL: 30, oldW: 28, oldH: 5,
			newL: 28, newW: 30, newH: 5,
			wantStatus: "skipped",
			wantReason: "over-declared",
		},
		{
			name: "one dim smaller but card volume greater — skip (no risk)",
			oldL: 50, oldW: 50, oldH: 20, // 50000
			newL: 48, newW: 52, newH: 15, // 37440
			wantStatus: "skipped",
			wantReason: "over-declared",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, reason := classifyFix(tc.oldL, tc.oldW, tc.oldH, tc.newL, tc.newW, tc.newH)
			if status != tc.wantStatus {
				t.Errorf("status = %q, want %q", status, tc.wantStatus)
			}
			if !strings.Contains(reason, tc.wantReason) {
				t.Errorf("reason = %q, want substring %q", reason, tc.wantReason)
			}
		})
	}
}
