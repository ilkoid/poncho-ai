// Package std предоставляет инструменты для аналитики рекламы Wildberries.
//
// Реализует инструменты для анализа рекламных кампаний:
// - Статистика кампаний (показы, клики, заказы)
// - Статистика по ключевым фразам
// - Атрибуция: органика vs реклама
package std

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WbCampaignStatsTool — инструмент для получения статистики рекламных кампаний.
//
// Использует Promotion API: POST /adv/v2/fullstats
// Возвращает показы, клики, CTR, CPC, затраты, заказы.
type WbCampaignStatsTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbCampaignStatsTool создает инструмент для получения статистики кампаний.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbCampaignStatsTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbCampaignStatsTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbCampaignStatsTool{
		client:      c,
		toolID:      "get_wb_campaign_stats",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbCampaignStatsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_campaign_stats",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"advertIds": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{"type": "integer"},
					"description": "Список ID рекламных кампаний",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-90)",
				},
			},
			"required": []string{"advertIds", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbCampaignStatsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		AdvertIds []int `json:"advertIds"`
		Days      int    `json:"days"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Валидация
	if len(args.AdvertIds) == 0 {
		return "", fmt.Errorf("advertIds cannot be empty")
	}
	if args.Days < 1 || args.Days > 90 {
		return "", fmt.Errorf("days must be between 1 and 90")
	}

	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock(args.AdvertIds, args.Days)
	}

	// Формируем даты
	now := time.Now()
	dates := make([]string, 0, args.Days)
	for i := args.Days - 1; i >= 0; i-- {
		dates = append(dates, now.AddDate(0, 0, -i).Format("2006-01-02"))
	}

	// Формируем запрос к WB API
	reqBody := map[string]interface{}{
		" adverIds": args.AdvertIds,
		"dates":     dates,
	}

	var response []struct {
		AdvertID    int     `json:"advertId"`
		Views       int     `json:"views"`
		Clicks      int     `json:"clicks"`
		CTR         float64 `json:"ctr"`
		CPC         int     `json:"cpc"`
		Sum         float64 `json:"sum"`      // Затраты
		Orders      int     `json:"orders"`
		AtmSales    int     `json:"atmSales"` // Продажи в автоматических кампаниях
		ShksSales   int     `json:"shksSales"` // Продажи в кампаниях с ручным управлением
	}

	err := t.client.Post(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		"/adv/v2/fullstats", reqBody, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get campaign stats: %w", err)
	}

	// Форматируем ответ для LLM
	result, _ := json.Marshal(response)
	return string(result), nil
}

// executeMock возвращает mock данные для demo режима.
func (t *WbCampaignStatsTool) executeMock(advertIds []int, days int) (string, error) {
	now := time.Now()

	results := make([]map[string]interface{}, 0, len(advertIds))

	for _, advertID := range advertIds {
		views := 5000 + advertID*100
		clicks := views / 20 // CTR ~5%
		sum := float64(clicks) * 5.0 // CPC ~5 руб

		results = append(results, map[string]interface{}{
			"advertId":  advertID,
			"views":     views,
			"clicks":    clicks,
			"ctr":       float64(clicks) / float64(views) * 100,
			"cpc":       5,
			"sum":       sum,
			"orders":    clicks / 10,
			"atmSales":  clicks / 15,
			"shksSales": clicks / 30,
			"period": map[string]interface{}{
				"begin": now.AddDate(0, 0, -days).Format("2006-01-02"),
				"end":   now.Format("2006-01-02"),
				"days":  days,
			},
			"mock": true,
		})
	}

	result, _ := json.Marshal(results)
	return string(result), nil
}

// WbKeywordStatsTool — инструмент для получения статистики по ключевым фразам.
//
// Использует Promotion API: GET /adv/v0/stats/keywords
// Возвращает статистику по фразам кампании (за 7 дней).
type WbKeywordStatsTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbKeywordStatsTool создает инструмент для получения статистики по фразам.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbKeywordStatsTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbKeywordStatsTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbKeywordStatsTool{
		client:      c,
		toolID:      "get_wb_keyword_stats",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbKeywordStatsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_keyword_stats",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"advertId": map[string]interface{}{
					"type":        "integer",
					"description": "ID рекламной кампании",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-7, максимум 7 дней)",
				},
			},
			"required": []string{"advertId", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbKeywordStatsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		AdvertID int `json:"advertId"`
		Days     int `json:"days"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Валидация
	if args.AdvertID <= 0 {
		return "", fmt.Errorf("advertId must be positive")
	}
	if args.Days < 1 || args.Days > 7 {
		return "", fmt.Errorf("days must be between 1 and 7 (API limit)")
	}

	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock(args.AdvertID, args.Days)
	}

	// Формируем запрос к WB API (GET запрос)
	// Примечание: WB API возвращает данные за последние 7 дней
	var response []struct {
		Keyword      string  `json:"keyword"`
		Views        int     `json:"views"`
		Clicks       int     `json:"clicks"`
		CTR          float64 `json:"ctr"`
		CPC          int     `json:"cpc"`
		Sum          float64 `json:"sum"`
		Orders       int     `json:"orders"`
		Conversions  float64 `json:"conversions"` // Конверсия в заказ
	}

	// Для GET запросов используем метод Get
	err := t.client.Get(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		"/adv/v0/stats/keywords", nil, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get keyword stats: %w", err)
	}

	// Форматируем ответ для LLM
	result, _ := json.Marshal(response)
	return string(result), nil
}

// executeMock возвращает mock данные для demo режима.
func (t *WbKeywordStatsTool) executeMock(advertID int, days int) (string, error) {
	now := time.Now()

	// Mock ключевые слова
	mockKeywords := []string{
		"платье женское", "вечернее платье", "платье черное",
		"платье длинное", "платье летнее", "сарафан",
		"платье коктейльное", "платье офисное", "платье красное",
		"платье с рукавами",
	}

	results := make([]map[string]interface{}, 0, len(mockKeywords))

	for _, kw := range mockKeywords {
		views := 500 + (len(kw) * 10)
		clicks := views / 25
		sum := float64(clicks) * 6.0

		results = append(results, map[string]interface{}{
			"keyword":     kw,
			"views":       views,
			"clicks":      clicks,
			"ctr":         float64(clicks) / float64(views) * 100,
			"cpc":         6,
			"sum":         sum,
			"orders":      clicks / 8,
			"conversions": float64(clicks/8) / float64(clicks) * 100,
			"period": map[string]interface{}{
				"begin": now.AddDate(0, 0, -days).Format("2006-01-02"),
				"end":   now.Format("2006-01-02"),
				"days":  days,
			},
			"mock": true,
		})
	}

	result, _ := json.Marshal(results)
	return string(result), nil
}

// WbAttributionSummaryTool — инструмент для атрибуции органика vs реклама.
//
// Это агрегатор, который объединяет данные из:
// - get_wb_product_funnel (общая статистика)
// - get_wb_campaign_stats (статистика рекламных кампаний)
type WbAttributionSummaryTool struct {
	funnelTool  *WbProductFunnelTool
	campaignTool *WbCampaignStatsTool
	description  string
}

// NewWbAttributionSummaryTool создает инструмент для атрибуции.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbAttributionSummaryTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbAttributionSummaryTool {
	return &WbAttributionSummaryTool{
		funnelTool:   NewWbProductFunnelTool(c, cfg, wbDefaults),
		campaignTool: NewWbCampaignStatsTool(c, cfg, wbDefaults),
		description:  cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbAttributionSummaryTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_attribution_summary",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmIDs": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{"type": "integer"},
					"description": "Список nmID товаров (макс. 100)",
				},
				"advertIds": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{"type": "integer"},
					"description": "Список ID рекламных кампаний для атрибуции",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-90)",
				},
			},
			"required": []string{"nmIDs", "advertIds", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbAttributionSummaryTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmIDs     []int `json:"nmIDs"`
		AdvertIds []int `json:"advertIds"`
		Days      int    `json:"days"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Валидация
	if len(args.NmIDs) == 0 {
		return "", fmt.Errorf("nmIDs cannot be empty")
	}
	if len(args.AdvertIds) == 0 {
		return "", fmt.Errorf("advertIds cannot be empty")
	}
	if args.Days < 1 || args.Days > 90 {
		return "", fmt.Errorf("days must be between 1 and 90")
	}

	// Получаем данные воронки продаж
	funnelArgs, _ := json.Marshal(map[string]interface{}{
		"nmIDs": args.NmIDs,
		"days":  args.Days,
	})
	funnelData, err := t.funnelTool.Execute(ctx, string(funnelArgs))
	if err != nil {
		return "", fmt.Errorf("failed to get funnel data: %w", err)
	}

	var funnelResponse struct {
		Data struct {
			Cards []struct {
				NMID  int `json:"nmID"`
				Statistics struct {
					SelectedPeriod struct {
						OpenCardCount  int `json:"openCardCount"`
						AddToCartCount int `json:"addToCartCount"`
						OrdersCount    int `json:"ordersCount"`
					} `json:"selectedPeriod"`
				} `json:"statistics"`
			} `json:"cards"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(funnelData), &funnelResponse); err != nil {
		return "", fmt.Errorf("failed to parse funnel response: %w", err)
	}

	// Получаем данные рекламных кампаний
	campaignArgs, _ := json.Marshal(map[string]interface{}{
		"advertIds": args.AdvertIds,
		"days":      args.Days,
	})
	campaignData, err := t.campaignTool.Execute(ctx, string(campaignArgs))
	if err != nil {
		// Если не удалось получить данные кампаний, продолжаем без них
		campaignData = "[]"
	}

	var campaignResponse []struct {
		AdvertID int     `json:"advertId"`
		Views    int     `json:"views"`
		Clicks   int     `json:"clicks"`
		Sum      float64 `json:"sum"`
		Orders   int     `json:"orders"`
	}

	json.Unmarshal([]byte(campaignData), &campaignResponse)

	// Агрегируем данные по товарам
	now := time.Now()
	begin := now.AddDate(0, 0, -args.Days)

	results := make([]map[string]interface{}, 0, len(args.NmIDs))

	// Считаем общие показатели по рекламе
	var totalAdViews, totalAdOrders int
	var totalAdSpent float64
	campaignMap := make(map[int]int) // advertID → orders

	for _, c := range campaignResponse {
		totalAdViews += c.Views
		totalAdOrders += c.Orders
		totalAdSpent += c.Sum
		campaignMap[c.AdvertID] += c.Orders
	}

	// Формируем результат по каждому товару
	for _, card := range funnelResponse.Data.Cards {
		totalViews := card.Statistics.SelectedPeriod.OpenCardCount
		totalOrders := card.Statistics.SelectedPeriod.OrdersCount

		// Предполагаем что все рекламные просмотры относятся к этим товарам
		// Это упрощение - в реальности нужно точное распределение
		organicViews := totalViews - totalAdViews
		if organicViews < 0 {
			organicViews = 0
		}

		organicOrders := totalOrders - totalAdOrders
		if organicOrders < 0 {
			organicOrders = 0
		}

		// Формируем распределение по кампаниям
		byCampaign := make([]map[string]interface{}, 0, len(campaignResponse))
		for _, c := range campaignResponse {
			byCampaign = append(byCampaign, map[string]interface{}{
				"advertId": c.AdvertID,
				"orders":   campaignMap[c.AdvertID],
				"spent":    c.Sum,
			})
		}

		results = append(results, map[string]interface{}{
			"nmID": card.NMID,
			"period": map[string]string{
				"begin": begin.Format("2006-01-02"),
				"end":   now.Format("2006-01-02"),
			},
			"summary": map[string]interface{}{
				"totalViews":   totalViews,
				"organicViews": organicViews,
				"adViews":      totalAdViews,
				"totalOrders":  totalOrders,
				"organicOrders": organicOrders,
				"adOrders":     totalAdOrders,
			},
			"byCampaign": byCampaign,
		})
	}

	data, _ := json.Marshal(results)
	return string(data), nil
}
