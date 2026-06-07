// download-wb-feedbacks-v2 downloads feedbacks and questions from WB Feedbacks API.
//
// V2 architecture: business logic in pkg/feedbacks/, this is a thin CLI driver.
// Supports both SQLite and PostgreSQL backends via config.
//
// ⚠️ Mock safety: --mock mode uses DiscardWriter — ZERO database interaction.
//
// Usage:
//
//	go run . --mock                                               # mock mode, no DB
//	go run . --mock --db /tmp/test-feedbacks.db                   # mock + test SQLite
//	go run . --mock --backend postgres --pg-database wb_data_test # mock + test PG
//	go run . --dry-run --db /tmp/test-feedbacks.db                # real API, no writes
//	go run . --config config.yaml                                 # production (user only!)
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/feedbacks"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Config holds YAML configuration for the feedbacks v2 downloader.
type Config struct {
	WB        config.WBClientConfig  `yaml:"wb"`
	Feedbacks config.FeedbacksConfig `yaml:"feedbacks"`
	Storage   config.V2StorageConfig `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	dbPath := flag.String("db", "", "Database path (overrides config)")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	begin := flag.String("begin", "", "Begin date YYYY-MM-DD (overrides config)")
	end := flag.String("end", "", "End date YYYY-MM-DD (overrides config)")
	days := flag.Int("days", 0, "Days from today (alternative to begin/end)")
	feedbacksOnly := flag.Bool("feedbacks-only", false, "Download only feedbacks (skip questions)")
	questionsOnly := flag.Bool("questions-only", false, "Download only questions (skip feedbacks)")
	mockMode := flag.Bool("mock", false, "Use mock source (no API calls, no DB writes)")
	dryRun := flag.Bool("dry-run", false, "Show what would be saved without writing to DB")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	cfg.Feedbacks = cfg.Feedbacks.GetDefaults()
	cfg.WB = cfg.WB.GetDefaults()

	// CLI overrides
	if *backend != "" {
		cfg.Storage.Backend = *backend
	}
	if *dbPath != "" {
		cfg.Storage.DbPath = *dbPath
	}
	if *pgDatabase != "" {
		cfg.Storage.PgDatabase = *pgDatabase
	}
	if *begin != "" {
		cfg.Feedbacks.Begin = *begin
	}
	if *end != "" {
		cfg.Feedbacks.End = *end
	}
	if *days > 0 {
		cfg.Feedbacks.Days = *days
	}
	cfg.Storage = cfg.Storage.GetDefaults()
	// Fallback: if storage.db_path not set, use feedbacks.db_path
	if cfg.Storage.DbPath == "" {
		cfg.Storage.DbPath = cfg.Feedbacks.DbPath
	}

	// Resolve entity toggles
	downloadFeedbacks := cfg.Feedbacks.Feedbacks && !*questionsOnly
	downloadQuestions := cfg.Feedbacks.Questions && !*feedbacksOnly

	rl := cfg.Feedbacks.GetDefaults().RateLimits

	dllog.PrintHeader("WB Feedbacks Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "DB", Value: cfg.Storage.DisplayDB()},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
		dllog.HeaderField{Key: "Feedbacks", Value: fmt.Sprintf("%v", downloadFeedbacks)},
		dllog.HeaderField{Key: "Questions", Value: fmt.Sprintf("%v", downloadQuestions)},
	)

	// ⚠️ Mock safety — writer creation goes INSIDE the else branch.
	var writer feedbacks.Writer
	var cleanup func()

	if *mockMode {
		writer = feedbacks.NewDiscardWriter()
		cleanup = func() {}
	} else {
		var err error
		writer, cleanup, err = createFeedbacksWriter(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	// Create source (real API or mock)
	var source feedbacks.Source
	if *mockMode {
		mockSrc := feedbacks.NewMockSource()
		mockSrc.PopulateFeedbacks(100)
		mockSrc.PopulateQuestions(50)
		source = mockSrc
	} else {
		apiKey := resolveAPIKey(cfg)
		if apiKey == "" {
			log.Fatal("No API key. Set WB_API_FEEDBACK_KEY or WB_API_KEY")
		}
		wbClient := wb.New(apiKey)

		// Two separate rate limiters for two endpoints
		wbClient.SetRateLimit(feedbacks.ToolIDFeedbacks,
			rl.DownloadFeedbacks, rl.DownloadFeedbacksBurst,
			rl.DownloadFeedbacksApi, rl.DownloadFeedbacksApiBurst)
		wbClient.SetRateLimit(feedbacks.ToolIDQuestions,
			rl.DownloadQuestions, rl.DownloadQuestionsBurst,
			rl.DownloadQuestionsApi, rl.DownloadQuestionsApiBurst)
		wbClient.SetAdaptiveParams(0,
			cfg.Feedbacks.GetDefaults().AdaptiveProbeAfter,
			cfg.Feedbacks.GetDefaults().MaxBackoffSeconds)

		source = feedbacks.NewWBSource(wbClient)
	}

	opts := feedbacks.DownloadOptions{
		Feedbacks:      downloadFeedbacks,
		Questions:      downloadQuestions,
		DateFrom:       cfg.Feedbacks.Begin,
		DateTo:         cfg.Feedbacks.End,
		Days:           cfg.Feedbacks.Days,
		DryRun:         *dryRun,
		FeedbacksRate:  rl.DownloadFeedbacks,
		FeedbacksBurst: rl.DownloadFeedbacksBurst,
		QuestionsRate:  rl.DownloadQuestions,
		QuestionsBurst: rl.DownloadQuestionsBurst,
		OnProgress: func() func(string) {
			start := time.Now()
			return func(msg string) {
				dllog.Progress(0, 0, "feedbacks", msg, start)
			}
		}(),
	}

	dl := feedbacks.NewDownloader(source, writer, opts)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download failed: %v", err)
	}

	dllog.Done(result.Duration, "feedbacks: %d (%d pages), questions: %d (%d pages)",
		result.FeedbacksSaved, result.FeedbacksPages,
		result.QuestionsSaved, result.QuestionsPages)
}

// createFeedbacksWriter creates the appropriate Writer based on backend config.
func createFeedbacksWriter(ctx context.Context, cfg config.V2StorageConfig) (feedbacks.Writer, func(), error) {
	switch cfg.Backend {
	case "postgres", "postgresql":
		dsn, err := cfg.GetEffectiveDSN()
		if err != nil {
			return nil, func() {}, fmt.Errorf("postgres DSN: %w", err)
		}

		pool, err := postgres.NewPool(ctx, dsn)
		if err != nil {
			return nil, func() {}, fmt.Errorf("postgres pool: %w", err)
		}

		repo := postgres.NewPgFeedbacksRepo(pool.DB())
		if err := repo.InitSchema(ctx); err != nil {
			pool.Close()
			return nil, func() {}, fmt.Errorf("postgres schema: %w", err)
		}
		return repo, pool.Close, nil

	default: // "sqlite"
		repo, err := sqlite.NewSQLiteSalesRepository(cfg.DbPath)
		if err != nil {
			return nil, func() {}, fmt.Errorf("open SQLite: %w", err)
		}
		return repo, func() { repo.Close() }, nil
	}
}

// resolveAPIKey resolves the WB Feedbacks API key from config.
// Priority: WB_API_FEEDBACK_KEY > WB_API_KEY > config value.
func resolveAPIKey(cfg *Config) string {
	if key := os.Getenv("WB_API_FEEDBACK_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("WB_API_KEY"); key != "" {
		return key
	}
	return cfg.WB.APIKey
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
