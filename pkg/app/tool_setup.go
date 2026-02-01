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
// Rule 3: Все инструменты регистрируются через Registry.Register().
// Rule 6: pkg/app может импортировать бизнес-логику (это app-specific слой).
func SetupToolsFromConfig(
	st *state.CoreState,
	cfg *config.AppConfig,
	clients map[string]any,
) error {
	registry := st.GetToolsRegistry()

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
