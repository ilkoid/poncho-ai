// Package tui предоставляет reusable Bubble Tea message types.
package tui

// saveSuccessMsg — сообщение об успешном сохранении.
type saveSuccessMsg struct {
	filename string
}

// saveErrorMsg — сообщение об ошибке сохранения.
type saveErrorMsg struct {
	err error
}
