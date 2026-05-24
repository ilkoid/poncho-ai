package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/cardupdate"
	"gopkg.in/yaml.v3"
)

type Config struct {
	DBPath   string                    `yaml:"db_path"`
	XLSPath  string                    `yaml:"xls_path"`
	Filters  Filters                   `yaml:"filters"`
	WBUpdate cardupdate.WBUpdateConfig `yaml:"wb_update"`
}

type Filters struct {
	InStock        bool     `yaml:"in_stock"`
	AllowedYears   []int    `yaml:"allowed_years"`
	ExcludeLengths []int    `yaml:"exclude_lengths"`
	Seasons        []string `yaml:"seasons"`
	Subject        string   `yaml:"subject"`
	SubjectIDs     []int    `yaml:"subject_ids"`
	VendorCodes    []string `yaml:"vendor_codes"`
	NmIDs          []int    `yaml:"nm_ids"`
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.DBPath == "" {
		cfg.DBPath = "/tmp/test.db"
	}
	cfg.WBUpdate = cfg.WBUpdate.Defaults()
	return &cfg, nil
}

// getWBApiKey resolves API key: config → env WB_API_CONTENT_KEY → WB_API_KEY → WB_API_ANALYTICS_AND_PROMO_KEY.
func getWBApiKey(preferred string) string {
	if preferred != "" {
		return preferred
	}
	if val := strings.TrimSpace(os.Getenv("WB_API_CONTENT_KEY")); val != "" {
		return val
	}
	if val := strings.TrimSpace(os.Getenv("WB_API_KEY")); val != "" {
		return val
	}
	if val := strings.TrimSpace(os.Getenv("WB_API_ANALYTICS_AND_PROMO_KEY")); val != "" {
		return val
	}
	return ""
}
