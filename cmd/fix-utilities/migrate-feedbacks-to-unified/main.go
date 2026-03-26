// One-time migration: feedbacks.db + quality_reports.db → unified db/wb-sales.db
//
// ATTACH both old databases and copy tables into the target.
// Safe to run multiple times — uses INSERT OR IGNORE/REPLACE.
//
// Usage:
//
//	go run main.go
//	go run main.go --from-feedbacks ./feedbacks.db --from-quality ./quality_reports.db --to ./db/wb-sales.db
//	go run main.go --dry-run
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	_ "github.com/mattn/go-sqlite3"
)

// repoRoot resolves the repository root directory from the source file location.
// This file lives at cmd/fix-utilities/migrate-feedbacks-to-unified/main.go (4 levels deep).
func repoRoot() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return ".." // fallback
	}
	return filepath.Join(filepath.Dir(filename), "..", "..", "..")
}

func main() {
	// Resolve repo root (3 levels up from this file: cmd/fix-utilities/migrate-feedbacks-to-unified)
	repoRoot := repoRoot()

	fromFeedbacks := flag.String("from-feedbacks", "", "Source feedbacks database (default: download-wb-feedbacks/feedbacks.db)")
	fromQuality := flag.String("from-quality", "", "Source quality reports database (default: analyze-wb-feedbacks/quality_reports.db)")
	toDB := flag.String("to", "", "Target unified database (default: db/wb-sales.db)")
	dryRun := flag.Bool("dry-run", false, "Show what would be migrated without changes")
	flag.Parse()

	// Apply defaults relative to repo root
	if *fromFeedbacks == "" {
		*fromFeedbacks = filepath.Join(repoRoot, "cmd/data-downloaders/download-wb-feedbacks/feedbacks.db")
	}
	if *fromQuality == "" {
		*fromQuality = filepath.Join(repoRoot, "cmd/data-analyzers/analyze-wb-feedbacks/quality_reports.db")
	}
	if *toDB == "" {
		*toDB = filepath.Join(repoRoot, "db/wb-sales.db")
	}

	// Check source files exist
	for _, path := range []string{*fromFeedbacks, *fromQuality} {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			log.Fatalf("Source database not found: %s", path)
		}
	}

	// Ensure target directory exists
	if dir := filepath.Dir(*toDB); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Fatalf("Cannot create target directory: %v", err)
		}
	}

	// Show plan
	fmt.Println("=== DB Migration Plan ===")
	fmt.Printf("  Feedbacks source: %s\n", *fromFeedbacks)
	fmt.Printf("  Quality source:   %s\n", *fromQuality)
	fmt.Printf("  Target database:  %s\n", *toDB)
	fmt.Println()

	// Count source rows
	feedbacksCount, questionsCount := countSourceTables(*fromFeedbacks)
	qualityCount := countSourceTable(*fromQuality, "product_quality_summary")

	fmt.Println("=== Source Data ===")
	fmt.Printf("  Feedbacks:          %d rows\n", feedbacksCount)
	fmt.Printf("  Questions:          %d rows\n", questionsCount)
	fmt.Printf("  Quality summaries:  %d rows\n", qualityCount)
	fmt.Println()

	if feedbacksCount == 0 && questionsCount == 0 && qualityCount == 0 {
		fmt.Println("Nothing to migrate — source databases are empty.")
		return
	}

	if *dryRun {
		fmt.Println("=== DRY RUN — no changes made ===")
		fmt.Println("Run without --dry-run to perform migration.")
		return
	}

	// Open target database and create schema via SalesRepository
	// SalesRepository.initSchema() creates ALL tables including feedbacks, questions, quality.
	schemaRepo, err := sqlite.NewSQLiteSalesRepository(*toDB)
	if err != nil {
		log.Fatalf("Cannot open/create target database: %v", err)
	}
	schemaRepo.Close()

	// Now open directly with database/sql for ATTACH support
	db, err := sql.Open("sqlite3", *toDB)
	if err != nil {
		log.Fatalf("Cannot open target database: %v", err)
	}
	defer db.Close()

	// Attach source databases
	fmt.Println("=== Migrating ===")

	absFeedbacks, _ := filepath.Abs(*fromFeedbacks)
	if _, err := db.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS src_feedbacks", absFeedbacks)); err != nil {
		log.Fatalf("ATTACH feedbacks: %v", err)
	}

	absQuality, _ := filepath.Abs(*fromQuality)
	if _, err := db.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS src_quality", absQuality)); err != nil {
		log.Fatalf("ATTACH quality: %v", err)
	}

	// Migrate feedbacks (INSERT OR IGNORE — don't overwrite existing)
	if feedbacksCount > 0 {
		result, err := db.Exec(`INSERT OR IGNORE INTO feedbacks SELECT * FROM src_feedbacks.feedbacks`)
		if err != nil {
			log.Fatalf("Migrate feedbacks: %v", err)
		}
		affected, _ := result.RowsAffected()
		fmt.Printf("  Feedbacks:   %d/%d rows copied\n", affected, feedbacksCount)
	}

	// Migrate questions (INSERT OR IGNORE)
	if questionsCount > 0 {
		result, err := db.Exec(`INSERT OR IGNORE INTO questions SELECT * FROM src_feedbacks.questions`)
		if err != nil {
			log.Fatalf("Migrate questions: %v", err)
		}
		affected, _ := result.RowsAffected()
		fmt.Printf("  Questions:   %d/%d rows copied\n", affected, questionsCount)
	}

	// Migrate quality summaries (INSERT OR REPLACE — always use latest)
	if qualityCount > 0 {
		result, err := db.Exec(`
			INSERT OR REPLACE INTO product_quality_summary
			SELECT * FROM src_quality.product_quality_summary
		`)
		if err != nil {
			log.Fatalf("Migrate quality summaries: %v", err)
		}
		affected, _ := result.RowsAffected()
		fmt.Printf("  Quality:     %d/%d rows copied\n", affected, qualityCount)
	}

	// Detach
	db.Exec("DETACH DATABASE src_feedbacks")
	db.Exec("DETACH DATABASE src_quality")

	// Verify
	fmt.Println()
	fmt.Println("=== Verification ===")
	var targetFeedbacks, targetQuestions, targetQuality int
	db.QueryRow("SELECT COUNT(*) FROM feedbacks").Scan(&targetFeedbacks)
	db.QueryRow("SELECT COUNT(*) FROM questions").Scan(&targetQuestions)
	db.QueryRow("SELECT COUNT(*) FROM product_quality_summary").Scan(&targetQuality)
	fmt.Printf("  Target feedbacks:  %d\n", targetFeedbacks)
	fmt.Printf("  Target questions:  %d\n", targetQuestions)
	fmt.Printf("  Target quality:    %d\n", targetQuality)
	fmt.Println()
	fmt.Println("  Migration complete!")
}

func countSourceTables(dbPath string) (feedbacks, questions int) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return 0, 0
	}
	defer db.Close()

	// Check if feedbacks table exists
	var exists int
	db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='feedbacks'").Scan(&exists)
	if exists > 0 {
		db.QueryRow("SELECT COUNT(*) FROM feedbacks").Scan(&feedbacks)
	}

	db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='questions'").Scan(&exists)
	if exists > 0 {
		db.QueryRow("SELECT COUNT(*) FROM questions").Scan(&questions)
	}

	return feedbacks, questions
}

func countSourceTable(dbPath, tableName string) int {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return 0
	}
	defer db.Close()

	var exists int
	db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='"+tableName+"'").Scan(&exists)
	if exists == 0 {
		return 0
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM " + tableName).Scan(&count)
	return count
}
