// Package config — PostgreSQL connection configuration for v2 downloaders.
//
// V2StorageConfig selects between SQLite and PostgreSQL backends.
// BuildPgDSN constructs a DSN from environment variables with sensible defaults.
package config

import (
	"fmt"
	"net/url"
	"os"
)

// V2StorageConfig selects the storage backend for v2 downloaders.
// Separate from StorageConfig (TUI app) which handles refresh windows.
//
// YAML example:
//
//	storage:
//	  backend: "sqlite"           # "sqlite" (default) or "postgres"
//	  db_path: "/var/db/wb-sales.db"
//	  pg_dsn: ""                  # Optional: full DSN overrides env-based construction
//	  pg_database: "wb_data_prod" # Database name (used when pg_dsn is empty)
//	  pg_password_env: "PG_PWD"   # Env var with password
type V2StorageConfig struct {
	// Backend type: "sqlite" (default) or "postgres".
	Backend string `yaml:"backend"`

	// DbPath is the SQLite database file path (used when Backend is "sqlite" or empty).
	DbPath string `yaml:"db_path"`

	// PgDSN is a full PostgreSQL connection string.
	// If empty, DSN is built from environment variables via BuildPgDSN().
	PgDSN string `yaml:"pg_dsn"`

	// PgDatabase is the database name (wb_data_prod, wb_data_test).
	// Used only when PgDSN is empty.
	PgDatabase string `yaml:"pg_database"`

	// PgPasswordEnv names the environment variable holding the PostgreSQL password.
	// Default: "PG_PWD".
	PgPasswordEnv string `yaml:"pg_password_env"`
}

// GetDefaults applies sensible defaults for zero-value fields.
func (s V2StorageConfig) GetDefaults() V2StorageConfig {
	result := s
	if result.Backend == "" {
		result.Backend = "sqlite"
	}
	if result.DbPath == "" {
		result.DbPath = "/var/db/wb-sales.db"
	}
	if result.PgDatabase == "" {
		result.PgDatabase = "wb_data_prod"
	}
	if result.PgPasswordEnv == "" {
		result.PgPasswordEnv = "PG_PWD"
	}
	return result
}

// GetEffectiveDSN returns the resolved DSN for the selected backend.
//
// For "sqlite": returns DbPath (with WAL parameters appended).
// For "postgres": returns the full PostgreSQL DSN string.
//
// Returns an error if required fields are missing (e.g., no password env var).
func (s V2StorageConfig) GetEffectiveDSN() (string, error) {
	s = s.GetDefaults()

	switch s.Backend {
	case "sqlite", "":
		return s.sqliteDSN(), nil
	case "postgres", "postgresql":
		return s.postgresDSN()
	default:
		return "", fmt.Errorf("unsupported storage backend: %s", s.Backend)
	}
}

// sqliteDSN builds SQLite DSN with WAL mode parameters.
func (s V2StorageConfig) sqliteDSN() string {
	return s.DbPath + "?_journal_mode=WAL&_cache_size=-65536&_busy_timeout=10000&_foreign_keys=1"
}

// postgresDSN builds PostgreSQL DSN from config or environment variables.
func (s V2StorageConfig) postgresDSN() (string, error) {
	// Full DSN provided directly
	if s.PgDSN != "" {
		return injectPassword(s.PgDSN, s.PgPasswordEnv)
	}

	// Build from environment variables
	dsn := BuildPgDSN(s.PgDatabase)
	return injectPassword(dsn, s.PgPasswordEnv)
}

// injectPassword reads password from environment and injects into DSN if needed.
// If DSN already has a password in the URL, returns as-is.
func injectPassword(dsn, passwordEnv string) (string, error) {
	pwd := os.Getenv(passwordEnv)
	if pwd == "" {
		// DSN might already contain password (e.g., from config)
		u, err := url.Parse(dsn)
		if err != nil {
			return "", fmt.Errorf("parse DSN: %w", err)
		}
		if _, ok := u.User.Password(); ok {
			return dsn, nil
		}
		return "", fmt.Errorf("PostgreSQL password not found: set %s env var or include in pg_dsn", passwordEnv)
	}

	// Inject password into DSN
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse DSN: %w", err)
	}
	user := u.User.Username()
	u.User = url.UserPassword(user, pwd)
	return u.String(), nil
}

// BuildPgDSN constructs a PostgreSQL DSN from environment variables with defaults.
//
// Defaults:
//   - host: 192.168.10.7 (override via PGHOST)
//   - port: 15432 (override via PGPORT)
//   - user: postgres
//   - password: from $PG_PWD (caller must inject via injectPassword)
//   - sslmode: disable
//
// The returned DSN does NOT include the password — call injectPassword() separately.
func BuildPgDSN(database string) string {
	host := envOrDefault("PGHOST", "192.168.10.7")
	port := envOrDefault("PGPORT", "15432")

	return fmt.Sprintf("postgres://postgres@%s:%s/%s?sslmode=disable", host, port, database)
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
