// data.go — сборка payload дашборда из строк снапшота (колоночные массивы,
// конвенция fbs-supplies-report: строка = один индекс во всех колонках).
package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AvailabilityData — весь payload дашборда.
type AvailabilityData struct {
	Meta Meta      `json:"meta"`
	Arts ArticleCol `json:"arts"`
	Sizes SizeCol   `json:"sizes"`
}

// Meta — служебные данные (шапка, свежесть, пороги, дельта к прошлому снапшоту).
type Meta struct {
	GeneratedAt  string   `json:"generated_at"`   // МСК, «02.01.2006 15:04»
	Db           string   `json:"db"`             // имя БД (не прод → бейдж «ТЕСТ»)
	SnapshotDate string   `json:"snapshot_date"`  // день снапшота стоков WB
	StocksDate   string   `json:"stocks_date"`
	RestsDate    string   `json:"rests_date"`
	OnecGoodsDate string  `json:"onec_goods_date"`
	MinUnits     int      `json:"min_units"`  // порог «представлен» (шт, строго больше)
	MinSizes     int      `json:"min_sizes"`  // порог «недоступен» (меньше размеров)
	OnecStorage  string   `json:"onec_storage"`
	Prev         *PrevKpi `json:"prev,omitempty"` // прошлый снапшот, если история есть
}

// PrevKpi — агрегат предыдущего снапшота для дельт KPI.
type PrevKpi struct {
	Date        string `json:"date"`
	Unavailable int    `json:"unavailable"`
	OnecFree    int64  `json:"onec_free"`
	NoCard      int    `json:"no_card"`
	ZeroSizes   int    `json:"zero_sizes"`
}

// ArticleCol — артикулы колонками (колонка = массив одной длины).
type ArticleCol struct {
	Article  []string `json:"article"`
	NmID     []int64  `json:"nm_id"` // 0 = карточки WB нет
	Brand    []string `json:"brand"`
	Cat1     []string `json:"cat1"`
	Cat2     []string `json:"cat2"`
	Season   []string `json:"season"`
	Year     []int    `json:"year"` // 0 = год по артикулу не определился
	SizesTotal []int  `json:"sizes_total"`
	SizesPres  []int  `json:"sizes_pres"` // по порогу конфига; UI пересчитывает своим порогом
	WbQty    []int64  `json:"wb_qty"`
	OnecFree []int64  `json:"onec_free"`
	OnecReserv []int64 `json:"onec_reserv"`
	HasCard  []bool   `json:"has_card"`
}

// SizeCol — размеры universe-карточек колонками (без nm-ключа в каждой строке:
// владелец определяется через Sizes.Nm, порядок отсортирован по nm).
type SizeCol struct {
	NmID []int64  `json:"nm"`
	Size []string `json:"size"`
	Qty  []int64  `json:"qty"`
}

// loadCube читает снапшот date из analytical-таблиц и собирает payload.
// cov — свежесть RAW-источников для шапки.
func loadCube(ctx context.Context, pool *pgxpool.Pool, date string, cfg *Config, cov Coverage) (*AvailabilityData, error) {
	arts, err := loadArticles(ctx, pool, date)
	if err != nil {
		return nil, err
	}
	sizes, err := loadSizes(ctx, pool, date)
	if err != nil {
		return nil, err
	}
	prev, err := loadPrevKpi(ctx, pool, date)
	if err != nil {
		return nil, err
	}

	cube := &AvailabilityData{Meta: Meta{
		GeneratedAt:   nowMoscow().Format("02.01.2006 15:04"),
		Db:            cfg.Storage.PgDatabase,
		SnapshotDate:  date,
		StocksDate:    cov.StocksDate,
		RestsDate:     cov.RestsDate,
		OnecGoodsDate: cov.OnecGoodsDate,
		OnecStorage:   cfg.OnecStorage,
		MinUnits:      cfg.MinUnitsPerSize,
		MinSizes:      cfg.MinSizesPresented,
		Prev:          prev,
	}}

	n := len(arts)
	cube.Arts.Article = make([]string, 0, n)
	cube.Arts.NmID = make([]int64, 0, n)
	cube.Arts.Brand = make([]string, 0, n)
	cube.Arts.Cat1 = make([]string, 0, n)
	cube.Arts.Cat2 = make([]string, 0, n)
	cube.Arts.Season = make([]string, 0, n)
	cube.Arts.Year = make([]int, 0, n)
	cube.Arts.SizesTotal = make([]int, 0, n)
	cube.Arts.SizesPres = make([]int, 0, n)
	cube.Arts.WbQty = make([]int64, 0, n)
	cube.Arts.OnecFree = make([]int64, 0, n)
	cube.Arts.OnecReserv = make([]int64, 0, n)
	cube.Arts.HasCard = make([]bool, 0, n)

	for _, r := range arts {
		cube.Arts.Article = append(cube.Arts.Article, r.Article)
		cube.Arts.NmID = append(cube.Arts.NmID, r.NmID)
		cube.Arts.Brand = append(cube.Arts.Brand, r.Brand)
		cube.Arts.Cat1 = append(cube.Arts.Cat1, r.Cat1)
		cube.Arts.Cat2 = append(cube.Arts.Cat2, r.Cat2)
		cube.Arts.Season = append(cube.Arts.Season, r.Season)
		cube.Arts.Year = append(cube.Arts.Year, r.Year)
		cube.Arts.SizesTotal = append(cube.Arts.SizesTotal, r.SizesTotal)
		cube.Arts.SizesPres = append(cube.Arts.SizesPres, r.SizesPresented)
		cube.Arts.WbQty = append(cube.Arts.WbQty, r.WbQty)
		cube.Arts.OnecFree = append(cube.Arts.OnecFree, r.OnecFree)
		cube.Arts.OnecReserv = append(cube.Arts.OnecReserv, r.OnecReserv)
		cube.Arts.HasCard = append(cube.Arts.HasCard, r.HasCard)
	}

	m := len(sizes)
	cube.Sizes.NmID = make([]int64, 0, m)
	cube.Sizes.Size = make([]string, 0, m)
	cube.Sizes.Qty = make([]int64, 0, m)
	for _, r := range sizes {
		cube.Sizes.NmID = append(cube.Sizes.NmID, r.NmID)
		cube.Sizes.Size = append(cube.Sizes.Size, r.Size)
		cube.Sizes.Qty = append(cube.Sizes.Qty, r.Qty)
	}
	return cube, nil
}

// summarize — консольная сводка по кубу. Вердикты уже посчитаны SQL с порогами
// конфига (SizesPres/HasCard), здесь только счёт по Meta.MinSizes.
func summarize(cube *AvailabilityData) (unavailable, nocard, zero, freeStuck int64) {
	for i := range cube.Arts.Article {
		if cube.Arts.SizesPres[i] >= cube.Meta.MinSizes {
			continue // доступно
		}
		unavailable++
		freeStuck += cube.Arts.OnecFree[i]
		if !cube.Arts.HasCard[i] {
			nocard++
		} else if cube.Arts.SizesPres[i] == 0 {
			zero++
		}
	}
	return unavailable, nocard, zero, freeStuck
}
