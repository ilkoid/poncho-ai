// Package main provides a utility to download WB Stock History CSV data to SQLite.
//
// Usage:
//
//	WB_API_KEY=your_key go run . --days=7
//	go run . --help             # Show help
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

	_ "github.com/mattn/go-sqlite3" // SQLite driver
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/utils"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func main() {
	// Flags
	configPath := flag.String("config", "config.yaml", "Path to config file")
	clean := flag.Bool("clean", false, "Delete database before download")
	begin := flag.String("begin", "", "Begin date (YYYY-MM-DD)")
	end := flag.String("end", "", "End date (YYYY-MM-DD)")
	days := flag.Int("days", 0, "Days from today (alternative to begin/end)")
	reportType := flag.String("report-type", "", "Report type: metrics or daily")
	stockType := flag.String("stock-type", "", "Stock type: '', wb, mp")
	resume := flag.Bool("resume", false, "Resume mode: continue from last date")
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
		dllog.Log("config not found, using defaults: %v", err)
		cfg = defaultConfig()
	}

	// Apply defaults
	cfg.StockHistory = cfg.StockHistory.GetDefaults()
	cfg.WB = cfg.WB.GetDefaults()

	// Apply CLI overrides
	if *begin != "" {
		cfg.StockHistory.Begin = *begin
	}
	if *end != "" {
		cfg.StockHistory.End = *end
	}
	if *days > 0 {
		cfg.StockHistory.Days = *days
	}
	if *reportType != "" {
		cfg.StockHistory.ReportType = *reportType
	}
	if *stockType != "" {
		cfg.StockHistory.StockType = *stockType
	}
	if *resume {
		cfg.StockHistory.Resume = true
	}
	if *dbPath != "" {
		cfg.StockHistory.DbPath = *dbPath
	}

	// Calculate date range
	beginDate, endDate := calculateDateRange(cfg)

	// Print header
	{
		fields := []dllog.HeaderField{
			{Key: "Config", Value: cfg.StockHistory.DbPath},
			{Key: "Period", Value: fmt.Sprintf("%s → %s", beginDate, endDate)},
			{Key: "Type", Value: cfg.StockHistory.ReportType},
		}
		if cfg.StockHistory.StockType != "" {
			fields = append(fields, dllog.HeaderField{Key: "Stock Type", Value: cfg.StockHistory.StockType})
		}
		if cfg.StockHistory.Resume {
			fields = append(fields, dllog.HeaderField{Key: "Resume", Value: "✓"})
		}
			dllog.PrintHeader("WB Stock History CSV Downloader", fields...)
	}

	// Handle Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		dllog.Error("interrupted!")
		cancel()
	}()

	// Delete database if --clean
	if *clean {
		if err := os.Remove(cfg.StockHistory.DbPath); err != nil && !os.IsNotExist(err) {
			log.Fatalf("failed to delete database: %v", err)
		}
		dllog.Log("database deleted")
	}

	// Create repository
	repo, err := sqlite.NewSQLiteSalesRepository(cfg.StockHistory.DbPath)
	if err != nil {
		log.Fatalf("failed to create repository: %v", err)
	}
	defer repo.Close()

	// Create client
	var wbClient *wb.Client
	apiKey := getAPIKey(cfg)
	if apiKey == "" {
		log.Fatal("no API key. Set WB_API_KEY")
	}
	wbClient = wb.New(apiKey)
	dllog.Log("API Key: %s", utils.MaskAPIKey(apiKey))

	// Download stock history
	t0 := time.Now()
	dllog.Log("downloading stock history (%s)...", cfg.StockHistory.ReportType)
	result, err := DownloadStockHistory(ctx, wbClient, repo, cfg.StockHistory, beginDate, endDate)
	if err != nil {
		dllog.Error("failed to download: %v", err)
		os.Exit(1)
	}

	// Summary
	dllog.Done(time.Since(t0), "report=%s rows=%d period=%s→%s db=%s",
		result.ReportID, result.RowsCount, beginDate, endDate, cfg.StockHistory.DbPath)
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func defaultConfig() *Config {
	cfg := &Config{}
	cfg.WB.RateLimit = 3
	cfg.WB.BurstLimit = 3
	cfg.WB.Timeout = "60s"
	cfg.StockHistory.DbPath = "stock_history.db"
	cfg.StockHistory.Days = 30
	cfg.StockHistory.ReportType = "daily"
	return cfg
}

func calculateDateRange(cfg *Config) (string, string) {
	if cfg.StockHistory.Begin != "" && cfg.StockHistory.End != "" {
		return cfg.StockHistory.Begin, cfg.StockHistory.End
	}

	days := cfg.StockHistory.Days
	if days == 0 {
		days = 30
	}

	now := time.Now()
	end := now.AddDate(0, 0, -1).Format("2006-01-02") // Exclude today (incomplete)
	begin := now.AddDate(0, 0, -days).Format("2006-01-02")
	return begin, end
}

func getAPIKey(cfg *Config) string {
	if key := os.Getenv("WB_API_KEY"); key != "" {
		return key
	}
	return cfg.WB.APIKey
}

func printHelp() {
	fmt.Print(`WB Stock History CSV Downloader - Download historical stock data from WB API

Usage:
  go run . [options]

Options:
  --config PATH     Config file path (default: config.yaml)
  --clean           Delete database before download
  --begin DATE      Begin date (YYYY-MM-DD)
  --end DATE        End date (YYYY-MM-DD)
  --days N          Days from today (alternative to begin/end)
  --report-type     Report type: metrics or daily (default: daily)
  --stock-type      Stock type: '', wb, mp (default: '')
  --resume          Resume mode: continue from last date
  --db PATH         Database path (overrides config)
  --help            Show this help

Report Types:
  metrics   - STOCK_HISTORY_REPORT_CSV (metrics with monthly columns)
  daily     - STOCK_HISTORY_DAILY_CSV (daily stock levels per warehouse)

Examples:
  # Download last 7 days (daily report)
  WB_API_KEY=xxx go run . --days=7

  # Download metrics for specific period
  WB_API_KEY=xxx go run . --report-type=metrics --begin=2026-01-01 --end=2026-01-31


`)
}
