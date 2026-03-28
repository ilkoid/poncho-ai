package testing

import (
	"math/rand"
	"testing"
	"time"
)

func TestGenerateSalesRows(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 1, 31, 23, 59, 59, 0, time.UTC)

	rows := GenerateSalesRows(from, to, 1, 100)

	if len(rows) != 100 {
		t.Fatalf("GenerateSalesRows() = %d rows, want 100", len(rows))
	}

	// Verify RrdID numbering (page 1: 1..100)
	if rows[0].RrdID != 1 {
		t.Errorf("rows[0].RrdID = %d, want 1", rows[0].RrdID)
	}
	if rows[99].RrdID != 100 {
		t.Errorf("rows[99].RrdID = %d, want 100", rows[99].RrdID)
	}

	// Verify essential fields are populated
	for i, row := range rows {
		if row.NmID == 0 {
			t.Errorf("rows[%d].NmID = 0, want non-zero", i)
		}
		if row.SupplierArticle == "" {
			t.Errorf("rows[%d].SupplierArticle is empty", i)
		}
		if row.SaleDT == "" {
			t.Errorf("rows[%d].SaleDT is empty", i)
		}
		if row.OrderDT == "" {
			t.Errorf("rows[%d].OrderDT is empty", i)
		}
		if row.RRDT == "" {
			t.Errorf("rows[%d].RRDT is empty", i)
		}
		if row.RetailPrice <= 0 {
			t.Errorf("rows[%d].RetailPrice = %f, want positive", i, row.RetailPrice)
		}
	}

	// Verify date range string fields
	if rows[0].DateFrom != "2025-01-01" {
		t.Errorf("DateFrom = %q, want %q", rows[0].DateFrom, "2025-01-01")
	}
	if rows[0].DateTo != "2025-01-31" {
		t.Errorf("DateTo = %q, want %q", rows[0].DateTo, "2025-01-31")
	}
}

func TestGenerateSalesRows_PageNumbering(t *testing.T) {
	from := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 6, 1, 23, 59, 59, 0, time.UTC)

	// Page 2 should start RrdID from 100001
	rows := GenerateSalesRows(from, to, 2, 50)
	if rows[0].RrdID != 100001 {
		t.Errorf("page=2 rows[0].RrdID = %d, want 100001", rows[0].RrdID)
	}

	// Page 3 should start from 200001
	rows = GenerateSalesRows(from, to, 3, 50)
	if rows[0].RrdID != 200001 {
		t.Errorf("page=3 rows[0].RrdID = %d, want 200001", rows[0].RrdID)
	}
}

func TestGenerateSalesRows_DateDistribution(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 1, 10, 23, 59, 59, 0, time.UTC)

	rows := GenerateSalesRows(from, to, 1, 100)

	// First row should be near the start of the period
	firstRR, _ := time.Parse(time.RFC3339, rows[0].RRDT)
	if firstRR.Before(from) || firstRR.After(from.Add(1*time.Hour)) {
		t.Errorf("first RRDT = %v, expected near %v", firstRR, from)
	}

	// Last row should be near the end of the period
	lastRR, _ := time.Parse(time.RFC3339, rows[99].RRDT)
	if lastRR.Before(to.Add(-1*time.Hour)) || lastRR.After(to.Add(1*time.Hour)) {
		t.Errorf("last RRDT = %v, expected near %v", lastRR, to)
	}
}

func TestGenerateSalesRows_FBWvsFBS(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 1, 1, 23, 59, 59, 0, time.UTC)

	rows := GenerateSalesRows(from, to, 1, 30)

	fbwCount := 0
	fbsCount := 0
	for _, row := range rows {
		switch row.DeliveryMethod {
		case "ФБВ":
			fbwCount++
		case "FBS, (МГТ)":
			fbsCount++
		default:
			t.Errorf("unexpected DeliveryMethod: %q", row.DeliveryMethod)
		}
	}

	// ~33% FBW (indices 0, 3, 6, 9, ... = 10 out of 30)
	if fbwCount == 0 || fbsCount == 0 {
		t.Errorf("expected both FBW and FBS, got FBW=%d FBS=%d", fbwCount, fbsCount)
	}
}

func TestGenerateSimpleSalesRows(t *testing.T) {
	date := time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC)

	rows := GenerateSimpleSalesRows(50, date)

	if len(rows) != 50 {
		t.Fatalf("GenerateSimpleSalesRows() = %d rows, want 50", len(rows))
	}

	if rows[0].RrdID != 1 {
		t.Errorf("rows[0].RrdID = %d, want 1", rows[0].RrdID)
	}

	if rows[0].DocTypeName != "Продажа" {
		t.Errorf("DocTypeName = %q, want 'Продажа'", rows[0].DocTypeName)
	}
}

func TestGenerateSalesWithRrdID(t *testing.T) {
	rows := GenerateSalesWithRrdID(20, 5000)

	if len(rows) != 20 {
		t.Fatalf("GenerateSalesWithRrdID() = %d rows, want 20", len(rows))
	}

	if rows[0].RrdID != 5000 {
		t.Errorf("rows[0].RrdID = %d, want 5000", rows[0].RrdID)
	}

	if rows[19].RrdID != 5019 {
		t.Errorf("rows[19].RrdID = %d, want 5019", rows[19].RrdID)
	}

	// All rows should have RRDT set (needed for resume mode)
	for i, row := range rows {
		if row.RRDT == "" {
			t.Errorf("rows[%d].RRDT is empty, need non-empty for resume mode", i)
		}
	}
}

func TestGenerateCampaigns(t *testing.T) {
	data := GenerateCampaigns(5, 7)

	if len(data.Groups) != 5 {
		t.Fatalf("Groups count = %d, want 5", len(data.Groups))
	}

	if len(data.Details) != 5 {
		t.Errorf("Details count = %d, want 5", len(data.Details))
	}

	if len(data.Stats) != 5 {
		t.Errorf("Stats count = %d, want 5", len(data.Stats))
	}

	// Verify campaign hierarchy: each campaign has daily stats
	for _, group := range data.Groups {
		advertID := group.AdvertList[0].AdvertID

		// Should have details
		if _, ok := data.Details[advertID]; !ok {
			t.Errorf("no details for advertID %d", advertID)
		}

		// Should have stats
		stats, ok := data.Stats[advertID]
		if !ok {
			t.Errorf("no stats for advertID %d", advertID)
			continue
		}

		// Stats should have 7 days
		if len(stats) != 1 || len(stats[0].Days) != 7 {
			t.Errorf("advertID %d: expected 7 days, got %d days", advertID, len(stats[0].Days))
		}

		// Each day should have 3 apps (site, android, ios)
		for _, day := range stats[0].Days {
			if len(day.Apps) != 3 {
				t.Errorf("day %s: expected 3 apps, got %d", day.Date, len(day.Apps))
			}

			// Each app should have 2 products
			for _, app := range day.Apps {
				if len(app.Nms) != 2 {
					t.Errorf("app %d: expected 2 nms, got %d", app.AppType, len(app.Nms))
				}
			}
		}
	}
}

func TestGenerateCampaigns_TypeVariation(t *testing.T) {
	data := GenerateCampaigns(10, 1)

	types := map[int]int{}
	for _, g := range data.Groups {
		types[g.Type]++
	}

	// Should have at least 2 different campaign types
	if len(types) < 2 {
		t.Errorf("expected ≥2 campaign types, got %d: %v", len(types), types)
	}
}

func TestGenerateCampaigns_StatusVariation(t *testing.T) {
	data := GenerateCampaigns(10, 1)

	statuses := map[int]int{}
	for _, g := range data.Groups {
		statuses[g.Status]++
	}

	// Should have at least 2 different statuses
	if len(statuses) < 2 {
		t.Errorf("expected ≥2 statuses, got %d: %v", len(statuses), statuses)
	}
}

func TestGenerateCampaigns_Empty(t *testing.T) {
	data := GenerateCampaigns(0, 0)

	if len(data.Groups) != 0 {
		t.Errorf("expected 0 groups, got %d", len(data.Groups))
	}
	if data.Details == nil {
		t.Error("Details should be initialized (non-nil map)")
	}
	if data.Stats == nil {
		t.Error("Stats should be initialized (non-nil map)")
	}
}

func TestGenerateFeedbacks(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	items := GenerateFeedbacks(rng, 20)

	if len(items) != 20 {
		t.Fatalf("GenerateFeedbacks() = %d items, want 20", len(items))
	}

	// Verify essential fields
	for i, f := range items {
		if f.ID == "" {
			t.Errorf("items[%d].ID is empty", i)
		}
		if f.Text == "" {
			t.Errorf("items[%d].Text is empty", i)
		}
		if f.ProductValuation < 1 || f.ProductValuation > 5 {
			t.Errorf("items[%d].ProductValuation = %d, want 1-5", i, f.ProductValuation)
		}
		if f.CreatedDate == "" {
			t.Errorf("items[%d].CreatedDate is empty", i)
		}
		if f.ProductDetails.NmId == 0 {
			t.Errorf("items[%d].ProductDetails.NmId = 0", i)
		}
	}
}

func TestGenerateFeedbacks_AnswerProbability(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	items := GenerateFeedbacks(rng, 100)

	withAnswer := 0
	for _, f := range items {
		if f.Answer != nil {
			withAnswer++
		}
	}

	// ~33% should have answers (with some tolerance)
	if withAnswer == 0 {
		t.Error("expected some feedbacks to have answers")
	}
	if withAnswer == 100 {
		t.Error("expected some feedbacks without answers")
	}
}

func TestGenerateQuestions(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	items := GenerateQuestions(rng, 20)

	if len(items) != 20 {
		t.Fatalf("GenerateQuestions() = %d items, want 20", len(items))
	}

	for i, q := range items {
		if q.ID == "" {
			t.Errorf("items[%d].ID is empty", i)
		}
		if q.Text == "" {
			t.Errorf("items[%d].Text is empty", i)
		}
		if q.CreatedDate == "" {
			t.Errorf("items[%d].CreatedDate is empty", i)
		}
		if q.ProductDetails.NmId == 0 {
			t.Errorf("items[%d].ProductDetails.NmId = 0", i)
		}
	}
}

func TestGenerateQuestions_AnswerProbability(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	items := GenerateQuestions(rng, 100)

	withAnswer := 0
	for _, q := range items {
		if q.Answer != nil {
			withAnswer++
		}
	}

	// ~50% should have answers
	if withAnswer == 0 {
		t.Error("expected some questions to have answers")
	}
	if withAnswer == 100 {
		t.Error("expected some questions without answers")
	}
}

func TestDefaultSalesMockConfig(t *testing.T) {
	cfg := DefaultSalesMockConfig()

	if cfg.RowsPerPage != 47000 {
		t.Errorf("RowsPerPage = %d, want 47000", cfg.RowsPerPage)
	}
	if cfg.PagesPerPeriod != 3 {
		t.Errorf("PagesPerPeriod = %d, want 3", cfg.PagesPerPeriod)
	}
}
