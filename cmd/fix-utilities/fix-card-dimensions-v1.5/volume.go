package main

import (
	"math"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
)

// applyVolumeFilter отсеивает строки по направлению расхождения объёмов.
// Сравнение — по сырым замерам 1С (New*) против текущих габаритов карточки (Old*);
// padding на отбор не влияет, он применяется только при записи (effectiveDims).
//
// under: объём 1С больше объёма WB минимум на margin_pct% И абсолютная дельта
// больше min_delta_cm3 (карточка с нулевым объёмом WB тоже считается under —
// пустую карточку надо заполнять, если её замер проходит пороги).
// over: зеркально (WB больше 1С — пересказанные, переплата тарифов).
func applyVolumeFilter(rows []sqlite.DimensionAggRow, cfg VolumeConfig) []sqlite.DimensionAggRow {
	if cfg.Direction == "any" {
		return rows
	}
	out := make([]sqlite.DimensionAggRow, 0, len(rows))
	for _, r := range rows {
		volWB := r.OldLength * r.OldWidth * r.OldHeight
		vol1c := r.NewLength * r.NewWidth * r.NewHeight
		var keep bool
		if cfg.Direction == "under" {
			keep = vol1c > volWB*(1+cfg.MarginPct/100) && vol1c-volWB > cfg.MinDeltaCm3
		} else { // over
			keep = volWB > vol1c*(1+cfg.MarginPct/100) && volWB-vol1c > cfg.MinDeltaCm3
		}
		if keep {
			out = append(out, r)
		}
	}
	return out
}

// effectiveDims возвращает габариты, которые реально запишутся в WB:
// сырой замер 1С + страховочный padding, округление вверх до целых см.
// Вес не затрагивается.
func effectiveDims(l, w, h, paddingCm float64) (float64, float64, float64) {
	return math.Ceil(l + paddingCm), math.Ceil(w + paddingCm), math.Ceil(h + paddingCm)
}

// applyOnlyNewFilter оставляет только карточки с нулевыми размерами WB
// (L=W=H=0). Вес не важен: карточка с нулевыми размерами и заполненным весом
// всё равно требует заполнения габаритов — то же определение NEW, что в --compare.
func applyOnlyNewFilter(rows []sqlite.DimensionAggRow) []sqlite.DimensionAggRow {
	out := make([]sqlite.DimensionAggRow, 0, len(rows))
	for _, r := range rows {
		if r.OldLength == 0 && r.OldWidth == 0 && r.OldHeight == 0 {
			out = append(out, r)
		}
	}
	return out
}
