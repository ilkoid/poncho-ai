package main

import (
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// Config — YAML configuration for fix-card-fields.
type Config struct {
	DBPath           string         `yaml:"db_path"`
	FixRules         []FixRule      `yaml:"fix_rules"`
	Filters          Filters        `yaml:"filters"`
	ProtectedCharIDs []int          `yaml:"protected_char_ids"`
	WBUpdate         WBUpdateConfig `yaml:"wb_update"`
}

// FixRule defines a single replacement rule.
type FixRule struct {
	CharID       int         `yaml:"char_id"`
	SearchValue  interface{} `yaml:"search_value"`
	ReplaceValue interface{} `yaml:"replace_value"`
	ValueType    string      `yaml:"value_type"`
}

// Filters restrict which cards are affected.
type Filters struct {
	SubjectIDs       []int    `yaml:"subject_ids"`
	VendorCodes      []string `yaml:"vendor_codes"`
	NmIDs            []int    `yaml:"nm_ids"`
	VendorCodePrefix string   `yaml:"vendor_code_prefix"`
	VendorCodeYears  []int    `yaml:"vendor_code_years"`
	InStock          bool     `yaml:"in_stock"`
}

// WBUpdateConfig controls batch size and rate limiting.
type WBUpdateConfig struct {
	BatchSize       int `yaml:"batch_size"`
	RatePerMin      int `yaml:"rate_per_min"`
	RateBurst       int `yaml:"rate_burst"`
	APIFloorPerMin  int `yaml:"api_floor_per_min"`
	APIFloorBurst   int `yaml:"api_floor_burst"`
	IntervalSeconds int `yaml:"interval_seconds"`
}

// loadConfig reads YAML from path and applies defaults + validation.
func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.DBPath == "" {
		c.DBPath = "/var/db/wb-sales.db"
	}
	for i := range c.FixRules {
		if c.FixRules[i].ValueType == "" {
			c.FixRules[i].ValueType = "string"
		}
	}
	if c.WBUpdate.BatchSize == 0 {
		c.WBUpdate.BatchSize = 30
	}
	if c.WBUpdate.RatePerMin == 0 {
		c.WBUpdate.RatePerMin = 8
	}
	if c.WBUpdate.RateBurst == 0 {
		c.WBUpdate.RateBurst = 2
	}
	if c.WBUpdate.APIFloorPerMin == 0 {
		c.WBUpdate.APIFloorPerMin = 5
	}
	if c.WBUpdate.APIFloorBurst == 0 {
		c.WBUpdate.APIFloorBurst = 1
	}
	if c.WBUpdate.IntervalSeconds == 0 {
		c.WBUpdate.IntervalSeconds = 8
	}
}

func (c *Config) validate() error {
	if len(c.FixRules) == 0 {
		return fmt.Errorf("fix_rules is empty — nothing to fix")
	}

	protected := make(map[int]bool, len(c.ProtectedCharIDs))
	for _, id := range c.ProtectedCharIDs {
		protected[id] = true
	}

	for i, rule := range c.FixRules {
		if protected[rule.CharID] {
			return fmt.Errorf("fix_rules[%d]: char_id %d is in protected_char_ids — remove it from rules or protection list", i, rule.CharID)
		}
		switch rule.ValueType {
		case "string", "number", "boolean":
		default:
			return fmt.Errorf("fix_rules[%d]: invalid value_type %q (must be string/number/boolean)", i, rule.ValueType)
		}
	}

	return nil
}
