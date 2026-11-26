// ops-token-watchdog — сторож срока жизни WB-токенов.
//
// Инцидент 17.11.2026: весь пакет WB-ключей (7 шт.) — это JWT с полем exp,
// выпущенные одним днём и истёкшие синхронно ночью 03:47–03:58 МСК — конвейер
// прозевал это и потерял сутки данных. exp зашит в самих токенах и читается
// локально, без сети, — но никто его не смотрел. Сторож смотрит за нас.
//
// Два контура:
//  1. exp-проверка: читает .env напрямую (канонический источник секретов,
//     AGENTS.md — независимо от корректности env раннера), берёт все WB_*,
//     декодирует у JWT-значений exp и печатает таблицу «осталось дней».
//  2. скан логов: сегодняшние logs/download-all*.log, логи «спасателей» и
//     логи роботов габаритов (fix-dims на VPS2, fill-dims на Mac) на
//     «token is expired» — если токен умер между проверками, утренний
//     вердикт назовёт виновника по происхождению (ag-advert и т.п.).
//     Инцидент 17–26.11: fix-dims 9 дней падал по 401 вне globs; поэтому
//     в лог печатается и число просмотренных файлов — «следов нет» должно
//     быть отличимо от «не смотрел». В накопительных логах учитываются
//     только строки сегодняшнего дня.
//
// Вердикты и exit-коды (канон ops-drain-report-quota):
//
//	0 — все живы, следов истечения нет;
//	1 — ТРЕВОГА: истёк / осталось ≤ alert_days / пустое значение / битый
//	    JWT / в логах за сегодня есть 401 «token is expired»;
//	2 — конфиг-ошибки (.env не найден и т.п.).
//
// Письма шлёт НЕ утилита, а раннер run-token-watchdog.sh (при exit≠0, через
// cmd/ops-notify/mail-notify). Тишина при exit 0 — норма.
//
// Безопасность: утилита строго read-only (читает .env и логи); значения
// токенов нигде не печатаются и не логируются — только имена, длины, даты.
//
// Usage:
//
//	go run . --config config.yaml            # обычная проверка
//	go run . --config config.yaml --days 999 # принудительный алерт (тест письма)
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// Config — yaml-конфиг утилиты (cmd/ops-token-watchdog/config.yaml).
type Config struct {
	Watchdog WatchdogConfig `yaml:"watchdog"`
}

// WatchdogConfig — параметры сторожа.
type WatchdogConfig struct {
	EnvPath      string   `yaml:"env_path"`       // canonical .env в корне репо
	AlertDays    int      `yaml:"alert_days"`     // ≤N дней до exp → тревога (письмо)
	RemindDays   int      `yaml:"remind_days"`    // ≤N дней → строка-напоминание в лог
	ScanLogGlobs []string `yaml:"scan_log_globs"` // сегодняшние логи конвейера (по mtime)
	IgnoreVars   []string `yaml:"ignore_vars"`    // не тревожить (например, ждёт перевыпуска)
}

func (c *Config) applyDefaults() {
	w := &c.Watchdog
	if w.EnvPath == "" {
		w.EnvPath = ".env"
	}
	if w.AlertDays <= 0 {
		w.AlertDays = 7
	}
	if w.RemindDays <= 0 {
		w.RemindDays = 14
	}
	if len(w.ScanLogGlobs) == 0 {
		w.ScanLogGlobs = []string{
			"logs/download-all*.log",
			"logs/download-day-snapshots*.log",
			"logs/fix-dims-*.log",     // VPS2: робот по штрафам за габариты
			"logs/cron-fill-dims.log", // Mac: fill-dims из 1С
		}
	}
}

// tokenStatus — класс вердикта по одному токену.
type tokenStatus string

const (
	statusOK      tokenStatus = "OK"
	statusRemind  tokenStatus = "СКОРО"
	statusAlert   tokenStatus = "ТРЕВОГА"
	statusExpired tokenStatus = "ИСТЁК"
	statusNoJWT   tokenStatus = "НЕ JWT"
	statusEmpty   tokenStatus = "ПУСТО"
	statusBroken  tokenStatus = "БИТЫЙ JWT"
	statusIgnored tokenStatus = "IGNORE"
	statusUnknown tokenStatus = "НЕТ В .env"
)

// envExportRe — канонический формат .env: export VAR='значение' (AGENTS.md).
var envExportRe = regexp.MustCompile(`^export\s+([A-Za-z_][A-Za-z0-9_]*)='([^']*)'\s*$`)

type envVar struct {
	name  string
	value string
}

// parseEnvFile читает .env построчно: канонические строки → vars, всё
// непустое и некомментарное, что не распознано, → skipped (для WARN: смена
// формата файла не должна проходить молча).
func parseEnvFile(path string) (vars []envVar, skipped int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // токены длинные, но <1 МБ
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if m := envExportRe.FindStringSubmatch(line); m != nil {
			vars = append(vars, envVar{name: m[1], value: m[2]})
		} else {
			skipped++
		}
	}
	return vars, skipped, sc.Err()
}

// jwtExp достаёт exp из payload JWT. Секрет не трогаем — только число.
// Поддерживает и RawURL (без паддинга — стандарт JWT), и URL с паддингом.
func jwtExp(token string) (int64, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return 0, fmt.Errorf("не JWT: нет разделителей '.'")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return 0, fmt.Errorf("payload не base64url: %w", err)
		}
	}
	var body struct {
		Exp float64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return 0, fmt.Errorf("payload не JSON: %w", err)
	}
	if body.Exp <= 0 {
		return 0, fmt.Errorf("в payload нет поля exp")
	}
	return int64(body.Exp), nil
}

// tokenRow — строка таблицы отчёта.
type tokenRow struct {
	name   string
	length int
	exp    time.Time // нулевая, если exp неизвестен
	status tokenStatus
	reason string // уточнение для не-OK статусов
}

// classifyToken — чистая классификация (тестируется без файлов).
func classifyToken(name, value string, now time.Time, alertDays, remindDays int) tokenRow {
	row := tokenRow{name: name, length: len(value)}
	if value == "" {
		row.status, row.reason = statusEmpty, "значение пустое — ключ пропал из .env?"
		return row
	}
	if !strings.HasPrefix(value, "eyJ") {
		row.status, row.reason = statusNoJWT, "старый формат без exp — вне контроля"
		return row
	}
	expUnix, err := jwtExp(value)
	if err != nil {
		row.status, row.reason = statusBroken, err.Error()
		return row
	}
	row.exp = time.Unix(expUnix, 0)
	days := int(now.Sub(row.exp).Hours() / 24) // >0 = уже истёк
	switch {
	case !row.exp.After(now):
		row.status, row.reason = statusExpired, fmt.Sprintf("истёк %d дн. назад", days)
	case now.AddDate(0, 0, alertDays).After(row.exp):
		row.status, row.reason = statusAlert, fmt.Sprintf("осталось ≤%d дн.", alertDays)
	case now.AddDate(0, 0, remindDays).After(row.exp):
		row.status, row.reason = statusRemind, fmt.Sprintf("осталось ≤%d дн.", remindDays)
	default:
		row.status = statusOK
	}
	return row
}

// expiredRe — след смерти токена в логах конвейера (401-е тело WB).
var (
	expiredLineRe = regexp.MustCompile(`token is expired`)
	originRe      = regexp.MustCompile(`"origin":"([a-z-]+)"`)
)

// logScan — итог скана логов за сегодня.
type logScan struct {
	scanned int // сегодняшние файлы, которые реально читали
	total   int // число следов «token is expired»
	perFile map[string]int
	origins []string // отсортированные уникальные ag-*
}

// scanTodayLogs ищет «token is expired» в файлах по glob'ам, изменённых
// сегодня (по mtime — ловит и кросс-полночные прогоны с датой вчерашнего
// дня в имени). В накопительном файле считаются только строки сегодняшнего
// дня (scanFileForExpired). Большие логи читает стримом.
func scanTodayLogs(globs []string, midnight time.Time) logScan {
	out := logScan{perFile: map[string]int{}}
	originSet := map[string]bool{}
	for _, g := range globs {
		paths, _ := filepath.Glob(g)
		for _, p := range paths {
			st, err := os.Stat(p)
			if err != nil || st.ModTime().Before(midnight) {
				continue
			}
			out.scanned++
			n := scanFileForExpired(p, originSet, midnight)
			if n > 0 {
				out.perFile[p] = n
				out.total += n
			}
		}
	}
	for o := range originSet {
		out.origins = append(out.origins, o)
	}
	sort.Strings(out.origins)
	return out
}

// scanFileForExpired считает следы в файле. След — только от сегодняшнего дня:
// строка с датой в начале, отличной от today, пропускается; без даты —
// считается сегодняшней (так пишут вложенные утилиты в дневных файлах).
// Причина: logs/cron-fill-dims.log накопительный, и след от 18.11 (окно до
// перевыпуска токенов) лежит в нём постоянно — без этой правки давал бы
// ложную тревогу каждое утро.
func scanFileForExpired(path string, origins map[string]bool, today time.Time) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !expiredLineRe.MatchString(line) || datedOtherDay(line, today) {
			continue
		}
		n++
		if m := originRe.FindStringSubmatch(line); m != nil {
			origins[m[1]] = true
		}
	}
	return n
}

// leadingDateRe — дата в начале строки лога: «2026/11/26 …» (std log Go)
// или «[2026-11-26 07:17:01] …» (log() крон-обёрток).
var leadingDateRe = regexp.MustCompile(`^\[?(\d{4})[/-](\d{2})[/-](\d{2})`)

// datedOtherDay — строка помечена датой, отличной от сегодняшней.
func datedOtherDay(line string, today time.Time) bool {
	m := leadingDateRe.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	return m[1]+"-"+m[2]+"-"+m[3] != today.Format("2006-01-02")
}

// evidence печатает строки в stdout и копит их в
// logs/token-watchdog-YYYY-MM-DD.log (паттерн ops-drain-report-quota).
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

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	return &cfg, nil
}

func run() int {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	daysFlag := flag.Int("days", -1, "Override alert_days (для проверки алерт-пути/письма)")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 2
	}
	if *daysFlag >= 0 {
		cfg.Watchdog.AlertDays = *daysFlag
	}

	ev := &evidence{path: filepath.Join("logs", "token-watchdog-"+time.Now().Format("2006-01-02")+".log")}
	defer ev.flush()

	now := time.Now() // обе машины хостятся в МСК
	ev.p("═══════  ops-token-watchdog — сторож срока жизни WB-токенов  ═══════")
	ev.p("  .env: %s | тревога ≤%d дн., напоминание ≤%d дн. | ignore: %s",
		cfg.Watchdog.EnvPath, cfg.Watchdog.AlertDays, cfg.Watchdog.RemindDays,
		strings.Join(cfg.Watchdog.IgnoreVars, ", "))

	// ── Контур 1: exp всех WB_* из .env ──
	vars, skipped, err := parseEnvFile(cfg.Watchdog.EnvPath)
	if err != nil {
		ev.p("FAIL: %s: %v (конфиг-ошибка — сторож не может работать)", cfg.Watchdog.EnvPath, err)
		return 2
	}
	ignored := map[string]bool{}
	for _, v := range cfg.Watchdog.IgnoreVars {
		ignored[v] = true
	}
	var rows []tokenRow
	for _, v := range vars {
		if !strings.HasPrefix(v.name, "WB_") {
			continue
		}
		row := classifyToken(v.name, v.value, now, cfg.Watchdog.AlertDays, cfg.Watchdog.RemindDays)
		if ignored[v.name] {
			row.status, row.reason = statusIgnored, "в ignore_vars конфига"
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })

	ev.p("токены WB_* в .env: %d шт.", len(rows))
	var alerts []string
	for _, r := range rows {
		expStr := "—"
		if !r.exp.IsZero() {
			expStr = r.exp.Format("02.01.2006 15:04")
		}
		suffix := ""
		if r.reason != "" {
			suffix = " (" + r.reason + ")"
		}
		ev.p("  %-32s len=%-4d exp=%-17s %s%s", r.name, r.length, expStr, r.status, suffix)
		switch r.status {
		case statusExpired:
			alerts = append(alerts, fmt.Sprintf("%s ИСТЁК (%s)", r.name, r.reason))
		case statusAlert:
			alerts = append(alerts, fmt.Sprintf("%s истекает (%s)", r.name, r.reason))
		case statusEmpty, statusBroken:
			alerts = append(alerts, fmt.Sprintf("%s %s (%s)", r.name, r.status, r.reason))
		}
	}
	if skipped > 0 {
		ev.p("WARN: %d строк(и) в .env не распознаны каноническим форматом export VAR='…' — проверить файл", skipped)
	}
	if len(rows) == 0 {
		ev.p("FAIL: в %s нет ни одной переменной WB_* — .env сломан или пуст", cfg.Watchdog.EnvPath)
		return 2
	}

	// ── Контур 2: следы «token is expired» в сегодняшних логах ──
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	scan := scanTodayLogs(cfg.Watchdog.ScanLogGlobs, midnight)
	if scan.total > 0 {
		var files []string
		for f := range scan.perFile {
			files = append(files, fmt.Sprintf("%s (%d)", filepath.Base(f), scan.perFile[f]))
		}
		sort.Strings(files)
		ev.p("SCAN: просмотрено сегодняшних логов: %d; следов «token is expired» за сегодня: %d (%s); сервисы: %s",
			scan.scanned, scan.total, strings.Join(files, ", "), strings.Join(scan.origins, ", "))
		alerts = append(alerts, fmt.Sprintf("в логах за сегодня %d «token is expired» (сервисы: %s)",
			scan.total, strings.Join(scan.origins, ", ")))
	} else {
		ev.p("SCAN: просмотрено сегодняшних логов: %d; следов «token is expired» за сегодня нет", scan.scanned)
	}

	// ── Вердикт ──
	if len(alerts) > 0 {
		for _, a := range alerts {
			ev.p("⚠️  %s", a)
		}
		ev.p("VERDICT: ALERT — требуется перевыпуск токенов в ЛК WB (Настройки → Интеграции → API-ключи) и обновление .env на ОБЕИХ машинах")
		return 1
	}
	ev.p("VERDICT: OK — все контролируемые токены живы, следов истечения в логах нет")
	return 0
}

func main() {
	os.Exit(run())
}
