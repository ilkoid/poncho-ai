// fsa_persist.go — маппинг результатов валидации в RAW-записи fsa.CheckResult
// и запись их в PostgreSQL (public.fsa_certificates).
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/fsa"
	"github.com/ilkoid/poncho-ai/pkg/storage/postgres"
)

// docKindOf маршрутизирует тип документа по строке типа из 1С.
func docKindOf(certificateType string) string {
	if isDeclaration(certificateType) {
		return fsa.DocKindDeclaration
	}
	return fsa.DocKindCertificate
}

// checkResultFromFSA — RAW-строка поиска номера в реестре; res == nil → не найден.
func checkResultFromFSA(number, certificateType string, res *FSASearchResult, exact bool, checkedAt time.Time) fsa.CheckResult {
	cr := fsa.CheckResult{
		CertificateNumber: number,
		DocKind:           docKindOf(certificateType),
		Found:             res != nil,
		ExactMatch:        exact,
		CheckedAt:         checkedAt,
	}
	if res != nil {
		fsaID := int64(res.ID)
		cr.FsaID = &fsaID
		cr.RegDate = res.RegDate
		cr.EndDate = res.EndDate
		idStatus := res.StatusID
		cr.IDStatus = &idStatus
		cr.CertType = res.CertType
		cr.ObjectType = res.ObjectType
	}
	return cr
}

// skippedCheck — номер не искали (не-RU формат: KG/KZ/legacy).
func skippedCheck(number, certificateType string, checkedAt time.Time) fsa.CheckResult {
	return fsa.CheckResult{
		CertificateNumber: number,
		DocKind:           docKindOf(certificateType),
		SkippedReason:     "не-RU формат (KG/KZ/legacy) — отсутствует в реестре ФСА",
		CheckedAt:         checkedAt,
	}
}

// errorCheck — API-запрос упал.
func errorCheck(number, certificateType, searchErr string, checkedAt time.Time) fsa.CheckResult {
	return fsa.CheckResult{
		CertificateNumber: number,
		DocKind:           docKindOf(certificateType),
		Error:             searchErr,
		CheckedAt:         checkedAt,
	}
}

// persistChecks пишет RAW-результаты в PostgreSQL: пул → InitSchema → AppendChecks.
func persistChecks(ctx context.Context, storage config.V2StorageConfig, checks []fsa.CheckResult) (int, error) {
	dsn, err := storage.GetEffectiveDSN()
	if err != nil {
		return 0, fmt.Errorf("DSN: %w", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return 0, fmt.Errorf("pg pool: %w", err)
	}
	defer pool.Close()

	repo := postgres.NewPgFSARepo(pool)
	if err := repo.InitSchema(ctx); err != nil {
		return 0, fmt.Errorf("init schema: %w", err)
	}
	return repo.AppendChecks(ctx, checks)
}
