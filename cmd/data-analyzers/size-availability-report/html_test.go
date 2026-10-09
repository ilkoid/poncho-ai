package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testCube() *AvailabilityData {
	return &AvailabilityData{
		Meta: Meta{
			GeneratedAt: "08.10.2026 23:00", Db: "wb_data_test",
			SnapshotDate: "2026-10-08", StocksDate: "2026-10-08", RestsDate: "2026-10-08",
			MinUnits: 2, MinSizes: 4, OnecStorage: "Внуково",
			Prev: &PrevKpi{Date: "2026-10-07", Unavailable: 9, OnecFree: 1000, NoCard: 2, ZeroSizes: 3},
		},
		Arts: ArticleCol{
			Article:    []string{"251000001", "251000002"},
			NmID:       []int64{111, 0},
			Brand:      []string{"Play Today", "Play Today"},
			Cat1:       []string{"Обувь", "Аксессуары"},
			Cat2:       []string{"Кроссовки", ""},
			Season:     []string{"ЛЕТО", "ЗИМА"},
			Year:       []int{2025, 2025},
			SizesTotal: []int{6, 0},
			SizesPres:  []int{1, 0},
			WbQty:      []int64{7, 0},
			OnecFree:   []int64{120, 40},
			OnecReserv: []int64{10, 0},
			HasCard:    []bool{true, false},
		},
		Sizes: SizeCol{
			NmID: []int64{111, 111, 111},
			Size: []string{"36", "37", "38"},
			Qty:  []int64{5, 2, 0},
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
	const open = `<script id="payload" type="application/json">`
	start := strings.Index(s, open)
	if start < 0 {
		t.Fatalf("нет payload-скрипта")
	}
	start += len(open)
	end := strings.Index(s[start:], `</script>`)
	var cube AvailabilityData
	if err := json.Unmarshal([]byte(s[start:start+end]), &cube); err != nil {
		t.Fatalf("payload не парсится: %v", err)
	}
	if len(cube.Arts.Article) != 2 || cube.Arts.Article[0] != "251000001" {
		t.Errorf("payload потерял данные: %+v", cube.Arts.Article)
	}
	if cube.Meta.OnecStorage != "Внуково" || cube.Meta.Prev == nil || cube.Meta.Prev.Unavailable != 9 {
		t.Errorf("payload потерял meta/prev: %+v", cube.Meta)
	}
	if len(cube.Sizes.Qty) != 3 || cube.Sizes.Qty[0] != 5 {
		t.Errorf("payload потерял размеры: %+v", cube.Sizes)
	}
	if !strings.Contains(s, "Глоссарий") || !strings.Contains(s, "Внуково") {
		t.Errorf("в HTML нет глоссария/методики")
	}
	// «</» внутри payload экранирован как «<\/» — иначе преждевременный </script>
	if strings.Contains(s[start:start+end], "</") {
		t.Errorf("payload содержит неэкранированный </ внутри script")
	}
}
