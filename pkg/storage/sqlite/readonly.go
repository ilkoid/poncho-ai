package sqlite

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// OpenReadOnly opens SQLite in read-only mode with optimized PRAGMAs.
// Use for dashboard and analyzer read-only access.
func OpenReadOnly(dbPath string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_journal_mode=WAL&_cache_size=-65536&_busy_timeout=10000", dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)

	for _, p := range []string{
		"PRAGMA mmap_size = 268435456",
		"PRAGMA temp_store = MEMORY",
	} {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %s: %w", p, err)
		}
	}
	return db, nil
}
