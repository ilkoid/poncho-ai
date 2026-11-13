// hypothesis.go — дерево гипотез «почему нет заказов» и правило «цена не первая».
//
// Письмо гендира: «снижение цены не должно быть автоматическим решением при
// отсутствии заказов. Сначала необходимо установить и устранить причины».
// Дерево идёт сверху вниз: видимость → контент → размеры/остаток; цена — только
// последняя упомянутая возможность и НЕ выдаётся утилитой как гипотеза.
package main

// Коды гипотез (код удобен для группировки в сводке xlsx и будущих версий).
const (
	HypoPresales     = "pre_sales"     // продажи не начались — остатка не было
	HypoShadowed     = "shadowed"      // WB скрыл карточку из каталога
	HypoNoVisibility = "no_visibility" // нет показов и нет продвижения
	HypoPromoBroken  = "promo_broken"  // показов нет, хотя кампания активна
	HypoNaming       = "naming"        // показы есть, корзин нет + плохое наименование
	HypoContent      = "content"       // показы есть, корзин нет — контент/фото
	HypoSizes        = "sizes"         // корзины есть, заказов нет + выбитый ряд
	HypoConversion   = "conversion"    // корзины есть, заказов нет — конверсия финальная
	HypoUnknown      = "unknown"       // данных мало — разбирать вручную
)

// Hypothesis — вход дерева: вычисленные факторы строки. Пороговые вердикты
// (короткое наименование, мало фото, неполный ряд) считает вызывающий код по
// конфигу — утилита хранит в БД только итог, вердикты колонками не хранятся.
type Hypothesis struct {
	Signal        string // slow_start | stall | pre_sales
	IsShadowed    *bool  // NULL = нет данных item_ratings
	Opens7d       int64
	Carts7d       int64
	TitleBad      bool // короче порога / однословное / == предмету
	PhotoLow      bool // photo_count < порога
	SizesRatioLow bool // sizes_ratio ниже порога (нет данных ≠ неполон)
	StockTotal    *int64
	Campaigns     int
}

// TopHypothesis — главная гипотеза строки по дереву (код + текст для отчёта).
func TopHypothesis(h Hypothesis) (code, text string) {
	switch {
	case h.Signal == SignalPreSales:
		return HypoPresales, "продажи не начались: остатка на WB не было"
	case h.IsShadowed != nil && *h.IsShadowed:
		return HypoShadowed, "скрыта из каталога WB (isShadowed) — разблокировать/проверить карточку"
	case h.Opens7d == 0 && h.Campaigns == 0:
		return HypoNoVisibility, "видимость: показов нет, продвижения нет — карточку не находят"
	case h.Opens7d == 0:
		return HypoPromoBroken, "видимость: показов нет при активной кампании — проверить ставки/кластеры"
	case h.Carts7d == 0 && h.TitleBad:
		return HypoNaming, "наименование: карточку открывают, но не добавляют — слишком простое название"
	case h.Carts7d == 0:
		return HypoContent, "контент: открывают, но в корзину не кладут — фото/описание/восприятие цены"
	case h.SizesRatioLow:
		return HypoSizes, "размерный ряд: на WB представлен неполно относительно 1С"
	case h.Carts7d > 0:
		return HypoConversion, "конверсия: корзина есть, заказа нет — проверить остатки размеров и условия"
	default:
		return HypoUnknown, "данных мало — разобрать вручную"
	}
}
