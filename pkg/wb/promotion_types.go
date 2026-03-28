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
