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
	"github.com/ilkoid/poncho-ai/pkg/nmreport"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

type Config struct {
	WB       config.WBClientConfig    `yaml:"wb"`
	Funnel   config.FunnelCSVConfig   `yaml:"funnel_csv"`
	Storage  struct {
		DBPath             string `yaml:"db_path"`
		FunnelRefreshWindow int   `yaml:"funnel_refresh_window"`
	} `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	days := flag.Int("days", 0, "Number of days (overrides config)")
	reportType := flag.String("report-type", "", "Report type: detail|grouped (overrides config)")
	pollInterval := flag.Int("poll-interval", 0, "Poll interval in seconds (overrides config)")
	pollTimeout := flag.Int("poll-timeout", 0, "Poll timeout in minutes (overrides config)")
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
	if *reportType != "" {
		cfg.Funnel.ReportType = *reportType
	}
	if *pollInterval > 0 {
		cfg.Funnel.PollIntervalSec = *pollInterval
	}
	if *pollTimeout > 0 {
		cfg.Funnel.PollTimeoutMin = *pollTimeout
	}

	apiKey := getAPIKey(cfg)

	dllog.PrintHeader("WB Funnel CSV Downloader",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "DB", Value: cfg.Storage.DBPath},
		dllog.HeaderField{Key: "Report", Value: cfg.Funnel.ReportType},
		dllog.HeaderField{Key: "Days", Value: fmt.Sprintf("%d", cfg.Funnel.Days)},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	repo, err := sqlite.NewSQLiteSalesRepository(cfg.Storage.DBPath)
	if err != nil {
		log.Fatalf("repo error: %v", err)
	}
	defer repo.Close()

	refreshWindow := cfg.Storage.FunnelRefreshWindow
	if refreshWindow == 0 {
		refreshWindow = 4
	}

	var source nmreport.NmReportSource
	if *mockMode {
		if cfg.Funnel.ReportType == "grouped" {
			source = &nmreport.MockGroupedSource{}
		} else {
			source = &nmreport.MockSource{}
		}
	} else {
		wbClient := wb.New(apiKey)
		rl := cfg.Funnel.RateLimits
		wbClient.SetRateLimit("nm_funnel_create", rl.Create, rl.CreateBurst, rl.CreateApi, rl.CreateApiBurst)
		wbClient.SetRateLimit("nm_funnel_status", rl.StatusCheck, rl.StatusCheckBurst, rl.StatusCheckApi, rl.StatusCheckApiBurst)
		wbClient.SetRateLimit("nm_funnel_download", rl.Download, rl.DownloadBurst, rl.DownloadApi, rl.DownloadApiBurst)
		wbClient.SetAdaptiveParams(0, cfg.Funnel.AdaptiveProbeAfter, cfg.Funnel.MaxBackoffSeconds)
		source = nmreport.NewWBSource(wbClient, rl.Create, rl.CreateBurst)
	}

	opts := nmreport.DownloadOptions{
		ReportType:      cfg.Funnel.ReportType,
		Days:            cfg.Funnel.Days,
		From:            cfg.Funnel.Begin,
		To:              cfg.Funnel.End,
		RefreshWindow:   refreshWindow,
		DryRun:          *dryRun,
		Resume:          cfg.Funnel.Resume,
		PollIntervalSec: cfg.Funnel.PollIntervalSec,
		PollTimeoutMin:  cfg.Funnel.PollTimeoutMin,
		OnProgress:      func(msg string) { fmt.Println(msg) },
	}

	dl := nmreport.NewDownloader(source, repo, opts)
	result, err := dl.Run(ctx)
	if err != nil {
		log.Fatalf("download failed: %v", err)
	}

	dllog.Done(result.Duration, "status=%s download_id=%s rows=%d",
		result.Status, result.DownloadID, result.RowsCount)
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
