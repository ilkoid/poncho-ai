// row.go — финальная строка среза analytical.card_health_daily (+ render-поля).
package main

// Row — обогащённая строка вотчлиста: то, что пишется в БД, плюс поля,
// живущие только при рендере xlsx (HypoCode для сводки, PhotoTM/PhotoBig).
type Row struct {
	SnapshotDate string
	NmID         int64

	// А. Идентификация
	Category1C string
	SubjectWB  string
	Article1C  string
	TitleWB    string
	Season1C   string

	// Б. Продажи
	SalesStart   string // '' = продажи не начались
	AnchorSource string
	FullDays     int
	Signal       string
	LastOrder    string // '' = заказов не было никогда
	OrdersTotal  int64  // заказов за жизнь — критерий разреза slow_start/stall
	Opens7d      int64
	Carts7d      int64

	// В. Факторы
	IsShadowed   *bool
	CardRating   *float64
	TitleLen     int
	DescLen      int // длина описания карточки, символы (0 = описания нет)
	NameIM1C     string
	PhotoCount   int
	HasVideo     bool
	CharCount    int
	WBStockTotal *int64
	SizesWB      *int
	Sizes1C      *int
	FboStorage   string
	SizesRatio   *float64
	StockAvail   string
	Campaigns    int

	// Г. Итог
	TopHypothesis string
	HypoCode      string // только для сводки xlsx, в БД не пишется

	// Render-поля (в БД не пишутся).
	PhotoTM  string
	PhotoBig string
}

// AnchorSourceRu — якорь начала продаж по-русски (для xlsx).
func (r *Row) AnchorSourceRu() string {
	switch r.AnchorSource {
	case "stocks":
		return "остаток WB"
	case "stock_products":
		return "портал"
	case "funnel_open":
		return "первый показ"
	case "cards_created":
		return "создание карточки"
	default:
		return "—"
	}
}

// SignalRu — сигнал по-русски (для xlsx).
func (r *Row) SignalRu() string {
	switch r.Signal {
	case SignalSlowStart:
		return "медленный старт"
	case SignalStall:
		return "остановка продаж"
	case SignalPreSales:
		return "продажи не начались"
	default:
		return r.Signal
	}
}
