// "Тупой" клиент. классификатор файлов будет отдельно

package s3storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	_ "path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

type Client struct {
    api    *minio.Client
    bucket string
}

// StoredObject - сырой объект из S3
type StoredObject struct {
	Key          string
	Size         int64
	LastModified time.Time
}

type FileMeta struct {
    Key  string
    Size int64
    Type string
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
