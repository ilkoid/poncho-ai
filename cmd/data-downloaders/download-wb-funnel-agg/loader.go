// Package main provides WB Aggregated Funnel Downloader utility.
// This file contains loading logic with pagination.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
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

	// Get total product count for progress estimation (from sales table)
	totalProducts := 0
	if count, err := cfg.Repo.GetDistinctNmIDCount(ctx); err == nil {
		totalProducts = count
	}

	fmt.Println("🔄 Начинаем загрузку с пагинацией...")
	fmt.Printf("📅 Период: %s → %s\n", cfg.Config.SelectedStart, cfg.Config.SelectedEnd)
	if cfg.Config.PastStart != "" && cfg.Config.PastEnd != "" {
		fmt.Printf("📅 Past:   %s → %s\n", cfg.Config.PastStart, cfg.Config.PastEnd)
	}
	if totalProducts > 0 {
		fmt.Printf("📊 Товаров в БД: ~%d (оценка прогресса)\n", totalProducts)
	}
	fmt.Println()

	// Offset resume: skip already-downloaded pages
	existingCount, resumeErr := cfg.Repo.GetFunnelAggregatedCount(ctx, cfg.Config.SelectedStart, cfg.Config.SelectedEnd)
	if resumeErr == nil && existingCount > 0 {
		// Overlap by one page for safety (INSERT OR REPLACE handles duplicates)
		offset = max(0, existingCount-pageSize)
		totalLoaded = offset
		fmt.Printf("📎 Resume: %d товаров уже в БД за период %s→%s, начинаем с offset=%d\n\n",
			existingCount, cfg.Config.SelectedStart, cfg.Config.SelectedEnd, offset)
	}

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

		// Calculate progress
		var progressInfo string
		if totalProducts > 0 {
			remaining := totalProducts - totalLoaded
			percent := float64(totalLoaded) / float64(totalProducts) * 100
			progressInfo = fmt.Sprintf("прогресс: %d/%d (%.1f%%, осталось: ~%d)", totalLoaded, totalProducts, percent, remaining)
		} else {
			progressInfo = fmt.Sprintf("загружено: %d", totalLoaded)
		}
		fmt.Printf("🔄 Страница %d (offset=%d, %s)... ", pageNum, offset, progressInfo)

		// Make API call with page-level retry for global limiter recovery
		const maxPageRetries = 3
		const pageRetryBaseSleep = 2 * time.Minute

		var response wb.FunnelAggregatedResponse
		var apiErr error
		rl := cfg.Config.RateLimits

		for retry := range maxPageRetries {
			response = wb.FunnelAggregatedResponse{}
			apiErr = cfg.Client.Post(ctx, "get_wb_funnel_aggregated",
				"https://seller-analytics-api.wildberries.ru",
				rl.FunnelAggregated, rl.FunnelAggregatedBurst,
				"/api/analytics/v3/sales-funnel/products",
				req, &response)

			if apiErr == nil {
				break
			}
			if retry < maxPageRetries-1 {
				sleepDur := pageRetryBaseSleep * time.Duration(retry+1)
				fmt.Printf("\n⏳ Глобальный лимитер, ожидание %v (попытка страницы %d/%d)...",
					sleepDur, retry+2, maxPageRetries)
				time.Sleep(sleepDur)
			}
		}

		if apiErr != nil {
			fmt.Printf("❌ Ошибка API после %d попыток страницы: %v\n", maxPageRetries, apiErr)
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

		// Информативный вывод с общим прогрессом
		if apiCount != saved {
			if totalProducts > 0 {
				remaining := totalProducts - totalLoaded
				percent := float64(totalLoaded) / float64(totalProducts) * 100
				fmt.Printf("⚠️  API: %d, Сохранено: %d (страница: %s, %d/%d: %.1f%%, осталось: ~%d, время: %s)\n",
					apiCount, saved, time.Since(pageStart).Round(time.Second),
					totalLoaded, totalProducts, percent, remaining, result.Duration.Round(time.Second))
			} else {
				fmt.Printf("⚠️  API: %d товаров, Сохранено: %d (страница: %s, прогресс: %d всего, время: %s)\n",
					apiCount, saved, time.Since(pageStart).Round(time.Second), totalLoaded, result.Duration.Round(time.Second))
			}
		} else {
			if totalProducts > 0 {
				remaining := totalProducts - totalLoaded
				percent := float64(totalLoaded) / float64(totalProducts) * 100
				fmt.Printf("✅ %d товаров (страница: %s, %d/%d: %.1f%%, осталось: ~%d, время: %s)\n",
					saved, time.Since(pageStart).Round(time.Second),
					totalLoaded, totalProducts, percent, remaining, result.Duration.Round(time.Second))
			} else {
				fmt.Printf("✅ %d товаров (страница: %s, прогресс: %d всего, время: %s)\n",
					saved, time.Since(pageStart).Round(time.Second), totalLoaded, result.Duration.Round(time.Second))
			}
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
	dllog.PrintHeader("AGGREGATED FUNNEL RESULTS",
		dllog.HeaderField{Key: "Period", Value: periodStart + " -> " + periodEnd},
		dllog.HeaderField{Key: "Products loaded", Value: fmt.Sprintf("%d", result.ProductsLoaded)},
		dllog.HeaderField{Key: "Pages loaded", Value: fmt.Sprintf("%d", result.PagesLoaded)},
	)
	dllog.Done(result.Duration, "%d products from %d pages", result.ProductsLoaded, result.PagesLoaded)
	fmt.Println()
}
