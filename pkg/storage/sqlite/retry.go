package sqlite

import (
	"context"
	"strings"
	"time"
)

// isBusyError checks if the error is a SQLite "database is locked" error.
// This happens when multiple processes compete for the write lock.
func isBusyError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "database is locked")
}

// retryOnBusy retries fn on SQLite "database is locked" errors with exponential backoff.
// Max 5 attempts: 100ms, 200ms, 400ms, 800ms, 1600ms total ~3s.
// Respects context cancellation between retries.
func retryOnBusy(ctx context.Context, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			backoff := 100 * time.Millisecond * time.Duration(1<<(attempt-1))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
		lastErr = fn()
		if lastErr == nil || !isBusyError(lastErr) {
			return lastErr
		}
	}
	return lastErr
}
