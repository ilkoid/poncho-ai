package sales

import (
	"context"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// FinanceSalesSource — альтернативная реализация SalesSource поверх нового
// асинхронного... на самом деле СИНХРОННОГО finance endpoint
// POST /api/finance/v1/sales-reports/detailed (замена отключённого
// GET /api/v5/supplier/reportDetailByPeriod, WB release-notes id=498 от 15.07.2026).
//
// Зачем отдельный тип, а не правка *wb.Client:
//   - Интерфейс SalesSource (pkg/sales/types.go) сегодня реализуется самим
//     *wb.Client через его методы ReportDetailByPeriodIterator{,WithTime}.
//     Подменять глобально — значит ломать все прочие потребители старого
//     endpoint'а (если WB ещё не везде отключил). Поэтому новый источник —
//     отдельный адаптер, который main.go подключает одной строкой.
//   - Методы интерфейса намеренно НЕ переименовываются (остаются
//     ReportDetailByPeriodIterator*): downloader ничего не знает о смене
//     endpoint'а, как и Writer, и таблицы, и sh-скрипты, и yaml.
//
// baseURL из параметра игнорируется: новый endpoint живёт на
// wb.FinanceReportsBaseURL (finance-api.wildberries.ru), а не на statistics-api,
// который приходит из старых конфигов sales.
type FinanceSalesSource struct {
	client *wb.Client
	// BaseURL можно переопределить для тестов; по умолчанию — wb.FinanceReportsBaseURL.
	BaseURL string
	// Period — "daily" | "weekly"; пусто = weekly (как было у старого endpoint).
	Period string
}

// NewFinanceSalesSource оборачивает wb.Client в источник продаж на finance endpoint.
func NewFinanceSalesSource(client *wb.Client) *FinanceSalesSource {
	return &FinanceSalesSource{
		client:  client,
		BaseURL: wb.FinanceReportsBaseURL,
		Period:  "daily",
	}
}

// Compile-time assertion: FinanceSalesSource удовлетворяет SalesSource.
// Методы НЕ переименованы — интерфейс остаётся прежним.
var _ SalesSource = (*FinanceSalesSource)(nil)

// financeURL возвращает baseURL с откатом к константе.
func (s *FinanceSalesSource) financeURL() string {
	if s.BaseURL != "" {
		return s.BaseURL
	}
	return wb.FinanceReportsBaseURL
}

// ReportDetailByPeriodIteratorWithTime — time-aware вариант (RFC3339).
// downloader.go вызывает его для диапазонов с HasTime()==true.
// Сигнатура идентична старой, чтобы сохранить контракт SalesSource.
func (s *FinanceSalesSource) ReportDetailByPeriodIteratorWithTime(
	ctx context.Context,
	_ string, // baseURL (statistics-api) игнорируется — нужен finance-api
	rateLimit int,
	burst int,
	dateFrom string,
	dateTo string,
	callback func([]wb.RealizationReportRow) error,
) (int, error) {
	return s.client.SalesReportDetailedIterator(ctx, s.financeURL(), rateLimit, burst, dateFrom, dateTo, s.Period, callback)
}

// ReportDetailByPeriodIterator — date-int вариант (YYYYMMDD).
// downloader.go вызывает его для диапазонов без времени.
// Конвертирует YYYYMMDD → YYYY-MM-DD (как делал оригинальный Page-метод).
func (s *FinanceSalesSource) ReportDetailByPeriodIterator(
	ctx context.Context,
	_ string, // baseURL игнорируется
	rateLimit int,
	burst int,
	dateFrom int,
	dateTo int,
	callback func([]wb.RealizationReportRow) error,
) (int, error) {
	fromStr, err := yyyymmddToDate(dateFrom)
	if err != nil {
		return 0, err
	}
	toStr, err := yyyymmddToDate(dateTo)
	if err != nil {
		return 0, err
	}
	return s.client.SalesReportDetailedIterator(ctx, s.financeURL(), rateLimit, burst, fromStr, toStr, s.Period, callback)
}

// yyyymmddToDate преобразует int YYYYMMDD → "YYYY-MM-DD".
func yyyymmddToDate(d int) (string, error) {
	if d <= 0 {
		return "", fmt.Errorf("invalid date int %d", d)
	}
	y := d / 10000
	m := (d % 10000) / 100
	day := d % 100
	if y < 2000 || y > 2100 || m < 1 || m > 12 || day < 1 || day > 31 {
		return "", fmt.Errorf("invalid date int %d", d)
	}
	return fmt.Sprintf("%04d-%02d-%02d", y, m, day), nil
}
