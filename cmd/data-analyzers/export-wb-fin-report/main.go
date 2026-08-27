package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ============================================================================
// export-wb-fin-report — прототип XLSX фин-отчёта ВБ по макету фин-аналитика.
//
// Тянет детализацию реализации напрямую через новый finance endpoint
// (POST /api/finance/v1/sales-reports/detailed — замена отключённого
// GET /api/v5/supplier/reportDetailByPeriod) и пишет 56 колонок макета
// включая серые AM:AP (тип платежа, эквайринг, вознаграждение ВВ, его НДС).
//
// НИЧЕГО НЕ ПИШЕТ В БД: только API → XLSX. Чтение из БД для сверки делается
// отдельными read-only SQL-запросами.
//
// Режим --acquiring: отдельный «Отчёт об издержках на приём платежей» за тот
// же период (банки-эквайеры, комиссия в т.ч. НДС, счета-фактуры) + лист
// «Сверка». Контрольная Σ из основного отчёта — флагом --sales-acquiring-sum.
//
// Использование:
//   set -a && source .env && set +a
//   go build ./cmd/data-analyzers/export-wb-fin-report/
//   ./export-wb-fin-report --begin 2026-08-10 --end 2026-08-16
//   ./export-wb-fin-report --begin 2026-08-10 --end 2026-08-16 --acquiring --sales-acquiring-sum 5160445.68
//
// Лимит finance endpoint — 1 req/min (поэтому неделя качается минуты).
// Ключи: WB_STAT → WB_API_KEY (fallback s2s-finance, см. download-wb-sales-v2).
// ============================================================================

func main() {
	beginFlag := flag.String("begin", "", "Начало периода, YYYY-MM-DD (включительно)")
	endFlag := flag.String("end", "", "Конец периода, YYYY-MM-DD (включительно)")
	outFlag := flag.String("out", "", "Путь к XLSX (default: reports/wb-fin-report-<begin>_<end>.xlsx; в --acquiring — wb-acquiring-report-…)")
	period := flag.String("period", "daily", "Группировка отчёта WB: daily|weekly")
	acquiringMode := flag.Bool("acquiring", false, "Выгрузить отдельный отчёт об издержках на приём платежей (эквайринг: банки, НДС, счета-фактуры)")
	acquiringListOnly := flag.Bool("list-only", false, "С --acquiring: только список отчётов (диагностика дат), без детализации и XLSX")
	salesAcquiringSum := flag.Float64("sales-acquiring-sum", 0, "Контрольная Σ acquiringFee из основного отчёта (колонка AN) для листа «Сверка»; 0 = не сверять")
	rateLimit := flag.Int("rate-limit", 1, "Запросов в минуту к finance endpoint")
	burst := flag.Int("burst", 1, "Burst адаптивного лимитера")
	flag.Parse()

	if *beginFlag == "" || *endFlag == "" {
		log.Fatal("укажите период: --begin YYYY-MM-DD --end YYYY-MM-DD")
	}
	fromStr, toStr, err := periodRFC3339MSK(*beginFlag, *endFlag)
	if err != nil {
		log.Fatalf("период: %v", err)
	}

	outPath := *outFlag
	if outPath == "" {
		if *acquiringMode {
			outPath = fmt.Sprintf("reports/wb-acquiring-report-%s_%s.xlsx", *beginFlag, *endFlag)
		} else {
			outPath = fmt.Sprintf("reports/wb-fin-report-%s_%s.xlsx", *beginFlag, *endFlag)
		}
	}
	if dir := filepath.Dir(outPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("создать каталог %s: %v", dir, err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	methodPath := "/api/finance/v1/sales-reports/detailed"
	if *acquiringMode {
		methodPath = acquiringDetailMethodName + " + /api/finance/v1/acquiring/list"
	}
	dllog.PrintHeader("WB Fin Report Export (prototype)",
		dllog.HeaderField{Key: "Period", Value: fmt.Sprintf("%s → %s (MSK)", *beginFlag, *endFlag)},
		dllog.HeaderField{Key: "Method", Value: "POST " + wb.FinanceReportsBaseURL + methodPath},
		dllog.HeaderField{Key: "RateLimit", Value: fmt.Sprintf("%d req/min, burst %d", *rateLimit, *burst)},
		dllog.HeaderField{Key: "Out", Value: outPath},
	)

	client := wb.New(os.Getenv("WB_STAT"))
	// Страница детализации до 100k строк тяжёлая: дефолтных 30s HTTP-таймаута
	// мало на чтение тела (см. Client.SetHTTPTimeout, client.go:267).
	client.SetHTTPTimeout(10 * time.Minute)
	// Fallback для шлюза s2s-finance: statistics-scoped токен отвергается с 401
	// "token scope not allowed" — см. pkg/wb/client.go SetFinanceKey.
	if fallback := os.Getenv("WB_API_KEY"); fallback != "" {
		client.SetFinanceKey(fallback)
	}
	client.SetRateLimit("finance_sales_report", *rateLimit, *burst, *rateLimit, *burst)
	if *acquiringMode {
		client.SetRateLimit("finance_acquiring_report", *rateLimit, *burst, *rateLimit, *burst)
		runAcquiring(ctx, client, finReportConfig{
			begin: *beginFlag, end: *endFlag,
			fromStr: fromStr, toStr: toStr,
			outPath:   outPath,
			rateLimit: *rateLimit, burst: *burst,
			salesAcquiringSum: *salesAcquiringSum,
			listOnly:         *acquiringListOnly,
		})
		return
	}

	var rows []wb.RealizationReportRow
	fetchedPages := 0
	start := time.Now()
	total, err := client.SalesReportDetailedIterator(ctx, wb.FinanceReportsBaseURL,
		*rateLimit, *burst, fromStr, toStr, *period,
		func(batch []wb.RealizationReportRow) error {
			rows = append(rows, batch...)
			fetchedPages++
			fmt.Printf("  страница %d: +%d строк (итого %d)\n", fetchedPages, len(batch), len(rows))
			return nil
		})
	if err != nil {
		log.Fatalf("выгрузка: %v", err)
	}
	if total == 0 && len(rows) == 0 {
		fmt.Printf("⚠️  API вернул 0 строк за период %s..%s — проверьте, закрыт ли отчёт WB\n", *beginFlag, *endFlag)
	}

	outAbs, _ := filepath.Abs(outPath)
	meta := ReportMeta{
		Period:      fmt.Sprintf("%s…%s (MSK)", *beginFlag, *endFlag),
		Method:      "POST " + wb.FinanceReportsBaseURL + "/api/finance/v1/sales-reports/detailed",
		GeneratedAt: time.Now().Format("2006-01-02 15:04:05 MSK"),
		Sheet:       filepath.Base(outPath),
	}
	if err := BuildXLSX(outAbs, meta, rows); err != nil {
		log.Fatalf("xlsx: %v", err)
	}

	printSummary(outAbs, rows, start)
	dllog.Done(time.Since(start), "готово: %s (%d строк, %d отчётов)", outPath, len(rows), countReports(rows))
}

// finReportConfig — разрешённые параметры прогона (общие для обоих режимов).
type finReportConfig struct {
	begin, end        string // YYYY-MM-DD
	fromStr, toStr    string // RFC3339 MSK
	outPath           string
	rateLimit, burst  int
	salesAcquiringSum float64 // контроль из основного отчёта, 0 = не сверять
	listOnly          bool    // только acquiring/list (диагностика дат)
}

// periodRFC3339MSK собирает границы периода в RFC3339 с таймзоной МСК (UTC+3),
// как требует SalesReportsDetailedReq.DateFrom/DateTo.
func periodRFC3339MSK(beginDay, endDay string) (string, string, error) {
	const layout = "2006-01-02"
	b, err := time.Parse(layout, beginDay)
	if err != nil {
		return "", "", fmt.Errorf("--begin %q: %w", beginDay, err)
	}
	e, err := time.Parse(layout, endDay)
	if err != nil {
		return "", "", fmt.Errorf("--end %q: %w", endDay, err)
	}
	if e.Before(b) {
		return "", "", fmt.Errorf("--end раньше --begin")
	}
	msk := b.Format(layout) + "T00:00:00+03:00"
	to := e.Format(layout) + "T23:59:59+03:00"
	return msk, to, nil
}

// printSummary выводит контрольные суммы для быстрой сверки с BI/SQL.
func printSummary(path string, rows []wb.RealizationReportRow, start time.Time) {
	var acquiring, acquiringPct, vw, vwNds, forPay, sales float64
	for _, r := range rows {
		acquiring += r.AcquiringFee
		vw += r.PPVzVw
		vwNds += r.PPVzVwNds
		forPay += r.PPVzForPay
		sales += r.RetailAmount
		acquiringPct += r.AcquiringPercent // среднее невзвешенное — только ориентир
	}
	fmt.Println("\n=== Контрольные суммы (для сверки математики) ===")
	fmt.Printf("  Строк выгружено:          %d (отчётов реализации: %d)\n", len(rows), countReports(rows))
	fmt.Printf("  Сумма продаж (retailAmount):      %.2f ₽\n", sales)
	fmt.Printf("  К перечислению (forPay):          %.2f ₽\n", forPay)
	fmt.Printf("  Эквайринг (acquiringFee):         %.2f ₽  [серая колонка AN]\n", acquiring)
	fmt.Printf("  Вознаграждение ВВ без НДС (vw):   %.2f ₽  [серая колонка AO]\n", vw)
	fmt.Printf("  НДС с вознаграждения (vwNds):     %.2f ₽  [серая колонка AP]\n", vwNds)
	fmt.Printf("  Средний %% эквайринга (невзвеш.): %.2f%%\n", avgOrZero(acquiringPct, len(rows)))
	fmt.Printf("  Файл:    %s\n", path)
	fmt.Printf("  Длительность выгрузки: %s\n", time.Since(start).Round(time.Second))
}

func countReports(rows []wb.RealizationReportRow) int {
	set := make(map[int]struct{}, 64)
	for _, r := range rows {
		set[r.RealizationReportID] = struct{}{}
	}
	return len(set)
}

func avgOrZero(sum float64, n int) float64 {
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}
