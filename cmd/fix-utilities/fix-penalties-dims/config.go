// fix-penalties-dims — robot fixer for WB measurement penalties (МГХ).
//
// Reads confirmed dimension penalties (table measurement_penalties), and for
// each penalized nmID rewrites the product card's L/W/H with WB's own warehouse
// measurement — so the card matches reality and repeat penalties stop.
//
// Autonomous SQLite-only robot. It works off its OWN isolated database
// (storage.db_path, default fixer.db) which the .sh wrapper repopulates each run
// with fresh REAL data via the existing downloaders (download-wb-cards,
// download-wb-penalties-v2). It never touches the shared PostgreSQL data-lake, so
// the cloud-LLM sanitization of PG ([PlayBrand]) can never reach WB through it.
// Reuses pkg/cardupdate (CardUpdater.LoadFullCard + ToUpdateItem) for the safe
// full-card overwrite invariant.
//
// ⚠️ WB write safety: --apply/--auto without --dry-run performs a REAL WB write
// and is run ONLY by the user (cron). Claude never runs --apply. Testing uses
// --dry-run + a throwaway SQLite DB (--db /tmp/…).
package main

import (
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/cardupdate"
	"github.com/ilkoid/poncho-ai/pkg/config"
)

// SourceConfig controls how measurement penalties are selected as the source of truth.
type SourceConfig struct {
	OnlyConfirmed bool `yaml:"only_confirmed"` // is_valid = 1 only (default true via loadConfig)
	LatestPerNm   bool `yaml:"latest_per_nm"`  // most recent measurement per nm_id (informational; query always takes latest)
}

// ArticleFilter restricts which penalized articles to process. Applied inline in
// the stage SQL as SQLite IN (?,?,…) / NOT IN — pg/filter is not used.
type ArticleFilter struct {
	NmIDs              []int    `yaml:"nm_ids"`
	VendorCodes        []string `yaml:"vendor_codes"`
	ExcludeVendorCodes []string `yaml:"exclude_vendor_codes"`
}

// AuditConfig configures the CSV audit log location.
type AuditConfig struct {
	LogDir string `yaml:"log_dir"` // daily-rotated CSV audit directory (next to binary)
}

// StorageConfig is the path to this fixer's isolated SQLite database. The .sh
// wrapper repopulates it with fresh real cards + penalties before each run.
type StorageConfig struct {
	DBPath string `yaml:"db_path"`
}

// DisplayDB returns the DB path for the run header (fallback to the default name).
func (s StorageConfig) DisplayDB() string {
	if s.DBPath == "" {
		return "fixer.db"
	}
	return s.DBPath
}

// Config is the YAML configuration for the penalties-dims fixer.
type Config struct {
	Storage  StorageConfig             `yaml:"storage"`
	WB       config.WBClientConfig     `yaml:"wb"`
	Source   SourceConfig              `yaml:"source"`
	Filter   ArticleFilter             `yaml:"filter"`
	WBUpdate cardupdate.WBUpdateConfig `yaml:"wb_update"`
	Audit    AuditConfig               `yaml:"audit"`
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	cfg.WBUpdate = cfg.WBUpdate.Defaults()
	if cfg.Storage.DBPath == "" {
		cfg.Storage.DBPath = "fixer.db"
	}
	if cfg.Audit.LogDir == "" {
		cfg.Audit.LogDir = "logs"
	}
	return &cfg, nil
}
