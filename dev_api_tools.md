# dev_api_tools.md — Guide: WB API Tools через Service Layer

**Дата**: 2026-05-31
**Статус**: Актуальный (на основе опыта ProductService + WbCardContentTool)
**Приоритет**: Доменный — побеждает `dev_v2_downloader.md` и `dev_best_practices.md` при конфликте по вопросам WB API tools

**Связанные документы:**
- [dev_v2_postgres.md](dev_v2_postgres.md) — Writer-интерфейсы для downloader (dual-backend)
- [dev_manifest.md](dev_manifest.md) — Rule 1 (Tool Interface), Rule 6 (Port & Adapter)
- [dev_best_practices.md](dev_best_practices.md) — общие паттерны, Rule 0: Code Reuse First
- [CLAUDE.md](CLAUDE.md) — WB API, Tool interface, команды

---

## Принцип

Этот документ — **практическое руководство** создания AI-инструментов, работающих с WB API, через Service Layer. Описывает паттерн `Client → Service → Tool` и его связь с параллельным паттерном `Client → Source → Downloader`.

**Ключевая идея:** два параллельных abstraction layer поверх `*wb.Client`:
- **Source** (downloader) — пакетная загрузка, пагинация, cursor, bulk write
- **Service** (tool) — точечные запросы, валидация, mock, transformation для LLM

Они НЕ конкурируют — они обслуживают разные use cases (ISP).

---

## Architecture Overview

```
                     *wb.Client (HTTP, retry, rate-limit)
                           │
              ┌────────────┴────────────┐
              │                         │
    Source Layer (downloaders)    Service Layer (tools)
    pkg/<domain>/source.go       pkg/wb/service_<domain>.go
    CardsSource.GetCardsPage()   productService.GetCardsByVendorCodes()
    Bulk, paginated, cursor      Targeted, validated, mock
              │                         │
    pkg/<domain>/downloader.go   pkg/tools/std/wb_<tool>.go
    Writer interface              Tool interface (Raw In, String Out)
              │                         │
    SQLite / PostgreSQL           LLM (JSON string → JSON string)
```

### Data Flow для Tool

```
Пользователь → "что по контенту ART001?"
    ↓
LLM → вызывает tool get_card_content(vendor_codes=["ART001"])
    ↓
Tool.Execute(argsJSON)
    ↓ parse JSON
    ↓ service.GetCardsByVendorCodes(ctx, ["ART001"])
        ↓ validation (1-10 codes)
        ↓ mock check (IsDemoKey?)
        ↓ client.GetCardsList(TextSearch="ART001")  ← Content API
        ↓ client-side exact match filter
        ↓ return []ProductCard
    ↓ marshal → JSON string
    ↓
LLM ← получает JSON с карточками
LLM → формирует ответ пользователю
```

---

## 1. Паттерны (что делать)

### 1.1. Трёхслойная Архитектура: Client → Service → Tool

Каждый слой имеет чёткую ответственность:

| Слой | Файл | Ответственность |
|------|------|-----------------|
| **Client** | `pkg/wb/client.go`, `content.go` | HTTP, retry, rate-limit, response unwrapping |
| **Service** | `pkg/wb/service_<domain>.go` | Validation, mock, API orchestration, transformation |
| **Tool** | `pkg/tools/std/wb_<tool>.go` | Parse args, call service, marshal response |

**Правило:** Tool НЕ знает про HTTP. Service НЕ знает про LLM. Client НЕ знает про бизнес-логику.

### 1.2. Service Method: Validation → Mock → API → Transform

Каждый метод service следует стандартному паттерну (4 шага):

```go
// pkg/wb/service_products.go — canonical example
func (s *productService) GetCardsByVendorCodes(ctx context.Context, codes []string) ([]ProductCard, error) {
    // 1. VALIDATION — bounds checking
    if len(codes) == 0 {
        return nil, fmt.Errorf("vendor codes cannot be empty")
    }
    if len(codes) > 10 {
        return nil, fmt.Errorf("maximum 10 vendor codes allowed per request")
    }

    // 2. MOCK MODE — early return for demo key
    if s.client.IsDemoKey() {
        return s.getMockCardsByVendorCodes(codes)
    }

    // 3. API CALL — orchestrate one or more client calls
    var results []ProductCard
    for _, code := range codes {
        settings := CardsSettings{
            Cursor: CardsCursor{Limit: 100},
            Filter: &CardsFilter{TextSearch: code},
        }
        cards, cursor, err := s.client.GetCardsList(ctx, settings, 100, 5)
        if err != nil {
            return nil, fmt.Errorf("search cards for '%s': %w", code, err)
        }
        // Client-side exact match (TextSearch may return partials)
        for _, card := range cards {
            if card.VendorCode == code {
                results = append(results, card)
            }
        }
        // ... pagination ...
    }

    // 4. TRANSFORM — (in this case, already ProductCard, no transform needed)
    return results, nil
}
```

### 1.3. Tool: Parse → Service → Marshal

Tool — тонкая обёртка. Три шага, не больше:

```go
// pkg/tools/std/wb_card_content.go
func (t *WbCardContentTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    // 1. Parse
    var args struct {
        VendorCodes []string `json:"vendor_codes"`
        NmIDs       []int    `json:"nm_ids"`
    }
    if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
        return "", fmt.Errorf("invalid arguments: %w", err)
    }

    // 2. Call service (validation inside service)
    cards, err := t.service.GetCardsByVendorCodes(ctx, args.VendorCodes)
    if err != nil {
        return "", fmt.Errorf("search by vendor codes: %w", err)
    }

    // 3. Marshal
    result, err := json.Marshal(cards)
    if err != nil {
        return "", fmt.Errorf("marshal response: %w", err)
    }
    return string(result), nil
}
```

### 1.4. WbService Facade — Extraction Суб-сервисов

`WbService` — composite facade, возвращает sub-services по домену:

```go
// Регистрация tool — извлекаем нужный sub-service
case "get_card_content":
    wbSvc := client.(wb.WbService)
    tool = std.NewWbCardContentTool(wbSvc.Products(), toolCfg)
```

**Почему не god-interface:** каждый tool получает только нужный ему sub-service.
`Products()`, `Sales()`, `Advertising()`, `Feedbacks()`, `Attribution()` — ISP в действии.

### 1.5. Mock Mode через client.IsDemoKey() в Service

Mock режим проверяется в service, не в tool:

```go
// ✅ Правильно: mock в service
if s.client.IsDemoKey() {
    return s.getMockCardsByVendorCodes(codes)
}
```

Mock data — детерминированный, использует modulo для variety:

```go
func makeMockCard(nmID int, vendorCode string) ProductCard {
    return ProductCard{
        SubjectID: 1 + (nmID % 50),  // variety через modulo
        Photos: []ProductPhoto{
            {Big: fmt.Sprintf("https://mock.wb.ru/photo/%d/big.jpg", nmID)},
        },
        Characteristics: []CardCharacteristic{
            {ID: 100 + (nmID % 5), Name: "Цвет", ValueRaw: json.RawMessage(`["синий"]`)},
        },
    }
}
```

### 1.6. Параллельные Пути: Source vs Service

Два abstraction layer поверх одного `*wb.Client`:

| | Source (downloader) | Service (tool) |
|---|---|---|
| **Интерфейс** | `CardsSource.GetCardsPage()` | `productService.GetCardsByVendorCodes()` |
| **Файл** | `pkg/cards/source.go` | `pkg/wb/service_products.go` |
| **Use case** | Пакетная загрузка 30k+ карточек | Точечный запрос 1-10 карточек |
| **Пагинация** | Cursor, полный обход | TextSearch или early-exit |
| **Валидация** | Минимальная (limit, cursor) | Bounds, empty checks |
| **Mock** | MockSource (отдельный тип) | client.IsDemoKey() внутри |
| **Вывод** | Writer interface (DB) | []ProductCard (для LLM) |

**Правило:** НЕ объединять Source и Service в один interface. Разные use cases = разные interfaces (ISP).

### 1.7. Validation Patterns

| Паттерн | Пример |
|---------|--------|
| Empty check | `if len(codes) == 0` |
| Bounds (array) | `if len(codes) > 10` |
| Bounds (scalar) | `if req.Period < 1 \|\| req.Period > 365` |
| Date format | `_, err := time.Parse("2006-01-02", date)` |
| Default value | `if req.Take <= 0 \|\| req.Take > 100 { req.Take = 100 }` |
| Positive ID | `if nmID <= 0` |

### 1.8. Pointer Fields для Optional Параметров

```go
// Optional фильтры — pointer, nil = "не задан"
type FeedbacksRequest struct {
    Take       int
    Noffset    int
    IsAnswered *bool  // nil = all, true = answered, false = unanswered
    NmID       int    // 0 = all
}
```

### 1.9. Client-Side Фильтрация для API Limitations

WB API не всегда поддерживает нужные фильтры. Решение — server-side что можем, client-side остальное:

```go
// TextSearch — server-side (API поддерживает)
settings := CardsSettings{Filter: &CardsFilter{TextSearch: code}}

// Exact match — client-side (TextSearch может вернуть частичные совпадения)
for _, card := range cards {
    if card.VendorCode == code {  // exact match
        results = append(results, card)
    }
}

// nmID filter — client-side (API не поддерживает фильтр по nmID)
// Paginate + early exit через map lookup
wanted := make(map[int]bool, len(nmIDs))
for _, id := range nmIDs { wanted[id] = true }
// ... iterate pages, delete from wanted when found ...
if len(wanted) == 0 { break }  // early exit
```

---

## 2. Антипаттерны (чего избегать)

### 2.1. ❌ Mock Data внутри Tool

```go
// ❌ НЕ ДЕЛАТЬ: mock логика в tool
func (t *WbSellerProductsTool) Execute(ctx, argsJSON) (string, error) {
    if t.isMock {
        return t.executeMock(...)  // 100 строк hardcoded данных
    }
    // ... real logic ...
}

// ✅ Правильно: mock в service layer
func (s *productService) GetCardsByVendorCodes(ctx, codes) ([]ProductCard, error) {
    if s.client.IsDemoKey() {
        return s.getMockCardsByVendorCodes(codes)  // единая точка mock
    }
    // ... API call ...
}
```

**Почему:** mock в tool = дублирование, нет переиспользования, сложно тестировать service отдельно.

### 2.2. ❌ Прямой вызов *wb.Client из Tool

```go
// ❌ НЕ ДЕЛАТЬ: tool вызывает Client напрямую
func (t *Tool) Execute(ctx, argsJSON) (string, error) {
    resp, err := t.client.Post(ctx, "tool_id", baseURL, 3, 3, "/api/v3/...", body, &response)
}

// ✅ Правильно: tool вызывает service, service вызывает client
cards, err := t.service.GetCardsByVendorCodes(ctx, codes)
```

**Почему:** tool пропускает validation, mock, transformation. Service = единственная точка входа.

### 2.3. ❌ Stub-Tools Возвращающие Фейковые Ошибки

```go
// ❌ НЕ ДЕЛАТЬ: stub tool
func (t *WbProductSearchTool) Execute(...) (string, error) {
    stub := map[string]interface{}{
        "error": "not_implemented",
        "message": "search tool is not implemented yet",
    }
    result, _ := json.Marshal(stub)
    return string(result), nil  // "успешный" возврат ошибки!
}
```

**Почему:** LLM не понимает что tool не реализован — она пробует интерпретировать результат. Либо реализуй, либо не регистрируй.

### 2.4. ❌ God-Interface Service

```go
// ❌ НЕ ДЕЛАТЬ: все домены в одном interface
type WbAllService interface {
    GetCards(...)
    GetSales(...)
    GetFunnel(...)
    GetCampaigns(...)
    GetFeedbacks(...)
    // ... 30 методов ...
}

// ✅ Правильно: ISP — один interface на домен
type ProductService interface {
    GetCardsByVendorCodes(...) ([]ProductCard, error)
    GetCardsByNmIDs(...) ([]ProductCard, error)
}
type SalesService interface {
    GetFunnelMetrics(...) (*FunnelMetrics, error)
}
```

### 2.5. ❌ Дублирование Валидации Tool + Service

```go
// ❌ НЕ ДЕЛАТЬ: валидация и в tool и в service
// Tool:
if len(args.Codes) > 10 { return "", fmt.Errorf("max 10 codes") }
cards, err := t.service.GetCards(ctx, args.Codes)  // service тоже валидирует!

// ✅ Правильно: валидация ТОЛЬКО в service
// Tool — только parse и call:
cards, err := t.service.GetCards(ctx, args.Codes)  // service валидирует
if err != nil { return "", err }  // tool только пробрасывает ошибку
```

**Почему:** единое место валидации = проще менять, нет рассинхрона.

### 2.6. ❌ Инлайн HTTP в Tool Execute()

```go
// ❌ НЕ ДЕЛАТЬ: HTTP-вызовы внутри Execute()
func (t *Tool) Execute(ctx, argsJSON) (string, error) {
    req, _ := http.NewRequest("POST", "https://api.wb.ru/...", body)
    resp, _ := http.DefaultClient.Do(req)
}

// ✅ Правильно: через service → client
// Tool не знает про HTTP. Service не знает про JSON marshaling.
```

### 2.7. ❌ Кастомные Хелперы Вместо Stdlib

```go
// ❌ НЕ ДЕЛАТЬ: custom case-insensitive matching
func containsFold(s, substr string) bool { /* 30 строк */ }

// ✅ Правильно: stdlib
strings.Contains(strings.ToLower(s), strings.ToLower(substr))
```

---

## 3. Reference Implementation: ProductService

`ProductService` — canonical example полного pipeline:

### Файлы

| Файл | Назначение | Строк |
|------|-----------|-------|
| `pkg/wb/service.go` | Interface + WbService facade | 370+ |
| `pkg/wb/service_products.go` | ProductService implementation | ~200 |
| `pkg/tools/std/wb_card_content.go` | Tool (parse → service → marshal) | ~100 |
| `pkg/app/tool_setup.go` | Registration (factory switch) | 1 case |
| `openr-conf.yaml` | Config (enabled, description, rate) | 6 строк |

### Service Interface

```go
// pkg/wb/service.go
type ProductService interface {
    GetProducts(ctx, ProductFilter) ([]ProductInfo, error)
    GetProductByID(ctx, nmID) (*ProductInfo, error)
    SyncProducts(ctx) (int, error)
    GetCardsByVendorCodes(ctx, codes []string) ([]ProductCard, error)  // для tool
    GetCardsByNmIDs(ctx, nmIDs []int) ([]ProductCard, error)           // для tool
}
```

### Tool Contract

```go
// pkg/tools/std/wb_card_content.go
type WbCardContentTool struct {
    service     wb.ProductService  // injected, not *wb.Client
    toolID      string
    description string
}

func (t *WbCardContentTool) Definition() tools.ToolDefinition { ... }
func (t *WbCardContentTool) Execute(ctx, argsJSON) (string, error) { ... }
```

### Registration

```go
// pkg/app/tool_setup.go — factory switch
case "get_card_content":
    wbSvc := client.(wb.WbService)
    tool = std.NewWbCardContentTool(wbSvc.Products(), toolCfg)
```

---

## 4. Quick Reference: Существующие Tools

| Tool | Service | Sub-service | API Domain |
|------|---------|------------|------------|
| `get_card_content` | `ProductService` | `wbSvc.Products()` | Content API |
| `get_wb_product_funnel2` | `SalesService` | `wbSvc.Sales()` | Seller Analytics v3 |
| `get_wb_product_funnel_history2` | `SalesService` | `wbSvc.Sales()` | Seller Analytics v3 |
| `get_wb_search_positions2` | `SalesService` | `wbSvc.Sales()` | Seller Analytics v3 |
| `get_wb_top_search_queries2` | `SalesService` | `wbSvc.Sales()` | Seller Analytics v3 |
| `get_wb_campaign_fullstats2` | `AdvertisingService` | `wbSvc.Advertising()` | Advertising API v3 |
| `get_wb_attribution_summary2` | `AttributionService` | `wbSvc.Attribution()` | Composite |
| `get_wb_feedbacks2` | `FeedbackService` | `wbSvc.Feedbacks()` | Feedbacks API |
| `get_wb_questions2` | `FeedbackService` | `wbSvc.Feedbacks()` | Feedbacks API |

**V1 tools (legacy, без service layer):** `search_wb_products`, `list_wb_seller_products`, `get_wb_feedbacks`, etc.
— используют `*wb.Client` напрямую, mock внутри tool.

---

## 5. Checklist: Создание Нового Tool

### Service (`pkg/wb/service_<domain>.go`)
- [ ] Struct: `type <domain>Service struct { client *Client }`
- [ ] Compile-time assertion: `var _ <Domain>Service = (*<domain>Service)(nil)`
- [ ] Каждый метод: validation → mock → API call → transform
- [ ] Validation bounds: empty check + upper limit
- [ ] Mock: `if s.client.IsDemoKey() { return s.getMock...() }`
- [ ] Mock data: deterministic (modulo for variety)
- [ ] Error wrapping: `fmt.Errorf("context: %w", err)`
- [ ] Rate limits: соответствуют API (Content: 100/5, Analytics: 3/3, Feedbacks: 60/3)

### Interface (`pkg/wb/service.go`)
- [ ] Добавить методы в interface (ISP — только нужные)
- [ ] Добавить `var _ Interface = (*impl)(nil)` в impl файле

### WbService Wiring (`pkg/wb/service.go`)
- [ ] `DefaultWbService.<Domain>()` возвращает `&<domain>Service{client: s.client}`

### Tool (`pkg/tools/std/wb_<tool>.go`)
- [ ] Struct с `service <Domain>Service`, не `*wb.Client`
- [ ] Constructor: `New<Tool>(service, cfg config.ToolConfig)`
- [ ] `Definition()`: JSON Schema для LLM function calling
- [ ] `Execute()`: parse → service call → marshal (не больше!)
- [ ] Нет валидации в tool — вся в service
- [ ] Нет mock логики в tool — вся в service
- [ ] Error wrapping: `fmt.Errorf("context: %w", err)`

### Registration (`pkg/app/tool_setup.go`)
- [ ] Case в `registerTool()` switch
- [ ] Извлечение sub-service: `wbSvc := client.(wb.WbService); tool = std.New...(wbSvc.<Domain>(), toolCfg)`
- [ ] Добавить tool name в `isWBTool()` список

### Config (`openr-conf.yaml`)
- [ ] Секция: `enabled`, `description`, `endpoint`, `rate_limit`, `burst`

### Verification
- [ ] `go build ./pkg/wb/` — компилируется
- [ ] `go build ./pkg/tools/std/` — компилируется
- [ ] `go build ./pkg/app/` — компилируется
- [ ] `go test ./pkg/wb/ -v` — PASS
- [ ] Demo key: tool возвращает mock data через service

---

## 6. Source vs Service: Когда Какой

| Вопрос | Source (downloader) | Service (tool) |
|--------|---|---|
| Нужно писать в БД? | ✅ Writer interface | ❌ Нет |
| Пакетная загрузка (30k+)? | ✅ Cursor, pagination | ❌ Нет |
| Точечный запрос (1-50)? | ❌ Избыточно | ✅ Optimized |
| Для LLM function calling? | ❌ Не подходит | ✅ Именно для этого |
| Нужен mock/demo? | MockSource | client.IsDemoKey() |
| Нужна валидация? | Минимальная | Bounds + format |

**Правило:** если задача "LLM вызывает tool, получает данные, формирует ответ" — это Service.
Если задача "выкачать все данные, записать в БД" — это Source.

---

**Last Updated:** 2026-05-31
**Version:** 1.0 (на основе ProductService + WbCardContentTool experience)
