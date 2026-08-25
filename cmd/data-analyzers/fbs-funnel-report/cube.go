// cube.go — датасет для HTML-дашборда (--html): один SQL-куб + словари.
//
// Зерно куба: (когортная дата, дата события, nm, город-бакет, статус, причина
// отмены) → количество + деньги (копейки). Все разрезы дашборда — деньги по
// дням/неделям, воронка, когорты, география, категории 1С, топ номенклатур,
// причины отмен — считает браузер за один проход по массиву фактов, поэтому
// смена фильтров пересобирает весь дашборд без обращения к БД.
//
// Окно куба — по дате создания (как когорты XLSX): каждая когорта в файле
// полная. Города: топ-300 по заказам + бакет «Прочие» (хвост из ~5.6k городов
// почти не повторяется и почти не сжимается).
//
// Категории — иерархия продавца из 1С (onec_goods: type → category_level1 →
// category_level2), джойн cards.vendor_code = onec_goods.article (покрытие
// ~99.8%). Если onec_goods в БД нет (тестовая база без 1С), джойн опускается и
// категории берутся из WB-предмета с маркером «WB ·».
package main

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

// cubeQuery — факты куба. $1 = all_models, $2 = since по дате создания.
const cubeQuery = `
WITH top_cities AS (
  SELECT destination_city
  FROM public.order_feed
  WHERE ($1 OR is_mp) AND destination_city <> ''
  GROUP BY destination_city
  ORDER BY count(*) DESC
  LIMIT 300
)
SELECT
  (created_at AT TIME ZONE 'Europe/Moscow')::date::text,
  (updated_at AT TIME ZONE 'Europe/Moscow')::date::text,
  nm_id,
  CASE WHEN destination_city IN (SELECT destination_city FROM top_cities)
       THEN destination_city ELSE '' END,
  status,
  COALESCE(cancel_type, ''),
  count(*)::int,
  COALESCE(sum(seller_price), 0)::float8
FROM public.order_feed
WHERE ($1 OR is_mp)
  AND ($2::date IS NULL OR (created_at AT TIME ZONE 'Europe/Moscow')::date >= $2::date)
GROUP BY 1, 2, 3, 4, 5, 6`

// cubeNmQueryOneC — словарь номенклатур с категориями 1С.
// cat1: уровень 1 1С → «WB · предмет» → «Прочее»; cat2 — только при живом 1С.
const cubeNmQueryOneC = `
SELECT
  f.nm_id,
  COALESCE(NULLIF(c.vendor_code, ''), f.nm_id::text),
  COALESCE(c.title, ''),
  COALESCE(c.subject_name, ''),
  COALESCE(og.type, ''),
  CASE
    WHEN COALESCE(og.category_level1, '') <> '' THEN og.category_level1
    WHEN COALESCE(c.subject_name, '') <> '' THEN 'WB · ' || c.subject_name
    ELSE 'Прочее'
  END,
  CASE WHEN COALESCE(og.category_level1, '') <> '' THEN COALESCE(og.category_level2, '') ELSE '' END
FROM (SELECT DISTINCT nm_id FROM public.order_feed WHERE ($1 OR is_mp)) f
LEFT JOIN public.cards c ON c.nm_id = f.nm_id
LEFT JOIN public.onec_goods og ON og.article = c.vendor_code`

// cubeNmQueryPlain — словарь без 1С (в БД нет onec_goods): предмет WB.
const cubeNmQueryPlain = `
SELECT
  f.nm_id,
  COALESCE(NULLIF(c.vendor_code, ''), f.nm_id::text),
  COALESCE(c.title, ''),
  COALESCE(c.subject_name, ''),
  '',
  CASE
    WHEN COALESCE(c.subject_name, '') <> '' THEN 'WB · ' || c.subject_name
    ELSE 'Прочее'
  END,
  ''
FROM (SELECT DISTINCT nm_id FROM public.order_feed WHERE ($1 OR is_mp)) f
LEFT JOIN public.cards c ON c.nm_id = f.nm_id`

// otherCity — отображаемое имя бакета городов за пределами топ-300.
const otherCity = "Прочие"

// DashMeta — служебные данные дашборда (шапка, бейдж БД, зрелость когорт).
type DashMeta struct {
	GeneratedAt      string  `json:"generated_at"` // МСК, «02.01.2006 15:04»
	Db               string  `json:"db"`           // имя БД (не прод → бейдж «ТЕСТ»)
	AllModels        bool    `json:"all_models"`
	FeedFrom         string  `json:"feed_from"` // события с … (YYYY-MM-DD)
	FeedTo           string  `json:"feed_to"`
	TotalOrders      int64   `json:"total_orders"`
	MatureAfterDays  int     `json:"mature_after_days"`     // p90 цикла в сутках; 0 = неизвестен
	LifecycleMedianD float64 `json:"lifecycle_median_days"` // медиана цикла, сутки; -1 = нет
	OnecCategories   bool    `json:"onec_categories"`
	OnecCoveragePct  float64 `json:"onec_coverage_pct"` // доля nm с категорией 1С, %
}

// CubeFacts — столбцы фактов (индексы в словари dims.*). Колоночный формат
// заметно компактнее построчного и быстрее разбирается в браузере.
type CubeFacts struct {
	Cohort []int32 `json:"cohort"`
	Event  []int32 `json:"event"`
	Nm     []int32 `json:"nm"`
	City   []int32 `json:"city"`
	Status []int32 `json:"status"`
	Ctype  []int32 `json:"ctype"`
	Cnt    []int32 `json:"cnt"`
	Kop    []int64 `json:"kop"` // рубли × 100, int — без float-шумов
}

// CubeDims — словари куба; факт ссылается на них индексами.
type CubeDims struct {
	Cohort []string   `json:"cohort"` // YYYY-MM-DD, отсортированы
	Event  []string   `json:"event"`  // YYYY-MM-DD, отсортированы
	Nm     [][]string `json:"nm"`     // [nm_id, артикул, название, предмет WB, тип 1С, кат1, кат2]
	City   []string   `json:"city"`   // '' → «Прочие»
	Status []string   `json:"status"` // литералы ленты как есть
	Ctype  []string   `json:"ctype"`  // app/receipt/expire/other/''
}

// CubeData — весь payload дашборда.
type CubeData struct {
	Meta  DashMeta  `json:"meta"`
	Dims  CubeDims  `json:"dims"`
	Facts CubeFacts `json:"facts"`
}

// dictBuilder — словарь «значение → индекс» с сохранением порядка добавления.
type dictBuilder struct {
	idx  map[string]int32
	vals []string
}

func newDict() *dictBuilder { return &dictBuilder{idx: map[string]int32{}} }

func (d *dictBuilder) put(v string) int32 {
	if i, ok := d.idx[v]; ok {
		return i
	}
	i := int32(len(d.vals))
	d.idx[v] = i
	d.vals = append(d.vals, v)
	return i
}

// loadCube собирает датасет дашборда. lifecycle берётся из существующего
// лоадера (медиана/p90 нужны в meta), остальное — два запроса выше.
func loadCube(ctx context.Context, pool *pgxpool.Pool, q queryParams, dbName string) (*CubeData, error) {
	var onec bool
	if err := pool.QueryRow(ctx,
		`SELECT to_regclass('public.onec_goods') IS NOT NULL`).Scan(&onec); err != nil {
		return nil, fmt.Errorf("onec check: %w", err)
	}

	cube := &CubeData{Meta: DashMeta{
		GeneratedAt: nowMoscow().Format("02.01.2006 15:04"),
		Db:          dbName,
		AllModels:   q.allModels,
	}}

	cohorts, events := newDict(), newDict()
	cities, statuses, ctypes := newDict(), newDict(), newDict()
	nmIdx := map[int64]int32{}

	rows, err := pool.Query(ctx, cubeQuery, q.args()...)
	if err != nil {
		return nil, fmt.Errorf("cube: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cohortD, eventD, city, status, ctype string
		var nmID int64
		var cnt int
		var rub float64
		if err := rows.Scan(&cohortD, &eventD, &nmID, &city, &status, &ctype, &cnt, &rub); err != nil {
			return nil, fmt.Errorf("cube scan: %w", err)
		}
		ci, ei := cohorts.put(cohortD), events.put(eventD)
		ni, ok := nmIdx[nmID]
		if !ok {
			ni = int32(len(cube.Dims.Nm))
			nmIdx[nmID] = ni
			// Ячейка заполняется вторым запросом по этому же порядку nm.
			cube.Dims.Nm = append(cube.Dims.Nm, []string{fmt.Sprint(nmID), "", "", "", "", "", ""})
		}
		cube.Facts.Cohort = append(cube.Facts.Cohort, ci)
		cube.Facts.Event = append(cube.Facts.Event, ei)
		cube.Facts.Nm = append(cube.Facts.Nm, ni)
		cube.Facts.City = append(cube.Facts.City, cities.put(city))
		cube.Facts.Status = append(cube.Facts.Status, statuses.put(status))
		cube.Facts.Ctype = append(cube.Facts.Ctype, ctypes.put(ctype))
		cube.Facts.Cnt = append(cube.Facts.Cnt, int32(cnt))
		cube.Facts.Kop = append(cube.Facts.Kop, int64(math.Round(rub*100)))
		cube.Meta.TotalOrders += int64(cnt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cube rows: %w", err)
	}

	nmQ := cubeNmQueryPlain
	if onec {
		nmQ = cubeNmQueryOneC
	}
	nRows, err := pool.Query(ctx, nmQ, q.allModels)
	if err != nil {
		return nil, fmt.Errorf("cube nm dict: %w", err)
	}
	defer nRows.Close()
	mapped := 0
	for nRows.Next() {
		var nm NmDictRow
		if err := nRows.Scan(&nm.ID, &nm.VendorCode, &nm.Title, &nm.Subject, &nm.Type, &nm.Cat1, &nm.Cat2); err != nil {
			return nil, fmt.Errorf("cube nm scan: %w", err)
		}
		if ni, ok := nmIdx[nm.ID]; ok {
			cube.Dims.Nm[ni] = []string{fmt.Sprint(nm.ID), nm.VendorCode, nm.Title, nm.Subject, nm.Type, nm.Cat1, nm.Cat2}
			if nm.Type != "" {
				mapped++
			}
		}
	}
	if err := nRows.Err(); err != nil {
		return nil, fmt.Errorf("cube nm rows: %w", err)
	}

	// Словари-даты сортируются: лексикографический порядок YYYY-MM-DD = хронология.
	// Возвращается переразметка old→new, факты переводятся на новые индексы.
	sortedRemap := func(d *dictBuilder) []int32 {
		old := make([]string, len(d.vals))
		copy(old, d.vals)
		sort.Strings(d.vals)
		m := make([]int32, len(old))
		for newI, v := range d.vals {
			m[d.idx[v]] = int32(newI) // d.idx ещё хранит старые индексы вставки
		}
		return m
	}
	cm, em := sortedRemap(cohorts), sortedRemap(events)
	for i := range cube.Facts.Cohort {
		cube.Facts.Cohort[i] = cm[cube.Facts.Cohort[i]]
		cube.Facts.Event[i] = em[cube.Facts.Event[i]]
	}

	cube.Dims.Cohort = cohorts.vals
	cube.Dims.Event = events.vals
	cube.Dims.City = renameBucket(cities.vals, otherCity)
	cube.Dims.Status = statuses.vals
	cube.Dims.Ctype = ctypes.vals

	if len(cube.Dims.Event) > 0 {
		cube.Meta.FeedFrom = cube.Dims.Event[0]
		cube.Meta.FeedTo = cube.Dims.Event[len(cube.Dims.Event)-1]
	}

	// Зрелость и скорость цикла — из лоадера XLSX-отчёта.
	if lc, err := loadLifecycle(ctx, pool, q); err == nil {
		if lc.MedianH != nil {
			cube.Meta.LifecycleMedianD = math.Round(*lc.MedianH/24*10) / 10
		}
		if lc.P90H != nil && *lc.P90H > 0 {
			cube.Meta.MatureAfterDays = int(math.Ceil(*lc.P90H / 24))
		}
	}
	if cube.Meta.LifecycleMedianD == 0 {
		cube.Meta.LifecycleMedianD = -1
	}

	cube.Meta.OnecCategories = onec
	if n := len(cube.Dims.Nm); n > 0 {
		cube.Meta.OnecCoveragePct = math.Round(float64(mapped)/float64(n)*1000) / 10
	}
	return cube, nil
}

// NmDictRow — строка словаря номенклатур (для Scan).
type NmDictRow struct {
	ID         int64
	VendorCode string
	Title      string
	Subject    string
	Type       string
	Cat1       string
	Cat2       string
}

// renameBucket — «» → otherCity (бакет городов за пределами топ-300).
func renameBucket(vals []string, name string) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		if v == "" {
			out[i] = name
		} else {
			out[i] = v
		}
	}
	return out
}
