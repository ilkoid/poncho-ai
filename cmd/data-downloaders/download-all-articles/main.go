// CLI утилита для массового скачивания всех артикулов из S3.
// Одноразовая утилита - конфигурация захардкожена.
//
// Usage:
//
//	export S3_ACCESS_KEY="..."
//	export S3_SECRET_KEY="..."
//	go run main.go
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/dllog"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// S3 конфигурация (захардкожена для одноразовой утилиты)
const (
	s3Endpoint = "storage.yandexcloud.net"
	s3Bucket   = "plm-ai"
	s3Region   = "ru-central1"
	s3UseSSL   = true
)

func main() {
	ctx := context.Background()

	// 1. Print header
	dllog.PrintHeader("S3 Article Downloader",
		dllog.HeaderField{Key: "Endpoint", Value: s3Endpoint},
		dllog.HeaderField{Key: "Bucket", Value: s3Bucket},
	)

	// 2. Получаем credentials из переменных окружения
	accessKey := os.Getenv("S3_ACCESS_KEY")
	secretKey := os.Getenv("S3_SECRET_KEY")

	if accessKey == "" || secretKey == "" {
		dllog.Error("Требуются переменные окружения S3_ACCESS_KEY и S3_SECRET_KEY")
		os.Exit(1)
	}

	dllog.Log("Access Key: %s", utils.MaskAPIKey(accessKey))

	// 3. Создаём S3 клиент
	client, err := s3storage.New(config.S3Config{
		Endpoint:  s3Endpoint,
		Bucket:    s3Bucket,
		Region:    s3Region,
		AccessKey: accessKey,
		SecretKey: secretKey,
		UseSSL:    s3UseSSL,
	})
	if err != nil {
		dllog.Error("Ошибка создания S3 клиента: %v", err)
		os.Exit(1)
	}

	dllog.Log("S3 клиент создан")

	// 4. Получаем список всех папок артикулов
	dllog.Log("Получение списка артикулов...")
	folders, err := client.ListTopLevelFolders(ctx)
	if err != nil {
		dllog.Error("Ошибка получения списка папок: %v", err)
		os.Exit(1)
	}

	dllog.Log("Найдено артикулов: %d", len(folders))

	// 5. Создаём папку ЗАГРУЗКИ
	execPath, _ := os.Executable()
	execDir := filepath.Dir(execPath)
	downloadDir := filepath.Join(execDir, "ЗАГРУЗКИ")

	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		dllog.Error("Ошибка создания папки ЗАГРУЗКИ: %v", err)
		os.Exit(1)
	}

	dllog.Log("Папка для скачивания: %s", downloadDir)

	// 6. Скачиваем каждый артикул
	startTime := time.Now()
	var totalFiles int
	var totalSize int64
	var errors []string

	for i, folder := range folders {
		articleID := strings.TrimSuffix(folder.Key, "/")

		filesCount, size, err := downloadArticle(ctx, client, folder.Key, downloadDir)
		if err != nil {
			errMsg := fmt.Sprintf("Артикул %s: %v", articleID, err)
			errors = append(errors, errMsg)
			dllog.Error("Артикул %s: %v", articleID, err)
			continue
		}

		totalFiles += filesCount
		totalSize += size
		dllog.Progress(i+1, len(folders), articleID, fmt.Sprintf("%d files, %s", filesCount, formatSize(size)), startTime)
	}

	// 7. Summary
	dllog.Done(time.Since(startTime), "%d articles, %d files, %s", len(folders), totalFiles, formatSize(totalSize))

	if len(errors) > 0 {
		dllog.Error("Ошибки (%d):", len(errors))
		for _, e := range errors {
			dllog.Log("  - %s", e)
		}
	}
}

// downloadArticle скачивает все файлы из папки артикула
func downloadArticle(ctx context.Context, client *s3storage.Client, prefix, downloadDir string) (filesCount int, totalSize int64, err error) {
	// Получаем список файлов в папке
	files, err := client.ListFiles(ctx, prefix)
	if err != nil {
		return 0, 0, fmt.Errorf("ошибка листинга: %w", err)
	}

	// Создаём локальную папку для артикула
	articleID := strings.TrimSuffix(prefix, "/")
	articleDir := filepath.Join(downloadDir, articleID)

	if err := os.MkdirAll(articleDir, 0755); err != nil {
		return 0, 0, fmt.Errorf("ошибка создания папки: %w", err)
	}

	// Скачиваем каждый файл (с перезаписью)
	for _, file := range files {
		// Пропускаем подпапки
		if strings.HasSuffix(file.Key, "/") {
			continue
		}

		// Формируем локальный путь
		filename := filepath.Base(file.Key)
		localPath := filepath.Join(articleDir, filename)

		// Скачиваем файл (перезаписываем если существует)
		if err := client.DownloadToFile(ctx, file.Key, localPath); err != nil {
			return filesCount, totalSize, fmt.Errorf("ошибка скачивания %s: %w", file.Key, err)
		}

		filesCount++
		totalSize += file.Size
	}

	return filesCount, totalSize, nil
}

// formatSize форматирует размер в человекочитаемый вид
func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
