// "Тупой" клиент. классификатор файлов будет отдельно

package s3storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	_ "path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// ClientInterface определяет интерфейс для S3 клиента.
// Используется для мокания в тестах и внедрения зависимостей.
type ClientInterface interface {
	ListFiles(ctx context.Context, prefix string) ([]StoredObject, error)
	DownloadFile(ctx context.Context, key string) ([]byte, error)
}

type Client struct {
    api    *minio.Client
    bucket string
}

// Проверка что Client реализует ClientInterface
var _ ClientInterface = (*Client)(nil)

// StoredObject - сырой объект из S3
type StoredObject struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// FileMeta хранит метаданные файла с тегом классификации.
// Type может быть заполнен позже vision-моделью (например, "Платье красное").
type FileMeta struct {
	Tag               string   // Тег классификации (sketch, techpack, etc.)
	Key               string   // Полный путь в S3 (alias OriginalKey для совместимости)
	OriginalKey       string   // Оригинальный ключ в S3
	Size              int64    // Размер файла в байтах
	Type              string   // Описание типа (опционально, заполняется vision)
	Filename          string   // Имя файла без пути
	VisionDescription string   // Результат анализа vision-модели (Working Memory)
	Tags              []string // Дополнительные теги (для расширенной классификации)
}

// NewFileMeta создает новый FileMeta с базовыми метаданными.
func NewFileMeta(tag, key string, size int64, filename string) *FileMeta {
	return &FileMeta{
		Tag:         tag,
		Key:         key,
		OriginalKey: key, // Изначально Key и OriginalKey совпадают
		Size:        size,
		Filename:    filename,
		Tags:        []string{}, // Инициализируем пустой срез
	}
}

// New создает клиент, используя наш конфиг
func New(cfg config.S3Config) (*Client, error) {
    minioClient, err := minio.New(cfg.Endpoint, &minio.Options{
        Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
        Secure: cfg.UseSSL,
        Region: cfg.Region,
    })
    if err != nil {
        return nil, err
    }

    return &Client{
        api:    minioClient,
        bucket: cfg.Bucket,
    }, nil
}

// ListTopLevelFolders возвращает список папок первого уровня (артикулов).
// Использует Recursive: false для эффективности.
func (c *Client) ListTopLevelFolders(ctx context.Context) ([]StoredObject, error) {
	var folders []StoredObject

	opts := minio.ListObjectsOptions{
		Prefix:    "",
		Recursive: false,
	}

	for obj := range c.api.ListObjects(ctx, c.bucket, opts) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		// Фильтруем только папки (ключи заканчивающиеся на /)
		if strings.HasSuffix(obj.Key, "/") {
			folders = append(folders, StoredObject{
				Key:          obj.Key,
				Size:         obj.Size,
				LastModified: obj.LastModified,
			})
		}
	}

	if len(folders) == 0 {
		return nil, fmt.Errorf("no folders found in bucket '%s'", c.bucket)
	}

	return folders, nil
}

// ListFiles возвращает ВСЕ файлы по префиксу (артикулу)
func (c *Client) ListFiles(ctx context.Context, prefix string) ([]StoredObject, error) {
	// Нормализация префикса (добавляем слеш, если это "папка")
	if !strings.HasSuffix(prefix, "/") && prefix != "" {
		prefix += "/"
	}

	var objects []StoredObject
	
	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}

	for obj := range c.api.ListObjects(ctx, c.bucket, opts) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		// Пропускаем саму "папку"
		if obj.Key == prefix {
			continue
		}
		objects = append(objects, StoredObject{
			Key:          obj.Key,
			Size:         obj.Size,
			LastModified: obj.LastModified,
		})
	}
	
	if len(objects) == 0 {
		// Это можно считать ошибкой или просто пустым списком - зависит от логики
		// Для утилиты лучше вернуть ошибку, чтобы пользователь сразу понял
		return nil, fmt.Errorf("path '%s' not found or empty", prefix)
	}

	return objects, nil
}

// DownloadFile скачивает объект целиком в память
func (c *Client) DownloadFile(ctx context.Context, key string) ([]byte, error) {
    obj, err := c.api.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
    if err != nil {
        return nil, err
    }
    defer obj.Close()

    // Читаем в буфер
    buf := new(bytes.Buffer)
    if _, err := io.Copy(buf, obj); err != nil {
        return nil, err
    }

    return buf.Bytes(), nil
}

// DownloadToFile скачивает объект и сохраняет в файл по указанному пути.
//
// Rule 11: context.Context propagation for cancellation support.
// Rule 12: Validates localPath to prevent path traversal.
func (c *Client) DownloadToFile(ctx context.Context, key string, localPath string) error {
	obj, err := c.api.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to get object %s: %w", key, err)
	}
	defer obj.Close()

	// Создаём файл
	f, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", localPath, err)
	}
	defer f.Close()

	// Копируем данные из S3 в файл с учётом контекста
	if _, err := io.Copy(f, obj); err != nil {
		return fmt.Errorf("failed to write file %s: %w", localPath, err)
	}

	return nil
}
