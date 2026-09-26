package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testCube() *SuppliesData {
	return &SuppliesData{
		Meta: Meta{
			GeneratedAt: "26.09.2026 21:00", Db: "wb_data_test",
			From: "2026-09-24", To: "2026-09-25",
			Supplies: 2, Tasks: 12, Open: 1,
		},
		Sups: Sups{
			ID:         []string{"WB-GI-111", "WB-GI-222"},
			Day:        []string{"2026-09-24", "2026-09-25"},
			Name:       []string{"25.09 — 1", "26.09 — 1"},
			Created:    []string{"2026-09-25 10:12", "2026-09-26 09:40"},
			First:      []string{"2026-09-24 16:03", "2026-09-25 18:55"},
			Closed:     []string{"2026-09-25 10:40", ""},
			Scanned:    []string{"2026-09-25 18:20", ""},
			Rejected:   []int{0, 0},
			LagH:       []float64{26.3, -1},
			NTasks:     []int{8, 4},
			CancelPre:  []int{0, 0},
			CancelPost: []int{1, 0},
			Hist:       [][8]int{{3, 2, 1, 1, 1, 0, 0, 0}, {0, 0, 0, 0, 0, 0, 0, 0}},
		},
	}
}

// exportHTML: все токены заменены, payload валиден и содержит данные.
func TestExportHTML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dash.html")
	size, err := exportHTML(testCube(), path)
	if err != nil {
		t.Fatalf("exportHTML: %v", err)
	}
	if size <= 0 {
		t.Fatalf("размер файла %d", size)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(b)
	for _, tok := range []string{tokPayload, tokApp, tokStyle, tokGlossary, tokMethod, tokEcharts} {
		if strings.Contains(s, tok) {
			t.Errorf("токен %s не заменён", tok)
		}
	}
	m := strings.Contains(s, `<script id="payload" type="application/json">`)
	if !m {
		t.Fatalf("нет payload-скрипта")
	}
	start := strings.Index(s, `<script id="payload" type="application/json">`) + len(`<script id="payload" type="application/json">`)
	end := strings.Index(s[start:], `</script>`)
	var cube SuppliesData
	if err := json.Unmarshal([]byte(s[start:start+end]), &cube); err != nil {
		t.Fatalf("payload не парсится: %v", err)
	}
	if len(cube.Sups.ID) != 2 || cube.Sups.ID[0] != "WB-GI-111" {
		t.Errorf("payload потерял данные: %+v", cube.Sups.ID)
	}
	if !strings.Contains(s, "Глоссарий") || !strings.Contains(s, "scan_dt") {
		t.Errorf("в HTML нет глоссария/методики")
	}
}
