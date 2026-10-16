package itemrating

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func baseOpts() DownloadOptions {
	return DownloadOptions{
		PeriodStart: "2026-10-11",
		PeriodEnd:   "2026-10-11",
		PastStart:   "2026-10-10",
		PastEnd:     "2026-10-10",
		PageSize:    1000,
	}
}

func TestBasicDownload(t *testing.T) {
	src := NewMockSource() // 2500 items → 3 pages (1000+1000+500)
	w := NewDiscardWriter()
	dl := NewDownloader(src, w, baseOpts())

	res, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ItemsLoaded != 2500 {
		t.Errorf("ItemsLoaded = %d, want 2500", res.ItemsLoaded)
	}
	if res.PagesLoaded != 3 {
		t.Errorf("PagesLoaded = %d, want 3", res.PagesLoaded)
	}
	if res.Errors != 0 {
		t.Errorf("Errors = %d, want 0", res.Errors)
	}
	if res.DBCount != 2500 {
		t.Errorf("DBCount = %d, want 2500", res.DBCount)
	}
	if w.SellerSaved() == 0 {
		t.Error("seller block never saved")
	}
	if w.Saved() != 2500 {
		t.Errorf("DiscardWriter.Saved() = %d, want 2500", w.Saved())
	}
}

func TestDryRun(t *testing.T) {
	src := NewMockSource()
	w := NewDiscardWriter()
	opts := baseOpts()
	opts.DryRun = true
	dl := NewDownloader(src, w, opts)

	res, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ItemsLoaded != 2500 {
		t.Errorf("ItemsLoaded = %d, want 2500 (dry-run still counts)", res.ItemsLoaded)
	}
	if w.Saved() != 0 {
		t.Errorf("DiscardWriter.Saved() = %d, want 0 in dry-run", w.Saved())
	}
	if res.DBCount != 0 {
		t.Errorf("DBCount = %d, want 0 in dry-run", res.DBCount)
	}
}

func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before run

	dl := NewDownloader(NewMockSource(), NewDiscardWriter(), baseOpts())
	_, err := dl.Run(ctx)
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
}

func TestAPIErrorStopsRun(t *testing.T) {
	src := &MockSource{CatalogSize: 5000, FailAtPage: 2} // page 1 OK, page 2 fails
	w := NewDiscardWriter()
	dl := NewDownloader(src, w, baseOpts())

	res, err := dl.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from failed page, got nil")
	}
	if res.Errors != 1 {
		t.Errorf("Errors = %d, want 1", res.Errors)
	}
	if res.PagesLoaded != 1 {
		t.Errorf("PagesLoaded = %d, want 1 (stop after failure)", res.PagesLoaded)
	}
}

func TestEmptyCatalog(t *testing.T) {
	src := &MockSource{CatalogSize: 1}
	// PageSize 1000 > catalog 1 → single page, single item, done.
	dl := NewDownloader(src, NewDiscardWriter(), baseOpts())
	res, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ItemsLoaded != 1 || res.PagesLoaded != 1 {
		t.Errorf("got ItemsLoaded=%d PagesLoaded=%d, want 1/1", res.ItemsLoaded, res.PagesLoaded)
	}
}

// goldenItemPayload carries every DistributionTableItem field from the spec
// (11-analytics.yaml:6271) plus a futureField that WB may add before the
// local spec is refreshed — proving both typed mapping and raw capture.
const goldenItemPayload = `{
	"nmId": 123456789,
	"title": "iPh 17 512 ГБ Серебристый",
	"vendorCode": "wb3ha2668w",
	"subjectId": 50,
	"subjectName": "Phones",
	"brandName": "Attlee",
	"tagName": "Phones",
	"tagId": 65,
	"pinnedFeedback": true,
	"rating": 10,
	"feedbackRating": {"current": 3.87, "dynamics": 0.31, "percentile": 1.7},
	"feedbackCount": {"current": 12, "dynamics": 9},
	"fiveStar": {"current": 9, "dynamics": 7},
	"fourStar": {"current": 0, "dynamics": -1},
	"threeStar": {"current": 1, "dynamics": 1},
	"twoStar": {"current": 2, "dynamics": 2},
	"oneStar": {"current": 0, "dynamics": 0},
	"disqualified": 7,
	"isShadowed": false,
	"futureField": {"any": "drift"}
}`

// goldenItemKeys — все ключи первого уровня DistributionTableItem по спеке.
// Если WB расширит схему, обновить и payload, и этот список (README: сверка после рефреша спек).
var goldenItemKeys = []string{
	"nmId", "title", "vendorCode", "subjectId", "subjectName", "brandName",
	"tagName", "tagId", "pinnedFeedback", "rating", "feedbackRating", "feedbackCount",
	"fiveStar", "fourStar", "threeStar", "twoStar", "oneStar", "disqualified", "isShadowed",
}

// TestGoldenPayloadCompleteness — AP-6 (усиленный): каждый ключ спеки декодируется
// в типизированные поля, а raw-захват сохраняет payload целиком, включая будущие поля.
func TestGoldenPayloadCompleteness(t *testing.T) {
	var item wb.ItemRatingItem
	if err := json.Unmarshal([]byte(goldenItemPayload), &item); err != nil {
		t.Fatalf("unmarshal golden payload: %v", err)
	}

	// Typed mapping assertions — one per spec key.
	if item.NmID != 123456789 {
		t.Errorf("nmId = %d", item.NmID)
	}
	if item.Title != "iPh 17 512 ГБ Серебристый" || item.VendorCode != "wb3ha2668w" {
		t.Errorf("title/vendorCode not mapped: %q/%q", item.Title, item.VendorCode)
	}
	if item.SubjectID != 50 || item.SubjectName != "Phones" || item.BrandName != "Attlee" {
		t.Errorf("subject/brand not mapped")
	}
	if item.TagID != 65 || item.TagName != "Phones" {
		t.Errorf("tag not mapped")
	}
	if !item.PinnedFeedback {
		t.Error("pinnedFeedback not mapped")
	}
	if item.Rating != 10 {
		t.Errorf("rating (card score) = %v, want 10", item.Rating)
	}
	if item.FeedbackRating.Current != 3.87 || item.FeedbackRating.Dynamics != 0.31 {
		t.Errorf("feedbackRating not mapped: %+v", item.FeedbackRating)
	}
	if item.FeedbackRating.Percentile == nil || *item.FeedbackRating.Percentile != 1.7 {
		t.Errorf("percentile not mapped: %v", item.FeedbackRating.Percentile)
	}
	if item.FeedbackCount.Current != 12 || item.FeedbackCount.Dynamics != 9 {
		t.Errorf("feedbackCount not mapped: %+v", item.FeedbackCount)
	}
	if item.FiveStar.Current != 9 || item.OneStar.Current != 0 || item.TwoStar.Dynamics != 2 {
		t.Errorf("star deltas not mapped")
	}
	if item.Disqualified != 7 {
		t.Errorf("disqualified = %d, want 7", item.Disqualified)
	}
	if item.IsShadowed {
		t.Error("isShadowed not mapped")
	}

	// Raw capture: payload preserved byte-for-byte, future field included.
	if len(item.Raw) == 0 || !json.Valid(item.Raw) {
		t.Fatal("Raw not captured")
	}
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(item.Raw, &rawMap); err != nil {
		t.Fatalf("raw is not a JSON object: %v", err)
	}
	if _, ok := rawMap["futureField"]; !ok {
		t.Error("futureField lost from Raw — drift protection broken")
	}
	for _, k := range goldenItemKeys {
		if _, ok := rawMap[k]; !ok {
			t.Errorf("spec key %q missing from Raw", k)
		}
	}

	// Source conversion: typed fields + raw travel into the domain Row intact.
	row := convertItem(item, "2026-10-11", "2026-10-11")
	if row.NmID != item.NmID || row.CardRating != 10 || row.Disqualified != 7 ||
		row.FeedbackRatingPercentile == nil || string(row.RawItem) != string(item.Raw) {
		t.Error("wb.ItemRatingItem → Row conversion lost data")
	}
}

func TestSellerBlocksRawCapture(t *testing.T) {
	payload := `{"data": {"sellerRating": {"current": 3.56, "dynamics": 0.03},
		"feedbackIncrease": {"current": 26, "total": 116, "dynamics": 23,
			"fiveStar": {"current": 19, "dynamics": 17, "total": 51}},
		"items": []}}`
	var resp wb.ItemRatingResponse
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data.SellerRating.Raw) == 0 || !strings.Contains(string(resp.Data.FeedbackIncrease.Raw), "116") {
		t.Error("seller/fbIncrease raw blocks not captured")
	}
	if resp.Data.FeedbackIncrease.FiveStar.Total != 51 {
		t.Errorf("fiveStar.total = %d, want 51", resp.Data.FeedbackIncrease.FiveStar.Total)
	}
}

func TestDurationSet(t *testing.T) {
	dl := NewDownloader(NewMockSource(), NewDiscardWriter(), baseOpts())
	res, err := dl.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Duration <= 0 {
		t.Error("Duration not set")
	}
}
