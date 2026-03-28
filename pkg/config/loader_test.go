package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadYAML_ValidStruct(t *testing.T) {
	content := `
name: test-app
port: 8080
enabled: true
`
	path := filepath.Join(t.TempDir(), "test.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	var cfg struct {
		Name    string `yaml:"name"`
		Port    int    `yaml:"port"`
		Enabled bool   `yaml:"enabled"`
	}
	if err := LoadYAML(path, &cfg); err != nil {
		t.Fatalf("LoadYAML() error = %v", err)
	}

	if cfg.Name != "test-app" {
		t.Errorf("Name = %q, want %q", cfg.Name, "test-app")
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want %d", cfg.Port, 8080)
	}
	if !cfg.Enabled {
		t.Errorf("Enabled = false, want true")
	}
}

func TestLoadYAML_ENVExpansion(t *testing.T) {
	t.Setenv("TEST_LOADER_KEY", "secret123")

	content := `
api_key: ${TEST_LOADER_KEY}
host: ${TEST_LOADER_HOST:localhost}
`
	path := filepath.Join(t.TempDir(), "env.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	var cfg struct {
		APIKey string `yaml:"api_key"`
		Host   string `yaml:"host"`
	}
	if err := LoadYAML(path, &cfg); err != nil {
		t.Fatalf("LoadYAML() error = %v", err)
	}

	if cfg.APIKey != "secret123" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "secret123")
	}
	// Unset env var expands to empty string (not the :default syntax, that's shell not os.ExpandEnv)
	if cfg.Host != "" {
		t.Errorf("Host = %q, want empty (unset env)", cfg.Host)
	}
}

func TestLoadYAML_MissingFile(t *testing.T) {
	err := LoadYAML("/nonexistent/path/config.yaml", &struct{}{})
	if err == nil {
		t.Fatal("LoadYAML() should return error for missing file")
	}

	want := "config file not found"
	if !contains(err.Error(), want) {
		t.Errorf("error = %q, want to contain %q", err.Error(), want)
	}
}

func TestLoadYAML_InvalidYAML(t *testing.T) {
	content := `
invalid: [yaml
  broken: {
`
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	err := LoadYAML(path, &struct{}{})
	if err == nil {
		t.Fatal("LoadYAML() should return error for invalid YAML")
	}

	want := "parse config"
	if !contains(err.Error(), want) {
		t.Errorf("error = %q, want to contain %q", err.Error(), want)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
