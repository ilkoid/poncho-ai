//go:build short

/* инструменты для работы с S3 в пакете pkg/tools/std/
Нам понадобятся два инструмента:

list_s3_files: Аналог ls. Позволяет агенту "осмотреться" в бакете и найти нужные файлы (артикулы, документы).
read_s3_object: Аналог cat. Позволяет агенту прочитать содержимое файла (JSON текст или получить ссылку на картинку).
*/
package std

import (
	"bytes"
	"context"
	"encoding/base64" // Теперь используем!
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"path/filepath"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/config" // Нужен конфиг для параметров ресайза
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/tools"

	"github.com/nfnt/resize" // go get github.com/nfnt/resize
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
			// prefix не обязателен (тогда покажет корень)
		},
	}
}

func (t *S3ListTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// TODO: Parse JSON arguments for prefix (optional)
	// TODO: Call S3 client to list files with prefix
	// TODO: Create simplified response with just key and readable size
	// TODO: Return JSON string of simplified file list
	return "", nil
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
	// TODO: Parse JSON arguments for key
	// TODO: Check if file extension is binary/image and reject if so
	// TODO: Download file content from S3
	// TODO: Return content as string, truncating if too large
	return "", nil
}

// --- Helpers ---

func formatSize(bytes int64) string {
	// TODO: Format bytes into human-readable format (KB, MB, etc.)
	return ""
}

func isBinaryExt(ext string) bool {
	// TODO: Check if extension indicates binary/image file
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
		Name:        "read_s3_image_base64",
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

func (t *S3ReadImageTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// TODO: Parse JSON arguments for key
	// TODO: Check if file extension is valid image
	// TODO: Download image bytes from S3
	// TODO: If configured, decode and resize image according to config
	// TODO: Encode image as base64
	// TODO: Return as data URI format (data:image/jpeg;base64,...)
	return "", nil
}

func isImageExt(ext string) bool {
	// TODO: Check if extension indicates valid image format
	return false
}

// Регистрация в main.go: reg.Register(std.NewS3ReadImageTool(s3Client, cfg.ImageProcessing))