// fix-penalties-dims — robot fixer for WB measurement penalties (МГХ).
//
// Reads confirmed dimension penalties (table measurement_penalties), and for
// each penalized nmID rewrites the product card's L/W/H with WB's own warehouse
// measurement — so the card matches reality and repeat penalties stop.
//
// PostgreSQL only. Reuses pkg/cardupdate (PGCardUpdater.LoadFullCard +
// ToUpdateItem) for the safe full-card overwrite invariant.
//
// ⚠️ WB write safety: --apply/--auto without --dry-run performs a REAL WB write
// and is run ONLY by the user (cron). Claude never runs --apply. Testing uses
// --dry-run + a test database (--pg-database wb_data_test).
package main

import (
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/cardupdate"
	"github.com/ilkoid/poncho-ai/pkg/config"
)

// SourceConfig controls how measurement penalties are selected as the source of truth.
type SourceConfig struct {
	OnlyConfirmed bool `yaml:"only_confirmed"` // is_valid = true only (default true via loadConfig)
	LatestPerNm   bool `yaml:"latest_per_nm"`  // most recent measurement per nm_id (informational; query always takes latest)
}

// ArticleFilter restricts which penalized articles to process (PG-native, inline $N).
// Applied directly in the stage SQL — pkg/filter is SQLite-oriented and not used here.
type ArticleFilter struct {
	NmIDs              []int    `yaml:"nm_ids"`
	VendorCodes        []string `yaml:"vendor_codes"`
	ExcludeVendorCodes []string `yaml:"exclude_vendor_codes"`
}

// AuditConfig configures the CSV audit log location.
type AuditConfig struct {
	LogDir string `yaml:"log_dir"` // daily-rotated CSV audit directory (next to binary)
}

// Config is the YAML configuration for the penalties-dims fixer.
type Config struct {
	Storage  config.V2StorageConfig    `yaml:"storage"`
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
	cfg.Storage = cfg.Storage.GetDefaults()
	cfg.WBUpdate = cfg.WBUpdate.Defaults()
	if cfg.Audit.LogDir == "" {
		cfg.Audit.LogDir = "logs"
	}
	return &cfg, nil
}
