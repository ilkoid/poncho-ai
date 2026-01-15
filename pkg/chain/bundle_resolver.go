// Package chain предоставляет Chain Pattern для AI агента.
//
// Этот файл реализует Bundle Resolver для токен-оптимизации.
//
// # Phase 3: Token Resolution
//
// Bundle Resolver позволяет динамически расширять bundle в реальные инструменты.
// Это снижает количество токенов в system prompt:
//   - Без bundles: 100 tools = ~15,000 tokens
//   - С bundles: 10 bundles = ~300 tokens (98% savings!)
//
// # Flow
//
// 1. LLM видит только bundle definitions (~300 tokens)
// 2. LLM вызывает bundle (например, "wb_content_tools")
// 3. BundleResolver.expandBundle() детектит bundle call
// 4. Получает реальные tools из bundle
// 5. Добавляет tool definitions как system message в history
// 6. Re-run LLM с расширенным контекстом
// 7. LLM вызывает конкретный tool
//
// # Configuration
//
// tool_resolution_mode: "bundle-first" | "flat"
//   - "bundle-first": сначала bundles, затем dynamic expansion
//   - "flat": backward compatible (все tools сразу)
package chain

import (
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// BundleResolver — резолвер bundle для динамического расширения.
type BundleResolver struct {
	// cfg — конфигурация приложения
	cfg *config.AppConfig

	// registry — реестр инструментов
	registry *tools.Registry

	// bundleCache — кэш bundle definitions (bundle name → tools)
	bundleCache map[string][]tools.ToolDefinition

	// mode — режим резолюции (bundle-first или flat)
	mode ResolutionMode
}

// ResolutionMode — режим резолюции инструментов.
type ResolutionMode string

const (
	// ResolutionModeBundleFirst — сначала bundles, затем dynamic expansion
	ResolutionModeBundleFirst ResolutionMode = "bundle-first"

	// ResolutionModeFlat — backward compatible (все tools сразу)
	ResolutionModeFlat ResolutionMode = "flat"
)

// NewBundleResolver создаёт новый BundleResolver.
func NewBundleResolver(cfg *config.AppConfig, registry *tools.Registry, mode ResolutionMode) *BundleResolver {
	return &BundleResolver{
		cfg:         cfg,
		registry:    registry,
		bundleCache: make(map[string][]tools.ToolDefinition),
		mode:        mode,
	}
}

// GetToolDefinitions возвращает определения инструментов для отправки в LLM.
//
// В зависимости от mode:
//   - bundle-first: возвращает bundle definitions
//   - flat: возвращает все tool definitions (backward compatible)
func (br *BundleResolver) GetToolDefinitions() []tools.ToolDefinition {
	if br.mode == ResolutionModeFlat {
		// Backward compatible: все tools сразу
		return br.registry.GetDefinitions()
	}

	// Bundle-first mode: возвращаем bundle definitions
	return br.getBundleDefinitions()
}

// IsBundleCall проверяет, является ли tool name bundle именем.
func (br *BundleResolver) IsBundleCall(toolName string) bool {
	if br.mode == ResolutionModeFlat {
		return false
	}

	// Проверяем есть ли такой bundle в конфиге
	_, exists := br.cfg.ToolBundles[toolName]
	return exists
}

// ExpandBundle расширяет bundle в реальные инструменты.
//
// Возвращает system message с definitions инструментов из bundle.
func (br *BundleResolver) ExpandBundle(bundleName string) (llm.Message, error) {
	// Проверяем что bundle существует
	bundle, exists := br.cfg.ToolBundles[bundleName]
	if !exists {
		return llm.Message{}, fmt.Errorf("bundle '%s' not found", bundleName)
	}

	// Получаем tool definitions из bundle
	toolDefs := br.getToolsFromBundle(bundle)

	// Форматируем как system message
	content := br.formatToolDefinitions(toolDefs, bundleName)

	return llm.Message{
		Role:    llm.RoleSystem,
		Content: content,
	}, nil
}

// getBundleDefinitions возвращает bundle definitions для отправки в LLM.
//
// Bundle definition — это упрощённое описание для LLM:
// {
//   "name": "wb_content_tools",
//   "description": "Wildberries Content API: поиск товаров, категории, бренды, характеристики, ТНВЭД"
// }
func (br *BundleResolver) getBundleDefinitions() []tools.ToolDefinition {
	defs := make([]tools.ToolDefinition, 0, len(br.cfg.ToolBundles))

	for bundleName, bundle := range br.cfg.ToolBundles {
		// Bundle definition без parameters (это meta-tool)
		defs = append(defs, tools.ToolDefinition{
			Name:        bundleName,
			Description: bundle.Description,
			Parameters:  nil, // Bundle не имеет параметров
		})
	}

	return defs
}

// getToolsFromBundle возвращает определения инструментов из bundle.
//
// Кэширует результат для производительности.
func (br *BundleResolver) getToolsFromBundle(bundle config.ToolBundle) []tools.ToolDefinition {
	// Проверяем кэш
	if cached, exists := br.bundleCache[bundle.Description]; exists {
		return cached
	}

	// Получаем definitions из registry
	toolDefs := make([]tools.ToolDefinition, 0, len(bundle.Tools))

	for _, toolName := range bundle.Tools {
		// Получаем tool из registry
		tool, err := br.registry.Get(toolName)
		if err != nil {
			// Tool не найден — пропускаем
			continue
		}

		toolDefs = append(toolDefs, tool.Definition())
	}

	// Кэшируем
	br.bundleCache[bundle.Description] = toolDefs

	return toolDefs
}

// formatToolDefinitions форматирует tool definitions в читаемый для LLM формат.
//
// Пример输出:
//
//	# Tool Definitions from bundle: wb_content_tools
//
//	You now have access to the following tools:
//
//	## search_wb_products
//	Description: Ищет товары Wildberries по артикулам поставщика...
//	Parameters: {"type": "object", "properties": {...}}
//
//	## get_wb_parent_categories
//	Description: Возвращает список родительских категорий...
//	...
func (br *BundleResolver) formatToolDefinitions(toolDefs []tools.ToolDefinition, bundleName string) string {
	content := fmt.Sprintf(`# Tool Definitions from bundle: %s

You now have access to the following tools from this bundle:

`, bundleName)

	for _, def := range toolDefs {
		content += fmt.Sprintf("## %s\n", def.Name)
		content += fmt.Sprintf("Description: %s\n", def.Description)

		// Сериализуем parameters в JSON
		if def.Parameters != nil {
			paramsJSON, err := json.Marshal(def.Parameters)
			if err == nil {
				content += fmt.Sprintf("Parameters: %s\n", string(paramsJSON))
			}
		}

		content += "\n"
	}

	return content
}

// GetResolutionMode возвращает текущий режим резолюции.
func (br *BundleResolver) GetResolutionMode() ResolutionMode {
	return br.mode
}

// HasBundles проверяет, есть ли bundles в конфиге.
func (br *BundleResolver) HasBundles() bool {
	return len(br.cfg.ToolBundles) > 0
}
