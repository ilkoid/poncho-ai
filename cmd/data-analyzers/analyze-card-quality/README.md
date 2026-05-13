# Card Quality Analyzer

Локальный анализатор качества карточек товаров Wildberries.

Читает данные из `wb-sales.db`, скорит каждую карточку, пишет результаты в `sku-analytics.db`. Никаких API-вызовов — всё из собственных данных.

## Скоринг

Каждая карточка получает оценку 0–100 по 4 категориям:

### Content Completeness (30%) — заполненность контента

| Критерий | Вес | Логика |
|----------|-----|--------|
| Title | 20% | `min(100, len/50*100)`. Короткий заголовок — плохо для SEO |
| Description | 35% | Ступени: нет → 0, <100 → 25, <500 → 50, <1000 → 75, ≥1000 символов → 100 |
| Photos | 25% | Ступени: 0 → 0, 1 → 20, 2-4 → 50, 5-7 → 80, 8+ → 100 |
| Video | 10% | Есть = 100, нет = 0. Видео даёт бонус в поиске WB |
| Brand | 10% | Заполнен = 100, пустой = 0 |

### Characteristics Coverage (30%) — покрытие характеристик

Вместо справочника WB API (который не всегда актуален) используется частотный анализ по собственным данным: если 50%+ карточек предмета имеют характеристику — она «ожидаемая», если 90%+ — «обязательная».

| Критерий | Вес | Логика |
|----------|-----|--------|
| Expected chars | 50% | Доля заполненных характеристик из числа «ожидаемых» (≥50% карточек предмета) |
| Common chars | 30% | Доля «обязательных» характеристик (≥90% карточек предмета) |
| Density | 20% | Сколько характеристик у карточки относительно максимума в её предмете |

### Technical Quality (20%) — техническое качество

| Критерий | Вес | Логика |
|----------|-----|--------|
| Dimensions | 40% | `dim_is_valid = 1` → 100. Невалидные габариты — проблема логистики |
| Size chart | 35% | 0 размеров → 0, 1 → 50, 2+ → 100 |
| Photo adequacy | 25% | `min(100, photoCount * 20)`. 5+ фото = 100 |

### Market Performance (20%) — рыночные показатели

| Критерий | Вес | Логика |
|----------|-----|--------|
| Rating | 50% | `avg_rating / 5 * 100`. Нет отзывов = нейтральные 50 |
| Feedback count | 30% | Ступени: 0→0, 1-2→30, 3-10→60, 11-50→80, 51+→100 |
| Answer rate | 20% | `% отвеченных отзывов`. Нет отзывов = нейтральные 50 |

### Финальный score

```
final = content * 0.30 + characteristics * 0.30 + technical * 0.20 + market * 0.20
```

Веса можно переопределить в `config.yaml`.

### Тиры качества

| Тир | Score | Смысл |
|-----|-------|-------|
| Excellent | 90–100 | Карточка заполнена полностью, конкурентов мало |
| Good | 75–89 | Хорошее заполнение, есть что улучшить |
| Average | 50–74 | Среднее, заметные пробелы |
| BelowAvg | 25–49 | Слабое заполнение, нужно срочное улучшение |
| Poor | 0–24 | Критически плохо, карточка не работает |

## Конфигурация

```yaml
# Откуда читаем
source:
  db_path: "/var/db/wb-sales.db"

# Фильтр по году производства (из 2-3 цифры артикула продавца)
# "12621749" → цифры 2,6 → год 26 → 2026
# Пустой = без фильтра
filter:
  allowed_years: [23, 24, 25, 26]

# Куда пишем результаты
output:
  db_path: "/var/db/sku-analytics.db"

# Веса категорий в финальном score (сумма = 1.0)
weights:
  content: 0.30
  characteristics: 0.30
  technical: 0.20
  market: 0.20
```

## Выходная таблица

Результаты записываются в `card_quality_scores`:

| Колонка | Тип | Описание |
|---------|-----|----------|
| nm_id | INTEGER PK | ID товара WB |
| vendor_code | TEXT | Артикул продавца |
| subject_name | TEXT | Предмет (категория WB) |
| year | TEXT | Год создания карточки |
| content_score | REAL | Content Completeness 0–100 |
| char_coverage | REAL | Characteristics Coverage 0–100 |
| technical_score | REAL | Technical Quality 0–100 |
| market_score | REAL | Market Performance 0–100 |
| final_score | REAL | Итоговый взвешенный score |
| tier | TEXT | Excellent / Good / Average / BelowAvg / Poor |
| computed_at | TEXT | Timestamp расчёта |

## Примеры использования

```bash
# Полный прогон с конфигом
go run ./cmd/data-analyzers/analyze-card-quality/ --config cmd/data-analyzers/analyze-card-quality/config.yaml

# Все карточки без фильтра по артикулу
go run ./cmd/data-analyzers/analyze-card-quality/ --db /var/db/wb-sales.db --out-db /var/db/sku-analytics.db

# Только карточки созданные в 2025
go run ./cmd/data-analyzers/analyze-card-quality/ --year 2025

# Только конкретный предмет
go run ./cmd/data-analyzers/analyze-card-quality/ --subject Футболки

# Комбо: предмет + год + кастомная база
go run ./cmd/data-analyzers/analyze-card-quality/ --year 2025 --subject "Кроссовки" --out-db /tmp/test-analytics.db

# SQL-запрос к результатам
sqlite3 /var/db/sku-analytics.db "
  SELECT tier, COUNT(*), ROUND(AVG(final_score),1)
  FROM card_quality_scores
  WHERE year >= '2024'
  GROUP BY tier ORDER BY AVG(final_score) DESC
"
```
