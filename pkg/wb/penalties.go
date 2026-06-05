// Package wb provides Measurement Penalties API methods.
// GET /api/analytics/v1/measurement-penalties — штрафы за неверные габариты упаковки.
package wb

import (
	"context"
	"fmt"
	"net/url"
)

const (
	penaltiesBaseURL = "https://seller-analytics-api.wildberries.ru"
	penaltiesPath    = "/api/analytics/v1/measurement-penalties"
)

// GetMeasurementPenalties fetches one page of dimension penalties from WB Seller Analytics API.
// GET /api/analytics/v1/measurement-penalties?dateFrom=...&dateTo=...&limit=N&offset=M
//
// Offset-пагинация: max 1000 за запрос, есть total.
// Rate limit: 1 req/min (shared seller-analytics pool).
func (c *Client) GetMeasurementPenalties(ctx context.Context, dateFrom, dateTo string, limit, offset, rateLimit, burst int) ([]MeasurementPenaltyItem, int, error) {
	params := url.Values{}
	params.Set("dateFrom", dateFrom)
	params.Set("dateTo", dateTo)
	params.Set("limit", fmt.Sprintf("%d", limit))
	params.Set("offset", fmt.Sprintf("%d", offset))

	var resp MeasurementPenaltiesResponse
	err := c.Get(ctx, "wb_measurement_penalties", penaltiesBaseURL, rateLimit, burst, penaltiesPath, params, &resp)
	if err != nil {
		return nil, 0, fmt.Errorf("measurement penalties %s to %s (offset=%d): %w", dateFrom, dateTo, offset, err)
	}

	return resp.Data.Reports, resp.Data.Total, nil
}

// MeasurementPenaltiesIterator итерирует по всем страницам штрафов (offset-пагинация).
// Callback вызывается для каждой страницы с (items, total).
// Возвращает общее кол-во обработанных записей.
//
// Логика: offset=0 → запрос → callback → offset += len(items) → повторять пока offset < total.
func (c *Client) MeasurementPenaltiesIterator(
	ctx context.Context,
	dateFrom, dateTo string,
	rateLimit, burst int,
	callback func([]MeasurementPenaltyItem, int) error,
) (int, error) {
	const pageSize = 1000
	var totalProcessed int
	var offset int

	for {
		select {
		case <-ctx.Done():
			return totalProcessed, ctx.Err()
		default:
		}

		items, total, err := c.GetMeasurementPenalties(ctx, dateFrom, dateTo, pageSize, offset, rateLimit, burst)
		if err != nil {
			return totalProcessed, fmt.Errorf("page offset=%d: %w", offset, err)
		}

		if len(items) == 0 {
			break
		}

		if err := callback(items, total); err != nil {
			return totalProcessed, fmt.Errorf("callback at offset=%d: %w", offset, err)
		}

		totalProcessed += len(items)
		offset += len(items)

		if offset >= total {
			break
		}
	}

	return totalProcessed, nil
}
