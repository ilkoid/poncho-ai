package stockproducts

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBasicDownload(t *testing.T) {
	src := NewMockStockProductsSource()
	src.PopulateProducts(50)
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalRows != 50 {
		t.Errorf("expected 50 rows, got %d", result.TotalRows)
	}
	if result.Pages != 1 {
		t.Errorf("expected 1 page, got %d", result.Pages)
	}
	if writer.Saved() != 50 {
		t.Errorf("writer: expected 50 saved, got %d", writer.Saved())
	}
}

func TestDryRun(t *testing.T) {
	src := NewMockStockProductsSource()
	src.PopulateProducts(20)
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{DryRun: true})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalRows != 20 {
		t.Errorf("expected 20 rows counted, got %d", result.TotalRows)
	}
	if writer.Saved() != 0 {
		t.Errorf("dry-run should not save, but got %d", writer.Saved())
	}
}

func TestContextCancel(t *testing.T) {
	src := NewMockStockProductsSource()
	src.PopulateProducts(100)
	writer := NewDiscardWriter()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	dl := NewDownloader(src, writer, DownloadOptions{})
	_, err := dl.Run(ctx)

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !strings.Contains(err.Error(), "context cancelled") {
		t.Errorf("expected context cancelled error, got: %v", err)
	}
}

func TestEmptySource(t *testing.T) {
	src := &MockStockProductsSource{} // empty — no auto-populate
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalRows != 0 {
		t.Errorf("expected 0 rows, got %d", result.TotalRows)
	}
	if result.Pages != 0 {
		t.Errorf("expected 0 pages, got %d", result.Pages)
	}
}

func TestDefaultDateYesterday(t *testing.T) {
	src := NewMockStockProductsSource()
	src.PopulateProducts(5)
	writer := NewDiscardWriter()

	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	var messages []string
	dl := NewDownloader(src, writer, DownloadOptions{
		OnProgress: func(msg string) { messages = append(messages, msg) },
	})
	_, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, msg := range messages {
		if strings.Contains(msg, yesterday) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected progress messages to contain yesterday %q, got: %v", yesterday, messages)
	}
}

func TestPagination(t *testing.T) {
	src := NewMockStockProductsSource()
	src.PopulateProducts(2500)
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{PageSize: 1000})
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalRows != 2500 {
		t.Errorf("expected 2500 rows, got %d", result.TotalRows)
	}
	if result.Pages != 3 { // 1000 + 1000 + 500
		t.Errorf("expected 3 pages, got %d", result.Pages)
	}
	if writer.Saved() != 2500 {
		t.Errorf("writer: expected 2500 saved, got %d", writer.Saved())
	}
}

func TestSourceFailure(t *testing.T) {
	src := NewMockStockProductsSource()
	src.PopulateProducts(10)
	src.SetFailCount(1) // first call fails
	writer := NewDiscardWriter()

	dl := NewDownloader(src, writer, DownloadOptions{})
	_, err := dl.Run(context.Background())

	if err == nil {
		t.Fatal("expected error from mock failure")
	}
	if !strings.Contains(err.Error(), "mock failure") {
		t.Errorf("expected mock failure error, got: %v", err)
	}
}
