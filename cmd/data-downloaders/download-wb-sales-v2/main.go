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
	"github.com/ilkoid/poncho-ai/pkg/sales"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/utils"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

type Config struct {
	WB struct {
		APIKey    string `yaml:"api_key"`
		RateLimit int    `yaml:"rate_limit"`
		Burst     int    `yaml:"burst"`
	} `yaml:"wb"`
	Download struct {
		Days               int  `yaml:"days"`
		Resume             bool `yaml:"resume"`
		Rewrite            bool `yaml:"rewrite"`
		SkipServiceRecords bool `yaml:"skip_service_records"`
	} `yaml:"download"`
	Storage struct {
		DBPath string `yaml:"db_path"`
	} `yaml:"storage"`
}

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
	days := flag.Int("days", 0, "Number of days to download")
	rewrite := flag.Bool("rewrite", false, "Rewrite mode")
	resume := flag.Bool("resume", false, "Resume mode")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	if *days > 0 {
		cfg.Download.Days = *days
	}
	if *rewrite {
		cfg.Download.Rewrite = true
	}
	if *resume {
		cfg.Download.Resume = true
	}

	utils.Info("=== download-wb-sales-v2 (pkg/sales based) ===")

	repo, err := sqlite.NewSQLiteSalesRepository(cfg.Storage.DBPath)
	if err != nil {
		log.Fatalf("repo error: %v", err)
	}
	defer repo.Close()

	wbClient := wb.New(cfg.WB.APIKey)

	opts := sales.DownloadOptions{
		RateLimit:          cfg.WB.RateLimit,
		Burst:              cfg.WB.Burst,
		SkipServiceRecords: cfg.Download.SkipServiceRecords,
		OnProgress:         func(msg string) { fmt.Println(msg) },
	}

	dl := sales.NewDownloader(wbClient, repo, opts)

	end := time.Now()
	begin := end.AddDate(0, 0, -cfg.Download.Days)
	ranges := wb.SplitPeriod(begin, end)

	result, err := dl.Run(ctx, ranges, cfg.Download.Resume, cfg.Download.Rewrite)
	if err != nil {
		log.Fatalf("download failed: %v", err)
	}

	fmt.Printf("\n✅ v2 finished. rows=%d, periods=%d, duration=%s\n",
		result.TotalRows, result.PeriodsCount, result.Duration.Round(time.Second))
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
