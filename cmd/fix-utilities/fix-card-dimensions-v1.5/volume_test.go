package main

import (
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
)

func dimRow(oldL, oldW, oldH, newL, newW, newH float64) sqlite.DimensionAggRow {
	return sqlite.DimensionAggRow{
		OldLength: oldL, OldWidth: oldW, OldHeight: oldH,
		NewLength: newL, NewWidth: newW, NewHeight: newH,
	}
}

func TestApplyVolumeFilter_AnyReturnsAll(t *testing.T) {
	rows := []sqlite.DimensionAggRow{
		dimRow(10, 10, 10, 5, 5, 5),
		dimRow(5, 5, 5, 10, 10, 10),
	}
	got := applyVolumeFilter(rows, VolumeConfig{Direction: "any"})
	if len(got) != len(rows) {
		t.Fatalf("any: ожидалось %d строк, получено %d", len(rows), len(got))
	}
}

func TestApplyVolumeFilter_Under(t *testing.T) {
	cfg := VolumeConfig{Direction: "under", MarginPct: 10, MinDeltaCm3: 1200}
	cases := []struct {
		name string
		row  sqlite.DimensionAggRow
		keep bool
	}{
		{"явно недосказанная (2x объём)", dimRow(10, 10, 20, 40, 10, 10), true},   // 2000 vs 4000: Δ2000 > 1200
		{"недосказанная ровно на границе маржи", dimRow(10, 10, 10, 11, 10, 10), false}, // 1000 vs 1100: не > 1100
		{"маржа ок, но дельта ниже min_delta", dimRow(10, 10, 20, 11, 10, 21), false}, // 2000 vs 2310: 2310>2200 ок, Δ310 < 1200
		{"маржа ок и дельта ок", dimRow(10, 10, 20, 16, 10, 21), true},            // 2000 vs 3360: 3360>2200 ок, Δ1360 > 1200
		{"пересказанная", dimRow(20, 10, 10, 10, 10, 10), false},
		{"нулевая WB, замер проходит пороги", dimRow(0, 0, 0, 12, 11, 10), true},  // 0 vs 1320: Δ1320 > 1200
		{"нулевая WB, замер мал", dimRow(0, 0, 0, 10, 10, 10), false},             // Δ1000 < 1200
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := applyVolumeFilter([]sqlite.DimensionAggRow{tc.row}, cfg)
			if (len(got) == 1) != tc.keep {
				t.Fatalf("keep=%v: получено %d строк", tc.keep, len(got))
			}
		})
	}
}

func TestApplyVolumeFilter_Over(t *testing.T) {
	cfg := VolumeConfig{Direction: "over", MarginPct: 10, MinDeltaCm3: 1200}
	cases := []struct {
		name string
		row  sqlite.DimensionAggRow
		keep bool
	}{
		{"явно пересказанная", dimRow(40, 10, 10, 10, 10, 20), true}, // 4000 vs 2000: Δ2000 > 1200
		{"недосказанная в over-режиме", dimRow(10, 10, 10, 20, 10, 10), false},
		{"пересказанная ниже маржи", dimRow(11, 10, 10, 10, 10, 10), false}, // 1100 vs 1000: не > 1100
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := applyVolumeFilter([]sqlite.DimensionAggRow{tc.row}, cfg)
			if (len(got) == 1) != tc.keep {
				t.Fatalf("keep=%v: получено %d строк", tc.keep, len(got))
			}
		})
	}
}

func TestEffectiveDims(t *testing.T) {
	cases := []struct {
		name             string
		l, w, h, padding float64
		wantL, wantW, wantH float64
	}{
		{"дробный замер +2 см", 46.4, 40.2, 11.2, 2, 49, 43, 14},
		{"целый замер +2 см", 46, 40, 11, 2, 48, 42, 13},
		{"дробный замер без padding (прежний ceil)", 46.4, 40.0, 11.0, 0, 47, 40, 11},
		{"дробный padding", 46.4, 46.4, 46.4, 1.5, 48, 48, 48},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotL, gotW, gotH := effectiveDims(tc.l, tc.w, tc.h, tc.padding)
			if gotL != tc.wantL || gotW != tc.wantW || gotH != tc.wantH {
				t.Fatalf("effectiveDims(%v,%v,%v,+%v) = %v,%v,%v; ожидалось %v,%v,%v",
					tc.l, tc.w, tc.h, tc.padding, gotL, gotW, gotH, tc.wantL, tc.wantW, tc.wantH)
			}
		})
	}
}

func TestVolumeConfigNormalized(t *testing.T) {
	if v, err := (VolumeConfig{}).normalized(); err != nil || v.Direction != "any" {
		t.Fatalf("пустая секция: ожидался any без ошибки, получено %q err=%v", v.Direction, err)
	}
	if v, err := (VolumeConfig{Direction: " UNDER "}).normalized(); err != nil || v.Direction != "under" {
		t.Fatalf("normalization: ожидался under, получено %q err=%v", v.Direction, err)
	}
	if _, err := (VolumeConfig{Direction: "side"}).normalized(); err == nil {
		t.Fatal("невалидное direction должно давать ошибку")
	}
	if _, err := (VolumeConfig{PaddingCm: -1}).normalized(); err == nil {
		t.Fatal("отрицательный padding должен давать ошибку")
	}
	// padding не влияет на отбор: замер должен сравниваться с WB без padding
	cfg, _ := VolumeConfig{Direction: "under", MarginPct: 10, MinDeltaCm3: 0, PaddingCm: 50}.normalized()
	rows := applyVolumeFilter([]sqlite.DimensionAggRow{dimRow(10, 10, 10, 10.5, 10, 10)}, cfg)
	if len(rows) != 0 {
		t.Fatal("padding не должен расширять отбор under")
	}
}
