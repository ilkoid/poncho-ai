// Package main provides a utility to download WB Feedbacks API data to SQLite.
//
// Usage:
//
//	WB_API_FEEDBACK_KEY=your_key go run . --days=7
//	go run . --mock --days=7    # Mock mode
//	go run . --help             # Show help
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

	_ "github.com/mattn/go-sqlite3" // SQLite driver
	"gopkg.in/yaml.v3"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config represents the YAML configuration.
type Config struct {
	WB        config.WBClientConfig `yaml:"wb"`
	Feedbacks config.FeedbacksConfig `yaml:"feedbacks"`
}

func main() {
	// Flags
	configPath := flag.String("config", "config.yaml", "Path to config file")
	mock := flag.Bool("mock", false, "Use mock client (no API calls)")
	clean := flag.Bool("clean", false, "Delete database before download")
	begin := flag.String("begin", "", "Begin date (YYYY-MM-DD)")
	end := flag.String("end", "", "End date (YYYY-MM-DD)")
	days := flag.Int("days", 0, "Days from today (alternative to begin/end)")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	help := flag.Bool("help", false, "Show help")
	flag.Parse()

	if *help {
		printHelp()
		return
	}

	// Load config
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Printf("Config not found, using defaults: %v", err)
		cfg = defaultConfig()
	}

	// Apply defaults
	cfg.Feedbacks = cfg.Feedbacks.GetDefaults()
	cfg.WB = cfg.WB.GetDefaults()

	// Apply CLI overrides
	if *begin != "" {
		cfg.Feedbacks.Begin = *begin
	}
	if *end != "" {
		cfg.Feedbacks.End = *end
	}
	if *days > 0 {
		cfg.Feedbacks.Days = *days
	}
	if *dbPath != "" {
		cfg.Feedbacks.DbPath = *dbPath
	}

	// Calculate date range
	beginDate, endDate := calculateDateRange(cfg)

	// Print header
	printHeader(cfg, beginDate, endDate, *mock)

	// Handle Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nInterrupted!")
		cancel()
	}()

	// Delete database if --clean
	if *clean {
		if err := os.Remove(cfg.Feedbacks.DbPath); err != nil && !os.IsNotExist(err) {
			log.Fatalf("Failed to delete database: %v", err)
		}
		fmt.Println("Database deleted")
	}

	// Initialize schema (creates all tables including feedbacks/questions)
	schemaRepo, err := sqlite.NewSQLiteSalesRepository(cfg.Feedbacks.DbPath)
	if err != nil {
		log.Fatalf("Failed to initialize schema: %v", err)
	}
	schemaRepo.Close()

	// Create repository (tables already exist, only sets PRAGMAs)
	repo, err := NewFeedbacksRepo(cfg.Feedbacks.DbPath)
	if err != nil {
		log.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Convert dates to Unix timestamps
	dateFrom, err := DateToTimestamp(beginDate, false)
	if err != nil {
		log.Fatalf("Invalid begin date: %v", err)
	}
	dateTo, err := DateToTimestamp(endDate, true)
	if err != nil {
		log.Fatalf("Invalid end date: %v", err)
	}

	// Rate limit from config
	rateLimit := cfg.WB.RateLimit
	burst := cfg.WB.BurstLimit

	var summary DownloadSummary

	// Download feedbacks
	if cfg.Feedbacks.Feedbacks {
		fmt.Println("\nDownloading feedbacks...")
		if *mock {
			summary.FeedbacksRows = mockDownload(ctx, repo, "feedbacks")
		} else {
			apiKey := getAPIKey(cfg)
			if apiKey == "" {
				log.Fatal("No API key. Set WB_API_FEEDBACK_KEY or WB_API_KEY")
			}
			client := wb.New(apiKey)
			count, err := DownloadFeedbacks(ctx, client, repo, dateFrom, dateTo, rateLimit, burst)
			if err != nil {
				log.Fatalf("Failed to download feedbacks: %v", err)
			}
			summary.FeedbacksRows = count
		}
		fmt.Printf("Saved %d feedbacks\n", summary.FeedbacksRows)
	}

	// Download questions
	if cfg.Feedbacks.Questions {
		fmt.Println("\nDownloading questions...")
		if *mock {
			summary.QuestionsRows = mockDownload(ctx, repo, "questions")
		} else {
			apiKey := getAPIKey(cfg)
			if apiKey == "" {
				log.Fatal("No API key. Set WB_API_FEEDBACK_KEY or WB_API_KEY")
			}
			client := wb.New(apiKey)
			count, err := DownloadQuestions(ctx, client, repo, dateFrom, dateTo, rateLimit, burst)
			if err != nil {
				log.Fatalf("Failed to download questions: %v", err)
			}
			summary.QuestionsRows = count
		}
		fmt.Printf("Saved %d questions\n", summary.QuestionsRows)
	}

	// Verify counts
	fmt.Println("\nVerifying...")
	fbCount, _ := repo.CountFeedbacks(ctx)
	fbAnswered, _ := repo.CountFeedbacksWithAnswer(ctx)
	qCount, _ := repo.CountQuestions(ctx)
	qAnswered, _ := repo.CountQuestionsWithAnswer(ctx)

	// Summary
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("Download complete!")
	fmt.Printf("  Feedbacks: %d total, %d with answer (%.1f%%)\n",
		fbCount, fbAnswered, pct(fbAnswered, fbCount))
	fmt.Printf("  Questions: %d total, %d with answer (%.1f%%)\n",
		qCount, qAnswered, pct(qAnswered, qCount))
	fmt.Printf("  Database:  %s\n", cfg.Feedbacks.DbPath)
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func defaultConfig() *Config {
	cfg := &Config{}
	cfg.WB.RateLimit = 180 // 3 req/sec * 60
	cfg.WB.BurstLimit = 6
	cfg.WB.Timeout = "30s"
	cfg.Feedbacks.DbPath = "feedbacks.db"
	cfg.Feedbacks.Days = 7
	return cfg
}

func calculateDateRange(cfg *Config) (string, string) {
	if cfg.Feedbacks.Begin != "" && cfg.Feedbacks.End != "" {
		return cfg.Feedbacks.Begin, cfg.Feedbacks.End
	}

	days := cfg.Feedbacks.Days
	if days == 0 {
		days = 7
	}

	now := time.Now()
	// --days 7 = last 7 days excluding today
	end := now.AddDate(0, 0, -1).Format("2006-01-02")
	begin := now.AddDate(0, 0, -days).Format("2006-01-02")
	return begin, end
}

func getAPIKey(cfg *Config) string {
	// Priority: env > config
	if key := os.Getenv("WB_API_FEEDBACK_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("WB_API_KEY"); key != "" {
		return key
	}
	return cfg.WB.APIKey
}

func printHelp() {
	fmt.Print(`WB Feedbacks Downloader - Download feedbacks and questions from WB API

Usage:
  go run . [options]

Options:
  --config PATH     Config file path (default: config.yaml)
  --mock            Use mock client (no API calls)
  --clean           Delete database before download
  --begin DATE      Begin date (YYYY-MM-DD)
  --end DATE        End date (YYYY-MM-DD)
  --days N          Days from today (alternative to begin/end)
  --db PATH         Database path (overrides config)
  --help            Show this help

Examples:
  # Download last 7 days
  WB_API_FEEDBACK_KEY=xxx go run . --days=7

  # Specific period
  WB_API_FEEDBACK_KEY=xxx go run . --begin=2025-01-01 --end=2025-01-31

  # Mock mode for testing
  go run . --mock --days=7

  # Clean + custom DB
  go run . --clean --days=30 --db=archive.db

`)
}

func printHeader(cfg *Config, beginDate, endDate string, mock bool) {
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("WB Feedbacks Downloader")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Config:     %s\n", cfg.Feedbacks.DbPath)
	fmt.Printf("Period:     %s -> %s\n", beginDate, endDate)

	if !cfg.Feedbacks.Feedbacks {
		fmt.Println("Feedbacks:  disabled")
	}
	if !cfg.Feedbacks.Questions {
		fmt.Println("Questions:  disabled")
	}
	if mock {
		fmt.Println("Mode:       Mock")
	}

	fmt.Println(strings.Repeat("=", 60))
}

// ============================================================================
// Mock data generation
// ============================================================================

func mockDownload(ctx context.Context, repo *FeedbacksRepo, kind string) int {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	count := rng.Intn(50) + 10

	if kind == "feedbacks" {
		items := make([]FeedbackFull, count)
		for i := range items {
			items[i] = generateMockFeedback(rng, i)
		}
		n, _ := repo.SaveFeedbacks(ctx, items)
		return n
	}

	items := make([]QuestionFull, count)
	for i := range items {
		items[i] = generateMockQuestion(rng, i)
	}
	n, _ := repo.SaveQuestions(ctx, items)
	return n
}

func generateMockFeedback(rng *rand.Rand, i int) FeedbackFull {
	f := FeedbackFull{
		ID:             fmt.Sprintf("mock-feedback-%d", i),
		Text:           fmt.Sprintf("Mock feedback text %d", i),
		Pros:           "Good quality",
		Cons:           "None",
		ProductValuation: rng.Intn(5) + 1,
		CreatedDate:   time.Now().Add(-time.Duration(rng.Intn(168)) * time.Hour).Format(time.RFC3339),
		State:          "wbRu",
		UserName:      fmt.Sprintf("User%d", i),
		WasViewed:     rng.Intn(2) == 0,
		OrderStatus:   "buyout",
		MatchingSize:  "ok",
		Color:         "colorless",
		SubjectId:     rng.Intn(1000) + 1,
		SubjectName:   "Test Subject",
		ProductName:   fmt.Sprintf("Product %d", i),
		Size:          "M",
		ProductNmId:   rng.Intn(100000000) + 100000,
		ProductImtId:  rng.Intn(1000000000) + 10000000,
	}
	if rng.Intn(3) == 0 {
		s := "Answer text"
		f.AnswerText = &s
		st := "wbRu"
		f.AnswerState = &st
	}
	return f
}

func generateMockQuestion(rng *rand.Rand, i int) QuestionFull {
	q := QuestionFull{
		ID:           fmt.Sprintf("mock-question-%d", i),
		Text:         fmt.Sprintf("Mock question text %d?", i),
		CreatedDate:  time.Now().Add(-time.Duration(rng.Intn(168)) * time.Hour).Format(time.RFC3339),
		State:        "suppliersPortalSynch",
		WasViewed:    rng.Intn(2) == 0,
		IsWarned:     false,
		ProductName:  fmt.Sprintf("Product %d", i),
		SupplierArticle: fmt.Sprintf("ART-%d", i),
		SupplierName: "Test Supplier",
		BrandName:    "Test Brand",
		ProductNmId:  rng.Intn(100000000) + 100000,
		ProductImtId: rng.Intn(1000000000) + 10000000,
	}
	if rng.Intn(2) == 0 {
		s := "Answer text"
		q.AnswerText = &s
	}
	return q
}

func pct(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}
