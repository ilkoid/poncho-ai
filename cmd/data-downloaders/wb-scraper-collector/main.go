// wb-scraper-collector is the Go half of the WB storefront competitor-scraper:
// a loopback HTTP server that hands targets to the wb-scraper browser extension
// (GET /targets) and accepts the WB responses it intercepts (POST /capture),
// decoding them into SQLite/PostgreSQL. The extension — not this binary — makes
// the storefront requests from a real, logged-in browser (the anti-bot bypass).
//
// This is a thin CLI driver (Rule 6): config + DI + lifecycle live here; the server,
// decode, query constructor, and storage are in pkg/wbscraper and pkg/storage.
//
// Usage:
//
//	# live (extension pulls targets + pushes captures → /tmp DB)
//	go run . --config cmd/.configs/download-all/wb-scraper-collector.yaml \
//	  --backend sqlite --db /tmp/wbscraper.db --addr 127.0.0.1:7780
//
//	# mock: transport + decode exercised, ZERO DB interaction (DiscardWriter)
//	go run . --mock --addr 127.0.0.1:7780
//
//	# dry-run: decode → printed JSON batches, no persistence
//	go run . --dry-run --addr 127.0.0.1:7780
//
// ⚠️ Database safety (CLAUDE.md): always /tmp for tests, NEVER /var/db (READ-ONLY in
// any mode). --mock/--dry-run open no database at all (DiscardWriter).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wbscraper"
)

func main() {
	configPath := flag.String("config", "cmd/.configs/download-all/wb-scraper-collector.yaml", "Path to wb-scraper-collector.yaml")
	backend := flag.String("backend", "", "Storage backend: sqlite|postgres (overrides config)")
	dbPath := flag.String("db", "", "SQLite database path (overrides config; use /tmp, never /var/db)")
	pgDatabase := flag.String("pg-database", "", "PostgreSQL database name (overrides config)")
	generatorKind := flag.String("generator", "", "Target source: static|llm (overrides config)")
	addrFlag := flag.String("addr", "", "Listen address host:port (overrides config server.host:port)")
	mockMode := flag.Bool("mock", false, "DiscardWriter: exercise transport/decode with ZERO DB writes")
	dryRun := flag.Bool("dry-run", false, "Decode and print batches to stdout, no persistence")
	reportOnly := flag.Bool("report-only", false, "Generate the HTML report from an existing DB (Stage 7)")
	flag.Parse()

	start := time.Now()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	cfg.Storage = cfg.Storage.GetDefaults()

	// CLI flag overrides (flag > config > default).
	if *backend != "" {
		cfg.Storage.Backend = *backend
	}
	if *dbPath != "" {
		cfg.Storage.DbPath = *dbPath
	}
	if *pgDatabase != "" {
		cfg.Storage.PgDatabase = *pgDatabase
	}
	if *generatorKind != "" {
		cfg.Generator.Kind = *generatorKind
	}

	dllog.PrintHeader("WB-Scraper Collector",
		dllog.HeaderField{Key: "Config", Value: *configPath},
		dllog.HeaderField{Key: "Backend", Value: cfg.Storage.Backend},
		dllog.HeaderField{Key: "DB", Value: cfg.Storage.DisplayDB()},
		dllog.HeaderField{Key: "Generator", Value: cfg.Generator.Kind},
		dllog.HeaderField{Key: "Addr", Value: addrOrConfig(*addrFlag, cfg.Server)},
		dllog.HeaderField{Key: "AllowedIPs", Value: allowedIPsLabel(cfg.Server.AllowedIPs)},
		dllog.HeaderField{Key: "Mock", Value: fmt.Sprintf("%v", *mockMode)},
		dllog.HeaderField{Key: "DryRun", Value: fmt.Sprintf("%v", *dryRun)},
	)

	// --report-only reads an existing DB and emits the HTML report; no server.
	// The report itself lands in Stage 7 — for now the flag is wired so the CLI
	// surface is stable (contract freeze), with an explicit not-implemented notice.
	if *reportOnly {
		dllog.Log("report-only: HTML report generation is Stage 7 (not implemented in this build)")
		return
	}

	// ⚠️ Mock/dry-run safety — Writer creation goes INSIDE the else branch:
	// --mock and --dry-run never open a database (DiscardWriter). This is the
	// critical safety pattern from CLAUDE.md "V2 Downloader Architecture".
	var writer wbscraper.Writer
	var cleanup func()
	if *mockMode || *dryRun {
		writer = wbscraper.NewDiscardWriter()
		cleanup = func() {}
	} else {
		var err error
		writer, cleanup, err = createWbscraperWriter(ctx, cfg.Storage)
		if err != nil {
			log.Fatalf("storage: %v", err)
		}
	}
	defer cleanup()

	// Build the target list: the config's `targets` override wins, else the
	// selected QueryGenerator (static constructor, or the LLM stub).
	targets, err := buildTargets(ctx, *cfg)
	if err != nil {
		log.Fatalf("targets: %v", err)
	}

	opts := wbscraper.ServerOptions{
		Snapshot:      wbscraper.SnapshotTs(time.Now().UTC().Format(time.RFC3339)),
		BatchTargets:  cfg.Collect.BatchTargets,
		FlushInterval: mustDuration("collect.flush_interval", cfg.Collect.FlushInterval),
		ReadTimeout:   mustDuration("server.read_timeout", cfg.Server.ReadTimeout),
		WriteTimeout:  mustDuration("server.write_timeout", cfg.Server.WriteTimeout),
		DryRun:        *dryRun,
		AllowedIPs:    cfg.Server.AllowedIPs,
	}

	addr := addrOrConfig(*addrFlag, cfg.Server)
	srv, err := wbscraper.NewServer(ctx, addr, writer, targets, opts)
	if err != nil {
		log.Fatalf("server: %v", err)
	}

	dllog.Log("listening on http://%s (Ctrl-C to stop, final flush on exit)", addr)
	if err := srv.Run(ctx); err != nil {
		log.Fatalf("run: %v", err)
	}
	dllog.Done(time.Since(start), "session %s complete", srv.SessionID())
}

// createWbscraperWriter builds the storage Writer for the configured backend. PG
// gets a focused PgWbscraperRepo (InitSchema creates the tables); SQLite reuses the
// monolithic SQLiteSalesRepository, whose initSchema() includes the wbscraper tables
// (registered in Stage 3). Returns a cleanup func the caller defers.
func createWbscraperWriter(ctx context.Context, cfg config.V2StorageConfig) (wbscraper.Writer, func(), error) {
	switch cfg.Backend {
	case "postgres", "postgresql":
		dsn, err := cfg.GetEffectiveDSN()
		if err != nil {
			return nil, func() {}, fmt.Errorf("postgres DSN: %w", err)
		}
		pool, err := postgres.NewPool(ctx, dsn)
		if err != nil {
			return nil, func() {}, fmt.Errorf("postgres pool: %w", err)
		}
		repo := postgres.NewPgWbscraperRepo(pool.DB())
		if err := repo.InitSchema(ctx); err != nil {
			pool.Close()
			return nil, func() {}, fmt.Errorf("postgres schema: %w", err)
		}
		return repo, pool.Close, nil

	default: // "sqlite"
		repo, err := sqlite.NewSQLiteSalesRepository(cfg.DbPath)
		if err != nil {
			return nil, func() {}, fmt.Errorf("open SQLite: %w", err)
		}
		return repo, func() { repo.Close() }, nil
	}
}

// buildTargets resolves the work queue. Config.Targets (debug override) wins and is
// classified per entry: an all-digits string is an nmId → card target, otherwise a
// search query. With no override, the configured generator (static constructor, or
// the LLM stub) produces the list.
func buildTargets(ctx context.Context, cfg wbscraper.Config) ([]wbscraper.Target, error) {
	if len(cfg.Targets) > 0 {
		out := make([]wbscraper.Target, 0, len(cfg.Targets))
		for _, t := range cfg.Targets {
			out = append(out, targetFromString(t))
		}
		return out, nil
	}

	var gen wbscraper.QueryGenerator
	switch cfg.Generator.Kind {
	case "llm":
		gen = wbscraper.NewLLMGenerator()
	default:
		gen = wbscraper.NewStaticGenerator(cfg.Generator.Constructor)
	}
	targets, err := gen.Generate(ctx)
	if err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}
	return targets, nil
}

// targetFromString classifies one debug target: all-digits → card (nmId), else a
// search query. Both use the package's URL helpers so the format lives in one place.
func targetFromString(s string) wbscraper.Target {
	if isAllDigits(s) {
		return wbscraper.Target{
			Kind:    "card",
			QueryID: wbscraper.NoQuery,
			URL:     wbscraper.CardURL(s),
		}
	}
	return wbscraper.Target{
		Kind:    "search",
		QueryID: wbscraper.NoQuery,
		Query:   s,
		URL:     wbscraper.SearchURL(s),
	}
}

// isAllDigits reports whether s is non-empty and entirely ASCII digits (an nmId).
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// addrOrConfig returns the --addr flag if set, else host:port from the config.
func addrOrConfig(addrFlag string, srv wbscraper.ServerConfig) string {
	if addrFlag != "" {
		return addrFlag
	}
	return fmt.Sprintf("%s:%d", srv.Host, srv.Port)
}

// allowedIPsLabel renders the IP allowlist for the startup header: "all" when empty
// (the allow-all loopback default — surfaced loudly so a wide binding is visible),
// otherwise the comma-joined entries.
func allowedIPsLabel(ips []string) string {
	if len(ips) == 0 {
		return "all (empty allowlist — loopback only)"
	}
	return strings.Join(ips, ", ")
}

// mustDuration parses a config duration string, failing fast on a malformed value
// (a typo like "2sec" should stop startup, not silently zero the timeout).
func mustDuration(field, s string) time.Duration {
	d, err := wbscraper.ParseDuration(field, s)
	if err != nil {
		log.Fatalf("%s", err)
	}
	return d
}

// loadConfig reads the YAML and applies package defaults. Storage env-overrides
// are applied later via GetDefaults at the call site (they are environment-dependent).
func loadConfig(path string) (*wbscraper.Config, error) {
	var raw wbscraper.Config
	if err := config.LoadYAML(path, &raw); err != nil {
		return nil, err
	}
	cfg := raw.Defaults()
	return &cfg, nil
}
