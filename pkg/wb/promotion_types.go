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
