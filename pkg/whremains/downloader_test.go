package whremains

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// TestDownloader_Basic tests the full async pipeline: create → poll → download → flatten → save.
func TestDownloader_Basic(t *testing.T) {
	source := NewMockSource()
	source.PopulateItems(10, 3) // 10 items × 3 warehouses = 30 flat rows

	writer := NewDiscardWriter()

	var msgs []string
	opts := DownloadOptions{
		SnapshotDate:    "2026-06-05",
		PollIntervalSec: 1,
		PollTimeoutMin:  1,
		OnProgress:      func(msg string) { msgs = append(msgs, msg) },
	}

	dl := NewDownloader(source, writer, opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.Status != "SUCCESS" {
		t.Errorf("Status = %q, want SUCCESS", result.Status)
	}
	if result.TotalRows != 30 {
		t.Errorf("TotalRows = %d, want 30 (10 items × 3 warehouses)", result.TotalRows)
	}
	if writer.Saved() != 30 {
		t.Errorf("DiscardWriter.Saved() = %d, want 30", writer.Saved())
	}
	if result.TaskID == "" {
		t.Error("TaskID is empty")
	}

	// Verify progress messages show key steps
	wantParts := []string{"Report created", "Report ready", "Downloaded", "Flattened"}
	for _, part := range wantParts {
		found := false
		for _, m := range msgs {
			if strings.Contains(m, part) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected progress message containing %q, got: %v", part, msgs)
		}
	}
}

// TestDownloader_DryRun tests that dry-run mode parses + flattens but does not save.
func TestDownloader_DryRun(t *testing.T) {
	source := NewMockSource()
	source.PopulateItems(5, 2) // 10 flat rows

	writer := NewDiscardWriter()

	opts := DownloadOptions{
		SnapshotDate:    "2026-06-05",
		DryRun:          true,
		PollIntervalSec: 1,
		PollTimeoutMin:  1,
	}
	dl := NewDownloader(source, writer, opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.TotalRows != 10 {
		t.Errorf("TotalRows = %d, want 10 (dry-run counts)", result.TotalRows)
	}
	if writer.Saved() != 0 {
		t.Errorf("DiscardWriter.Saved() = %d, want 0 (dry-run should not save)", writer.Saved())
	}
}

// TestDownloader_Resume tests that download is skipped when data already exists.
func TestDownloader_Resume(t *testing.T) {
	source := NewMockSource()
	source.PopulateItems(5, 2)

	writer := NewDiscardWriter()
	writer.SetMockCountForDate(100) // simulate existing data

	opts := DownloadOptions{
		SnapshotDate:    "2026-06-05",
		PollIntervalSec: 1,
		PollTimeoutMin:  1,
	}
	dl := NewDownloader(source, writer, opts)
	result, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.Status != "RESUMED" {
		t.Errorf("Status = %q, want RESUMED", result.Status)
	}
	if result.TotalRows != 100 {
		t.Errorf("TotalRows = %d, want 100 (from mock count)", result.TotalRows)
	}
	if writer.Saved() != 0 {
		t.Errorf("DiscardWriter.Saved() = %d, want 0 (resumed should not save)", writer.Saved())
	}
}

// TestDownloader_ContextCancel tests that context cancellation propagates.
func TestDownloader_ContextCancel(t *testing.T) {
	source := NewMockSource()
	source.PopulateItems(5, 2)

	writer := NewDiscardWriter()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	opts := DownloadOptions{
		SnapshotDate:    "2026-06-05",
		PollIntervalSec: 1,
		PollTimeoutMin:  1,
	}
	dl := NewDownloader(source, writer, opts)
	_, err := dl.Run(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// TestFlattenRemains tests the nested → flat transformation.
func TestFlattenRemains(t *testing.T) {
	items := []wb.WarehouseRemainsItem{
		{
			Brand: "BrandA", VendorCode: "VC-1", NmID: 100, TechSize: "42",
			Warehouses: []wb.WarehouseRemainsEntry{
				{WarehouseName: "Коледино", Quantity: 10},
				{WarehouseName: "Казань", Quantity: 5},
			},
		},
		{
			Brand: "BrandB", VendorCode: "VC-2", NmID: 200, TechSize: "M",
			Volume: 1.5,
			Warehouses: []wb.WarehouseRemainsEntry{
				{WarehouseName: "Подольск", Quantity: 20},
				{WarehouseName: "Краснодар", Quantity: 15},
				{WarehouseName: "Электросталь", Quantity: 7},
			},
		},
	}

	rows := flattenRemains(items)

	if len(rows) != 5 { // 2 + 3 = 5
		t.Fatalf("flattenRemains returned %d rows, want 5", len(rows))
	}

	// Verify first row (item 0 × warehouse 0)
	if rows[0].NmID != 100 {
		t.Errorf("rows[0].NmID = %d, want 100", rows[0].NmID)
	}
	if rows[0].WarehouseName != "Коледино" {
		t.Errorf("rows[0].WarehouseName = %q, want Коледино", rows[0].WarehouseName)
	}
	if rows[0].Quantity != 10 {
		t.Errorf("rows[0].Quantity = %d, want 10", rows[0].Quantity)
	}

	// Verify last row (item 1 × warehouse 2)
	if rows[4].NmID != 200 {
		t.Errorf("rows[4].NmID = %d, want 200", rows[4].NmID)
	}
	if rows[4].WarehouseName != "Электросталь" {
		t.Errorf("rows[4].WarehouseName = %q, want Электросталь", rows[4].WarehouseName)
	}
	if rows[4].Volume != 1.5 {
		t.Errorf("rows[4].Volume = %f, want 1.5", rows[4].Volume)
	}
}

// TestFlattenRemains_Empty tests flatten with empty input.
func TestFlattenRemains_Empty(t *testing.T) {
	rows := flattenRemains(nil)
	if len(rows) != 0 {
		t.Errorf("flattenRemains(nil) returned %d rows, want 0", len(rows))
	}
}

// TestFlattenRemains_NoWarehouses tests items with empty warehouses[].
func TestFlattenRemains_NoWarehouses(t *testing.T) {
	items := []wb.WarehouseRemainsItem{
		{NmID: 100, Warehouses: nil},
		{NmID: 200, Warehouses: []wb.WarehouseRemainsEntry{}},
	}

	rows := flattenRemains(items)
	if len(rows) != 0 {
		t.Errorf("flattenRemains with no warehouses returned %d rows, want 0", len(rows))
	}
}

// TestDownloader_DefaultDate tests that snapshot date defaults to today.
func TestDownloader_DefaultDate(t *testing.T) {
	source := NewMockSource()
	source.PopulateItems(3, 1)
	writer := NewDiscardWriter()

	var msgs []string
	opts := DownloadOptions{
		// SnapshotDate intentionally empty — should default to today
		PollIntervalSec: 1,
		PollTimeoutMin:  1,
		OnProgress:      func(msg string) { msgs = append(msgs, msg) },
	}
	dl := NewDownloader(source, writer, opts)
	_, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	today := time.Now().Format("2006-01-02")
	found := false
	for _, m := range msgs {
		if strings.Contains(m, today) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected today's date %s in progress messages, got: %v", today, msgs)
	}
}
