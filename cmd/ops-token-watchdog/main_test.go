package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// fakeJWT собирает валидный по форме JWT с заданным exp (unix-секунды).
func fakeJWT(t *testing.T, exp int64) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"exp": exp, "uid": 42})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	return "eyJhbGciOiJFUzI1NiJ9." + enc + ".sig"
}

func TestParseEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "# комментарий\n" +
		"\n" +
		"export WB_API_KEY='eyJone.two.three'\n" +
		"export PG_PWD='secret!@#$'\n" +
		"export WB_BAD=\"double-quoted\"\n" +
		"голая мусорная строка\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	vars, skipped, err := parseEnvFile(path)
	if err != nil {
		t.Fatalf("parseEnvFile: %v", err)
	}
	if len(vars) != 2 {
		t.Fatalf("vars: хочу 2 канонические строки, got %d (%v)", len(vars), vars)
	}
	if vars[0].name != "WB_API_KEY" || vars[0].value != "eyJone.two.three" {
		t.Errorf("vars[0] = %+v", vars[0])
	}
	if vars[1].name != "PG_PWD" || vars[1].value != "secret!@#$" {
		t.Errorf("vars[1] = %+v", vars[1])
	}
	if skipped != 2 { // двойные кавычки + мусор
		t.Errorf("skipped: хочу 2, got %d", skipped)
	}
}

func TestParseEnvFileMissing(t *testing.T) {
	if _, _, err := parseEnvFile(filepath.Join(t.TempDir(), "no-such.env")); err == nil {
		t.Fatal("хочу ошибку для отсутствующего файла")
	}
}

func TestJwtExp(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{"валидный с exp", fakeJWT(t, now.Add(30*24*time.Hour).Unix()), false},
		{"без exp", "eyJhbGciOiJFUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte(`{"uid":1}`)) + ".sig", true},
		{"битый base64", "eyJh.!!!.sig", true},
		{"не JWT", "abc123hexkey", true},
		{"payload не JSON", "eyJh.garbage.sig", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := jwtExp(c.token)
			if (err != nil) != c.wantErr {
				t.Fatalf("jwtExp err=%v, wantErr=%v", err, c.wantErr)
			}
		})
	}
}

func TestJwtExpValue(t *testing.T) {
	want := int64(1800000000)
	got, err := jwtExp(fakeJWT(t, want))
	if err != nil {
		t.Fatalf("jwtExp: %v", err)
	}
	if got != want {
		t.Fatalf("exp: хочу %d, got %d", want, got)
	}
}

func TestClassifyToken(t *testing.T) {
	// Фиксированная зона МСК для детерминизма (канон main_test.go:56).
	msk := time.FixedZone("MSK", 3*3600)
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, msk)
	day := 24 * time.Hour
	cases := []struct {
		name  string
		value string
		want  tokenStatus
	}{
		{"живой на 180 дней", fakeJWT(t, now.Add(180*day).Unix()), statusOK},
		{"на 10 дней", fakeJWT(t, now.Add(10*day).Unix()), statusRemind},
		{"на 5 дней", fakeJWT(t, now.Add(5*day).Unix()), statusAlert},
		{"истёк вчера", fakeJWT(t, now.Add(-day).Unix()), statusExpired},
		{"истёк месяц назад", fakeJWT(t, now.Add(-30*day).Unix()), statusExpired},
		{"старый hex-формат", "abcdef0123456789abcdef0123456789", statusNoJWT},
		{"пустой", "", statusEmpty},
		{"битый eyJ", "eyJbroken.broken.broken", statusBroken},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row := classifyToken("WB_X", c.value, now, 7, 14)
			if row.status != c.want {
				t.Fatalf("classifyToken(%s): хочу %s, got %s (reason=%q)",
					c.name, c.want, row.status, row.reason)
			}
		})
	}
}

func TestScanTodayLogs(t *testing.T) {
	dir := t.TempDir()
	today := filepath.Join(dir, "download-all-2026-11-19.log")
	yesterday := filepath.Join(dir, "download-all-2026-11-18.log")

	withOrigin := `2026/11/18 error: wb api error: status 401, body: {"status":401,"detail":"token is expired","origin":"ag-advert"}`
	if err := os.WriteFile(today, []byte("ok line\n"+withOrigin+"\nok\n"+withOrigin+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yesterday, []byte(withOrigin+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// mtime вчерашнего — до полуночи, сегодняшнего — после.
	midnight := time.Date(2026, 11, 19, 0, 0, 0, 0, time.Local)
	if err := os.Chtimes(yesterday, midnight.Add(-time.Hour), midnight.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(today, midnight.Add(time.Hour), midnight.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	globs := []string{filepath.Join(dir, "download-all-*.log")}
	got := scanTodayLogs(globs, midnight)
	if got.total != 2 {
		t.Fatalf("total: хочу 2 (только сегодняшний файл), got %d (perFile=%v)", got.total, got.perFile)
	}
	if len(got.origins) != 1 || got.origins[0] != "ag-advert" {
		t.Fatalf("origins: хочу [ag-advert], got %v", got.origins)
	}
	if got.perFile[today] != 2 {
		t.Errorf("perFile[today]: хочу 2, got %d", got.perFile[today])
	}
}

// TestConfigFileReal — реальный config.yaml утилиты (канон TestConfigFileLetter
// из ops-drain-report-quota): ловит дрейф схемы/значений между кодом и yaml.
func TestConfigFileReal(t *testing.T) {
	var cfg Config
	if err := config.LoadYAML("config.yaml", &cfg); err != nil {
		t.Fatalf("LoadYAML(config.yaml): %v", err)
	}
	cfg.applyDefaults()
	if cfg.Watchdog.EnvPath != ".env" {
		t.Errorf("env_path: хочу .env, got %q", cfg.Watchdog.EnvPath)
	}
	if cfg.Watchdog.AlertDays != 7 {
		t.Errorf("alert_days: хочу 7, got %d", cfg.Watchdog.AlertDays)
	}
	if cfg.Watchdog.RemindDays != 14 {
		t.Errorf("remind_days: хочу 14, got %d", cfg.Watchdog.RemindDays)
	}
	if len(cfg.Watchdog.ScanLogGlobs) == 0 {
		t.Error("scan_log_globs пуст")
	}
	for _, v := range cfg.Watchdog.IgnoreVars {
		if v == "" {
			t.Error("ignore_vars содержит пустое имя")
		}
	}
}
