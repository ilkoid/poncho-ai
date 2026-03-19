// Package main provides WB Aggregated Funnel Downloader utility.
// This file contains loading logic with pagination.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// AggregatedLoadResult holds loading statistics.
type AggregatedLoadResult struct {
	ProductsLoaded int
	PagesLoaded    int
	Duration       time.Duration
}

// AggregatedLoaderConfig holds loader configuration.
type AggregatedLoaderConfig struct {
	Client *wb.Client
	Repo   *sqlite.SQLiteSalesRepository
	Config config.FunnelAggregatedConfig
}

// LoadAggregatedFunnel loads aggregated funnel data with pagination.
// Automatically pages through all products until empty response.
func LoadAggregatedFunnel(ctx context.Context, cfg AggregatedLoaderConfig) (*AggregatedLoadResult, error) {
	start := time.Now()
	result := &AggregatedLoadResult{}

	pageSize := cfg.Config.PageSize
	offset := 0
	totalLoaded := 0
	pages := 0

	fmt.Println("🔄 Начинаем загрузку с пагинацией...")
	fmt.Println()

	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		// Build request
		req := wb.FunnelAggregatedRequest{
			SelectedPeriod: wb.PeriodRange{
				Start: cfg.Config.SelectedStart,
				End:   cfg.Config.SelectedEnd,
			},
			NmIDs:         cfg.Config.NmIDs,
			BrandNames:    cfg.Config.BrandNames,
			SubjectIDs:    cfg.Config.SubjectIDs,
			TagIDs:        cfg.Config.TagIDs,
			SkipDeletedNm: cfg.Config.SkipDeletedNm,
			Limit:         pageSize,
			Offset:        offset,
		}

		// Add past period if configured
		if cfg.Config.PastStart != "" && cfg.Config.PastEnd != "" {
			req.PastPeriod = &wb.PeriodRange{
				Start: cfg.Config.PastStart,
				End:   cfg.Config.PastEnd,
			}
		}

		// Add ordering if configured
		if cfg.Config.OrderByField != "" {
			req.OrderBy = &wb.OrderBy{
				Field: cfg.Config.OrderByField,
				Mode:  cfg.Config.OrderByMode,
			}
		}

		pageStart := time.Now()
		pageNum := offset/pageSize + 1
		fmt.Printf("🔄 Страница %d (offset=%d)... ", pageNum, offset)

		// Make API call
		var response wb.FunnelAggregatedResponse
		err := cfg.Client.Post(ctx, "get_wb_funnel_aggregated",
			"https://seller-analytics-api.wildberries.ru",
			cfg.Config.RateLimit, cfg.Config.BurstLimit,
			"/api/analytics/v3/sales-funnel/products",
			req, &response)

		if err != nil {
			fmt.Printf("❌ Ошибка API: %v\n", err)
			break
		}

		products := response.Data.Products
		apiCount := len(products)

		// Если пустой ответ от API - нет данных за период
		if apiCount == 0 {
			if pageNum == 1 {
				fmt.Println("ℹ️  Нет данных за указанный период")
			} else {
				fmt.Println("✅ Все данные загружены")
			}
			break
		}

		// Save to database
		saved, err := cfg.Repo.SaveFunnelAggregatedBatch(
			ctx, products,
			cfg.Config.SelectedStart, cfg.Config.SelectedEnd,
			response.Data.Currency,
		)
		if err != nil {
			fmt.Printf("❌ API: %d товаров, ❌ Ошибка сохранения: %v (страница: %s)\n",
				apiCount, err, time.Since(pageStart).Round(time.Second))
			break
		}

		totalLoaded += saved
		pages++
		result.ProductsLoaded = totalLoaded
		result.PagesLoaded = pages
		result.Duration = time.Since(start)

		// Информативный вывод
		if apiCount != saved {
			fmt.Printf("⚠️  API: %d товаров, Сохранено: %d (страница: %s, всего: %s)\n",
				apiCount, saved, time.Since(pageStart).Round(time.Second), result.Duration.Round(time.Second))
		} else {
			fmt.Printf("✅ %d товаров (страница: %s, всего: %s)\n",
				saved, time.Since(pageStart).Round(time.Second), result.Duration.Round(time.Second))
		}

		// Check if we got less than pageSize - means last page
		if apiCount < pageSize {
			fmt.Println("✅ Последняя страница загружена")
			break
		}

		offset += pageSize
	}

	return result, nil
}

// PrintAggregatedSummary prints loading summary.
func PrintAggregatedSummary(result *AggregatedLoadResult, periodStart, periodEnd string) {
	fmt.Println()
	fmt.Println(repeat("=", 71))
	fmt.Println("📊 ИТОГИ ЗАГРУЗКИ AGGREGATED FUNNEL")
	fmt.Println(repeat("=", 71))
	fmt.Printf("Период:            %s → %s\n", periodStart, periodEnd)
	fmt.Printf("Товаров загружено: %d\n", result.ProductsLoaded)
	fmt.Printf("Страниц загружено: %d\n", result.PagesLoaded)
	fmt.Printf("Время выполнения:  %s\n", result.Duration.Round(time.Second))
	fmt.Println()
}
