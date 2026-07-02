package wbscraper

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// writeYAML writes s to a temp file named config.yaml and returns its dir,
// so config.LoadYAML resolves it as a relative path the way the CLI does.
func writeYAML(t *testing.T, s string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatalf("write temp yaml: %v", err)
	}
	return dir, path
}

// TestConfigParse verifies the Config struct mirrors the YAML shape: every section
// and the "" dimension token (skip-dimension) round-trips. Uses an inline YAML
// representative of cmd/.configs/download-all/wb-scraper-collector.yaml.
func TestConfigParse(t *testing.T) {
	const yaml = `
storage:
  backend: "postgres"
  db_path: "/tmp/wbscraper.db"
  pg_database: "wb_data_test"
  pg_password_env: "PG_PWD"
server:
  host: "127.0.0.1"
  port: 7780
  read_timeout: "30s"
  write_timeout: "30s"
generator:
  kind: "static"
  constructor:
    subjects: ["бейсболки", "футболки"]
    gender: ["", "для мальчика"]
    season: ["", "зима"]
    age: ["", "детские"]
    max_queries: 300
    dedup: true
collect:
  batch_targets: 50
  flush_interval: "2s"
report:
  enabled: true
  out_dir: "/tmp"
  snapshot_lookback: "24h"
targets:
  - "кроссовки детские"
  - "17401163"
`
	_, path := writeYAML(t, yaml)
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}

	// storage
	if cfg.Storage.Backend != "postgres" {
		t.Errorf("storage.backend = %q, want postgres", cfg.Storage.Backend)
	}
	if cfg.Storage.DbPath != "/tmp/wbscraper.db" {
		t.Errorf("storage.db_path = %q", cfg.Storage.DbPath)
	}
	if cfg.Storage.PgDatabase != "wb_data_test" {
		t.Errorf("storage.pg_database = %q", cfg.Storage.PgDatabase)
	}

	// server
	if cfg.Server.Port != 7780 || cfg.Server.Host != "127.0.0.1" {
		t.Errorf("server = %+v", cfg.Server)
	}
	if cfg.Server.ReadTimeout != "30s" {
		t.Errorf("server.read_timeout = %q", cfg.Server.ReadTimeout)
	}

	// generator + the "" dimension token must survive (it is the skip-dimension marker)
	if cfg.Generator.Kind != "static" {
		t.Errorf("generator.kind = %q", cfg.Generator.Kind)
	}
	c := cfg.Generator.Constructor
	if len(c.Subjects) != 2 || c.Subjects[0] != "бейсболки" {
		t.Errorf("constructor.subjects = %v", c.Subjects)
	}
	if len(c.Gender) != 2 || c.Gender[0] != "" {
		t.Errorf("constructor.gender = %v (want leading \"\")", c.Gender)
	}
	if c.MaxQueries != 300 || !c.Dedup {
		t.Errorf("constructor max_queries/dedup = %d/%v", c.MaxQueries, c.Dedup)
	}

	// collect / report
	if cfg.Collect.BatchTargets != 50 || cfg.Collect.FlushInterval != "2s" {
		t.Errorf("collect = %+v", cfg.Collect)
	}
	if !cfg.Report.Enabled || cfg.Report.OutDir != "/tmp" {
		t.Errorf("report = %+v", cfg.Report)
	}

	// targets override
	if len(cfg.Targets) != 2 || cfg.Targets[1] != "17401163" {
		t.Errorf("targets = %v", cfg.Targets)
	}
}

// TestConfigDefaults verifies Defaults() fills zero values with sane defaults
// (used when a minimal config omits optional blocks).
func TestConfigDefaults(t *testing.T) {
	cfg := Config{}.Defaults()
	if cfg.Server.Host != "127.0.0.1" || cfg.Server.Port != 7780 {
		t.Errorf("server defaults = %+v", cfg.Server)
	}
	if cfg.Server.ReadTimeout != "30s" || cfg.Server.WriteTimeout != "30s" {
		t.Errorf("timeout defaults = %q/%q", cfg.Server.ReadTimeout, cfg.Server.WriteTimeout)
	}
	if cfg.Generator.Kind != "static" {
		t.Errorf("generator.kind default = %q", cfg.Generator.Kind)
	}
	if cfg.Generator.Constructor.MaxQueries != 300 {
		t.Errorf("max_queries default = %d", cfg.Generator.Constructor.MaxQueries)
	}
	if cfg.Collect.BatchTargets != 50 || cfg.Collect.FlushInterval != "2s" {
		t.Errorf("collect defaults = %+v", cfg.Collect)
	}
	if cfg.Report.OutDir != "/tmp" || cfg.Report.SnapshotLookback != "24h" {
		t.Errorf("report defaults = %+v", cfg.Report)
	}
}

// TestConfigParseDuration verifies the duration-string helper used by the
// server/ticker/report builders (good, empty, malformed).
func TestConfigParseDuration(t *testing.T) {
	cases := []struct {
		name    string
		s       string
		wantErr bool
	}{
		{"good", "30s", false},
		{"empty", "", false}, // empty → zero, no error
		{"bad", "nope", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := ParseDuration("flush_interval", tc.s)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %q, got %v", tc.s, d)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.s, err)
			}
		})
	}
}
