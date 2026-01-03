// Package std содержит инструменты для работы со справочниками Wildberries.
//
// Инструменты принимают *wb.Dictionaries через конструктор для кэширования
// и переиспользования данных, загруженных при старте приложения.
package std

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WbColorsTool — инструмент для поиска цветов с fuzzy search.
//
// Использует ColorService для нечеткого поиска по справочнику цветов WB.
type WbColorsTool struct {
	colorService *wb.ColorService
	dicts        *wb.Dictionaries // Для получения топа цветов при пустом поиске
	toolID       string
	description  string
}

// NewWbColorsTool создает инструмент для поиска цветов.
//
// Параметры:
//   - dicts: Кэшированные справочники (полученные из wb.Client.LoadDictionaries)
//   - cfg: Конфигурация tool из YAML (используется для единообразия)
//
// Возвращает инструмент с инициализированным ColorService.
func NewWbColorsTool(dicts *wb.Dictionaries, cfg config.ToolConfig) *WbColorsTool {
	return &WbColorsTool{
		colorService: wb.NewColorService(dicts.Colors),
		dicts:        dicts,
		toolID:       "get_wb_colors",
		description:  cfg.Description,
	}
}

func (t *WbColorsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_colors",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"search": map[string]interface{}{
					"type":        "string",
					"description": "Подстрока для поиска цвета (например, 'персиковый', 'красный', 'голубой'). Поиск нечеткий, работает по подстроке и схожим названиям.",
				},
				"top": map[string]interface{}{
					"type":        "integer",
					"description": "Количество результатов (по умолчанию 10, максимум 50).",
				},
			},
			"required": []string{}, // search опционален - можно вызвать без параметров для топа
		},
	}
}

func (t *WbColorsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Search string `json:"search"`
		Top    int    `json:"top"`
	}

	// Аргументы опциональны - если пусто, возвращаем топ цветов
	if argsJSON != "" && argsJSON != "{}" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}

	// Дефолтные значения
	if args.Top <= 0 {
		args.Top = 10
	}
	if args.Top > 50 {
		args.Top = 50
	}

	// Если поиск пустой - возвращаем топ цветов из справочника
	if args.Search == "" {
		// Возвращаем первые top цветов как "популярные"
		top := args.Top
		if top > len(t.dicts.Colors) {
			top = len(t.dicts.Colors)
		}
		data, err := json.Marshal(t.dicts.Colors[:top])
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	// Fuzzy search через ColorService
	matches := t.colorService.FindTopMatches(args.Search, args.Top)

	data, err := json.Marshal(matches)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WbCountriesTool — инструмент для получения справочника стран.
//
// Возвращает список стран производства для карточки товара.
type WbCountriesTool struct {
	dicts  *wb.Dictionaries
	toolID string
	description string
}

// NewWbCountriesTool создает инструмент для получения стран.
func NewWbCountriesTool(dicts *wb.Dictionaries, cfg config.ToolConfig) *WbCountriesTool {
	return &WbCountriesTool{
		dicts:  dicts,
		toolID: "get_wb_countries",
		description: cfg.Description,
	}
}

func (t *WbCountriesTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_countries",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{}, // Нет обязательных параметров
		},
	}
}

func (t *WbCountriesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	data, err := json.Marshal(t.dicts.Countries)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WbGendersTool — инструмент для получения справочника полов.
//
// Возвращает список допустимых значений для характеристики "Пол".
type WbGendersTool struct {
	dicts  *wb.Dictionaries
	toolID string
	description string
}

// NewWbGendersTool создает инструмент для получения полов.
func NewWbGendersTool(dicts *wb.Dictionaries, cfg config.ToolConfig) *WbGendersTool {
	return &WbGendersTool{
		dicts:  dicts,
		toolID: "get_wb_genders",
		description: cfg.Description,
	}
}

func (t *WbGendersTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_genders",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{}, // Нет обязательных параметров
		},
	}
}

func (t *WbGendersTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	data, err := json.Marshal(t.dicts.Genders)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WbSeasonsTool — инструмент для получения справочника сезонов.
//
// Возвращает список допустимых значений для характеристики "Сезон".
type WbSeasonsTool struct {
	dicts  *wb.Dictionaries
	toolID string
	description string
}

// NewWbSeasonsTool создает инструмент для получения сезонов.
func NewWbSeasonsTool(dicts *wb.Dictionaries, cfg config.ToolConfig) *WbSeasonsTool {
	return &WbSeasonsTool{
		dicts:  dicts,
		toolID: "get_wb_seasons",
		description: cfg.Description,
	}
}

func (t *WbSeasonsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_seasons",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{}, // Нет обязательных параметров
		},
	}
}

func (t *WbSeasonsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	data, err := json.Marshal(t.dicts.Seasons)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WbVatRatesTool — инструмент для получения справочника ставок НДС.
//
// Возвращает список допустимых значений НДС для карточки товара.
type WbVatRatesTool struct {
	dicts  *wb.Dictionaries
	toolID string
	description string
}

// NewWbVatRatesTool создает инструмент для получения ставок НДС.
func NewWbVatRatesTool(dicts *wb.Dictionaries, cfg config.ToolConfig) *WbVatRatesTool {
	return &WbVatRatesTool{
		dicts:  dicts,
		toolID: "get_wb_vat_rates",
		description: cfg.Description,
	}
}

func (t *WbVatRatesTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_vat_rates",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{}, // Нет обязательных параметров
		},
	}
}

func (t *WbVatRatesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	data, err := json.Marshal(t.dicts.Vats)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ReloadWbDictionariesTool — заглушка для перезагрузки справочников из API.
//
// TODO: Реализовать полную перезагрузку справочников через WB API.
type ReloadWbDictionariesTool struct {
	client *wb.Client
	dicts  *wb.Dictionaries
}

// NewReloadWbDictionariesTool создает заглушку для инструмента перезагрузки справочников.
func NewReloadWbDictionariesTool(client *wb.Client, cfg config.ToolConfig) *ReloadWbDictionariesTool {
	// dicts передается через компоненты, здесь заглушка
	return &ReloadWbDictionariesTool{
		client: client,
		dicts:  nil, // Будет установлен при инициализации
	}
}

func (t *ReloadWbDictionariesTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "reload_wb_dictionaries",
		Description: "Перезагружает справочники Wildberries из API. Возвращает количество записей в каждом справочнике. Используй для проверки доступности API или после изменения данных. ВНИМАНИЕ: не обновляет состояние агента, только возвращает данные. [STUB - НЕ РЕАЛИЗОВАНО]",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		},
	}
}

func (t *ReloadWbDictionariesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// STUB: Возврат заглушки с информацией о текущих справочниках
	stub := map[string]interface{}{
		"error":   "not_implemented",
		"message": "reload_wb_dictionaries tool is not implemented yet. Dictionaries are loaded at startup.",
		"current_counts": map[string]interface{}{
			"colors":    0,
			"countries": 0,
			"genders":   0,
			"seasons":   0,
			"vat_rates": 0,
		},
	}
	result, _ := json.Marshal(stub)
	return string(result), nil
}

