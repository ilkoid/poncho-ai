package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DBPath   string         `yaml:"db_path"`
	XLSPath  string         `yaml:"xls_path"`
	Filters  Filters        `yaml:"filters"`
	WBUpdate WBUpdateConfig `yaml:"wb_update"`
}

type WBUpdateConfig struct {
	APIKey             string `yaml:"api_key"`
	BatchSize          int    `yaml:"batch_size"`
	RatePerMin         int    `yaml:"rate_per_min"`
	RateBurst          int    `yaml:"rate_burst"`
	APIFloorPerMin     int    `yaml:"api_floor_per_min"`
	APIFloorBurst      int    `yaml:"api_floor_burst"`
	AdaptiveProbeAfter int    `yaml:"adaptive_probe_after"`
	MaxBackoffSeconds  int    `yaml:"max_backoff_seconds"`
	IntervalSeconds    int    `yaml:"interval_seconds"`
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

func (c WBUpdateConfig) Defaults() WBUpdateConfig {
	if c.BatchSize == 0 {
		c.BatchSize = 30
	}
	if c.RatePerMin == 0 {
		c.RatePerMin = 8
	}
	if c.RateBurst == 0 {
		c.RateBurst = 2
	}
	if c.APIFloorPerMin == 0 {
		c.APIFloorPerMin = 5
	}
	if c.APIFloorBurst == 0 {
		c.APIFloorBurst = 1
	}
	if c.AdaptiveProbeAfter == 0 {
		c.AdaptiveProbeAfter = 10
	}
	if c.MaxBackoffSeconds == 0 {
		c.MaxBackoffSeconds = 60
	}
	if c.IntervalSeconds == 0 {
		c.IntervalSeconds = 8
	}
	return c
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
