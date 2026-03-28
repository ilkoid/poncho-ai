// Package testing provides mock data generators for WB API types.
//
// This package contains standalone data generation functions for E2E tests
// and mock mode across all downloaders. Each function generates realistic
// test data that mimics the structure and relationships of real WB API responses.
//
// Design note (ISP): No single interface is defined because each downloader
// uses only one generator. Per dev_solid.md: "single implementation → no interface".
// Standalone functions provide the same reusability without ISP violation.
package testing

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// SalesMockConfig controls sales mock data generation.
type SalesMockConfig struct {
	RowsPerPage    int           // Rows per page (default: 47000)
	PagesPerPeriod int           // Pages per period (default: 3)
	PageDelay      time.Duration // Delay between pages (default: 0)
}

// DefaultSalesMockConfig returns sensible defaults for sales mock generation.
func DefaultSalesMockConfig() SalesMockConfig {
	return SalesMockConfig{
		RowsPerPage:    47000,
		PagesPerPeriod: 3,
		PageDelay:      0,
	}
}

// GenerateSalesRows creates realistic mock sales data for a date range.
// Dates are distributed evenly across the period with proper temporal
// relationships: order_dt < sale_dt < rr_dt.
//
// Parameters:
//   - from, to: the date range for data distribution
//   - page: page number (affects RrdID numbering)
//   - count: number of rows to generate
func GenerateSalesRows(from, to time.Time, page, count int) []wb.RealizationReportRow {
	rows := make([]wb.RealizationReportRow, count)

	periodDuration := to.Sub(from)
	periodMinutes := int(periodDuration.Minutes())
	if periodMinutes < 1 {
		periodMinutes = 24 * 60 // Minimum 1 day
	}

	giBoxTypes := []string{"", "Монопаллета", "Короб СТ", "Микс", "Без коробов"}
	subjects := []string{"Футболки", "Джинсы", "Платья", "Куртки", "Обувь"}
	brands := []string{"Nike", "Adidas", "Puma", "Reebok", "New Balance"}

	for i := 0; i < count; i++ {
		rrdID := (page-1)*100000 + i + 1
		isFBW := i%3 == 0 // ~33% FBW like real data

		// Distribute rr_dt evenly across period
		var offsetMinutes int
		if count > 1 {
			offsetMinutes = periodMinutes * i / (count - 1)
		}
		rrDT := from.Add(time.Duration(offsetMinutes) * time.Minute)

		// order_dt BEFORE rr_dt (0-30 days before)
		daysBeforeRR := 1 + (i % 30)
		orderDT := rrDT.Add(-time.Duration(daysBeforeRR) * 24 * time.Hour)

		// sale_dt close to rr_dt (0-2 hours before)
		saleDT := rrDT.Add(-time.Duration(i%3) * time.Hour)

		giBoxType := ""
		deliveryMethod := ""
		if isFBW {
			giBoxType = giBoxTypes[1+(i%3)]
			deliveryMethod = "ФБВ"
		} else {
			deliveryMethod = "FBS, (МГТ)"
		}

		retailPrice := float64(2000 + i%8000)
		retailAmount := retailPrice

		rows[i] = wb.RealizationReportRow{
			RrdID:               rrdID,
			RealizationReportID: 10000 + page,
			DocTypeName:         "Продажа",
			SaleID:              fmt.Sprintf("SALE_%d", rrdID),
			DateFrom:            from.Format("2006-01-02"),
			DateTo:              to.Format("2006-01-02"),
			SupplierArticle:     fmt.Sprintf("ART_%d", i%1000),
			SubjectName:         subjects[i%len(subjects)],
			NmID:                100000 + i%50000,
			BrandName:           brands[i%len(brands)],
			TechSize:            fmt.Sprintf("%d", (i%50)+40),
			Barcode:             fmt.Sprintf("%013d", 2000000000000+int64(i)),
			Quantity:            1,
			IsCancel:            false,
			DeliveryMethod:      deliveryMethod,
			GiBoxTypeName:       giBoxType,
			OfficeName:          fmt.Sprintf("Склад-%d", i%10),
			PPVzForPay:          float64(1000 + i%9000),
			RetailPrice:         retailPrice,
			RetailAmount:        retailAmount,
			SalePercent:         15.5,
			CommissionPercent:   7.5,
			DeliveryRub:         float64(i % 100),
			OrderDT:             orderDT.Format(time.RFC3339),
			SaleDT:              saleDT.Format(time.RFC3339),
			RRDT:                rrDT.Format(time.RFC3339),
			// Financial fields
			RetailPriceWithDiscRub: retailPrice * 0.85,
			PPVzSppPrc:            25.0,
			PPVzKvwPrc:            42.0,
			PPVzKvwPrcBase:        45.0,
			PPVzSalesCommission:   retailAmount * 0.25,
			AcquiringFee:          retailAmount * 0.015,
			AcquiringPercent:      1.5,
			GiID:                  100000 + i%1000,
		}
	}

	return rows
}

// GenerateSimpleSalesRows creates minimal sales rows for quick tests.
// Uses fewer fields than GenerateSalesRows — suitable for unit tests
// that don't need realistic date relationships or financial fields.
func GenerateSimpleSalesRows(count int, date time.Time) []wb.RealizationReportRow {
	rows := make([]wb.RealizationReportRow, count)

	for i := 0; i < count; i++ {
		rows[i] = wb.RealizationReportRow{
			RrdID:           i + 1,
			NmID:            1000000 + i,
			SupplierArticle: fmt.Sprintf("ART-%03d", i),
			SubjectName:     "Test Subject",
			BrandName:       "Test Brand",
			DocTypeName:     "Продажа",
			SaleDT:          date.Format(time.RFC3339),
			Quantity:        1,
			RetailPrice:     1000.0 + float64(i),
			SalePercent:     15.0,
			GiBoxTypeName:   "Микс",
		}
	}

	return rows
}

// GenerateSalesWithRrdID creates sales rows with specific RrdID range.
// Useful for testing resume mode where RrdID deduplication matters.
func GenerateSalesWithRrdID(count, startRrdID int) []wb.RealizationReportRow {
	rows := make([]wb.RealizationReportRow, count)
	now := time.Now()

	for i := 0; i < count; i++ {
		rows[i] = wb.RealizationReportRow{
			RrdID:           startRrdID + i,
			NmID:            1000000 + i,
			SupplierArticle: fmt.Sprintf("ART-%03d", i),
			SubjectName:     "Test Subject",
			BrandName:       "Test Brand",
			DocTypeName:     "Продажа",
			SaleDT:          now.Format(time.RFC3339),
			RRDT:            now.Format(time.RFC3339),
			Quantity:        1,
			RetailPrice:     1000.0 + float64(i),
			SalePercent:     15.0,
			GiBoxTypeName:   "Микс",
		}
	}

	return rows
}

// ============================================================================
// Promotion mock data generators
// ============================================================================

// CampaignMockData holds all generated promotion mock data.
// Contains the full 4-level hierarchy: Campaign → Day → App → Nm.
type CampaignMockData struct {
	Groups  []wb.PromotionAdvertGroup
	Details map[int]wb.AdvertDetail
	Stats   map[int][]wb.CampaignFullstatsResponse
}

// GenerateCampaigns creates realistic promotion campaign data with
// full 4-level hierarchy: Campaign → Day → App → Nm.
//
// Parameters:
//   - campaignCount: number of campaigns to generate
//   - days: number of days of daily stats per campaign
func GenerateCampaigns(campaignCount, days int) CampaignMockData {
	result := CampaignMockData{
		Details: make(map[int]wb.AdvertDetail),
		Stats:   make(map[int][]wb.CampaignFullstatsResponse),
	}

	now := time.Now()

	for i := 0; i < campaignCount; i++ {
		advertID := 1000000 + i

		// Vary campaign types: Auto(9), Search(8), Catalog(50)
		campaignType := 9
		if i%3 == 0 {
			campaignType = 8
		} else if i%3 == 1 {
			campaignType = 50
		}

		// Vary statuses: Active(9), Paused(11), Finished(7)
		status := 9
		if i%5 == 0 {
			status = 11
		} else if i%5 == 1 {
			status = 7
		}

		group := wb.PromotionAdvertGroup{
			Type:   campaignType,
			Status: status,
			Count:  1,
			AdvertList: []wb.PromotionAdvert{
				{
					AdvertID:   advertID,
					ChangeTime: now.Add(-time.Duration(i) * time.Hour).Format(time.RFC3339),
				},
			},
		}
		result.Groups = append(result.Groups, group)

		// Campaign details
		paymentTypes := []string{"cpm", "cpc"}
		bidTypes := []string{"manual", "unified"}
		result.Details[advertID] = wb.AdvertDetail{
			ID:      advertID,
			BidType: bidTypes[i%2],
			Status:  status,
			Settings: wb.AdvertSettings{
				Name:        fmt.Sprintf("Test Campaign %d", advertID),
				PaymentType: paymentTypes[i%2],
				Placements: wb.AdvertPlacements{
					Search:          i%2 == 0,
					Recommendations: i%3 == 0,
				},
			},
			Timestamps: wb.AdvertTimestamps{
				Created: now.Add(-time.Duration(30+i) * 24 * time.Hour).Format(time.RFC3339),
				Updated: now.Add(-time.Duration(i) * time.Hour).Format(time.RFC3339),
				Started: now.Add(-time.Duration(29+i) * 24 * time.Hour).Format(time.RFC3339),
			},
		}

		// Generate daily stats with full hierarchy
		var daysList []wb.CampaignFullstatsDay
		totalViews, totalClicks, totalOrders, totalShks := 0, 0, 0, 0
		var totalSum, totalSumPrice float64

		for d := 0; d < days; d++ {
			date := now.AddDate(0, 0, -d).Format(time.RFC3339)
			views := 1000 + (i*100) - (d*10)
			if views < 100 {
				views = 100
			}
			clicks := views / 20
			orders := clicks / 10
			shks := orders - orders/5
			atbs := orders / 10
			canceled := orders / 20
			sum := float64(clicks) * 5.0
			sumPrice := float64(orders) * 1500.0

			totalViews += views
			totalClicks += clicks
			totalOrders += orders
			totalShks += shks
			totalSum += sum
			totalSumPrice += sumPrice

			// Platform breakdown: site=1, android=32, ios=64
			apps := []wb.CampaignFullstatsApp{
				{AppType: 1, Views: views * 60 / 100, Clicks: clicks * 60 / 100, Orders: orders * 60 / 100, Shks: shks * 60 / 100, Sum: sum * 0.6, SumPrice: sumPrice * 0.6},
				{AppType: 32, Views: views * 25 / 100, Clicks: clicks * 25 / 100, Orders: orders * 25 / 100, Shks: shks * 25 / 100, Sum: sum * 0.25, SumPrice: sumPrice * 0.25},
				{AppType: 64, Views: views * 15 / 100, Clicks: clicks * 15 / 100, Orders: orders * 15 / 100, Shks: shks * 15 / 100, Sum: sum * 0.15, SumPrice: sumPrice * 0.15},
			}

			// Product breakdown per platform (2 products per app)
			for ai := range apps {
				nmBase := 2000000 + i*10
				apps[ai].Nms = []wb.CampaignFullstatsNm{
					{NmID: nmBase, Name: fmt.Sprintf("Product %d-A", i), Views: apps[ai].Views * 70 / 100, Clicks: apps[ai].Clicks * 70 / 100, Orders: apps[ai].Orders * 70 / 100, Shks: apps[ai].Shks * 70 / 100, Sum: apps[ai].Sum * 0.7, SumPrice: apps[ai].SumPrice * 0.7},
					{NmID: nmBase + 1, Name: fmt.Sprintf("Product %d-B", i), Views: apps[ai].Views * 30 / 100, Clicks: apps[ai].Clicks * 30 / 100, Orders: apps[ai].Orders * 30 / 100, Shks: apps[ai].Shks * 30 / 100, Sum: apps[ai].Sum * 0.3, SumPrice: apps[ai].SumPrice * 0.3},
				}
			}

			daysList = append(daysList, wb.CampaignFullstatsDay{
				Date:     date,
				Views:    views,
				Clicks:   clicks,
				CTR:      float64(clicks) / float64(views) * 100,
				CPC:      5.0 + float64(i%5),
				CR:       float64(orders) / float64(clicks) * 100,
				Orders:   orders,
				Shks:     shks,
				Atbs:     atbs,
				Canceled: canceled,
				Sum:      sum,
				SumPrice: sumPrice,
				Apps:     apps,
			})
		}

		result.Stats[advertID] = []wb.CampaignFullstatsResponse{
			{
				AdvertID: advertID,
				Views:    totalViews,
				Clicks:   totalClicks,
				CTR:      float64(totalClicks) / float64(totalViews) * 100,
				CPC:      5.0 + float64(i%5),
				CR:       float64(totalOrders) / float64(totalClicks) * 100,
				Orders:   totalOrders,
				Shks:     totalShks,
				Sum:      totalSum,
				SumPrice: totalSumPrice,
				Days:     daysList,
			},
		}
	}

	return result
}

// ============================================================================
// Feedbacks mock data generators
// ============================================================================

// GenerateFeedbacks creates realistic mock feedback data.
// Uses wb.Feedback type (pkg/wb) which is the API-level representation.
func GenerateFeedbacks(rng *rand.Rand, count int) []wb.Feedback {
	items := make([]wb.Feedback, count)

	for i := 0; i < count; i++ {
		f := wb.Feedback{
			ID:               fmt.Sprintf("mock-feedback-%d", i),
			Text:             fmt.Sprintf("Mock feedback text %d", i),
			ProductValuation: rng.Intn(5) + 1,
			CreatedDate:      time.Now().Add(-time.Duration(rng.Intn(168)) * time.Hour).Format(time.RFC3339),
			UserName:         fmt.Sprintf("User%d", i),
			ProductDetails: wb.FeedbackProduct{
				ImtID:       rng.Intn(1000000000) + 10000000,
				NmId:        rng.Intn(100000000) + 100000,
				ProductName: fmt.Sprintf("Product %d", i),
				BrandName:   "Test Brand",
			},
		}

		// 33% chance of having an answer
		if rng.Intn(3) == 0 {
			f.Answer = &wb.FeedbackAnswer{
				Text:  "Answer text",
				State: "wbRu",
			}
		}

		items[i] = f
	}

	return items
}

// GenerateQuestions creates realistic mock question data.
// Uses wb.Question type (pkg/wb) which is the API-level representation.
func GenerateQuestions(rng *rand.Rand, count int) []wb.Question {
	items := make([]wb.Question, count)

	for i := 0; i < count; i++ {
		q := wb.Question{
			ID:          fmt.Sprintf("mock-question-%d", i),
			Text:        fmt.Sprintf("Mock question text %d?", i),
			CreatedDate: time.Now().Add(-time.Duration(rng.Intn(168)) * time.Hour).Format(time.RFC3339),
			State:       "suppliersPortalSynch",
			WasViewed:   rng.Intn(2) == 0,
			ProductDetails: wb.QuestionProduct{
				ImtID:           rng.Intn(1000000000) + 10000000,
				NmId:            rng.Intn(100000000) + 100000,
				ProductName:     fmt.Sprintf("Product %d", i),
				SupplierArticle: fmt.Sprintf("ART-%d", i),
				SupplierName:    "Test Supplier",
				BrandName:       "Test Brand",
			},
		}

		// 50% chance of having an answer
		if rng.Intn(2) == 0 {
			q.Answer = &wb.QuestionAnswer{
				Text: "Answer text",
			}
		}

		items[i] = q
	}

	return items
}
