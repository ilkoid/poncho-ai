// Package sqlite предоставляет SQLite реализацию репозиториев.
//
// Этот файл содержит функционал проверки свежести данных в таблицах.
// Правило 6: pkg/storage/sqlite — библиотечный код, переиспользуемый.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// TableSpec описывает таблицу и колонку с датой для проверки.
type TableSpec struct {
	Category   string // Категория: "Sales", "Analytics", и т.д.
	Table      string // Имя таблицы
	DateColumn string // Колонка с датой (MAX())
}

// FreshnessStatus — статус свежести данных.
type FreshnessStatus string

const (
	StatusFresh    FreshnessStatus = "FRESH"
	StatusStale    FreshnessStatus = "STALE"
	StatusCritical FreshnessStatus = "CRITICAL"
	StatusError    FreshnessStatus = "ERROR"
)

// FreshnessResult — результат проверки одной таблицы.
type FreshnessResult struct {
	TableSpec
	LatestDate *time.Time // Последняя дата (nil если таблица пуста или ошибка)
	AgeDays    int        // Возраст данных в днях
	Status     FreshnessStatus
	Error      error // Ошибка если status = ERROR
	RecordCount int    // Количество записей (опционально)
}

// IsCritical возвращает true, если статус критический.
func (r *FreshnessResult) IsCritical() bool {
	return r.Status == StatusCritical
}

// IsStale возвращает true, если данные устарели.
func (r *FreshnessResult) IsStale() bool {
	return r.Status == StatusStale || r.Status == StatusCritical
}

// AllTableSpecs возвращает все известные спецификации таблиц для wb-sales.db.
//
// Список основан на схеме базы данных от 2026-04-07.
func AllTableSpecs() []TableSpec {
	return []TableSpec{
		// SALES
		{"Sales", "sales", "sale_dt"},
		{"Sales", "fbw_sales", "sale_dt"},

		// ANALYTICS / FUNNEL
		{"Analytics", "funnel_metrics_daily", "metric_date"},
		{"Analytics", "funnel_metrics_aggregated", "period_end"},

		// PROMOTION / CAMPAIGNS
		{"Promotion", "campaigns", "updated_at"},
		{"Promotion", "campaign_stats_daily", "stats_date"},
		{"Promotion", "campaign_stats_app", "stats_date"},
		{"Promotion", "campaign_stats_nm", "stats_date"},
		{"Promotion", "campaign_booster_stats", "stats_date"},
		{"Promotion", "campaign_products", "created_at"},

		// STOCKS
		{"Stocks", "stocks_daily_warehouses", "snapshot_date"},
		{"Stocks", "stock_history_reports", "end_date"},
		{"Stocks", "stock_history_daily", "created_at"},
		{"Stocks", "stock_history_metrics", "created_at"},

		// CATALOG / PRODUCTS
		{"Catalog", "products", "updated_at"},
		{"Catalog", "cards", "updated_at"},
		{"Catalog", "product_prices", "snapshot_date"},  // Используем snapshot_date вместо created_at
		{"Catalog", "card_sizes", "N/A"},
		{"Catalog", "card_photos", "N/A"},
		{"Catalog", "card_characteristics", "N/A"},
		{"Catalog", "card_tags", "N/A"},

		// FEEDBACKS
		{"Feedbacks", "feedbacks", "created_date"},
		{"Feedbacks", "questions", "created_date"},

		// LOGISTICS
		{"Logistics", "region_sales", "date_to"},

		// SERVICE
		{"Service", "service_records", "created_at"},
		{"Service", "product_quality_summary", "analyzed_at"},
	}
}

// FilterTableSpecs фильтрует спецификации по списку имён таблиц.
//
// Если tables пустой — возвращает все спецификации.
func FilterTableSpecs(all []TableSpec, tables []string) []TableSpec {
	if len(tables) == 0 {
		return all
	}

	tableMap := make(map[string]bool, len(tables))
	for _, t := range tables {
		tableMap[t] = true
	}

	var filtered []TableSpec
	for _, spec := range all {
		if tableMap[spec.Table] {
			filtered = append(filtered, spec)
		}
	}
	return filtered
}

// FreshnessChecker проверяет свежесть данных в таблицах SQLite.
//
// Правило 11: Поддерживает context.Context для отмены операций.
// Правило 6: Библиотечный код в pkg/, переиспользуемый.
type FreshnessChecker struct {
	db *sql.DB
}

// NewFreshnessChecker создаёт новый проверщик свежести данных.
//
// Открывает SQLite соединение с оптимизационными PRAGMAs.
func NewFreshnessChecker(dbPath string) (*FreshnessChecker, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Проверка соединения
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	// Оптимизационные PRAGMAs (как в repository.go)
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 10000",
		"PRAGMA page_size = 8192",
		"PRAGMA cache_size = -65536",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %s: %w", p, err)
		}
	}

	return &FreshnessChecker{db: db}, nil
}

// Close закрывает соединение с базой данных.
func (fc *FreshnessChecker) Close() error {
	return fc.db.Close()
}

// CheckTable проверяет freshness для одной таблицы.
//
// Правило 11: Учитывает context.Context для отмены.
// Для таблиц без колонки даты (DateColumn = "N/A") возвращает StatusFresh с количеством записей.
func (fc *FreshnessChecker) CheckTable(ctx context.Context, spec TableSpec, warnAge, critAge int) FreshnessResult {
	result := FreshnessResult{
		TableSpec: spec,
		Status:    StatusError,
	}

	// Таблицы без колонки даты (справочные данные)
	if spec.DateColumn == "N/A" {
		var count int
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s", spec.Table)
		err := fc.db.QueryRowContext(ctx, query).Scan(&count)
		if err != nil {
			result.Error = fmt.Errorf("query table: %w", err)
			return result
		}
		result.RecordCount = count
		result.Status = StatusFresh // Справочные данные всегда "свежие"
		result.AgeDays = 0
		return result
	}

	// SQL запрос для получения максимальной даты
	query := fmt.Sprintf("SELECT MAX(%s), COUNT(*) FROM %s", spec.DateColumn, spec.Table)

	var maxDateStr sql.NullString
	var count int

	// Выполняем запрос с учётом контекста
	err := fc.db.QueryRowContext(ctx, query).Scan(&maxDateStr, &count)
	if err != nil {
		result.Error = fmt.Errorf("query table: %w", err)
		return result
	}

	result.RecordCount = count

	// Если таблица пуста
	if !maxDateStr.Valid {
		result.Status = StatusCritical
		result.AgeDays = -1 // Специальное значение для пустой таблицы
		return result
	}

	// Парсим дату
	// SQLite хранит даты в разных форматах, пробуем несколько
	latestDate, err := parseSQLiteDate(maxDateStr.String)
	if err != nil {
		result.Error = fmt.Errorf("parse date: %w", err)
		return result
	}

	result.LatestDate = &latestDate

	// Вычисляем возраст в днях
	ageDays := int(time.Since(latestDate).Hours() / 24)
	result.AgeDays = ageDays

	// Определяем статус
	switch {
	case ageDays >= critAge:
		result.Status = StatusCritical
	case ageDays >= warnAge:
		result.Status = StatusStale
	default:
		result.Status = StatusFresh
	}

	return result
}

// CheckAll проверяет freshness для всех указанных таблиц параллельно.
//
// Правило 11: Учитывает context.Context для отмены.
// Использует goroutines для ускорения проверки.
func (fc *FreshnessChecker) CheckAll(ctx context.Context, specs []TableSpec, warnAge, critAge int) []FreshnessResult {
	results := make([]FreshnessResult, len(specs))
	var wg sync.WaitGroup

	for i, spec := range specs {
		wg.Add(1)
		go func(idx int, s TableSpec) {
			defer wg.Done()

			// Проверяем context перед запросом
			select {
			case <-ctx.Done():
				results[idx] = FreshnessResult{
					TableSpec: s,
					Status:    StatusError,
					Error:     ctx.Err(),
				}
				return
			default:
			}

			results[idx] = fc.CheckTable(ctx, s, warnAge, critAge)
		}(i, spec)
	}

	wg.Wait()
	return results
}

// parseSQLiteDate парсит дату из различных форматов SQLite.
//
// Поддерживаемые форматы:
// - RFC3339: 2026-04-05T00:00:00Z
// - RFC3339 с timezone: 2026-04-05T23:59:59+03:00
// - Date only: 2026-04-05
// - SQLite format: 2026-04-07
func parseSQLiteDate(s string) (time.Time, error) {
	// Пробуем разные форматы
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05-07:00",
		"2006-01-02T15:04:05+07:00",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05.999999999Z",
	}

	for _, format := range formats {
		t, err := time.Parse(format, s)
		if err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unknown date format: %s", s)
}

// StatusIndicator возвращает emoji индикатор для статуса.
func StatusIndicator(status FreshnessStatus) string {
	switch status {
	case StatusFresh:
		return "✅"
	case StatusStale:
		return "⚠️ "
	case StatusCritical:
		return "❌"
	case StatusError:
		return "💥"
	default:
		return "❓"
	}
}

// CategoryEmoji возвращает emoji для категории.
func CategoryEmoji(category string) string {
	switch category {
	case "Sales":
		return "📊"
	case "Analytics":
		return "📈"
	case "Promotion":
		return "📢"
	case "Stocks":
		return "📦"
	case "Catalog":
		return "🏪"
	case "Feedbacks":
		return "💬"
	case "Logistics":
		return "🌍"
	case "Service":
		return "⚙️ "
	default:
		return "📋"
	}
}

// FormatDate форматирует дату для вывода.
func FormatDate(t *time.Time) string {
	if t == nil {
		return "N/A"
	}
	return t.Format("2006-01-02")
}

// FormatAge форматирует возраст для вывода.
func FormatAge(ageDays int) string {
	if ageDays < 0 {
		return "EMPTY"
	}
	return fmt.Sprintf("%dd", ageDays)
}

// FormatRecordCount форматирует количество записей для таблиц без даты.
func FormatRecordCount(count int) string {
	return fmt.Sprintf("%d rows", count)
}

// TableWidth возвращает ширину колонок для таблицы вывода.
const (
	ColCategory = 12
	ColTable    = 28
	ColDate     = 12
	ColAge      = 8
	ColStatus   = 12
)

// TruncateString обрезает строку до указанной длины (в рунах, для корректной работы с emoji).
func TruncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-3]) + "..."
}

// PadRight дополняет строку пробелами справа (учитывает руны, а не байты).
func PadRight(s string, length int) string {
	runes := []rune(s)
	if len(runes) >= length {
		return TruncateString(s, length)
	}
	return s + strings.Repeat(" ", length-len(runes))
}

// PadLeft дополняет строку пробелами слева (учитывает руны, а не байты).
func PadLeft(s string, length int) string {
	runes := []rune(s)
	if len(runes) >= length {
		return TruncateString(s, length)
	}
	return strings.Repeat(" ", length-len(runes)) + s
}

// PrintRow печатает строку таблицы.
func PrintRow(category, table, date, age, status string) {
	indicator := StatusIndicator(FreshnessStatus(status))
	categoryEmoji := CategoryEmoji(category)

	// Формируем статус с indicator
	statusWithIndicator := status + " " + indicator

	fmt.Printf(
		"%s%s │ %s%s │ %s%s │ %s%s │ %s\n",
		categoryEmoji, PadRight(category, ColCategory-2),
		"", PadRight(table, ColTable),
		"", PadRight(date, ColDate),
		"", PadLeft(age, ColAge),
		PadRight(statusWithIndicator, ColStatus),
	)
}

// PrintRowWithCount печатает строку таблицы с количеством записей (для таблиц без даты).
func PrintRowWithCount(category, table, count, status string) {
	indicator := StatusIndicator(FreshnessStatus(status))
	categoryEmoji := CategoryEmoji(category)

	// Формируем статус с indicator
	statusWithIndicator := status + " " + indicator

	fmt.Printf(
		"%s%s │ %s%s │ %s │ %s │ %s\n",
		categoryEmoji, PadRight(category, ColCategory-2),
		"", PadRight(table, ColTable),
		PadRight(count, ColDate),
		PadLeft("N/A", ColAge),
		PadRight(statusWithIndicator, ColStatus),
	)
}

// PrintSeparator печатает разделитель таблицы.
func PrintSeparator() {
	fmt.Println(
		strings.Repeat("─", ColCategory) + "┼" +
			strings.Repeat("─", ColTable) + "┼" +
			strings.Repeat("─", ColDate) + "┼" +
			strings.Repeat("─", ColAge) + "┼" +
			strings.Repeat("─", ColStatus),
	)
}

// PrintHeader печатает заголовок таблицы.
func PrintHeader() {
	fmt.Printf(
		"%s │ %s │ %s │ %s │ %s\n",
		PadRight("CATEGORY", ColCategory),
		PadRight("TABLE", ColTable),
		PadRight("LATEST DATE", ColDate),
		PadLeft("AGE", ColAge),
		PadRight("STATUS", ColStatus),
	)
	PrintSeparator()
}
