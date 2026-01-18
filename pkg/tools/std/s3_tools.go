/* инструменты для работы с S3 в пакете pkg/tools/std/
Нам понадобятся два инструмента:

list_s3_files: Аналог ls. Позволяет агенту "осмотреться" в бакете и найти нужные файлы (артикулы, документы).
read_s3_object: Аналог cat. Позволяет агенту прочитать содержимое файла (JSON текст или получить ссылку на картинку).
*/
package std

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// --- Tool: list_s3_files ---
// Позволяет агенту узнать, какие файлы есть по указанному пути (префиксу).

type S3ListTool struct {
	client *s3storage.Client
}

func NewS3ListTool(c *s3storage.Client) *S3ListTool {
	return &S3ListTool{client: c}
}

func (t *S3ListTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "list_s3_files",
		Description: "Возвращает список файлов в S3 хранилище по указанному пути (префиксу). Используй это, чтобы найти артикулы или проверить наличие файлов.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"prefix": map[string]interface{}{
					"type":        "string",
					"description": "Путь к папке (например '12345/' или пусто для корня).",
				},
			},
			"required": []string{}, // Prefix is optional, but field must be present for LLM API compatibility
		},
	}
}

func (t *S3ListTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Prefix string `json:"prefix"`
	}
	// Если аргументы пустые или кривые, пробуем продолжить с дефолтом
	if argsJSON != "" {
		_ = json.Unmarshal([]byte(argsJSON), &args)
	}

	// Вызываем наш S3 клиент
	files, err := t.client.ListFiles(ctx, args.Prefix)
	if err != nil {
		return "", fmt.Errorf("s3 list error: %w", err)
	}

	// Упрощаем ответ для LLM (экономим токены)
	// Отдаем только имена и размеры, без метаданных
	type simpleFile struct {
		Key  string `json:"key"`
		Size string `json:"size"` // "10.5 KB" читаемее для LLM, чем байты
	}

	simpleList := make([]simpleFile, 0, len(files))
	for _, f := range files {
		simpleList = append(simpleList, simpleFile{
			Key:  f.Key,
			Size: formatSize(f.Size),
		})
	}

	data, err := json.Marshal(simpleList)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// --- Tool: read_s3_object ---
// Позволяет прочитать содержимое файла.
// Если это текст/JSON — возвращает текст.
// Если это картинка — возвращает сообщение, что это бинарный файл (или base64, если попросят).
// Для агента безопаснее читать только текст, а картинки обрабатывать через Vision-инструменты.

type S3ReadTool struct {
	client *s3storage.Client
}

func NewS3ReadTool(c *s3storage.Client) *S3ReadTool {
	return &S3ReadTool{client: c}
}

func (t *S3ReadTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "read_s3_object",
		Description: "Читает содержимое файла из S3. Поддерживает текстовые файлы (JSON, TXT, MD). Не используй для картинок.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"key": map[string]interface{}{
					"type":        "string",
					"description": "Полный путь к файлу (ключ), полученный из list_s3_files.",
				},
			},
			"required": []string{"key"},
		},
	}
}

func (t *S3ReadTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Простая защита от дурака (чтобы не качать гигабайтные видео)
	ext := strings.ToLower(filepath.Ext(args.Key))
	if isBinaryExt(ext) {
		return "", fmt.Errorf("file type '%s' is binary/image. Use specialized vision tools for images", ext)
	}

	// Скачиваем
	contentBytes, err := t.client.DownloadFile(ctx, args.Key)
	if err != nil {
		return "", fmt.Errorf("s3 download error: %w", err)
	}

	// Возвращаем как строку (предполагаем UTF-8)
	// Для plm.json не обрезаем - сохраняем полный контент для последующей обработки
	if strings.Contains(strings.ToLower(args.Key), "plm.json") {
		return string(contentBytes), nil
	}

	// Для остальных файлов ограничиваем длину, чтобы не забить контекст LLM
	const maxTextSize = 3000 // Снижено с 20KB до 3KB для экономии токенов
	if len(contentBytes) > maxTextSize {
		// Для JSON файлов добавляем предупреждение
		truncated := string(contentBytes[:maxTextSize])
		warning := "\n\n...[TRUNCATED - File too large for context. Use specific queries for partial data.]"
		if ext == ".json" {
			warning = "\n\n...[TRUNCATED - JSON file too large. Request specific fields you need.]"
		}
		return truncated + warning, nil
	}

	return string(contentBytes), nil
}

// --- Helpers ---

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func isBinaryExt(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".zip", ".pdf", ".mp4":
		return true
	}
	return false
}

// --- Tool: read_s3_image ---
/*
реализуем инструмент read_image_base64 (или улучшим read_s3_object), который будет:

- Скачивать картинку.
- Ресайзить её согласно конфигу.
- Возвращать Base64 строку (готовую для отправки в Vision API).
- Для ресайза нам понадобится библиотека github.com/nfnt/resize или стандартная image.

Улучшенный s3_tools.go с поддержкой изображений: добавим новый инструмент S3ReadImageTool. Он будет специализированным.
*/

type S3ReadImageTool struct {
	client *s3storage.Client
	cfg    config.ImageProcConfig
}

func NewS3ReadImageTool(c *s3storage.Client, cfg config.ImageProcConfig) *S3ReadImageTool {
	return &S3ReadImageTool{
		client: c,
		cfg:    cfg,
	}
}

func (t *S3ReadImageTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "read_s3_image",
		Description: "Скачивает изображение из S3, оптимизирует его (resize) и возвращает в формате Base64. Используй это для Vision-анализа.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"key": map[string]interface{}{
					"type": "string",
				},
			},
			"required": []string{"key"},
		},
	}
}

// DownloadAndEncodeImage скачивает изображение из S3, ресайзит (если настроено)
// и возвращает Base64-encoded data URI для Vision API.
//
// Экспортированная helper-функция для переиспользования логики обработки изображений
// между разными tools (read_s3_image, analyze_article_images_batch).
//
// Pipeline:
//   1. Валидация расширения файла
//   2. Скачивание из S3
//   3. Ресайз (если cfg.MaxWidth > 0)
//   4. Base64 encoding в data URI формат
//
// Rule 11: context.Context propagation для возможности отмены.
func DownloadAndEncodeImage(
	ctx context.Context,
	client *s3storage.Client,
	key string,
	cfg config.ImageProcConfig,
) (string, error) {
	// 1. Проверяем расширение
	ext := strings.ToLower(filepath.Ext(key))
	if !isImageExt(ext) {
		return "", fmt.Errorf("file '%s' is not an image", key)
	}

	// 2. Скачиваем байты
	rawBytes, err := client.DownloadFile(ctx, key)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}

	// 3. Ресайзим (если включено в конфиге)
	if cfg.MaxWidth > 0 {
		rawBytes, err = utils.ResizeImage(rawBytes, cfg.MaxWidth, cfg.Quality)
		if err != nil {
			return "", fmt.Errorf("resize error: %w", err)
		}
	}

	// 4. Base64 encode
	b64 := base64.StdEncoding.EncodeToString(rawBytes)

	// Возвращаем как data URI (для вставки в Vision API)
	// Обычно провайдеры (OpenAI, GLM-4.6v) хотят data:image/jpeg;base64,...
	mimeType := "image/jpeg" // Мы конвертировали в jpeg при ресайзе
	if cfg.MaxWidth == 0 && ext == ".png" {
		mimeType = "image/png"
	}

	return fmt.Sprintf("data:%s;base64,%s", mimeType, b64), nil
}

func (t *S3ReadImageTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)

	// Делегируем обработку helper-функции
	return DownloadAndEncodeImage(ctx, t.client, args.Key, t.cfg)
}

func isImageExt(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	}
	return false
}

// Регистрация в main.go: reg.Register(std.NewS3ReadImageTool(s3Client, cfg.ImageProcessing))

// --- Tool: get_plm_data ---
// Загружает plm.json из S3, очищает от лишних полей и возвращает sanitized JSON.
// Использует utils.SanitizePLMJson для удаления:
//   - Ответственные, Эскизы (загружаются отдельно)
//   - НомерСтроки, ИдентификаторСтроки, Статус
//   - Миниатюра_Файл (огромный base64)
//   - Пустые значения и технические поля
type GetPLMDataTool struct {
	client *s3storage.Client
}

func NewGetPLMDataTool(c *s3storage.Client) *GetPLMDataTool {
	return &GetPLMDataTool{client: c}
}

func (t *GetPLMDataTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "get_plm_data",
		Description: "Загружает PLM данные (plm.json) артикула из S3 и возвращает очищенный JSON без технических полей, миниатюр и эскизов. Используй для получения информации о товаре: артикул, категория, сезон, цвета, материалы, размерный ряд.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"article_id": map[string]interface{}{
					"type":        "string",
					"description": "ID артикула (например, '12611516'). Если не указан, используется текущий артикул из контекста.",
				},
			},
		},
	}
}

func (t *GetPLMDataTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		ArticleID string `json:"article_id"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)

	// Если article_id не указан, возвращаем ошибку
	if args.ArticleID == "" {
		return "", fmt.Errorf("article_id is required. Specify the article ID (e.g., '12611516')")
	}

	// Формируем путь к plm.json
	key := fmt.Sprintf("%s/%s.json", args.ArticleID, args.ArticleID)

	// Скачиваем файл
	contentBytes, err := t.client.DownloadFile(ctx, key)
	if err != nil {
		return "", fmt.Errorf("failed to download plm.json: %w", err)
	}

	// Санитайзим JSON (удаляем лишние поля)
	sanitized, err := utils.SanitizePLMJson(string(contentBytes))
	if err != nil {
		return "", fmt.Errorf("failed to sanitize PLM JSON: %w", err)
	}

	return sanitized, nil
}

