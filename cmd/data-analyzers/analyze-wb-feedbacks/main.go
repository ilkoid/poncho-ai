// Package main provides a utility to analyze WB feedbacks quality using LLM.
//
// Reads feedbacks from SQLite (downloaded by download-wb-feedbacks),
// generates per-product quality summaries via two-level LLM aggregation,
// and stores results in a separate SQLite database.
//
// Usage:
//
//	OPENROUTER_API_KEY=sk-or-... go run . --days=30
//	go run . --days=7 --subject=Платья
//	go run . --days=30 --nm-ids-file=batch1.txt --force
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/models"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"gopkg.in/yaml.v3"
)

// Config represents the YAML configuration.
type Config struct {
	LLM      LLMConfig      `yaml:"llm"`
	Analysis AnalysisConfig  `yaml:"analysis"`
	Source   SourceConfig    `yaml:"source"`
	Results  ResultsConfig   `yaml:"results"`
}

type LLMConfig struct {
	Provider    string        `yaml:"provider"`
	APIKey      string        `yaml:"api_key"`
	BaseURL     string        `yaml:"base_url"`
	ChatModel   string        `yaml:"chat_model"`
	Temperature float64       `yaml:"temperature"`
	MaxTokens   int           `yaml:"max_tokens"`
	Timeout     time.Duration `yaml:"timeout"`
	Thinking    string        `yaml:"thinking"` // "enabled", "disabled" или пусто
}

type AnalysisConfig struct {
	BatchSize    int   `yaml:"batch_size"`
	MinFeedbacks int   `yaml:"min_feedbacks"`
	MaxFeedbacks int   `yaml:"max_feedbacks"`
	MaxProducts  int   `yaml:"max_products"`
	MinArticleLen int  `yaml:"min_article_len"`
	Seasons      []int `yaml:"season"` // include only these seasons (1st char of article)
	Years        []int `yaml:"year"`   // include only these years (chars 2-3 of article)
}

type SourceConfig struct {
	DbPath string `yaml:"db_path"`
}

type ResultsConfig struct {
	DbPath   string `yaml:"db_path"`
	LogsDir string `yaml:"logs_dir"`
}

// readFile reads a file — used by analyzer.go for prompts loading.
// Defined here for testability (can be overridden in tests).
var readFile = os.ReadFile

func main() {
	// CLI flags
	configPath := flag.String("config", "config.yaml", "Path to config file")
	days := flag.Int("days", 0, "Days from today (alternative to --from/--to)")
	dateFrom := flag.String("from", "", "Start date YYYY-MM-DD")
	dateTo := flag.String("to", "", "End date YYYY-MM-DD")
	subject := flag.String("subject", "", "Filter by WB category (subject_name)")
	nmIdsFile := flag.String("nm-ids-file", "", "Path to file with nmID list (one per line)")
	batchSize := flag.Int("batch-size", 0, "Override batch size from config")
	force := flag.Bool("force", false, "Force re-analysis of all products")
	dbPath := flag.String("db", "", "Override source DB path")
	help := flag.Bool("help", false, "Show help")
	flag.Parse()

	if *help {
		printHelp()
		return
	}

	// Load config
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	applyDefaults(cfg)

	// Apply CLI overrides
	if *days > 0 {
		// days overrides from/to calculation
	}
	if *batchSize > 0 {
		cfg.Analysis.BatchSize = *batchSize
	}
	if *dbPath != "" {
		cfg.Source.DbPath = *dbPath
	}

	// Calculate date range
	fromDate, toDate := resolveDateRange(*dateFrom, *dateTo, *days)

	// Load nmIDs from file if specified
	var nmIDs []int
	if *nmIdsFile != "" {
		nmIDs, err = loadNmIDs(*nmIdsFile)
		if err != nil {
			log.Fatalf("Failed to load nmIDs: %v", err)
		}
	}

	// Handle Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nInterrupted!")
		cancel()
	}()

	// Initialize LLM provider
	provider, err := createProvider(cfg)
	if err != nil {
		log.Fatalf("Failed to create LLM provider: %v", err)
	}

	// Load prompts
	prompts, err := LoadPrompts("prompts.yaml")
	if err != nil {
		log.Fatalf("Failed to load prompts: %v", err)
	}

	// Initialize schema (creates all tables including feedbacks/questions/quality)
	schemaRepo, err := sqlite.NewSQLiteSalesRepository(cfg.Source.DbPath)
	if err != nil {
		log.Fatalf("Failed to initialize schema: %v", err)
	}
	schemaRepo.Close()

	// Open databases
	source, err := NewSourceRepo(cfg.Source.DbPath)
	if err != nil {
		log.Fatalf("Failed to open source DB: %v", err)
	}
	defer source.Close()

	results, err := NewResultsRepo(cfg.Results.DbPath)
	if err != nil {
		log.Fatalf("Failed to open results DB: %v", err)
	}
	defer results.Close()

	// Create analyzer with optional logger
	logger, err := NewLogger(cfg.Results.LogsDir)
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}
	if logger != nil {
		logger.SetConfig(LogConfig{
			Period:    fmt.Sprintf("%s -> %s", fromDate, toDate),
			Model:     cfg.LLM.ChatModel,
			Thinking:  cfg.LLM.Thinking,
			BatchSize: cfg.Analysis.BatchSize,
			Subject:   *subject,
			NmIDsFile: *nmIdsFile,
		})
	}

	analyzer := NewAnalyzer(provider, prompts, cfg.Analysis.BatchSize, cfg.LLM.ChatModel, logger)

	// Query products
	queryParams := QueryParams{
		DateFrom:      fromDate,
		DateTo:        toDate,
		Subject:       *subject,
		NmIDs:         nmIDs,
		MinArticleLen: cfg.Analysis.MinArticleLen,
		Seasons:       cfg.Analysis.Seasons,
		Years:         cfg.Analysis.Years,
	}
	products, err := source.ListProducts(ctx, queryParams)
	if err != nil {
		log.Fatalf("Failed to list products: %v", err)
	}

	if len(products) == 0 {
		fmt.Println("No products found for the given filters.")
		return
	}

	// Print header
	maxProducts := cfg.Analysis.MaxProducts
	printHeader(fromDate, toDate, *subject, len(nmIDs), len(products), maxProducts)

	// Show log file path for tail -f
	if logger != nil {
		fmt.Printf("Log file:   %s\n", logger.FilePath())
	}

	// Process products
	var stats ProcessStats
	for _, product := range products {
		// Stop if we've analyzed enough (max_products limits analyzed, not selected)
		if cfg.Analysis.MaxProducts > 0 && stats.Analyzed >= cfg.Analysis.MaxProducts {
			break
		}

		if err := ctx.Err(); err != nil {
			break
		}

		// Incremental check
		shouldAnalyze, err := checkIncremental(ctx, results, source, product, fromDate, toDate, *force)
		if err != nil {
			log.Printf("WARN: incremental check nm_id=%d: %v", product.ProductNmID, err)
		}
		if !shouldAnalyze {
			stats.Skipped++
			continue
		}

		// Get feedbacks for this product
		feedbacks, err := source.GetFeedbacks(ctx, product.ProductNmID, fromDate, toDate, cfg.Analysis.MaxFeedbacks)
		if err != nil {
			log.Printf("ERROR: get feedbacks nm_id=%d: %v", product.ProductNmID, err)
			stats.Errors++
			continue
		}

		// Analyze
		limit := len(products)
		if maxProducts > 0 {
			limit = maxProducts
		}
		fmt.Printf("  [%d/%d] Analyzing nm_id=%d (%d feedbacks)...",
			stats.Analyzed+stats.Errors+1, limit, product.ProductNmID, len(feedbacks))

		start := time.Now()
		summary, err := analyzer.AnalyzeProduct(ctx, product, feedbacks)
		elapsed := time.Since(start)
		if err != nil {
			log.Printf("ERROR: analyze nm_id=%d: %v", product.ProductNmID, err)
			stats.Errors++
			fmt.Printf(" FAILED (%.1fs)\n", elapsed.Seconds())
			continue
		}

		// Get actual feedback date range for this product
		actualFrom, actualTo, _ := source.GetFeedbackDateRange(ctx, product.ProductNmID, fromDate, toDate)

		// Merge periods for re-analysis
		mergedReqFrom, mergedReqTo := fromDate, toDate
		mergedActualFrom, mergedActualTo := actualFrom, actualTo
		if existing, _ := results.GetSummary(ctx, product.ProductNmID); existing != nil {
			mergedReqFrom = minDate(existing.RequestFrom, fromDate)
			mergedReqTo = maxDate(existing.RequestTo, toDate)
			if actualFrom != "" {
				mergedActualFrom = minDate(existing.AnalyzedFrom, actualFrom)
			}
			if actualTo != "" {
				mergedActualTo = maxDate(existing.AnalyzedTo, actualTo)
			}
		}

		// Save result
		err = results.SaveSummary(ctx, ProductSummary{
			ProductNmID:    product.ProductNmID,
			SupplierArticle: product.SupplierArticle,
			ProductName:    product.ProductName,
			AvgRating:      product.AvgRating,
			FeedbackCount:  product.FeedbackCount,
			QualitySummary: summary,
			RequestFrom:    mergedReqFrom,
			RequestTo:      mergedReqTo,
			AnalyzedFrom:   mergedActualFrom,
			AnalyzedTo:     mergedActualTo,
			AnalyzedAt:     time.Now().Format(time.RFC3339),
			ModelUsed:      cfg.LLM.ChatModel,
		})
		if err != nil {
			log.Printf("ERROR: save summary nm_id=%d: %v", product.ProductNmID, err)
			stats.Errors++
			continue
		}

		stats.Analyzed++
		fmt.Printf(" OK (%.1fs)\n", elapsed.Seconds())
	}

	// Print results table (use fresh context — original may be cancelled)
	fmt.Println()
	printResultsTable(context.Background(), results)

	// Print summary
	fmt.Println()
	printStats(stats, len(products))

	// Save JSON log (logger.Finalize does not use context)
	if logger != nil {
		logPath, err := logger.Finalize(LogSummary{
			TotalProducts: len(products),
			Analyzed:     stats.Analyzed,
			Skipped:      stats.Skipped,
			Errors:       stats.Errors,
			TotalLLMCalls: stats.Analyzed + stats.Errors, // approximate
		})
		if err != nil {
			log.Printf("WARN: failed to save log: %v", err)
		} else {
			fmt.Printf("Log saved: %s\n", logPath)
		}
	}
}

// ============================================================================
// Incremental analysis logic
// ============================================================================

// checkIncremental determines if a product needs (re-)analysis.
func checkIncremental(ctx context.Context, results *ResultsRepo, source *SourceRepo,
	product ProductStats, requestFrom, requestTo string, force bool) (bool, error) {

	if force {
		return true, nil
	}

	existing, err := results.GetSummary(ctx, product.ProductNmID)
	if err != nil {
		return true, nil // on error, analyze to be safe
	}
	if existing == nil {
		return true, nil // first run for this product
	}

	// Check if request_period is within stored request_period
	if requestFrom >= existing.RequestFrom && requestTo <= existing.RequestTo {
		// Same request period → SKIP (batch splitting scenario)
		return false, nil
	}

	// Request period extends beyond stored → check for new feedbacks
	actualFrom, actualTo, err := source.GetFeedbackDateRange(ctx, product.ProductNmID, requestFrom, requestTo)
	if err != nil {
		return true, nil
	}

	// Check if there are feedbacks outside the previously analyzed range
	if actualFrom != "" && actualFrom < existing.AnalyzedFrom {
		return true, nil // new earlier feedbacks
	}
	if actualTo != "" && actualTo > existing.AnalyzedTo {
		return true, nil // new later feedbacks
	}

	return false, nil // no new data
}

// ============================================================================
// ProcessStats tracks analysis results
// ============================================================================

type ProcessStats struct {
	Analyzed int
	Skipped  int
	Errors   int
}

// ============================================================================
// Helpers
// ============================================================================

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Expand ${VAR} environment variables before parsing YAML
	expanded := os.ExpandEnv(string(data))
	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Analysis.BatchSize <= 0 {
		cfg.Analysis.BatchSize = 20
	}
	if cfg.Analysis.MinFeedbacks <= 0 {
		cfg.Analysis.MinFeedbacks = 1
	}
	if cfg.Analysis.MaxFeedbacks <= 0 {
		cfg.Analysis.MaxFeedbacks = 500
	}
	if cfg.LLM.Timeout <= 0 {
		cfg.LLM.Timeout = 120 * time.Second
	}
	if cfg.Results.DbPath == "" {
		cfg.Results.DbPath = "./quality_reports.db"
	}
}

func createProvider(cfg *Config) (llm.Provider, error) {
	modelDef := config.ModelDef{
		Provider:    cfg.LLM.Provider,
		ModelName:   cfg.LLM.ChatModel,
		APIKey:      cfg.LLM.APIKey,
		BaseURL:     cfg.LLM.BaseURL,
		MaxTokens:   cfg.LLM.MaxTokens,
		Temperature: cfg.LLM.Temperature,
		Timeout:     cfg.LLM.Timeout,
		Thinking:    cfg.LLM.Thinking,
	}
	return models.CreateProvider(modelDef)
}

func resolveDateRange(from, to string, days int) (string, string) {
	if from != "" && to != "" {
		return from, to
	}
	if days <= 0 {
		days = 30
	}
	now := time.Now()
	end := now.Format("2006-01-02")
	begin := now.AddDate(0, 0, -days).Format("2006-01-02")
	return begin, end
}

func loadNmIDs(path string) ([]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var ids []int
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var id int
		if _, err := fmt.Sscanf(line, "%d", &id); err != nil {
			return nil, fmt.Errorf("invalid nmID '%s': %w", line, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func minDate(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	if a < b {
		return a
	}
	return b
}

func maxDate(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	if a > b {
		return a
	}
	return b
}

// ============================================================================
// Output formatting
// ============================================================================

func printHelp() {
	fmt.Print(`WB Feedbacks Quality Analyzer - Generate LLM quality summaries from feedbacks

Usage:
  go run . [options]

Options:
  --config PATH       Config file path (default: config.yaml)
  --days N             Days from today (default: 30)
  --from DATE          Start date YYYY-MM-DD
  --to DATE            End date YYYY-MM-DD
  --subject NAME       Filter by WB category (subject_name)
  --nm-ids-file PATH   File with nmID list (one per line)
  --batch-size N       Override batch size from config
  --force              Force re-analysis of all products
  --db PATH            Override source DB path
  --help               Show this help

Examples:
  # Analyze last 30 days
  OPENROUTER_API_KEY=xxx go run . --days=30

  # Specific category
  go run . --days=30 --subject=Платья

  # Specific products from file
  go run . --days=30 --nm-ids-file=batch1.txt

  # Force re-analysis
  go run . --days=30 --force

`)
}

func printHeader(from, to, subject string, nmIDsCount, productCount, maxProducts int) {
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println("WB Feedbacks Quality Analyzer")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("Period:      %s -> %s\n", from, to)
	if subject != "" {
		fmt.Printf("Category:    %s\n", subject)
	}
	if nmIDsCount > 0 {
		fmt.Printf("nmID filter: %d IDs from file\n", nmIDsCount)
	}
	if maxProducts > 0 {
		fmt.Printf("Products:    %d (analyzing up to %d)\n", productCount, maxProducts)
	} else {
		fmt.Printf("Products:    %d\n", productCount)
	}
	fmt.Println(strings.Repeat("=", 80))
}

func printResultsTable(ctx context.Context, results *ResultsRepo) {
	summaries, err := results.GetAllSummaries(ctx)
	if err != nil {
		log.Printf("WARN: failed to load results for table: %v", err)
		return
	}

	if len(summaries) == 0 {
		fmt.Println("No results to display.")
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "nmID\tАртикул\tРейтинг\tОтзывов\tРезюме")
	fmt.Fprintln(tw, strings.Repeat("-", 120))

	for _, s := range summaries {
		summary := truncate(s.QualitySummary, 80)
		if s.QualitySummary != "" {
			// Replace newlines with spaces for table display
			summary = strings.ReplaceAll(summary, "\n", " ")
		}
		fmt.Fprintf(tw, "%d\t%s\t%.1f\t%d\t%s\n",
			s.ProductNmID, s.SupplierArticle, s.AvgRating, s.FeedbackCount, summary)
	}
	tw.Flush()
}

func printStats(stats ProcessStats, total int) {
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println("Summary:")
	fmt.Printf("  Analyzed:  %d\n", stats.Analyzed)
	fmt.Printf("  Skipped:   %d (already processed)\n", stats.Skipped)
	fmt.Printf("  Errors:    %d\n", stats.Errors)
	fmt.Printf("  Total:     %d products\n", total)
	fmt.Println(strings.Repeat("=", 80))
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
