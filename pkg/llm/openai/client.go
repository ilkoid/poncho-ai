// Package openai реализует адаптер LLM провайдера для OpenAI-совместимых API.
//
// Поддерживает Function Calling (tools) для интеграции с агент-системой.
// Соблюдает правило 4 манифеста: работает только через интерфейс llm.Provider.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

	// Устанавливаем parallel_tool_calls из конфигурации модели
	if modelDef.ParallelToolCalls != nil {
		baseConfig.ParallelToolCalls = modelDef.ParallelToolCalls
	}

	// Timeout из конфигурации модели с fallback на дефолт
	httpTimeout := modelDef.Timeout
	if httpTimeout == 0 {
		httpTimeout = 120 * time.Second // дефолтный fallback
	}

	// Custom transport для решения timeout проблем в WSL2
	transport := &http.Transport{
		// Disable HTTP/2 для стабильности (может вызывать timeout в WSL2)
		ForceAttemptHTTP2: false,
		// Increase connection timeouts
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: httpTimeout,
		// Keep-alive settings
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
	}

	return &Client{
		api:        client,
		apiKey:     modelDef.APIKey,
		baseConfig: baseConfig,
		thinking:   modelDef.Thinking,
		baseURL:    baseURL,
		httpClient: &http.Client{
			Timeout:   httpTimeout,
			Transport: transport,
		},
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

// generateStandard выполняет запрос через кастомный HTTP для поддержки parallel_tool_calls.
//
// ПРИМЕЧАНИЕ: Используем map[string]interface{} вместо openaisdk.ChatCompletionRequest,
// чтобы поддерживать параметр parallel_tool_calls который может отсутствовать в SDK.
func (c *Client) generateStandard(ctx context.Context, messages []llm.Message, options llm.GenerateOptions, toolDefs []tools.ToolDefinition, startTime time.Time) (llm.Message, error) {
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

		// Устанавливаем parallel_tool_calls если указан
		if options.ParallelToolCalls != nil {
			reqBody["parallel_tool_calls"] = *options.ParallelToolCalls
		}
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

	utils.Debug("Sending standard HTTP request",
		"url", url,
		"body_size", len(jsonBody),
		"parallel_tool_calls", fmt.Sprintf("%v", options.ParallelToolCalls))

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
				Role      string             `json:"role"`
				Content   string             `json:"content"`
				ToolCalls []openaisdk.ToolCall `json:"tool_calls,omitempty"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		return llm.Message{}, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return llm.Message{}, fmt.Errorf("no choices in response")
	}

	choice := apiResp.Choices[0]

	// Конвертируем tool calls
	var toolCalls []llm.ToolCall
	if len(choice.Message.ToolCalls) > 0 {
		toolCalls = make([]llm.ToolCall, len(choice.Message.ToolCalls))
		for i, tc := range choice.Message.ToolCalls {
			toolCalls[i] = llm.ToolCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: tc.Function.Arguments,
			}
		}
	}

	return llm.Message{
		Role:      llm.RoleAssistant,
		Content:   choice.Message.Content,
		ToolCalls: toolCalls,
	}, nil
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

		// Устанавливаем parallel_tool_calls если указан
		if options.ParallelToolCalls != nil {
			reqBody["parallel_tool_calls"] = *options.ParallelToolCalls
		}
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
		// Избегаем дублирования: если content уже содержится в reasoning_content, не добавляем
		if choice.Content == "" || strings.Contains(choice.ReasoningContent, choice.Content) {
			result.Content = choice.ReasoningContent
		} else if strings.Contains(choice.Content, choice.ReasoningContent) {
			// content уже содержит reasoning_content
			result.Content = choice.Content
		} else {
			// Объединяем без дубликатов
			result.Content = choice.ReasoningContent + "\n\n" + choice.Content
		}
		utils.Debug("Thinking content extracted",
			"reasoning_length", len(choice.ReasoningContent),
			"content_length", len(choice.Content),
			"result_length", len(result.Content))
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

// GenerateStream выполняет запрос к API с потоковой передачей ответа.
//
// Реализует llm.StreamingProvider интерфейс. Отправляет чанки через callback,
// накапливает финальное сообщение и возвращает его после завершения стриминга.
//
// Rule 11: Уважает context.Context, прерывает стриминг при отмене.
func (c *Client) GenerateStream(
	ctx context.Context,
	messages []llm.Message,
	callback func(llm.StreamChunk),
	opts ...any,
) (llm.Message, error) {
	startTime := time.Now()

	// 1. Начинаем с дефолтных значений из config.yaml
	options := c.baseConfig

	// 2. Разделяем opts на tools, GenerateOption и StreamOption
	var toolDefs []tools.ToolDefinition
	streamEnabled := true // Default: true (opt-out)
	thinkingOnly := true // Default: true

	for _, opt := range opts {
		switch v := opt.(type) {
		case []tools.ToolDefinition:
			toolDefs = v
		case llm.GenerateOption:
			v(&options)
		case llm.StreamOption:
			var so llm.StreamOptions
			v(&so)
			streamEnabled = so.Enabled
			thinkingOnly = so.ThinkingOnly
		default:
			return llm.Message{}, fmt.Errorf("invalid option type: expected []tools.ToolDefinition, llm.GenerateOption or llm.StreamOption, got %T", opt)
		}
	}

	// 3. Если стриминг выключен, fallback на обычный Generate
	if !streamEnabled {
		return c.Generate(ctx, messages, opts...)
	}

	utils.Debug("LLM streaming request started",
		"model", options.Model,
		"thinking", c.thinking,
		"thinking_only", thinkingOnly,
		"temperature", options.Temperature,
		"max_tokens", options.MaxTokens,
		"messages_count", len(messages),
		"tools_count", len(toolDefs))

	// 4. Если thinking включен - используем streaming с thinking
	if c.thinking != "" && c.thinking != "disabled" {
		return c.generateWithThinkingStream(ctx, messages, options, toolDefs, callback, thinkingOnly, startTime)
	}

	// 5. Иначе - стандартный streaming путь
	return c.generateStandardStream(ctx, messages, options, toolDefs, callback, thinkingOnly, startTime)
}

// generateWithThinkingStream выполняет streaming запрос с thinking параметром.
func (c *Client) generateWithThinkingStream(
	ctx context.Context,
	messages []llm.Message,
	options llm.GenerateOptions,
	toolDefs []tools.ToolDefinition,
	callback func(llm.StreamChunk),
	thinkingOnly bool,
	startTime time.Time,
) (llm.Message, error) {
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
		"stream":      true, // Включаем стриминг
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

		if options.ParallelToolCalls != nil {
			reqBody["parallel_tool_calls"] = *options.ParallelToolCalls
		}
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

	utils.Debug("Sending streaming HTTP request with thinking",
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

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		utils.Error("HTTP request returned error",
			"status", resp.StatusCode,
			"body", string(body),
			"duration_ms", time.Since(startTime).Milliseconds())
		return llm.Message{}, fmt.Errorf("api returned status %d: %s", resp.StatusCode, string(body))
	}

	// Парсим SSE ответ
	return c.parseSSEStream(ctx, resp.Body, callback, thinkingOnly, startTime)
}

// generateStandardStream выполняет стандартный streaming запрос (без thinking).
func (c *Client) generateStandardStream(
	ctx context.Context,
	messages []llm.Message,
	options llm.GenerateOptions,
	toolDefs []tools.ToolDefinition,
	callback func(llm.StreamChunk),
	thinkingOnly bool,
	startTime time.Time,
) (llm.Message, error) {
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
		"stream":      true, // Включаем стриминг
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

		if options.ParallelToolCalls != nil {
			reqBody["parallel_tool_calls"] = *options.ParallelToolCalls
		}
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

	utils.Debug("Sending standard streaming HTTP request",
		"url", url,
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

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		utils.Error("HTTP request returned error",
			"status", resp.StatusCode,
			"body", string(body),
			"duration_ms", time.Since(startTime).Milliseconds())
		return llm.Message{}, fmt.Errorf("api returned status %d: %s", resp.StatusCode, string(body))
	}

	// Парсим SSE ответ
	return c.parseSSEStream(ctx, resp.Body, callback, thinkingOnly, startTime)
}

// parseSSEStream парсит Server-Sent Events ответ от API.
//
// SSE формат: "data: {json}\n\n"
// Каждая строка содержит JSON объект с чанком ответа.
func (c *Client) parseSSEStream(
	ctx context.Context,
	body io.Reader,
	callback func(llm.StreamChunk),
	thinkingOnly bool,
	startTime time.Time,
) (llm.Message, error) {
	scanner := bufio.NewScanner(body)

	// Накопленные данные
	var accumulatedContent string
	var accumulatedReasoning string

	// ID последнего tool call для накопления аргументов
	pendingToolCalls := make(map[string]*llm.ToolCall)

	for scanner.Scan() {
		// Rule 11: Проверяем context cancellation
		select {
		case <-ctx.Done():
			return llm.Message{}, fmt.Errorf("streaming cancelled: %w", ctx.Err())
		default:
		}

		line := scanner.Text()

		// SSE формат: строки начинаются с "data: "
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		// Убираем префикс "data: "
		jsonStr := strings.TrimPrefix(line, "data: ")

		// Пустые данные или [DONE] маркер
		if jsonStr == "" || jsonStr == "[DONE]" {
			continue
		}

		// Парсим JSON
		var chunkData struct {
			Choices []struct {
				Delta struct {
					Role            string `json:"role"`
					Content         string `json:"content"`
					ReasoningContent string `json:"reasoning_content,omitempty"`
					ToolCallID      string `json:"tool_call_id,omitempty"`
					// Tool calls в streaming приходят через delta
					ToolCalls []struct {
						Index    int `json:"index"`
						ID       string `json:"id,omitempty"`
						Type     string `json:"type,omitempty"`
						Function struct {
							Name      string `json:"name,omitempty"`
							Arguments string `json:"arguments,omitempty"`
						} `json:"function,omitempty"`
					} `json:"tool_calls,omitempty"`
				} `json:"delta"`
				Message struct {
					Role            string                   `json:"role,omitempty"`
					Content         string                   `json:"content,omitempty"`
					ReasoningContent string                   `json:"reasoning_content,omitempty"`
					ToolCalls       []openaisdk.ToolCall     `json:"tool_calls,omitempty"`
				} `json:"message,omitempty"`
				FinishReason string `json:"finish_reason,omitempty"`
			} `json:"choices"`
		}

		if err := json.Unmarshal([]byte(jsonStr), &chunkData); err != nil {
			utils.Debug("Failed to parse SSE chunk", "error", err, "json", jsonStr)
			continue
		}

		if len(chunkData.Choices) == 0 {
			continue
		}

		choice := chunkData.Choices[0]

		// Обрабатываем reasoning_content из thinking mode
		if choice.Delta.ReasoningContent != "" {
			delta := choice.Delta.ReasoningContent
			accumulatedReasoning += delta

			// ВСЕГДА отправляем reasoning chunks для streaming
			// (включая thinkingOnly режим - именно ради него мы стримим!)
			if callback != nil {
				callback(llm.StreamChunk{
					Type:             llm.ChunkThinking,
					Content:          accumulatedContent,
					ReasoningContent: accumulatedReasoning,
					Delta:            delta,
				})
			}
		}

		// Обрабатываем обычный контент
		if choice.Delta.Content != "" {
			delta := choice.Delta.Content
			accumulatedContent += delta

			// Отправляем событие только если НЕ thinkingOnly
			if callback != nil && !thinkingOnly {
				callback(llm.StreamChunk{
					Type:             llm.ChunkContent,
					Content:          accumulatedContent,
					ReasoningContent: accumulatedReasoning,
					Delta:            delta,
				})
			}
		}

		// Обрабатываем tool calls из delta (streaming mode!)
		// В streaming режиме tool calls приходят через delta, а не через message
		if len(choice.Delta.ToolCalls) > 0 {
			for _, deltaTC := range choice.Delta.ToolCalls {
				// Используем index как ключ (может быть 0, 1, 2...)
				indexKey := fmt.Sprintf("delta_%d", deltaTC.Index)

				if _, exists := pendingToolCalls[indexKey]; !exists {
					// Новый tool call
					pendingToolCalls[indexKey] = &llm.ToolCall{
						ID:   deltaTC.ID,
						Name: deltaTC.Function.Name,
						Args: deltaTC.Function.Arguments,
					}
					utils.Debug("New tool call in delta", "index", deltaTC.Index, "id", deltaTC.ID, "name", deltaTC.Function.Name)
				} else {
					// Обновляем существующий tool call (ID может прийти позже, накапливаем arguments)
					if deltaTC.ID != "" {
						pendingToolCalls[indexKey].ID = deltaTC.ID
					}
					if deltaTC.Function.Name != "" {
						pendingToolCalls[indexKey].Name = deltaTC.Function.Name
					}
					if deltaTC.Function.Arguments != "" {
						pendingToolCalls[indexKey].Args += deltaTC.Function.Arguments
					}
					utils.Debug("Accumulated tool call in delta", "index", deltaTC.Index, "args_len", len(deltaTC.Function.Arguments))
				}
			}
		}

		// Обрабатываем tool calls из message (финальные данные)
		if len(choice.Message.ToolCalls) > 0 {
			for _, tc := range choice.Message.ToolCalls {
				if _, exists := pendingToolCalls[tc.ID]; !exists {
					pendingToolCalls[tc.ID] = &llm.ToolCall{
						ID:   tc.ID,
						Name: tc.Function.Name,
						Args: tc.Function.Arguments,
					}
				} else {
					// Накапливаем аргументы
					pendingToolCalls[tc.ID].Args += tc.Function.Arguments
				}
			}
		}

		// Проверяем завершение стриминга
		if choice.FinishReason != "" {
			utils.Debug("Streaming finished",
				"reason", choice.FinishReason,
				"content_length", len(accumulatedContent),
				"reasoning_length", len(accumulatedReasoning),
				"tool_calls_count", len(pendingToolCalls))
		}
	}

	// Проверяем ошибки сканера
	if err := scanner.Err(); err != nil {
		utils.Error("Scanner error during streaming", "error", err)
		return llm.Message{}, fmt.Errorf("scanner error: %w", err)
	}

	// Собираем финальное сообщение
	result := llm.Message{
		Role:    llm.RoleAssistant,
		Content: accumulatedContent,
	}

	// Если есть reasoning_content, добавляем к контенту (как в generateWithThinking)
	if accumulatedReasoning != "" {
		if accumulatedContent == "" || strings.Contains(accumulatedReasoning, accumulatedContent) {
			result.Content = accumulatedReasoning
		} else if strings.Contains(accumulatedContent, accumulatedReasoning) {
			result.Content = accumulatedContent
		} else {
			result.Content = accumulatedReasoning + "\n\n" + accumulatedContent
		}
	}

	// Собираем tool calls
	if len(pendingToolCalls) > 0 {
		result.ToolCalls = make([]llm.ToolCall, 0, len(pendingToolCalls))
		for _, tc := range pendingToolCalls {
			result.ToolCalls = append(result.ToolCalls, *tc)
		}
	}

	// Отправляем финальный chunk
	if callback != nil {
		callback(llm.StreamChunk{
			Type:             llm.ChunkDone,
			Content:          result.Content,
			ReasoningContent: accumulatedReasoning,
			Done:             true,
		})
	}

	utils.Info("LLM streaming response received",
		"model", c.baseConfig.Model,
		"tool_calls_count", len(result.ToolCalls),
		"content_length", len(result.Content),
		"reasoning_length", len(accumulatedReasoning),
		"duration_ms", time.Since(startTime).Milliseconds())

	return result, nil
}
