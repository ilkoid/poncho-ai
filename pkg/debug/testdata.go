// Package debug предоставляет тестовые данные для unit-тестирования.
//
// Функции GenerateCategoriesJSON и GenerateSubjectsJSON используются
// в CLI утилитах (cmd/debug-test) для демонстрации работы debug logging.
package debug

// GenerateCategoriesJSON возвращает тестовые данные категорий WB.
//
// Используется в cmd/debug-test для симуляции работы инструмента
// get_wb_parent_categories без реального API вызова.
func GenerateCategoriesJSON() string {
	return `[{"id":1541,"name":"Женщинам","isVisible":true},{"id":1535,"name":"Мужчинам","isVisible":true},{"id":1537,"name":"Детям","isVisible":true}]`
}

// GenerateSubjectsJSON возвращает тестовые данные подкатегорий WB.
//
// Используется в cmd/debug-test для симуляции работы инструмента
// get_wb_subjects без реального API вызова.
func GenerateSubjectsJSON() string {
	return `[{"id":685,"name":"Платья","subjectID":1541,"parentID":1541},{"id":1256,"name":"Юбки","subjectID":1541,"parentID":1541},{"id":1271,"name":"Блузки и рубашки","subjectID":1541,"parentID":1541}]`
}
