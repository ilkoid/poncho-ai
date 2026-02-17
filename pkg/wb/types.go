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
	RRDT               string  `json:"rr_dt,omitempty"`                // Дата отчета
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
