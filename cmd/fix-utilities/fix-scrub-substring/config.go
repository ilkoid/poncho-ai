// fix-scrub-substring — YAML-driven case-insensitive substring scrubber for PostgreSQL.
//
// Replaces every case-insensitive occurrence of read_substring with write_substring
// across all text columns of every base table in the configured schema. See plan at
// .claude/plans/fix-polymorphic-sprout.md.
//
// Design rule: NO hardcoded defaults, NO fallbacks. Every connection param and both
// substrings MUST be present in YAML or Validate() fails loudly. We deliberately do
// NOT reuse pkg/config/pgconfig.go — its GetDefaults()/BuildPgDSN() inject fallback
// host/port/user/db (10.120.24.155/5432/arm_ai_admin/wb_data_prod), which would
// silently point this destructive tool at the wrong database.
package main

import (
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// PGConfig holds PostgreSQL connection params. Every field is REQUIRED.
type PGConfig struct {
	Host     string `yaml:"host"`     // REQUIRED
	Port     int    `yaml:"port"`     // REQUIRED (1..65535)
	User     string `yaml:"user"`     // REQUIRED
	Password string `yaml:"password"` // REQUIRED — use "${PG_PWD}" in YAML (expanded by LoadYAML)
	Database string `yaml:"database"` // REQUIRED
	SSLMode  string `yaml:"sslmode"`  // REQUIRED — e.g. "disable", "require"
}

// Config is the full YAML config. Only optional fields have empty zero values; every
// REQUIRED field is enforced by Validate().
type Config struct {
	PG            PGConfig `yaml:"pg"`
	Schema        string   `yaml:"schema"`         // REQUIRED (e.g. "public")
	Read          string   `yaml:"read_substring"` // REQUIRED — matched case-insensitively
	Write         string   `yaml:"write_substring"`// REQUIRED — inserted verbatim; empty is FORBIDDEN
	ColumnTypes   []string `yaml:"column_types"`   // REQUIRED, non-empty (information_schema.data_type names)
	IncludeJSONB  bool     `yaml:"include_jsonb"`  // OPTIONAL — appends jsonb/json to ColumnTypes
	IncludeTables []string `yaml:"include_tables"` // OPTIONAL — keep only these tables
	ExcludeTables []string `yaml:"exclude_tables"` // OPTIONAL — drop these tables
	SampleRows    int      `yaml:"sample_rows"`    // OPTIONAL — 0 → defaultSampleRows at use site

	// StatementTimeoutSeconds overrides the pool's default 5-min statement_timeout
	// (inherited from postgres.NewPool). On a multi-million-row scrub a single
	// consolidated UPDATE can legitimately run longer than 5 minutes, and hitting
	// that timeout mid-transaction rolls back the WHOLE scrub. 0 = no limit (the
	// safe default for this tool — the user explicitly invokes --apply and watches
	// it). This is an OPERATIONAL tuning param, not a connection-identity default,
	// so it does not violate the "no fallbacks for routing" rule.
	StatementTimeoutSeconds int `yaml:"statement_timeout_seconds"` // OPTIONAL, ≥0
}

// defaultSampleRows is used when SampleRows is unset. This is a DISPLAY default
// (how many before→after pairs to show in --dry-run), not a connection default —
// it cannot route the tool at the wrong database, so it does not violate the
// "no defaults" rule, which targets connection/substring params.
const defaultSampleRows = 3

// loadConfig reads YAML, expanding ${ENV} (so password: "${PG_PWD}" works), then
// validates strictly. Returns a clear error for any missing REQUIRED field.
func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}
	return &cfg, nil
}

// Validate enforces every REQUIRED field. No defaults, no fallbacks.
func (c *Config) Validate() error {
	if c.PG.Host == "" {
		return fmt.Errorf("pg.host is required")
	}
	if c.PG.Port < 1 || c.PG.Port > 65535 {
		return fmt.Errorf("pg.port is required and must be 1..65535")
	}
	if c.PG.User == "" {
		return fmt.Errorf("pg.user is required")
	}
	if c.PG.Password == "" {
		return fmt.Errorf("pg.password is required (use ${PG_PWD} in YAML)")
	}
	if c.PG.Database == "" {
		return fmt.Errorf("pg.database is required")
	}
	if c.PG.SSLMode == "" {
		return fmt.Errorf("pg.sslmode is required (e.g. disable, require)")
	}
	if c.Schema == "" {
		return fmt.Errorf("schema is required (e.g. public)")
	}
	if c.Read == "" {
		return fmt.Errorf("read_substring is required")
	}
	if c.Write == "" {
		return fmt.Errorf("write_substring must not be empty (deletion is forbidden)")
	}
	if len(c.ColumnTypes) == 0 {
		return fmt.Errorf("column_types is required (e.g. [text, \"character varying\", character])")
	}
	if c.StatementTimeoutSeconds < 0 {
		return fmt.Errorf("statement_timeout_seconds must be >= 0 (0 = no limit)")
	}
	return nil
}

// buildDSN constructs the connection string from validated YAML values only.
// Uses net/url so the password is correctly percent-encoded for any character
// (@, :, /, etc.). No pgconfig defaults are consulted.
func (c *Config) buildDSN() string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.PG.User, c.PG.Password),
		Host:   net.JoinHostPort(c.PG.Host, strconv.Itoa(c.PG.Port)),
		Path:   "/" + c.PG.Database,
	}
	q := url.Values{}
	q.Set("sslmode", c.PG.SSLMode)
	u.RawQuery = q.Encode()
	return u.String()
}
