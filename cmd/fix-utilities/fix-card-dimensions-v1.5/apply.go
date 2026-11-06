package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/cardupdate"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// wbUpdateClient — узкое отверстие к WB вместо *wb.Client: даёт фейк в тестах
// (зависимость от интерфейса, а не от конкретного типа SDK).
type wbUpdateClient interface {
	UpdateCards(ctx context.Context, baseURL string, rateLimit, burst int, cards []wb.CardUpdateItem) (string, string, error)
	GetCardErrorsList(ctx context.Context, baseURL string, rateLimit, burst int, req wb.CardErrorsListRequest) ([]wb.CardErrorItem, error)
}

// Гарантия: реальный клиент удовлетворяет интерфейсу (ловит дрейф сигнатур).
var _ wbUpdateClient = (*wb.Client)(nil)

// cardSend — неразрывная пара строка staging ↔ готовый payload: бисект режет их
// вместе, исключая рассинхрон параллельных слайсов (в items из-за build-ошибок
// попадают не все строки чанка).
type cardSend struct {
	row  stagedDimRow
	item wb.CardUpdateItem
}

// maxConsecutiveSingleRejects — предохранитель деградации: после стольких
// одиночных WB-отказов подряд без единого успешного POST за прогон остаток
// батча помечается error без отправки. Профиль «мёртвого сервера»: ~8-10 POST
// вместо ~155 полного бисекта (у WB 4XX-запросы тарифицируются с коэффициентом).
// Именованная константа поведения, не лимит API.
const maxConsecutiveSingleRejects = 3

// bisectState — состояние авто-бисекта на один прогон apply.
type bisectState struct {
	singleRejects int    // одиночные WB-отказы подряд; любой успех сбрасывает
	aborted       bool   // сервер признан мёртвым: остаток не отправляется
	lastText      string // последний errorText — в сообщение unattributed-ошибок
}

// applyRun — состояние одного прогона apply: доступы, счётчики, предохранитель.
type applyRun struct {
	db               *sql.DB
	client           wbUpdateClient
	cfg              *Config
	bisect           bisectState
	sent             int
	failed           int
	skippedHollow    int
	appliedThisChunk []cardSend
}

// buildDimensionUpdatePayload creates a complete CardUpdateItem with updated dimensions.
func buildDimensionUpdatePayload(ctx context.Context, u *cardupdate.CardUpdater, staged stagedDimRow) (wb.CardUpdateItem, error) {
	card, err := u.LoadFullCard(ctx, staged.NmID)
	if err != nil {
		return wb.CardUpdateItem{}, fmt.Errorf("load full card: %w", err)
	}

	item := cardupdate.ToUpdateItem(card)
	item.Dimensions = &wb.CardDimensionsUpdate{
		Length:       staged.NewLength,
		Width:        staged.NewWidth,
		Height:       staged.NewHeight,
		WeightBrutto: staged.NewWeight,
	}
	return item, nil
}

// runApply sends staged dimension updates to WB API (or prints payloads in dry-run mode).
//
// Честный exit: при failed>0 (или ошибке read-after-write) возвращается ошибка —
// прежде runApply возвращал nil при поломке, и статус-файл ночного скрипта
// 8 ночей писал «OK» при 0 применённых (инцидент 28.10–05.11).
// skipped_hollow — не провал: неполные карточки ждут дозаполнения и возвращаются
// следующей ночью (staging пересоздаётся каждым --stage).
func runApply(ctx context.Context, db *sql.DB, client wbUpdateClient, cfg *Config, dryRun, allowHollow bool) error {
	if !dryRun {
		if err := checkSnapshotFreshness(ctx, db, cfg.maxSnapshotAgeHours()); err != nil {
			return err
		}
	}

	batch, err := loadPendingStaging(ctx, db)
	if err != nil {
		return err
	}
	if len(batch) == 0 {
		fmt.Println("No rows to apply. Run --stage first or check status.")
		return nil
	}

	a := &applyRun{db: db, client: client, cfg: cfg}
	cards, err := a.buildAll(ctx, cardupdate.NewCardUpdater(db), batch, allowHollow)
	if err != nil {
		return err
	}

	fmt.Printf("Applying %d cards (batch size %d, dry-run=%v): %d to send, %d skipped_hollow, %d failed-build\n",
		len(batch), cfg.WBUpdate.BatchSize, dryRun, len(cards), a.skippedHollow, a.failed)

	if dryRun {
		a.dryRunPrint(cards)
		return nil
	}

	var stopErr error
	bs := cfg.WBUpdate.BatchSize
	for i := 0; i < len(cards) && stopErr == nil; i += bs {
		end := min(i+bs, len(cards))
		a.appliedThisChunk = a.appliedThisChunk[:0]
		a.sendChunk(ctx, cards[i:end])

		// read-after-write один раз на top-level чанк по применённому
		// подмножеству (не после каждого листа бисекта — лимит общий).
		if len(a.appliedThisChunk) > 0 {
			if err := checkWBErrors(ctx, client, cfg, rowsOf(a.appliedThisChunk)); err != nil {
				log.Printf("  STOPPING: %v", err)
				stopErr = err
			}
		}

		if i+bs < len(cards) {
			time.Sleep(time.Duration(cfg.WBUpdate.IntervalSeconds) * time.Second)
		}
	}

	fmt.Printf("\nDone: %d sent, %d failed, %d skipped_hollow\n", a.sent, a.failed, a.skippedHollow)
	switch {
	case stopErr != nil:
		return stopErr
	case a.failed > 0:
		return fmt.Errorf("apply: %d карточек не применено — см. fix_card_dimensions_staging", a.failed)
	}
	return nil
}

// loadPendingStaging — pending-строки staging (запрос без изменений против
// прежнего инлайн-кода runApply).
func loadPendingStaging(ctx context.Context, db *sql.DB) ([]stagedDimRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT nm_id, vendor_code, old_length, old_width, old_height, old_weight,
		       new_length, new_width, new_height, new_weight, status, error_msg
		FROM fix_card_dimensions_staging
		WHERE status = 'pending'
		ORDER BY nm_id
	`)
	if err != nil {
		return nil, fmt.Errorf("query staging: %w", err)
	}
	defer rows.Close()

	var batch []stagedDimRow
	for rows.Next() {
		var r stagedDimRow
		var errMsg sql.NullString
		if err := rows.Scan(
			&r.NmID, &r.VendorCode,
			&r.OldLength, &r.OldWidth, &r.OldHeight, &r.OldWeight,
			&r.NewLength, &r.NewWidth, &r.NewHeight, &r.NewWeight,
			&r.Status, &errMsg,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if errMsg.Valid {
			r.ErrorMsg = errMsg.String
		}
		batch = append(batch, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return batch, nil
}

// buildAll строит payload для каждой pending-строки; неполные карточки (гард
// неполноты) уходят в skipped_hollow с поимённой причиной — в ОБЕИХ режимах
// (прецедент: build-ошибки и прежде писали статус в dry-run).
func (a *applyRun) buildAll(ctx context.Context, u *cardupdate.CardUpdater, batch []stagedDimRow, allowHollow bool) ([]cardSend, error) {
	out := make([]cardSend, 0, len(batch))
	for _, r := range batch {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item, err := buildDimensionUpdatePayload(ctx, u, r)
		if err != nil {
			log.Printf("  ERROR build payload nm_id=%d: %v", r.NmID, err)
			a.markStatus(ctx, r.NmID, "error", err.Error())
			a.failed++
			continue
		}
		if !allowHollow {
			if reasons := hollowCardReasons(item); len(reasons) > 0 {
				msg := "hollow: " + strings.Join(reasons, "; ")
				log.Printf("  SKIP hollow nm_id=%d %s: %s", r.NmID, r.VendorCode, msg)
				a.markStatus(ctx, r.NmID, "skipped_hollow", msg)
				a.skippedHollow++
				continue
			}
		}
		out = append(out, cardSend{row: r, item: item})
	}
	return out, nil
}

// hollowCardReasons — гард неполноты на границе с WB API (dev_best_practices.md:35):
// валидирует ПОСТРОЕННЫЙ payload (после Smart Merge). Неполная карточка (пустой
// description/wbSize/баркоды и т.п.) отравляла весь батч — WB отвечал 400
// «Invalid request format» на все 76-101 карточки скопом (инцидент 28.10–05.11).
// Инвариант: nil ⇔ payload полный. Причины — человекочитаемые, уходят в staging.
func hollowCardReasons(item wb.CardUpdateItem) []string {
	var reasons []string
	if strings.TrimSpace(item.Brand) == "" {
		reasons = append(reasons, "brand пустой")
	}
	if strings.TrimSpace(item.Title) == "" {
		reasons = append(reasons, "title пустой")
	}
	if strings.TrimSpace(item.Description) == "" {
		reasons = append(reasons, "description пустой")
	}
	if len(item.Sizes) == 0 {
		reasons = append(reasons, "sizes пустой")
	} else {
		var noChrt, noWbSize, noSkus int
		for _, s := range item.Sizes {
			if s.ChrtID <= 0 {
				noChrt++
			}
			if strings.TrimSpace(s.WBSize) == "" {
				noWbSize++
			}
			if len(s.Skus) == 0 {
				noSkus++
			}
		}
		if noChrt > 0 {
			reasons = append(reasons, fmt.Sprintf("chrtID=0 у %d размеров", noChrt))
		}
		if noWbSize > 0 {
			reasons = append(reasons, fmt.Sprintf("wbSize пустой у %d размеров", noWbSize))
		}
		if noSkus > 0 {
			reasons = append(reasons, fmt.Sprintf("нет баркодов у %d размеров", noSkus))
		}
	}
	if d := item.Dimensions; d == nil || d.Length <= 0 || d.Width <= 0 || d.Height <= 0 || d.WeightBrutto <= 0 {
		reasons = append(reasons, "dimensions неполные")
	}
	for _, ch := range item.Characteristics {
		if ch.ID <= 0 || !charcValueFilled(ch.Value) {
			reasons = append(reasons, fmt.Sprintf("характеристика id=%d без значения", ch.ID))
			break
		}
	}
	return reasons
}

// charcValueFilled — непустое значение характеристики (Value interface{}:
// после UnwrapValue бывает string / []any / число).
func charcValueFilled(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(t) != ""
	case []any:
		return len(t) > 0
	default:
		return true
	}
}

// isWBRejectionError отличает WB-уровневый отказ от транспортного сбоя по
// стабильному формату ошибки нашего клиента. Ночь 06.11 показала: при HTTP
// 4xx клиент возвращает errorText="" (тело ответа зашито в текст err,
// pkg/wb/content.go:576-578) — триггер «errorText != ""» был мёртв, все
// отказы неверно помечались «транспорт». Формат HTTP-обёртки стабилен:
// «wb api error: status NNN, body: …» (pkg/wb/client.go). 429 исключаем —
// это исчерпанный рейт-лимит (погода, ретраи уже исчерпаны внутри клиента),
// а не валидационный отказ: бисект по нему не атрибутирует яд.
func isWBRejectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "wb api error: status 4") && !strings.Contains(msg, "status 429")
}

// sendChunk — POST чанка; при WB-уровневом отказе (errorText != "" ИЛИ
// HTTP-обёртка 4xx в тексте ошибки — см. isWBRejectionError) делит пополам
// до одиночных карточек: чистые под-чанки применяются, яд получает
// поимённый error со своим nm_id. Транспортные сбои бисектом не являются.
//
// Инварианты рекурсии (проверены мысленным экспериментом, см. тесты):
//   - завершимость: рекурсия только при len ≥ 2; потомки floor(n/2) и
//     n-floor(n/2), оба ≥1 и <n → глубина ≤ ⌈log2 n⌉+1 (5 кадров при batch 20);
//   - покрытие: потомки [:mid] и [mid:] не пересекаются и в объединении равны
//     срезу; ветвь сплита ничего не помечает сама → каждая карточка получает
//     ровно один терминальный статус, sent+failed+skipped == размер выборки;
//   - память: подслайсы — views одного backing array из buildAll, горутин нет.
func (a *applyRun) sendChunk(ctx context.Context, cards []cardSend) {
	if len(cards) == 0 {
		return
	}
	if err := ctx.Err(); err != nil {
		a.failAll(ctx, cards, "cancel: "+err.Error())
		return
	}
	if a.bisect.aborted {
		a.failAll(ctx, cards, "WB-отказ без атрибуции (стоп после 3 одиночных при 0 успехов): "+a.bisect.lastText)
		return
	}

	_, errorText, err := a.client.UpdateCards(ctx, wb.CardsBaseURL,
		a.cfg.WBUpdate.RatePerMin, a.cfg.WBUpdate.RateBurst, itemsOf(cards))
	wbReject := err != nil && (errorText != "" || isWBRejectionError(err))
	detail := errorText
	if detail == "" && err != nil {
		detail = err.Error() // тело ответа WB зашито клиентом в текст ошибки
	}
	switch {
	case err == nil:
		for _, c := range cards {
			a.markStatus(ctx, c.row.NmID, "applied", "")
		}
		a.sent += len(cards)
		a.appliedThisChunk = append(a.appliedThisChunk, cards...)
		a.bisect.singleRejects = 0 // любой успех сбрасывает счётчик деградации
		fmt.Printf("  batch %d-%d: OK (%d cards)\n", cards[0].row.NmID, cards[len(cards)-1].row.NmID, len(cards))
	case !wbReject:
		// Транспорт/исчерпанные 429 — транзиентно: чанк целиком в error,
		// бисекта нет (не наказывать хорошие карточки за погоду).
		log.Printf("  batch %d cards: %v", len(cards), err)
		a.failAll(ctx, cards, "транспорт: "+err.Error())
	case len(cards) == 1:
		a.markStatus(ctx, cards[0].row.NmID, "error", "WB: "+detail)
		a.failed++
		a.noteSingleReject(detail)
	default:
		mid := len(cards) / 2
		a.sendChunk(ctx, cards[:mid])
		a.sendChunk(ctx, cards[mid:])
	}
}

// noteSingleReject фиксирует одиночный WB-отказ; maxConsecutiveSingleRejects
// подряд без единого успеха за прогон взводят abort (мёртвый сервер).
// Trade-off: 3+ неизвестных ядов подряд до первого успеха тоже взводят abort —
// оставшиеся карточки уедут следующей ночью (staging пересоздаётся), потери нет.
func (a *applyRun) noteSingleReject(errorText string) {
	a.bisect.singleRejects++
	a.bisect.lastText = errorText
	if a.bisect.singleRejects >= maxConsecutiveSingleRejects && a.sent == 0 {
		a.bisect.aborted = true
	}
}

// failAll помечает срез ошибкой и увеличивает счётчик провала.
func (a *applyRun) failAll(ctx context.Context, cards []cardSend, msg string) {
	for _, c := range cards {
		a.markStatus(ctx, c.row.NmID, "error", msg)
	}
	a.failed += len(cards)
}

// markStatus — updateStagingStatus с видимым сбоем записи
// (прежде ошибки записи статусов молча глотались всеми вызовами).
func (a *applyRun) markStatus(ctx context.Context, nmID int, status, msg string) {
	if err := updateStagingStatus(ctx, a.db, nmID, status, msg); err != nil {
		log.Printf("  WARN: staging status nm_id=%d -> %s: %v", nmID, status, err)
	}
}

// dryRunPrint печатает батчи payload'ов как прежний dry-run (WB не зовётся).
func (a *applyRun) dryRunPrint(cards []cardSend) {
	bs := a.cfg.WBUpdate.BatchSize
	for i := 0; i < len(cards); i += bs {
		end := min(i+bs, len(cards))
		payload, _ := json.MarshalIndent(itemsOf(cards[i:end]), "", "  ")
		fmt.Printf("\n--- Batch %d-%d (%d cards) ---\n%s\n", i+1, end, end-i, payload)
	}
	fmt.Printf("\nDone (dry-run): %d to send, %d skipped_hollow, %d failed-build\n", len(cards), a.skippedHollow, a.failed)
}

func itemsOf(cards []cardSend) []wb.CardUpdateItem {
	items := make([]wb.CardUpdateItem, len(cards))
	for i, c := range cards {
		items[i] = c.item
	}
	return items
}

func rowsOf(cards []cardSend) []stagedDimRow {
	rows := make([]stagedDimRow, len(cards))
	for i, c := range cards {
		rows[i] = c.row
	}
	return rows
}

// checkWBErrors queries WB error list after a successful UpdateCards batch.
func checkWBErrors(ctx context.Context, client wbUpdateClient, cfg *Config, batch []stagedDimRow) error {
	items, err := client.GetCardErrorsList(ctx, wb.CardsBaseURL,
		cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst,
		wb.CardErrorsListRequest{
			Cursor: &wb.CardErrorsCursor{Limit: 100},
			Order:  &wb.CardErrorsOrder{Ascending: false},
		})
	if err != nil {
		log.Printf("  WARN: error list query failed: %v (non-fatal)", err)
		return nil
	}

	batchVC := make(map[string]int, len(batch))
	for _, r := range batch {
		batchVC[r.VendorCode] = r.NmID
	}

	var foundErrors map[string][]string
	for _, item := range items {
		for vc, errs := range item.Errors {
			if _, ok := batchVC[vc]; ok {
				if foundErrors == nil {
					foundErrors = make(map[string][]string)
				}
				foundErrors[vc] = append(foundErrors[vc], errs...)
			}
		}
	}

	if len(foundErrors) == 0 {
		log.Printf("  WBREPLY: no validation errors in error list")
		return nil
	}

	nmIDs := make([]int, 0, len(foundErrors))
	vcs := make([]string, 0, len(foundErrors))
	for vc := range foundErrors {
		nmIDs = append(nmIDs, batchVC[vc])
		vcs = append(vcs, vc)
	}

	report := struct {
		Timestamp   string              `json:"timestamp"`
		BatchNmIDs  []int               `json:"batch_nm_ids"`
		VendorCodes []string            `json:"vendor_codes"`
		WBErrors    map[string][]string `json:"wb_errors"`
	}{
		Timestamp:   time.Now().Format(time.RFC3339),
		BatchNmIDs:  nmIDs,
		VendorCodes: vcs,
		WBErrors:    foundErrors,
	}

	filename := fmt.Sprintf("wb-errors-%s.json", time.Now().Format("2006-01-02_150405"))
	data, _ := json.MarshalIndent(report, "", "  ")
	if writeErr := os.WriteFile(filename, data, 0644); writeErr != nil {
		log.Printf("  ERROR: failed to write error report: %v", writeErr)
	} else {
		log.Printf("  Error report saved: %s", filename)
	}

	for vc, errs := range foundErrors {
		nmID := batchVC[vc]
		for _, e := range errs {
			log.Printf("  WB ERROR vendor_code=%s nm_id=%d: %q", vc, nmID, e)
		}
	}

	return fmt.Errorf("WB validation errors found for %d vendor_codes — see %s", len(foundErrors), filename)
}

// maxSnapshotAgeHours возвращает порог свежести карточного снапшота:
// nil/отсутствует в yaml = 24ч (fail-safe), 0 = гард выключен.
func (c *Config) maxSnapshotAgeHours() int {
	if c.MaxSnapshotAgeHours == nil {
		return 24
	}
	return *c.MaxSnapshotAgeHours
}

// checkSnapshotFreshness отказывает реальный apply по протухшему кешу карточек:
// round-trip documents/kizMarked/характеристик из старого снапшота откатывает
// свежие правки, сделанные в ЛК после последнего prepare-fix-dims.sh.
func checkSnapshotFreshness(ctx context.Context, db *sql.DB, maxAgeHours int) error {
	if maxAgeHours <= 0 {
		return nil
	}
	var maxDL sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT MAX(downloaded_at) FROM cards").Scan(&maxDL); err != nil {
		return fmt.Errorf("snapshot freshness: %w", err)
	}
	if !maxDL.Valid || maxDL.String == "" {
		return fmt.Errorf("snapshot freshness: таблица cards пуста — прогоните prepare-fix-dims.sh перед --apply")
	}
	// SQLite CURRENT_TIMESTAMP → UTC "2006-01-02 15:04:05"
	ts, err := time.Parse("2006-01-02 15:04:05", maxDL.String)
	if err != nil {
		return fmt.Errorf("snapshot freshness: не разобрать downloaded_at %q: %w", maxDL.String, err)
	}
	if age := time.Since(ts); age > time.Duration(maxAgeHours)*time.Hour {
		return fmt.Errorf("snapshot freshness: карточный снапшот протух (%s = %.1f ч назад > max_snapshot_age_hours=%d) — прогоните prepare-fix-dims.sh перед --apply",
			maxDL.String, age.Hours(), maxAgeHours)
	}
	return nil
}

// runCheck queries the WB error list and prints all card validation errors.
func runCheck(ctx context.Context, client *wb.Client, cfg cardupdate.WBUpdateConfig) error {
	items, err := client.GetCardErrorsList(ctx, wb.CardsBaseURL,
		cfg.RatePerMin, cfg.RateBurst,
		wb.CardErrorsListRequest{
			Cursor: &wb.CardErrorsCursor{Limit: 100},
			Order:  &wb.CardErrorsOrder{Ascending: false},
		})
	if err != nil {
		return fmt.Errorf("get card errors: %w", err)
	}

	if len(items) == 0 {
		fmt.Println("No errors found in WB error list.")
		return nil
	}

	totalErrors := 0
	for i, item := range items {
		fmt.Printf("\n── Batch %d (UUID: %s) ──\n", i+1, item.BatchUUID)
		fmt.Printf("  Vendor codes: %v\n", item.VendorCodes)
		for vc, sub := range item.Subjects {
			fmt.Printf("  Subject: %s → %s (id=%d)\n", vc, sub.Name, sub.ID)
		}
		for vc, errs := range item.Errors {
			for _, e := range errs {
				fmt.Printf("  ERROR vendor_code=%s: %s\n", vc, e)
				totalErrors++
			}
		}
	}
	fmt.Printf("\nTotal: %d error batches, %d individual errors\n", len(items), totalErrors)

	allVCs := make(map[string]bool)
	allErrors := make(map[string][]string)
	for _, item := range items {
		for _, vc := range item.VendorCodes {
			allVCs[vc] = true
		}
		for vc, errs := range item.Errors {
			allErrors[vc] = append(allErrors[vc], errs...)
		}
	}
	report := struct {
		Timestamp   string              `json:"timestamp"`
		VendorCodes []string            `json:"vendor_codes"`
		WBErrors    map[string][]string `json:"wb_errors"`
		RawResponse []wb.CardErrorItem  `json:"raw_response"`
	}{
		Timestamp:   time.Now().Format(time.RFC3339),
		RawResponse: items,
		WBErrors:    allErrors,
	}
	for vc := range allVCs {
		report.VendorCodes = append(report.VendorCodes, vc)
	}

	data, _ := json.MarshalIndent(report, "", "  ")
	filename := fmt.Sprintf("wb-errors-%s.json", time.Now().Format("2006-01-02_150405"))
	if err := os.WriteFile(filename, data, 0644); err != nil {
		log.Printf("  WARN: failed to write report: %v", err)
	} else {
		log.Printf("Full report saved: %s", filename)
	}

	if totalErrors > 0 {
		return fmt.Errorf("WB error list contains %d errors", totalErrors)
	}
	return nil
}
