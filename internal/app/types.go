// Общие типы для пакета app
package app

// CommandResultMsg - сообщение, которое возвращает worker после выполнения команды
type CommandResultMsg struct {
	Output string
	Err    error
}
