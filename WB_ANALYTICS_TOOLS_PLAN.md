# Plan: WB Product Analytics Tools

## Цель
Создать инструменты для анализа эффективности товаров на Wildberries:
- Просмотры за период
- Переходы в карточку
- Разделение органика/рекламы
- Средняя позиция в выдаче
- Топ-10 мест по органике по запросам
- Конверсия в корзину и заказ

## Требования пользователя
- **Scope**: Только свои товары (nmID)
- **Период**: Последняя неделя
- **Атрибуция**: Да, к конкретным рекламным кампаниям

---

## Доступные WB API Endpoints

### 1. Воронка продаж (Sales Funnel)
| Endpoint | Base URL | Описание |
|----------|----------|----------|
| `POST /api/v2/nm-report/detail` | `https://seller-analytics-api.wildberries.ru` | Статистика карточек за период (до 365 дней) |
| `POST /api/v2/nm-report/detail/history` | `https://seller-analytics-api.wildberries.ru` | История по дням (до 7 дней бесплатно, до года с Джем) |

**Ответ включает**:
- `openCardCount` — просмотры карточки
- `addToCartCount` — добавления в корзину
- `ordersCount` — заказы
- `conversions.addToCartPercent` — конверсия просмотр→корзина
- `conversions.cartToOrderPercent` — конверсия корзина→заказ

### 2. Поисковые запросы и позиции
| Endpoint | Base URL | Описание |
|----------|----------|----------|
| `POST /api/v2/search-report/report` | `https://seller-analytics-api.wildberries.ru` | Основной отчёт (позиции, видимость) |
| `POST /api/v2/search-report/product/search-texts` | `https://seller-analytics-api.wildberries.ru` | Топ поисковых фраз по товару |
| `POST /api/v2/search-report/product/orders` | `https://seller-analytics-api.wildberries.ru` | Заказы и позиции по запросам |

**Ответ включает**:
- `avgPosition.current` — средняя позиция
- `clusters.firstHundred` — товары в топ-100
- `openCard.current` — переходы из поиска
- `orders.current` — заказы из поиска

### 3. Статистика рекламных кампаний
| Endpoint | Base URL | Описание |
|----------|----------|----------|
| `POST /adv/v2/fullstats` | `https://advert-api.wildberries.ru` | Статистика всех кампаний |
| `GET /adv/v0/stats/keywords` | `https://advert-api.wildberries.ru` | Статистика по ключевым фразам (7 дней) |

**Ответ включает**:
- `views` — показы
- `clicks` — клики
- `ctr` — CTR
- `cpc` — стоимость клика
- `sum` — затраты
- `orders` — заказы

---

## План реализации

### Phase 1: Создать файлы для WB Analytics Tools

```
pkg/tools/std/
├── wb_analytics.go          # Основные инструменты аналитики
├── wb_search_analytics.go   # Поисковые запросы и позиции
└── wb_ad_analytics.go       # Статистика рекламных кампаний
```

### Phase 2: Реализовать инструменты

#### 2.1. Воронка продаж (`wb_analytics.go`)

| Tool | Endpoint | Параметры | Возвращает |
|------|----------|-----------|------------|
| `get_wb_product_funnel` | `/api/v2/nm-report/detail` | `nmIDs[]`, `period{begin,end}` | Просмотры, корзина, заказы, конверсии |
| `get_wb_product_funnel_history` | `/api/v2/nm-report/detail/history` | `nmIDs[]`, `period{begin,end}` | История по дням |

**Структура ответа**:
```json
{
  "nmID": 1234567,
  "period": {"begin": "2025-01-05", "end": "2025-01-12"},
  "funnel": {
    "views": 1000,
    "addToCart": 150,
    "orders": 30,
    "conversions": {
      "toCartPercent": 15.0,
      "toOrderPercent": 20.0
    }
  }
}
```

#### 2.2. Поисковые запросы (`wb_search_analytics.go`)

| Tool | Endpoint | Параметры | Возвращает |
|------|----------|-----------|------------|
| `get_wb_search_positions` | `/api/v2/search-report/report` | `nmIds[]`, `currentPeriod`, `pastPeriod` | Средняя позиция, видимость |
| `get_wb_top_search_queries` | `/api/v2/search-report/product/search-texts` | `nmIds[]`, `period`, `topOrderBy`, `limit` | Топ запросов (до 30) |
| `get_wb_top_organic_positions` | *(расчёт)* | данные из `get_wb_top_search_queries` | Топ-10 позиций по запросам |

**Структура ответа для топ-10**:
```json
{
  "nmID": 1234567,
  "topPositions": [
    {"query": "платье черное", "position": 3, "orders": 10},
    {"query": "вечернее платье", "position": 7, "orders": 5}
  ]
}
```

#### 2.3. Рекламные кампании (`wb_ad_analytics.go`)

| Tool | Endpoint | Параметры | Возвращает |
|------|----------|-----------|------------|
| `get_wb_campaign_stats` | `/adv/v2/fullstats` | `advertId`, `dates[]` | Показы, клики, CTR, CPC, затраты, заказы |
| `get_wb_keyword_stats` | `/adv/v0/stats/keywords` | `advert_id`, `from`, `to` | Статистика по фразам |
| `get_wb_attribution_summary` | *(агрегатор)* | данные из выше + воронка | Органика vs Реклама |

**Структура ответа для атрибуции**:
```json
{
  "nmID": 1234567,
  "period": {"begin": "2025-01-05", "end": "2025-01-12"},
  "summary": {
    "totalViews": 5000,
    "organicViews": 3500,
    "adViews": 1500,
    "totalOrders": 50,
    "organicOrders": 35,
    "adOrders": 15
  },
  "byCampaign": [
    {"advertId": 123, "views": 1000, "orders": 10, "spent": 500}
  ]
}
```

### Phase 3: Обновить конфигурацию

Добавить в `config.yaml`:
```yaml
tools:
  get_wb_product_funnel:
    enabled: true
    description: "Воронка продаж по товарам за период (просмотры→корзина→заказ)"
    endpoint: "https://seller-analytics-api.wildberries.ru"
    rate_limit: 180  # 3 req/min (лимит WB)
    burst: 3

  get_wb_product_funnel_history:
    enabled: true
    description: "История воронки по дням (до 7 дней бесплатно)"
    endpoint: "https://seller-analytics-api.wildberries.ru"
    rate_limit: 180
    burst: 3

  get_wb_search_positions:
    enabled: true
    description: "Позиции товаров в поиске и видимость"
    endpoint: "https://seller-analytics-api.wildberries.ru"
    rate_limit: 180
    burst: 3

  get_wb_top_search_queries:
    enabled: true
    description: "Топ поисковых запросов по товару (до 30 фраз)"
    endpoint: "https://seller-analytics-api.wildberries.ru"
    rate_limit: 180
    burst: 3

  get_wb_top_organic_positions:
    enabled: true
    description: "Топ-10 позиций в органическом поиске по запросам"
    # Calculated from search data

  get_wb_campaign_stats:
    enabled: true
    description: "Статистика рекламных кампаний (показы, клики, заказы)"
    endpoint: "https://advert-api.wildberries.ru"
    rate_limit: 60
    burst: 1

  get_wb_keyword_stats:
    enabled: true
    description: "Статистика по ключевым фразам кампании"
    endpoint: "https://advert-api.wildberries.ru"
    rate_limit: 240  # 4 req/sec
    burst: 4

  get_wb_attribution_summary:
    enabled: true
    description: "Атрибуция заказов: органика vs реклама по товару"
    # Aggregates data from multiple sources
```

### Phase 4: Обновить регистрацию инструментов

Добавить в `pkg/app/components.go`:
```go
wbAnalyticsTools := []string{
    "get_wb_product_funnel",
    "get_wb_product_funnel_history",
    "get_wb_search_positions",
    "get_wb_top_search_queries",
    "get_wb_top_organic_positions",
    "get_wb_campaign_stats",
    "get_wb_keyword_stats",
    "get_wb_attribution_summary",
}
```

---

## Структура кода (шаблон)

```go
// WbProductFunnelTool — инструмент для получения воронки продаж.
type WbProductFunnelTool struct {
    client      *wb.Client
    toolID      string
    endpoint    string
    rateLimit   int
    burst       int
    description string
}

func NewWbProductFunnelTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbProductFunnelTool {
    endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)
    return &WbProductFunnelTool{
        client:      c,
        toolID:      "get_wb_product_funnel",
        endpoint:    endpoint,
        rateLimit:   rateLimit,
        burst:       burst,
        description: cfg.Description,
    }
}

func (t *WbProductFunnelTool) Definition() tools.ToolDefinition {
    return tools.ToolDefinition{
        Name:        "get_wb_product_funnel",
        Description: t.description,
        Parameters: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "nmIDs": map[string]interface{}{
                    "type": "array",
                    "items": map[string]interface{}{"type": "integer"},
                    "description": "Список nmID товаров (макс. 100)",
                },
                "days": map[string]interface{}{
                    "type": "integer",
                    "description": "Количество дней (1-365, для истории по дням: 1-7)",
                },
            },
            "required": []string{"nmIDs", "days"},
        },
    }
}

func (t *WbProductFunnelTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    var args struct {
        NmIDs []int `json:"nmIDs"`
        Days  int    `json:"days"`
    }
    if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
        return "", fmt.Errorf("invalid arguments: %w", err)
    }

    // Формируем запрос к WB API
    reqBody := map[string]interface{}{
        "nmIDs": args.NmIDs,
        "period": map[string]string{
            "begin": fmt.Sprintf("%s", time.Now().AddDate(0, 0, -args.Days).Format("2006-01-02 15:04:05"),
            "end":   fmt.Sprintf("%s", time.Now().Format("2006-01-02 15:04:05")),
        },
        "page": 1,
    }

    var response struct {
        Data struct {
            Cards []struct {
                NMID       int `json:"nmID"`
                Statistics struct {
                    SelectedPeriod struct {
                        OpenCardCount  int  `json:"openCardCount"`
                        AddToCartCount int  `json:"addToCartCount"`
                        OrdersCount    int  `json:"ordersCount"`
                        Conversions    struct {
                            AddToCartPercent float64 `json:"addToCartPercent"`
                            CartToOrderPercent float64 `json:"cartToOrderPercent"`
                        } `json:"conversions"`
                    } `json:"selectedPeriod"`
                } `json:"statistics"`
            } `json:"cards"`
        } `json:"data"`
    }

    err := t.client.Post(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst, "/api/v2/nm-report/detail", reqBody, &response)
    if err != nil {
        return "", fmt.Errorf("failed to get product funnel: %w", err)
    }

    // Форматируем ответ для LLM
    result, _ := json.Marshal(response.Data.Cards)
    return string(result), nil
}
```

---

## Проверка

### Тестирование через CLI
```bash
cd cmd/tools-test && go run main.go

# Или создать专门的 тестовый CLI:
cd cmd/wb-analytics-test && go run main.go
```

### Проверка инструментов
1. `get_wb_product_funnel` — воронка за неделю
2. `get_wb_top_organic_positions` — топ-10 позиций
3. `get_wb_attribution_summary` — органика vs реклама
4. `get_wb_campaign_stats` — статистика кампании

---

## Критические файлы для изменения

| Файл | Действие |
|------|----------|
| `pkg/tools/std/wb_analytics.go` | Создать |
| `pkg/tools/std/wb_search_analytics.go` | Создать |
| `pkg/tools/std/wb_ad_analytics.go` | Создать |
| `config.yaml` | Обновить |
| `pkg/app/components.go` | Обновить регистрацию |

---

## Примечания

1. **API Ключи**: Нужны токены для категорий "Статистика" и "Продвижение"
2. **Rate Limits**: Строго соблюдать лимиты WB API
3. **Ограничения**:
   - История по дням — до 7 дней бесплатно
   - До 365 дней с подпиской Джем
4. **Mock режим**: Поддерживать demo_key для тестирования
