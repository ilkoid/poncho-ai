package main

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// validConfig returns a fully-populated Config that passes Validate().
func validConfig() *Config {
	return &Config{
		PG:          PGConfig{Host: "h", Port: 5432, User: "u", Password: "p", Database: "d", SSLMode: "disable"},
		Schema:      "public",
		Read:        "brand",
		Write:       "[X]",
		ColumnTypes: []string{"text", "character varying", "character"},
	}
}

// TestValidate_AcceptsValidConfig — the baseline must pass.
func TestValidate_AcceptsValidConfig(t *testing.T) {
	assert.NoError(t, validConfig().Validate())
}

// TestValidate_StatementTimeout — negative is rejected; 0 (default) and positive pass.
func TestValidate_StatementTimeout(t *testing.T) {
	c := validConfig()
	c.StatementTimeoutSeconds = 0
	assert.NoError(t, c.Validate())

	c.StatementTimeoutSeconds = 600
	assert.NoError(t, c.Validate())

	c.StatementTimeoutSeconds = -1
	err := c.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "statement_timeout_seconds")
}

// TestValidate_MissingFields — every REQUIRED field, zeroed one at a time, errors.
func TestValidate_MissingFields(t *testing.T) {
	cases := map[string]func(*Config){
		"pg.host":           func(c *Config) { c.PG.Host = "" },
		"pg.port":           func(c *Config) { c.PG.Port = 0 },
		"pg.port out of range": func(c *Config) { c.PG.Port = 70000 },
		"pg.user":           func(c *Config) { c.PG.User = "" },
		"pg.password":       func(c *Config) { c.PG.Password = "" },
		"pg.database":       func(c *Config) { c.PG.Database = "" },
		"pg.sslmode":        func(c *Config) { c.PG.SSLMode = "" },
		"schema":            func(c *Config) { c.Schema = "" },
		"read_substring":    func(c *Config) { c.Read = "" },
		"write_substring":   func(c *Config) { c.Write = "" },
		"column_types":      func(c *Config) { c.ColumnTypes = nil },
	}
	for hint, mutate := range cases {
		c := validConfig()
		mutate(c)
		err := c.Validate()
		assert.Error(t, err, "expected error when %s is missing", hint)
		if err != nil {
			assert.True(t, strings.Contains(err.Error(), hint) || strings.Contains(err.Error(), "required"),
				"error %q should mention %s", err.Error(), hint)
		}
	}
}

// TestBuildDSN_NoFallbacks — the DSN reflects ONLY YAML values and never leaks the
// repo's hardcoded defaults (the whole point of not reusing pgconfig).
func TestBuildDSN_NoFallbacks(t *testing.T) {
	c := validConfig()
	c.PG.Host = "myhost"
	c.PG.Port = 9999
	c.PG.User = "myuser"
	c.PG.Password = "p@ss:word"
	c.PG.Database = "mydb"
	c.PG.SSLMode = "require"

	dsn := c.buildDSN()

	assert.Contains(t, dsn, "myhost:9999")
	assert.Contains(t, dsn, "myuser")
	assert.Contains(t, dsn, "mydb")
	assert.Contains(t, dsn, "sslmode=require")

	// The dangerous defaults from pkg/config/pgconfig.go must NEVER appear.
	assert.NotContains(t, dsn, "10.120.24.155")
	assert.NotContains(t, dsn, "5432") // we use 9999
	assert.NotContains(t, dsn, "arm_ai_admin")
	assert.NotContains(t, dsn, "wb_data_prod")
}

// TestBuildDSN_PasswordRoundTrip — special chars in the password survive encoding.
func TestBuildDSN_PasswordRoundTrip(t *testing.T) {
	c := validConfig()
	c.PG.Password = "p@ss:/wo?rd"

	u, err := url.Parse(c.buildDSN())
	assert.NoError(t, err)
	pwd, ok := u.User.Password()
	assert.True(t, ok)
	assert.Equal(t, "p@ss:/wo?rd", pwd)
}

// TestBuildDSN_DatabaseInPath — the database is the URL path.
func TestBuildDSN_DatabaseInPath(t *testing.T) {
	c := validConfig()
	c.PG.Database = "weird-db"
	u, err := url.Parse(c.buildDSN())
	assert.NoError(t, err)
	assert.Equal(t, "/weird-db", u.Path)
}
