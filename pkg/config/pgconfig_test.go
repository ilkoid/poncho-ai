package config

import (
	"strings"
	"testing"
)

// GetDefaults precedence: env var > YAML value > hardcoded default.

func TestGetDefaults_PGDATABASE_OverridesYAML(t *testing.T) {
	t.Setenv("PGDATABASE", "wb_data_test")
	got := V2StorageConfig{Backend: "postgres", PgDatabase: "wb_data_prod"}.GetDefaults()
	if got.PgDatabase != "wb_data_test" {
		t.Fatalf("PGDATABASE must override YAML pg_database: got %q", got.PgDatabase)
	}
}

func TestGetDefaults_PGDATABASE_UnsetKeepsYAML(t *testing.T) {
	t.Setenv("PGDATABASE", "")
	got := V2StorageConfig{Backend: "postgres", PgDatabase: "wb_data_prod"}.GetDefaults()
	if got.PgDatabase != "wb_data_prod" {
		t.Fatalf("without env, YAML pg_database must stand: got %q", got.PgDatabase)
	}
}

func TestGetDefaults_PGDATABASE_EmptyYAMLFallsBackToDefault(t *testing.T) {
	t.Setenv("PGDATABASE", "")
	got := V2StorageConfig{Backend: "postgres"}.GetDefaults()
	if got.PgDatabase != "wb_data_prod" {
		t.Fatalf("without env and YAML, hardcoded default expected: got %q", got.PgDatabase)
	}
}

func TestGetDefaults_SQLITE_PATH_OverridesYAML(t *testing.T) {
	t.Setenv("SQLITE_PATH", "/tmp/test.db")
	got := V2StorageConfig{Backend: "sqlite", DbPath: "/var/db/wb-sales.db"}.GetDefaults()
	if got.DbPath != "/tmp/test.db" {
		t.Fatalf("SQLITE_PATH must override YAML db_path: got %q", got.DbPath)
	}
}

func TestGetDefaults_SQLITE_PATH_UnsetKeepsYAML(t *testing.T) {
	t.Setenv("SQLITE_PATH", "")
	got := V2StorageConfig{Backend: "sqlite", DbPath: "/var/db/wb-sales.db"}.GetDefaults()
	if got.DbPath != "/var/db/wb-sales.db" {
		t.Fatalf("without env, YAML db_path must stand: got %q", got.DbPath)
	}
}

func TestGetDefaults_SQLITE_PATH_EmptyYAMLFallsBackToDefault(t *testing.T) {
	t.Setenv("SQLITE_PATH", "")
	got := V2StorageConfig{Backend: "sqlite"}.GetDefaults()
	if got.DbPath != "/var/db/wb-sales.db" {
		t.Fatalf("without env and YAML, hardcoded default expected: got %q", got.DbPath)
	}
}

// End-to-end: the override must reach the actual DSN string and the log header.

func TestGetEffectiveDSN_Postgres_UsesPGDATABASE(t *testing.T) {
	t.Setenv("PGDATABASE", "wb_data_test")
	t.Setenv("PG_PWD", "secret")
	dsn, err := V2StorageConfig{Backend: "postgres", PgDatabase: "wb_data_prod"}.GetEffectiveDSN()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(dsn, "/wb_data_test?") {
		t.Fatalf("DSN must use env-overridden database: %q", dsn)
	}
	if strings.Contains(dsn, "wb_data_prod") {
		t.Fatalf("DSN must not contain the YAML db name: %q", dsn)
	}
}

func TestGetEffectiveDSN_Sqlite_UsesSQLITE_PATH(t *testing.T) {
	t.Setenv("SQLITE_PATH", "/tmp/test.db")
	dsn, err := V2StorageConfig{Backend: "sqlite", DbPath: "/var/db/wb-sales.db"}.GetEffectiveDSN()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(dsn, "/tmp/test.db?") {
		t.Fatalf("DSN must use env-overridden sqlite path: %q", dsn)
	}
}

func TestDisplayDB_ReflectsPGDATABASE(t *testing.T) {
	t.Setenv("PGDATABASE", "wb_data_test")
	got := V2StorageConfig{Backend: "postgres", PgDatabase: "wb_data_prod"}.DisplayDB()
	if got != "wb_data_test" {
		t.Fatalf("DisplayDB must reflect env-overridden db name (stay in sync with DSN): got %q", got)
	}
}
