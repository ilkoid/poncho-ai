// Package openai реализует адаптер LLM провайдера для OpenAI-совместимых API.
//
// Поддерживает Function Calling (tools) для интеграции с агент-системой.
// Соблюдает правило 4 манифеста: работает только через интерфейс llm.Provider.
package openai

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/utils"
	openai "github.com/sashabaranov/go-openai"
)

// Client реализует интерфейс llm.Provider для OpenAI-совместимых API.
//
// Поддерживает:
//   - Базовую генерацию текста
//   - Function Calling (tools)
//   - Vision запросы (изображения)
//   - Runtime переопределение параметров через GenerateOption
//
// Правило 4 ("Тупой клиент"): Не хранит состояние между вызовами.
// Все параметры передаются при каждом Generate() вызове.
type Client struct {
	api        *openai.Client
	baseConfig llm.GenerateOptions // Дефолтные параметры из config.yaml
}

// NewClient создает OpenAI клиент на основе конфигурации модели.
//
// Принимает ModelDef напрямую для упрощения создания клиентов через factory.
// Использует APIKey из конфигурации для аутентификации.
//
// Правило 2: Все настройки из конфигурации, никакого хардкода.
func NewClient(modelDef config.ModelDef) *Client {
	// Поддержка custom BaseURL для non-OpenAI провайдеров (Zai, DeepSeek и т.д.)
	cfg := openai.DefaultConfig(modelDef.APIKey)
	if modelDef.BaseURL != "" {
		cfg.BaseURL = modelDef.BaseURL
	}

	client := openai.NewClientWithConfig(cfg)

	// Заполняем baseConfig из ModelDef для использования как дефолты
	baseConfig := llm.GenerateOptions{
		Model:       modelDef.ModelName,
		Temperature: modelDef.Temperature,
		MaxTokens:   modelDef.MaxTokens,
		Format:      "",
	}

	return &Client{
		api:        client,
		baseConfig: baseConfig,
	}
}

// Generate выполняет запрос к API и возвращает ответ модели.
//
// Поддерживает:
//   - Function Calling (tools): []tools.ToolDefinition
//   - Runtime переопределение параметров: GenerateOption
//
// Алгоритм:
//   1. Разделяет opts на tools и GenerateOptions
//   2. Применяет GenerateOptions к baseConfig (runtime override)
//   3. Конвертирует сообщения в формат OpenAI SDK
//   4. Если переданы tools — добавляет их в запрос
//   5. Вызывает API с итоговыми параметрами
//   6. Конвертирует ответ обратно в наш формат
//
// Правило 7: Все ошибки возвращаются, никаких panic.
// Правило 4: Работает только через интерфейс llm.Provider.
func (c *Client) Generate(ctx context.Context, messages []llm.Message, opts ...any) (llm.Message, error) {
	startTime := time.Now()

	// 1. Начинаем с дефолтных значений из config.yaml
	options := c.baseConfig

	// 2. Разделяем opts на tools и GenerateOption
	var toolDefs []tools.ToolDefinition
	for _, opt := range opts {
		switch v := opt.(type) {
		case []tools.ToolDefinition:
			// Tools для Function Calling
			toolDefs = v
		case llm.GenerateOption:
			// Runtime переопределение параметров модели
			v(&options)
		default:
			return llm.Message{}, fmt.Errorf("invalid option type: expected []tools.ToolDefinition or llm.GenerateOption, got %T", opt)
		}
	}

	utils.Debug("LLM request started",
		"model", options.Model,
		"temperature", options.Temperature,
		"max_tokens", options.MaxTokens,
		"messages_count", len(messages),
		"tools_count", len(toolDefs))

	// Debug: логируем структуру сообщений
	for i, msg := range messages {
		imagesCount := len(msg.Images)
		contentPreview := msg.Content
		if len(contentPreview) > 100 {
			contentPreview = contentPreview[:100] + "..."
		}
		utils.Debug("LLM message",
			"index", i,
			"role", msg.Role,
			"content_preview", contentPreview,
			"images_count", imagesCount)
	}

	// Debug: логируем инструменты
	for i, tool := range toolDefs {
		utils.Debug("LLM tool",
			"index", i,
			"name", tool.Name,
			"description", tool.Description)
	}

	// 3. Конвертируем наши сообщения в формат OpenAI SDK
	openaiMsgs := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		openaiMsgs[i] = mapToOpenAI(m)
	}

	// 4. Создаём запрос с итоговыми параметрами (baseConfig + runtime override)
	req := openai.ChatCompletionRequest{
		Model:       options.Model,
		Temperature: float32(options.Temperature),
		MaxTokens:   options.MaxTokens,
		Messages:    openaiMsgs,
	}

	// 5. ResponseFormat если указан
	if options.Format == "json_object" {
		req.ResponseFormat = &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		}
	}

	// 6. Добавляем tools если переданы
	if len(toolDefs) > 0 {
		req.Tools = convertToolsToOpenAI(toolDefs)
		req.ToolChoice = "auto"
	}

	// 7. Вызываем API
	resp, err := c.api.CreateChatCompletion(ctx, req)
	if err != nil {
		utils.Error("LLM API request failed",
			"error", err,
			"model", options.Model,
			"duration_ms", time.Since(startTime).Milliseconds())
		return llm.Message{}, fmt.Errorf("openai api error: %w", err)
	}

	// 8. Проверяем что есть хотя бы один выбор
	if len(resp.Choices) == 0 {
		return llm.Message{}, fmt.Errorf("no choices in response")
	}

	// 9. Маппим ответ обратно в наш формат
	choice := resp.Choices[0].Message

	result := llm.Message{
		Role:    llm.Role(choice.Role),
		Content: choice.Content,
	}

	// 10. Извлекаем ToolCalls если модель решила вызвать функции
	if len(choice.ToolCalls) > 0 {
		result.ToolCalls = make([]llm.ToolCall, len(choice.ToolCalls))
		for i, tc := range choice.ToolCalls {
			result.ToolCalls[i] = llm.ToolCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: tc.Function.Arguments,
			}
		}
	}

	// Debug: логируем tool calls в ответе
	for i, tc := range result.ToolCalls {
		argsPreview := tc.Args
		if len(argsPreview) > 200 {
			argsPreview = argsPreview[:200] + "..."
		}
		utils.Debug("LLM tool call",
			"index", i,
			"id", tc.ID,
			"name", tc.Name,
			"args_preview", argsPreview)
	}

	utils.Info("LLM response received",
		"model", options.Model,
		"tool_calls_count", len(result.ToolCalls),
		"content_length", len(result.Content),
		"duration_ms", time.Since(startTime).Milliseconds())

	return result, nil
}

// mapToOpenAI конвертирует наше внутреннее сообщение в формат SDK.
// Здесь происходит магия Vision: если есть картинки, создаем MultiContent.
func mapToOpenAI(m llm.Message) openai.ChatCompletionMessage {
	msg := openai.ChatCompletionMessage{
		Role: string(m.Role),
	}

	// Если картинок нет, отправляем просто текст
	if len(m.Images) == 0 {
		msg.Content = m.Content
		return msg
	}

	// Если есть картинки (Vision запрос)
	parts := []openai.ChatMessagePart{
		{
			Type: openai.ChatMessagePartTypeText,
			Text: m.Content,
		},
	}

	for _, imgURL := range m.Images {
		parts = append(parts, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{
				URL:    imgURL, // Ожидается base64 data-uri или http ссылка
				Detail: openai.ImageURLDetailAuto,
			},
		})
	}

	msg.MultiContent = parts
	return msg
}

// convertToolsToOpenAI конвертирует определения инструментов во внутреннем формате
// в формат OpenAI Function Calling.
//
// Соответствие структур:
//   tools.ToolDefinition → openai.Tool (type=function)
//   Parameters (interface{}) → openai.FunctionDefinition.Parameters
//
// Поскольку ToolDefinition.Parameters уже является JSON Schema объектом
// (map[string]interface{}), он напрямую передаётся в OpenAI SDK.
func convertToolsToOpenAI(defs []tools.ToolDefinition) []openai.Tool {
	result := make([]openai.Tool, len(defs))

	for i, def := range defs {
		result[i] = openai.Tool{
			Type: "function",
			Function: &openai.FunctionDefinition{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.Parameters,
			},
		}
	}

	return result
}
