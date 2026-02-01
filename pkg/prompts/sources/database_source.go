package sources

import (
	"database/sql"
	"fmt"
)

// DatabaseSource — загрузка промптов из SQL базы данных.
//
// Пример реализации для демонстрации OCP расширяемости.
// В продакшене может быть расширён для кэширования, connection pooling и т.д.
type DatabaseSource struct {
	db    *sql.DB
	table string
}

// NewDatabaseSource создаёт источник промптов из PostgreSQL.
//
// Параметры:
//   - db: *sql.DB с открытым соединением
//   - table: имя таблицы с промптами (default: "prompts")
//
// Структура таблицы (пример SQL):
//   CREATE TABLE prompts (
//       id VARCHAR(255) PRIMARY KEY,
//       system TEXT,
//       template TEXT,
//       variables JSONB,
//       metadata JSONB
//   );
func NewDatabaseSource(db *sql.DB, table string) *DatabaseSource {
	if table == "" {
		table = "prompts"
	}
	return &DatabaseSource{
		db:    db,
		table: table,
	}
}

// Load загружает промпт из базы данных по ID.
//
// Возвращает *PromptData для избежания циклического импорта.
func (s *DatabaseSource) Load(promptID string) (*PromptData, error) {
	// Query prompt by ID
	var system, template, variablesJSON, metadataJSON sql.NullString

	query := fmt.Sprintf(
		"SELECT system, template, variables, metadata FROM %s WHERE id = $1",
		s.table,
	)

	err := s.db.QueryRow(query, promptID).Scan(&system, &template, &variablesJSON, &metadataJSON)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("prompt '%s' not found in table '%s'", promptID, s.table)
	}
	if err != nil {
		return nil, fmt.Errorf("database query failed: %w", err)
	}

	// Parse JSON fields (simplified — в продакшене использовать json.Unmarshal)
	file := &PromptData{
		System:    system.String,
		Template:  template.String,
		Variables: make(map[string]string),
		Metadata:  make(map[string]any),
	}

	// TODO: Parse variablesJSON and metadataJSON if not null
	// Для простоты пропущено в этом примере

	return file, nil
}
