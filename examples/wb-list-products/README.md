# WB Seller Products List

Утилита для просмотра товаров продавца с фильтрацией по предмету.

## Установка

```bash
cd examples/wb-list-products
```

## Использование

### Все товары продавца

```bash
WB_API_KEY=your_key go run main.go
```

### Фильтр по предмету

```bash
# Найти комбинезоны
WB_API_KEY=your_key go run main.go --search комбинезон

# Найти платья
WB_API_KEY=your_key go run main.go --search платье
```

## Пример вывода

```
=== WB Seller Products List ===
🔑 API Key: ab12...yz89
🔍 Фильтр: комбинезон

⏳ Загружаем товары...

📊 НАЙДЕНО ТОВАРОВ: 3

🛍️  ТОВАР #1
  nmID:          123456789
  Артикул:       ART001
  Название:      Детский комбинезон зимний
  Бренд:         BabyBrand
  Предмет:       Комбинезоны
  ID предмета:   1481

...

🔧 КОМАНДЫ ДЛЯ ТЕСТА ВОРОНКИ:
======================================================================

# Тест первых 3 товаров:
cd ../wb-funnel-demo && WB_API_KEY=$WB_API_KEY go run main.go --nmIds 123456789,234567890,345678901 --days 7

# Тест конкретного товара (ART001):
cd ../wb-funnel-demo && WB_API_KEY=$WB_API_KEY go run main.go --nmIds 123456789 --days 7
```

## Архитектура

- `pkg/wb.Client.Post()` — существующий SDK метод
- Content API `/content/v2/get/cards/list` — возвращает только товары продавца
- Фильтрация по `SubjectName` содержит искомую строку

## Лицензия

MIT
