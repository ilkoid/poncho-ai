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
type Client struct {
	api   *openai.Client
	model string
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

	return &Client{
		api:   client,
		model: modelDef.ModelName,
	}
}

// Generate выполняет запрос к API и возвращает ответ модели.
//
// Поддерживает опциональную передачу definitions инструментов для Function Calling:
//   toolsArgs[0] должен быть []tools.ToolDefinition
//
// Алгоритм:
//   1. Конвертирует внутренние сообщения в формат OpenAI SDK
//   2. Если переданы tools — добавляет их в запрос
//   3. Вызывает API
//   4. Конвертирует ответ обратно в наш формат
//   5. Извлекает ToolCalls если модель решила вызвать функции
//
// Правило 7: Все ошибки возвращаются, никаких panic.
//
// Правило 4: Работает только через интерфейс llm.Provider.
func (c *Client) Generate(ctx context.Context, messages []llm.Message, toolsArgs ...any) (llm.Message, error) {
	startTime := time.Now()

	utils.Debug("LLM request started",
		"model", c.model,
		"messages_count", len(messages),
		"tools_count", func() int {
			if len(toolsArgs) > 0 {
				if defs, ok := toolsArgs[0].([]tools.ToolDefinition); ok {
					return len(defs)
				}
			}
			return 0
		}())

	// 1. Конвертируем наши сообщения в формат OpenAI SDK
	openaiMsgs := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		openaiMsgs[i] = mapToOpenAI(m)
	}

	// 2. Создаём базовый запрос
	req := openai.ChatCompletionRequest{
		Model:    c.model,
		Messages: openaiMsgs,
	}

	// 3. Добавляем tools если переданы
	// Ожидаем toolsArgs[0] = []tools.ToolDefinition
	if len(toolsArgs) > 0 {
		toolDefs, ok := toolsArgs[0].([]tools.ToolDefinition)
		if !ok {
			return llm.Message{}, fmt.Errorf("invalid tools type: expected []tools.ToolDefinition, got %T", toolsArgs[0])
		}

		// Конвертируем в формат OpenAI
		req.Tools = convertToolsToOpenAI(toolDefs)

		// Включаем автоматический режим — LLM сама решает когда вызывать tools
		req.ToolChoice = "auto"
	}

	// 4. Вызываем API
	// Правило 7: возвращаем ошибку вместо panic
	resp, err := c.api.CreateChatCompletion(ctx, req)
	if err != nil {
		utils.Error("LLM API request failed",
			"error", err,
			"model", c.model,
			"duration_ms", time.Since(startTime).Milliseconds())
		return llm.Message{}, fmt.Errorf("openai api error: %w", err)
	}

	// Проверяем что есть хотя бы один выбор
	if len(resp.Choices) == 0 {
		return llm.Message{}, fmt.Errorf("no choices in response")
	}

	// 5. Маппим ответ обратно в наш формат
	choice := resp.Choices[0].Message

	result := llm.Message{
		Role:    llm.Role(choice.Role),
		Content: choice.Content,
	}

	// 6. Извлекаем ToolCalls если модель решила вызвать функции
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

	utils.Info("LLM response received",
		"model", c.model,
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
