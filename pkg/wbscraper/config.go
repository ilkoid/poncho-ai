package wbscraper

import (
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// Config is the YAML configuration for the wb-scraper-collector. It is the
// "mirror of system logic": each block maps to a component and an implementation
// stage (see plan § Config YAML mirror table). The YAML is committed up-front as
// a living contract; later stages implement their block against it.
//
// Storage reuses config.V2StorageConfig (dual-backend, same as all v2 downloaders).
// There is intentionally no `wb:` block — the collector never calls the WB API;
// all storefront requests are made by the browser extension.
type Config struct {
	Storage   config.V2StorageConfig `yaml:"storage"`
	Server    ServerConfig           `yaml:"server"`
	Generator GeneratorConfig        `yaml:"generator"`
	Collect   CollectConfig          `yaml:"collect"`
	Report    ReportConfig           `yaml:"report"`
	// Targets, when non-empty, overrides Generator.Constructor with an explicit
	// list of debug targets (search text or numeric nmId strings).
	Targets []string `yaml:"targets"`
}

// ServerConfig is the loopback HTTP server the browser extension talks to.
// Timeouts are duration strings ("30s") parsed at the use site via time.ParseDuration,
// matching the codebase precedent (pkg/config/config.go chain timeout).
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	// ReadTimeout / WriteTimeout are duration strings ("30s"); empty → default.
	ReadTimeout  string `yaml:"read_timeout"`
	WriteTimeout string `yaml:"write_timeout"`
}

// GeneratorConfig selects the target source. kind "static" uses the constructor
// below; "llm" is a stub for future query generation under a goal via pkg/llm.
type GeneratorConfig struct {
	Kind        string             `yaml:"kind"` // "static" (default) | "llm"
	Constructor ConstructorConfig  `yaml:"constructor"`
	LLM         LLMGeneratorConfig `yaml:"llm"` // used only when Kind == "llm"
}

// ConstructorConfig drives the Cartesian-product query generator.
// Each dimension list includes "" as the "skip this dimension" token, so
// subject × gender × season × age produces the intended combinations.
type ConstructorConfig struct {
	Subjects []string `yaml:"subjects"`
	Gender   []string `yaml:"gender"`
	Season   []string `yaml:"season"`
	Age      []string `yaml:"age"`
	// MaxQueries caps the generated set (explosion guard).
	MaxQueries int `yaml:"max_queries"`
	// Dedup drops duplicate query strings after token join.
	Dedup bool `yaml:"dedup"`
}

// LLMGeneratorConfig is reserved for future LLM-driven query generation.
// Untouched until the LLM phase; kept here so the config shape is stable.
type LLMGeneratorConfig struct {
	Provider   string `yaml:"provider"`    // pkg/llm Provider name (zai/openrouter/openai)
	PromptFile string `yaml:"prompt_file"` // prompt source (pkg/prompts: File→Default→API→DB)
}

// CollectConfig tunes the server-side collection loop (how targets are batched
// out and how fast captures are flushed to the DB). Navigation pace, MAX_PAGES
// and DETAIL_K live in the extension (offscreen.js) and are not duplicated here.
type CollectConfig struct {
	BatchTargets int `yaml:"batch_targets"`
	// FlushInterval is a duration string ("2s"); empty → default.
	FlushInterval string `yaml:"flush_interval"`
}

// ReportConfig controls the single-file HTML report written after a session.
type ReportConfig struct {
	Enabled bool   `yaml:"enabled"`
	OutDir  string `yaml:"out_dir"`
	// SnapshotLookback is a duration string ("24h"): include rows whose SnapshotTs
	// falls within the last N. Empty → default.
	SnapshotLookback string `yaml:"snapshot_lookback"`
}

// Defaults returns cfg with sensible defaults applied to zero-value fields.
// Precedence: existing value > default. Storage backend/porting env overrides are
// applied by config.V2StorageConfig.GetDefaults at the call site (not here), since
// they are environment-dependent.
//
// Duration strings are NOT parsed here — callers parse them when building the
// server/ticker/report, so a malformed value surfaces at the use site with context.
func (cfg Config) Defaults() Config {
	out := cfg
	if out.Server.Host == "" {
		out.Server.Host = "127.0.0.1"
	}
	if out.Server.Port == 0 {
		out.Server.Port = 7780
	}
	if out.Server.ReadTimeout == "" {
		out.Server.ReadTimeout = "30s"
	}
	if out.Server.WriteTimeout == "" {
		out.Server.WriteTimeout = "30s"
	}
	if out.Generator.Kind == "" {
		out.Generator.Kind = "static"
	}
	if out.Generator.Constructor.MaxQueries == 0 {
		out.Generator.Constructor.MaxQueries = 300
	}
	// Dedup defaults to true: dropping duplicate query strings is the safe default.
	// yaml decodes an absent bool as false, so we treat false+absent identically —
	// callers wanting duplicates must be rare and can set max_queries accordingly.
	if out.Collect.BatchTargets == 0 {
		out.Collect.BatchTargets = 50
	}
	if out.Collect.FlushInterval == "" {
		out.Collect.FlushInterval = "2s"
	}
	if out.Report.OutDir == "" {
		out.Report.OutDir = "/tmp"
	}
	if out.Report.SnapshotLookback == "" {
		out.Report.SnapshotLookback = "24h"
	}
	return out
}

// ParseDuration parses a Config duration string ("30s", "2s", "24h") with context
// for clear errors. Returns the zero duration for an empty string (caller decides
// whether that is meaningful).
func ParseDuration(field, s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%s: parse duration %q: %w", field, s, err)
	}
	return d, nil
}
