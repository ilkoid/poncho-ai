# fix-penalties-dims — робот-фиксер штрафняка МГХ

Перезаписывает габариты карточки (L/W/H) складскими замерами WB, когда по артикулу
прилетел штраф за неверные габариты. Цель — карточка совпадает с фактом WB, и
повторных штрафов нет. Работает автоматически по cron после загрузчика
`download-wb-penalties-v2`.

**PostgreSQL only.** Переиспользует `pkg/cardupdate` (безопасная полная перезапись
карточки: `LoadFullCard` → `ToUpdateItem` → мутация только L/W/H).

## Что делает

1. **stage** — берёт последний подтверждённый замер (`is_valid=true`, max `dt_bonus`)
   на каждый `nm_id` из `measurement_penalties`, джойнит с текущими габаритами
   карточки (`cards`), пишет в staging-таблицу `fix_penalties_dims_staging`.
   - Если габариты карточки уже = замеру → `status='skipped'` (**идемпотентность**:
     почасовой прогон не перезаписывает уже исправленные карточки).
   - Иначе → `status='pending'`.
2. **apply** — батчами шлёт `POST /content/v2/cards/update` (полный payload, вес
   `WeightBrutto` сохраняется из карточки — штрафы веса не содержат). После каждого
   батча — read-after-write (`GetCardErrorsList`); при первой ошибке валидации —
   **стоп** прогона (оставшиеся `pending` подберёт следующий прогон крона).
3. **audit** — дневной CSV в `logs/penalties-dims-YYYY-MM-DD.csv`: строка на каждую
   карточку (`FIX`/`SKIP`/`ERROR`) + отдельная строка на WB-проверку батча
   (`WB_OK`/`WB_ERROR`/`WB_STOP`) + строки сводки прогона (`RUN`).

## Конфиг (`config.yaml`)

- `storage.backend: postgres`, `pg_database` (wb_data_prod / wb_data_test),
  `pg_password_env: PG_PWD`. Host/port/user из `PGHOST`/`PGPORT`/`PGUSER`.
- `wb.api_key_env: WB_API_ANALYTICS_AND_PROMO_KEY`.
- `filter` — `nm_ids` / `vendor_codes` / `exclude_vendor_codes` (PG-native).
- `wb_update` — rate/batch (defaults из `cardupdate.WBUpdateConfig.Defaults()`).
- `audit.log_dir` — каталог CSV (по умолчанию `logs` рядом с бинарем).

## Команды

```bash
# ТЕСТ (только wb_data_test + --dry-run; НИКОГДА /var/db, prod --apply — только юзер):
go run . --stage  --config config.yaml --pg-database wb_data_test
go run . --diff   --config config.yaml --pg-database wb_data_test
go run . --apply --dry-run --config config.yaml --pg-database wb_data_test

# Ручной запуск на одной карточке (фильтр в config.yaml: filter.nm_ids: [<id>]):
go run . --auto --dry-run --config config.yaml --pg-database wb_data_test

# Авто (cron) — stage + apply одним вызовом:
go run . --auto --config config.yaml                 # ⚠ prod-запись, только юзер

# Проверить недавние ошибки валидации WB:
go run . --check --config config.yaml
```

## Cron

`run-penalties-dims-fixer.sh` (flock + дневной лог + source env) запускается
после `download-wb-penalties-v2`:

```
37 * * * * .../fix-penalties-dims/run-penalties-dims-fixer.sh
```

Период настраивает пользователь в cron'е.

## Безопасность

- WB API `/content/v2/cards/update` **полностью перезаписывает** карточку. Инвариант
  фиксера: `LoadFullCard` (все поля) → `ToUpdateItem` (полный payload) → менять
  только L/W/H → отправить. Никаких частичных payload'ов.
- `WeightBrutto`, характеристики и размеры несутся из загруженной карточки целиком.
- Prod `--apply`/`--auto` запускает **только пользователь**. Claude не выполняет
  запись; тесты — на `wb_data_test` + `--dry-run`.
