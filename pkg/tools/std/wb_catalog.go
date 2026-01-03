// Package std содержит стандартные инструменты для работы с Wildberries.
//
// Реализует инструменты для получения каталога категорий и предметов.
package std

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WbParentCategoriesTool — инструмент для получения родительских категорий Wildberries.
//
// Позволяет агенту получить список верхнеуровневых категорий (Женщинам, Мужчинам, и т.д.).
type WbParentCategoriesTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string // Описание из YAML конфигурации
}

// NewWbParentCategoriesTool создает инструмент для получения родительских категорий.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbParentCategoriesTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbParentCategoriesTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbParentCategoriesTool{
		client:      c,
		toolID:      "get_wb_parent_categories",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbParentCategoriesTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_parent_categories",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{}, // Нет обязательных параметров
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbParentCategoriesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock()
	}

	// Передаем параметры из tool config в client
	cats, err := t.client.GetParentCategories(ctx, t.endpoint, t.rateLimit, t.burst)
	if err != nil {
		return "", fmt.Errorf("failed to get parent categories: %w", err)
	}

	data, err := json.Marshal(cats)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// executeMock возвращает mock данные с реальной структурой WB API.
func (t *WbParentCategoriesTool) executeMock() (string, error) {
	mockCats := []map[string]interface{}{
		{"id": 1541, "name": "Женщинам", "isVisible": true},
		{"id": 1542, "name": "Мужчинам", "isVisible": true},
		{"id": 1543, "name": "Детям", "isVisible": true},
		{"id": 1544, "name": "Обувь", "isVisible": true},
		{"id": 1545, "name": "Аксессуары", "isVisible": true},
		{"id": 1546, "name": "Белье", "isVisible": true},
		{"id": 1547, "name": "Красота", "isVisible": true},
		{"id": 1525, "name": "Дом", "isVisible": true},
		{"id": 1534, "name": "Электроника", "isVisible": true},
	}

	data, err := json.Marshal(mockCats)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WbSubjectsTool — инструмент для получения предметов (подкатегорий) Wildberries.
//
// Позволяет агенту получить список подкатегорий для заданной родительской категории.
type WbSubjectsTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbSubjectsTool создает инструмент для получения предметов (подкатегорий).
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbSubjectsTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbSubjectsTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbSubjectsTool{
		client:      c,
		toolID:      "get_wb_subjects",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbSubjectsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_subjects",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"parentID": map[string]interface{}{
					"type":        "integer",
					"description": "ID родительской категории (получи его из get_wb_parent_categories)",
				},
			},
			"required": []string{"parentID"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbSubjectsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		ParentID int `json:"parentID"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments json: %w", err)
	}

	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock(args.ParentID)
	}

	// Передаем параметры из tool config в client
	subjects, err := t.client.GetAllSubjectsLazy(ctx, t.endpoint, t.rateLimit, t.burst, args.ParentID)
	if err != nil {
		return "", fmt.Errorf("failed to get subjects: %w", err)
	}

	data, err := json.Marshal(subjects)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// executeMock возвращает mock данные с реальной структурой WB API для subjects.
func (t *WbSubjectsTool) executeMock(parentID int) (string, error) {
	// Определяем родительскую категорию для более реалистичного mock
	var parentName string
	var mockSubjects []map[string]interface{}

	switch parentID {
	case 1541: // Женщинам
		parentName = "Женщинам"
		mockSubjects = []map[string]interface{}{
			{"id": 685, "name": "Платья", "objectName": "Платье", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1031, "name": "Блузки и рубашки", "objectName": "Блузка", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1571, "name": "Брюки и джинсы", "objectName": "Брюки", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1727, "name": "Юбки", "objectName": "Юбка", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1335, "name": "Костюмы", "objectName": "Костюм", "hasParent": true, "parentID": parentID, "parentName": parentName},
		}
	case 1542: // Мужчинам
		parentName = "Мужчинам"
		mockSubjects = []map[string]interface{}{
			{"id": 1171, "name": "Футболки и майки", "objectName": "Футболка", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1177, "name": "Рубашки", "objectName": "Рубашка", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1350, "name": "Брюки", "objectName": "Брюки", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1349, "name": "Джинсы", "objectName": "Джинсы", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1467, "name": "Пиджаки и жакеты", "objectName": "Пиджак", "hasParent": true, "parentID": parentID, "parentName": parentName},
		}
	case 1543: // Детям
		parentName = "Детям"
		mockSubjects = []map[string]interface{}{
			{"id": 1479, "name": "Для девочек", "objectName": "Платье", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1480, "name": "Для мальчиков", "objectName": "Брюки", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1481, "name": "Для новорожденных", "objectName": "Комбинезон", "hasParent": true, "parentID": parentID, "parentName": parentName},
		}
	case 1544: // Обувь
		parentName = "Обувь"
		mockSubjects = []map[string]interface{}{
			{"id": 642, "name": "Женская обувь", "objectName": "Босоножки", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 643, "name": "Мужская обувь", "objectName": "Кроссовки", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 644, "name": "Детская обувь", "objectName": "Ботинки", "hasParent": true, "parentID": parentID, "parentName": parentName},
		}
	default:
		// Для неизвестного parentID возвращаем общие данные
		parentName = "Unknown"
		mockSubjects = []map[string]interface{}{
			{"id": 1, "name": "Категория 1", "objectName": "Объект 1", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 2, "name": "Категория 2", "objectName": "Объект 2", "hasParent": true, "parentID": parentID, "parentName": parentName},
		}
	}

	data, err := json.Marshal(mockSubjects)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WbPingTool — инструмент для проверки доступности Wildberries API.
//
// Позволяет агенту проверить, доступен ли WB Content API и валиден ли API ключ.
// Возвращает детальную диагностику: статус сервиса, timestamp, тип ошибки.
type WbPingTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbPingTool создает инструмент для проверки доступности WB API.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbPingTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbPingTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbPingTool{
		client:      c,
		toolID:      "ping_wb_api",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbPingTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "ping_wb_api",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{}, // Нет обязательных параметров
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbPingTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock()
	}

	// Передаем параметры из tool config в client
	resp, err := t.client.Ping(ctx, t.endpoint, t.rateLimit, t.burst)

	// Формируем развернутый ответ для LLM
	result := map[string]interface{}{
		"available": err == nil,
	}

	if err != nil {
		errType := t.client.ClassifyError(err)
		result["error"] = err.Error()
		result["error_type"] = errType.String()
		result["message"] = errType.HumanMessage()
	} else {
		result["status"] = resp.Status
		result["timestamp"] = resp.TS
		result["message"] = "Wildberries Content API доступен и работает корректно."
	}

	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// executeMock возвращает mock данные для ping (demo режим работает).
func (t *WbPingTool) executeMock() (string, error) {
	result := map[string]interface{}{
		"available": true,
		"status":    "OK",
		"timestamp": "2026-01-01T12:00:00Z",
		"message":   "Demo режим: Wildberries Content API имитирует доступность. Для реальных запросов установите WB_API_KEY.",
	}

	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
