package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AnalysisLog represents a complete analysis run saved as JSON.
type AnalysisLog struct {
	RunID    string       `json:"run_id"`
	Timestamp time.Time    `json:"timestamp"`
	Config   LogConfig     `json:"config"`
	Products []ProductLog `json:"products"`
	Summary  *LogSummary   `json:"summary,omitempty"`
}

// LogConfig records analysis configuration for the log.
type LogConfig struct {
	Period    string `json:"period"`
	Model     string `json:"model"`
	Thinking  string `json:"thinking,omitempty"`
	BatchSize int    `json:"batch_size"`
	Subject   string `json:"subject,omitempty"`
	NmIDsFile string `json:"nm_ids_file,omitempty"`
}

// ProductLog records analysis details for a single product.
type ProductLog struct {
	NmID           int           `json:"nm_id"`
	SupplierArticle string       `json:"supplier_article"`
	ProductName    string       `json:"product_name"`
	AvgRating      float64      `json:"avg_rating"`
	FeedbackCount  int          `json:"feedback_count"`
	LLMCalls       []LLMCallLog `json:"llm_calls"`
	Summary        string       `json:"summary,omitempty"`
	DurationMs     int64        `json:"duration_ms"`
	Error          string       `json:"error,omitempty"`
}

// LLMCallLog records a single LLM call with full request/response details.
type LLMCallLog struct {
	Level         int    `json:"level"`             // 1 = batch aggregation, 2 = final summary
	Batch         int    `json:"batch,omitempty"`   // batch number (level 1 only)
	SystemPrompt  string `json:"system_prompt"`     // full system prompt
	UserPrompt    string `json:"user_prompt"`        // full user prompt (rendered)
	Response      string `json:"response"`           // LLM response text
	DurationMs    int64  `json:"duration_ms"`
	Error         string `json:"error,omitempty"`
	Attempt       int    `json:"attempt,omitempty"` // retry attempt (0 = first try)
}

// LogSummary records aggregate stats for the run.
type LogSummary struct {
	TotalProducts   int   `json:"total_products"`
	Analyzed        int   `json:"analyzed"`
	Skipped         int   `json:"skipped"`
	Errors          int   `json:"errors"`
	TotalLLMCalls   int   `json:"total_llm_calls"`
	TotalDurationMs int64 `json:"total_duration_ms"`
}

// Logger records analysis details to a JSON file incrementally.
// After each product is analyzed, the log file is updated.
type Logger struct {
	mu      sync.Mutex
	log     AnalysisLog
	logsDir string
}

// NewLogger creates a new logger. If logsDir is empty, logging is disabled.
func NewLogger(logsDir string) (*Logger, error) {
	if logsDir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, fmt.Errorf("create logs dir: %w", err)
	}
	return &Logger{
		log: AnalysisLog{
			RunID:    fmt.Sprintf("analyze_%s", time.Now().Format("20060102_150405")),
			Timestamp: time.Now(),
		},
		logsDir: logsDir,
	}, nil
}

// FilePath returns the path to the current log file.
func (l *Logger) FilePath() string {
	if l == nil {
		return ""
	}
	return filepath.Join(l.logsDir, l.log.RunID+".json")
}

// SetConfig records the analysis configuration.
func (l *Logger) SetConfig(cfg LogConfig) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.log.Config = cfg
	l.mu.Unlock()
}

// StartProduct begins logging for a product. Returns a ProductLog to fill.
func (l *Logger) StartProduct(product ProductStats) *ProductLog {
	if l == nil {
		return nil
	}
	pl := &ProductLog{
		NmID:           product.ProductNmID,
		SupplierArticle: product.SupplierArticle,
		ProductName:    product.ProductName,
		AvgRating:      product.AvgRating,
		FeedbackCount:  product.FeedbackCount,
	}
	return pl
}

// FinishProduct adds a completed product log and flushes to disk immediately.
func (l *Logger) FinishProduct(pl *ProductLog) {
	if l == nil || pl == nil {
		return
	}
	l.mu.Lock()
	l.log.Products = append(l.log.Products, *pl)
	l.flushLocked()
	l.mu.Unlock()
}

// Finalize writes the final log with summary to disk and returns the path.
func (l *Logger) Finalize(summary LogSummary) (string, error) {
	if l == nil {
		return "", nil
	}
	l.mu.Lock()
	l.log.Summary = &summary
	l.flushLocked()
	l.mu.Unlock()
	return l.FilePath(), nil
}

// flushLocked writes the current log to disk. Must be called with l.mu held.
func (l *Logger) flushLocked() {
	data, err := json.MarshalIndent(l.log, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(l.FilePath(), data, 0644)
}
