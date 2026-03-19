// Package main provides WB Sales Downloader utility.
// This file contains funnel analytics loading logic.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// FunnelLoadResult holds statistics after funnel data loading.
type FunnelLoadResult struct {
	ProductsLoaded int
	MetricsLoaded  int
	Duration       time.Duration
}

// FunnelLoaderConfig holds configuration for funnel loading.
type FunnelLoaderConfig struct {
	Client        *wb.Client
	Repo          *sqlite.SQLiteSalesRepository
	Days          int    // Days of history to load (1-365) — fallback if From/To not set
	BatchSize     int    // Products per API request (max 20 for Analytics API v3)
	RefreshWindow int    // Refresh window in days (0 = always replace, 7 = update last 7 days)
	From          string // Start date YYYY-MM-DD (optional, takes precedence over Days)
	To            string // End date YYYY-MM-DD (optional, takes precedence over Days)
	MaxBatches    int    // Max batches to load (0 = all, useful for testing)
}

// LoadFunnelHistory loads funnel analytics history for all products in sales table.
// Uses WB Analytics API v3 with rate limiting (3 req/min) via wb.Client.
func LoadFunnelHistory(ctx context.Context, cfg FunnelLoaderConfig) (*FunnelLoadResult, error) {
	start := time.Now()
	result := &FunnelLoadResult{}

	// Show refresh window info
	if cfg.RefreshWindow > 0 {
		windowStart := time.Now().AddDate(0, 0, -cfg.RefreshWindow)
		fmt.Printf("🔄 Refresh window: %d дней (обновление с %s)\n",
			cfg.RefreshWindow, windowStart.Format("2006-01-02"))
	} else {
		fmt.Println("🔄 Refresh window: отключён (полная замена данных)")
	}
	fmt.Println()

	// 1. Get list of products from sales table
	nmIDs, err := cfg.Repo.GetDistinctNmIDs(ctx)
	if err != nil {
		return result, fmt.Errorf("get product list: %w", err)
	}

	if len(nmIDs) == 0 {
		fmt.Println("⚠️  Нет товаров в базе sales - нечего загружать")
		return result, nil
	}

	fmt.Printf("📊 Найдено товаров: %d\n", len(nmIDs))

	// Show period info
	if cfg.From != "" && cfg.To != "" {
		fmt.Printf("📅 Период: %s → %s\n", cfg.From, cfg.To)
	} else {
		fmt.Printf("📅 Загрузка истории: %d дней\n", cfg.Days)
	}

	// Default batch size
	// API limit: max 20 nmIds per request (Analytics API v3)
	if cfg.BatchSize <= 0 || cfg.BatchSize > 20 {
		cfg.BatchSize = 20
	}

	// 2. Load funnel history in batches
	batches := chunkIntSlice(nmIDs, cfg.BatchSize)

	// Apply max_batches limit if set (useful for testing)
	totalBatches := len(batches)
	batchesToProcess := totalBatches
	if cfg.MaxBatches > 0 && cfg.MaxBatches < totalBatches {
		batchesToProcess = cfg.MaxBatches
		fmt.Printf("⚠️  Ограничение: %d/%d батчей (max_batches=%d)\n", batchesToProcess, totalBatches, cfg.MaxBatches)
	}

	fmt.Printf("📦 Батчей: %d (по %d товаров)\n", batchesToProcess, cfg.BatchSize)
	fmt.Println()

	for i, batch := range batches[:batchesToProcess] {
		select {
		case <-ctx.Done():
			fmt.Println("\n⚠️  Прервано пользователем")
			return result, ctx.Err()
		default:
		}

		batchStart := time.Now()
		fmt.Printf("🔄 Батч %d/%d: %d товаров... ",
			i+1, len(batches), len(batch))

		// Load funnel history for this batch
		// wb.Client automatically enforces rate limiting (3 req/min = 20s between requests)
		productsLoaded, metricsLoaded, err := loadFunnelBatch(ctx, cfg.Client, cfg.Repo, batch, cfg.From, cfg.To, cfg.Days, cfg.RefreshWindow)
		if err != nil {
			fmt.Printf("❌ Ошибка: %v\n", err)
			// Continue with next batch on error
			continue
		}

		result.ProductsLoaded += productsLoaded
		result.MetricsLoaded += metricsLoaded
		result.Duration = time.Since(start)

		// Calculate effective rate (includes client-side rate limiting)
		batchDuration := time.Since(batchStart)
		fmt.Printf("✅ %d товаров, %d метрик (батч: %s, всего: %s)\n",
			productsLoaded, metricsLoaded, batchDuration.Round(time.Second), result.Duration.Round(time.Second))
	}

	result.Duration = time.Since(start)
	return result, nil
}

// loadFunnelBatch loads funnel history for a single batch of products.
func loadFunnelBatch(ctx context.Context, client *wb.Client, repo *sqlite.SQLiteSalesRepository, nmIDs []int, from, to string, days int, refreshDays int) (productsLoaded, metricsLoaded int, err error) {
	// Determine period: explicit from/to takes precedence over days
	var begin, end time.Time

	if from != "" && to != "" {
		// Explicit dates from config
		begin, err = time.Parse("2006-01-02", from)
		if err != nil {
			return 0, 0, fmt.Errorf("parse from date %q: %w", from, err)
		}
		end, err = time.Parse("2006-01-02", to)
		if err != nil {
			return 0, 0, fmt.Errorf("parse to date %q: %w", to, err)
		}
	} else {
		// Relative period (days)
		now := time.Now()
		begin = now.AddDate(0, 0, -days)
		end = now
	}

	reqBody := map[string]interface{}{
		"nmIds": nmIDs,
		"selectedPeriod": map[string]string{
			"start": begin.Format("2006-01-02"),
			"end":   end.Format("2006-01-02"),
		},
	}

	// Make API request
	// API v3 returns array directly: [{product: {...}, history: [...], currency: "RUB"}]
	// Note: history is at top level, not nested in statistic wrapper
	var response []struct {
		Product struct {
			NmID           int     `json:"nmId"`
			Title          string  `json:"title"`
			VendorCode     string  `json:"vendorCode"`
			BrandName      string  `json:"brandName"`
			SubjectID      int     `json:"subjectId"`
			SubjectName    string  `json:"subjectName"`
			ProductRating  float64 `json:"productRating"`
			FeedbackRating float64 `json:"feedbackRating"`
			Stocks         struct {
				WB         int `json:"wb"`
				MP         int `json:"mp"`
				BalanceSum int `json:"balanceSum"`
			} `json:"stocks"`
		} `json:"product"`
		History []struct {
			Date                  string  `json:"date"`
			OpenCount             int     `json:"openCount"`
			CartCount             int     `json:"cartCount"`
			OrderCount            int     `json:"orderCount"`
			OrderSum              int     `json:"orderSum"`
			BuyoutCount           int     `json:"buyoutCount"`
			BuyoutSum             int     `json:"buyoutSum"`
			BuyoutPercent         float64 `json:"buyoutPercent"`
			AddToCartConversion   float64 `json:"addToCartConversion"`
			CartToOrderConversion float64 `json:"cartToOrderConversion"`
			AddToWishlistCount    int     `json:"addToWishlistCount"`
		} `json:"history"`
		Currency string `json:"currency"`
	}

	err = client.Post(ctx, "get_wb_product_funnel_history",
		"https://seller-analytics-api.wildberries.ru",
		3, 3,
		"/api/analytics/v3/sales-funnel/products/history",
		reqBody, &response)
	if err != nil {
		return 0, 0, fmt.Errorf("API request: %w", err)
	}

	// Process each product
	for _, p := range response {
		// Build product metadata
		productMeta := wb.FunnelProductMeta{
			NmID:          p.Product.NmID,
			VendorCode:    p.Product.VendorCode,
			Title:         p.Product.Title,
			BrandName:     p.Product.BrandName,
			SubjectID:     p.Product.SubjectID,
			SubjectName:   p.Product.SubjectName,
			ProductRating: p.Product.ProductRating,
			FeedbackRating: p.Product.FeedbackRating,
			StockWB:       p.Product.Stocks.WB,
			StockMP:       p.Product.Stocks.MP,
			StockBalance:  p.Product.Stocks.BalanceSum,
		}

		// Build history rows
		var historyRows []wb.FunnelHistoryRow
		for _, h := range p.History {
			row := wb.FunnelHistoryRow{
				NmID:                  p.Product.NmID,
				MetricDate:            h.Date,
				OpenCount:             h.OpenCount,
				CartCount:             h.CartCount,
				OrderCount:            h.OrderCount,
				BuyoutCount:           h.BuyoutCount,
				CancelCount:           0, // Not in simplified v3 history response
				AddToWishlist:         h.AddToWishlistCount,
				OrderSum:              h.OrderSum,
				BuyoutSum:             h.BuyoutSum,
				CancelSum:             0, // Not in simplified v3 history response
				AvgPrice:              0, // Not in simplified v3 history response
				ConversionAddToCart:   h.AddToCartConversion,
				ConversionCartToOrder: h.CartToOrderConversion,
				ConversionBuyout:      h.BuyoutPercent,
				WBClubOrderCount:      0, // Not in simplified v3 history response
				WBClubBuyoutCount:     0, // Not in simplified v3 history response
				WBClubBuyoutPercent:   0, // Not in simplified v3 history response
				TimeToReadyDays:       0, // Not in simplified v3 history response
				TimeToReadyHours:      0, // Not in simplified v3 history response
				TimeToReadyMins:       0, // Not in simplified v3 history response
				LocalizationPercent:   0, // Not in simplified v3 history response
			}
			historyRows = append(historyRows, row)
		}

		// Save to database
		// Use SaveFunnelHistoryWithWindow if refresh window is enabled
		if err := repo.SaveFunnelHistoryWithWindow(ctx, productMeta, historyRows, refreshDays); err != nil {
			log.Printf("⚠️  Ошибка сохранения nm_id=%d: %v", p.Product.NmID, err)
			continue
		}

		productsLoaded++
		metricsLoaded += len(historyRows)
	}

	return productsLoaded, metricsLoaded, nil
}

// chunkIntSlice splits a slice of ints into chunks of specified size.
func chunkIntSlice(slice []int, size int) [][]int {
	var chunks [][]int
	for i := 0; i < len(slice); i += size {
		end := i + size
		if end > len(slice) {
			end = len(slice)
		}
		chunks = append(chunks, slice[i:end])
	}
	return chunks
}

// PrintFunnelSummary prints funnel loading summary with trend analysis.
func PrintFunnelSummary(result *FunnelLoadResult, repo *sqlite.SQLiteSalesRepository) {
	fmt.Println()
	fmt.Println(repeat("=", 71))
	fmt.Println("📊 ИТОГИ ЗАГРУЗКИ FUNNEL")
	fmt.Println(repeat("=", 71))
	fmt.Printf("Товаров загружено:  %d\n", result.ProductsLoaded)
	fmt.Printf("Метрик загружено:   %d\n", result.MetricsLoaded)
	fmt.Printf("Время выполнения:   %s\n", result.Duration.Round(time.Second))
	fmt.Println()

	// Show trending products
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	trending, err := repo.GetTrendingProducts(ctx, 10)
	if err != nil {
		fmt.Printf("⚠️  Не удалось получить тренды: %v\n", err)
		return
	}

	if len(trending) == 0 {
		fmt.Println("📈 Недостаточно данных для анализа трендов")
		return
	}

	fmt.Println("📈 ТОП-10 ТРЕНДОВЫХ ТОВАРОВ (неделя к неделе)")
	fmt.Println(repeat("-", 71))
	fmt.Printf("%-12s %-30s %8s %8s %12s\n",
		"nm_id", "название", "7д", "пр.7д", "рост%")
	fmt.Println(repeat("-", 71))

	for _, tp := range trending {
		title := tp.Title
		if len(title) > 28 {
			title = title[:25] + "..."
		}

		growth := "---"
		if tp.OrderGrowthPercent != 0 {
			growth = fmt.Sprintf("%+.1f%%", tp.OrderGrowthPercent)
		}

		status := ""
		switch tp.TrendStatus {
		case "TRENDING_UP":
			status = " 📈"
		case "TRENDING_DOWN":
			status = " 📉"
		case "NEW":
			status = " 🆕"
		}

		fmt.Printf("%-12d %-30s %8d %8d %12s%s\n",
			tp.NmID, title, tp.Orders7d, tp.OrdersPrev7d, growth, status)
	}
}

// FunnelMockLoader generates mock funnel data for testing.
// Uses refresh window logic identical to real loader for comprehensive testing.
func FunnelMockLoader(ctx context.Context, repo *sqlite.SQLiteSalesRepository, days int, refreshDays int) (*FunnelLoadResult, error) {
	start := time.Now()
	result := &FunnelLoadResult{}

	// Show refresh window info (same as real loader)
	if refreshDays > 0 {
		windowStart := time.Now().AddDate(0, 0, -refreshDays)
		fmt.Printf("🔄 Refresh window: %d дней (обновление с %s)\n",
			refreshDays, windowStart.Format("2006-01-02"))
	} else {
		fmt.Println("🔄 Refresh window: отключён (полная замена данных)")
	}
	fmt.Println()

	// Get products from sales table
	nmIDs, err := repo.GetDistinctNmIDs(ctx)
	if err != nil {
		return result, fmt.Errorf("get product list: %w", err)
	}

	if len(nmIDs) == 0 {
		fmt.Println("⚠️  Нет товаров в базе sales - генерирую моковые nm_id")
		// Generate some mock nm_ids
		nmIDs = []int{1001, 1002, 1003, 1004, 1005}
	}

	fmt.Printf("📊 Генерация моковых данных для %d товаров, %d дней\n", len(nmIDs), days)

	// Generate mock data for each product
	for _, nmID := range nmIDs {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		// Create mock product metadata
		productMeta := wb.FunnelProductMeta{
			NmID:          nmID,
			VendorCode:    fmt.Sprintf("ART%d", nmID),
			Title:         fmt.Sprintf("Mock Product %d", nmID),
			BrandName:     "Mock Brand",
			SubjectName:   "Mock Category",
			ProductRating: 4.5,
			StockWB:       50,
			StockMP:       20,
		}

		// Generate mock history
		var historyRows []wb.FunnelHistoryRow
		now := time.Now()

		for i := 0; i < days; i++ {
			date := now.AddDate(0, 0, -i).Format("2006-01-02")

			// Generate some variation
			openCount := 1000 + (nmID%500) - i*10
			cartCount := openCount / 10
			orderCount := cartCount / 3
			buyoutCount := int(float64(orderCount) * 0.85)

			row := wb.FunnelHistoryRow{
				NmID:                nmID,
				MetricDate:          date,
				OpenCount:           openCount,
				CartCount:           cartCount,
				OrderCount:          orderCount,
				BuyoutCount:         buyoutCount,
				CancelCount:         orderCount - buyoutCount,
				AddToWishlist:       openCount / 3,
				OrderSum:            orderCount * 1500,
				BuyoutSum:           buyoutCount * 1500,
				CancelSum:           (orderCount - buyoutCount) * 1500,
				AvgPrice:            1500,
				ConversionAddToCart: float64(cartCount) / float64(openCount) * 100,
				ConversionCartToOrder: float64(orderCount) / float64(cartCount) * 100,
				ConversionBuyout:    85.0,
				WBClubOrderCount:    orderCount / 2,
				WBClubBuyoutCount:   buyoutCount / 2,
				WBClubBuyoutPercent: 85.0,
				TimeToReadyDays:     1,
				TimeToReadyHours:    8,
				LocalizationPercent: 45.0,
			}
			historyRows = append(historyRows, row)
		}

		// Save to database with refresh window logic
		if refreshDays > 0 {
			if err := repo.SaveFunnelHistoryWithWindow(ctx, productMeta, historyRows, refreshDays); err != nil {
				log.Printf("⚠️  Ошибка сохранения nm_id=%d: %v", nmID, err)
				continue
			}
		} else {
			// No refresh window - use standard REPLACE
			if err := repo.SaveFunnelHistory(ctx, productMeta, historyRows); err != nil {
				log.Printf("⚠️  Ошибка сохранения nm_id=%d: %v", nmID, err)
				continue
			}
		}

		result.ProductsLoaded++
		result.MetricsLoaded += len(historyRows)
	}

	result.Duration = time.Since(start)
	return result, nil
}

// init ensures json package is imported
func init() {
	_ = json.Marshal
}
