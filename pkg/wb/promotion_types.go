// Package wb provides Promotion API types for Wildberries.
package wb

// ============================================================================
// Promotion API Types (adv/v1, adv/v3)
// ============================================================================

// PromotionCountResponse — ответ от GET /adv/v1/promotion/count.
// Содержит список всех кампаний, сгруппированных по типу и статусу.
type PromotionCountResponse struct {
	Adverts []PromotionAdvertGroup `json:"adverts"`
	All     int                    `json:"all"` // Общее количество кампаний
}

// PromotionAdvertGroup — группа кампаний по type+status.
type PromotionAdvertGroup struct {
	Type       int               `json:"type"`       // Тип кампании (8,9,50...)
	Status     int               `json:"status"`     // Статус (-1,4,7,8,9,11)
	Count      int               `json:"count"`      // Количество в группе
	AdvertList []PromotionAdvert `json:"advert_list"` // Список кампаний
}

// PromotionAdvert — одна кампания в списке.
type PromotionAdvert struct {
	AdvertID   int    `json:"advertId"`   // ID кампании
	ChangeTime string `json:"changeTime"` // Время последнего изменения
}

// ============================================================================
// Advert Details API v2 Types (GET /api/advert/v2/adverts)
// ============================================================================

// AdvertsResponse — ответ от GET /api/advert/v2/adverts.
type AdvertsResponse struct {
	Adverts []AdvertDetail `json:"adverts"`
}

// AdvertDetail — детальная информация о кампании.
// NOTE: v2 may not return details for all campaign types (e.g. type=8 legacy, type=6 booster).
type AdvertDetail struct {
	ID         int               `json:"id"`
	BidType    string            `json:"bid_type"`
	Status     int               `json:"status"`
	NmSettings []AdvertNmSetting `json:"nm_settings"`
	Settings   AdvertSettings    `json:"settings"`
	Timestamps AdvertTimestamps  `json:"timestamps"`
}

// AdvertSettings — настройки кампании.
type AdvertSettings struct {
	Name        string           `json:"name"`
	PaymentType string           `json:"payment_type"`
	Placements  AdvertPlacements `json:"placements"`
}

// AdvertPlacements — места размещения рекламы.
type AdvertPlacements struct {
	Search          bool `json:"search"`
	Recommendations bool `json:"recommendations"`
}

// AdvertTimestamps — временные отметки кампании.
// NOTE: "started" may be null (campaign not yet started) — Go string gets "".
type AdvertTimestamps struct {
	Created string `json:"created"`
	Updated string `json:"updated"`
	Started string `json:"started"`
	Deleted string `json:"deleted"`
}

// AdvertNmSetting — настройки товара в кампании.
type AdvertNmSetting struct {
	NmID        int           `json:"nm_id"`
	Subject     AdvertSubject `json:"subject"`
	BidsKopecks AdvertBids    `json:"bids_kopecks"`
}

// AdvertSubject — предмет (категория) товара.
type AdvertSubject struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// AdvertBids — ставки в копейках.
type AdvertBids struct {
	Search          int `json:"search"`
	Recommendations int `json:"recommendations"`
}

// CampaignStatus — константы статусов кампаний.
// Документация WB: https://openapi.wildberries.ru
const (
	CampaignStatusDeleted  = -1 // Удалена (процесс удаления до 10 мин)
	CampaignStatusReady    = 4  // Готова к запуску
	CampaignStatusFinished = 7  // Завершена
	CampaignStatusCanceled = 8  // Отменена
	CampaignStatusActive   = 9  // Активна
	CampaignStatusPaused   = 11 // На паузе
)

// CampaignType — константы типов кампаний.
const (
	CampaignTypeSearch  = 8  // Поиск (по ключевым словам)
	CampaignTypeAuto    = 9  // Автоматическая
	CampaignTypeBooster = 6  // Бустер
	CampaignTypeCatalog = 50 // Каталог
)

// CampaignDailyStats — статистика кампании за день для сохранения в БД.
// Заполняется из CampaignFullstatsResponse.Days[].
type CampaignDailyStats struct {
	AdvertID  int     `json:"advertId"`
	StatsDate string  `json:"statsDate"`
	Views     int     `json:"views"`
	Clicks    int     `json:"clicks"`
	CTR       float64 `json:"ctr"`  // Click-through rate (%)
	CPC       float64 `json:"cpc"`  // Cost per click (rub)
	CR        float64 `json:"cr"`   // Conversion rate (%)
	Orders    int     `json:"orders"`
	Shks      int     `json:"shks"`     // Выкупы
	Atbs      int     `json:"atbs"`     // Возвраты
	Canceled  int     `json:"canceled"` // Отмены
	Sum       float64 `json:"sum"`      // Затраты на рекламу (rub)
	SumPrice  float64 `json:"sumPrice"` // Сумма заказов (rub)
}

// ============================================================================
// Campaign Fullstats API v3 Types (GET /adv/v3/fullstats)
// Canonical types — 4-level hierarchy: Campaign → Day → App → Nm
// ============================================================================

// CampaignFullstatsResponse — полный ответ от GET /adv/v3/fullstats.
// Содержит итого по кампании, booster-статистику и дневную разбивку.
type CampaignFullstatsResponse struct {
	AdvertID     int                        `json:"advertId"`
	Views        int                        `json:"views"`
	Clicks       int                        `json:"clicks"`
	CTR          float64                    `json:"ctr"`
	CPC          float64                    `json:"cpc"`
	CR           float64                    `json:"cr"`
	Orders       int                        `json:"orders"`
	Shks         int                        `json:"shks"`
	Atbs         int                        `json:"atbs"`
	Canceled     int                        `json:"canceled"`
	Sum          float64                    `json:"sum"`
	SumPrice     float64                    `json:"sum_price"`
	BoosterStats []CampaignFullstatsBooster `json:"boosterStats"`
	Days         []CampaignFullstatsDay     `json:"days"`
}

// CampaignFullstatsBooster — booster-статистика кампании.
// Присутствует только для кампаний типа Booster (type=6).
type CampaignFullstatsBooster struct {
	AvgPosition float64 `json:"avg_position"`
	Date        string  `json:"date"`
	Nm          int     `json:"nm"`
}

// CampaignFullstatsDay — статистика за один день с разбивкой по платформам.
type CampaignFullstatsDay struct {
	Date     string                   `json:"date"`
	Views    int                      `json:"views"`
	Clicks   int                      `json:"clicks"`
	CTR      float64                  `json:"ctr"`
	CPC      float64                  `json:"cpc"`
	CR       float64                  `json:"cr"`
	Orders   int                      `json:"orders"`
	Shks     int                      `json:"shks"`
	Atbs     int                      `json:"atbs"`
	Canceled int                      `json:"canceled"`
	Sum      float64                  `json:"sum"`
	SumPrice float64                  `json:"sum_price"`
	Apps     []CampaignFullstatsApp   `json:"apps"`
}

// CampaignFullstatsApp — статистика по платформе (1=сайт, 32=Android, 64=iOS).
type CampaignFullstatsApp struct {
	AppType  int                     `json:"appType"`
	Views    int                     `json:"views"`
	Clicks   int                     `json:"clicks"`
	CTR      float64                 `json:"ctr"`
	CPC      float64                 `json:"cpc"`
	CR       float64                 `json:"cr"`
	Orders   int                     `json:"orders"`
	Shks     int                     `json:"shks"`
	Atbs     int                     `json:"atbs"`
	Canceled int                     `json:"canceled"`
	Sum      float64                 `json:"sum"`
	SumPrice float64                 `json:"sum_price"`
	Nms      []CampaignFullstatsNm   `json:"nms"`
}

// CampaignFullstatsNm — статистика по товару в рамках платформы.
type CampaignFullstatsNm struct {
	NmID     int     `json:"nmId"`
	Name     string  `json:"name"`
	Views    int     `json:"views"`
	Clicks   int     `json:"clicks"`
	CTR      float64 `json:"ctr"`
	CPC      float64 `json:"cpc"`
	CR       float64 `json:"cr"`
	Orders   int     `json:"orders"`
	Shks     int     `json:"shks"`
	Atbs     int     `json:"atbs"`
	Canceled int     `json:"canceled"`
	Sum      float64 `json:"sum"`
	SumPrice float64 `json:"sum_price"`
}

// ============================================================================
// Flattened Row Types (for database storage)
// ============================================================================

// CampaignAppStatsRow — плоская строка для таблицы campaign_stats_app.
// Зерно: (advert_id, stats_date, app_type).
type CampaignAppStatsRow struct {
	AdvertID  int
	StatsDate string
	AppType   int
	Views     int
	Clicks    int
	CTR       float64
	CPC       float64
	CR        float64
	Orders    int
	Shks      int
	Atbs      int
	Canceled  int
	Sum       float64
	SumPrice  float64
}

// CampaignNmStatsRow — плоская строка для таблицы campaign_stats_nm.
// Зерно: (advert_id, stats_date, app_type, nm_id).
type CampaignNmStatsRow struct {
	AdvertID  int
	StatsDate string
	AppType   int
	NmID      int
	NmName    string
	Views     int
	Clicks    int
	CTR       float64
	CPC       float64
	CR        float64
	Orders    int
	Shks      int
	Atbs      int
	Canceled  int
	Sum       float64
	SumPrice  float64
}

// CampaignBoosterStatsRow — плоская строка для таблицы campaign_booster_stats.
// Зерно: (advert_id, stats_date, nm_id).
type CampaignBoosterStatsRow struct {
	AdvertID    int
	StatsDate   string
	NmID        int
	AvgPosition float64
}

// ============================================================================
// Flatten Helpers — чистые функции для преобразования иерархии → плоских строк
// ============================================================================

// FlattenAllResult содержит все плоские строки из 4-уровневой иерархии fullstats.
// Получается за один обход вместо 4 отдельных (FlattenToDailyStats, etc.).
type FlattenAllResult struct {
	Daily   []CampaignDailyStats
	App     []CampaignAppStatsRow
	Nm      []CampaignNmStatsRow
	Booster []CampaignBoosterStatsRow
}

// FlattenAll преобразует CampaignFullstatsResponse в плоские строки для всех 4 таблиц
// за один обход иерархии Campaign → Day → App → Nm.
// Использует string interning для NmName: одно имя товара разделяется между всеми
// записями с одинаковым nmId (экономия ~92× аллокаций на товар при 31 дне × 3 платформы).
func FlattenAll(responses []CampaignFullstatsResponse) FlattenAllResult {
	var r FlattenAllResult
	// String interning pool: один экземпляр строки на nmId
	nmNames := make(map[int]string, 64)

	for _, campaign := range responses {
		// Booster stats — отдельный срез, не входит в Day→App→Nm
		for _, bs := range campaign.BoosterStats {
			r.Booster = append(r.Booster, CampaignBoosterStatsRow{
				AdvertID:    campaign.AdvertID,
				StatsDate:   parseDateToYMD(bs.Date),
				NmID:        bs.Nm,
				AvgPosition: bs.AvgPosition,
			})
		}

		for _, day := range campaign.Days {
			dateStr := parseDateToYMD(day.Date)

			// Daily level
			r.Daily = append(r.Daily, CampaignDailyStats{
				AdvertID: campaign.AdvertID,
				StatsDate: dateStr,
				Views:    day.Views,
				Clicks:   day.Clicks,
				CTR:      day.CTR,
				CPC:      day.CPC,
				CR:       day.CR,
				Orders:   day.Orders,
				Shks:     day.Shks,
				Atbs:     day.Atbs,
				Canceled: day.Canceled,
				Sum:      day.Sum,
				SumPrice: day.SumPrice,
			})

			for _, app := range day.Apps {
				// App level
				r.App = append(r.App, CampaignAppStatsRow{
					AdvertID: campaign.AdvertID,
					StatsDate: dateStr,
					AppType:   app.AppType,
					Views:     app.Views,
					Clicks:    app.Clicks,
					CTR:       app.CTR,
					CPC:       app.CPC,
					CR:        app.CR,
					Orders:    app.Orders,
					Shks:      app.Shks,
					Atbs:      app.Atbs,
					Canceled:  app.Canceled,
					Sum:       app.Sum,
					SumPrice:  app.SumPrice,
				})

				for _, nm := range app.Nms {
					// Nm level — intern name by nmId
					if _, ok := nmNames[nm.NmID]; !ok {
						nmNames[nm.NmID] = nm.Name
					}
					r.Nm = append(r.Nm, CampaignNmStatsRow{
						AdvertID: campaign.AdvertID,
						StatsDate: dateStr,
						AppType:   app.AppType,
						NmID:      nm.NmID,
						NmName:    nmNames[nm.NmID],
						Views:     nm.Views,
						Clicks:    nm.Clicks,
						CTR:       nm.CTR,
						CPC:       nm.CPC,
						CR:        nm.CR,
						Orders:    nm.Orders,
						Shks:      nm.Shks,
						Atbs:      nm.Atbs,
						Canceled:  nm.Canceled,
						Sum:       nm.Sum,
						SumPrice:  nm.SumPrice,
					})
				}
			}
		}
	}
	return r
}

// parseDateToYMD converts RFC3339 date string to YYYY-MM-DD format.
// Handles "2025-09-07T00:00:00Z" → "2025-09-07" and plain "2025-09-07".
func parseDateToYMD(dateStr string) string {
	if idx := len(dateStr); idx > 10 {
		// RFC3339 format — strip time component
		return dateStr[:10]
	}
	return dateStr
}

// CampaignProduct — связь кампании с товаром.
// Заполняется из CampaignFullstatsResponse.Days[].Apps[].Nms[].
type CampaignProduct struct {
	AdvertID int    `json:"advertId"`
	NmID     int    `json:"nmId"`
	Name     string `json:"name"`
	Views    int    `json:"views"`
	Clicks   int    `json:"clicks"`
	Orders   int    `json:"orders"`
	Sum      float64 `json:"sum"`
}

// StatusName возвращает человекочитаемое название статуса.
func StatusName(status int) string {
	switch status {
	case CampaignStatusDeleted:
		return "Удалена"
	case CampaignStatusReady:
		return "Готова к запуску"
	case CampaignStatusFinished:
		return "Завершена"
	case CampaignStatusCanceled:
		return "Отменена"
	case CampaignStatusActive:
		return "Активна"
	case CampaignStatusPaused:
		return "На паузе"
	default:
		return "Неизвестно"
	}
}

// TypeName возвращает человекочитаемое название типа кампании.
func TypeName(campaignType int) string {
	switch campaignType {
	case CampaignTypeSearch:
		return "Поиск"
	case CampaignTypeAuto:
		return "Автоматическая"
	case CampaignTypeBooster:
		return "Бустер"
	case CampaignTypeCatalog:
		return "Каталог"
	default:
		return "Неизвестно"
	}
}

// ============================================================================
// V2 Promotion API Types — normquery, bid recommendations, finance, calendar
// ============================================================================

// --- Campaign Bids (extracted from AdvertDetail.NmSettings) ---

// CampaignBidRow — плоская строка для таблицы campaign_bids.
// Заполняется из AdvertDetail.NmSettings[].BidsKopecks.
type CampaignBidRow struct {
	AdvertID    int
	NmID        int
	SubjectID   int
	SubjectName string
	BidSearch   int // копейки
	BidReco     int // копейки
}

// --- Normquery (batched API — up to 100 items per request) ---

// NormqueryItem — пара (advert_id, nm_id) для батч-запросов normquery.
type NormqueryItem struct {
	AdvertID int `json:"advert_id"`
	NmID     int `json:"nm_id"`
}

// NormqueryStatsRequest — body для POST /adv/v0/normquery/stats.
type NormqueryStatsRequest struct {
	From  string          `json:"from"`  // YYYY-MM-DD
	To    string          `json:"to"`    // YYYY-MM-DD
	Items []NormqueryItem `json:"items"` // max 100
}

// NormqueryStatsResponse — ответ от POST /adv/v0/normquery/stats.
type NormqueryStatsResponse struct {
	Stats []NormqueryStatsGroup `json:"stats"`
}

// NormqueryStatsGroup — статистика по одному (advert_id, nm_id).
type NormqueryStatsGroup struct {
	AdvertID int                  `json:"advert_id"`
	NmID     int                  `json:"nm_id"`
	Stats    []NormqueryStatRow   `json:"stats"`
}

// NormqueryStatRow — статистика по одному поисковому кластеру.
type NormqueryStatRow struct {
	NormQuery string   `json:"norm_query"`
	Views     *int     `json:"views"`  // nil для CPC-кампаний
	Clicks    int      `json:"clicks"`
	Atbs      int      `json:"atbs"`
	Orders    int      `json:"orders"`
	CTR       *float64 `json:"ctr"`    // nil для CPC-кампаний
	CPC       float64  `json:"cpc"`
	CPM       *float64 `json:"cpm"`    // nil для CPC-кампаний
	AvgPos    float64  `json:"avg_pos"`
	SHKS      int      `json:"shks"`
	Spend     float64  `json:"spend"`
}

// NormqueryListRequest — body для POST /adv/v0/normquery/list.
type NormqueryListRequest struct {
	Items []NormqueryItem `json:"items"` // max 100
}

// NormqueryListResponse — ответ от POST /adv/v0/normquery/list.
type NormqueryListResponse struct {
	Items []NormqueryListItem `json:"items"`
}

// NormqueryListItem — активные/исключённые кластеры для одного (advert_id, nm_id).
type NormqueryListItem struct {
	AdvertID    int                 `json:"advertId"`
	NmID        int                 `json:"nmId"`
	NormQueries NormqueryClusters   `json:"normQueries"`
}

// NormqueryClusters — активные и исключённые поисковые кластеры.
type NormqueryClusters struct {
	Active   []string `json:"active"`
	Excluded []string `json:"excluded"`
}

// NormqueryBidsRequest — body для POST /adv/v0/normquery/get-bids.
type NormqueryBidsRequest struct {
	Items []NormqueryItem `json:"items"` // max 100
}

// NormqueryBidsResponse — ответ от POST /adv/v0/normquery/get-bids.
type NormqueryBidsResponse struct {
	Bids []NormqueryBidItem `json:"bids"`
}

// NormqueryBidItem — ставка для одного поискового кластера.
type NormqueryBidItem struct {
	AdvertID  int    `json:"advert_id"`
	NmID      int    `json:"nm_id"`
	Bid       int    `json:"bid"`        // копейки
	NormQuery string `json:"norm_query"` // поисковый кластер
}

// NormqueryMinusRequest — body для POST /adv/v0/normquery/get-minus.
type NormqueryMinusRequest struct {
	Items []NormqueryItem `json:"items"`
}

// NormqueryMinusResponse — ответ от POST /adv/v0/normquery/get-minus.
type NormqueryMinusResponse struct {
	Items []NormqueryMinusItem `json:"items"`
}

// NormqueryMinusItem — минус-фразы для одного (advert_id, nm_id).
type NormqueryMinusItem struct {
	AdvertID    int      `json:"advert_id"`
	NmID        int      `json:"nm_id"`
	NormQueries []string `json:"norm_queries"`
}

// --- Bid Recommendations ---

// BidRecommendationsResponse — ответ от GET /api/advert/v0/bids/recommendations.
type BidRecommendationsResponse struct {
	AdvertID int                      `json:"advertId"`
	NmID     int                      `json:"nmId"`
	Base     BidRecommendBase         `json:"base"`
	NormQueries []BidRecommendNormQ   `json:"normQueries"`
}

// BidRecommendBase — базовые рекомендованные ставки.
type BidRecommendBase struct {
	CompetitiveBid BidRecommendLevel `json:"competitiveBid"`
	LeadersBid     BidRecommendLevel `json:"leadersBid"`
	Top2           BidRecommendLevel `json:"top2"`
}

// BidRecommendLevel — ставка в копейках.
type BidRecommendLevel struct {
	BidKopecks int `json:"bidKopecks"`
}

// BidRecommendNormQ — рекомендованные ставки по поисковому кластеру.
type BidRecommendNormQ struct {
	NormQuery   string             `json:"normQuery"`
	ReachMax    BidRecommendReach  `json:"reachMax"`
	ReachMedium BidRecommendReach  `json:"reachMedium"`
	ReachMin    BidRecommendReach  `json:"reachMin"`
}

// BidRecommendReach — ставка и минимум для уровня охвата.
type BidRecommendReach struct {
	BidKopecks    int `json:"bidKopecks"`
	BidKopecksMin int `json:"bidKopecksMin"`
}

// --- Finance ---

// ExpensesResponse — ответ от GET /adv/v1/upd.
type ExpensesResponse []ExpenseRow

// ExpenseRow — запись о списании средств со счёта кампании.
// Источник: GET /adv/v1/upd.
type ExpenseRow struct {
	UpdNum      int    `json:"updNum"`      // Номер документа
	UpdTime     string `json:"updTime"`     // Время списания (nullable, RFC3339)
	UpdSum      int    `json:"updSum"`      // Сумма списания, копейки
	AdvertID    int    `json:"advertId"`    // ID кампании
	CampName    string `json:"campName"`    // Название кампании
	AdvertType  int    `json:"advertType"`  // Тип кампании (6,8,9,50)
	PaymentType string `json:"paymentType"` // Источник: Баланс/Бонусы/Счёт/Кэшбэк
	AdvertStatus int   `json:"advertStatus"` // Статус кампании на момент списания
}

// BalanceResponse — ответ от GET /adv/v1/balance.
type BalanceResponse struct {
	Balance   int              `json:"balance"` // ₽
	Net       int              `json:"net"`     // ₽
	Bonus     int              `json:"bonus"`   // ₽
	Cashbacks []BalanceCashback `json:"cashbacks"`
}

// BalanceCashback — бонусный кэшбэк.
type BalanceCashback struct {
	Sum            int    `json:"sum"`
	Percent        int    `json:"percent"`
	ExpirationDate string `json:"expiration_date"`
}

// PaymentsResponse — ответ от GET /adv/v1/payments.
type PaymentsResponse []PaymentRow

// PaymentRow — запись о пополнении баланса.
type PaymentRow struct {
	ID         int    `json:"id"`
	Date       string `json:"date"`
	Sum        int    `json:"sum"`  // ₽
	Type       int    `json:"type"` // 0=счёт, 1=баланс, 3=карта
	StatusID   int    `json:"statusId"`
	CardStatus string `json:"cardStatus"`
}

// --- Calendar ---

// CalendarPromotionsResponse — ответ от GET /api/v1/calendar/promotions.
type CalendarPromotionsResponse struct {
	Data CalendarPromotionsData `json:"data"`
}

// CalendarPromotionsData — обёртка данных ответа.
type CalendarPromotionsData struct {
	Promotions []CalendarPromotion `json:"promotions"`
}

// CalendarPromotion — акция WB (Мегасейл и пр.).
type CalendarPromotion struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Start string `json:"startDateTime"`
	End   string `json:"endDateTime"`
	Type  string `json:"type"`
}

// CalendarPromotionDetail — детальная информация об акции.
// Источник: GET /api/v1/calendar/promotions/details
type CalendarPromotionDetail struct {
	ID                        int                        `json:"id"`
	Name                      string                     `json:"name"`
	Description               string                     `json:"description"`
	Advantages                []string                   `json:"advantages"`
	StartDateTime             string                     `json:"startDateTime"`
	EndDateTime               string                     `json:"endDateTime"`
	InPromoActionLeftovers    int                        `json:"inPromoActionLeftovers"`
	InPromoActionTotal        int                        `json:"inPromoActionTotal"`
	NotInPromoActionLeftovers int                        `json:"notInPromoActionLeftovers"`
	NotInPromoActionTotal     int                        `json:"notInPromoActionTotal"`
	ParticipationPercentage   int                        `json:"participationPercentage"`
	Type                      string                     `json:"type"`
	ExceptionProductsCount    int                        `json:"exceptionProductsCount"`
	Ranging                   []CalendarPromotionRanging `json:"ranging"`
}

// CalendarPromotionRanging — условие буста в поиске.
type CalendarPromotionRanging struct {
	Condition        string `json:"condition"`         // productsInPromotion | calculateProducts | allProducts
	ParticipationRate int   `json:"participationRate"` // 0-100%
	Boost            int    `json:"boost"`             // % поднятия в поиске
}

// CalendarPromotionDetailsResponse — обёртка ответа /details.
type CalendarPromotionDetailsResponse struct {
	Data CalendarPromotionDetailsData `json:"data"`
}

// CalendarPromotionDetailsData — контейнер деталей акций.
type CalendarPromotionDetailsData struct {
	Promotions []CalendarPromotionDetail `json:"promotions"`
}

// CalendarPromotionNom — товар, подходящий для участия в акции.
// Источник: GET /api/v1/calendar/promotions/nomenclatures
type CalendarPromotionNom struct {
	ID           int     `json:"id"`           // nm_id
	InAction     bool    `json:"inAction"`
	Price        float64 `json:"price"`        // текущая розничная цена
	CurrencyCode string  `json:"currencyCode"` // ISO 4217
	PlanPrice    float64 `json:"planPrice"`    // цена во время акции
	Discount     int     `json:"discount"`     // текущая скидка %
	PlanDiscount int     `json:"planDiscount"` // рекомендованная скидка %
}

// CalendarPromotionNomsResponse — обёртка ответа /nomenclatures.
type CalendarPromotionNomsResponse struct {
	Data CalendarPromotionNomsData `json:"data"`
}

// CalendarPromotionNomsData — контейнер товаров акции.
type CalendarPromotionNomsData struct {
	Nomenclatures []CalendarPromotionNom `json:"nomenclatures"`
}

// --- Campaign Budget ---

// BudgetResponse — ответ от GET /adv/v1/budget.
type BudgetResponse struct {
	Total int `json:"total"` // Бюджет кампании, ₽
}

// --- Minimum Bids ---

// MinBidsRequest — body для POST /api/advert/v1/bids/min.
type MinBidsRequest struct {
	AdvertID       int      `json:"advert_id"`
	NmIDs          []int    `json:"nm_ids"`
	PaymentType    string   `json:"payment_type"`    // cpm | cpc
	PlacementTypes []string `json:"placement_types"` // search, recommendation, combined
}

// MinBidsResponse — ответ от POST /api/advert/v1/bids/min.
type MinBidsResponse struct {
	Bids []MinBidItem `json:"bids"`
}

// MinBidItem — минимальные ставки для одного товара.
type MinBidItem struct {
	NmID int          `json:"nm_id"`
	Bids []MinBidEntry `json:"bids"`
}

// MinBidEntry — минимальная ставка для одного размещения.
type MinBidEntry struct {
	Placement string `json:"placement"` // combined, search, recommendation
	Bid       int    `json:"bid"`       // копейки
}
