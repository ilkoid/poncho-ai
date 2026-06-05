package onec

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// HTTPRestsSource — real API adapter (streaming JSON decode + batch writes)
// ---------------------------------------------------------------------------

const restsHTTPTimeout = 15 * time.Minute // 82MB payload needs generous timeout

// HTTPRestsSource fetches warehouse stock levels from the 1C RESTs API.
type HTTPRestsSource struct {
	httpClient *http.Client
}

// NewHTTPRestsSource creates a source with a 15-minute timeout for large payloads.
func NewHTTPRestsSource() *HTTPRestsSource {
	return &HTTPRestsSource{
		httpClient: &http.Client{
			Timeout: restsHTTPTimeout,
		},
	}
}

// FetchRests streams from the 1C RESTs API, applies storage filter during parse,
// and writes rows to the writer in batches of 500.
// Returns (goodsCount, totalRowsSaved, filteredOut, error).
func (s *HTTPRestsSource) FetchRests(ctx context.Context, apiURL string, filter RestsStorageFilter,
	writer RestsWriter, snapshotDate string) (int, int, int, error) {

	body, err := s.fetchBody(ctx, apiURL)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("fetch rests: %w", err)
	}
	defer body.Close()

	br := bufio.NewReaderSize(body, 4096)
	peek, _ := br.Peek(256)
	dec := json.NewDecoder(br)

	if err := expectArrayDelim(dec, peek); err != nil {
		return 0, 0, 0, fmt.Errorf("rests API: %w", err)
	}

	batch := make([]RestsRow, 0, 500)
	totalGoods := 0
	totalSaved := 0
	filteredOut := 0

	for dec.More() {
		var item apiRestItem
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

			for rawGUID, storageRaw := range skuMap {
				skuGUID := normalizeRestsGUID(rawGUID)

				var storages []apiStorageRow
				if err := json.Unmarshal(storageRaw, &storages); err != nil {
					continue
				}

				for _, st := range storages {
					if !filter.Matches(st.StorageGUID, st.StorageName) {
						filteredOut++
						continue
					}

					batch = append(batch, RestsRow{
						GoodGUID:    item.GoodGUID,
						SKUGUID:     skuGUID,
						StorageGUID: st.StorageGUID,
						StorageName: st.StorageName,
						Stock:       st.Stock,
						Reserv:      st.Reserv,
						Free:        st.Free,
						FirstStage:  st.FirstStage,
					})
				}
			}
		}

		// Flush batch every 500 rows
		if len(batch) >= 500 {
			n, err := writer.SaveRests(ctx, batch, snapshotDate)
			if err != nil {
				return totalGoods, totalSaved, filteredOut, err
			}
			totalSaved += n
			batch = batch[:0]
		}
	}

	// Flush remaining rows
	if len(batch) > 0 {
		n, err := writer.SaveRests(ctx, batch, snapshotDate)
		if err != nil {
			return totalGoods, totalSaved, filteredOut, err
		}
		totalSaved += n
	}

	return totalGoods, totalSaved, filteredOut, nil
}

// ---------------------------------------------------------------------------
// HTTP helpers (specific to rests — avoids modifying existing HTTPSource)
// ---------------------------------------------------------------------------

// fetchBody makes a GET request and returns the response body for streaming.
// Supports basic auth embedded in URL (user:pass@host).
func (s *HTTPRestsSource) fetchBody(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}

	// Extract basic auth from URL if present
	if u.User != nil {
		password, _ := u.User.Password()
		req.SetBasicAuth(u.User.Username(), password)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, resp.Status)
	}

	return resp.Body, nil
}

// normalizeRestsGUID converts 1C RESTs API GUID format to standard UUID format.
// API returns keys like "_13209c78_1651_11e4_9401_2c768a56a25b",
// standard UUID is "13209c78-1651-11e4-9401-2c768a56a25b".
func normalizeRestsGUID(guid string) string {
	if len(guid) > 0 && guid[0] == '_' {
		guid = guid[1:]
	}
	return strings.ReplaceAll(guid, "_", "-")
}
