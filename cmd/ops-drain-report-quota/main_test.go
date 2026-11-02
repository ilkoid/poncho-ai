package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func TestIsDailyLimit(t *testing.T) {
	daily := errors.New("max retries exceeded, last error: 429 rate limited: The daily response limit has been exceeded. The maximum number is 20.")
	if !isDailyLimit(daily) {
		t.Error("daily-limit 429 must be detected (case-insensitive substring)")
	}
	minute := errors.New("max retries exceeded, last error: 429 rate limited: limited by c122a060-a7fb-4bb4-abb0-32fd4e18d489")
	if isDailyLimit(minute) {
		t.Error("minute-level 429 must NOT be classified as daily limit")
	}
	other := errors.New("wb api error: status 500, body: oops")
	if isDailyLimit(other) {
		t.Error("non-429 error must not be classified as daily limit")
	}
	if isDailyLimit(nil) {
		t.Error("nil error must not be classified as daily limit")
	}
}

func TestComputeTarget(t *testing.T) {
	cases := []struct {
		name                       string
		already, limit, reserve, max int
		want                       int
	}{
		{"обычная ночь: наши 3 заняты", 3, 20, 0, 19, 17},
		{"квота пуста полностью", 0, 20, 0, 19, 19},
		{"квота уже исчерпана", 20, 20, 0, 19, 0},
		{"перебор счётчика", 25, 20, 0, 19, 0},
		{"резерв съедает цель", 18, 20, 2, 19, 0},
		{"резерв частично", 15, 20, 2, 19, 3},
		{"max_fillers режет", 1, 20, 0, 5, 5},
	}
	for _, c := range cases {
		if got := computeTarget(c.already, c.limit, c.reserve, c.max); got != c.want {
			t.Errorf("%s: computeTarget(%d,%d,%d,%d) = %d, want %d",
				c.name, c.already, c.limit, c.reserve, c.max, got, c.want)
		}
	}
}

func TestFilterToday(t *testing.T) {
	// Фиксированная зона МСК: тест не зависит от таймзоны машины.
	msk := time.FixedZone("MSK", 3*3600)
	now := time.Date(2026, 11, 3, 0, 50, 0, 0, msk) // 00:50 МСК 03.11
	midnight := localMidnight(now)                  // 00:00 МСК = 21:00 UTC 02.11

	items := []wb.StockHistoryReportItem{
		// наш отчёт 00:05 МСК (21:05 UTC 02.11) — в окне
		{ID: "ours", CreatedAt: "2026-11-02 21:05:33"},
		// пакет нарушителя 01:20 МСК предыдущих суток (22:20 UTC 01.11) — вне окна
		{ID: "old", CreatedAt: "2026-11-01 22:20:00"},
		// мусор/нечитаемый createdAt — пропуск
		{ID: "junk", CreatedAt: ""},
		{ID: "bad", CreatedAt: "not-a-time"},
	}
	got := filterToday(items, midnight, now)
	if len(got) != 1 || got[0].ID != "ours" {
		t.Errorf("filterToday = %+v, want only [ours]", got)
	}
}

func TestFindMarkers(t *testing.T) {
	cfg := &Config{}
	cfg.applyDefaults()
	today := "2026-11-03"

	items := []wb.StockHistoryReportItem{
		// все три наших маркера; у metrics WB эхает endDate днём раньше
		// (наблюдение 02.11) — потому свежесть по дате в имени, не по endDate
		{Name: "Funnel detail 2026-10-04..2026-11-03", EndDate: today, Status: wb.StockHistoryStatusSuccess},
		{Name: "Stock History daily 2026-11-01-2026-11-03", EndDate: today},
		{Name: "Stock History metrics 2026-11-01-2026-11-03", EndDate: "2026-11-02"},
		// вчерашние наши — не маркеры (в имени нет сегодняшней даты)
		{Name: "Funnel detail 2026-10-03..2026-11-02", EndDate: "2026-11-02"},
		{Name: "Stock History daily 2026-10-31-2026-11-02", EndDate: "2026-11-02"},
		// чужие имена
		{Name: "Inventory report 2026-11-03", EndDate: today},
		{Name: "Quota drain 2026-11-02 #1", EndDate: "2026-11-02"},
	}
	m := findMarkers(items, cfg, today)
	if !m.complete() {
		t.Errorf("markers = %v, want complete", m)
	}

	// легаси-имя воронки (код до adopt): авто-имя WB — нижний регистр
	legacy := findMarkers([]wb.StockHistoryReportItem{
		{Name: "detail_history_report", EndDate: today},
		{Name: "Stock History daily 2026-11-01-2026-11-03", EndDate: today},
		{Name: "Stock History metrics 2026-11-01-2026-11-03", EndDate: today},
	}, cfg, today)
	if !legacy.complete() {
		t.Errorf("legacy markers = %v, want complete", legacy)
	}

	// воронки нет — guard должен ждать
	noFunnel := findMarkers([]wb.StockHistoryReportItem{
		{Name: "Stock History daily 2026-11-01-2026-11-03", EndDate: today},
	}, cfg, today)
	if noFunnel.funnel || noFunnel.complete() {
		t.Errorf("markers = %v, want funnel missing", noFunnel)
	}
}

func TestParseDeadline(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	now := time.Date(2026, 11, 3, 0, 10, 0, 0, msk)
	dl, err := parseDeadline("01:05", now)
	if err != nil {
		t.Fatalf("parseDeadline: %v", err)
	}
	want := time.Date(2026, 11, 3, 1, 5, 0, 0, msk)
	if !dl.Equal(want) {
		t.Errorf("deadline = %v, want %v", dl, want)
	}
	if _, err := parseDeadline("25:99", now); err == nil {
		t.Error("bad HH:MM must be rejected")
	}
	if _, err := parseDeadline("0105", now); err == nil {
		t.Error("missing colon must be rejected")
	}
}

func TestDecideAtDeadline(t *testing.T) {
	// нет воронки — всегда ABORT
	drain, _ := decideAtDeadline(markers{}, ".nonexistent-lock")
	if drain {
		t.Error("missing funnel marker must abort (protect other machine's fallback)")
	}
	// все маркеры — дренаж
	drain, _ = decideAtDeadline(markers{funnel: true, stockDaily: true, stockMetrics: true}, ".nonexistent-lock")
	if !drain {
		t.Error("complete markers must drain")
	}
	// стока нет, лок жив (блок ещё идёт) — ABORT
	tmp := t.TempDir()
	lock := tmp + "/.download-all.lock"
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	drain, reason := decideAtDeadline(markers{funnel: true}, lock)
	if drain || !strings.Contains(reason, "лок") {
		t.Errorf("live lock must abort, got drain=%v reason=%q", drain, reason)
	}
	// стока нет, лок снят (блок завершился) — DRAIN ANYWAY
	drain, reason = decideAtDeadline(markers{funnel: true}, tmp+"/.no-such-lock")
	if !drain {
		t.Errorf("released lock must drain anyway, reason=%q", reason)
	}
}
