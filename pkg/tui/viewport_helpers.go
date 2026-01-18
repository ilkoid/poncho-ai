// Package tui предоставляет reusable helpers для работы с Bubble Tea viewport.
//
// viewport_helpers.go содержит функции для умной обработки прокрутки,
// которые сохраняют позицию пользователя при добавлении нового контента.
package tui

import "github.com/charmbracelet/bubbles/viewport"

// shouldGotoBottom проверяет, следует ли скроллить viewport вниз.
//
// Возвращает true если пользователь находится в нижней позиции viewport.
// Сохраняет позицию пользователя если он прокрутил вверх для просмотра истории.
//
// Algorithm:
//   - Проверяем текущую позицию (YOffset) + высоту viewport
//   - Если сумма >= общее количество строк, пользователь в нижней позиции
//   - В этом случае новые сообщения должны вызывать автоскролл вниз
//   - Иначе позиция пользователя сохраняется
//
// Thread-safe: не модифицирует viewport, только читает.
func shouldGotoBottom(vp viewport.Model) bool {
	return vp.YOffset+vp.Height >= vp.TotalLineCount()
}

// appendToViewport добавляет текст в viewport с умной обработкой прокрутки.
//
// Проверяет позицию пользователя ДО изменения контента и скроллит вниз
// только если пользователь был в нижней позиции. Это позволяет просматривать
// историю сообщений без прыжков при поступлении новых сообщений.
//
// Parameters:
//   - vp: Указатель на viewport.Model (изменяется in-place)
//   - newContent: Новый контент для отображения
//
// Thread-safe: модифицирует viewport, требует внешней синхронизации
// если вызывается из нескольких горутин.
//
// Example:
//
//	// В Update() методе Bubble Tea Model:
//	content := strings.Join(m.messages, "\n")
//	tui.AppendToViewport(&m.viewport, content)
func AppendToViewport(vp *viewport.Model, newContent string) {
	wasAtBottom := shouldGotoBottom(*vp)
	vp.SetContent(newContent)
	if wasAtBottom {
		vp.GotoBottom()
	}
}
