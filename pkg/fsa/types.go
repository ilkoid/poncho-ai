// Package fsa — домен ФГИС РОСАККРЕДИТАЦИИ (pub.fsa.gov.ru):
// проверка сертификатов и деклараций соответствия по госреестру.
//
// CheckResult — RAW-запись одного поиска одного номера: поля хранятся как
// вернул API (даты TEXT без нормализации). Производные (verdict,
// days_remaining) не хранятся — их считает view analytical.certificate_validity.
//
// Этап 1 (этот пакет): типы + Writer для персиста результатов валидатором
// cmd/data-analyzers/validate-certificates. Этап 2: переезд FSAClient сюда
// и инкрементальный downloader.
package fsa

import (
	"context"
	"time"
)

// DocKind — тип разрешительного документа; определяет endpoint реестра ФСА.
const (
	DocKindCertificate = "certificate" // POST /api/v1/rss/common/certificates/get
	DocKindDeclaration = "declaration" // POST /api/v1/rds/common/declarations/get
)

// CheckResult maps to public.fsa_certificates (append-only, grain: один поиск одного номера).
type CheckResult struct {
	CertificateNumber string
	DocKind           string // certificate | declaration; для пропущенных — как собирались искать
	FsaID             *int64 // NULL = не найден / ошибка / пропуск
	RegDate           string // как вернул API ("2023-05-25" или "28.12.2024")
	EndDate           string // как вернул API ("24.05.2026" или "2029-12-26")
	IDStatus          *int   // код статуса ФСА 1..14 (bounded → INTEGER)
	CertType          string
	ObjectType        string
	Found             bool
	ExactMatch        bool   // false = взят первый результат поиска (approx)
	SkippedReason     string // непусто = номер не искали (не-RU формат KG/KZ/legacy)
	Error             string // непусто = API-запрос упал (сеть / 503 / токен)
	CheckedAt         time.Time
}

// Writer — персист результатов проверки (ISP: только методы, вызываемые потребителем).
type Writer interface {
	// AppendChecks добавляет результаты проверок (append-only, без upsert).
	AppendChecks(ctx context.Context, checks []CheckResult) (int, error)
}
