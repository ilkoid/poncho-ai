package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
)

// OneCRestsClient fetches warehouse stock levels from the 1C RESTs API.
type OneCRestsClient struct {
	httpClient *http.Client
}

// NewOneCRestsClient creates a client with generous timeout for 82MB payloads.
func NewOneCRestsClient() *OneCRestsClient {
	return &OneCRestsClient{
		httpClient: &http.Client{
			Timeout: 15 * time.Minute,
		},
	}
}

// FetchRests fetches and saves warehouse stock levels using streaming decode.
// Applies storage filter during parsing — filtered-out rows never hit the DB.
// Returns (goodsCount, totalRowsSaved, filteredOut, error).
func (c *OneCRestsClient) FetchRests(ctx context.Context, apiURL string, filter config.OneCRestsStorageFilter, repo *sqlite.SQLiteSalesRepository, snapshotDate string) (int, int, int, error) {
	body, err := c.fetchBody(ctx, apiURL)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("fetch rests: %w", err)
	}
	defer body.Close()

	dec := json.NewDecoder(body)
	if err := expectDelim(dec, '['); err != nil {
		return 0, 0, 0, err
	}

	batch := make([]sqlite.OneCRestsRow, 0, 500)
	totalGoods := 0
	totalSaved := 0
	filteredOut := 0

	for dec.More() {
		var item OneCRestItem
		if err := dec.Decode(&item); err != nil {
			return totalGoods, totalSaved, filteredOut, fmt.Errorf("decode good #%d: %w", totalGoods+1, err)
		}
		totalGoods++

		for _, skuRaw := range item.SKU {
			// SKU entries are objects with dynamic keys (SKU GUIDs):
			//   {"_13209c78_1651_11e4_9401_2c768a56a25b": [...storage rows...]}
			skuMap := make(map[string]json.RawMessage)
			if err := json.Unmarshal(skuRaw, &skuMap); err != nil {
				continue
			}

			for skuGUID, storageRaw := range skuMap {
				var storages []OneCStorageRow
				if err := json.Unmarshal(storageRaw, &storages); err != nil {
					continue
				}

				for _, s := range storages {
					if !filter.Matches(s.StorageGUID, s.StorageName) {
						filteredOut++
						continue
					}

					batch = append(batch, sqlite.OneCRestsRow{
						GoodGUID:    item.GoodGUID,
						SKUGUID:     skuGUID,
						StorageGUID: s.StorageGUID,
						StorageName: s.StorageName,
						Stock:       s.Stock,
						Reserv:      s.Reserv,
						Free:        s.Free,
						FirstStage:  s.FirstStage,
					})
				}
			}
		}

		if len(batch) >= 500 {
			n, err := repo.SaveOneCRests(ctx, batch, snapshotDate)
			if err != nil {
				return totalGoods, totalSaved, filteredOut, err
			}
			totalSaved += n
			batch = batch[:0]
		}
	}

	if len(batch) > 0 {
		n, err := repo.SaveOneCRests(ctx, batch, snapshotDate)
		if err != nil {
			return totalGoods, totalSaved, filteredOut, err
		}
		totalSaved += n
	}

	return totalGoods, totalSaved, filteredOut, nil
}

// fetchBody makes a GET request and returns the response body for streaming.
// Supports basic auth embedded in URL (user:pass@host).
func (c *OneCRestsClient) fetchBody(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}

	if u.User != nil {
		password, _ := u.User.Password()
		req.SetBasicAuth(u.User.Username(), password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, resp.Status)
	}

	return resp.Body, nil
}

// expectDelim reads and validates the opening JSON delimiter.
func expectDelim(dec *json.Decoder, expected json.Delim) error {
	t, err := dec.Token()
	if err != nil {
		return fmt.Errorf("read opening token: %w", err)
	}
	d, ok := t.(json.Delim)
	if !ok || d != expected {
		return fmt.Errorf("expected '%c', got %v", expected, t)
	}
	return nil
}
