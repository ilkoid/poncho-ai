package cardupdate

import "database/sql"

// EnsureCardsMigrations применяет идемпотентные миграции карточных таблиц для
// соединений, открытых напрямую (sql.Open), мимо NewSQLiteSalesRepository.
//
// Мотивация (12.10): LoadFullCard читает cards.exclude_documents и card_documents;
// база, созданная бинарём до documents-фикса, их не имеет → «no such column/table».
// Репозиторий применяет те же DDL при открытии (repository.go), фиксерам с raw-open
// нужен этот хелпер. Ошибки игнорируются сознательно: ALTER на существующей колонке
// и CREATE IF NOT EXISTS безвредны, а настоящая проблема (нет таблицы cards) всплывёт
// первым же запросом LoadFullCard с внятной ошибкой.
func EnsureCardsMigrations(db *sql.DB) {
	_, _ = db.Exec("ALTER TABLE cards ADD COLUMN kiz_marked INTEGER")
	_, _ = db.Exec("ALTER TABLE cards ADD COLUMN exclude_documents INTEGER")
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS card_documents (
		nm_id INTEGER NOT NULL,
		doc_id TEXT NOT NULL,
		type INTEGER NOT NULL DEFAULT 0,
		number TEXT NOT NULL DEFAULT '',
		product_number TEXT NOT NULL DEFAULT '',
		trade_name TEXT NOT NULL DEFAULT '',
		applicant TEXT NOT NULL DEFAULT '',
		start_date TEXT NOT NULL DEFAULT '',
		end_date TEXT NOT NULL DEFAULT '',
		is_endless INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (nm_id, doc_id)
	)`)
}
