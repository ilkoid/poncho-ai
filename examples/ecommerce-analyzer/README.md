# E-commerce Product Analyzer

Практический пример Poncho AI с реальными данными Wildberries API.

## Демонстрирует

Все 4 фазы UX улучшений Poncho AI:

### ✅ Phase 1: SimpleTui
Красивый TUI интерфейс с streaming событиями.

### ✅ Phase 2: Tool Bundles
Группировка инструментов по бизнес-контексту:
```yaml
tool_bundles:
  wb-content-tools:
    description: "Wildberries Content API: categories, brands, products"
    tools:
      - get_wb_parent_categories
      - get_wb_subjects
      - get_wb_brands
      - search_wb_products
```

### ✅ Phase 3: Token Resolution
Bundle-first mode экономит 75-95% токенов:
```
System prompt: ~300 tokens (вместо ~15,000)
Bundle expansion: wb-content-tools → 4 tools
```

### ✅ Phase 4: Presets System
2-строчный запуск приложения:
```go
client, _ := agent.NewFromPreset(ctx, "interactive-tui")
result, _ := client.Run(ctx, query)
```

## Запуск

```bash
cd examples/ecommerce-analyzer
go run main.go
```

## Требования

- `ZAI_API_KEY` — переменная окружения для LLM
- `WB_API_KEY` — переменная окружения для Wildberries API

## Результаты

```
✅ Agent created successfully!
✅ Bundle Resolver: Expanded wb-content-tools → 4 tools
✅ Real WB API: 89 parent categories loaded
📁 Results: debug_logs/analysis_*.json
📊 Debug logs: debug_logs/debug_*.json
```

## Token Savings

| Metric | Without Bundles | With Bundles | Savings |
|--------|----------------|--------------|---------|
| System prompt | ~15,000 tokens | ~300 tokens | **98%** |
| Total per request | ~15,000 | ~1,800 | **88%** |

## Известные проблемы

- **JSON Unmarshal Error**: LLM передает `parentID` как строку вместо числа
  - **Решение**: Исправить tool definition или добавить prompt engineering
  - **Статус**: Не влияет на демонстрацию 4х фаз UX

## Структура

```
ecommerce-analyzer/
├── main.go          # 2-строчный агент + обработка результатов
├── config.yaml      # WB API + tool bundles
├── prompts/         # (пустой, можно добавить custom prompts)
└── debug_logs/      # JSON результаты + debug traces
    ├── analysis_*.json      # Результаты анализа
    └── debug_*.json         # Подробные trace логи
```

## Во Славу Божию

Аминь.
