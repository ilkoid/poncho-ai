// Package utils предоставляет простой файловый логгер для TUI приложений.
//
// Логгер создаёт .log файл в текущей директории с timestamp в имени.
// Thread-safe через sync.Mutex.
package utils

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	logFile    *os.File
	logMutex   sync.Mutex
	initialized bool
)

// InitLogger создает/открывает .log файл в текущей директории.
//
// Имя файла: poncho-YYYY-MM-DD-HH-MM.log (например, poncho-2025-12-27-15-30.log)
// Файл создаётся в той же директории, откуда запущена утилита.
func InitLogger() error {
	logMutex.Lock()
	defer logMutex.Unlock()

	if initialized {
		return nil
	}

	// Имя файла: poncho-2025-12-27-15-30.log
	timestamp := time.Now().Format("2006-01-02-15-04")
	filename := fmt.Sprintf("poncho-%s.log", timestamp)

	var err error
	logFile, err = os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	initialized = true
	// Пишем напрямую без Info чтобы избежать deadlock (мьютекс уже захвачен)
	timestampNow := time.Now().Format("2006-01-02 15:04:05")
	logFile.WriteString(fmt.Sprintf("[%s] INFO: Logger initialized file=%s\n", timestampNow, filename))
	logFile.Sync() // Сбрасываем буфер на диск
	return nil
}

// Info - информационное сообщение.
func Info(msg string, keyvals ...any) {
	log("INFO", msg, keyvals...)
}

// Error - сообщение об ошибке.
func Error(msg string, keyvals ...any) {
	log("ERROR", msg, keyvals...)
}

// Debug - отладочное сообщение.
func Debug(msg string, keyvals ...any) {
	log("DEBUG", msg, keyvals...)
}

// log - внутренняя функция записи в лог.
//
// Формат: [YYYY-MM-DD HH:MM:SS] LEVEL: message key1=value1 key2=value2
func log(level, msg string, keyvals ...any) {
	logMutex.Lock()
	defer logMutex.Unlock()

	if logFile == nil {
		return
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	line := fmt.Sprintf("[%s] %s: %s", timestamp, level, msg)

	for i := 0; i < len(keyvals); i += 2 {
		if i+1 < len(keyvals) {
			line += fmt.Sprintf(" %v=%v", keyvals[i], keyvals[i+1])
		}
	}

	line += "\n"
	logFile.WriteString(line)
	logFile.Sync() // Сбрасываем буфер на диск немедленно
}

// Close закрывает лог-файл.
//
// Вызывается через defer в main().
func Close() {
	logMutex.Lock()
	defer logMutex.Unlock()

	if logFile != nil {
		logFile.Close()
		logFile = nil
	}
}
