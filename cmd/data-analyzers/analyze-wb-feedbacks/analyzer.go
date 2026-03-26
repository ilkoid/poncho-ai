package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"gopkg.in/yaml.v3"
)

// Prompts holds loaded prompt templates from prompts.yaml.
type Prompts struct {
	Level1 PromptPair `yaml:"level1"`
	Level2 PromptPair `yaml:"level2"`
}

// PromptPair holds system and user prompt templates.
type PromptPair struct {
	System string `yaml:"system"`
	User   string `yaml:"user"`
}

// LoadPrompts reads prompts.yaml from the given path.
func LoadPrompts(path string) (*Prompts, error) {
	data, err := readFile(path)
	if err != nil {
		return nil, fmt.Errorf("read prompts: %w", err)
	}
	var p Prompts
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse prompts: %w", err)
	}
	return &p, nil
}

// Analyzer performs two-level LLM aggregation of product feedbacks.
type Analyzer struct {
	provider  llm.Provider
	prompts   *Prompts
	batchSize int
	chatModel string
	logger    *Logger
	maxRetries int
}

// NewAnalyzer creates a new feedback analyzer.
func NewAnalyzer(provider llm.Provider, prompts *Prompts, batchSize int, chatModel string, logger *Logger) *Analyzer {
	return &Analyzer{
		provider:   provider,
		prompts:    prompts,
		batchSize:  batchSize,
		chatModel:  chatModel,
		logger:     logger,
		maxRetries: 3,
	}
}

// AnalyzeProduct runs the full two-level aggregation pipeline for a product.
// Returns the quality summary text.
func (a *Analyzer) AnalyzeProduct(ctx context.Context, product ProductStats, feedbacks []Feedback) (string, error) {
	if len(feedbacks) == 0 {
		return "", fmt.Errorf("no feedbacks to analyze for nm_id=%d", product.ProductNmID)
	}

	var productLog *ProductLog
	if a.logger != nil {
		productLog = a.logger.StartProduct(product)
	}
	start := time.Now()

	var summary string
	var err error

	if len(feedbacks) <= a.batchSize {
		summary, err = a.analyzeDirect(ctx, product, feedbacks, productLog)
	} else {
		summary, err = a.analyzeTwoLevel(ctx, product, feedbacks, productLog)
	}

	if productLog != nil {
		productLog.DurationMs = time.Since(start).Milliseconds()
		if err != nil {
			productLog.Error = err.Error()
		}
		productLog.Summary = summary
		a.logger.FinishProduct(productLog)
	}

	return summary, err
}

// analyzeDirect sends all feedbacks directly to Level 2 (no batching needed).
func (a *Analyzer) analyzeDirect(ctx context.Context, product ProductStats, feedbacks []Feedback, pl *ProductLog) (string, error) {
	input := formatFeedbacks(feedbacks)
	prompt := renderTemplate(a.prompts.Level2.User, map[string]string{
		"product_name":     product.ProductName,
		"nm_id":            fmt.Sprintf("%d", product.ProductNmID),
		"supplier_article": product.SupplierArticle,
		"avg_rating":       fmt.Sprintf("%.1f", product.AvgRating),
		"feedback_count":   fmt.Sprintf("%d", product.FeedbackCount),
		"input_data":       input,
	})

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: a.prompts.Level2.System},
		{Role: llm.RoleUser, Content: prompt},
	}

	resp, dur, err := a.callLLM(ctx, messages, 2, 0, a.prompts.Level2.System, prompt, pl)
	if err != nil {
		return "", fmt.Errorf("LLM level2 direct nm_id=%d: %w", product.ProductNmID, err)
	}

	_ = dur // duration recorded in pl via callLLM
	return resp, nil
}

// analyzeTwoLevel runs Level 1 (batch aggregation) then Level 2 (final summary).
func (a *Analyzer) analyzeTwoLevel(ctx context.Context, product ProductStats, feedbacks []Feedback, pl *ProductLog) (string, error) {
	// Level 1: batch aggregation
	var aggregatedThemes []string
	for i := 0; i < len(feedbacks); i += a.batchSize {
		end := i + a.batchSize
		if end > len(feedbacks) {
			end = len(feedbacks)
		}
		batch := feedbacks[i:end]
		batchNum := i/a.batchSize + 1

		prompt := renderTemplate(a.prompts.Level1.User, map[string]string{
			"product_name": product.ProductName,
			"nm_id":        fmt.Sprintf("%d", product.ProductNmID),
			"feedbacks":    formatFeedbacks(batch),
		})

		messages := []llm.Message{
			{Role: llm.RoleSystem, Content: a.prompts.Level1.System},
			{Role: llm.RoleUser, Content: prompt},
		}

		resp, _, err := a.callLLM(ctx, messages, 1, batchNum, a.prompts.Level1.System, prompt, pl)
		if err != nil {
			return "", fmt.Errorf("LLM level1 batch %d nm_id=%d: %w", batchNum, product.ProductNmID, err)
		}
		aggregatedThemes = append(aggregatedThemes, resp)
	}

	// Level 2: final summary from aggregated themes
	combinedThemes := strings.Join(aggregatedThemes, "\n\n---\n\n")
	prompt := renderTemplate(a.prompts.Level2.User, map[string]string{
		"product_name":     product.ProductName,
		"nm_id":            fmt.Sprintf("%d", product.ProductNmID),
		"supplier_article": product.SupplierArticle,
		"avg_rating":       fmt.Sprintf("%.1f", product.AvgRating),
		"feedback_count":   fmt.Sprintf("%d", product.FeedbackCount),
		"input_data":       combinedThemes,
	})

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: a.prompts.Level2.System},
		{Role: llm.RoleUser, Content: prompt},
	}

	resp, _, err := a.callLLM(ctx, messages, 2, 0, a.prompts.Level2.System, prompt, pl)
	if err != nil {
		return "", fmt.Errorf("LLM level2 nm_id=%d: %w", product.ProductNmID, err)
	}
	return resp, nil
}

// callLLM wraps provider.Generate with timing, retry, and optional logging.
func (a *Analyzer) callLLM(ctx context.Context, messages []llm.Message, level, batch int, systemPrompt, userPrompt string, pl *ProductLog) (string, int64, error) {
	var lastErr error
	var totalDur int64

	for attempt := 0; attempt <= a.maxRetries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(attempt) * 5 * time.Second
			log.Printf("    Retry %d/%d after %v (error: %v)", attempt, a.maxRetries, wait, lastErr)
			select {
			case <-ctx.Done():
				return "", totalDur, ctx.Err()
			case <-time.After(wait):
			}
		}

		start := time.Now()
		resp, err := a.provider.Generate(ctx, messages)
		dur := time.Since(start).Milliseconds()
		totalDur += dur

		if pl != nil {
			callLog := LLMCallLog{
				Level:        level,
				Batch:        batch,
				SystemPrompt: systemPrompt,
				UserPrompt:   userPrompt,
				DurationMs:   dur,
				Attempt:      attempt,
			}
			if err != nil {
				callLog.Error = err.Error()
			} else {
				callLog.Response = resp.Content
			}
			pl.LLMCalls = append(pl.LLMCalls, callLog)
		}

		if err == nil {
			return resp.Content, totalDur, nil
		}

		// Don't retry on context cancellation
		if ctx.Err() != nil {
			return "", totalDur, err
		}
		lastErr = err
	}

	return "", totalDur, lastErr
}

// formatFeedbacks converts feedbacks to a compact text representation for LLM.
func formatFeedbacks(feedbacks []Feedback) string {
	var b strings.Builder
	for i, f := range feedbacks {
		b.WriteString(fmt.Sprintf("%d. Рейтинг: %d/5", i+1, f.ProductValuation))
		if f.Text != "" {
			b.WriteString(fmt.Sprintf(" | Отзыв: %s", f.Text))
		}
		if f.Pros != "" {
			b.WriteString(fmt.Sprintf(" | Плюсы: %s", f.Pros))
		}
		if f.Cons != "" {
			b.WriteString(fmt.Sprintf(" | Минусы: %s", f.Cons))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderTemplate replaces {key} placeholders in a template string.
func renderTemplate(tmpl string, vars map[string]string) string {
	result := tmpl
	for key, val := range vars {
		result = strings.ReplaceAll(result, "{"+key+"}", val)
	}
	return result
}
