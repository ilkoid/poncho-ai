# fix-card-dimensions-v1.5 — габариты карточек WB из 1С WMS

Рабочая версия фиксера габаритов (старый `../fix-card-dimensions/` — deprecated).
Отличия от старой версии: `--compare` (расхождения WB vs 1С), `--force` (перезапись
существующих габаритов по direction/volume-фильтрам), volume-padding, `--check`
(ошибки валидации WB), `only_new`, изолированный SQLite-кеш вместо `/var/db`.

## Поток запуска

```bash
bash prepare-fix-dims.sh          # свежий кеш ~/dev/fix-dims.db: 1С + cards + stocks
go run . --compare  --config config-fixdims.yaml --db ~/dev/fix-dims.db
go run . --stage    --config config-fixdims.yaml --db ~/dev/fix-dims.db
go run . --diff     --config config-fixdims.yaml --db ~/dev/fix-dims.db
go run . --check    --config config-fixdims.yaml              # ошибки WB (API)
go run . --apply --dry-run --config config-fixdims.yaml --db ~/dev/fix-dims.db
go run . --apply   --config config-fixdims.yaml --db ~/dev/fix-dims.db   # ⚠ запись, только вручную
```

Кеш наполняет `prepare-fix-dims.sh` (1С-v2 + cards-v2 sqlite + stocks-снапшот);
реальный бренд без санитайза, PG не участвует.

## Одноартикульный тест round-trip documents

```bash
bash run-test-one.sh          # stage → снапшот ДО → реальный --apply (1 карточка) → снапшот ПОСЛЕ + автосравнение
```

Артикул 22621252 (nmID 1217654308), пин в `config-test-one.yaml`. Скрипт сам проверяет,
что в staging ровно одна карточка, печатает документы до/после и фейлится при любом
расхождении id/verdict.createdAt/kizMarked или габаритов. Повторный запуск идемпотентен.
Проверка окружения без записи: `APPLY_DRYRUN=1 bash run-test-one.sh`.

## Безопасность записи (полная перезапись карточки)

`POST /content/v2/cards/update` **перезаписывает карточку целиком**. Инвариант
фиксера: `cardupdate.LoadFullCard` (все поля из кеша) → `ToUpdateItem` (полный
payload) → мутация только L/W/H (+вес) → отправка. Характеристики, размеры,
brand/title/description несутся целиком.

- **documents (разрешительные документы, спека 2026-10-11)**: WB перезаписывает
  объект `documents` целиком, поэтому загрузчик cards складывает документы в
  `card_documents`, а `ToUpdateItem` round-trip'ит их с их `id` — по спеке это
  «документ без изменений», повторная проверка не запускается. Карточки без
  документов → поле опускается.
- **kizMarked («Честный ЗНАК»)**: WB возвращает поле в `cards/list`, загрузчик
  пишет `cards.kiz_marked`, payload несёт явное значение. NULL остаётся только
  у строк, не перезакачанных после фикса парсинга, — свежий кеш закрывает это.
- **Гард свежести**: реальный `--apply` отказывает, если карточный снапшот старше
  `max_snapshot_age_hours` (default 24, `0` = off) — round-trip по протухшему
  кешу откатил бы правки из ЛК. Лечение: `bash prepare-fix-dims.sh`.
- **Read-after-write**: после каждого успешного батча — `GetCardErrorsList`;
  первая ошибка валидации останавливает прогон (`wb-errors-*.json` рядом с CWD).
- Prod `--apply` запускает **только пользователь**.

## Конфиг `config-fixdims.yaml`

- `only_new` — только карточки с L×W×H=0 (вес не важен).
- `filters` — pkg/filter (nm_ids, vendor_codes, allowed_years, in_stock, …), AND между полями.
- `compare.tolerance_cm3` / `tolerance_kg` — порог показа расхождений в `--compare`.
- `volume` — отбор по направлению расхождения объёма 1С vs WB (`--stage --force`
  и `--compare`): `direction` (under/over/any), `margin_pct`, `min_delta_cm3`;
  `padding_cm` — прибавка к каждой оси при записи (страховка от штрафов МГХ:
  WB получает `ceil(замер + padding)`).
- `max_snapshot_age_hours` — гард свежести кеша.
- `wb_update` — rate/batch (лимит эндпоинта по спеке: 10 req/min, максимум 3000 карточек).
