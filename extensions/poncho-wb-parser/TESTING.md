# Poncho WB Parser — пошаговая самопроверка в Chrome

Расширение собрано и покрыто 50 юнит-тестами, но в реальном Chrome его ещё не гоняли. Эта инструкция — прогнать его руками **без live-WB** (mock-сессия): загрузка, IndexedDB-схема, движок сбора, отчёты, экспорт, конструктор, (опц.) устойчивость к смерти SW. Live WB — последний, необязательный шаг.

## 0. До старта
- WSL2 → Chrome на Windows читает файлы через `/mnt/c` (надёжно) или UNC `\\wsl.localhost\GLM\...` (нестабильно для расширений).
- `dist/` уже собран. Пересобирать не нужно, если код не менялся.

## 1. Положи dist в Windows-папку
Chrome плохо работает с MV3-расширениями по UNC-пути — скопируй сборку на Windows-сторону:
```bash
cp -r /home/ilkoid/go-workspace/src/poncho-ai/extensions/poncho-wb-parser/dist \
      /mnt/c/Users/Admin/poncho-wb-parser-dist
```
> Если Windows-пользователь не `Admin` — проверь `ls /mnt/c/Users` и подставь своё имя. После каждого `npm run build` повторяй `cp`.
>
> Фолбэк (без копирования): в диалоге Load Unpacked вставь UNC-путь в поле «Папка»:
> `\\wsl.localhost\GLM\home\ilkoid\go-workspace\src\poncho-ai\extensions\poncho-wb-parser\dist`

## 2. Загрузи расширение
1. `chrome://extensions`
2. Правый верх → **Developer mode** = ON
3. **Load unpacked** → выбери `C:\Users\Admin\poncho-wb-parser-dist`
4. ✅ Появилась карточка **«Poncho WB Parser» 0.1.0**, без красных ошибок. Закрепи 📌 в тулбаре.

## 3. Где смотреть логи (3 места)
- **Service worker:** `chrome://extensions` → «Service worker» на карточке → DevTools SW.
- **Dashboard:** правый клик по панели расширения → «Inspect» → Console.
- **IndexedDB:** DevTools панели → **Application → Storage → IndexedDB → poncho_wb_parser** (7 хранилищ).
- **WB-вкладка** (только live): F12 на WB → Console (`[Poncho] MAIN-world injector installed` + `ISOLATED bridge ready`).

---

## ТЕСТ A — схема (30 сек)
1. 🤠 Poncho в тулбаре → **«Открыть панель управления»** → дашборд (3 закладки).
2. DevTools → Application → IndexedDB → `poncho_wb_parser`.
✅ Ровно **7 stores**: `search_queries, search_positions, vitrine_ads, competitor_cards, competitor_card_prices, competitor_card_details, competitor_card_stocks`.

## ТЕСТ B — mock-сессия (главный, ~5 сек, без WB)
1. **«Сбор данных»** → **▶ Run mock session**.
2. Live-лог покажет `mock decode (search) — всего строк: 2`, `mock decode (card_detail) — всего строк: 5`, `mock done`.
3. Карточки **«Состояние базы»** обновятся.
✅ Счётчики: Позиции **2**, Карточки **1**, Цены **1**, Детали **1**, Остатки **1**, Баннеры **0**.
4. Доп: IndexedDB → `search_positions` (2 записи, position 101/102), `competitor_cards` (1: Nike / ООО Рога).

## ТЕСТ C — отчёты + экспорт (~10 сек)
1. **«Отчёты и экспорт»** → **Снимок A** уже выбран → **«Построить»**.
✅ Три панели: Видимость (nm 111/222), Карта конкурентов (supplier 900, 899 ₽), Цены и остатки (гистограмма + «в наличии: 1»).
2. **[xlsx]** → `poncho-<report>-<ts>.xlsx` → 1 лист, кириллица читается.
3. **[csv]** → открой в редакторе → `﻿nm_id,brand,...` (BOM), кириллица цела.

## ТЕСТ D — конструктор + стабильность query_id (~1 мин)
1. **«Настройки»**. В «Предметы» добавь «рюкзаки», в «Пол» — «для мальчика». Превью «2×2×1×1 = 4 → 4 → 4».
2. **«Сохранить конструктор»** → «✓ Сохранено: 4 запрос(ов), 4 с стабильными query_id».
3. IndexedDB → `search_queries`: 4 записи.
4. **F5** на дашборде → поля снова заполнены → снова «Сохранить» → `search_queries` осталось **4** (те же id). Это кросс-сессионная стабильность.
5. (Опц) «Свой supplier_id» → `900` → в «Отчёты» Карта конкурентов подсветит 900 «вы».

## ТЕСТ E (продвинутый) — смерть SW mid-session
1. **▶ Run mock session** → сразу `chrome://extensions` → **↻ reload** на карточке Poncho.
2. В дашборд — лог дошёл до конца, счётчики заполнились.
✅ Данные дошли до Dexie, несмотря на убитый SW (записи в offscreen).

## ТЕСТ F (опц, РЕАЛЬНЫЙ WB) — один запрос
1. **«Сбор данных»** → «один запрос»: `кроссовки детские` → **Старт (запрос)**.
2. Откроется вкладка WB, навигация в человеческом темпе; live-лог: `navigate → scroll p2 → ... → done`.
3. «Отчёты» → НОВЫЙ снимок → «Построить». Реальные данные (на порядки больше mock).
⚠️ Живой трафик на WB — антибот не нужен (расширение в реальном браузере).

---

## Чек-лист «всё работает»
- [ ] 7 stores в IndexedDB
- [ ] mock: счётчики 2 / 1 / 1 / 1 / 1 / 0
- [ ] отчёты по 3 панелям
- [ ] xlsx + csv качаются, кириллица читается
- [ ] конструктор переживает F5
- [ ] reload SW mid-session не роняет данные

## Если что-то не так
- **Не грузится / красная ошибка** → «Errors» на карточке; обычно не та папка (нужен `dist`, не корень) или старая сборка.
- **Кнопки мертвы / панель пустая** → DevTools панели → Console, ищи `[Poncho] ...`.
- **mock не пишет данные** → DevTools SW → ищи `offscreen create FAILED` (Chrome <109 без offscreen API).
- **Отчёты пустые** → проверь **Снимок A** → «Построить».
- **xlsx не качается** → разрешение `downloads` / блокировщик загрузок.
