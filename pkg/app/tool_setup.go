package app

import (
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/models"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/todo"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/tools/std"
	"github.com/ilkoid/poncho-ai/pkg/utils"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// SetupToolsFromConfig регистрирует инструменты на основе YAML конфигурации.
//
// OCP Principle: Открыт для расширения через конфигурацию, закрыт для изменения.
// Добавление новой категории (Ozon, Yandex) требует только:
// 1. Добавить секцию в config.yaml
// 2. Добавить клиента в clients map
// 3. Добавить case в registerTool()
//
// FALLBACK: Если tool_categories пустой, использует legacy tools секцию для backward compatibility.
//
// Rule 3: Все инструменты регистрируются через Registry.Register().
// Rule 6: pkg/app может импортировать бизнес-логику (это app-specific слой).
func SetupToolsFromConfig(
	st *state.CoreState,
	cfg *config.AppConfig,
	clients map[string]any,
) error {
	registry := st.GetToolsRegistry()

	// Fallback: если tool_categories пустой, используем legacy подход
	if len(cfg.ToolCategories) == 0 {
		utils.Debug("tool_categories empty, using legacy tools registration")
		return setupToolsFromLegacy(st, cfg, clients)
	}

	// OCP approach: tool categories
	for categoryName, categoryCfg := range cfg.ToolCategories {
		if !categoryCfg.Enabled {
			utils.Debug("Tool category disabled, skipping", "category", categoryName)
			continue
		}

		// Get client dependency
		var client any
		if categoryCfg.Client != "" {
			var ok bool
			client, ok = clients[categoryCfg.Client]
			if !ok || client == nil {
				utils.Warn("Tool category client not found, skipping",
					"category", categoryName,
					"client", categoryCfg.Client)
				continue
			}
		}

		// Register each tool in category
		for _, toolName := range categoryCfg.Tools {
			if err := registerTool(toolName, registry, cfg, st, client); err != nil {
				return fmt.Errorf("category %s, tool %s: %w", categoryName, toolName, err)
			}
		}
	}

	return nil
}

// setupToolsFromLegacy регистрирует инструменты из legacy tools секции.
//
// Fallback функция для обратной совместимости с конфигами без tool_categories.
func setupToolsFromLegacy(
	st *state.CoreState,
	cfg *config.AppConfig,
	clients map[string]any,
) error {
	registry := st.GetToolsRegistry()

	// Собираем все включенные инструменты
	var enabledTools []string
	for toolName, toolCfg := range cfg.Tools {
		if toolCfg.Enabled {
			enabledTools = append(enabledTools, toolName)
		}
	}

	utils.Info("Registering tools from legacy config", "count", len(enabledTools))

	// Регистрируем каждый инструмент через registerTool
	for _, toolName := range enabledTools {
		// Определяем клиента по имени инструмента (legacy mapping)
		var client any
		switch {
		case isWBTool(toolName):
			client = clients["wb_client"]
		case isS3Tool(toolName):
			client = clients["s3_client"]
		case isLLMTool(toolName):
			client = clients["model_registry"]
		case isTodoTool(toolName):
			client = clients["todo_manager"]
		case isDictionaryTool(toolName):
			client = nil // tools используют state напрямую
		default:
			// Для неизвестных инструментов пробуем без клиента
			client = nil
		}

		if err := registerTool(toolName, registry, cfg, st, client); err != nil {
			// Логируем ошибку, но продолжаем регистрацию других инструментов
			utils.Warn("Failed to register tool", "name", toolName, "error", err)
		}
	}

	return nil
}

// isWBTool проверяет, что инструмент относится к WB API
func isWBTool(name string) bool {
	wbTools := []string{
		"search_wb_products", "list_wb_seller_products", "get_wb_parent_categories", "get_wb_subjects",
		"ping_wb_api", "get_wb_feedbacks", "get_wb_questions",
		"get_wb_new_feedbacks_questions", "get_wb_unanswered_feedbacks_counts",
		"get_wb_unanswered_questions_counts", "get_wb_subjects_by_name",
		"get_wb_characteristics", "get_wb_tnved", "get_wb_brands",
		"reload_wb_dictionaries", "get_wb_product_funnel",
		"get_wb_product_funnel_history", "get_wb_search_positions",
		"get_wb_top_search_queries", "get_wb_top_organic_positions",
		"get_wb_campaign_stats", "get_wb_keyword_stats", "get_wb_attribution_summary",
	}
	for _, t := range wbTools {
		if name == t {
			return true
		}
	}
	return false
}

// isS3Tool проверяет, что инструмент относится к S3 хранилищу
func isS3Tool(name string) bool {
	s3Tools := []string{
		"list_s3_files", "read_s3_object", "read_s3_image",
		"get_plm_data", "download_s3_files",
		"classify_and_download_s3_files", "analyze_article_images_batch",
	}
	for _, t := range s3Tools {
		if name == t {
			return true
		}
	}
	return false
}

// isLLMTool проверяет, что инструмент относится к LLM провайдерам
func isLLMTool(name string) bool {
	return name == "ping_llm_provider" || name == "ask_user_question"
}

// isTodoTool проверяет, что инструмент относится к планировщику задач
func isTodoTool(name string) bool {
	todoTools := []string{
		"plan_add_task", "plan_mark_done", "plan_mark_failed",
		"plan_clear", "plan_set_tasks",
	}
	for _, t := range todoTools {
		if name == t {
			return true
		}
	}
	return false
}

// isDictionaryTool проверяет, что инструмент относится к словарям
func isDictionaryTool(name string) bool {
	dictTools := []string{
		"wb_colors", "wb_countries", "wb_genders", "wb_seasons", "wb_vat_rates",
	}
	for _, t := range dictTools {
		if name == t {
			return true
		}
	}
	return false
}

// registerTool регистрирует отдельный инструмент через factory pattern.
//
// Factory switch позволяет добавлять новые инструменты без создания новых функций.
// Для добавления нового инструмента добавьте case сюда.
func registerTool(
	name string,
	registry *tools.Registry,
	cfg *config.AppConfig,
	st *state.CoreState,
	client any,
) error {
	toolCfg, exists := cfg.Tools[name]
	if !exists {
		return fmt.Errorf("tool config not found")
	}

	if !toolCfg.Enabled {
		return nil
	}

	var tool tools.Tool

	// Factory switch: создаём инструмент по имени
	switch name {
	// ========================================
	// WB Content API Tools
	// ========================================
	case "search_wb_products":
		tool = std.NewWbProductSearchTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "list_wb_seller_products":
		tool = std.NewWbSellerProductsTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "get_wb_parent_categories":
		tool = std.NewWbParentCategoriesTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "get_wb_subjects":
		tool = std.NewWbSubjectsTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "ping_wb_api":
		tool = std.NewWbPingTool(client.(*wb.Client), toolCfg, cfg.WB)

	// ========================================
	// WB Feedbacks API Tools
	// ========================================
	case "get_wb_feedbacks":
		tool = std.NewWbFeedbacksTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "get_wb_questions":
		tool = std.NewWbQuestionsTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "get_wb_new_feedbacks_questions":
		tool = std.NewWbNewFeedbacksQuestionsTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "get_wb_unanswered_feedbacks_counts":
		tool = std.NewWbUnansweredFeedbacksCountsTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "get_wb_unanswered_questions_counts":
		tool = std.NewWbUnansweredQuestionsCountsTool(client.(*wb.Client), toolCfg, cfg.WB)

	// ========================================
	// WB Characteristics Tools
	// ========================================
	case "get_wb_subjects_by_name":
		tool = std.NewWbSubjectsByNameTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "get_wb_characteristics":
		tool = std.NewWbCharacteristicsTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "get_wb_tnved":
		tool = std.NewWbTnvedTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "get_wb_brands":
		tool = std.NewWbBrandsTool(client.(*wb.Client), toolCfg, cfg.WB)

	// ========================================
	// WB Service Tools
	// ========================================
	case "reload_wb_dictionaries":
		tool = std.NewReloadWbDictionariesTool(client.(*wb.Client), toolCfg)

	// ========================================
	// WB Analytics Tools
	// ========================================
	case "get_wb_product_funnel":
		tool = std.NewWbProductFunnelTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "get_wb_product_funnel_history":
		tool = std.NewWbProductFunnelHistoryTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "get_wb_search_positions":
		tool = std.NewWbSearchPositionsTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "get_wb_top_search_queries":
		tool = std.NewWbTopSearchQueriesTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "get_wb_top_organic_positions":
		tool = std.NewWbTopOrganicPositionsTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "get_wb_campaign_stats":
		tool = std.NewWbCampaignStatsTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "get_wb_keyword_stats":
		tool = std.NewWbKeywordStatsTool(client.(*wb.Client), toolCfg, cfg.WB)
	case "get_wb_attribution_summary":
		tool = std.NewWbAttributionSummaryTool(client.(*wb.Client), toolCfg, cfg.WB)

	// ========================================
	// Dictionary Tools (клиент не нужен, используем state)
	// ========================================
	case "wb_colors":
		dicts := st.GetDictionaries()
		tool = std.NewWbColorsTool(dicts, toolCfg)
	case "wb_countries":
		dicts := st.GetDictionaries()
		tool = std.NewWbCountriesTool(dicts, toolCfg)
	case "wb_genders":
		dicts := st.GetDictionaries()
		tool = std.NewWbGendersTool(dicts, toolCfg)
	case "wb_seasons":
		dicts := st.GetDictionaries()
		tool = std.NewWbSeasonsTool(dicts, toolCfg)
	case "wb_vat_rates":
		dicts := st.GetDictionaries()
		tool = std.NewWbVatRatesTool(dicts, toolCfg)

	// ========================================
	// LLM Tools
	// ========================================
	case "ping_llm_provider":
		modelRegistry, ok := client.(*models.Registry)
		if !ok {
			return fmt.Errorf("ping_llm_provider requires model_registry client")
		}
		tool = std.NewLLMPingTool(modelRegistry, cfg, toolCfg)
	case "ask_user_question":
		// TODO: requires QuestionManager - skip for now
		return nil

	// ========================================
	// S3 Tools
	// ========================================
	case "list_s3_files":
		tool = std.NewS3ListTool(client.(*s3storage.Client))
	case "read_s3_object":
		tool = std.NewS3ReadTool(client.(*s3storage.Client))
	case "read_s3_image":
		tool = std.NewS3ReadImageTool(client.(*s3storage.Client), cfg.ImageProcessing)
	case "get_plm_data":
		tool = std.NewGetPLMDataTool(client.(*s3storage.Client))
	case "download_s3_files":
		tool = std.NewDownloadS3FilesTool(client.(*s3storage.Client))
	case "classify_and_download_s3_files":
		tool = std.NewClassifyAndDownloadS3Files(
			client.(*s3storage.Client),
			st,
			cfg.ImageProcessing,
			cfg.FileRules,
			toolCfg,
		)
	case "analyze_article_images_batch":
		// Note: requires special handling (vision LLM)
		// Handled separately in setupVisionToolsFromConfig
		return nil

	// ========================================
	// Todo/Planner Tools (клиент не нужен, используем tm)
	// ========================================
	case "plan_add_task":
		tool = std.NewPlanAddTaskTool(client.(*todo.Manager), toolCfg)
	case "plan_mark_done":
		tool = std.NewPlanMarkDoneTool(client.(*todo.Manager), toolCfg)
	case "plan_mark_failed":
		tool = std.NewPlanMarkFailedTool(client.(*todo.Manager), toolCfg)
	case "plan_clear":
		tool = std.NewPlanClearTool(client.(*todo.Manager), toolCfg)
	case "plan_set_tasks":
		tool = std.NewPlanSetTasksTool(client.(*todo.Manager), toolCfg)

	default:
		return fmt.Errorf("unknown tool '%s'", name)
	}

	// Register tool
	if err := registry.Register(tool); err != nil {
		return fmt.Errorf("failed to register tool '%s': %w", name, err)
	}

	utils.Debug("Tool registered", "name", name)
	return nil
}
