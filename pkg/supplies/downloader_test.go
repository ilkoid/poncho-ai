package supplies

import (
	"context"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func TestBasicDownload(t *testing.T) {
	src := NewMockSource(15)
	writer := NewDiscardWriter()

	opts := DownloadOptions{
		Begin:          "2026-01-01",
		End:            "2026-01-31",
		DateFilterType: "updatedDate",
		OnProgress:     func(msg string) {},
	}

	dl := NewDownloader(src, writer, opts)
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 3 warehouses
	if result.Warehouses != 3 {
		t.Errorf("warehouses: got %d, want 3", result.Warehouses)
	}
	// 1 tariff
	if result.Tariffs != 1 {
		t.Errorf("tariffs: got %d, want 1", result.Tariffs)
	}
	// 15 supplies from list (detail updates don't add to result.Supplies)
	if result.Supplies != 15 {
		t.Errorf("supplies: got %d, want 15", result.Supplies)
	}
	// 15 supplies × 3 goods = 45
	if result.Goods != 45 {
		t.Errorf("goods: got %d, want 45", result.Goods)
	}
	// 15 supplies × 1 package = 15
	if result.Packages != 15 {
		t.Errorf("packages: got %d, want 15", result.Packages)
	}
	if result.APICalls == 0 {
		t.Error("expected non-zero API calls")
	}
	if result.Errors != 0 {
		t.Errorf("errors: got %d, want 0", result.Errors)
	}
	if result.Duration == 0 {
		t.Error("expected non-zero duration")
	}

	// Verify DiscardWriter counts
	if writer.SavedWarehouses() != 3 {
		t.Errorf("saved warehouses: got %d, want 3", writer.SavedWarehouses())
	}
	if writer.SavedTariffs() != 1 {
		t.Errorf("saved tariffs: got %d, want 1", writer.SavedTariffs())
	}
	if writer.SavedSupplies() != 30 {
		t.Errorf("saved supplies: got %d, want 30", writer.SavedSupplies())
	}
	if writer.SavedGoods() != 45 {
		t.Errorf("saved goods: got %d, want 45", writer.SavedGoods())
	}
	if writer.SavedPackages() != 15 {
		t.Errorf("saved packages: got %d, want 15", writer.SavedPackages())
	}
}

func TestDryRun(t *testing.T) {
	src := NewMockSource(10)
	writer := NewDiscardWriter()

	opts := DownloadOptions{
		Begin:          "2026-01-01",
		End:            "2026-01-31",
		DateFilterType: "updatedDate",
		DryRun:         true,
	}

	dl := NewDownloader(src, writer, opts)
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// DryRun: all save counts should be 0
	if result.Warehouses != 0 {
		t.Errorf("warehouses: got %d, want 0 (dry run)", result.Warehouses)
	}
	if result.Supplies != 0 {
		t.Errorf("supplies: got %d, want 0 (dry run)", result.Supplies)
	}
	if result.Goods != 0 {
		t.Errorf("goods: got %d, want 0 (dry run)", result.Goods)
	}
	if result.Packages != 0 {
		t.Errorf("packages: got %d, want 0 (dry run)", result.Packages)
	}

	// API calls should still happen
	if result.APICalls == 0 {
		t.Error("expected non-zero API calls even in dry run")
	}
	if writer.SavedSupplies() != 0 {
		t.Errorf("saved supplies: got %d, want 0 (dry run)", writer.SavedSupplies())
	}
}

func TestSkipReference(t *testing.T) {
	src := NewMockSource(5)
	writer := NewDiscardWriter()

	opts := DownloadOptions{
		Begin:          "2026-01-01",
		End:            "2026-01-31",
		DateFilterType: "updatedDate",
		SkipReference:  true,
		OnProgress:     func(msg string) {},
	}

	dl := NewDownloader(src, writer, opts)
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Warehouses != 0 {
		t.Errorf("warehouses: got %d, want 0 (skip ref)", result.Warehouses)
	}
	if result.Tariffs != 0 {
		t.Errorf("tariffs: got %d, want 0 (skip ref)", result.Tariffs)
	}
	if result.Supplies == 0 {
		t.Error("expected supplies to be downloaded even with skip reference")
	}
}

func TestCancelled(t *testing.T) {
	src := NewMockSource(20)
	writer := NewDiscardWriter()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Pre-cancel

	opts := DownloadOptions{
		Begin:          "2026-01-01",
		End:            "2026-01-31",
		DateFilterType: "updatedDate",
	}

	dl := NewDownloader(src, writer, opts)
	_, err := dl.Run(ctx)

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if ctx.Err() != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", ctx.Err())
	}
}

func TestEmptySource(t *testing.T) {
	src := NewMockSource(0)
	writer := NewDiscardWriter()

	opts := DownloadOptions{
		Begin:          "2026-01-01",
		End:            "2026-01-31",
		DateFilterType: "updatedDate",
		OnProgress:     func(msg string) {},
	}

	dl := NewDownloader(src, writer, opts)
	result, err := dl.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Warehouses != 3 {
		t.Errorf("warehouses: got %d, want 3", result.Warehouses)
	}
	if result.Supplies != 0 {
		t.Errorf("supplies: got %d, want 0", result.Supplies)
	}
	if result.Goods != 0 {
		t.Errorf("goods: got %d, want 0", result.Goods)
	}
}

func TestMockSourceMethods(t *testing.T) {
	src := NewMockSource(10)
	filter := testFilter()

	warehouses, err := src.GetWarehouses(context.Background())
	if err != nil || len(warehouses) != 3 {
		t.Errorf("GetWarehouses: err=%v, len=%d", err, len(warehouses))
	}

	tariffs, err := src.GetTransitTariffs(context.Background())
	if err != nil || len(tariffs) != 1 {
		t.Errorf("GetTransitTariffs: err=%v, len=%d", err, len(tariffs))
	}

	supplies, err := src.GetSupplies(context.Background(), filter, 1000, 0)
	if err != nil || len(supplies) != 10 {
		t.Errorf("GetSupplies: err=%v, len=%d", err, len(supplies))
	}

	details, err := src.GetSupplyDetails(context.Background(), 10001)
	if err != nil || details == nil {
		t.Errorf("GetSupplyDetails: err=%v, details=%v", err, details)
	}

	goods, err := src.GetSupplyGoods(context.Background(), 10001, 1000, 0)
	if err != nil || len(goods) != 3 {
		t.Errorf("GetSupplyGoods: err=%v, len=%d", err, len(goods))
	}

	packages, err := src.GetSupplyPackages(context.Background(), 10001)
	if err != nil || len(packages) != 1 {
		t.Errorf("GetSupplyPackages: err=%v, len=%d", err, len(packages))
	}
}

func TestMockSourcePagination(t *testing.T) {
	src := NewMockSource(25)
	filter := testFilter()

	page1, err := src.GetSupplies(context.Background(), filter, 10, 0)
	if err != nil || len(page1) != 10 {
		t.Errorf("page 1: err=%v, len=%d", err, len(page1))
	}

	page2, err := src.GetSupplies(context.Background(), filter, 10, 10)
	if err != nil || len(page2) != 10 {
		t.Errorf("page 2: err=%v, len=%d", err, len(page2))
	}

	page3, err := src.GetSupplies(context.Background(), filter, 10, 20)
	if err != nil || len(page3) != 5 {
		t.Errorf("page 3: err=%v, len=%d", err, len(page3))
	}

	empty, err := src.GetSupplies(context.Background(), filter, 10, 30)
	if err != nil || len(empty) != 0 {
		t.Errorf("beyond range: err=%v, len=%d", err, len(empty))
	}
}

func TestMockSourceContextCancellation(t *testing.T) {
	src := NewMockSource(5)
	filter := testFilter()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := src.GetWarehouses(ctx)
	if err == nil {
		t.Error("expected error from cancelled context")
	}

	_, err = src.GetSupplies(ctx, filter, 10, 0)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestSupplyFromAPIConversion(t *testing.T) {
	supplyID := int64(12345)
	now := time.Now().Format("2006-01-02 15:04:05")

	// Test with full data
	s := &wb.Supply{
		Phone:      "+79991234567",
		SupplyID:   &supplyID,
		PreorderID: 99999,
		CreateDate: "2026-01-15",
		SupplyDate: strPtr("2026-01-16"),
		StatusID:   4,
		BoxTypeID:  1,
	}

	row := SupplyFromAPI(s, now)
	if row.SupplyID != 12345 {
		t.Errorf("SupplyID: got %d, want 12345", row.SupplyID)
	}
	if row.PreorderID != 99999 {
		t.Errorf("PreorderID: got %d, want 99999", row.PreorderID)
	}
	if row.StatusID != 4 {
		t.Errorf("StatusID: got %d, want 4", row.StatusID)
	}
	if !row.SupplyDate.Valid || row.SupplyDate.String != "2026-01-16" {
		t.Errorf("SupplyDate: got %v", row.SupplyDate)
	}

	// Test with nil supplyID (unplanned supply)
	s2 := &wb.Supply{
		PreorderID: 88888,
		StatusID:   1,
		BoxTypeID:  0,
	}
	row2 := SupplyFromAPI(s2, now)
	if row2.SupplyID != 0 {
		t.Errorf("SupplyID: got %d, want 0 (unplanned)", row2.SupplyID)
	}
}

// testFilter returns a minimal SuppliesFilterRequest for testing.
func testFilter() wb.SuppliesFilterRequest {
	return wb.SuppliesFilterRequest{
		Dates: []wb.DateFilter{
			{From: "2026-01-01", Till: "2026-01-31", Type: "updatedDate"},
		},
	}
}
