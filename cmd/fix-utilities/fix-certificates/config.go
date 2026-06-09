package main

import (
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/cardupdate"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/filter"
)

// TrashFilterConfig — конфиг фильтрации корзинных карточек на этапе --stage.
type TrashFilterConfig struct {
	Enabled    bool `yaml:"enabled"`
	RatePerMin int  `yaml:"rate_per_min"`
	RateBurst  int  `yaml:"rate_burst"`
}

// Defaults применяет значения по умолчанию для TrashFilterConfig.
func (c TrashFilterConfig) Defaults() TrashFilterConfig {
	if c.RatePerMin == 0 {
		c.RatePerMin = 100
	}
	if c.RateBurst == 0 {
		c.RateBurst = 5
	}
	return c
}

// Config — YAML configuration for fix-certificates.
type Config struct {
	DBPath      string                    `yaml:"db_path"`
	Filters     filter.Filter             `yaml:"filters"`
	WBUpdate    cardupdate.WBUpdateConfig `yaml:"wb_update"`
	TrashFilter TrashFilterConfig         `yaml:"trash_filter"`
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if cfg.DBPath == "" {
		cfg.DBPath = "/var/db/wb-sales.db"
	}
	cfg.WBUpdate = cfg.WBUpdate.Defaults()
	cfg.TrashFilter = cfg.TrashFilter.Defaults()
	return &cfg, nil
}
