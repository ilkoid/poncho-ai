//go:build short

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
	// TODO: Create minio client with provided config
	// TODO: Set up credentials and security options
	// TODO: Return initialized client
	return nil, nil
}

// ListFiles возвращает ВСЕ файлы по префиксу (артикулу)
func (c *Client) ListFiles(ctx context.Context, prefix string) ([]StoredObject, error) {
	// TODO: Normalize prefix (add slash if it's a "folder")
	// TODO: Create list objects options
	// TODO: Iterate through objects and collect them
	// TODO: Skip the "folder" object itself
	// TODO: Return error if no objects found
	// TODO: Return list of stored objects
	return nil, nil
}

// DownloadFile скачивает объект целиком в память
func (c *Client) DownloadFile(ctx context.Context, key string) ([]byte, error) {
	// TODO: Get object from S3
	// TODO: Ensure object is closed after reading
	// TODO: Read object content into buffer
	// TODO: Return bytes or error
	return nil, nil
}