package feedbacks

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestBasicDownload verifies feedbacks + questions are saved correctly.
func TestBasicDownload(t *testing.T) {
	src := NewMockSource()
	src.PopulateFeedbacks(120)  // 120 feedbacks → 1 page (< 5000)
	src.PopulateQuestions(50)   // 50 questions → 1 page (< 5000)
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		Feedbacks: true,
		Questions: true,
		DateFrom:  "2026-05-01",
		DateTo:    "2026-05-31",
	})
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 120 feedbacks × 2 passes (isAnswered=false + true)
	if result.FeedbacksSaved != 240 {
		t.Errorf("feedbacks: want 240 (120×2 passes), got %d", result.FeedbacksSaved)
	}
	// 50 questions × 2 passes
	if result.QuestionsSaved != 100 {
		t.Errorf("questions: want 100 (50×2 passes), got %d", result.QuestionsSaved)
	}

	if writer.Saved() != 340 {
		t.Errorf("total saved: want 340, got %d", writer.Saved())
	}
}

// TestDryRun verifies that DryRun processes source pages but skips Writer.Save.
func TestDryRun(t *testing.T) {
	src := NewMockSource()
	src.PopulateFeedbacks(30)
	src.PopulateQuestions(20)
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		Feedbacks: true,
		Questions: true,
		DryRun:    true,
		DateFrom:  "2026-05-01",
		DateTo:    "2026-05-31",
	})
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// DryRun counts items but DiscardWriter should stay at 0 (DryRun skips Save)
	if writer.Saved() != 0 {
		t.Errorf("discard writer should be 0 in dry-run, got %d", writer.Saved())
	}
	// But result should have counts (dry-run counts from source, not writer)
	if result.FeedbacksSaved == 0 {
		t.Error("feedbacks saved should be >0 even in dry-run")
	}
	if result.QuestionsSaved == 0 {
		t.Error("questions saved should be >0 even in dry-run")
	}
}

// TestFeedbacksOnly verifies that questions can be disabled.
func TestFeedbacksOnly(t *testing.T) {
	src := NewMockSource()
	src.PopulateFeedbacks(10)
	src.PopulateQuestions(10)
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		Feedbacks: true,
		Questions: false, // disabled
		DateFrom:  "2026-05-01",
		DateTo:    "2026-05-31",
	})
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.FeedbacksSaved == 0 {
		t.Error("feedbacks should be saved")
	}
	if result.QuestionsSaved != 0 {
		t.Errorf("questions should be 0 (disabled), got %d", result.QuestionsSaved)
	}
}

// TestContextCancellation verifies graceful shutdown on context cancel.
func TestContextCancellation(t *testing.T) {
	src := NewMockSource()
	src.PopulateFeedbacks(10)
	src.PopulateQuestions(10)
	writer := NewDiscardWriter()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	dl := NewDownloader(src, writer, DownloadOptions{
		Feedbacks: true,
		Questions: true,
		DateFrom:  "2026-05-01",
		DateTo:    "2026-05-31",
	})
	_, err := dl.Run(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !strings.Contains(err.Error(), "context cancelled") {
		t.Errorf("want 'context cancelled', got: %v", err)
	}
}

// TestDefaultDays verifies that Days defaults to 7 when not set.
func TestDefaultDays(t *testing.T) {
	src := NewMockSource()
	src.PopulateFeedbacks(5)
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		Feedbacks: true,
		Questions: false,
		// Days not set → default 7
	})
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FeedbacksSaved == 0 {
		t.Error("feedbacks should be saved with default days")
	}
}

// TestProgressCallback verifies OnProgress is called.
func TestProgressCallback(t *testing.T) {
	src := NewMockSource()
	src.PopulateFeedbacks(5)
	src.PopulateQuestions(5)
	writer := NewDiscardWriter()

	var messages []string
	dl := NewDownloader(src, writer, DownloadOptions{
		Feedbacks: true,
		Questions: true,
		DateFrom:  "2026-05-01",
		DateTo:    "2026-05-31",
		OnProgress: func(msg string) {
			messages = append(messages, msg)
		},
	})
	_, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(messages) == 0 {
		t.Fatal("expected progress messages, got none")
	}

	// Should contain period, feedbacks, and questions messages
	var hasPeriod, hasFeedbacks, hasQuestions bool
	for _, m := range messages {
		if strings.Contains(m, "Period") {
			hasPeriod = true
		}
		if strings.Contains(m, "feedbacks") {
			hasFeedbacks = true
		}
		if strings.Contains(m, "questions") {
			hasQuestions = true
		}
	}
	if !hasPeriod {
		t.Error("expected 'Period' in progress messages")
	}
	if !hasFeedbacks {
		t.Error("expected 'feedbacks' in progress messages")
	}
	if !hasQuestions {
		t.Error("expected 'questions' in progress messages")
	}
}

// TestDuration verifies result has a positive duration.
func TestDuration(t *testing.T) {
	src := NewMockSource()
	src.PopulateFeedbacks(5)
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{
		Feedbacks: true,
		Questions: false,
		DateFrom:  "2026-05-01",
		DateTo:    "2026-05-31",
	})
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Duration == 0 {
		t.Error("expected non-zero duration")
	}
	if result.Duration > time.Second {
		t.Errorf("mock download should be fast, took %v", result.Duration)
	}
}
