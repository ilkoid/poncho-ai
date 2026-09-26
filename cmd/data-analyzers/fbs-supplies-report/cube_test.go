package main

import "testing"

// P3-регрессия: период Meta.From/To — по фактическим дням строк куба.
// Раньше assembleCube сеял туда диапазон coverage (весь диапазон таблицы),
// и при --days N шапка HTML показывала весь диапазон вместо окна.
func TestAssembleCubePeriodFromRows(t *testing.T) {
	rows := []supplyRow{
		{ID: "A", Day: "2026-09-24", Name: "s1", Created: "2026-09-24 10:00",
			FirstTask: "2026-09-23 20:00", Closed: "2026-09-24 11:00",
			Scanned: "2026-09-24 18:00", LagH: 22.0, NTasks: 5,
			Hist: [8]int{2, 2, 1, 0, 0, 0, 0, 0}},
		{ID: "B", Day: "2026-09-25", Name: "s2", Created: "2026-09-25 09:00",
			FirstTask: "2026-09-24 13:00", LagH: -1, NTasks: 4},
	}
	cube := assembleCube(rows, "wb_data_test")

	if cube.Meta.From != "2026-09-24" || cube.Meta.To != "2026-09-25" {
		t.Errorf("период из строк куба: got %s..%s, want 2026-09-24..2026-09-25",
			cube.Meta.From, cube.Meta.To)
	}
	if cube.Meta.Supplies != 2 || cube.Meta.Tasks != 9 {
		t.Errorf("Meta: supplies=%d tasks=%d, want 2/9", cube.Meta.Supplies, cube.Meta.Tasks)
	}
	// открытая = нет scan_dt и не отказ; отказ (Rejected=1) не «ждёт»
	if cube.Meta.Open != 1 {
		t.Errorf("Meta.Open=%d, want 1", cube.Meta.Open)
	}
	rows[1].Rejected = 1
	if cube := assembleCube(rows, "db"); cube.Meta.Open != 0 {
		t.Errorf("отказ не должен считаться ждущим: Open=%d", cube.Meta.Open)
	}

	if len(cube.Sups.ID) != 2 || cube.Sups.LagH[1] != -1 || cube.Sups.Hist[1] != ([8]int{}) {
		t.Errorf("колонки payload сломаны: %+v", cube.Sups)
	}
	if d := dayList(cube); len(d) != 2 || d[0] != "2026-09-24" || d[1] != "2026-09-25" {
		t.Errorf("dayList: %v", d)
	}
}

// Пустой куб (окно без поставок) — не паникует, период пустой.
func TestAssembleCubeEmpty(t *testing.T) {
	cube := assembleCube(nil, "db")
	if cube.Meta.From != "" || cube.Meta.To != "" || cube.Meta.Supplies != 0 {
		t.Errorf("пустой куб: %+v", cube.Meta)
	}
}
