// Command fix-scrub-substring replaces every case-insensitive occurrence of
// read_substring with write_substring across all text columns of every base table
// in a PostgreSQL schema. Fully YAML-driven; crashes on any missing required
// config value. See .claude/plans/fix-polymorphic-sprout.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5"

	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
)

func main() {
	configPath := flag.String("config", "config.yaml", "YAML config path")
	mock := flag.Bool("mock", false, "No DB connection; print a deterministic synthetic plan")
	show := flag.Bool("show", false, "Read-only scan: which tables/columns match + counts")
	dryRun := flag.Bool("dry-run", false, "Connect, compute exact UPDATEs + samples, NO writes")
	apply := flag.Bool("apply", false, "Execute the scrub UPDATEs (DESTRUCTIVE, single transaction)")
	selectTables := flag.String("select_tables", "", "Comma-separated table subset (any mode)")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	selectTbls := parseSelectTables(*selectTables)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := dispatch(ctx, cfg, *mock, *show, *dryRun, *apply, selectTbls); err != nil {
		log.Fatalf("%v", err)
	}
}

// dispatch routes to the selected mode. --mock wins and never opens a pool.
func dispatch(ctx context.Context, cfg *Config, mock, show, dryRun, apply bool, selectTables []string) error {
	switch {
	case mock:
		printMock(cfg, selectTables)
		return nil
	case show:
		return runReadOnly(ctx, cfg, selectTables, false)
	case dryRun:
		return runReadOnly(ctx, cfg, selectTables, true)
	case apply:
		return runApply(ctx, cfg, selectTables)
	default:
		return fmt.Errorf("specify one of --show, --dry-run, --apply (or --mock for an offline demo)")
	}
}

// runReadOnly powers --show (withSamples=false) and --dry-run (withSamples=true).
// All reads run inside a READ ONLY transaction.
func runReadOnly(ctx context.Context, cfg *Config, selectTables []string, withSamples bool) error {
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	s := newScrubber(pool.DB(), cfg, selectTables)

	var updates []Update
	if err := s.withReadOnlyTx(ctx, func(tx pgx.Tx) error {
		targets, err := s.discoverTargets(ctx, tx)
		if err != nil {
			return err
		}
		groups := groupTargetsByTable(targets)
		updates, err = collectMatches(ctx, s, tx, groups, withSamples)
		return err
	}); err != nil {
		return err
	}

	if withSamples {
		printDryRun(cfg.Schema, cfg.Read, cfg.Write, updates)
	} else {
		printShow(cfg.Schema, cfg.Read, cfg.IncludeTables, selectTables, cfg.ExcludeTables, updates)
	}
	return nil
}

// runApply executes the scrub. Discovery runs on the pool; writes run in one tx.
func runApply(ctx context.Context, cfg *Config, selectTables []string) error {
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	db := pool.DB()
	s := newScrubber(db, cfg, selectTables)

	targets, err := s.discoverTargets(ctx, db)
	if err != nil {
		return err
	}
	groups := groupTargetsByTable(targets)

	printApplyBegin(cfg.Schema, cfg.Read, cfg.Write)
	total, touched, err := s.apply(ctx, groups)
	if err != nil {
		return err
	}
	printApplyDone(total, touched)
	return nil
}

// collectMatches counts matches per table group in a single scan each (and gathers
// samples when requested), keeping only columns with at least one match. The output
// is per-column so --show/--dry-run keep their familiar shape; only the underlying
// scans are consolidated to one per table.
func collectMatches(ctx context.Context, s *Scrubber, q queryer, groups []TableGroup, withSamples bool) ([]Update, error) {
	var updates []Update
	for _, g := range groups {
		counts, err := s.countTable(ctx, q, g)
		if err != nil {
			return nil, err
		}
		for i, t := range g.Cols {
			n := counts[i]
			if n == 0 {
				continue
			}
			u := Update{Target: t, Matches: n}
			if withSamples {
				if u.Samples, err = s.samples(ctx, q, t); err != nil {
					return nil, err
				}
			}
			updates = append(updates, u)
		}
	}
	return updates, nil
}

// openPool connects to PostgreSQL using ONLY YAML values (no pgconfig defaults).
func openPool(ctx context.Context, cfg *Config) (*postgres.Pool, error) {
	pool, err := postgres.NewPool(ctx, cfg.buildDSN())
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	return pool, nil
}

// parseSelectTables splits a comma-separated --select_tables value. Returns nil
// (not an empty slice) when there is nothing to filter on — callers treat nil as
// "no constraint".
func parseSelectTables(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
