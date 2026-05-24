// Shared configuration for WB card update operations.
//
// All utilities that call POST /content/v2/cards/update share the same
// rate limiting, batching, and adaptive backoff parameters. This type
// replaces four identical copies across fix-card-dimensions, fix-card-fields,
// and check-card-consistency.
package cardupdate

// WBUpdateConfig controls batch size and rate limiting for WB card updates.
// Fields map directly to YAML config keys under the "wb_update" section.
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

// Defaults returns a copy with zero-valued fields replaced by production defaults.
// Non-zero fields are preserved, allowing partial overrides from config files.
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
