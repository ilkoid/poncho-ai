// Package std предоставляет стандартные инструменты для AI агента.
//
// LLMPingTool — инструмент для проверки доступности LLM провайдера.
package std

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/models"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// LLMPingTool — инструмент для проверки доступности LLM провайдера.
//
// Позволяет агенту проверить, доступен ли LLM провайдер и валиден ли API ключ.
// Поддерживает все провайдеры: openai, openrouter, zai, deepseek.
type LLMPingTool struct {
	modelRegistry *models.Registry
	cfg           *config.AppConfig // Для получения default_chat
	toolID        string
	description   string
}

// NewLLMPingTool создает инструмент для проверки доступности LLM провайдера.
//
// Параметры:
//   - registry: реестр LLM провайдеров
//   - cfg: конфигурация приложения
//   - toolCfg: конфигурация tool из YAML
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewLLMPingTool(registry *models.Registry, cfg *config.AppConfig, toolCfg config.ToolConfig) *LLMPingTool {
	return &LLMPingTool{
		modelRegistry: registry,
		cfg:           cfg,
		toolID:        "ping_llm_provider",
		description:   toolCfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *LLMPingTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "ping_llm_provider",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{
				"model": map[string]interface{}{
					"type":        "string",
					"description": "Алиас модели для проверки (например, 'glm-4.7', 'gemini-2.0-flash'). Если не указан, используется default_chat модель.",
				},
			},
			"required": []string{}, // model - опциональный параметр
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *LLMPingTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// Парсим аргументы
	var args struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		// Если JSON пустой - это нормально, модель будет выбрана по умолчанию
		args.Model = ""
	}

	// Получаем модель для проверки
	modelAlias := args.Model
	if modelAlias == "" {
		// Используем default_chat из конфига
		modelAlias = t.cfg.Models.DefaultChat
		if modelAlias == "" {
			return t.marshalErrorResult("default_chat модель не настроена в конфигурации", "CONFIG_ERROR")
		}
	}

	// Получаем провайдера из реестра
	_, modelDef, err := t.modelRegistry.Get(modelAlias)
	if err != nil {
		return t.marshalErrorResult(fmt.Sprintf("модель '%s' не найдена в реестре: %v", modelAlias, err), "MODEL_NOT_FOUND")
	}

	// Проверяем базовую конфигурацию
	if modelDef.BaseURL == "" {
		return t.marshalErrorResult(fmt.Sprintf("модель '%s' не имеет base_url в конфигурации", modelAlias), "CONFIG_ERROR")
	}

	// Проверяем API ключ (базовая проверка на placeholder)
	if modelDef.APIKey == "" {
		return t.marshalErrorResult(
			fmt.Sprintf("API ключ для модели '%s' не настроен", modelAlias),
			"API_KEY_MISSING",
		)
	}

	// Делаем тестовый запрос к API
	result := t.pingAPI(ctx, modelAlias, modelDef)
	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// pingAPI выполняет тестовый запрос к API провайдера.
func (t *LLMPingTool) pingAPI(ctx context.Context, modelAlias string, modelDef config.ModelDef) map[string]interface{} {
	// Создаем HTTP клиент с таймаутом
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Для OpenAI-совместимых API используем /models endpoint
	// Для других провайдеров можно использовать другие endpoints
	var endpoint string
	switch modelDef.Provider {
	case "openrouter", "openai", "zai", "deepseek":
		endpoint = modelDef.BaseURL + "/models"
	default:
		endpoint = modelDef.BaseURL + "/models"
	}

	// Создаем запрос
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return t.buildErrorResult(fmt.Sprintf("ошибка создания запроса: %v", err), "REQUEST_ERROR")
	}

	// Добавляем заголовки
	req.Header.Set("Authorization", "Bearer "+modelDef.APIKey)
	req.Header.Set("Content-Type", "application/json")

	// OpenRouter требует специальные заголовки
	if modelDef.Provider == "openrouter" {
		req.Header.Set("HTTP-Referer", "https://poncho-ai.dev")
		req.Header.Set("X-Title", "Poncho AI")
	}

	// Выполняем запрос
	startTime := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(startTime)

	if err != nil {
		return t.buildErrorResult(fmt.Sprintf("ошибка подключения: %v", err), "CONNECTION_ERROR")
	}
	defer resp.Body.Close()

	// Проверяем статус код
	result := map[string]interface{}{
		"available": true,
		"provider":  modelDef.Provider,
		"model":     modelDef.ModelName,
		"base_url":  modelDef.BaseURL,
		"status_code": resp.StatusCode,
		"latency_ms": latency.Milliseconds(),
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result["status"] = "OK"
		result["message"] = fmt.Sprintf("%s API доступен. Модель '%s' (%s) работает корректно.", modelDef.Provider, modelAlias, modelDef.ModelName)
	} else if resp.StatusCode == 401 {
		result["available"] = false
		result["error"] = "недействительный API ключ"
		result["error_type"] = "AUTH_ERROR"
		result["message"] = fmt.Sprintf("API ключ для модели '%s' недействителен. Проверьте значение %s", modelAlias, modelDef.APIKey)
	} else if resp.StatusCode == 429 {
		result["available"] = false
		result["error"] = "превышен лимит запросов"
		result["error_type"] = "RATE_LIMIT_ERROR"
		result["message"] = fmt.Sprintf("Превышен лимит запросов к %s API. Попробуйте позже.", modelDef.Provider)
	} else {
		result["available"] = false
		result["error"] = fmt.Sprintf("HTTP %d", resp.StatusCode)
		result["error_type"] = "HTTP_ERROR"
		result["message"] = fmt.Sprintf("%s API вернул статус %d. Проверьте конфигурацию.", modelDef.Provider, resp.StatusCode)
	}

	return result
}

// buildErrorResult создает результат ошибки в формате map.
func (t *LLMPingTool) buildErrorResult(message, errType string) map[string]interface{} {
	return map[string]interface{}{
		"available": false,
		"error":     message,
		"error_type": errType,
		"message":   message,
	}
}

// marshalErrorResult создает результат ошибки и маршалит его в JSON строку.
func (t *LLMPingTool) marshalErrorResult(message, errType string) (string, error) {
	result := t.buildErrorResult(message, errType)
	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
