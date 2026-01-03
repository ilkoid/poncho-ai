// Package std предоставляет стандартные инструменты для Poncho AI.
package std

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// === S3 Batch Tools ===
// TODO: Реализовать инструменты для пакетной обработки файлов из S3

// ClassifyAndDownloadS3FilesTool — заглушка для классификации и скачивания файлов.
type ClassifyAndDownloadS3FilesTool struct {
	s3Client      *s3storage.Client
	imageCfg      config.ImageProcConfig
	fileRules     []config.FileRule
	toolCfg       config.ToolConfig
}

// NewClassifyAndDownloadS3Files создает заглушку для инструмента классификации файлов.
func NewClassifyAndDownloadS3Files(
	s3Client *s3storage.Client,
	_ interface{}, // state - не используется в stub
	imageCfg config.ImageProcConfig,
	fileRules []config.FileRule,
	toolCfg config.ToolConfig,
) *ClassifyAndDownloadS3FilesTool {
	return &ClassifyAndDownloadS3FilesTool{
		s3Client:  s3Client,
		imageCfg:  imageCfg,
		fileRules: fileRules,
		toolCfg:   toolCfg,
	}
}

func (t *ClassifyAndDownloadS3FilesTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "classify_and_download_s3_files",
		Description: "Загружает все файлы артикула из S3 хранилища, классифицирует их по тегам (sketch, plm_data, marketing) и сохраняет в состояние. Выполняет ресайз изображений если включено в конфиге. Используй это для загрузки артикула перед анализом изображений. [STUB - НЕ РЕАЛИЗОВАНО]",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"article_id": map[string]interface{}{
					"type":        "string",
					"description": "Артикул товара для загрузки файлов",
				},
			},
			"required": []string{"article_id"},
		},
	}
}

func (t *ClassifyAndDownloadS3FilesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		ArticleID string `json:"article_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// STUB: Возврат заглушки
	stub := map[string]interface{}{
		"error":      "not_implemented",
		"message":    "classify_and_download_s3_files tool is not implemented yet",
		"article_id": args.ArticleID,
		"files": map[string]interface{}{
			"sketch":     []string{},
			"plm_data":   []string{},
			"marketing":  []string{},
		},
	}
	result, _ := json.Marshal(stub)
	return string(result), nil
}

// AnalyzeArticleImagesBatchTool — заглушка для анализа изображений.
type AnalyzeArticleImagesBatchTool struct {
	state      interface{} // GlobalState - не используется в stub
	s3Client   *s3storage.Client
	visionLLM  interface{} // LLM Provider - не используется в stub
	visionPrompt string   // Vision system prompt - не используется в stub
	imageCfg   config.ImageProcConfig
	toolCfg    config.ToolConfig
}

// NewAnalyzeArticleImagesBatch создает заглушку для инструмента анализа изображений.
func NewAnalyzeArticleImagesBatch(
	state interface{},
	s3Client *s3storage.Client,
	visionLLM interface{},
	visionPrompt string,
	imageCfg config.ImageProcConfig,
	toolCfg config.ToolConfig,
) *AnalyzeArticleImagesBatchTool {
	return &AnalyzeArticleImagesBatchTool{
		state:        state,
		s3Client:     s3Client,
		visionLLM:    visionLLM,
		visionPrompt: visionPrompt,
		imageCfg:     imageCfg,
		toolCfg:      toolCfg,
	}
}

func (t *AnalyzeArticleImagesBatchTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "analyze_article_images_batch",
		Description: "Анализирует изображения из текущего артикула с помощью Vision LLM. Обрабатывает картинки параллельно в горутинах. Опционально фильтрует по тегу (sketch, plm_data, marketing). Используй это после classify_and_download_s3_files для анализа эскизов. [STUB - НЕ РЕАЛИЗОВАНО]",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tag": map[string]interface{}{
					"type":        "string",
					"description": "Фильтр по тегу (sketch, plm_data, marketing). Если не указан - анализирует все изображения.",
					"enum":        []string{"sketch", "plm_data", "marketing", ""},
				},
			},
			"required": []string{},
		},
	}
}

func (t *AnalyzeArticleImagesBatchTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// STUB: Возврат заглушки
	stub := map[string]interface{}{
		"error":   "not_implemented",
		"message": "analyze_article_images_batch tool is not implemented yet",
		"tag":     args.Tag,
		"results": []map[string]interface{}{
			{
				"file":     "example.jpg",
				"analysis": "STUB: vision analysis not implemented",
			},
		},
	}
	result, _ := json.Marshal(stub)
	return string(result), nil
}
