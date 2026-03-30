// Модели данных

package wb

// Common Response Wrapper
type APIResponse[T any] struct {
	Data      T           `json:"data"`
	Error     bool        `json:"error"`
	ErrorText string      `json:"errorText"`
	// AdditionalErrors игнорируем, так как тип плавает (string/null)
}

// 1. Parent Category
type ParentCategory struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	IsVisible bool   `json:"isVisible"`
}

// 2. Subject (Предмет)
type Subject struct {
	SubjectID   int    `json:"subjectID"`
	ParentID    int    `json:"parentID"`
	SubjectName string `json:"subjectName"`
	ParentName  string `json:"parentName"`
}

// 3. Characteristic (Характеристика)
type Characteristic struct {
	CharcID     int    `json:"charcID"`
	SubjectName string `json:"subjectName"`
	SubjectID   int    `json:"subjectID"`
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	UnitName    string `json:"unitName"`
	MaxCount    int    `json:"maxCount"`
	Popular     bool   `json:"popular"`
	CharcType   int    `json:"charcType"` // 1: string, 4: number? Нужно уточнять в доке, но int безопасен
}

type Color struct {
    Name       string `json:"name"`       // "персиковый мелок"
    ParentName string `json:"parentName"` // "оранжевый"
}

type Country struct {
    Name     string `json:"name"`     // "Китай"
    FullName string `json:"fullName"` // "Китайская Народная Республика"
}

// Brand представляет бренд в справочнике WB
type Brand struct {
    ID      int    `json:"id"`      // Уникальный ID бренда
    LogoURL string `json:"logoUrl"` // URL логотипа бренда
    Name    string `json:"name"`    // Название бренда
}

// ============================================================================
// Feedbacks API Types
// ============================================================================

// FeedbacksResponse представляет ответ от API отзывов.
type FeedbacksResponse struct {
	Data struct {
		Feedbacks       []Feedback `json:"feedbacks"`
		CountUnanswered int        `json:"countUnanswered"`
		CountArchive    int        `json:"countArchive"`
	} `json:"data"`
	Error     bool   `json:"error"`
	ErrorText string `json:"errorText"`
}

// Feedback представляет отзыв на товар.
type Feedback struct {
	ID              string           `json:"id"`
	Text            string           `json:"text"`
	ProductValuation int              `json:"productValuation"`
	CreatedDate     string           `json:"createdDate"`
	Answer          *FeedbackAnswer  `json:"answer,omitempty"`
	ProductDetails  FeedbackProduct  `json:"productDetails"`
	UserName        string           `json:"userName"`
	PhotoLinks      []FeedbackPhoto  `json:"photoLinks,omitempty"`
}

// FeedbackAnswer представляет ответ продавца на отзыв.
type FeedbackAnswer struct {
	Text     string `json:"text"`
	State    string `json:"state"`
	Editable bool   `json:"editable"`
}

// FeedbackProduct представляет информацию о товаре в отзыве.
type FeedbackProduct struct {
	ImtID           int    `json:"imtId"`
	NmId            int    `json:"nmId"`
	ProductName     string `json:"productName"`
	SupplierArticle string `json:"supplierArticle"`
	BrandName       string `json:"brandName"`
}

// FeedbackPhoto представляет фото в отзыве.
type FeedbackPhoto struct {
	FullSize string `json:"fullSize"`
	MiniSize string `json:"miniSize"`
}

// QuestionsResponse представляет ответ от API вопросов.
type QuestionsResponse struct {
	Data struct {
		Questions        []Question `json:"questions"`
		CountUnanswered  int        `json:"countUnanswered"`
		CountArchive     int        `json:"countArchive"`
	} `json:"data"`
	Error     bool   `json:"error"`
	ErrorText string `json:"errorText"`
}

// Question представляет вопрос о товаре.
type Question struct {
	ID            string           `json:"id"`
	Text          string           `json:"text"`
	CreatedDate   string           `json:"createdDate"`
	State         string           `json:"state"`
	Answer        *QuestionAnswer  `json:"answer,omitempty"`
	ProductDetails QuestionProduct  `json:"productDetails"`
	WasViewed     bool             `json:"wasViewed"`
}

// QuestionAnswer представляет ответ на вопрос.
type QuestionAnswer struct {
	Text string `json:"text"`
}

// QuestionProduct представляет информацию о товаре в вопросе.
type QuestionProduct struct {
	ImtID           int    `json:"imtId"`
	NmId            int    `json:"nmId"`
	ProductName     string `json:"productName"`
	SupplierArticle string `json:"supplierArticle"`
	SupplierName    string `json:"supplierName"`
	BrandName       string `json:"brandName"`
}

// UnansweredFeedbacksCountsResponse представляет ответ с количеством неотвеченных отзывов.
type UnansweredFeedbacksCountsResponse struct {
	Data struct {
		CountUnanswered      int    `json:"countUnanswered"`
		CountUnansweredToday int    `json:"countUnansweredToday"`
		Valuation            string `json:"valuation"` // Средняя оценка (будет удалена WB после 11 декабря)
	} `json:"data"`
	Error     bool   `json:"error"`
	ErrorText string `json:"errorText"`
}

// UnansweredQuestionsCountsResponse представляет ответ с количеством неотвеченных вопросов.
type UnansweredQuestionsCountsResponse struct {
	Data struct {
		CountUnanswered      int `json:"countUnanswered"`
		CountUnansweredToday int `json:"countUnansweredToday"`
	} `json:"data"`
	Error     bool   `json:"error"`
	ErrorText string `json:"errorText"`
}

// NewFeedbacksQuestionsResponse представляет ответ о наличии новых отзывов/вопросов.
type NewFeedbacksQuestionsResponse struct {
	Data struct {
		HasNewQuestions bool `json:"hasNewQuestions"`
		HasNewFeedbacks  bool `json:"hasNewFeedbacks"`
	} `json:"data"`
	Error     bool   `json:"error"`
	ErrorText string `json:"errorText"`
}

// ============================================================================
// Product Search API Types (for supplierArticle -> nmID mapping)
// ============================================================================

// ProductsSearchRequest представляет запрос к API поиска товаров.
type ProductsSearchRequest struct {
	Filter struct {
		ArticleInList []string `json:"article_in_list"` // Артикулы поставщика (max 1000)
	} `json:"filter"`
}

// ProductsSearchResponse представляет ответ от API поиска товаров.
type ProductsSearchResponse struct {
	Data struct {
		Products []ProductInfo `json:"products"`
	} `json:"data"`
	Status struct {
		ErrorCode    *int    `json:"error_code,omitempty"`
		ErrorMessage *string `json:"error_message,omitempty"`
		Message      *string `json:"message,omitempty"`
	} `json:"status"`
}

// ProductInfo представляет информацию о товаре.
type ProductInfo struct {
	NmID          int    `json:"nmID"`
	Article       string `json:"article"`        // Артикул поставщика (vendor code)
	Name          string `json:"name"`
	Price         int    `json:"price"`
	SalePriceUfact int   `json:"salePriceUfact"`
}

// ProductSearchResult представляет результат поиска товара для LLM.
type ProductSearchResult struct {
	NmID            int    `json:"nmId"`            // WB ID товара
	SupplierArticle string `json:"supplierArticle"` // Артикул поставщика
	Name            string `json:"name"`            // Название товара
	Price           int    `json:"price"`           // Цена
	Found           bool   `json:"found"`           // Найден ли товар
}

// ============================================================================
// Content API Cards List Types (for Promotion category tokens)
// ============================================================================

// CardsListRequest представляет запрос для получения списка карточек товаров.
type CardsListRequest struct {
	Settings CardsSettings `json:"settings"`
}

// CardsSettings содержит настройки запроса карточек.
type CardsSettings struct {
	Cursor CardsCursor `json:"cursor"`
	Filter *CardsFilter `json:"filter,omitempty"`
	Sort   *CardsSort   `json:"sort,omitempty"`
}

// CardsCursor содержит параметры пагинации.
type CardsCursor struct {
	Limit    int    `json:"limit"`              // Максимум 100
	UpdatedAt string `json:"updatedAt,omitempty"` // Для пагинации
	NmID      int    `json:"nmID,omitempty"`      // Для пагинации
}

// CardsFilter содержит параметры фильтрации карточек.
type CardsFilter struct {
	TextSearch string `json:"textSearch,omitempty"` // Поиск по артикулу/названию
	// Другие поля фильтра можно добавить по необходимости
}

// CardsSort содержит параметры сортировки.
type CardsSort struct {
	Ascending bool `json:"ascending,omitempty"`
}

// CardsListResponse представляет ответ от Content API с карточками товаров.
type CardsListResponse struct {
	Cards []ProductCard `json:"cards"`
	Cursor *CardsCursorResponse `json:"cursor,omitempty"`
	Error  bool          `json:"error"`
	ErrorText string      `json:"errorText,omitempty"`
}

// CardsCursorResponse содержит информацию о пагинации в ответе.
type CardsCursorResponse struct {
	UpdatedAt string `json:"updatedAt"`
	NmID      int    `json:"nmID"`
	Total     int    `json:"total"`
}

// ProductCard представляет карточку товара от Content API.
type ProductCard struct {
	NmID       int    `json:"nmID"`
	ImtID      int    `json:"imtID"`
	NmUUID     string `json:"nmUUID"`
	SubjectID  int    `json:"subjectID"`
	SubjectName string `json:"subjectName"`
	VendorCode string `json:"vendorCode"` // Артикул поставщика!
	Brand      string `json:"brand"`
	Title      string `json:"title"`
	Description string `json:"description"`
	Photos     []ProductPhoto `json:"photos"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

// ProductPhoto представляет фото товара.
type ProductPhoto struct {
	Big      string `json:"big"`
	C246x328 string `json:"c246x328"`
	Square   string `json:"square"`
}

// ============================================================================
// Financial API Types (for reportDetailByPeriod)
// ============================================================================

// RealizationReportRow представляет строку из отчета реализации.
// Используется для подсчета транзакций и возвратов.
// Поля, которые могут быть null, представлены как указатели или float64.
type RealizationReportRow struct {
	RrdID              int     `json:"rrd_id"`                        // Уникальный ID записи (для пагинации)
	RealizationReportID int    `json:"realizationreport_id,omitempty"` // ID отчёта реализации
	DocTypeName        string  `json:"doc_type_name"`                 // "Продажа", "Возврат"
	SaleID             string  `json:"sale_id"`                       // ID продажи
	DateFrom           string  `json:"date_from"`                     // Начало периода
	DateTo             string  `json:"date_to"`                       // Конец периода
	SupplierArticle    string  `json:"sa_name"`                       // Артикул поставщика (API: sa_name)
	SubjectName        string  `json:"subject_name"`                  // Предмет
	NmID               int     `json:"nm_id"`                         // ID товара на WB
	BrandName          string  `json:"brand_name"`                    // Бренд
	TechSize           string  `json:"ts_name"`                       // Размер (API: ts_name)
	Barcode            string  `json:"barcode,omitempty"`             // Штрихкод
	Quantity           int     `json:"quantity"`                      // Количество (API: quantity)
	IsCancel           bool    `json:"is_cancel"`                     // Отменен
	CancelDateTime     *string `json:"cancel_date_time,omitempty"`    // Дата отмены

	// Новые поля для FBW фильтрации и более полной информации
	DeliveryMethod     string  `json:"delivery_method,omitempty"`     // Способ доставки: "FBS, (МГТ)", "FBW" и т.д.
	GiBoxTypeName      string  `json:"gi_box_type_name,omitempty"`    // Тип короба: "Монопаллета", "Короб" и т.д.
	OfficeName         string  `json:"office_name,omitempty"`         // Офис/склад
	PPVzForPay         float64 `json:"ppvz_for_pay,omitempty"`        // К выплате продавцу
	RetailPrice        float64 `json:"retail_price,omitempty"`        // Розничная цена
	RetailAmount       float64 `json:"retail_amount,omitempty"`       // Розничная сумма
	SalePercent        float64 `json:"sale_percent,omitempty"`        // Процент продажи
	CommissionPercent  float64 `json:"commission_percent,omitempty"`  // Процент комиссии
	DeliveryRub        float64 `json:"delivery_rub,omitempty"`        // Стоимость доставки
	OrderDT            string  `json:"order_dt,omitempty"`            // Дата заказа
	SaleDT             string  `json:"sale_dt,omitempty"`             // Дата продажи
	RRDT               string  `json:"rr_dt,omitempty"`               // Дата отчета

	// Поля для служебных записей (логистика, ПВЗ, удержания)
	SupplierOperName   string  `json:"supplier_oper_name,omitempty"`  // Тип операции: "Возмещение издержек...", "Удержание"
	ShkID              int64   `json:"shk_id,omitempty"`              // ID штрихкода
	Srid               string  `json:"srid,omitempty"`                // Уникальный ID
	RebillLogisticCost float64 `json:"rebill_logistic_cost,omitempty"` // Стоимость логистики
	PPVzVw             float64 `json:"ppvz_vw,omitempty"`             // Корректировка
	PPVzVwNds          float64 `json:"ppvz_vw_nds,omitempty"`         // НДС корректировки

	// Расшифровка ценообразования и затрат (из Swagger DetailReportItem)
	RetailPriceWithDiscRub    float64 `json:"retail_price_withdisc_rub,omitempty"`    // Цена со скидкой
	PPVzSppPrc               float64 `json:"ppvz_spp_prc,omitempty"`               // СПП процент
	PPVzKvwPrcBase           float64 `json:"ppvz_kvw_prc_base,omitempty"`           // Базовый % к выплате (до категорийного)
	PPVzKvwPrc               float64 `json:"ppvz_kvw_prc,omitempty"`               // % к выплате (с учетом категорийного)
	SupRatingPrcUp           float64 `json:"sup_rating_prc_up,omitempty"`           // Надбавка за рейтинг продавца
	IsKgvpV2                 float64 `json:"is_kgvp_v2,omitempty"`                 // КГВП (корпоративные goodwill)
	PPVzSalesCommission      float64 `json:"ppvz_sales_commission,omitempty"`      // Комиссия за продажу
	AcquiringFee             float64 `json:"acquiring_fee,omitempty"`              // Эквайринг (сумма)
	AcquiringPercent         float64 `json:"acquiring_percent,omitempty"`          // Эквайринг (процент)
	ProductDiscountForReport float64 `json:"product_discount_for_report,omitempty"` // Скидка на товар
	SupplierPromo            float64 `json:"supplier_promo,omitempty"`             // Промокод поставщика
	SellerPromoDiscount      float64 `json:"seller_promo_discount,omitempty"`      // Скидка продавца
	SalePricePromocodeDiscPrc float64 `json:"sale_price_promocode_discount_prc,omitempty"` // Скидка по промокоду (%)
	WibesWbDiscountPercent   float64 `json:"wibes_wb_discount_percent,omitempty"`  // Скидка WB (e-com)
	LoyaltyDiscount          float64 `json:"loyalty_discount,omitempty"`           // Скидка за лояльность
	CashbackAmount           float64 `json:"cashback_amount,omitempty"`            // Кешбэк (сумма)
	CashbackDiscount         float64 `json:"cashback_discount,omitempty"`          // Скидка кешбэка
	CashbackCommissionChange float64 `json:"cashback_commission_change,omitempty"` // Возмещение кешбэка продавцу
	Penalty                  float64 `json:"penalty,omitempty"`                   // Штраф
	Deduction                float64 `json:"deduction,omitempty"`                 // Удержание
	StorageFee               float64 `json:"storage_fee,omitempty"`               // Хранение
	Acceptance               float64 `json:"acceptance,omitempty"`                 // Приемка
	GiID                     int     `json:"gi_id,omitempty"`                     // ID товара в складе
}

// ReportDetailByPeriodRequest представляет запрос к API отчета реализации.
type ReportDetailByPeriodRequest struct {
	DateFrom int    `json:"dateFrom"` // YYYYMMDD
	DateTo   int    `json:"dateTo"`   // YYYYMMDD
	Limit    int    `json:"limit"`    // Макс. 100000
	RrdID    int    `json:"rrdid"`    // Для пагинации (0 при первом запросе)
}

// ReportDetailByPeriodResponse представляет ответ от API отчета реализации.
// WB API возвращает либо массив строк, либо HTTP 204 (No Content) при конце пагинации.
type ReportDetailByPeriodResponse struct {
	Rows  []RealizationReportRow `json:"rows"`  // Массив строк отчета
	Total int                    `json:"total"` // Общее количество (если есть)
	Error *string                `json:"error"` // Ошибка если есть
}

// ============================================================================
// Funnel Analytics Types (for WB Analytics API v3)
// ============================================================================

// FunnelProductMeta stores product metadata for the products table.
// Updated when funnel data is loaded from WB Analytics API v3.
type FunnelProductMeta struct {
	NmID           int     `json:"nmId"`
	VendorCode     string  `json:"vendorCode"`
	Title          string  `json:"title"`
	BrandName      string  `json:"brandName"`
	SubjectID      int     `json:"subjectId"`
	SubjectName    string  `json:"subjectName"`
	ProductRating  float64 `json:"productRating"`
	FeedbackRating float64 `json:"feedbackRating"`
	StockWB        int     `json:"stockWb"`
	StockMP        int     `json:"stockMp"`
	StockBalance   int     `json:"stockBalanceSum"`
}

// FunnelHistoryRow stores daily funnel metrics for funnel_metrics_daily table.
// One row per (nm_id, date) combination.
// Fields match /api/analytics/v3/sales-funnel/products/history response (11 fields).
type FunnelHistoryRow struct {
	NmID       int    `json:"nmId"`
	MetricDate string `json:"date"`

	// Funnel counts
	OpenCount     int `json:"openCount"`
	CartCount     int `json:"cartCount"`
	OrderCount    int `json:"orderCount"`
	BuyoutCount   int `json:"buyoutCount"`
	AddToWishlist int `json:"addToWishlistCount"`

	// Financial metrics
	OrderSum  int `json:"orderSum"`
	BuyoutSum int `json:"buyoutSum"`

	// Conversion rates
	ConversionAddToCart   float64 `json:"addToCartConversion"`
	ConversionCartToOrder float64 `json:"cartToOrderConversion"`
	ConversionBuyout      float64 `json:"buyoutPercent"`
}

// TrendingProduct represents a product with trend analysis results.
// Used in GetTrendingProducts query results.
type TrendingProduct struct {
	NmID               int     `json:"nmId"`
	Title              string  `json:"title"`
	BrandName          string  `json:"brandName"`
	Orders7d           int     `json:"orders7d"`
	OrdersPrev7d       int     `json:"ordersPrev7d"`
	OrderGrowthPercent float64 `json:"orderGrowthPercent"`
	Revenue7d          int     `json:"revenue7d"`
	RevenueGrowth      float64 `json:"revenueGrowth"`
	AvgConversion      float64 `json:"avgConversion"`
	TrendStatus        string  `json:"trendStatus"` // TRENDING_UP, TRENDING_DOWN, STABLE, NEW
}

// ============================================================================
// Aggregated Funnel Types (for /api/analytics/v3/sales-funnel/products)
// ============================================================================

// ProductTag represents a tag in product metadata.
type ProductTag struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// PeriodRange represents date range for funnel periods.
type PeriodRange struct {
	Start string `json:"start"` // YYYY-MM-DD
	End   string `json:"end"`   // YYYY-MM-DD
}

// OrderBy represents sorting parameters.
type OrderBy struct {
	Field string `json:"field"` // openCard, orders, buyouts, etc.
	Mode  string `json:"mode"`  // asc, desc
}

// FunnelAggregatedRequest represents request to /api/analytics/v3/sales-funnel/products.
type FunnelAggregatedRequest struct {
	SelectedPeriod PeriodRange `json:"selectedPeriod"`
	PastPeriod     *PeriodRange `json:"pastPeriod,omitempty"`
	NmIDs          []int        `json:"nmIds,omitempty"`
	BrandNames     []string     `json:"brandNames,omitempty"`
	SubjectIDs     []int        `json:"subjectIds,omitempty"`
	TagIDs         []int        `json:"tagIds,omitempty"`
	SkipDeletedNm  bool         `json:"skipDeletedNm,omitempty"`
	OrderBy        *OrderBy     `json:"orderBy,omitempty"`
	Limit          int          `json:"limit,omitempty"`
	Offset         int          `json:"offset,omitempty"`
}

// FunnelAggregatedResponse represents full API response from aggregated funnel endpoint.
type FunnelAggregatedResponse struct {
	Data struct {
		Products []FunnelAggregatedProduct `json:"products"`
		Currency string                   `json:"currency"`
	} `json:"data"`
}

// ProductStocks represents stock levels for a product.
// API returns this as a nested object: {"wb": 50, "mp": 20, "balanceSum": 70}
type ProductStocks struct {
	WB         int `json:"wb"`
	MP         int `json:"mp"`
	BalanceSum int `json:"balanceSum"`
}

// FunnelProductExtended extends FunnelProductMeta with tags and stocks.
type FunnelProductExtended struct {
	FunnelProductMeta
	Stocks ProductStocks `json:"stocks"`
	Tags   []ProductTag  `json:"tags"`
}

// FunnelAggregatedProduct represents one product in aggregated response.
type FunnelAggregatedProduct struct {
	Product   FunnelProductExtended     `json:"product"`
	Statistic FunnelAggregatedStatistic `json:"statistic"`
}

// FunnelAggregatedStatistic contains selected, past, and comparison periods.
type FunnelAggregatedStatistic struct {
	Selected   FunnelPeriodStats       `json:"selected"`
	Past       *FunnelPeriodStats      `json:"past,omitempty"`
	Comparison *FunnelComparisonStats  `json:"comparison,omitempty"`
}

// FunnelPeriodStats contains metrics for a single period.
type FunnelPeriodStats struct {
	Period               PeriodRange     `json:"period"`
	OpenCount            int             `json:"openCount"`
	CartCount            int             `json:"cartCount"`
	OrderCount           int             `json:"orderCount"`
	OrderSum             int             `json:"orderSum"`
	BuyoutCount          int             `json:"buyoutCount"`
	BuyoutSum            int             `json:"buyoutSum"`
	CancelCount          int             `json:"cancelCount"`
	CancelSum            int             `json:"cancelSum"`
	AvgPrice             int             `json:"avgPrice"`
	AvgOrdersCountPerDay float64         `json:"avgOrdersCountPerDay"`
	ShareOrderPercent    float64         `json:"shareOrderPercent"`
	AddToWishlist        int             `json:"addToWishlist"`
	TimeToReady          FunnelTimeToReady `json:"timeToReady"`
	LocalizationPercent  float64         `json:"localizationPercent"`
	WBClub               FunnelWBClubStats `json:"wbClub"`
	Conversions          FunnelConversionStats `json:"conversions"`
}

// FunnelTimeToReady contains order processing time metrics.
type FunnelTimeToReady struct {
	Days  int `json:"days"`
	Hours int `json:"hours"`
	Mins  int `json:"mins"`
}

// FunnelWBClubStats contains WB Club specific metrics.
type FunnelWBClubStats struct {
	OrderCount          int     `json:"orderCount"`
	OrderSum            int     `json:"orderSum"`
	BuyoutSum           int     `json:"buyoutSum"`
	BuyoutCount         int     `json:"buyoutCount"`
	CancelSum           int     `json:"cancelSum"`
	CancelCount         int     `json:"cancelCount"`
	AvgPrice            int     `json:"avgPrice"`
	BuyoutPercent       float64 `json:"buyoutPercent"`
	AvgOrderCountPerDay float64 `json:"avgOrderCountPerDay"`
}

// FunnelConversionStats contains conversion rate metrics.
type FunnelConversionStats struct {
	AddToCartPercent   float64 `json:"addToCartPercent"`
	CartToOrderPercent float64 `json:"cartToOrderPercent"`
	BuyoutPercent      float64 `json:"buyoutPercent"`
}

// FunnelComparisonStats contains comparison between periods.
type FunnelComparisonStats struct {
	OpenCountDynamic            int                      `json:"openCountDynamic"`
	CartCountDynamic            int                      `json:"cartCountDynamic"`
	OrderCountDynamic           int                      `json:"orderCountDynamic"`
	OrderSumDynamic             int                      `json:"orderSumDynamic"`
	BuyoutCountDynamic          int                      `json:"buyoutCountDynamic"`
	BuyoutSumDynamic            int                      `json:"buyoutSumDynamic"`
	CancelCountDynamic          int                      `json:"cancelCountDynamic"`
	CancelSumDynamic            int                      `json:"cancelSumDynamic"`
	AvgOrdersCountPerDayDynamic float64                  `json:"avgOrdersCountPerDayDynamic"`
	AvgPriceDynamic             int                      `json:"avgPriceDynamic"`
	ShareOrderPercentDynamic    float64                  `json:"shareOrderPercentDynamic"`
	AddToWishlistDynamic        int                      `json:"addToWishlistDynamic"`
	TimeToReadyDynamic          FunnelTimeToReady       `json:"timeToReadyDynamic"`
	LocalizationPercentDynamic  float64                  `json:"localizationPercentDynamic"`
	WBClubDynamic               FunnelWBClubDynamic    `json:"wbClubDynamic"`
	ConversionsDynamic         FunnelConversionDynamic `json:"conversions"`
}

// FunnelWBClubDynamic contains WB Club comparison metrics.
type FunnelWBClubDynamic struct {
	OrderCount          int     `json:"orderCount"`
	OrderSum            int     `json:"orderSum"`
	BuyoutSum           int     `json:"buyoutSum"`
	BuyoutCount         int     `json:"buyoutCount"`
	CancelSum           int     `json:"cancelSum"`
	CancelCount         int     `json:"cancelCount"`
	AvgPrice            int     `json:"avgPrice"`
	BuyoutPercent       float64 `json:"buyoutPercent"`
	AvgOrderCountPerDay float64 `json:"avgOrderCountPerDay"`
}

// FunnelConversionDynamic contains conversion comparison metrics.
type FunnelConversionDynamic struct {
	AddToCartPercent   float64 `json:"addToCartPercent"`
	CartToOrderPercent float64 `json:"cartToOrderPercent"`
	BuyoutPercent      float64 `json:"buyoutPercent"`
}

// FunnelAggregatedRow represents a row for funnel_metrics_aggregated table.
// Combines selected, past, and comparison data for one product.
type FunnelAggregatedRow struct {
	NmID int `json:"nmId"`

	// Period (selected)
	PeriodStart string `json:"periodStart"`
	PeriodEnd   string `json:"periodEnd"`

	// Selected period metrics
	SelectedOpenCount            int     `json:"selectedOpenCount"`
	SelectedCartCount            int     `json:"selectedCartCount"`
	SelectedOrderCount           int     `json:"selectedOrderCount"`
	SelectedOrderSum             int     `json:"selectedOrderSum"`
	SelectedBuyoutCount          int     `json:"selectedBuyoutCount"`
	SelectedBuyoutSum            int     `json:"selectedBuyoutSum"`
	SelectedCancelCount          int     `json:"selectedCancelCount"`
	SelectedCancelSum            int     `json:"selectedCancelSum"`
	SelectedAvgPrice             int     `json:"selectedAvgPrice"`
	SelectedAvgOrdersCountPerDay float64 `json:"selectedAvgOrdersCountPerDay"`
	SelectedShareOrderPercent    float64 `json:"selectedShareOrderPercent"`
	SelectedAddToWishlist        int     `json:"selectedAddToWishlist"`
	SelectedLocalizationPercent  float64 `json:"selectedLocalizationPercent"`
	SelectedTimeToReadyDays      int     `json:"selectedTimeToReadyDays"`
	SelectedTimeToReadyHours     int     `json:"selectedTimeToReadyHours"`
	SelectedTimeToReadyMins      int     `json:"selectedTimeToReadyMins"`
	// Selected WBClub
	SelectedWBClubOrderCount          int     `json:"selectedWBClubOrderCount"`
	SelectedWBClubOrderSum            int     `json:"selectedWBClubOrderSum"`
	SelectedWBClubBuyoutCount         int     `json:"selectedWBClubBuyoutCount"`
	SelectedWBClubBuyoutSum           int     `json:"selectedWBClubBuyoutSum"`
	SelectedWBClubCancelCount         int     `json:"selectedWBClubCancelCount"`
	SelectedWBClubCancelSum           int     `json:"selectedWBClubCancelSum"`
	SelectedWBClubAvgPrice            int     `json:"selectedWBClubAvgPrice"`
	SelectedWBClubBuyoutPercent       float64 `json:"selectedWBClubBuyoutPercent"`
	SelectedWBClubAvgOrderCountPerDay float64 `json:"selectedWBClubAvgOrderCountPerDay"`
	// Selected Conversions
	SelectedConversionAddToCart   float64 `json:"selectedConversionAddToCart"`
	SelectedConversionCartToOrder float64 `json:"selectedConversionCartToOrder"`
	SelectedConversionBuyout      float64 `json:"selectedConversionBuyout"`

	// Past period metrics (nullable)
	PastPeriodStart *string `json:"pastPeriodStart,omitempty"`
	PastPeriodEnd   *string `json:"pastPeriodEnd,omitempty"`
	PastOpenCount            *int     `json:"pastOpenCount,omitempty"`
	PastCartCount            *int     `json:"pastCartCount,omitempty"`
	PastOrderCount           *int     `json:"pastOrderCount,omitempty"`
	PastOrderSum             *int     `json:"pastOrderSum,omitempty"`
	PastBuyoutCount          *int     `json:"pastBuyoutCount,omitempty"`
	PastBuyoutSum            *int     `json:"pastBuyoutSum,omitempty"`
	PastCancelCount          *int     `json:"pastCancelCount,omitempty"`
	PastCancelSum            *int     `json:"pastCancelSum,omitempty"`
	PastAvgPrice             *int     `json:"pastAvgPrice,omitempty"`
	PastAvgOrdersCountPerDay *float64 `json:"pastAvgOrdersCountPerDay,omitempty"`
	PastShareOrderPercent    *float64 `json:"pastShareOrderPercent,omitempty"`
	PastAddToWishlist        *int     `json:"pastAddToWishlist,omitempty"`
	PastLocalizationPercent  *float64 `json:"pastLocalizationPercent,omitempty"`
	PastTimeToReadyDays      *int     `json:"pastTimeToReadyDays,omitempty"`
	PastTimeToReadyHours     *int     `json:"pastTimeToReadyHours,omitempty"`
	PastTimeToReadyMins      *int     `json:"pastTimeToReadyMins,omitempty"`
	// Past WBClub
	PastWBClubOrderCount          *int     `json:"pastWBClubOrderCount,omitempty"`
	PastWBClubOrderSum            *int     `json:"pastWBClubOrderSum,omitempty"`
	PastWBClubBuyoutCount         *int     `json:"pastWBClubBuyoutCount,omitempty"`
	PastWBClubBuyoutSum           *int     `json:"pastWBClubBuyoutSum,omitempty"`
	PastWBClubCancelCount         *int     `json:"pastWBClubCancelCount,omitempty"`
	PastWBClubCancelSum           *int     `json:"pastWBClubCancelSum,omitempty"`
	PastWBClubAvgPrice            *int     `json:"pastWBClubAvgPrice,omitempty"`
	PastWBClubBuyoutPercent       *float64 `json:"pastWBClubBuyoutPercent,omitempty"`
	PastWBClubAvgOrderCountPerDay *float64 `json:"pastWBClubAvgOrderCountPerDay,omitempty"`
	// Past Conversions
	PastConversionAddToCart   *float64 `json:"pastConversionAddToCart,omitempty"`
	PastConversionCartToOrder *float64 `json:"pastConversionCartToOrder,omitempty"`
	PastConversionBuyout      *float64 `json:"pastConversionBuyout,omitempty"`

	// Comparison metrics (nullable)
	ComparisonOpenCountDynamic            *int     `json:"comparisonOpenCountDynamic,omitempty"`
	ComparisonCartCountDynamic            *int     `json:"comparisonCartCountDynamic,omitempty"`
	ComparisonOrderCountDynamic           *int     `json:"comparisonOrderCountDynamic,omitempty"`
	ComparisonOrderSumDynamic             *int     `json:"comparisonOrderSumDynamic,omitempty"`
	ComparisonBuyoutCountDynamic          *int     `json:"comparisonBuyoutCountDynamic,omitempty"`
	ComparisonBuyoutSumDynamic            *int     `json:"comparisonBuyoutSumDynamic,omitempty"`
	ComparisonCancelCountDynamic          *int     `json:"comparisonCancelCountDynamic,omitempty"`
	ComparisonCancelSumDynamic            *int     `json:"comparisonCancelSumDynamic,omitempty"`
	ComparisonAvgOrdersCountPerDayDynamic *float64 `json:"comparisonAvgOrdersCountPerDayDynamic,omitempty"`
	ComparisonAvgPriceDynamic             *int     `json:"comparisonAvgPriceDynamic,omitempty"`
	ComparisonShareOrderPercentDynamic    *float64 `json:"comparisonShareOrderPercentDynamic,omitempty"`
	ComparisonAddToWishlistDynamic        *int     `json:"comparisonAddToWishlistDynamic,omitempty"`
	ComparisonLocalizationPercentDynamic  *float64 `json:"comparisonLocalizationPercentDynamic,omitempty"`
	ComparisonTimeToReadyDays             *int     `json:"comparisonTimeToReadyDays,omitempty"`
	ComparisonTimeToReadyHours            *int     `json:"comparisonTimeToReadyHours,omitempty"`
	ComparisonTimeToReadyMins             *int     `json:"comparisonTimeToReadyMins,omitempty"`
	// Comparison WBClub
	ComparisonWBClubOrderCount          *int     `json:"comparisonWBClubOrderCount,omitempty"`
	ComparisonWBClubOrderSum            *int     `json:"comparisonWBClubOrderSum,omitempty"`
	ComparisonWBClubBuyoutCount         *int     `json:"comparisonWBClubBuyoutCount,omitempty"`
	ComparisonWBClubBuyoutSum           *int     `json:"comparisonWBClubBuyoutSum,omitempty"`
	ComparisonWBClubCancelCount         *int     `json:"comparisonWBClubCancelCount,omitempty"`
	ComparisonWBClubCancelSum           *int     `json:"comparisonWBClubCancelSum,omitempty"`
	ComparisonWBClubAvgPrice            *int     `json:"comparisonWBClubAvgPrice,omitempty"`
	ComparisonWBClubBuyoutPercent       *float64 `json:"comparisonWBClubBuyoutPercent,omitempty"`
	ComparisonWBClubAvgOrderCountPerDay *float64 `json:"comparisonWBClubAvgOrderCountPerDay,omitempty"`
	// Comparison Conversions
	ComparisonConversionAddToCart   *float64 `json:"comparisonConversionAddToCart,omitempty"`
	ComparisonConversionCartToOrder *float64 `json:"comparisonConversionCartToOrder,omitempty"`
	ComparisonConversionBuyout      *float64 `json:"comparisonConversionBuyout,omitempty"`

	// Metadata
	Currency string `json:"currency"`
}

// ============================================================================
// Stock Warehouse Types (for WB Analytics API — new endpoint)
// ============================================================================

// StockWarehouseItem represents a single warehouse stock record from the new API.
// POST /api/analytics/v1/stocks-report/wb-warehouses
type StockWarehouseItem struct {
	NmID            int64  `json:"nmId"`
	ChrtID          int64  `json:"chrtId"`
	WarehouseID     int64  `json:"warehouseId"`
	WarehouseName   string `json:"warehouseName"`
	RegionName      string `json:"regionName"`
	Quantity        int64  `json:"quantity"`
	InWayToClient   int64  `json:"inWayToClient"`
	InWayFromClient int64  `json:"inWayFromClient"`
}

// StocksWarehouseAPIResponse wraps the API response for stocks warehouse endpoint.
// POST /api/analytics/v1/stocks-report/wb-warehouses
type StocksWarehouseAPIResponse struct {
	Data struct {
		Items []StockWarehouseItem `json:"items"`
	} `json:"data"`
	Error     bool   `json:"error"`
	ErrorText string `json:"errorText"`
}

// ============================================================================
// Stock History CSV Report Types (Analytics API — async reports)
// ============================================================================

// StockHistoryReportRequest creates a task to generate stock history CSV report.
// POST /api/v2/nm-report/downloads
//
// Report types:
//   - STOCK_HISTORY_REPORT_CSV: metrics with monthly columns
//   - STOCK_HISTORY_DAILY_CSV: daily stock levels
type StockHistoryReportRequest struct {
	ID             string                    `json:"id"` // UUID generated by seller
	ReportType     string                    `json:"reportType"`
	UserReportName string                    `json:"userReportName,omitempty"`
	Params         StockHistoryReportParams  `json:"params"`
}

// StockHistoryReportParams contains report generation parameters.
type StockHistoryReportParams struct {
	CurrentPeriod      StockHistoryPeriod `json:"currentPeriod"`
	StockType          string             `json:"stockType"` // "", "wb", or "mp"
	SkipDeletedNm      bool               `json:"skipDeletedNm"`
	AvailabilityFilters []string          `json:"availabilityFilters,omitempty"` // for metrics only
	OrderBy            *StockHistoryOrderBy `json:"orderBy,omitempty"`           // for metrics only
	NmIds              []int64            `json:"nmIds,omitempty"`
	SubjectIds         []int              `json:"subjectIds,omitempty"`
	BrandNames         []string           `json:"brandNames,omitempty"`
	TagIds             []int64            `json:"tagIds,omitempty"`
}

// StockHistoryPeriod defines date range for the report.
// Start date cannot be earlier than 3 months from current date.
type StockHistoryPeriod struct {
	Start string `json:"start"` // YYYY-MM-DD
	End   string `json:"end"`   // YYYY-MM-DD
}

// StockHistoryOrderBy defines sorting for metrics report.
type StockHistoryOrderBy struct {
	Field string `json:"field"` // avgOrders, stockCount, etc.
	Mode  string `json:"mode"`  // asc, desc
}

// StockHistoryReportCreateResponse is the response when creating a report.
type StockHistoryReportCreateResponse struct {
	Data string `json:"data"` // "Началось формирование файла/отчета"
}

// Stock history report statuses.
const (
	StockHistoryStatusWaiting    = "WAITING"
	StockHistoryStatusProcessing = "PROCESSING"
	StockHistoryStatusSuccess    = "SUCCESS"
	StockHistoryStatusRetry      = "RETRY"
	StockHistoryStatusFailed     = "FAILED"
)

// StockHistoryReportItem represents a report in the list.
type StockHistoryReportItem struct {
	ID        string `json:"id"`         // UUID
	Status    string `json:"status"`     // WAITING, PROCESSING, SUCCESS, RETRY, FAILED
	Name      string `json:"name"`
	Size      int64  `json:"size"`       // bytes
	StartDate string `json:"startDate"`  // YYYY-MM-DD
	EndDate   string `json:"endDate"`    // YYYY-MM-DD
	CreatedAt string `json:"createdAt"`  // "2024-06-26 20:05:32"
}

// StockHistoryReportListResponse is the response for listing reports.
type StockHistoryReportListResponse struct {
	Data []StockHistoryReportItem `json:"data"`
}

// ============================================================================
// Region Sale Types (Seller Analytics API — /api/v1/analytics/region-sale)
// ============================================================================

// RegionSaleItem represents a single row from the region sale report.
// GET /api/v1/analytics/region-sale?dateFrom=...&dateTo=...
//
// Grain: one row per (nm_id, city_name, region_name, country_name) for the requested period.
type RegionSaleItem struct {
	CityName                 string  `json:"cityName"`
	CountryName              string  `json:"countryName"`
	FoName                   string  `json:"foName"`
	NmID                     int     `json:"nmID"`
	RegionName               string  `json:"regionName"`
	Sa                       string  `json:"sa"`
	SaleInvoiceCostPrice     float64 `json:"saleInvoiceCostPrice"`
	SaleInvoiceCostPricePerc float64 `json:"saleInvoiceCostPricePerc"`
	SaleItemInvoiceQty       int     `json:"saleItemInvoiceQty"`
}

// RegionSaleResponse wraps the API response for the region sale endpoint.
type RegionSaleResponse struct {
	Report []RegionSaleItem `json:"report"`
}