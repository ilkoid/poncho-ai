# Ozon Parser

Утилита для извлечения данных с маркетплейса Ozon используя headless браузер (Playwright).

## Возможности

- ✅ **Парсинг страницы товара** — название, цена, скидка, рейтинг, отзывы
- ✅ **Парсинг категории** — список товаров с ценами
- ✅ **Поиск по запросу** — автоматический поиск и извлечение результатов
- ✅ **Headless режим** — работа без графического интерфейса
- ✅ **JSON вывод** — удобный формат для дальнейшей обработки

## Установка

### 1. Требования
- Python 3.10 или выше
- pip

### 2. Настройка окружения

```bash
# Переходим в папку утилиты
cd tools/ozon_parser

# Создаём виртуальное окружение
python3 -m venv venv

# Активируем (Linux/Mac)
source venv/bin/activate
# Активируем (Windows)
venv\Scripts\activate

# Устанавливаем зависимости
pip install -r requirements.txt

# Устанавливаем браузер Chromium
playwright install chromium
```

## Использование

### Парсинг страницы товара

```bash
python ozon_parser.py product --url "https://www.ozon.ru/product/futbolka-cosmo-2543561282/"
```

**Результат:**
```json
{
  "url": "https://www.ozon.ru/product/...",
  "title": "Футболка Cosmo",
  "price": "399 ₽",
  "price_number": 399,
  "old_price": "2 000 ₽",
  "discount": "-80%",
  "rating": "4.8",
  "reviews": "403",
  "brand": "Cosmo",
  "seller": "Ozon",
  "description": "..."
}
```

### Парсинг категории

```bash
python ozon_parser.py category --url "https://www.ozon.ru/category/hlopkovye-muzhskie-futbolki/" --limit 20
```

### Поиск по запросу

```bash
python ozon_parser.py search --query "футболки мужские хлопковые" --limit 10
```

### Сохранение в файл

```bash
python ozon_parser.py product --url "..." --output result.json
```

### Отладочный режим (с видимым браузером)

```bash
python ozon_parser.py product --url "..." --no-headless
```

## Команды

| Команда | Описание | Обязательные аргументы |
|---------|----------|----------------------|
| `product` | Парсинг страницы товара | `--url` |
| `category` | Парсинг категории | `--url` |
| `search` | Поиск по запросу | `--query` |

## Опции

| Опция | Описание | По умолчанию |
|-------|----------|-------------|
| `--url` | URL страницы | — |
| `--query` | Поисковый запрос | — |
| `--limit` | Лимит товаров (для category/search) | 20 |
| `--output`, `-o` | Файл для сохранения JSON | auto |
| `--no-headless` | Показать браузер | выключено |

## Структура проекта

```
ozon_parser/
├── ozon_parser.py      # Основной скрипт
├── requirements.txt     # Зависимости Python
├── README.md           # Документация (этот файл)
└── output/             # Результаты (создаётся автоматически)
    ├── product_result.json
    ├── category_result.json
    └── search_result.json
```

## Troubleshooting

### Playwright не может найти браузер

```bash
playwright install chromium
```

### Ошибка таймаута

Попробуйте увеличить таймаут в коде или использовать `--no-headless` для отладки.

### Селекторы не работают

Ozon периодически меняет структуру страницы. Если данные не извлекаются:
1. Запустите с `--no-headless`
2. Откройте DevTools в браузере (F12)
3. Найдите актуальные CSS-селекторы
4. Обновите словарь `SELECTORS` в `ozon_parser.py`

## Лицензия

MIT

## Автор

Создано с помощью Claude Code
