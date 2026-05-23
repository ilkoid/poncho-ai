package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/sales"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

type Config struct {
	WB       config.WBClientConfig `yaml:"wb"`
	Download config.DownloadConfig `yaml:"download"`
	Storage  struct {
		DBPath string `yaml:"db_path"`
	} `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	days := flag.Int("days", 0, "Number of days to download")
	rewrite := flag.Bool("rewrite", false, "Rewrite mode")
	resume := flag.Bool("resume", false, "Resume mode")
	mockMode := flag.Bool("mock", false, "Use mock client (no API calls)")
	dryRun := flag.Bool("dry-run", false, "Show what would be saved without writing to DB")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	cfg.Download = cfg.Download.GetDefaults()

	if *days > 0 {
		cfg.Download.Days = *days
	}
	if *rewrite {
		cfg.Download.Rewrite = true
	}
	if *resume {
		cfg.Download.Resume = true
	}

	dllog.PrintHeader("WB Sales Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "DB", Value: cfg.Storage.DBPath},
		dllog.HeaderField{Key: "Days", Value: fmt.Sprintf("%d", cfg.Download.Days)},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	repo, err := sqlite.NewSQLiteSalesRepository(cfg.Storage.DBPath)
	if err != nil {
		log.Fatalf("repo error: %v", err)
	}
	defer repo.Close()

	var source sales.SalesSource
	if *mockMode {
		source = &sales.MockSalesSource{}
	} else {
		wbClient := wb.New(cfg.WB.APIKey)
		wbClient.SetRateLimit("sales",
			cfg.WB.RateLimit, cfg.WB.BurstLimit,
			1, 1) // api floor: 1 req/min
		wbClient.SetAdaptiveParams(
			cfg.Download.AdaptiveRecoverAfter,
			cfg.Download.AdaptiveProbeAfter,
			cfg.Download.MaxBackoffSeconds)
		source = wbClient
	}

	opts := sales.DownloadOptions{
		RateLimit:          cfg.WB.RateLimit,
		Burst:              cfg.WB.BurstLimit,
		SkipServiceRecords: cfg.Download.SkipServiceRecords,
		DryRun:             *dryRun,
		OnProgress:         func(msg string) { fmt.Println(msg) },
	}

	dl := sales.NewDownloader(source, repo, opts)

	end := time.Now()
	begin := end.AddDate(0, 0, -cfg.Download.Days)
	ranges := wb.SplitPeriod(begin, end)

	result, err := dl.Run(ctx, ranges, cfg.Download.Resume, cfg.Download.Rewrite)
	if err != nil {
		log.Fatalf("download failed: %v", err)
	}

	dllog.Done(result.Duration, "%d rows, %d periods", result.TotalRows, result.PeriodsCount)
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
