# fix-penalties-dims — автономный робот-фиксер штрафняка МГХ

Перезаписывает габариты карточки (L/W/H) складскими замерами WB, когда по артикулу
прилетел штраф за неверные габариты. Цель — карточка совпадает с фактом WB, и
повторных штрафов нет. Работает автоматически по cron.

## Автономная SQLite-архитектура (изоляция от PG)

Робот работает с **собственной изолированной SQLite** (`fixer.db`). Обёртка
`run-penalties-dims-fixer.sh` перед каждым прогоном грузит в неё свежие **реальные**
данные через существующие загрузчики, и только потом запускает фиксер:

1. `download-wb-penalties-v2 --backend sqlite --db fixer.db` — подтверждённые штрафы.
2. `download-wb-cards --db fixer.db` — полный каталог карточек (v1, **без scrub** →
   реальный бренд `PlayToday`).
3. `fix-penalties-dims --auto --db fixer.db` — stage (гард направления) + apply (WB API).

На **PostgreSQL** фиксер не опирается. Это сделано осознанно: общая PG-база
(`wb_data_prod`) может санитизироваться для облака (`PlayToday` → `[PlayBrand]`), и
изоляция гарантирует, что **фейковый бренд никогда не попадёт в WB** через этого
робота. Соответствует Rule 13 (cmd-утилита автономна, ресурсы рядом). Переиспользует
`pkg/cardupdate` (`CardUpdater.LoadFullCard` + `ToUpdateItem`) — безопасная полная
перезапись: загрузить ВСЕ поля → мутировать только L/W/H → отправить.

## Что делает

1. **stage** — берёт последний подтверждённый замер (`is_valid=1`, max `dt_bonus`)
   на каждый `nm_id` (оконная функция `ROW_NUMBER()` — SQLite не умеет `DISTINCT ON`),
   джойнит с габаритами карточки, пишет в `fix_penalties_dims_staging`.
   - **Гард направления**: `pending` только при **недозаявке** (объём карточки <
     замера → реальный риск штрафа). Пере-заявленные и точные карточки → `skipped`
     (риска нет, не уменьшаем). Сравнение по **объёму** (коммутативно — устойчиво к
     перестановке осей L/W).
   - Идемпотентность: уже-фиксенные карточки → `skipped`, почасовой прогон их не трогает.
2. **apply** — батчами шлёт `POST /content/v2/cards/update` (вес `WeightBrutto`
   сохраняется из карточки — штрафы веса не содержат). После каждого батча —
   read-after-write (`GetCardErrorsList`); при первой ошибке валидации — **стоп**
   прогона (оставшиеся `pending` подберёт следующий прогон крона).
3. **audit** — дневной CSV в `logs/penalties-dims-YYYY-MM-DD.csv`: строка на каждую
   перезаписанную карточку (`FIX` — было→стало) + отдельная строка на WB-проверку
   батча (`WB_OK`/`WB_ERROR`/`WB_STOP`) + строка сводки прогона (`RUN`). Skipped-
   карточки в CSV **не пишутся** (лог — хроника изменений, а skipped = «ничего не
   менялось»); их счётчик виден в `RUN` и в staging-таблице.

## Конфиг (`config.yaml`)

- `storage.db_path` — изолированная SQLite робота (`fixer.db`; override через `--db`).
- `wb.api_key_env: WB_API_ANALYTICS_AND_PROMO_KEY`.
- `source` — `only_confirmed`, `latest_per_nm`.
- `filter` — `nm_ids` / `vendor_codes` / `exclude_vendor_codes` (SQLite `IN`/`NOT IN`).
  Скоуп рабочего множества на **любом** чтении staging: применяется и на `--stage`
  (что попадает в staging), и на `--apply`/`--diff` (что читается из staging). Поэтому
  правка фильтра в yaml Honourится `--apply --dry-run` **сразу**, без ре-стейджа. Пустой
  фильтр → все подтверждённые штрафы (поведение cron `--auto` не меняется).
- `wb_update` — rate/batch (defaults из `cardupdate.WBUpdateConfig.Defaults()`).
- `audit.log_dir` — каталог CSV (по умолчанию `logs` рядом с бинарем).

## Команды

```bash
# Авто (cron) — ilkoid.sh сам грузит cards+penalties в fixer.db, потом stage+apply:
./ilkoid.sh                 # из корня репо

# Ручная инспекция staging-таблицы (fixer.db уже загружен ilkoid.sh):
go run . --stage  --config config.yaml --db fixer.db
go run . --diff   --config config.yaml --db fixer.db
go run . --apply --dry-run --config config.yaml --db fixer.db   # payload без отправки

# Проверить недавние ошибки валидации WB:
go run . --check --config config.yaml
```

## Запуск / cron — `ilkoid.sh` (корень репо)

Один простой launchер в стиле `download-all.sh`: `export`-ключи прямо в скрипте, `go run`
(без сборки бинарей), линейно. Грузит penalties + cards в изолированный `fixer.db`
(SQLite; прод-PG **не трогается**), затем правит габариты.

**`ilkoid.sh` занесён в `.gitignore`** (несёт API-ключи через `export`). На новой машине:
создай его по образцу ниже (или `scp` с другой машины) и `chmod +x ilkoid.sh`.

Образец `ilkoid.sh` (использует выделенные SQLite-конфиги в `cmd/.configs/ilkoid/` —
PG вообще не участвует, даже без флагов):
```bash
#!/bin/bash
export WB_API_KEY="<ключ>"                       # для download-wb-cards
export WB_API_ANALYTICS_AND_PROMO_KEY="<ключ>"   # для penalties + fixer
PONCHO="$(cd "$(dirname "$0")" && pwd)"
FIXER_DB="$PONCHO/cmd/fix-utilities/fix-penalties-dims/fixer.db"
CFG="$PONCHO/cmd/.configs/ilkoid"
go run "$PONCHO/cmd/data-downloaders/download-wb-penalties-v2" --config "$CFG/penalties.yaml" --db "$FIXER_DB"
go run "$PONCHO/cmd/data-downloaders/download-wb-cards"         --config "$CFG/cards.yaml"     --db "$FIXER_DB"
( cd "$PONCHO/cmd/fix-utilities/fix-penalties-dims" && go run . --auto --db fixer.db "$@" )
```

Запуск:
```bash
./ilkoid.sh --dry-run    # ПЕРВЫЙ прогон: payload brand=PlayToday, pending≈5, без WB-записи
./ilkoid.sh              # реальный прогон (WB-запись)
# cron:  37 * * * * /path/to/ilkoid.sh
```


## Безопасность

- WB API `/content/v2/cards/update` **полностью перезаписывает** карточку. Инвариант
  фиксера: `LoadFullCard` (все поля) → `ToUpdateItem` (полный payload) → менять только
  L/W/H → отправить. `WeightBrutto`, характеристики и размеры несутся из карточки целиком.
- **Целочисленные габариты (ceil):** `/content/v2/cards/update` требует
  `dimensions.length/width/height` как `integer` (swagger `02-products.yaml`). Складской
  замер WB бывает дробным (`81.1×21.3×2.3`), поэтому на стадии staging фиксер округляет
  габариты **вверх** до целых см (`ceilCm`). Ceil по каждой оси гарантирует
  `card_volume ≥ measured_volume` → МГХ-штраф за занижение гаснет; round/floor могли бы
  оставить карточку недозаявленной (`81×21×2=3402 < 3976`). Over-declaration МГХ не
  штрафуется (чуть выше тарифы хранения). Staged = записываемому, поэтому `--diff` честно
  показывает что уйдёт в WB.
- **Изоляция брендов**: `fixer.db` заполняется v1-загрузчиком без scrub, поэтому бренд
  всегда реальный. Санитизация PG (`[PlayBrand]`) физически не может попасть в робота.
- Prod `--apply`/`--auto` запускает **только пользователь**. Claude не выполняет запись;
  тесты — на `--db /tmp/…` + `--dry-run`. `/var/db` и общая PG роботу недоступны.

## Известные ограничения

- **kizMarked не round-trip'ится** (отложено). `POST /content/v2/cards/update` принимает
  `kizMarked` (default false), но поля нет в `ProductCard`/`CardUpdateItem`/`cards`/
  загрузчике → при перезаписи подтверждение маркировки «Честный ЗНАК» может сброситься.
  Для маркированной одежды (needKiz=true) учитывать вручную. См. `pkg/cardupdate`
  (`KNOWN GAP` у `ToUpdateItem`) и memory `cardupdate_kizmarked_gap`. Не влияет на
  габариты — поэтому в scope этой утилиты не входит.
- Сертификаты/декларации, photos/video/tags/wholesale, imtID/subject*/needKz/timestamps —
  **безопасны** (audit сверен со swagger): либо не входят в объект карточки, либо
  read-only, либо явно «невозможно обновлять» через этот эндпоинт.
