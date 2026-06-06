// validate-certificates — утилита для проверки сертификатов и деклараций соответствия
// через ФГИС РОСАККРЕДИТАЦИИ (pub.fsa.gov.ru).
//
// Читает номера сертификатов из onec_goods, сверяет статус и сроки с реестром ФСА.
// Использует headless Chromium для получения Bearer-токена, затем net/http для поиска.
//
// Usage:
//
//	go run ./cmd/data-analyzers/validate-certificates/ [options]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/filter"
)

// Config — конфигурация утилиты (config.yaml + CLI overrides).
type Config struct {
	DBPath       string        `yaml:"db_path"`
	Filters      filter.Filter `yaml:"filters"`
	Limit        int           `yaml:"limit"`
	DelayMin     float64       `yaml:"delay_min"`
	DelayMax     float64       `yaml:"delay_max"`
	ChromiumPath string        `yaml:"chromium_path"`
	CSV          string        `yaml:"csv"`
	XLSX         string        `yaml:"xlsx"`
}

// loadConfig загружает конфигурацию из YAML файла.
func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// defaultConfig возвращает конфигурацию по умолчанию.
func defaultConfig() *Config {
	return &Config{
		DBPath:       "/var/db/wb-sales.db",
		Limit:        0,
		DelayMin:     1.0,
		DelayMax:     4.0,
		ChromiumPath: "/usr/bin/chromium-browser",
		CSV:          "",
	}
}

// applyDefaults заполняет нулевые поля значениями по умолчанию.
func (c *Config) applyDefaults() {
	d := defaultConfig()
	if c.DBPath == "" {
		c.DBPath = d.DBPath
	}
	if c.DelayMin == 0 {
		c.DelayMin = d.DelayMin
	}
	if c.DelayMax == 0 {
		c.DelayMax = d.DelayMax
	}
	if c.DelayMax < c.DelayMin {
		c.DelayMax = c.DelayMin
	}
	if c.DelayMin < 0.5 {
		c.DelayMin = 0.5
	}
	if c.ChromiumPath == "" {
		c.ChromiumPath = d.ChromiumPath
	}
}

func printHelp() {
	fmt.Printf(`Usage: %s [options]

Проверка сертификатов/деклараций соответствия через ФГИС РОСАККРЕДИТАЦИИ.
Читает из wb-sales.db (read-only). Фильтрация через config.yaml (pkg/filter).
Использует headless Chromium для авторизации.

Options:
  --config PATH    Путь к конфигурационному файлу (default: config.yaml)
  --db PATH        Путь к базе данных (default: /var/db/wb-sales.db)
  --limit N        Ограничить количество записей (0 = все)
  --number NUM     Проверить конкретный номер (без обращения к БД)
  --delay MIN,MAX  Мин/макс задержка между запросами, сек (default: 1,4)
  --chromium PATH  Путь к Chromium (default: /usr/bin/chromium-browser)
  --csv PATH       Экспорт результатов в CSV
  --xlsx PATH      Экспорт результатов в XLSX
  --mock           Использовать тестовые данные (без API-запросов, без браузера)
  --dry-run        Показать список сертификатов без проверки
  -h, --help       Показать справку

Фильтры (через config.yaml → filters):
  vendor_codes       — конкретные артикулы
  allowed_years      — год из позиций 2-3 артикула (напр. [25, 26])
  exclude_lengths    — исключить артикулы указанной длины
  subject            — предмет WB (точное совпадение)
  in_stock           — только с остатками
  onec_type          — тип 1C (Обувь/Одежда/Аксессуары)
  category_level1/2  — 1C категории
  active_only        — исключить заблокированные в 1C

Examples:
  %s                                                       # С фильтрами из config.yaml
  %s --dry-run                                             # Показать что будет проверено
  %s --number 'ЕАЭС RU С-CN.ПФ02.В.07011/23'              # Проверить один номер
  %s --mock                                                # Тестовый прогон
`, os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0])
}

func main() {
	configPath := flag.String("config", "config.yaml", "Путь к конфигурационному файлу")
	dbPath := flag.String("db", "", "Путь к базе данных (overrides config)")
	mock := flag.Bool("mock", false, "Использовать тестовые данные")
	dryRun := flag.Bool("dry-run", false, "Показать список без проверки")
	csvPath := flag.String("csv", "", "Экспорт результатов в CSV (overrides config)")
	xlsxPath := flag.String("xlsx", "", "Экспорт результатов в XLSX (overrides config)")
	limit := flag.Int("limit", 0, "Ограничить количество (0 = все, overrides config)")
	testNumber := flag.String("number", "", "Проверить конкретный номер (без обращения к БД)")
	delayStr := flag.String("delay", "", "Задержка между запросами (мин,макс, overrides config)")
	chromiumPath := flag.String("chromium", "", "Путь к Chromium (overrides config)")
	help := flag.Bool("help", false, "Показать справку")
	flag.BoolVar(help, "h", false, "Показать справку")
	flag.Parse()

	if *help {
		printHelp()
		os.Exit(0)
	}

	// Load config: YAML → apply defaults.
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Printf("Конфиг не найден (%s), используем defaults: %v", *configPath, err)
		cfg = defaultConfig()
	}
	cfg.applyDefaults()

	// CLI overrides (non-zero values override config).
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}
	if *csvPath != "" {
		cfg.CSV = *csvPath
	}
	if *xlsxPath != "" {
		cfg.XLSX = *xlsxPath
	}
	if *limit != 0 {
		cfg.Limit = *limit
	}
	if *chromiumPath != "" {
		cfg.ChromiumPath = *chromiumPath
	}
	if *delayStr != "" {
		if parts := strings.SplitN(*delayStr, ",", 2); len(parts) == 2 {
			fmt.Sscanf(parts[0], "%f", &cfg.DelayMin)
			fmt.Sscanf(parts[1], "%f", &cfg.DelayMax)
		}
	}
	if cfg.DelayMax < cfg.DelayMin {
		cfg.DelayMax = cfg.DelayMin
	}

	// Graceful shutdown on Ctrl+C.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n  Прервано!")
		cancel()
	}()

	// Header.
	sep := strings.Repeat("═", 60)
	fmt.Println(sep)
	fmt.Println("  ВАЛИДАЦИЯ СЕРТИФИКАТОВ — ФГИС РОСАККРЕДИТАЦИИ")
	fmt.Println(sep)
	fmt.Printf("  Задержка: %.1f — %.1f сек\n", cfg.DelayMin, cfg.DelayMax)
	if !cfg.Filters.Empty() {
		fmt.Printf("  Фильтр:   активен\n")
	}
	fmt.Println(sep)

	start := time.Now()

	// Direct number test mode (--number).
	if *testNumber != "" {
		fmt.Printf("\n  Прямая проверка номера: %s\n", *testNumber)
		testDirectNumber(ctx, *testNumber, cfg.ChromiumPath)
		fmt.Printf("\n  Готово за %s\n", time.Since(start).Round(time.Second))
		return
	}

	fmt.Printf("  База: %s\n", cfg.DBPath)

	// Step 1: Load certificates from DB (read-only, with filter).
	fmt.Print("\n  Загрузка сертификатов из БД...")
	certs, err := loadCertificates(ctx, cfg.DBPath, cfg.Limit, cfg.Filters)
	if err != nil {
		log.Fatalf("Ошибка загрузки: %v", err)
	}
	fmt.Printf(" %d записей\n", len(certs))

	if len(certs) == 0 {
		fmt.Println("\n  Нет сертификатов для проверки (попробуй изменить фильтры).")
		return
	}

	// Step 2: Dry-run mode — show what would be checked.
	if *dryRun {
		printDryRun(certs)
		return
	}

	// Step 3: Validate certificates.
	var results []ValidationResult

	if *mock {
		results = mockValidate(certs)
	} else {
		results, err = liveValidate(ctx, certs, cfg.DelayMin, cfg.DelayMax, cfg.ChromiumPath)
		if err != nil {
			log.Fatalf("Ошибка валидации: %v", err)
		}
	}

	// Step 4: Print report.
	printReport(results)

	// Step 5: CSV export.
	if cfg.CSV != "" {
		fmt.Printf("\n  Экспорт CSV: %s...", cfg.CSV)
		if err := exportCSV(results, cfg.CSV); err != nil {
			log.Printf(" Ошибка: %v", err)
		} else {
			fmt.Println(" ok")
		}
	}

	// Step 6: XLSX export.
	if cfg.XLSX != "" {
		fmt.Printf("  Экспорт XLSX: %s...", cfg.XLSX)
		if err := exportXLSX(results, cfg.XLSX); err != nil {
			log.Printf(" Ошибка: %v", err)
		} else {
			fmt.Println(" ok")
		}
	}

	fmt.Printf("\n  Готово за %s\n", time.Since(start).Round(time.Second))
}

// liveValidate performs real FSA API validation for each unique certificate number.
func liveValidate(ctx context.Context, certs []CertRecord, delayMin, delayMax float64, chromiumPath string) ([]ValidationResult, error) {
	// Launch headless Chromium and capture Bearer token.
	fmt.Print("  Запуск headless Chromium для получения токена...")
	fsaClient, err := NewFSAClient(ctx, chromiumPath)
	if err != nil {
		return nil, fmt.Errorf("FSA client: %w", err)
	}
	defer fsaClient.Close()
	fmt.Println(" ok (токен получен)")

	// Deduplicate by certificate number — validate each unique number once.
	type job struct {
		number string
		certs  []CertRecord
		isDecl bool // true if any cert in this job is a declaration
		skip   bool // true if number format is not searchable in FSA
	}
	seen := make(map[string]*job)
	var jobs []*job
	for _, c := range certs {
		j, exists := seen[c.CertificateNumber]
		if !exists {
			j = &job{number: c.CertificateNumber}
			seen[c.CertificateNumber] = j
			jobs = append(jobs, j)
		}
		j.certs = append(j.certs, c)
		if isDeclaration(c.CertificateType) {
			j.isDecl = true
		}
		if !j.skip && !isSearchable(j.number) {
			j.skip = true
		}
	}

	// Count types for progress info.
	var nCerts, nDecls, nSkip int
	for _, j := range jobs {
		switch {
		case j.skip:
			nSkip++
		case j.isDecl:
			nDecls++
		default:
			nCerts++
		}
	}
	fmt.Printf("  Уникальных номеров: %d (серт: %d, декл: %d, пропуск: %d)\n\n",
		len(jobs), nCerts, nDecls, nSkip)

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	var results []ValidationResult

	for i, j := range jobs {
		if ctx.Err() != nil {
			fmt.Printf("\n  [%d/%d] Прервано пользователем.\n", i+1, len(jobs))
			break
		}

		// Skip non-searchable numbers (KG417, KZ, old formats).
		if j.skip {
			fmt.Printf("  [%d/%d] %s... ПРОПУСК (не-RU формат)\n", i+1, len(jobs), j.number)
			for _, c := range j.certs {
				results = append(results, ValidationResult{
					Article:           c.Article,
					GoodName:          c.GoodName,
					CertificateType:   c.CertificateType,
					CertificateNumber: c.CertificateNumber,
					LocalEnd:          c.LocalEnd,
					FSAStatus:         "Пропущен",
				})
			}
			continue
		}

		fmt.Printf("  [%d/%d] %s...", i+1, len(jobs), j.number)
		if j.isDecl {
			fmt.Printf(" [ДЕКЛ]")
		}

		// Route by known type: declarations use Chrome, certificates use API.
		var fsaResult *FSASearchResult
		var searchErr error
		if j.isDecl {
			fsaResult, searchErr = fsaClient.SearchDeclaration(ctx, j.number)
		} else {
			fsaResult, searchErr = fsaClient.SearchCertificate(ctx, j.number)
		}

		if searchErr != nil {
			fmt.Printf(" ОШИБКА: %v\n", searchErr)
			for _, c := range j.certs {
				results = append(results, ValidationResult{
					Article:           c.Article,
					GoodName:          c.GoodName,
					CertificateType:   c.CertificateType,
					CertificateNumber: c.CertificateNumber,
					LocalEnd:          c.LocalEnd,
					Error:             searchErr.Error(),
				})
			}
		} else {
			// Map FSA result to all articles sharing this certificate number.
			for _, c := range j.certs {
				vr := ValidationResult{
					Article:           c.Article,
					GoodName:          c.GoodName,
					CertificateType:   c.CertificateType,
					CertificateNumber: c.CertificateNumber,
					LocalEnd:          c.LocalEnd,
				}
				if fsaResult != nil {
					vr.Found = true
					vr.FSAStatus = statusName(fsaResult.StatusID)
					vr.FSAEndDate = fsaResult.EndDate
					vr.DateMatch = compareDates(c.LocalEnd, fsaResult.EndDate)
					vr.DaysRemaining = daysUntilExpiry(fsaResult.EndDate)
				} else {
					vr.FSAStatus = "Не найден"
				}
				results = append(results, vr)
			}

			if fsaResult != nil {
				fmt.Printf(" %s (до %s, %d дн.)\n", statusName(fsaResult.StatusID), fsaResult.EndDate, daysUntilExpiry(fsaResult.EndDate))
			} else {
				fmt.Println(" НЕ НАЙДЕН")
			}
		}

		// Random delay between requests (anti-detection).
		if i < len(jobs)-1 && ctx.Err() == nil {
			delay := delayMin + rng.Float64()*(delayMax-delayMin)
			time.Sleep(time.Duration(delay * float64(time.Second)))
		}
	}

	return results, nil
}

// isSearchable returns true if the certificate number is likely findable in Russian FSA registry.
// Returns false for: KG417 (Kyrgyzstan), KZ (Kazakhstan), old formats (digits only, 008и-...).
func isSearchable(number string) bool {
	// Russian certificates and declarations contain "RU".
	if strings.Contains(number, "RU") {
		return true
	}
	// Some formats start with "ТС N RU" or just "ЕАЭС" without "RU" visible.
	if strings.HasPrefix(number, "ЕАЭС") && !strings.Contains(number, "KG417") && !strings.Contains(number, "KZ") {
		return true
	}
	return false
}

// truncate shortens a string for display.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// mockValidate generates deterministic fake results for --mock mode.
func mockValidate(certs []CertRecord) []ValidationResult {
	fmt.Println("  Режим --mock: генерация тестовых результатов\n")
	results := make([]ValidationResult, len(certs))
	for i, c := range certs {
		r := ValidationResult{
			Article:           c.Article,
			GoodName:          c.GoodName,
			CertificateType:   c.CertificateType,
			CertificateNumber: c.CertificateNumber,
			LocalEnd:          c.LocalEnd,
			Found:             true,
			FSAStatus:         "Действующий",
			FSAEndDate:        "2027-12-31",
			DateMatch:         "—",
			DaysRemaining:     575,
		}
		// Vary results for realism: every 5th expired, every 7th not found.
		switch {
		case i%7 == 0:
			r.Found = false
			r.FSAStatus = "Не найден"
			r.FSAEndDate = ""
			r.DaysRemaining = 0
		case i%5 == 0:
			r.FSAStatus = "Архивный"
			r.FSAEndDate = "2025-01-15"
			r.DaysRemaining = -141
		}
		results[i] = r
	}
	return results
}

// isDeclaration returns true if the certificate type indicates a declaration.
func isDeclaration(certType string) bool {
	return strings.HasPrefix(certType, "Декларация")
}

// testDirectNumber validates a single certificate number without DB.
func testDirectNumber(ctx context.Context, number string, chromiumPath string) {
	fmt.Print("  Запуск headless Chromium для получения токена...")
	fsaClient, err := NewFSAClient(ctx, chromiumPath)
	if err != nil {
		log.Fatalf("FSA client: %v", err)
	}
	defer fsaClient.Close()
	fmt.Println(" ok (токен получен)")

	fmt.Printf("  Поиск сертификата: %s\n", number)
	cert, err := fsaClient.SearchCertificate(ctx, number)
	if err != nil {
		fmt.Printf("  Сертификат — ОШИБКА: %v\n", err)
	}
	if cert == nil {
		fmt.Printf("  Не найден как сертификат. Пробуем декларацию...\n")
		cert, err = fsaClient.SearchDeclaration(ctx, number)
		if err != nil {
			fmt.Printf("  Декларация — ОШИБКА: %v\n", err)
			return
		}
	}
	if cert == nil {
		fmt.Println("  НЕ НАЙДЕН ни как сертификат, ни как декларация")
		return
	}

	fmt.Println("\n  ┌─────────────────────────────────────────────")
	fmt.Printf("  │ ID:          %d\n", cert.ID)
	fmt.Printf("  │ Номер:       %s\n", cert.Number)
	fmt.Printf("  │ Рег. дата:   %s\n", cert.RegDate)
	fmt.Printf("  │ Действует до: %s\n", cert.EndDate)
	fmt.Printf("  │ Статус:      %s (id=%d)\n", statusName(cert.StatusID), cert.StatusID)
	fmt.Printf("  │ Тип:         %s\n", cert.CertType)
	fmt.Printf("  │ Дней до:     %d\n", daysUntilExpiry(cert.EndDate))
	fmt.Println("  └─────────────────────────────────────────────")
}
