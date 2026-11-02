// ops-drain-report-quota — ВРЕМЕННАЯ утилита кампании давления по квоте
// созданий async-отчётов WB (POST /api/v2/nm-report/downloads: 20/сутки на
// аккаунт, docs/wb_api_swagger/11-analytics.yaml:163; темп 3 создания/мин, :472-482).
//
// Зачем: внешний потребитель общего ключа ежедневно создаёт пакет 18×
// «Inventory report» в 01:10–01:33 МСК и не отвечает на письма. После того
// как НАШ ночной конвейер занял свои 3 слота (воронка Phase 0 + 2 стока,
// VPS2 00:05–~01:00), утилита закрывает остаток квоты однодневными
// отчётами-наполнителями «Quota drain …» → пакет нарушителя получает 429,
// его потребители сами выходят на переговоры.
//
// Guard «строго после наших утилит»: перед дренажем ждёт (поллинг
// бесплатного GET-списка) наши три маркера за сегодня — «Funnel detail …»
// (или легаси DETAIL_HISTORY_REPORT) + «Stock History daily …» +
// «Stock History metrics …». Дедлайн (конфиг, дефолт 01:05 локального
// времени = МСК на VPS2) — до пакета нарушителя.
//
// Безопасность:
//   - без --apply НИЧЕГО не создаёт — только снимок списка и план (dry-run);
//   - дневной 429 «The daily response limit has been exceeded» — это ЦЕЛЬ:
//     вердикт QUOTA_EXHAUSTED, exit 0;
//   - темп созданий 3/мин (лимит сваггера), идемпотентна: при 20/20 —
//     NOTHING_TO_DRAIN, exit 0.
//
// Выключатель кампании — маркер-файл .quota-drain-enabled (проверяет раннер
// run-quota-drain.sh); сама утилита про него не знает — её можно звать и
// вручную: dry-run безопасен в любое время суток.
//
// Usage:
//
//	go run . --config config.yaml            # dry-run: снимок списка + план
//	go run . --config config.yaml --apply    # бой (обычно из run-quota-drain.sh)
//	go run . --config config.yaml --apply --reserve 2
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config — yaml-конфиг утилиты (cmd/ops-drain-report-quota/config.yaml).
type Config struct {
	WB    config.WBClientConfig `yaml:"wb"`
	Drain DrainConfig           `yaml:"drain"`
}

// DrainConfig — параметры кампании дренажа.
type DrainConfig struct {
	DailyLimit        int    `yaml:"daily_limit"`         // 20 (11-analytics.yaml:163)
	MaxFillers        int    `yaml:"max_fillers"`         // предохранитель: ≤19
	Reserve           int    `yaml:"reserve"`             // слоты, которые НЕ занимать
	Deadline          string `yaml:"deadline"`            // "01:05" локального времени (МСК на VPS2)
	PollIntervalSec   int    `yaml:"poll_interval_sec"`   // каденс guard-поллинга
	MainLockDir       string `yaml:"main_lock_dir"`       // жив → ночной блок ещё работает
	FillerNamePrefix  string `yaml:"filler_name_prefix"`  // "Quota drain"
	FunnelNamePrefix  string `yaml:"funnel_name_prefix"`  // наш маркер воронки
	FunnelLegacyName  string `yaml:"funnel_legacy_name"`  // авто-имя до adopt
	StockDailyPrefix  string `yaml:"stock_daily_prefix"`  // наш маркер стока
	StockMetricsPrefix string `yaml:"stock_metrics_prefix"`
}

func (c *Config) applyDefaults() {
	if c.WB.APIKeyEnv == "" {
		c.WB.APIKeyEnv = "WB_API_ANALYTICS_AND_PROMO_KEY"
	}
	d := &c.Drain
	if d.DailyLimit <= 0 {
		d.DailyLimit = 20
	}
	if d.MaxFillers <= 0 {
		d.MaxFillers = 19
	}
	if d.Deadline == "" {
		d.Deadline = "01:05"
	}
	if d.PollIntervalSec <= 0 {
		d.PollIntervalSec = 60
	}
	if d.MainLockDir == "" {
		d.MainLockDir = ".download-all.lock"
	}
	if d.FillerNamePrefix == "" {
		d.FillerNamePrefix = "Quota drain"
	}
	if d.FunnelNamePrefix == "" {
		d.FunnelNamePrefix = "Funnel detail"
	}
	if d.FunnelLegacyName == "" {
		d.FunnelLegacyName = "DETAIL_HISTORY_REPORT"
	}
	if d.StockDailyPrefix == "" {
		d.StockDailyPrefix = "Stock History daily"
	}
	if d.StockMetricsPrefix == "" {
		d.StockMetricsPrefix = "Stock History metrics"
	}
}

// markers — наши создания за сегодня, видимые в GET-списке.
type markers struct {
	funnel       bool
	stockDaily   bool
	stockMetrics bool
}

func (m markers) complete() bool {
	return m.funnel && m.stockDaily && m.stockMetrics
}

func (m markers) String() string {
	return fmt.Sprintf("funnel=%v stock-daily=%v stock-metrics=%v", m.funnel, m.stockDaily, m.stockMetrics)
}

// findMarkers ищет наши отчёты за сегодня. Свежесть — по сегодняшней дате
// ВНУТРИ ИМЕНИ (наши имена период тоже кодируют: «Funnel detail X..сегодня»,
// «Stock History daily X-сегодня»): createdAt у ещё генерирующихся отчётов
// может быть пуст, а endDate WB эхает для metrics днём раньше (наблюдение
// 02.11). Легаси-имя воронки дат не содержит — для него EndDate == today.
func findMarkers(items []wb.StockHistoryReportItem, cfg *Config, today string) markers {
	var m markers
	for _, it := range items {
		name := it.Name
		switch {
		case strings.HasPrefix(name, cfg.Drain.FunnelNamePrefix+" ") && strings.Contains(name, today):
			m.funnel = true
		case strings.EqualFold(name, cfg.Drain.FunnelLegacyName) && it.EndDate == today:
			// авто-имя WB = тип отчёта в нижнем регистре («detail_history_report»)
			m.funnel = true
		case strings.HasPrefix(name, cfg.Drain.StockDailyPrefix+" ") && strings.Contains(name, today):
			m.stockDaily = true
		case strings.HasPrefix(name, cfg.Drain.StockMetricsPrefix+" ") && strings.Contains(name, today):
			m.stockMetrics = true
		}
	}
	return m
}

// filterToday оставляет отчёты, чей createdAt (UTC-строка списка) попадает в
// текущие локальные сутки [00:00, now] — на VPS2 локальное время = МСК, тот
// же календарный день, по которому WB сбрасывает квоту в 00:00 МСК.
// Счётчик — оценка: незавершённые отчёты могут не иметь createdAt; истину
// показывает только дневной 429 на создании.
func filterToday(items []wb.StockHistoryReportItem, midnight, now time.Time) []wb.StockHistoryReportItem {
	var out []wb.StockHistoryReportItem
	for _, it := range items {
		at, ok := parseListedAt(it.CreatedAt)
		if !ok {
			continue
		}
		if !at.Before(midnight) && !at.After(now) {
			out = append(out, it)
		}
	}
	return out
}

// parseListedAt парсит createdAt из списка отчётов («2024-06-26 20:05:32», UTC).
func parseListedAt(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func localMidnight(now time.Time) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
}

// parseDeadline строит момент «сегодня HH:MM» в локальном времени.
func parseDeadline(s string, now time.Time) (time.Time, error) {
	hm := strings.Split(s, ":")
	if len(hm) != 2 {
		return time.Time{}, fmt.Errorf("deadline %q: want HH:MM", s)
	}
	h, errH := strconv.Atoi(hm[0])
	m, errM := strconv.Atoi(hm[1])
	if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return time.Time{}, fmt.Errorf("deadline %q: bad HH:MM", s)
	}
	y, mo, d := now.Date()
	return time.Date(y, mo, d, h, m, 0, 0, now.Location()), nil
}

// computeTarget — сколько наполнителей создавать: остаток минус резерв,
// ограниченный предохранителем maxFillers.
func computeTarget(already, limit, reserve, maxFillers int) int {
	remaining := limit - already
	if remaining <= 0 {
		return 0
	}
	t := remaining - reserve
	if t > maxFillers {
		t = maxFillers
	}
	if t < 0 {
		t = 0
	}
	return t
}

// isDailyLimit отличает дневной 429 (квота исчерпана — наша цель) от
// минутного («limited by <uuid>» — transient).
func isDailyLimit(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "daily response limit")
}

// decideAtDeadline решает, дренажить ли при неполных маркерах после дедлайна.
func decideAtDeadline(m markers, lockDir string) (drain bool, reason string) {
	if !m.funnel {
		return false, "наша воронка за сегодня не создана — не блокируем fallback второй машины на создание"
	}
	if m.stockDaily && m.stockMetrics {
		return true, "все наши маркеры на месте — дренажим"
	}
	if _, err := os.Stat(lockDir); os.IsNotExist(err) {
		return true, "сток-маркер(ы) отсутствуют, ночной блок уже завершился (лок снят) — сток эту ночь всё равно потерян, дренажим"
	}
	return false, "сток-маркер(ы) отсутствуют, ночной блок ещё идёт (лок жив) — сток-фаза могла не дойти"
}

// evidence печатает строки в stdout и копит их в
// logs/quota-drain-YYYY-MM-DD.log — файл-доказательство для переговоров.
type evidence struct {
	lines []string
	path  string
}

func (e *evidence) p(format string, a ...any) {
	line := time.Now().Format("2006-01-02 15:04:05") + " | " + fmt.Sprintf(format, a...)
	fmt.Println(line)
	e.lines = append(e.lines, line)
}

func (e *evidence) flush() {
	if len(e.lines) == 0 || e.path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(e.path), 0o755)
	f, err := os.OpenFile(e.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "evidence file:", err)
		return
	}
	defer f.Close()
	for _, l := range e.lines {
		fmt.Fprintln(f, l)
	}
}

func printTable(ev *evidence, title string, items []wb.StockHistoryReportItem) {
	ev.p("%s: %d отчёт(ов)", title, len(items))
	for _, it := range items {
		ev.p("  %s  %-50s  %s..%s  %s", it.CreatedAt, it.Name, it.StartDate, it.EndDate, it.Status)
	}
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	return &cfg, nil
}

// Темп созданий из сваггера: 3/мин на профиль (11-analytics.yaml:472-482).
const (
	createPerMin = 3
	createBurst  = 3
)

func run() int {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	apply := flag.Bool("apply", false, "Реальный дренаж (создавать наполнители). Без флага — dry-run: снимок и план")
	dryRunFlag := flag.Bool("dry-run", false, "Явный dry-run (то же, что без --apply)")
	reserveFlag := flag.Int("reserve", -1, "Сколько слотов НЕ занимать (override конфига)")
	flag.Parse()
	if *apply && *dryRunFlag {
		fmt.Fprintln(os.Stderr, "--apply и --dry-run взаимоисключимы")
		return 2
	}
	dryRun := !*apply

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 2
	}
	reserve := cfg.Drain.Reserve
	if *reserveFlag >= 0 {
		reserve = *reserveFlag
	}

	apiKey := os.Getenv(cfg.WB.APIKeyEnv)
	if apiKey == "" && cfg.WB.APIKeyEnv != "WB_API_ANALYTICS_AND_PROMO_KEY" {
		apiKey = os.Getenv("WB_API_ANALYTICS_AND_PROMO_KEY")
	}
	if apiKey == "" {
		fmt.Fprintf(os.Stderr, "env %s пуст — источник ключей .env (раннер run-quota-drain.sh его загружает)\n", cfg.WB.APIKeyEnv)
		return 2
	}

	ev := &evidence{path: filepath.Join("logs", "quota-drain-"+time.Now().Format("2006-01-02")+".log")}
	defer ev.flush()

	mode := "DRY-RUN (снимок и план, ничего не создаётся)"
	if !dryRun {
		mode = "APPLY (бой)"
	}
	ev.p("═══════  ops-drain-report-quota — кампания давления по квоте отчётов WB  ═══════")
	ev.p("  режим: %s | лимит %d/сутки | reserve=%d | дедлайн %s | max_fillers=%d",
		mode, cfg.Drain.DailyLimit, reserve, cfg.Drain.Deadline, cfg.Drain.MaxFillers)

	client := wb.New(apiKey)
	client.SetRateLimit("stock_history_create", createPerMin, createBurst, createPerMin, createBurst)
	client.SetRateLimit("stock_history_list", createPerMin, createBurst, createPerMin, createBurst)

	snapshot := func() ([]wb.StockHistoryReportItem, error) {
		return client.ListStockHistoryReports(ctx, createPerMin, createBurst)
	}

	deadline, err := parseDeadline(cfg.Drain.Deadline, time.Now())
	if err != nil {
		ev.p("FAIL: %v", err)
		return 2
	}

	// ── Снимок и маркеры ──
	items, err := snapshot()
	if err != nil {
		ev.p("FAIL: GET-список отчётов: %v", err)
		return 1
	}
	now := time.Now()
	todayStr := now.Format("2006-01-02")
	today := filterToday(items, localMidnight(now), now)
	printTable(ev, "список за сегодня (по createdAt)", today)
	m := findMarkers(items, cfg, todayStr)
	already := len(today)
	ev.p("наши маркеры за сегодня: %s", m)

	if dryRun {
		remaining := cfg.Drain.DailyLimit - already
		target := computeTarget(already, cfg.Drain.DailyLimit, reserve, cfg.Drain.MaxFillers)
		ev.p("план: создано %d/%d, remaining=%d, target=%d наполнителей (reserve=%d, max=%d)",
			already, cfg.Drain.DailyLimit, remaining, target, reserve, cfg.Drain.MaxFillers)
		ev.p("VERDICT: DRY-RUN — ничего не создано")
		return 0
	}

	// ── Guard «строго после наших утилит»: ждём три маркера до дедлайна ──
	for !m.complete() {
		if time.Now().After(deadline) {
			drain, reason := decideAtDeadline(m, cfg.Drain.MainLockDir)
			ev.p("DEADLINE %s: %s", cfg.Drain.Deadline, reason)
			if !drain {
				ev.p("VERDICT: ABORT — кампания эту ночь пропущена (наши данные важнее тайминга)")
				return 1
			}
			break
		}
		ev.p("guard: маркеры неполные (%s) — жду %dс …", m, cfg.Drain.PollIntervalSec)
		select {
		case <-ctx.Done():
			ev.p("FAIL: прерывание")
			return 1
		case <-time.After(time.Duration(cfg.Drain.PollIntervalSec) * time.Second):
		}
		items, err = snapshot()
		if err != nil {
			ev.p("FAIL: GET-список отчётов: %v", err)
			return 1
		}
		now = time.Now()
		today = filterToday(items, localMidnight(now), now)
		m = findMarkers(items, cfg, todayStr)
		already = len(today)
	}

	// ── Дренаж ──
	remaining := cfg.Drain.DailyLimit - already
	target := computeTarget(already, cfg.Drain.DailyLimit, reserve, cfg.Drain.MaxFillers)
	ev.p("сегодня уже создано: %d/%d; remaining=%d, target=%d", already, cfg.Drain.DailyLimit, remaining, target)
	if remaining <= 0 {
		ev.p("VERDICT: QUOTA_ALREADY_FULL — дренажить нечего (кто-то закрыл квоту раньше нас)")
		return 0
	}
	if target <= 0 {
		ev.p("VERDICT: RESERVE_KEPT — осталось %d слотов, reserve=%d", remaining, reserve)
		return 0
	}

	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	created := 0
	exhausted := false
	for i := 1; i <= target; i++ {
		req := wb.StockHistoryReportRequest{
			ID:             uuid.New().String(),
			ReportType:     "STOCK_HISTORY_DAILY_CSV",
			UserReportName: fmt.Sprintf("%s %s #%02d", cfg.Drain.FillerNamePrefix, todayStr, i),
			Params: wb.StockHistoryReportParams{
				CurrentPeriod: wb.StockHistoryPeriod{Start: yesterday, End: yesterday},
				StockType:     "",
				SkipDeletedNm: true,
			},
		}
		if _, err := client.CreateStockHistoryReport(ctx, req, createPerMin, createBurst); err != nil {
			if isDailyLimit(err) {
				exhausted = true
				ev.p("наполнитель %d/%d: 429 дневной лимит — квота исчерпана (это цель)", i, target)
				break
			}
			ev.p("FAIL: наполнитель %d/%d: %v", i, target, err)
			return 1
		}
		created++
		ev.p("наполнитель %d/%d создан: %q", i, target, req.UserReportName)
	}

	// ── Финальный снимок (evidence) ──
	if items2, err := snapshot(); err == nil {
		today2 := filterToday(items2, localMidnight(time.Now()), time.Now())
		printTable(ev, "список за сегодня ПОСЛЕ дренажа", today2)
		ev.p("итого за сегодня: %d/%d, из них наполнителей нашими: %d", len(today2), cfg.Drain.DailyLimit, created)
	} else {
		ev.p("warning: финальный снимок не удался: %v", err)
	}

	if exhausted {
		ev.p("VERDICT: QUOTA_EXHAUSTED — дневной лимит достигнут, пакет нарушителя 01:10 МСК получит 429")
	} else {
		ev.p("VERDICT: DRAINED — создано %d наполнителей", created)
	}
	return 0
}

func main() {
	os.Exit(run())
}
