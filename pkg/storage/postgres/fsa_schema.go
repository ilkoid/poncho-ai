package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fsaTableSQL — RAW-таблица результатов проверок реестра ФСА.
//
// Grain: один поиск одного номера (append-only, история смен статусов бесплатно).
// Даты reg_date/end_date — TEXT как вернул API; нормализация — задача view
// analytical.certificate_validity, не хранилища (RAW без бизнес-логики).
const fsaTableSQL = `
-- ============================================================================
-- FSA CERTIFICATES (ФГИС РОСАККРЕДИТАЦИИ check results, RAW)
-- Источник: pub.fsa.gov.ru API (cmd/data-analyzers/validate-certificates)
-- Grain: один поиск одного номера сертификата/декларации
-- ============================================================================
CREATE TABLE IF NOT EXISTS fsa_certificates (
    id                  BIGSERIAL PRIMARY KEY,
    certificate_number  TEXT    NOT NULL,
    doc_kind            TEXT    NOT NULL,   -- certificate | declaration
    fsa_id              BIGINT,             -- NULL = не найден / ошибка / пропуск
    reg_date            TEXT    DEFAULT '', -- как вернул API: "2023-05-25" или "28.12.2024"
    end_date            TEXT    DEFAULT '', -- как вернул API: "24.05.2026" или "2029-12-26"
    id_status           INTEGER,            -- код статуса ФСА 1..14 (bounded → INTEGER)
    cert_type           TEXT    DEFAULT '',
    object_type         TEXT    DEFAULT '',
    found               BOOLEAN NOT NULL DEFAULT FALSE,
    exact_match         BOOLEAN NOT NULL DEFAULT FALSE, -- false = approx (первый результат)
    skipped_reason      TEXT    NOT NULL DEFAULT '',    -- непусто = не искали (KG/KZ/legacy)
    error               TEXT    NOT NULL DEFAULT '',    -- непусто = API-запрос упал
    checked_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

const fsaIndexSQL = `
CREATE INDEX IF NOT EXISTS idx_fsa_certificates_number
    ON fsa_certificates (certificate_number, checked_at DESC);
`

// analyticalSchemaSQL создаёт схему аналитического слоя (первый перенос аналитики в PG).
const analyticalSchemaSQL = `CREATE SCHEMA IF NOT EXISTS analytical`

// certificateValidityViewSQL — тонкая проекция «состояние сертификатов» поверх RAW.
//
// DISTINCT ON берёт последнюю проверку номера; verdict/days_remaining/date_match
// вычисляются на лету (всегда свежи относительно CURRENT_DATE). Приоритеты verdict
// зеркалят report.go:verdict(): error → skip → approx → not found → истёкшие
// статусы → дни ≤ 0 → ≤ 30 дней → ок; неизвестные статусы → на ручную проверку.
const certificateValidityViewSQL = `
CREATE OR REPLACE VIEW analytical.certificate_validity AS
WITH last_check AS (
    SELECT DISTINCT ON (certificate_number) *
    FROM public.fsa_certificates
    ORDER BY certificate_number, checked_at DESC
),
base AS (
    SELECT
        og.article,
        og.name                          AS good_name,
        og.type                          AS onec_type,
        og.category_level1_name          AS onec_category,
        og.certificate_type,
        og.certificate_number,
        CASE
            WHEN og.certificate_end ~ '^\d{4}-\d{2}-\d{2}'
                THEN substring(og.certificate_end from 1 for 10)::date
        END AS local_end,
        CASE
            WHEN lc.end_date ~ '^\d{2}\.\d{2}\.\d{4}$' THEN to_date(lc.end_date, 'DD.MM.YYYY')
            WHEN lc.end_date ~ '^\d{4}-\d{2}-\d{2}'   THEN substring(lc.end_date from 1 for 10)::date
        END AS fsa_end,
        lc.doc_kind,
        lc.id_status,
        CASE lc.id_status
            WHEN 1  THEN 'Архивный'
            WHEN 2  THEN 'Приостановлен'
            WHEN 3  THEN 'Возобновлён'
            WHEN 4  THEN 'Прекращён'
            WHEN 5  THEN 'Аннулирован'
            WHEN 6  THEN 'Действующий'
            WHEN 7  THEN 'Продлён'
            WHEN 8  THEN 'Внесены изменения'
            WHEN 10 THEN 'На рассмотрении'
            WHEN 11 THEN 'Зарегистрирован'
            WHEN 12 THEN 'Выдан'
            WHEN 14 THEN 'Окончание срока'
        END AS fsa_status,
        lc.found,
        lc.exact_match,
        lc.skipped_reason,
        lc.error,
        lc.checked_at
    FROM public.onec_goods og
    LEFT JOIN last_check lc USING (certificate_number)
    WHERE og.has_certificate
      AND og.certificate_number <> ''
)
SELECT
    b.*,
    CASE WHEN b.fsa_end IS NOT NULL THEN b.fsa_end - CURRENT_DATE END AS days_remaining,
    CASE
        WHEN b.local_end IS NULL OR b.fsa_end IS NULL THEN '—'
        WHEN b.local_end = b.fsa_end THEN '✓'
        ELSE '✗'
    END AS date_match,
    CASE
        WHEN b.checked_at IS NULL                                THEN '⏳ Не проверялся'
        WHEN b.error <> ''                                       THEN '⛔ Ошибка'
        WHEN b.skipped_reason <> ''                              THEN '🔧 KG/KZ'
        WHEN b.found AND NOT b.exact_match                       THEN '❓ Неточный'
        WHEN NOT b.found                                         THEN '❓ Не найден'
        WHEN b.id_status IN (1, 4, 5, 14)                        THEN '❌ Протух'
        WHEN b.id_status NOT IN (6, 3, 7)                        THEN '❓ Не найден'
        WHEN b.fsa_end IS NULL                                   THEN '❓ Не найден'
        WHEN b.fsa_end <= CURRENT_DATE                           THEN '❌ Протух'
        WHEN b.fsa_end <= CURRENT_DATE + 30                      THEN '⚠️ Скоро истекает'
        ELSE '✅ Ок'
    END AS verdict
FROM base b;
`

// initFSASchema создаёт RAW-таблицу fsa_certificates, схему analytical и view
// certificate_validity, затем выдаёт read-only права на analytical (доктрина
// dev_data_layers.md: «GRANT SELECT ... когда созреет»). Каждый DDL — отдельный
// Exec: pgx не парсит CASE внутри multi-statement блока (гатч pgx.Exec, AGENTS.md).
func initFSASchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, fsaTableSQL); err != nil {
		return fmt.Errorf("fsa table: %w", err)
	}
	if _, err := pool.Exec(ctx, fsaIndexSQL); err != nil {
		return fmt.Errorf("fsa index: %w", err)
	}
	if _, err := pool.Exec(ctx, analyticalSchemaSQL); err != nil {
		return fmt.Errorf("analytical schema: %w", err)
	}
	if _, err := pool.Exec(ctx, certificateValidityViewSQL); err != nil {
		return fmt.Errorf("certificate_validity view: %w", err)
	}
	return grantAnalyticalReadonly(ctx, pool)
}

// analyticalRORoles — read-only роли для доступа к схеме analytical.
// Источник — env ANALYTICAL_RO_ROLES (через запятую); не задан → default mcp_ro;
// задан пустым → гранты отключены (явный opt-out). Роли, которых нет в pg_roles,
// пропускаются молча — InitSchema не должен ломаться на отсутствующей роли хоста.
func analyticalRORoles() []string {
	raw, ok := os.LookupEnv("ANALYTICAL_RO_ROLES")
	if !ok {
		raw = "mcp_ro"
	}
	var roles []string
	for _, r := range strings.Split(raw, ",") {
		if r = strings.TrimSpace(r); r != "" {
			roles = append(roles, r)
		}
	}
	return roles
}

// quoteIdent экранирует идентификатор роли для подстановки в GRANT (env-источник
// → инъекции исключены двойными кавычками).
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// grantAnalyticalReadonly выдаёт USAGE на схему analytical + SELECT на все её
// таблицы/view + DEFAULT PRIVILEGES на будущие объекты. Ожидает исполнителя —
// владельца схемы (проектная конвенция: DSN строится под postgres). Гранты
// идемпотентны; ALTER без FOR ROLE привязывает default-права к создателю
// объектов — тому же роли, что выполняет InitSchema.
func grantAnalyticalReadonly(ctx context.Context, pool *pgxpool.Pool) error {
	for _, role := range analyticalRORoles() {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).
			Scan(&exists); err != nil {
			return fmt.Errorf("check role %s: %w", role, err)
		}
		if !exists {
			continue
		}
		stmts := []string{
			fmt.Sprintf(`GRANT USAGE ON SCHEMA analytical TO %s`, quoteIdent(role)),
			fmt.Sprintf(`GRANT SELECT ON ALL TABLES IN SCHEMA analytical TO %s`, quoteIdent(role)),
			fmt.Sprintf(`ALTER DEFAULT PRIVILEGES IN SCHEMA analytical GRANT SELECT ON TABLES TO %s`, quoteIdent(role)),
		}
		for _, stmt := range stmts {
			if _, err := pool.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("grant to %s [%s]: %w", role, stmt, err)
			}
		}
	}
	return nil
}
