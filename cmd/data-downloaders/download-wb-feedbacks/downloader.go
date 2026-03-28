// Package main provides download logic for WB Feedbacks API.
package main

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const (
	// Feedbacks API limits (from docs/09-communications.yaml)
	feedbacksMaxTake  = 5000
	feedbacksMaxSkip  = 199990

	// Questions API limits: take + skip <= 10000
	questionsMaxTake  = 5000
	questionsMaxSkip  = 5000

	// Feedbacks API host
	feedbacksBaseURL = "https://feedbacks-api.wildberries.ru"
)

// DownloadSummary holds counts of rows saved.
type DownloadSummary struct {
	FeedbacksRows int
	QuestionsRows int
}

// DownloadFeedbacks downloads all feedbacks for the period (two passes: isAnswered=false, true).
func DownloadFeedbacks(
	ctx context.Context,
	client *wb.Client,
	repo *sqlite.SQLiteSalesRepository,
	dateFrom, dateTo int64,
	rateLimit, burst int,
) (int, error) {
	total := 0

	for i, isAnswered := range []bool{false, true} {
		pass := i + 1
		fmt.Printf("  Loading feedbacks (pass %d, isAnswered=%t)...\n", pass, isAnswered)

		count, err := downloadFeedbacksPass(ctx, client, repo, dateFrom, dateTo, isAnswered, rateLimit, burst)
		if err != nil {
			return total, fmt.Errorf("pass %d feedbacks: %w", pass, err)
		}
		total += count
		fmt.Printf("    %d rows\n", count)
	}

	return total, nil
}

func downloadFeedbacksPass(
	ctx context.Context,
	client *wb.Client,
	repo *sqlite.SQLiteSalesRepository,
	dateFrom, dateTo int64,
	isAnswered bool,
	rateLimit, burst int,
) (int, error) {
	total := 0

	for skip := 0; skip <= feedbacksMaxSkip; skip += feedbacksMaxTake {
		params := url.Values{
			"isAnswered": {fmt.Sprintf("%t", isAnswered)},
			"take":       {fmt.Sprintf("%d", feedbacksMaxTake)},
			"skip":       {fmt.Sprintf("%d", skip)},
			"order":      {"dateAsc"},
			"dateFrom":   {fmt.Sprintf("%d", dateFrom)},
			"dateTo":     {fmt.Sprintf("%d", dateTo)},
		}

		var resp FeedbacksResponse
		if err := client.Get(ctx, "download_feedbacks", feedbacksBaseURL, rateLimit, burst,
			"/api/v1/feedbacks", params, &resp); err != nil {
			return total, fmt.Errorf("API call skip=%d: %w", skip, err)
		}

		if resp.Error {
			return total, fmt.Errorf("API error: %s", resp.ErrorText)
		}

		items := resp.Data.Feedbacks
		if len(items) == 0 {
			break
		}

		n, err := repo.SaveFeedbacks(ctx, items)
		if err != nil {
			return total, err
		}
		total += n

		fmt.Printf("    skip=%d: %d items\n", skip, len(items))

		// Less than full page → no more data
		if len(items) < feedbacksMaxTake {
			break
		}
	}

	return total, nil
}

// DownloadQuestions downloads all questions for the period (two passes + period splitting).
func DownloadQuestions(
	ctx context.Context,
	client *wb.Client,
	repo *sqlite.SQLiteSalesRepository,
	dateFrom, dateTo int64,
	rateLimit, burst int,
) (int, error) {
	total := 0

	for i, isAnswered := range []bool{false, true} {
		pass := i + 1
		fmt.Printf("  Loading questions (pass %d, isAnswered=%t)...\n", pass, isAnswered)

		count, err := downloadQuestionsPeriod(ctx, client, repo, dateFrom, dateTo, isAnswered, rateLimit, burst, 0)
		if err != nil {
			return total, fmt.Errorf("pass %d questions: %w", pass, err)
		}
		total += count
		fmt.Printf("    %d rows\n", count)
	}

	return total, nil
}

// downloadQuestionsPeriod downloads questions for a single period.
// If the API returns exactly maxTake items at skip=0, the period may have more data.
// In that case, we split the period in half and recurse.
const maxSplitDepth = 10

func downloadQuestionsPeriod(
	ctx context.Context,
	client *wb.Client,
	repo *sqlite.SQLiteSalesRepository,
	dateFrom, dateTo int64,
	isAnswered bool,
	rateLimit, burst int,
	depth int,
) (int, error) {
	if depth > maxSplitDepth {
		return 0, fmt.Errorf("max split depth reached (%d)", maxSplitDepth)
	}

	total := 0

	// First page: take=5000, skip=0
	params := url.Values{
		"isAnswered": {fmt.Sprintf("%t", isAnswered)},
		"take":       {fmt.Sprintf("%d", questionsMaxTake)},
		"skip":       {"0"},
		"order":      {"dateAsc"},
		"dateFrom":   {fmt.Sprintf("%d", dateFrom)},
		"dateTo":     {fmt.Sprintf("%d", dateTo)},
	}

	var resp QuestionsResponse
	if err := client.Get(ctx, "download_questions", feedbacksBaseURL, rateLimit, burst,
		"/api/v1/questions", params, &resp); err != nil {
		return 0, fmt.Errorf("API call: %w", err)
	}

	if resp.Error {
		return 0, fmt.Errorf("API error: %s", resp.ErrorText)
	}

	items := resp.Data.Questions
	if len(items) == 0 {
		return 0, nil
	}

	n, err := repo.SaveQuestions(ctx, items)
	if err != nil {
		return 0, err
	}
	total += n

	// If we got a full page, try to get more with skip
	if len(items) == questionsMaxTake {
		params.Set("skip", fmt.Sprintf("%d", questionsMaxSkip))

		var resp2 QuestionsResponse
		if err := client.Get(ctx, "download_questions", feedbacksBaseURL, rateLimit, burst,
			"/api/v1/questions", params, &resp2); err != nil {
			return total, fmt.Errorf("API call skip=%d: %w", questionsMaxSkip, err)
		}

		if resp2.Error {
			return total, fmt.Errorf("API error: %s", resp2.ErrorText)
		}

		items2 := resp2.Data.Questions
		if len(items2) > 0 {
			n, err := repo.SaveQuestions(ctx, items2)
			if err != nil {
				return total, err
			}
			total += n
		}

		// If second page was also full → more data exists, split period
		if len(items2) == questionsMaxTake {
			fmt.Printf("      splitting period (depth=%d)...\n", depth)
			mid := (dateFrom + dateTo) / 2

			n1, err := downloadQuestionsPeriod(ctx, client, repo, dateFrom, mid, isAnswered, rateLimit, burst, depth+1)
			if err != nil {
				return total, fmt.Errorf("first half: %w", err)
			}
			total += n1

			n2, err := downloadQuestionsPeriod(ctx, client, repo, mid+1, dateTo, isAnswered, rateLimit, burst, depth+1)
			if err != nil {
				return total, fmt.Errorf("second half: %w", err)
			}
			total += n2
		}
	}

	return total, nil
}

// DateToTimestamp converts a date-only string (YYYY-MM-DD) to Unix timestamp.
// For "from" dates, returns start of day (00:00:00).
func DateToTimestamp(dateStr string, endOfDay bool) (int64, error) {
	layout := "2006-01-02"
	t, err := time.Parse(layout, dateStr)
	if err != nil {
		return 0, fmt.Errorf("parse date %q: %w", dateStr, err)
	}
	if endOfDay {
		t = t.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
	}
	return t.Unix(), nil
}
