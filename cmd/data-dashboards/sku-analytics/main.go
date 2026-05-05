package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dashboard"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	port := flag.Int("port", 0, "Override server port")
	dbPath := flag.String("db", "", "Override DB path")
	help := flag.Bool("help", false, "Show help")
	flag.BoolVar(help, "h", false, "Show help")
	flag.Parse()

	if *help {
		fmt.Println("SKU Analytics Dashboard")
		fmt.Println()
		flag.PrintDefaults()
		os.Exit(0)
	}

	// Load config
	var cfg dashboard.ServerConfig
	if err := config.LoadYAML(*configPath, &cfg); err != nil {
		log.Fatalf("load config: %v", err)
	}

	// CLI overrides
	if *port != 0 {
		cfg.Port = *port
	}
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}

	// Defaults
	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	if cfg.Host == "" {
		cfg.Host = "0.0.0.0"
	}
	if cfg.Table == "" {
		cfg.Table = "ma_sku_daily"
	}
	if cfg.DateColumn == "" {
		cfg.DateColumn = "snapshot_date"
	}

	if cfg.DBPath == "" {
		log.Fatalf("db_path is required in config")
	}

	// Open DB (read-only)
	db, err := dashboard.OpenReadOnlyDB(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	log.Printf("[main] db: %s", cfg.DBPath)

	// Attach analytics DB if configured
	if cfg.DBAnalyticsPath != "" {
		if err := dashboard.AttachDB(db, "analytics", cfg.DBAnalyticsPath); err != nil {
			log.Printf("[main] attach analytics db: %v (sales/warehouse tabs will be empty)", err)
		} else {
			log.Printf("[main] analytics db attached: %s", cfg.DBAnalyticsPath)
		}
	}

	// Start server
	srv := dashboard.NewServer(cfg, db, BuildDashboard)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server: %v", err)
	}
}
