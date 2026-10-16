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

// Регрессия аудита 11.10 (обновлена 16.10): get-bids имеет свой ключ
// normquery_bids, отдельный от normquery. Спека 12.10: 5 req/sec = 300/min
// burst 10 (08-promotion.yaml:2521-2526; «2/сек ужесточение 11.10» устарело) —
// дефолт ровно сваггер; охрана прежняя: старые конфиги без ключа не должны
// получать норму выше сваггера (наследование normquery=300 теперь ему равно,
// но ключ остаётся отдельным на случай нового ужесточения спеки).
func TestPromotionV2Defaults_NormqueryBids(t *testing.T) {
	cfg := (&PromotionV2Config{}).GetDefaults()
	if cfg.RateLimits.NormqueryBids != 300 || cfg.RateLimits.NormqueryBidsApi != 300 {
		t.Fatalf("normquery_bids default = %d/%d, хочу 300/300 (swagger 5/sec)", cfg.RateLimits.NormqueryBids, cfg.RateLimits.NormqueryBidsApi)
	}
	if cfg.RateLimits.NormqueryBidsBurst != 10 || cfg.RateLimits.NormqueryBidsApiBurst != 10 {
		t.Fatalf("normquery_bids burst = %d/%d, хочу 10/10 (swagger)", cfg.RateLimits.NormqueryBidsBurst, cfg.RateLimits.NormqueryBidsApiBurst)
	}
	// Явное значение в конфиге не затирается дефолтом.
	cfg = (&PromotionV2Config{RateLimits: PromotionV2RateLimits{NormqueryBids: 60, NormqueryBidsBurst: 2}}).GetDefaults()
	if cfg.RateLimits.NormqueryBids != 60 || cfg.RateLimits.NormqueryBidsBurst != 2 {
		t.Fatalf("явный normquery_bids затёрт дефолтом: %d/%d", cfg.RateLimits.NormqueryBids, cfg.RateLimits.NormqueryBidsBurst)
	}
}
