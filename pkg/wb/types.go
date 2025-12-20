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

