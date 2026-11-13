// query.go — SQL выборки сырых данных среза и запись в analytical.card_health_daily.
//
// Источники (RAW, schema public):
//
//   - cards ⋈ onec_goods — популяция: белый список model_status, не заблокирован;
//     onec_goods дедуплицируется DISTINCT ON (article) по свежести (конвенция v1);
//   - stocks_daily_warehouses — FBO-остатки: первый день qty>0 (якорь) и последний
//     срез (сумма + размеры по chrt);
//   - stock_products — портальный агрегат: первый срез stock_count>0 (якорь-fallback
//     для карточек-«невидимок») и последний срез (количество + availability);
//   - warehouse_remains — fallback размерных остатков (tech_size);
//   - funnel_metrics_daily — первый open (якорь-fallback) + окно opens/carts;
//   - orders — последний заказ (is_cancel=false; строка = единица заказа);
//   - item_ratings — is_shadowed последнего периода (кандидат в NULL);
//   - products — рейтинг карточки 0-10;
//   - card_photos / card_characteristics — количества;
//   - campaign_products ⋈ campaigns — живые кампании (статус из конфига);
//   - onec_rests ⋈ onec_goods_sku-каскад — размеры 1С на складе каскада
//     («РВБ ФБО» → «Внуково»): приоритет склада через unnest WITH ORDINALITY.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// mskTZ — часовой пояс МСК: все TEXT-даты WB/1С в базе — московские.
var mskTZ = time.FixedZone("MSK", 3*60*60)

// todayMoscow — сегодняшняя дата МСК в каноническом формате среза.
func todayMoscow() string {
	return time.Now().In(mskTZ).Format(dateLayout)
}

// rawRow — сырые данные SQL до обогащения в Go.
type rawRow struct {
	NmID           int64
	Article        string
	Title          string
	Subject        string
	Category1C     string
	Season1C       string
	NameIM         string
	CardCreated    string
	DescriptionLen int  // длина описания карточки, символы (0 = описания нет)
	HasVideo       bool // у карточки есть видео (cards.video непуст)
	FirstStock     *string
	FirstPortal    *string
	FirstOpen      *string
	LastOrder      *string
	OrdersTotal    int64 // заказов за жизнь (is_cancel=false) — критерий разреза slow_start/stall
	Opens7d        int64
	Carts7d        int64
	IsShadowed     *bool
	CardRating     *float64
	PhotoCount     int
	CharCount      int
	StockQty       *int64 // stocks, последний срез
	StockSizes     *int   // stocks, последний срез
	WhrQty         *int64 // warehouse_remains, последний срез
	WhrSizes       *int   // warehouse_remains, последний срез
	PortalQty      *int64 // stock_products, последний срез
	PortalAvail    string
	Sizes1C        *int
	FboStorage     string
	Campaigns      int
}

// loadRawSQL — вся сырая выборка популяции одним запросом (CTE по последним срезам).
//
// $1 — белый список model_status; $2 — начало окна воронки 'YYYY-MM-DD';
// $3 — каскад 1С-складов (порядок = приоритет); $4 — статусы живых кампаний;
// $5 — годы коллекции (2-значные строки, символы 2-3 артикула; NULL = без фильтра —
// конвенция allowed_years: при активном фильтре артикулы с невалидным «годом»
// отсеиваются SQL-side, включая легаси-мусор 6/7-значной нумерации).
const loadRawSQL = `
WITH goods AS (
    SELECT DISTINCT ON (article) article, guid, category_level2_name, category_level1_name,
           category, season, name_im, model_status, is_article_blocked, is_model_cancelled,
           is_exclude_from_site
    FROM public.onec_goods
    ORDER BY article, downloaded_at DESC, guid
),
pop AS (
    SELECT c.nm_id, c.vendor_code AS article, c.title, c.subject_name, c.created_at,
           COALESCE(length(c.description), 0) AS description_len, -- length(text)=символы, не байты
           (NULLIF(c.video, '') IS NOT NULL) AS has_video,
           COALESCE(NULLIF(o.category_level2_name, ''), NULLIF(o.category_level1_name, ''), o.category) AS category_1c,
           o.season AS season_1c, o.name_im
    FROM public.cards c
    JOIN goods o ON o.article = c.vendor_code
    WHERE o.model_status = ANY($1::text[])
      AND NOT o.is_article_blocked AND NOT o.is_model_cancelled AND NOT o.is_exclude_from_site
      AND (substring(o.article FROM 2 FOR 2) = ANY($5::text[]) OR $5::text[] IS NULL)
),
stocks_first AS (
    SELECT nm_id, MIN(snapshot_date) AS d
    FROM public.stocks_daily_warehouses WHERE quantity > 0 GROUP BY nm_id
),
stocks_last AS (
    SELECT nm_id, SUM(quantity) AS qty,
           COUNT(DISTINCT chrt_id) FILTER (WHERE quantity > 0) AS sizes
    FROM public.stocks_daily_warehouses
    WHERE snapshot_date = (SELECT MAX(snapshot_date) FROM public.stocks_daily_warehouses)
    GROUP BY nm_id
),
whr_last AS (
    SELECT nm_id, SUM(quantity) AS qty,
           COUNT(DISTINCT tech_size) FILTER (WHERE quantity > 0) AS sizes
    FROM public.warehouse_remains
    WHERE snapshot_date = (SELECT MAX(snapshot_date) FROM public.warehouse_remains)
    GROUP BY nm_id
),
portal AS (
    SELECT DISTINCT ON (nm_id) nm_id, stock_count, availability
    FROM public.stock_products ORDER BY nm_id, snapshot_date DESC
),
portal_first AS (
    SELECT nm_id, MIN(snapshot_date) AS d
    FROM public.stock_products WHERE stock_count > 0 GROUP BY nm_id
),
funnel7 AS (
    SELECT nm_id, SUM(open_count) AS opens, SUM(cart_count) AS carts
    FROM public.funnel_metrics_daily WHERE metric_date >= $2 GROUP BY nm_id
),
funnel_first AS (
    SELECT nm_id, MIN(metric_date) AS d
    FROM public.funnel_metrics_daily WHERE open_count > 0 GROUP BY nm_id
),
lastord AS (
    SELECT nm_id, MAX(substring(order_date, 1, 10)) AS d, COUNT(*) AS cnt
    FROM public.orders WHERE is_cancel = false GROUP BY nm_id
),
shadow AS (
    SELECT DISTINCT ON (nm_id) nm_id, is_shadowed
    FROM public.item_ratings ORDER BY nm_id, period_start DESC, period_end DESC
),
photos AS (
    SELECT nm_id, COUNT(*) AS cnt FROM public.card_photos GROUP BY nm_id
),
chars AS (
    SELECT nm_id, COUNT(*) AS cnt FROM public.card_characteristics GROUP BY nm_id
),
camp AS (
    SELECT cp.nm_id, COUNT(DISTINCT c.advert_id) AS cnt
    FROM public.campaign_products cp
    JOIN public.campaigns c ON c.advert_id = cp.advert_id
    WHERE c.status = ANY($4::int[])
    GROUP BY cp.nm_id
),
storage_ord AS (
    SELECT name, ord FROM unnest($3::text[]) WITH ORDINALITY AS t(name, ord)
),
onec_sizes AS (
    SELECT g.article, r.storage_name,
           COUNT(DISTINCT r.sku_guid) FILTER (WHERE r.free > 0) AS sizes
    FROM public.onec_rests r
    JOIN goods g ON g.guid = r.good_guid
    WHERE r.snapshot_date = (SELECT MAX(snapshot_date) FROM public.onec_rests)
      AND r.storage_name = ANY($3::text[])
    GROUP BY g.article, r.storage_name
),
fbo_pick AS (
    SELECT DISTINCT ON (os.article) os.article, os.storage_name, os.sizes
    FROM onec_sizes os JOIN storage_ord so ON so.name = os.storage_name
    ORDER BY os.article, so.ord
)
SELECT p.nm_id, p.article, p.title, p.subject_name, p.category_1c, p.season_1c,
       p.name_im, p.created_at, p.description_len, p.has_video,
       sf.d, pf.d, ff.d, lo.d, COALESCE(lo.cnt, 0),
       COALESCE(f7.opens, 0), COALESCE(f7.carts, 0),
       sh.is_shadowed, pr.product_rating,
       COALESCE(ph.cnt, 0), COALESCE(ch.cnt, 0),
       sl.qty, sl.sizes, wl.qty, wl.sizes, po.stock_count, COALESCE(po.availability, ''),
       fp.sizes, COALESCE(fp.storage_name, ''),
       COALESCE(cm.cnt, 0)
FROM pop p
LEFT JOIN stocks_first sf ON sf.nm_id = p.nm_id
LEFT JOIN portal_first  pf ON pf.nm_id = p.nm_id
LEFT JOIN funnel_first  ff ON ff.nm_id = p.nm_id
LEFT JOIN lastord       lo ON lo.nm_id = p.nm_id
LEFT JOIN funnel7       f7 ON f7.nm_id = p.nm_id
LEFT JOIN shadow        sh ON sh.nm_id = p.nm_id
LEFT JOIN public.products pr ON pr.nm_id = p.nm_id
LEFT JOIN photos        ph ON ph.nm_id = p.nm_id
LEFT JOIN chars         ch ON ch.nm_id = p.nm_id
LEFT JOIN stocks_last   sl ON sl.nm_id = p.nm_id
LEFT JOIN whr_last      wl ON wl.nm_id = p.nm_id
LEFT JOIN portal        po ON po.nm_id = p.nm_id
LEFT JOIN fbo_pick      fp ON fp.article = p.article
LEFT JOIN camp          cm ON cm.nm_id = p.nm_id
`

// loadRaw — выполняет сырую выборку и сканирует строки.
func loadRaw(ctx context.Context, db *pgxpool.Pool, cfg *Config, funnelFrom string) ([]rawRow, error) {
	storages := cfg.CascadeStorages()
	// Годы коллекции → 2-значные строки; пустой список → NULL (фильтр выключен).
	var yearFilter []string
	for _, y := range cfg.Watch.AllowedYears {
		yearFilter = append(yearFilter, fmt.Sprintf("%02d", y%100))
	}
	if len(yearFilter) == 0 {
		yearFilter = nil
	}
	rows, err := db.Query(ctx, loadRawSQL,
		cfg.Watch.ModelStatusWhitelist, funnelFrom, storages, cfg.Thresholds.CampaignActiveStatuses, yearFilter)
	if err != nil {
		return nil, fmt.Errorf("сырая выборка: %w", err)
	}
	defer rows.Close()

	out := make([]rawRow, 0, 512)
	for rows.Next() {
		var r rawRow
		if err := rows.Scan(
			&r.NmID, &r.Article, &r.Title, &r.Subject, &r.Category1C, &r.Season1C,
			&r.NameIM, &r.CardCreated, &r.DescriptionLen, &r.HasVideo,
			&r.FirstStock, &r.FirstPortal, &r.FirstOpen, &r.LastOrder, &r.OrdersTotal,
			&r.Opens7d, &r.Carts7d,
			&r.IsShadowed, &r.CardRating,
			&r.PhotoCount, &r.CharCount,
			&r.StockQty, &r.StockSizes, &r.WhrQty, &r.WhrSizes, &r.PortalQty, &r.PortalAvail,
			&r.Sizes1C, &r.FboStorage,
			&r.Campaigns,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EnrichRow — обогащение сырой строки до финальной: каскады якоря/остатков, счётчик
// полных дней, сигнал гендира, размерное отношение, гипотеза. Возвращает nil, если
// строка критериям не соответствует (в срез не попадает).
func EnrichRow(r rawRow, cfg *Config, today string) *Row {
	salesStart, anchorSrc := SalesStartCascade(
		deref(r.FirstStock), deref(r.FirstPortal), deref(r.FirstOpen), r.CardCreated)

	lastOrder := deref(r.LastOrder)
	fullDays := FullDaysNoOrders(today, lastOrder, salesStart)

	// Каскад суммарного остатка: первый источник с qty>0 (stocks → warehouse_remains
	// → портал), иначе первый существующий (честный 0). Приоритет «положительности»
	// важен для FBS-товаров: строки stocks есть, но FBO=0, а в портале остаток живой.
	wbStock := pickPositive(r.StockQty, r.WhrQty, r.PortalQty)
	if wbStock == nil {
		wbStock = firstNonnull(r.StockQty, r.WhrQty, r.PortalQty)
	}
	var stock int64
	if wbStock != nil {
		stock = *wbStock
	}

	signal := ClassifySignal(salesStart, fullDays, stock, r.OrdersTotal, &cfg.Watch)
	if signal == "" {
		return nil
	}

	// Каскад размерных данных: строки stocks → строки whremains → нет данных.
	sizesWB := r.StockSizes
	if sizesWB == nil {
		sizesWB = r.WhrSizes
	}
	var ratio *float64
	if sizesWB != nil && r.Sizes1C != nil && *r.Sizes1C > 0 {
		v := float64(*sizesWB) / float64(*r.Sizes1C)
		ratio = &v
	}

	row := &Row{
		SnapshotDate: today,
		NmID:         r.NmID,
		Category1C:   r.Category1C,
		SubjectWB:    r.Subject,
		Article1C:    r.Article,
		TitleWB:      r.Title,
		Season1C:     r.Season1C,
		SalesStart:   salesStart,
		AnchorSource: anchorSrc,
		FullDays:     fullDays,
		Signal:       signal,
		LastOrder:    lastOrder,
		OrdersTotal:  r.OrdersTotal,
		Opens7d:      r.Opens7d,
		Carts7d:      r.Carts7d,
		IsShadowed:   r.IsShadowed,
		CardRating:   r.CardRating,
		TitleLen:     len([]rune(r.Title)),
		DescLen:      r.DescriptionLen,
		NameIM1C:     r.NameIM,
		PhotoCount:   r.PhotoCount,
		HasVideo:     r.HasVideo,
		CharCount:    r.CharCount,
		WBStockTotal: wbStock,
		SizesWB:      sizesWB,
		Sizes1C:      r.Sizes1C,
		FboStorage:   r.FboStorage,
		SizesRatio:   ratio,
		StockAvail:   r.PortalAvail,
		Campaigns:    r.Campaigns,
	}

	h := Hypothesis{
		Signal:        signal,
		IsShadowed:    r.IsShadowed,
		Opens7d:       r.Opens7d,
		Carts7d:       r.Carts7d,
		TitleBad:      TitleBad(r.Title, r.Subject, &cfg.Thresholds),
		PhotoLow:      r.PhotoCount < cfg.Thresholds.PhotoMin,
		SizesRatioLow: ratio != nil && *ratio < cfg.Thresholds.SizesRatioMin,
		StockTotal:    wbStock,
		Campaigns:     r.Campaigns,
	}
	row.HypoCode, row.TopHypothesis = TopHypothesis(h)
	return row
}

// saveSnapshot — атомарная пересборка среза дня: DELETE + INSERT одной транзакцией
// (при ошибке старый срез остаётся нетронутым). Чанки по 500 строк (конвенция репо).
func saveSnapshot(ctx context.Context, db *pgxpool.Pool, rows []Row) (int, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("tx begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	if _, err = tx.Exec(ctx, deleteSnapshotSQL, rows[0].SnapshotDate); err != nil {
		return 0, fmt.Errorf("delete snapshot: %w", err)
	}
	const chunk = 500
	saved := 0
	for start := 0; start < len(rows); start += chunk {
		end := start + chunk
		if end > len(rows) {
			end = len(rows)
		}
		b := &pgx.Batch{}
		for i := start; i < end; i++ {
			r := rows[i]
			b.Queue(insertRowSQL,
				r.SnapshotDate, r.NmID,
				r.Category1C, r.SubjectWB, r.Article1C, r.TitleWB, r.Season1C,
				nullIfEmpty(r.SalesStart), r.AnchorSource, r.FullDays, r.Signal,
				nullIfEmpty(r.LastOrder),
				r.OrdersTotal, r.Opens7d, r.Carts7d,
				r.IsShadowed, r.CardRating, r.TitleLen, r.DescLen, r.NameIM1C, r.PhotoCount,
				r.HasVideo, r.CharCount,
				r.WBStockTotal, r.SizesWB, r.Sizes1C, r.FboStorage, r.SizesRatio,
				r.StockAvail, r.Campaigns,
				r.TopHypothesis)
		}
		if err = tx.SendBatch(ctx, b).Close(); err != nil {
			return 0, fmt.Errorf("insert chunk %d: %w", start, err)
		}
		saved += end - start
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("tx commit: %w", err)
	}
	return saved, nil
}

// trimRetention — удалить срезы старше retentionDays; возвращает число удалённых.
func trimRetention(ctx context.Context, db *pgxpool.Pool, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().In(mskTZ).AddDate(0, 0, -retentionDays).Format(dateLayout)
	ct, err := db.Exec(ctx, trimRetentionSQL, cutoff)
	if err != nil {
		return 0, fmt.Errorf("retention delete: %w", err)
	}
	return ct.RowsAffected(), nil
}

// photoURL — tm/big первой фотографии карточки (для миниатюры и ссылки).
type photoURL struct {
	TM  string
	Big string
}

// loadPhotoURLs — URL первых фото для списка nmID (после фильтрации вотчлиста).
func loadPhotoURLs(ctx context.Context, db *pgxpool.Pool, nmIDs []int64) (map[int64]photoURL, error) {
	if len(nmIDs) == 0 {
		return map[int64]photoURL{}, nil
	}
	rows, err := db.Query(ctx, `
		SELECT nm_id, tm, big FROM public.card_photos
		WHERE id IN (SELECT MIN(id) FROM public.card_photos WHERE nm_id = ANY($1) GROUP BY nm_id)`,
		nmIDs)
	if err != nil {
		return nil, fmt.Errorf("photo urls: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]photoURL, len(nmIDs))
	for rows.Next() {
		var nm int64
		var u photoURL
		if err := rows.Scan(&nm, &u.TM, &u.Big); err != nil {
			return nil, fmt.Errorf("photo scan: %w", err)
		}
		out[nm] = u
	}
	return out, rows.Err()
}

// coverage — свежесть источников для шапки и fail-fast (как coverageSQL в
// size-availability: переименование/пропажа 1С-склада роняют прогон ДО пустого среза).
type coverage struct {
	RestsDate     string // последний срез onec_rests
	RestsDateRaw  string
	StorageRows   int64  // строк каскадных складов в последнем срезе onec_rests
	StocksDate    string // последний срез stocks
	StocksDateRaw string
	Cards         int64
}

// loadCoverage — проверка источников. Ошибка, если по каскадным складам 0 строк
// (склад переименован/остатки не доехали) или нет свежести onec_rests вовсе.
func loadCoverage(ctx context.Context, db *pgxpool.Pool, storages []string) (coverage, error) {
	var c coverage
	err := db.QueryRow(ctx, `
		SELECT
		  (SELECT COALESCE(MAX(snapshot_date), '') FROM public.onec_rests),
		  (SELECT COUNT(*) FROM public.onec_rests
		     WHERE snapshot_date = (SELECT MAX(snapshot_date) FROM public.onec_rests)
		       AND storage_name = ANY($1)),
		  (SELECT COALESCE(MAX(snapshot_date), '') FROM public.stocks_daily_warehouses),
		  (SELECT COUNT(*) FROM public.cards)`,
		storages).Scan(&c.RestsDate, &c.StorageRows, &c.StocksDate, &c.Cards)
	if err != nil {
		return c, fmt.Errorf("coverage: %w", err)
	}
	if c.RestsDate == "" || c.StorageRows == 0 {
		return c, fmt.Errorf(
			"на каскадных складах %v нет строк в последнем срезе onec_rests (%q): склад переименован или остатки не доехали",
			storages, c.RestsDate)
	}
	return c, nil
}

// deref — значение указателя или ” (для TEXT-дат).
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// pickPositive — первый указатель со значением >0 (каскад остатков: важен для
// FBS-товаров с нулевым FBO, но живым портальным остатком).
func pickPositive(vals ...*int64) *int64 {
	for _, v := range vals {
		if v != nil && *v > 0 {
			return v
		}
	}
	return nil
}

// firstNonnull — первый существующий указатель (честный 0, когда все нули).
func firstNonnull(vals ...*int64) *int64 {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

// nullIfEmpty — пустая строка → NULL для записи в БД.
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
