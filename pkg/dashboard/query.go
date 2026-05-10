package dashboard

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
)

// OpenReadOnlyDB открывает SQLite в режиме только чтения.
//
// Использует mode=ro для гарантии неизменности данных дашбордом.
// MaxOpenConns(2) — дашборд однопоточный, 2 соединения достаточно
// (одно для запросов, одно для pragma).
func OpenReadOnlyDB(dbPath string) (*sql.DB, error) {
	db, err := sqlite.OpenReadOnly(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	return db, nil
}

// QueryRows выполняет запрос и возвращает строки через callback.
//
// Callback получает *sql.Rows, caller отвечает на Rows.Next()/Scan().
// Rows автоматически закрываются при выходе.
func QueryRows(db *sql.DB, query string, args []any, fn func(rows *sql.Rows) error) error {
	rows, err := db.Query(query, args...)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	if err := fn(rows); err != nil {
		return err
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows iteration: %w", err)
	}

	return nil
}

// QueryStringList возвращает список строк из одноколоночного SELECT.
func QueryStringList(db *sql.DB, query string, args ...any) ([]string, error) {
	var result []string
	err := QueryRows(db, query, args, func(rows *sql.Rows) error {
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return err
			}
			result = append(result, s)
		}
		return nil
	})
	return result, err
}

// QueryMaxDate возвращает MAX(date_column) из таблицы.
func QueryMaxDate(db *sql.DB, table, dateColumn string) (string, error) {
	var date string
	err := db.QueryRow(fmt.Sprintf("SELECT MAX(%s) FROM %s", dateColumn, table)).Scan(&date)
	return date, err
}

// QuerySingleRow выполняет запрос и вызывает fn для первой строки.
func QuerySingleRow(db *sql.DB, query string, args []any, fn func(row *sql.Row) error) error {
	row := db.QueryRow(query, args...)
	return fn(row)
}

// QueryInt executes a query that returns a single integer.
func QueryInt(db *sql.DB, query string, args ...any) (int, error) {
	var n int
	err := db.QueryRow(query, args...).Scan(&n)
	return n, err
}

// QueryDistinctValues возвращает DISTINCT значения из колонки.
func QueryDistinctValues(db *sql.DB, table, column string) ([]string, error) {
	q := fmt.Sprintf("SELECT DISTINCT %s FROM %s WHERE %s IS NOT NULL AND %s != '' ORDER BY %s", column, table, column, column, column)
	return QueryStringList(db, q)
}

// --- QueryBuilder: динамическая сборка WHERE ---

// QueryBuilder собирает SQL запрос с динамическими условиями WHERE.
//
// Паттерн: задаём базовый запрос с WHERE 1=1, затем добавляем
// условия через Where(). Build() возвращает итоговый запрос и args.
type QueryBuilder struct {
	base       string
	conditions []string
	args       []any
}

// NewQuery создаёт QueryBuilder с базовым запросом.
func NewQuery(base string) *QueryBuilder {
	return &QueryBuilder{base: base}
}

// Where добавляет условие, если arg не пустой.
//
// Типы аргументов:
//   - string: добавляется как ? параметр, пропускается если пустая.
//   - bool: условие — статический SQL (без ?), добавляется если true.
//   - nil: условие — статический SQL (без ?), добавляется всегда.
func (q *QueryBuilder) Where(condition string, arg any) *QueryBuilder {
	switch v := arg.(type) {
	case string:
		if v == "" {
			return q
		}
		q.conditions = append(q.conditions, condition)
		q.args = append(q.args, v)
	case bool:
		if !v {
			return q
		}
		// Статическое условие без параметра
		q.conditions = append(q.conditions, condition)
	case nil:
		// Статическое условие без параметра
		q.conditions = append(q.conditions, condition)
	}
	return q
}

// Build возвращает итоговый SQL и slice аргументов.
func (q *QueryBuilder) Build() (string, []any) {
	if len(q.conditions) == 0 {
		return q.base, q.args
	}

	result := q.base
	for _, cond := range q.conditions {
		result += " " + cond
	}
	return result, q.args
}

// AttachDB подключает дополнительную SQLite БД через ATTACH DATABASE.
//
// После вызова таблицы подключённой БД доступны как alias.table_name.
func AttachDB(db *sql.DB, alias, dbPath string) error {
	_, err := db.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS %s", dbPath, alias))
	if err != nil {
		return fmt.Errorf("attach %s: %w", alias, err)
	}
	return nil
}

// LogQuery логирует запрос и args (для debug).
func LogQuery(query string, args []any) {
	log.Printf("[dashboard] query: %s args: %v", query, args)
}
