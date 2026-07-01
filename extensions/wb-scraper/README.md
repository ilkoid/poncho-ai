# WB Scraper — браузерное расширение (Edge/Chromium, MV3)

Собирает данные с витрины Wildberries, перехватывая API-ответы прямо из реального
браузера. WB не даёт парсить headless-браузером (антибот); расширение в настоящем
браузере неотличимо от обычного юзера.

Два режима в одном расширении, переключаются в popup.

## Установка (load unpacked)

1. Edge: `edge://extensions` (или Chrome: `chrome://extensions`).
2. Включить **Developer mode** (переключатель справа сверху).
3. **Load unpacked** → выбрать папку `extensions/wb-scraper/` (где лежит `manifest.json`).
4. Закрепить значок на панели для удобства.

После правки любого файла — кнопка **Reload** (↻) на карточке расширения.

## Режим Recon (разведка) — фаза 1

Пассивный сниффер. Ты открываешь страницу и **сам** кликаешь по ней, расширение пишет
**все** запросы к `*.wildberries.ru` (URL, status, content-type, байты, тело для JSON).

1. Popup → **Recon** → вставь стартовый URL, например:
   - карточка: `https://www.wildberries.ru/catalog/17401163/detail.aspx`
   - поиск: `https://www.wildberries.ru/catalog/0/search.aspx?search=...`
   - промо: `https://www.wildberries.ru/promotions/...`
   - бренд: `https://www.wildberries.ru/brands/le-and-lo`
2. **Открыть** → откроется вкладка.
3. Кликай/скролль/переходи как обычно (открой характеристики, пролистай отзывы, пагинация…).
4. **Экспорт JSON** → `wb-recon-<ts>.json` в папке загрузок.
5. Открой дамп: найди «жирные» JSON-эндпоинты, которые повторяются — это реальные
   источники данных (карточка/выдача/промо/бренд).

> Recon ничего не дёргает активно — только слушает. Самый безопасный режим.

## Режим Collect (сбор) — фаза 2

Активный обход по списку целей с человеческим темпом (паузы 2-7с), реальная навигация
фоновой вкладкой. Хранит только то, что попадает под **паттерны** (см. ниже).

1. Popup → **Collect** → вставь цели (по строке):
   - число (5-12 цифр) → карточка товара
   - текст → поисковый запрос
   - `http(s)://...` → как есть
2. **Старт** → откроется фоновая вкладка WB, расширение пойдёт по целям с паузами 2-7с.
3. **Экспорт JSON** → `wb-scrape-<ts>.json` (только нужные эндпоинты, без шума).

> Коллектор БД НЕ подключён — это следующая фаза. Сейчас вывод только в JSON-файлы,
> которые станут тестовыми fixture'ами для будущего Go-коллектора.

## Collect-паттерны — верифицированы по Recon (2026-06-28)

`src/background.js`, массив `COLLECT_PATTERNS`. Сопоставлены с реальным дампом:

| kind | Реальный эндпоинт | Примечание |
|------|-------------------|------------|
| `card` | `/__internal/card/cards/v4/detail` и `/list` | `products[]`: цена/рейтинг/остатки/продавец/габариты |
| `search` | `/__internal/search/exactmatch/ru/common/v18/search` | ⚠️ приходит как **`text/plain`** — `inject.js` читает тело по содержимому, не по content-type |
| `ad` | `/__internal/banners/shelfs/search`, `banners-website…/banners` | `data.shelfs.data.{cpm, ordBannerErid, products}` |
| `brand` | `/__internal/catalog/brands/v4/catalog` (+`/v8/filters`) | каталог товаров бренда (та же схема `products[]`, что в search) |

> **Промо не размечено:** в дампах видны только `/webapi/spa/promotions/metatags/<slug>` (SEO-метатеги). Товары акции нужно развести отдельным Recon на странице `/promotions/<x>`.

WB меняет версии (`v4`/`v18`…) со временем; паттерны используют `/v\d+/`. После
существенных изменений на WB — перезапусти Recon и сверь regex'ы заново.

## Где смотреть логи (дебаг)

| Что | Где |
|------|-----|
| Логи SW (`background.js`) и collect-навигации | `edge://extensions` → карточка → **Inspect views: service worker** |
| Логи `inject.js` (перехват) и MAIN world | F12 на вкладке WB → Console (перехватчик живёт в контексте страницы, не SW) |
| Логи content.js / offscreen | там же, в SW DevTools (offscreen — отдельная inspect view) |

Если перехват пустой: проверь, что `inject.js` встал в MAIN world (лог
`inject.js installed in MAIN world` в консоли страницы WB), и что запросы реально
летят к `*.wildberries.ru` (Network tab).

## Файлы

```
manifest.json        MV3 каркас, два content_scripts (MAIN inject + ISOLATED bridge)
src/inject.js        MAIN world: обёртки fetch + XHR, wide-open, postMessage наверх
src/content.js       ISOLATED мост: window.message → chrome.runtime.sendMessage
src/background.js    SW: режимы, фильтр, storage буферы, downloads export, offscreen, collect
src/offscreen.html   скрытый документ (держит setTimeout надёжным)
src/offscreen.js     дирижёр Collect-loop: навигация + рандом-пауза 2-7с
src/popup.html/.js   UI: режим, URL/цели, Start/Stop/Export/Clear
```

## Что НЕ входит (следующие планы)
- Go-коллектор (`net/http` на localhost) + таблицы `competitor_cards` / `search_positions`
  / `vitrine_ads` (снапшотный паттерн). JSON-дампы из расширения = fixtures.
- LLM-генерация целей (`pkg/agent`/`Provider`): примеры URL → список целей → расширению.
- Мультирегион (крутить `dest`/куку `wbxrc`).
