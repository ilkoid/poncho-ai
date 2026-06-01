package main

import (
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/cardupdate"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/filter"
)

type CompareConfig struct {
	ToleranceCm3 float64 `yaml:"tolerance_cm3"`
	ToleranceKg  float64 `yaml:"tolerance_kg"`
}

type Config struct {
	DBPath   string                    `yaml:"db_path"`
	Filters  filter.Filter             `yaml:"filters"`
	WBUpdate cardupdate.WBUpdateConfig `yaml:"wb_update"`
	Compare  CompareConfig             `yaml:"compare"`
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
	return &cfg, nil
}
