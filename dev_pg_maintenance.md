# Справка: REINDEX и VACUUM для wb_data_prod

Короткий практикум по обслуживанию PostgreSQL-базы загрузчиков (тяжёлые ночные
`ON CONFLICT DO UPDATE` → блоат таблиц и индексов). Контекст: `download-all.sh`
каждую ночь пишет в одни и те же таблицы, autovacuum не всегда успевает.

---

## 1. Три операции — когда какую

| Операция | Что делает | Блокировки | Когда применять |
|---|---|---|---|
| `VACUUM tbl` | Помечает мёртвые строки переиспользуемыми | нет, параллельно с записью | регулярно — **ночная Phase 7 уже делает** |
| `VACUUM (ANALYZE) tbl` | то же + обновляет статистику планировщика | нет | там, где менялся профиль данных |
| `VACUUM FULL tbl` | **Переписывает файл таблицы**, отдаёт место ОС, пересобирает индексы | ACCESS EXCLUSIVE на всё время — таблица недоступна | heap сильно раздут (см. §3), окно без записи |
| `REINDEX TABLE tbl` | Пересобирает все индексы | ACCESS EXCLUSIVE | индексный блоат, окно без записи |
| `REINDEX TABLE CONCURRENTLY tbl` | то же, но читатели/писатели работают | только короткие локи | живая база, загрузки идут |

Помни:
- **plain VACUUM НЕ уменьшает файл** — место помечается свободным внутри таблицы;
- **REINDEX НЕ трогает heap** — раздутую таблицу лечит только VACUUM FULL;
- `VACUUM FULL` нельзя выполнять в транзакции (`BEGIN…COMMIT` вокруг — ошибка);
- если `CONCURRENTLY` убит посередине — останутся невалидные индексы `_ccnew`,
  чек см. §4, лечение: `DROP INDEX ..._ccnew` и перестроить заново.

## 2. Диагностика — как понять, что пора

Индексный блоат (норма 20–40%, тревога >60–70%):

```sql
SELECT c.relname AS tbl,
       pg_size_pretty(pg_relation_size(c.oid))                          AS heap,
       pg_size_pretty(pg_total_relation_size(c.oid)-pg_relation_size(c.oid)) AS indexes,
       round(100.0*(pg_total_relation_size(c.oid)-pg_relation_size(c.oid))
             / nullif(pg_relation_size(c.oid),0))::int                  AS idx_pct
FROM pg_class c
WHERE c.relname IN ('orders','operational_sales','warehouse_remains', /* ... */)
ORDER BY pg_total_relation_size(c.oid) DESC;
```

Мёртвые строки и последний autovacuum:

```sql
SELECT relname, n_live_tup, n_dead_tup, last_autovacuum
FROM pg_stat_user_tables
ORDER BY n_dead_tup DESC LIMIT 15;
```

Прогресс идущего REINDEX (psql молчит до конца — смотри сюда):

```sql
SELECT index_relid::regclass, phase,
       round(100.0*blocks_done/nullif(blocks_total,0))::int AS pct
FROM pg_stat_progress_create_index;
```

## 3. Эмпирические пороги (по факту обслуживания 2026-08)

- `idx_pct` > 70% **или**heap на строку аномален (например 233k строк на 2.9 GB) → глубоко чистить;
- быстрые апдейты упёрлись (~30 строк/с при норме сотен): сначала смотреть wait_event
  (`IO/DataFileRead`) в `pg_stat_activity`, потом dead tuples → VACUUM FULL, потом REINDEX.

## 4. Утилита `pg-maintenance`

```bash
set -a; source .env; set +a   # PG_PWD обязателен
go run ./cmd/data-maintenance/pg-maintenance \
  --config cmd/.configs/download-all/pg-maintenance-PG.yaml [флаги]
```

| Флаг | Поведение |
|---|---|
| *(без флагов)* | `ANALYZE` + `VACUUM` всех таблиц — ровно это делает ночная Phase 7 |
| `--tables orders,sales` | точечно, порядок сохраняется из канонического списка фаз |
| `--reindex` | добавить `REINDEX` для heavy-update таблиц (со `--tables` — по списку) |
| `--full` | `VACUUM FULL` вместо обычного (внимание: лок, к одной таблице за вызов) |
| `--dry-run` | репетиция: подключение/валидация без выполнения |

Утилита сама снимает `statement_timeout`, скипает отсутствующие таблицы,
логирует reclaimed dead tuples / freed bytes. Крон должен вызывать её как Phase 7
(без `--reindex`: глубокое обслуживание — ручное, по показаниям §2).

## 5. Типовые сценарии командой psql

Тихое окно (загрузки не идут), несколько таблиц разом:

```bash
set -a; source .env; set +a
PGPASSWORD="$PG_PWD" psql -h "${PGHOST:-192.168.10.7}" -p "${PGPORT:-15432}" \
  -U "${PGUSER:-postgres}" -d wb_data_prod \
  -c "SET statement_timeout = 0" \
  -c "REINDEX TABLE operational_sales"      -c "ANALYZE operational_sales" \
  -c "VACUUM FULL orders"                   -c "ANALYZE orders"
```

Живая база (не блокируя запись):

```bash
  -c "SET statement_timeout = 0" \
  -c "REINDEX TABLE CONCURRENTLY warehouse_remains" -c "ANALYZE warehouse_remains"
```

После Completion-check:

```sql
-- битых индексов быть не должно:
SELECT indexrelid::regclass FROM pg_index WHERE NOT indisvalid;
```

## 6. Правила безопасности

1. Глубокие операции (FULL / блокирующий REINDEX) — только когда загрузчики стоят;
   иначе их INSERT'ы встанут за локом и умрут от statement_timeout (5 мин у пула).
2. `SET statement_timeout = 0` — отдельным стейтментом той же сессии psql.
3. Свободное место ≈ 2× размера объекта (переписывается копия).
4. REINDEX лечит индексы, VACUUM FULL лечит heap — они не взаимозаменяемы.
5. Никогда не обрывай `REINDEX CONCURRENTLY` по Ctrl-C без последующей проверки §5.
