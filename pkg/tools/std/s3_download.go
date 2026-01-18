// Package std предоставляет инструменты для скачивания файлов из S3 на локальный диск.
//
// Tool: download_s3_files
// - Скачивает файлы/папки из S3 в локальную папку ЗАГРУЗКИ
// - Защита от дурака: нельзя скачать весь бакет
// - Rule 11: context.Context propagation
// - Rule 12: Input validation (защита от path traversal)
package std

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// DownloadS3FilesTool скачивает файлы из S3 на локальный диск.
//
// Правила валидации:
// - key не может быть пустым или "/"
// - key должен быть конкретным файлом или папкой (с / в конце)
// - Максимальная глубина скачивания: 1 папка
//
// Локальная структура:
// - Файлы сохраняются в папку ЗАГРУЗКИ рядом с исполняемым файлом
// - Сохраняется исходная структура файлов
type DownloadS3FilesTool struct {
	client *s3storage.Client
}

// NewDownloadS3FilesTool создаёт новый tool для скачивания файлов.
func NewDownloadS3FilesTool(c *s3storage.Client) *DownloadS3FilesTool {
	return &DownloadS3FilesTool{client: c}
}

// Definition возвращает описание tool для LLM (Rule 1).
func (t *DownloadS3FilesTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "download_s3_files",
		Description: "Скачивает файлы или папку из S3 на локальный диск в папку ЗАГРУЗКИ. " +
			"Можно скачать только конкретный файл или содержимое одной папки. " +
			"Нельзя скачать весь бакет (защита от случайной выгрузки).",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"key": map[string]interface{}{
					"type":        "string",
					"description": "Путь к файлу или папке в S3 (например, '12345/plm.json' или '12345/'). Для папки укажите путь с / в конце.",
				},
			},
			"required": []string{"key"},
		},
	}
}

// Execute скачивает файлы из S3 (Rule 1: Raw In, String Out).
//
// Rule 11: Respects context cancellation.
// Rule 12: Validates key to prevent accidental bucket download.
func (t *DownloadS3FilesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// === Rule 12: Валидация входных данных (защита от дурака) ===

	// 1. Проверяем что key не пустой
	if args.Key == "" {
		return "", fmt.Errorf("key is required: specify a file or folder path (e.g., '12345/plm.json' or '12345/')")
	}

	// 2. Проверяем что key не является корнем бакета
	if args.Key == "/" || args.Key == "." || args.Key == "" {
		return "", fmt.Errorf("downloading entire bucket is not allowed for safety reasons. " +
			"Specify a specific file or folder (e.g., '12345/plm.json' or '12345/')")
	}

	// 3. Проверяем на path traversal (../../etc/passwd)
	cleanedKey := filepath.Clean(args.Key)
	if strings.Contains(cleanedKey, "..") {
		return "", fmt.Errorf("path traversal detected: key contains '..'. Use a valid S3 path")
	}

	// 4. Определяем тип пути: файл или папка
	isFolder := strings.HasSuffix(args.Key, "/")

	// Получаем путь к исполняемому файлу для создания папки ЗАГРУЗКИ
	execPath, err := os.Executable()
	if err != nil {
		// Fallback: используем текущую директорию
		execPath = "."
	}
	execDir := filepath.Dir(execPath)
	downloadDir := filepath.Join(execDir, "ЗАГРУЗКИ")

	// Создаём папку ЗАГРУЗКИ если не существует
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create download directory: %w", err)
	}

	var result downloadResult

	if isFolder {
		// === Скачивание папки ===
		result, err = t.downloadFolder(ctx, args.Key, downloadDir)
		if err != nil {
			return "", err
		}
	} else {
		// === Скачивание одного файла ===
		result, err = t.downloadSingleFile(ctx, args.Key, downloadDir)
		if err != nil {
			return "", err
		}
	}

	// Возвращаем JSON результат
	jsonResult, err := json.Marshal(result)
	if err != nil {
		return "", err
	}

	return string(jsonResult), nil
}

// downloadSingleFile скачивает один файл из S3.
func (t *DownloadS3FilesTool) downloadSingleFile(ctx context.Context, key, downloadDir string) (downloadResult, error) {
	// Извлекаем имя файла из пути
	filename := filepath.Base(key)
	localPath := filepath.Join(downloadDir, filename)

	// Rule 11: Передаём контекст для возможности отмены
	if err := t.client.DownloadToFile(ctx, key, localPath); err != nil {
		return downloadResult{}, fmt.Errorf("failed to download file '%s': %w", key, err)
	}

	// Получаем размер файла
	fileInfo, _ := os.Stat(localPath)

	return downloadResult{
		Success:     true,
		Type:        "file",
		SourcePath:  key,
		DestPath:    localPath,
		FilesCount:  1,
		TotalSize:   fileInfo.Size(),
		Description: fmt.Sprintf("File '%s' downloaded to '%s'", key, localPath),
	}, nil
}

// downloadFolder скачивает все файлы из папки S3.
func (t *DownloadS3FilesTool) downloadFolder(ctx context.Context, prefix, downloadDir string) (downloadResult, error) {
	// Rule 11: Передаём контекст для возможности отмены
	files, err := t.client.ListFiles(ctx, prefix)
	if err != nil {
		return downloadResult{}, fmt.Errorf("failed to list folder '%s': %w", prefix, err)
	}

	// Создаём подпапку с именем папки из S3
	folderName := strings.TrimSuffix(prefix, "/")
	if folderName == "" {
		folderName = "downloaded"
	}
	targetDir := filepath.Join(downloadDir, folderName)

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return downloadResult{}, fmt.Errorf("failed to create target directory: %w", err)
	}

	var totalSize int64
	downloadedFiles := []string{}

	// Скачиваем каждый файл
	for _, file := range files {
		// Пропускаем папки (ключи заканчивающиеся на /)
		if strings.HasSuffix(file.Key, "/") {
			continue
		}

		// Формируем локальный путь
		filename := filepath.Base(file.Key)
		localPath := filepath.Join(targetDir, filename)

		// Rule 11: Передаём контекст для возможности отмены
		if err := t.client.DownloadToFile(ctx, file.Key, localPath); err != nil {
			return downloadResult{}, fmt.Errorf("failed to download '%s': %w", file.Key, err)
		}

		totalSize += file.Size
		downloadedFiles = append(downloadedFiles, localPath)
	}

	return downloadResult{
		Success:     true,
		Type:        "folder",
		SourcePath:  prefix,
		DestPath:    targetDir,
		FilesCount:  len(downloadedFiles),
		TotalSize:   totalSize,
		Description: fmt.Sprintf("Downloaded %d files from '%s' to '%s'", len(downloadedFiles), prefix, targetDir),
		Files:       downloadedFiles,
	}, nil
}

// downloadResult представляет результат скачивания файлов.
type downloadResult struct {
	Success     bool     `json:"success"`
	Type        string   `json:"type"`        // "file" или "folder"
	SourcePath  string   `json:"source_path"` // Путь в S3
	DestPath    string   `json:"dest_path"`   // Локальный путь
	FilesCount  int      `json:"files_count"`
	TotalSize   int64    `json:"total_size"`
	Description string   `json:"description"`
	Files       []string `json:"files,omitempty"` // Список скачанных файлов (для папок)
}
