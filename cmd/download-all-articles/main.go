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
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
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

	fmt.Println("🚀 S3 Article Downloader")
	fmt.Println("========================")
	fmt.Printf("Endpoint: %s\n", s3Endpoint)
	fmt.Printf("Bucket:   %s\n", s3Bucket)
	fmt.Println()

	// 1. Получаем credentials из переменных окружения
	accessKey := os.Getenv("S3_ACCESS_KEY")
	secretKey := os.Getenv("S3_SECRET_KEY")

	if accessKey == "" || secretKey == "" {
		fmt.Println("❌ Ошибка: требуются переменные окружения S3_ACCESS_KEY и S3_SECRET_KEY")
		os.Exit(1)
	}

	// 2. Создаём S3 клиент
	client, err := s3storage.New(config.S3Config{
		Endpoint:  s3Endpoint,
		Bucket:    s3Bucket,
		Region:    s3Region,
		AccessKey: accessKey,
		SecretKey: secretKey,
		UseSSL:    s3UseSSL,
	})
	if err != nil {
		fmt.Printf("❌ Ошибка создания S3 клиента: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✅ S3 клиент создан")

	// 3. Получаем список всех папок артикулов
	fmt.Println("📂 Получение списка артикулов...")
	folders, err := client.ListTopLevelFolders(ctx)
	if err != nil {
		fmt.Printf("❌ Ошибка получения списка папок: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Найдено артикулов: %d\n\n", len(folders))

	// 4. Создаём папку ЗАГРУЗКИ
	execPath, _ := os.Executable()
	execDir := filepath.Dir(execPath)
	downloadDir := filepath.Join(execDir, "ЗАГРУЗКИ")

	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		fmt.Printf("❌ Ошибка создания папки ЗАГРУЗКИ: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("📁 Папка для скачивания: %s\n\n", downloadDir)

	// 5. Скачиваем каждый артикул
	startTime := time.Now()
	var totalFiles int
	var totalSize int64
	var errors []string

	for i, folder := range folders {
		articleID := strings.TrimSuffix(folder.Key, "/")
		fmt.Printf("[%d/%d] 📥 Скачиваю артикул %s...\n", i+1, len(folders), articleID)

		filesCount, size, err := downloadArticle(ctx, client, folder.Key, downloadDir)
		if err != nil {
			errMsg := fmt.Sprintf("Артикул %s: %v", articleID, err)
			errors = append(errors, errMsg)
			fmt.Printf("         ⚠️  Ошибка: %v\n", err)
			continue
		}

		totalFiles += filesCount
		totalSize += size
		fmt.Printf("         ✅ Скачано файлов: %d, размер: %s\n", filesCount, formatSize(size))
	}

	// 6. Выводим статистику
	elapsed := time.Since(startTime)
	fmt.Println()
	fmt.Println("================================")
	fmt.Println("📊 ИТОГИ")
	fmt.Println("================================")
	fmt.Printf("Артикулов обработано: %d\n", len(folders))
	fmt.Printf("Файлов скачано:       %d\n", totalFiles)
	fmt.Printf("Общий размер:         %s\n", formatSize(totalSize))
	fmt.Printf("Время выполнения:     %v\n", elapsed)

	if len(errors) > 0 {
		fmt.Printf("\n⚠️  Ошибки (%d):\n", len(errors))
		for _, e := range errors {
			fmt.Printf("   - %s\n", e)
		}
	}

	fmt.Println("\n✅ Готово!")
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
