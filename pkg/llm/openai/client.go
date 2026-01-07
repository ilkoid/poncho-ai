// Package openai реализует адаптер LLM провайдера для OpenAI-совместимых API.
//
// Поддерживает Function Calling (tools) для интеграции с агент-системой.
// Соблюдает правило 4 манифеста: работает только через интерфейс llm.Provider.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/utils"
	openaisdk "github.com/sashabaranov/go-openai"
)

// Client реализует интерфейс llm.Provider для OpenAI-совместимых API.
//
// Поддерживает:
//   - Базовую генерацию текста
//   - Function Calling (tools)
//   - Vision запросы (изображения)
//   - Runtime переопределение параметров через GenerateOption
//   - Thinking mode для Zai GLM (параметр thinking)
//
// Правило 4 ("Тупой клиент"): Не хранит состояние между вызовами.
// Все параметры передаются при каждом Generate() вызове.
type Client struct {
	api        *openaisdk.Client
	apiKey     string               // API Key для кастомных HTTP запросов с thinking
	baseConfig llm.GenerateOptions // Дефолтные параметры из config.yaml
	thinking   string               // Thinking parameter для Zai GLM: "enabled", "disabled" или ""
	httpClient *http.Client         // Для кастомных запросов с thinking
	baseURL    string               // Base URL для HTTP запросов
}

// NewClient создает OpenAI клиент на основе конфигурации модели.
//
// Принимает ModelDef напрямую для упрощения создания клиентов через factory.
// Использует APIKey из конфигурации для аутентификации.
//
// Правило 2: Все настройки из конфигурации, никакого хардкода.
func NewClient(modelDef config.ModelDef) *Client {
	// Поддержка custom BaseURL для non-OpenAI провайдеров (Zai, DeepSeek и т.д.)
	cfg := openaisdk.DefaultConfig(modelDef.APIKey)
	baseURL := modelDef.BaseURL
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}

	client := openaisdk.NewClientWithConfig(cfg)

	// Если baseURL не задан, используем дефолтный OpenAI
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	// Заполняем baseConfig из ModelDef для использования как дефолты
	baseConfig := llm.GenerateOptions{
		Model:       modelDef.ModelName,
		Temperature: modelDef.Temperature,
		MaxTokens:   modelDef.MaxTokens,
		Format:      "",
	}

	return &Client{
		api:        client,
		apiKey:     modelDef.APIKey,
		baseConfig: baseConfig,
		thinking:   modelDef.Thinking,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// Generate выполняет запрос к API и возвращает ответ модели.
//
// Поддерживает:
//   - Function Calling (tools): []tools.ToolDefinition
//   - Runtime переопределение параметров: GenerateOption
//   - Thinking mode для Zai GLM
//
// Алгоритм:
//   1. Разделяет opts на tools и GenerateOptions
//   2. Применяет GenerateOptions к baseConfig (runtime override)
//   3. Конвертирует сообщения в формат OpenAI SDK
//   4. Если thinking включен — использует кастомный HTTP запрос
//   5. Иначе — использует стандартный go-openai клиент
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
		"thinking", c.thinking,
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

	// 3. Если thinking включен - используем кастомный HTTP запрос
	if c.thinking != "" && c.thinking != "disabled" {
		return c.generateWithThinking(ctx, messages, options, toolDefs, startTime)
	}

	// 4. Иначе - стандартный путь через go-openai SDK
	return c.generateStandard(ctx, messages, options, toolDefs, startTime)
}

// generateStandard выполняет запрос через стандартный go-openai SDK.
func (c *Client) generateStandard(ctx context.Context, messages []llm.Message, options llm.GenerateOptions, toolDefs []tools.ToolDefinition, startTime time.Time) (llm.Message, error) {
	// Конвертируем наши сообщения в формат OpenAI SDK
	openaiMsgs := make([]openaisdk.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		openaiMsgs[i] = mapToOpenAI(m)
	}

	// Создаём запрос с итоговыми параметрами
	req := openaisdk.ChatCompletionRequest{
		Model:       options.Model,
		Temperature: float32(options.Temperature),
		MaxTokens:   options.MaxTokens,
		Messages:    openaiMsgs,
	}

	// ResponseFormat если указан
	if options.Format == "json_object" {
		req.ResponseFormat = &openaisdk.ChatCompletionResponseFormat{
			Type: openaisdk.ChatCompletionResponseFormatTypeJSONObject,
		}
	}

	// Добавляем tools если переданы
	if len(toolDefs) > 0 {
		req.Tools = convertToolsToOpenAI(toolDefs)
		req.ToolChoice = "auto"
	}

	// Вызываем API
	resp, err := c.api.CreateChatCompletion(ctx, req)
	if err != nil {
		utils.Error("LLM API request failed",
			"error", err,
			"model", options.Model,
			"duration_ms", time.Since(startTime).Milliseconds())
		return llm.Message{}, fmt.Errorf("openai api error: %w", err)
	}

	return c.parseResponse(resp, startTime, options.Model)
}

// generateWithThinking выполняет запрос с thinking параметром через кастомный HTTP.
func (c *Client) generateWithThinking(ctx context.Context, messages []llm.Message, options llm.GenerateOptions, toolDefs []tools.ToolDefinition, startTime time.Time) (llm.Message, error) {
	// Конвертируем сообщения
	openaiMsgs := make([]openaisdk.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		openaiMsgs[i] = mapToOpenAI(m)
	}

	// Строим запрос body
	reqBody := map[string]interface{}{
		"model":       options.Model,
		"temperature": options.Temperature,
		"max_tokens":  options.MaxTokens,
		"messages":    openaiMsgs,
		"thinking": map[string]string{
			"type": c.thinking,
		},
	}

	// ResponseFormat если указан
	if options.Format == "json_object" {
		reqBody["response_format"] = map[string]string{
			"type": "json_object",
		}
	}

	// Добавляем tools если переданы
	if len(toolDefs) > 0 {
		reqBody["tools"] = convertToolsToOpenAI(toolDefs)
		reqBody["tool_choice"] = "auto"
	}

	// Marshal в JSON
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return llm.Message{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Создаём HTTP запрос
	url := c.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return llm.Message{}, fmt.Errorf("failed to create request: %w", err)
	}

	// Устанавливаем заголовки
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	utils.Debug("Sending custom HTTP request with thinking",
		"url", url,
		"thinking", c.thinking,
		"body_size", len(jsonBody))

	// Выполняем запрос
	resp, err := c.httpClient.Do(req)
	if err != nil {
		utils.Error("HTTP request failed",
			"error", err,
			"url", url,
			"duration_ms", time.Since(startTime).Milliseconds())
		return llm.Message{}, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	// Читаем тело ответа
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return llm.Message{}, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		utils.Error("HTTP request returned error",
			"status", resp.StatusCode,
			"body", string(body),
			"duration_ms", time.Since(startTime).Milliseconds())
		return llm.Message{}, fmt.Errorf("api returned status %d: %s", resp.StatusCode, string(body))
	}

	// Парсим ответ
	var apiResp struct {
		Choices []struct {
			Message struct {
				Role            string                   `json:"role"`
				Content         string                   `json:"content"`
				ToolCalls       []openaisdk.ToolCall     `json:"tool_calls,omitempty"`
				ReasoningContent string                   `json:"reasoning_content,omitempty"` // Thinking контент
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		return llm.Message{}, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return llm.Message{}, fmt.Errorf("no choices in response")
	}

	choice := apiResp.Choices[0].Message
	result := llm.Message{
		Role:    llm.Role(choice.Role),
		Content: choice.Content,
	}

	// Если есть reasoning_content от thinking mode, добавляем к контенту
	if choice.ReasoningContent != "" {
		result.Content = choice.ReasoningContent + "\n\n" + choice.Content
		utils.Debug("Thinking content extracted",
			"reasoning_length", len(choice.ReasoningContent),
			"content_length", len(choice.Content))
	}

	// Извлекаем ToolCalls
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

	utils.Info("LLM response received (with thinking)",
		"model", options.Model,
		"tool_calls_count", len(result.ToolCalls),
		"content_length", len(result.Content),
		"duration_ms", time.Since(startTime).Milliseconds())

	return result, nil
}

// parseResponse парсит ответ от go-openai SDK.
func (c *Client) parseResponse(resp openaisdk.ChatCompletionResponse, startTime time.Time, modelName string) (llm.Message, error) {
	// Проверяем что есть хотя бы один выбор
	if len(resp.Choices) == 0 {
		return llm.Message{}, fmt.Errorf("no choices in response")
	}

	// Маппим ответ обратно в наш формат
	choice := resp.Choices[0].Message

	result := llm.Message{
		Role:    llm.Role(choice.Role),
		Content: choice.Content,
	}

	// Извлекаем ToolCalls если модель решила вызвать функции
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
		"model", modelName,
		"tool_calls_count", len(result.ToolCalls),
		"content_length", len(result.Content),
		"duration_ms", time.Since(startTime).Milliseconds())

	return result, nil
}

// mapToOpenAI конвертирует наше внутреннее сообщение в формат SDK.
// Здесь происходит магия Vision: если есть картинки, создаем MultiContent.
func mapToOpenAI(m llm.Message) openaisdk.ChatCompletionMessage {
	msg := openaisdk.ChatCompletionMessage{
		Role: string(m.Role),
	}

	// Если картинок нет, отправляем просто текст
	if len(m.Images) == 0 {
		msg.Content = m.Content
		return msg
	}

	// Если есть картинки (Vision запрос)
	parts := []openaisdk.ChatMessagePart{
		{
			Type: openaisdk.ChatMessagePartTypeText,
			Text: m.Content,
		},
	}

	for _, imgURL := range m.Images {
		parts = append(parts, openaisdk.ChatMessagePart{
			Type: openaisdk.ChatMessagePartTypeImageURL,
			ImageURL: &openaisdk.ChatMessageImageURL{
				URL:    imgURL, // Ожидается base64 data-uri или http ссылка
				Detail: openaisdk.ImageURLDetailAuto,
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
//   tools.ToolDefinition → openaisdk.Tool (type=function)
//   Parameters (interface{}) → openaisdk.FunctionDefinition.Parameters
//
// Поскольку ToolDefinition.Parameters уже является JSON Schema объектом
// (map[string]interface{}), он напрямую передаётся в OpenAI SDK.
func convertToolsToOpenAI(defs []tools.ToolDefinition) []openaisdk.Tool {
	result := make([]openaisdk.Tool, len(defs))

	for i, def := range defs {
		result[i] = openaisdk.Tool{
			Type: "function",
			Function: &openaisdk.FunctionDefinition{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.Parameters,
			},
		}
	}

	return result
}
