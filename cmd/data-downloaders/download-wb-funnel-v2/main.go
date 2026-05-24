package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/funnel"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

type Config struct {
	WB     config.WBClientConfig  `yaml:"wb"`
	Funnel config.FunnelConfig    `yaml:"funnel"`
	Filter config.FunnelFilterConfig `yaml:"filter"`
	Storage struct {
		DBPath             string `yaml:"db_path"`
		FunnelRefreshWindow int   `yaml:"funnel_refresh_window"`
	} `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	days := flag.Int("days", 0, "Number of days (overrides config)")
	mockMode := flag.Bool("mock", false, "Use mock source (no API calls)")
	dryRun := flag.Bool("dry-run", false, "Show what would be saved without writing to DB")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	cfg.Funnel = cfg.Funnel.GetDefaults()

	if *days > 0 {
		cfg.Funnel.Days = *days
	}

	apiKey := getAPIKey(cfg)

	dllog.PrintHeader("WB Funnel Downloader v2",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "DB", Value: cfg.Storage.DBPath},
		dllog.HeaderField{Key: "Days", Value: fmt.Sprintf("%d", cfg.Funnel.Days)},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	repo, err := sqlite.NewSQLiteSalesRepository(cfg.Storage.DBPath)
	if err != nil {
		log.Fatalf("repo error: %v", err)
	}
	defer repo.Close()

	var source funnel.FunnelSource
	if *mockMode {
		source = &funnel.MockFunnelSource{}
	} else {
		wbClient := wb.New(apiKey)
		wbClient.SetRateLimit(funnel.ToolID,
			cfg.Funnel.FunnelRateLimit, cfg.Funnel.FunnelRateLimitBurst,
			cfg.Funnel.FunnelRateLimitApi, cfg.Funnel.FunnelRateLimitApiBurst)
		wbClient.SetAdaptiveParams(
			cfg.Funnel.AdaptiveRecoverAfter,
			cfg.Funnel.AdaptiveProbeAfter,
			cfg.Funnel.MaxBackoffSeconds)
		source = funnel.NewWBSource(wbClient, cfg.Funnel.FunnelRateLimit, cfg.Funnel.FunnelRateLimitBurst)
	}

	refreshWindow := cfg.Storage.FunnelRefreshWindow
	if refreshWindow == 0 {
		refreshWindow = 4
	}

	opts := funnel.DownloadOptions{
		Days:             cfg.Funnel.Days,
		BatchSize:        cfg.Funnel.BatchSize,
		MaxBatches:       cfg.Funnel.MaxBatches,
		RefreshWindow:    refreshWindow,
		IncrementalHours: cfg.Funnel.IncrementalHours,
		From:             cfg.Funnel.From,
		To:               cfg.Funnel.To,
		Filter:           cfg.Filter,
		DryRun:           *dryRun,
		OnProgress:       func(msg string) { fmt.Println(msg) },
	}

	dl := funnel.NewDownloader(source, repo, opts)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download failed: %v", err)
	}

	dllog.Done(result.Duration, "%d products, %d metrics, %d batches",
		result.ProductsLoaded, result.MetricsLoaded, result.BatchesTotal)
}

func getAPIKey(cfg *Config) string {
	if cfg.WB.AnalyticsAPIKey != "" {
		return cfg.WB.AnalyticsAPIKey
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
