// Package utils предоставляет утилиты для обработки изображений.
package utils

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // Регистрируем PNG декодер

	"github.com/nfnt/resize"
)

// ResizeImage ресайзит изображение до указанной ширины, сохраняя пропорции.
//
// Параметры:
//   - data: байты исходного изображения (JPEG, PNG)
//   - maxWidth: целевая ширина в пикселях. Если 0 или меньше исходной ширины — ресайз не применяется.
//   - quality: качество JPEG при кодировании (1-100). Рекомендуется 85.
//
// Возвращает байты JPEG изображения (для LLM и base64).
func ResizeImage(data []byte, maxWidth int, quality int) ([]byte, error) {
	// 1. Декодируем изображение
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	originalBounds := img.Bounds()
	originalWidth := originalBounds.Dx()

	// 2. Проверяем нужен ли ресайз
	if maxWidth <= 0 || originalWidth <= maxWidth {
		// Ресайз не нужен, но конвертируем в JPEG для консистентности
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, fmt.Errorf("encode to jpeg: %w", err)
		}
		return buf.Bytes(), nil
	}

	// 3. Вычисляем новую высоту сохраняя aspect ratio
	aspectRatio := float64(originalBounds.Dy()) / float64(originalWidth)
	newHeight := uint(float64(maxWidth) * aspectRatio)

	// 4. Ресайзим используя Lanczos3 (качественный алгоритм)
	resized := resize.Resize(uint(maxWidth), newHeight, img, resize.Lanczos3)

	// 5. Кодируем в JPEG
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("encode resized image: %w", err)
	}

	return buf.Bytes(), nil
}
