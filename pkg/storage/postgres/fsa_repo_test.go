package postgres

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/fsa"
)

// ============================================================================
// Integration tests (real PostgreSQL). Gated on WB_TEST_PG — same convention
// as wbscraper_repo_test.go, so `go test ./...` skips them by default:
//
//	WB_TEST_PG=1 PG_PWD=... go test ./pkg/storage/postgres/ -run TestPgFSARepo -v
//
// Runs against wb_data_test (NEVER prod). Rows use a unique TEST-FSA- number
// prefix and are deleted on completion, so repeats against a shared test DB
// never collide with real check results.
// ============================================================================

func newPgFSATestRepo(t *testing.T) (*PgFSARepo, *Pool) {
	t.Helper()
	if !pgEnabled() {
		t.Skipf("skipping PostgreSQL integration test: set WB_TEST_PG=1 (and PG_PWD) to run")
	}
	ctx := context.Background()

	// Same DSN construction the validator uses (V2StorageConfig → GetEffectiveDSN),
	// so env overrides PGHOST/PGPORT/PGUSER apply; PGDATABASE is pinned to wb_data_test.
	dsn, err := config.V2StorageConfig{Backend: "postgres", PgDatabase: "wb_data_test"}.GetEffectiveDSN()
	if err != nil {
		t.Skipf("skipping: cannot build PG DSN (is PG_PWD set?): %v", err)
	}
	pool, err := NewPool(ctx, dsn)
	if err != nil {
		t.Skipf("skipping: cannot reach wb_data_test (%v) — is the PG test DB up?", err)
	}
	t.Cleanup(pool.Close)

	repo := NewPgFSARepo(pool.DB())
	if err := repo.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	return repo, pool
}

func fsaTestCleanup(t *testing.T, pool *Pool) {
	t.Helper()
	if _, err := pool.DB().Exec(context.Background(),
		`DELETE FROM fsa_certificates WHERE certificate_number LIKE 'TEST-FSA-%'`); err != nil {
		t.Errorf("cleanup fsa_certificates: %v", err)
	}
}

// fsaTestReset cleans prior residue AND registers an after-test cleanup, so the
// shared wb_data_test never accumulates TEST-FSA- rows across runs.
func fsaTestReset(t *testing.T, pool *Pool) {
	t.Helper()
	fsaTestCleanup(t, pool)
	t.Cleanup(func() { fsaTestCleanup(t, pool) })
}

func TestPgFSARepo_AppendChecksRoundTrip(t *testing.T) {
	repo, pool := newPgFSATestRepo(t)
	fsaTestReset(t, pool)
	ctx := context.Background()

	checkedAt := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	fsaID := int64(3254311)
	idStatus := 6
	checks := []fsa.CheckResult{
		{CertificateNumber: "TEST-FSA-found", DocKind: fsa.DocKindCertificate,
			FsaID: &fsaID, RegDate: "2023-05-25", EndDate: "24.05.2029", IDStatus: &idStatus,
			CertType: "Сертификат соответствия", ObjectType: "Серийный выпуск",
			Found: true, ExactMatch: true, CheckedAt: checkedAt},
		{CertificateNumber: "TEST-FSA-notfound", DocKind: fsa.DocKindDeclaration,
			Found: false, CheckedAt: checkedAt},
		{CertificateNumber: "TEST-FSA-skip", DocKind: fsa.DocKindCertificate,
			SkippedReason: "не-RU формат (KG/KZ/legacy)", CheckedAt: checkedAt},
		{CertificateNumber: "TEST-FSA-error", DocKind: fsa.DocKindCertificate,
			Error: "FSA API 503", CheckedAt: checkedAt},
	}

	n, err := repo.AppendChecks(ctx, checks)
	if err != nil {
		t.Fatalf("AppendChecks: %v", err)
	}
	if n != len(checks) {
		t.Fatalf("AppendChecks = %d, want %d", n, len(checks))
	}

	// Found row: every field round-trips.
	var fsaIDOut int64
	var idStatusOut int
	var regDate, endDate, certType, objectType string
	var found, exact bool
	err = pool.DB().QueryRow(ctx, `
		SELECT fsa_id, id_status, reg_date, end_date, cert_type, object_type, found, exact_match
		FROM fsa_certificates WHERE certificate_number = $1`, "TEST-FSA-found").
		Scan(&fsaIDOut, &idStatusOut, &regDate, &endDate, &certType, &objectType, &found, &exact)
	if err != nil {
		t.Fatalf("select found row: %v", err)
	}
	if fsaIDOut != fsaID || idStatusOut != idStatus || regDate != "2023-05-25" || endDate != "24.05.2029" ||
		certType != "Сертификат соответствия" || objectType != "Серийный выпуск" || !found || !exact {
		t.Errorf("found row round-trip mismatch: got fsa_id=%d id_status=%d reg=%q end=%q found=%v exact=%v",
			fsaIDOut, idStatusOut, regDate, endDate, found, exact)
	}

	// Not-found / skip / error rows: NULLs where expected, flags intact.
	for _, tc := range []struct {
		number string
		query  string
		verify func(*testing.T, int64, string, string, bool)
	}{
		{"TEST-FSA-notfound", `SELECT COALESCE(fsa_id,0), COALESCE(skipped_reason,''), COALESCE(error,''), found FROM fsa_certificates WHERE certificate_number=$1`,
			func(t *testing.T, id int64, skip, errStr string, found bool) {
				if id != 0 || skip != "" || errStr != "" || found {
					t.Errorf("notfound row: want NULLs + found=false, got fsa_id=%d skip=%q error=%q found=%v", id, skip, errStr, found)
				}
			}},
		{"TEST-FSA-skip", `SELECT COALESCE(fsa_id,0), COALESCE(skipped_reason,''), COALESCE(error,''), found FROM fsa_certificates WHERE certificate_number=$1`,
			func(t *testing.T, id int64, skip, errStr string, found bool) {
				if id != 0 || skip == "" || errStr != "" || found {
					t.Errorf("skip row: want skipped_reason set + NULLs, got fsa_id=%d skip=%q error=%q found=%v", id, skip, errStr, found)
				}
			}},
		{"TEST-FSA-error", `SELECT COALESCE(fsa_id,0), COALESCE(skipped_reason,''), COALESCE(error,''), found FROM fsa_certificates WHERE certificate_number=$1`,
			func(t *testing.T, id int64, skip, errStr string, found bool) {
				if id != 0 || skip != "" || errStr != "FSA API 503" || found {
					t.Errorf("error row: want error set + NULLs, got fsa_id=%d skip=%q error=%q found=%v", id, skip, errStr, found)
				}
			}},
	} {
		var id int64
		var skip, errStr string
		var f bool
		if err := pool.DB().QueryRow(ctx, tc.query, tc.number).Scan(&id, &skip, &errStr, &f); err != nil {
			t.Fatalf("select %s: %v", tc.number, err)
		}
		tc.verify(t, id, skip, errStr, f)
	}

	// Idempotent schema: re-running InitSchema on existing objects must not fail.
	if err := repo.InitSchema(ctx); err != nil {
		t.Errorf("InitSchema re-run (idempotency): %v", err)
	}
}

func TestPgFSARepo_AppendChecksChunking(t *testing.T) {
	repo, pool := newPgFSATestRepo(t)
	fsaTestReset(t, pool)
	ctx := context.Background()

	// 501 rows > pgFSAChunkSize (500) → two COPY chunks must land atomically per chunk.
	const total = pgFSAChunkSize + 1
	checks := make([]fsa.CheckResult, total)
	checkedAt := time.Now().UTC()
	for i := range checks {
		checks[i] = fsa.CheckResult{
			CertificateNumber: fmt.Sprintf("TEST-FSA-chunk-%04d", i),
			DocKind:           fsa.DocKindCertificate,
			Found:             false,
			CheckedAt:         checkedAt,
		}
	}

	n, err := repo.AppendChecks(ctx, checks)
	if err != nil {
		t.Fatalf("AppendChecks (chunked): %v", err)
	}
	if n != total {
		t.Fatalf("AppendChecks = %d, want %d", n, total)
	}

	var cnt int
	if err := pool.DB().QueryRow(ctx,
		`SELECT count(*) FROM fsa_certificates WHERE certificate_number LIKE 'TEST-FSA-chunk-%'`).
		Scan(&cnt); err != nil {
		t.Fatalf("count chunk rows: %v", err)
	}
	if cnt != total {
		t.Fatalf("chunked rows = %d, want %d", cnt, total)
	}
}

func TestAnalyticalRORoles(t *testing.T) {
	cases := []struct {
		name  string
		set   bool // env задан?
		value string
		want  []string
	}{
		{"unset → default mcp_ro", false, "", []string{"mcp_ro"}},
		{"empty set → grants off", true, "", nil},
		{"single role", true, "arm_ai_ro", []string{"arm_ai_ro"}},
		{"csv with spaces", true, " mcp_ro , arm_ai_ro ,", []string{"mcp_ro", "arm_ai_ro"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("ANALYTICAL_RO_ROLES", tc.value)
			} else {
				os.Unsetenv("ANALYTICAL_RO_ROLES")
			}
			got := analyticalRORoles()
			if len(got) != len(tc.want) {
				t.Fatalf("analyticalRORoles() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("analyticalRORoles() = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestQuoteIdent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"mcp_ro", `"mcp_ro"`},
		{`ev"il`, `"ev""il"`},
		{`x; DROP TABLE t; --`, `"x; DROP TABLE t; --"`},
	}
	for _, tc := range cases {
		if got := quoteIdent(tc.in); got != tc.want {
			t.Errorf("quoteIdent(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestPgFSARepo_AnalyticalReadonlyAccess verifies the actual contract: role
// mcp_ro can SELECT from the analytical view but cannot write. Requires the
// mcp_ro password via PG_RO_PWD (same var the readonly MCP server uses).
func TestPgFSARepo_AnalyticalReadonlyAccess(t *testing.T) {
	_, _ = newPgFSATestRepo(t) // InitSchema → grants applied

	pwd := os.Getenv("PG_RO_PWD")
	if pwd == "" {
		t.Skip("skipping: PG_RO_PWD not set (mcp_ro password) — cannot verify role access")
	}

	// DSN под mcp_ro: тот же host/port/db, что у основного DSN тестовой базы.
	base, err := config.V2StorageConfig{Backend: "postgres", PgDatabase: "wb_data_test"}.GetEffectiveDSN()
	if err != nil {
		t.Skipf("skipping: cannot build DSN: %v", err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse base DSN: %v", err)
	}
	u.User = url.UserPassword("mcp_ro", pwd)
	pool, err := NewPool(context.Background(), u.String())
	if err != nil {
		t.Skipf("skipping: cannot connect as mcp_ro (%v) — is PG_RO_PWD correct?", err)
	}
	t.Cleanup(pool.Close)
	ctx := context.Background()

	// SELECT из view — разрешён (исходная жалоба аудита: permission denied).
	var n int
	if err := pool.DB().QueryRow(ctx,
		`SELECT count(*) FROM analytical.certificate_validity`).Scan(&n); err != nil {
		t.Fatalf("mcp_ro SELECT analytical.certificate_validity: %v (grants not applied?)", err)
	}

	// Запись — запрещена: read-only контракт роли.
	_, err = pool.DB().Exec(ctx, `INSERT INTO public.fsa_certificates (certificate_number, doc_kind) VALUES ('RO-TEST', 'certificate')`)
	if err == nil {
		t.Fatal("mcp_ro INSERT must be denied, got nil error")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("INSERT error = %v, want permission denied", err)
	}
}
