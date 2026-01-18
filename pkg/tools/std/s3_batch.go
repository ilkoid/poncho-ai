// Package std предоставляет стандартные инструменты для Poncho AI.
package std

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/classifier"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// === S3 Batch Tools ===

// ClassifyAndDownloadS3FilesTool — инструмент для классификации файлов артикула в S3.
//
// Списывает файлы из папки артикула в S3, классифицирует их по тегам (sketch, plm_data, marketing)
// используя glob-паттерны. Хранит только метаданные в state, контент не скачивает.
//
// Content-on-demand паттерн:
// - Изображения: vision анализ через read_s3_image (скачает + проанализирует)
// - JSON файлы: чтение через read_s3_object (скачает content)
//
// Это предотвращает разрастание контекста LLM (BuildAgentContext инжектит
// VisionDescription в каждый запрос).
//
// Rule 1: Реализует Tool interface ("Raw In, String Out")
// Rule 5: Thread-safe через CoreState
// Rule 7: Возвращает ошибки вместо panic
// Rule 11: Распространяет context.Context во все S3 вызовы
type ClassifyAndDownloadS3FilesTool struct {
	s3Client  *s3storage.Client
	state     *state.CoreState    // Rule 5: Thread-safe state management
	imageCfg  config.ImageProcConfig
	fileRules []config.FileRule
	toolCfg   config.ToolConfig
}

// NewClassifyAndDownloadS3Files создает инструмент для пакетной загрузки и классификации файлов.
//
// Rule 3: Регистрация через Registry.Register()
// Rule 5: Thread-safe через CoreState
func NewClassifyAndDownloadS3Files(
	s3Client *s3storage.Client,
	state *state.CoreState,
	imageCfg config.ImageProcConfig,
	fileRules []config.FileRule,
	toolCfg config.ToolConfig,
) *ClassifyAndDownloadS3FilesTool {
	return &ClassifyAndDownloadS3FilesTool{
		s3Client:  s3Client,
		state:     state,
		imageCfg:  imageCfg,
		fileRules: fileRules,
		toolCfg:   toolCfg,
	}
}

// Definition возвращает определение инструмента для function calling.
//
// Rule 1: Tool interface - Definition() не изменяется
func (t *ClassifyAndDownloadS3FilesTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "classify_and_download_s3_files",
		Description: "Списывает и классифицирует файлы артикула из S3 по тегам (sketch, plm_data, marketing). Хранит только метаданные. Для контента используй read_s3_image (images) или read_s3_object (JSON).",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"article_id": map[string]interface{}{
					"type":        "string",
					"description": "Артикул товара для загрузки файлов (например, '12345')",
				},
			},
			"required": []string{"article_id"},
		},
	}
}

// Execute выполняет загрузку и классификацию файлов артикула из S3.
//
// Rule 1: "Raw In, String Out" - получаем JSON строку, возвращаем строку
// Rule 7: Возвращаем ошибки вместо panic
// Rule 11: Распространяем context.Context во все S3 вызовы
func (t *ClassifyAndDownloadS3FilesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// 1. Парсим аргументы
	var args struct {
		ArticleID string `json:"article_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// 2. Валидация
	if t.s3Client == nil {
		return "", fmt.Errorf("S3 client not initialized")
	}
	if args.ArticleID == "" {
		return "", fmt.Errorf("article_id is required")
	}

	// 3. Получаем список файлов из S3
	prefix := args.ArticleID + "/"
	objects, err := t.s3Client.ListFiles(ctx, prefix)
	if err != nil {
		return "", fmt.Errorf("failed to list files for article '%s': %w", args.ArticleID, err)
	}

	if len(objects) == 0 {
		return fmt.Sprintf(`{"article_id":"%s","status":"warning","message":"No files found in S3 for article %s"}`, args.ArticleID, args.ArticleID), nil
	}

	// 4. Классифицируем файлы используя classifier engine
	engine := classifier.New(t.fileRules)
	classifiedFiles, err := engine.Process(objects)
	if err != nil {
		return "", fmt.Errorf("classification failed: %w", err)
	}

	// 5. Загружаем и обрабатываем файлы
	processedCount := 0
	for _, files := range classifiedFiles {
		for i, file := range files {
			// Определяем тип файла (метаданные, без скачивания контента)
			if isImageFile(file.Filename) {
				files[i].Type = "image"
				// VisionDescription заполнится позже через read_s3_image
				processedCount++
			} else if isJSONFile(file.Filename) {
				// JSON файлы: только метаданные, контент не храним
				// иначе BuildAgentContext инжектит весь JSON в каждый запрос
				files[i].Type = "json"
				files[i].VisionDescription = fmt.Sprintf("JSON available at %s (use read_s3_object to read)", file.Key)
				processedCount++
			}
		}
	}

	// 6. Сохраняем в CoreState
	if err := t.state.SetCurrentArticle(args.ArticleID, classifiedFiles); err != nil {
		return "", fmt.Errorf("failed to store article in state: %w", err)
	}

	// 7. Формируем ответ для LLM
	summary := map[string]interface{}{
		"article_id":  args.ArticleID,
		"status":      "success",
		"message":     fmt.Sprintf("Found %d files for article %s (metadata only, content on-demand)", processedCount, args.ArticleID),
		"files":       buildTagSummary(classifiedFiles),
		"total_count": countTotalFiles(classifiedFiles),
		"next_steps": []string{
			"Use read_s3_image for vision analysis of specific images",
			"Use read_s3_object to read JSON files with PLM metadata",
		},
	}

	result, err := json.Marshal(summary)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}

	return string(result), nil
}

// AnalyzeArticleImagesBatchTool — инструмент для последовательного анализа изображений.
//
// Загружает изображения артикула из CoreState (после classify_and_download_s3_files),
// фильтрует по тегу и анализирует с помощью Vision LLM последовательно (одна за одной).
//
// ПАРАМЕТРЫ:
//   - max_images (int, опционально): Максимум изображений для анализа (default: 3, range: 1-10)
//   - tag (string, опционально): Фильтр по тегу (sketch, plm_data, marketing, "")
//
// SEQ-PROCESSING PATTER (избегает переполнения контекста):
//   Вместо параллельных вызовов read_s3_image (которые накапливают base64 в контексте),
//   этот tool делает последовательные вызовы Vision LLM и агрегирует результаты.
//
// ПРЯМОЙ ВЫЗОВ LLM (документированное исключение из dev_manifest.md):
//   Tool напрямую вызывает llm.Provider.Generate() для vision-анализа.
//   Это необходимо для последовательной обработки с агрегацией результатов.
//
// Rule 1: "Raw In, String Out" - Tool interface
// Rule 5: Thread-safe через CoreState (только чтение)
// Rule 11: context.Context propagation
type AnalyzeArticleImagesBatchTool struct {
	state        *state.CoreState  // Rule 5: Thread-safe state management
	s3Client     *s3storage.Client
	visionLLM    llm.Provider       // Vision LLM Provider (прямой вызов - документированное исключение)
	visionPrompt string            // System prompt для vision-анализа
	imageCfg     config.ImageProcConfig
	toolCfg      config.ToolConfig
}

// NewAnalyzeArticleImagesBatch создаёт инструмент для пакетного анализа изображений.
//
// Rule 3: Регистрация через Registry.Register()
// Rule 5: Thread-safe через CoreState
func NewAnalyzeArticleImagesBatch(
	state *state.CoreState,
	s3Client *s3storage.Client,
	visionLLM llm.Provider,
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

// Definition возвращает определение инструмента для function calling.
//
// Rule 1: Tool interface - Definition() не изменяется
func (t *AnalyzeArticleImagesBatchTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "analyze_article_images_batch",
		Description: "Анализирует изображения текущего артикула с помощью Vision LLM. Последовательно обрабатывает изображения (одна за одной) чтобы избежать переполнения контекста. Опционально фильтрует по тегу. Используй это после classify_and_download_s3_files для анализа эскизов.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"max_images": map[string]interface{}{
					"type":        "integer",
					"description": "Максимум изображений для анализа (default: 3, range: 1-10)",
					"default":     3,
					"minimum":     1,
					"maximum":     10,
				},
				"tag": map[string]interface{}{
					"type":        "string",
					"description": "Фильтр по тегу (sketch, plm_data, marketing). Пусто для всех изображений.",
					"enum":        []string{"sketch", "plm_data", "marketing", ""},
				},
			},
			"required": []string{},
		},
	}
}

// Execute выполняет пакетный анализ изображений артикула.
//
// Rule 1: "Raw In, String Out" - получаем JSON строку, возвращаем строку
// Rule 7: Возвращаем ошибки вместо panic
// Rule 11: Распространяем context.Context во все вызовы
func (t *AnalyzeArticleImagesBatchTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// 1. Парсим аргументы
	var args struct {
		MaxImages int    `json:"max_images"`
		Tag       string `json:"tag"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// 2. Валидация
	if t.visionLLM == nil {
		return "", fmt.Errorf("vision LLM not configured")
	}
	if t.s3Client == nil {
		return "", fmt.Errorf("S3 client not initialized")
	}

	// 3. Применяем дефолты и ограничения
	maxImages := args.MaxImages
	if maxImages <= 0 {
		maxImages = 3 // default
	}
	if maxImages > 10 {
		maxImages = 10 // hard limit
	}

	// 4. Получаем текущий артикул и файлы из CoreState
	articleID, files := t.state.GetCurrentArticle()
	if articleID == "NONE" || len(files) == 0 {
		return "", fmt.Errorf("no article loaded. Call classify_and_download_s3_files first")
	}

	// 6. Фильтруем по тегу и типу (только изображения)
	var imagesToAnalyze []*s3storage.FileMeta
	if args.Tag != "" {
		// Фильтр по тегу
		if tagFiles, exists := files[args.Tag]; exists {
			for _, f := range tagFiles {
				if isImageFile(f.Filename) {
					imagesToAnalyze = append(imagesToAnalyze, f)
				}
			}
		}
	} else {
		// Все изображения из всех тегов
		for _, tagFiles := range files {
			for _, f := range tagFiles {
				if isImageFile(f.Filename) {
					imagesToAnalyze = append(imagesToAnalyze, f)
				}
			}
		}
	}

	if len(imagesToAnalyze) == 0 {
		return fmt.Sprintf(`{"article_id":"%s","status":"warning","message":"No images found for tag '%s'"}`,
			articleID, args.Tag), nil
	}

	// 7. Ограничиваем количество
	if len(imagesToAnalyze) > maxImages {
		imagesToAnalyze = imagesToAnalyze[:maxImages]
	}

	// 8. Последовательно анализируем изображения
	type imageResult struct {
		Filename string
		Key      string
		Analysis string
		Error    string
	}

	results := make([]imageResult, 0, len(imagesToAnalyze))

	for _, img := range imagesToAnalyze {
		// 8a. Скачиваем и кодируем изображение (переиспользуем helper из s3_tools.go)
		dataURI, err := DownloadAndEncodeImage(ctx, t.s3Client, img.Key, t.imageCfg)
		if err != nil {
			results = append(results, imageResult{
				Filename: img.Filename,
				Key:      img.Key,
				Error:    fmt.Sprintf("download failed: %v", err),
			})
			continue
		}

		// 8b. Формируем сообщение для Vision LLM
		messages := []llm.Message{
			{
				Role:    llm.RoleSystem,
				Content: t.visionPrompt,
			},
			{
				Role:    llm.RoleUser,
				Content: fmt.Sprintf("Проанализируй это изображение: %s", img.Filename),
				Images:  []string{dataURI},
			},
		}

		// 8c. Вызываем Vision LLM (ПРЯМОЙ ВЫЗОВ - документированное исключение)
		response, err := t.visionLLM.Generate(ctx, messages)
		if err != nil {
			results = append(results, imageResult{
				Filename: img.Filename,
				Key:      img.Key,
				Error:    fmt.Sprintf("vision analysis failed: %v", err),
			})
			continue
		}

		results = append(results, imageResult{
			Filename: img.Filename,
			Key:      img.Key,
			Analysis: response.Content,
		})
	}

	// 9. Формируем ответ для LLM
	successCount := 0
	errorCount := 0
	for _, r := range results {
		if r.Error != "" {
			errorCount++
		} else {
			successCount++
		}
	}

	summary := map[string]interface{}{
		"article_id":  articleID,
		"status":      "completed",
		"tag":         args.Tag,
		"total":       len(imagesToAnalyze),
		"successful":  successCount,
		"failed":      errorCount,
		"results":     results,
		"message":     fmt.Sprintf("Analyzed %d images (success: %d, failed: %d)", len(imagesToAnalyze), successCount, errorCount),
	}

	result, err := json.Marshal(summary)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}

	return string(result), nil
}

// === Helper Functions ===

// isImageFile проверяет, является ли файл изображением по расширению.
func isImageFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	}
	return false
}

// isJSONFile проверяет, является ли файл JSON по расширению.
func isJSONFile(filename string) bool {
	return strings.ToLower(filepath.Ext(filename)) == ".json"
}

// buildTagSummary строит сводку по тегам для ответа LLM.
func buildTagSummary(files map[string][]*s3storage.FileMeta) map[string]interface{} {
	result := make(map[string]interface{})
	for tag, fileList := range files {
		filenames := make([]string, len(fileList))
		for i, f := range fileList {
			filenames[i] = f.Filename
		}
		result[tag] = map[string]interface{}{
			"count": len(fileList),
			"files": filenames,
		}
	}
	return result
}

// countTotalFiles подсчитывает общее количество файлов во всех тегах.
func countTotalFiles(files map[string][]*s3storage.FileMeta) int {
	total := 0
	for _, fileList := range files {
		total += len(fileList)
	}
	return total
}
