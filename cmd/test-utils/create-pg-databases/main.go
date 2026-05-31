// create-pg-databases creates PostgreSQL databases for Poncho AI.
//
// Connects to the maintenance database and creates:
//   - wb_data_prod — production data
//   - wb_data_test — test data
//
// Idempotent: checks pg_database before CREATE.
//
// Usage:
//
//	PG_PWD=password go run ./cmd/test-utils/create-pg-databases/
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultHost = "192.168.10.7"
	defaultPort = "15432"
	defaultUser = "postgres"
	pwdEnvVar   = "PG_PWD"

	maintenanceDB = "postgres"
)

var databases = []string{"wb_data_prod", "wb_data_test"}

func main() {
	ctx := context.Background()

	pwd := os.Getenv(pwdEnvVar)
	if pwd == "" {
		fmt.Fprintf(os.Stderr, "❌ %s environment variable is required\n", pwdEnvVar)
		os.Exit(1)
	}

	host := envOrDefault("PGHOST", defaultHost)
	port := envOrDefault("PGPORT", defaultPort)

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		defaultUser, pwd, host, port, maintenanceDB)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ connect to maintenance DB: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "❌ ping: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Connected to %s:%s\n", host, port)

	for _, db := range databases {
		if err := createDatabaseIfNotExists(ctx, pool, db); err != nil {
			fmt.Fprintf(os.Stderr, "❌ create %s: %v\n", db, err)
			os.Exit(1)
		}
	}

	fmt.Println("✅ All databases ready")
}

func createDatabaseIfNotExists(ctx context.Context, pool *pgxpool.Pool, dbName string) error {
	// Check if database already exists
	var exists bool
	err := pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", dbName,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check existence: %w", err)
	}

	if exists {
		fmt.Printf("  ⏭️  %s already exists\n", dbName)
		return nil
	}

	// CREATE DATABASE cannot be run inside a transaction
	_, err = pool.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s ENCODING 'UTF8'", dbName))
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}

	fmt.Printf("  ✅ %s created\n", dbName)
	return nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
