// photos.go — скачивание миниатюр WB и конвертация WebP → JPEG для встраивания в xlsx.
//
// Копия паттерна из cmd/data-analyzers/check-card-consistency/main.go (downloadThumbnails),
// адаптированная под int64 nmID. Загружает card_photos.tm (WebP), даунскейлит до 100×75 и
// ре-кодирует в JPEG quality 60 → ~2-4 КБ на миниатюру. При 2.6k карточек это ~6-12 МБ к xlsx.
package main

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"golang.org/x/image/draw"
)

// downloadThumbnails скачивает миниатюры по urlMap (nmID → tm-URL), конвертирует WebP → JPEG,
// растягивает до targetW×targetH без сохранения пропорций. Параллельно (maxWorkers горутин).
//
// Ошибки (404, таймаут, битый WebP) логируются как WARN и пропускаются — отсутствие
// отдельного фото не должно валить весь отчёт. Возвращает map[nmID]jpeg-bytes.
func downloadThumbnails(ctx context.Context, urlMap map[int64]string) map[int64][]byte {
	const targetW, targetH = 100, 75
	const maxWorkers = 20
	const jpegQuality = 60

	type photoResult struct {
		nmID int64
		data []byte
	}

	result := make(map[int64][]byte, len(urlMap))
	client := &http.Client{Timeout: 10 * time.Second}

	sem := make(chan struct{}, maxWorkers)
	ch := make(chan photoResult, len(urlMap))
	var wg sync.WaitGroup

	for nmID, url := range urlMap {
		if url == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(nmID int64, url string) {
			defer wg.Done()
			defer func() { <-sem }()

			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				log.Printf("WARN: create request nm_id=%d: %v", nmID, err)
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				log.Printf("WARN: download photo nm_id=%d: %v", nmID, err)
				return
			}
			if resp.StatusCode != 200 {
				resp.Body.Close()
				log.Printf("WARN: photo nm_id=%d returned status %d", nmID, resp.StatusCode)
				return
			}
			data, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				log.Printf("WARN: read photo nm_id=%d: %v", nmID, err)
				return
			}

			img, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				log.Printf("WARN: decode image nm_id=%d: %v", nmID, err)
				return
			}

			dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
			draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)

			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
				log.Printf("WARN: encode jpeg nm_id=%d: %v", nmID, err)
				return
			}
			ch <- photoResult{nmID: nmID, data: buf.Bytes()}
		}(nmID, url)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	for pr := range ch {
		result[pr.nmID] = pr.data
	}
	return result
}
