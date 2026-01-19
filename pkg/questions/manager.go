// Package questions предоставляет QuestionManager для координации вопросов.
//
// QuestionManager является центральным хабом для управления интерактивными
// вопросами от LLM к пользователю через TUI.
package questions

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// QuestionManager управляет интерактивными вопросами от LLM к пользователю.
//
// Потокобезопасен: использует sync.RWMutex для защиты внутреннего состояния.
// Работает как координатор между Tool (задает вопрос) и TUI (показывает вопрос).
//
// Паттерн:
// 1. Tool создает вопрос через CreateQuestion()
// 2. Tool блокируется на WaitForAnswer()
// 3. TUI получает вопрос через GetQuestion()
// 4. Пользователь выбирает вариант
// 5. TUI отправляет ответ через SubmitAnswer()
// 6. Tool разблокируется и получает результат
type QuestionManager struct {
	mu          sync.RWMutex
	pending     map[string]*PendingQuestion // ID → вопрос
	responseCh  map[string]chan QuestionResult // ID → канал для ответа
	maxOptions  int                           // Максимум вариантов
	timeout     time.Duration                 // Таймаут ожидания
}

// NewQuestionManager создает новый QuestionManager.
func NewQuestionManager(maxOptions int, timeout time.Duration) *QuestionManager {
	return &QuestionManager{
		pending:    make(map[string]*PendingQuestion),
		responseCh: make(map[string]chan QuestionResult),
		maxOptions: maxOptions,
		timeout:    timeout,
	}
}

// CreateQuestion создает новый вопрос и возвращает его ID.
//
// После вызова нужно использовать WaitForAnswer() для получения ответа.
//
// Возвращает ошибку если:
// - question пустой
// - options пустой или больше maxOptions
// - label в какой-то опции пустой
func (qm *QuestionManager) CreateQuestion(question string, options []QuestionOption) (string, error) {
	pq := &PendingQuestion{
		ID:        generateQuestionID(),
		Question:  question,
		Options:   options,
		CreatedAt: time.Now(),
	}

	// Валидация
	if err := pq.Validate(qm.maxOptions); err != nil {
		return "", fmt.Errorf("invalid question: %w", err)
	}

	// Регистрация
	qm.mu.Lock()
	defer qm.mu.Unlock()

	qm.pending[pq.ID] = pq
	qm.responseCh[pq.ID] = make(chan QuestionResult, 1)

	return pq.ID, nil
}

// WaitForAnswer блокируется до получения ответа от пользователя.
//
// Возвращает QuestionResult с одним из статусов:
// - "answered" - пользователь выбрал вариант
// - "cancelled" - пользователь отменил (Ctrl+C)
// - "timeout" - истекло время ожидания
//
// Context может быть использован для отмены ожидания (Rule 11 compliance).
func (qm *QuestionManager) WaitForAnswer(ctx context.Context, questionID string) (QuestionResult, error) {
	qm.mu.RLock()
	ch, ok := qm.responseCh[questionID]
	qm.mu.RUnlock()

	if !ok {
		return QuestionResult{}, fmt.Errorf("question not found: %s", questionID)
	}

	// Ждем ответа или таймаута
	select {
	case result := <-ch:
		// Очистка
		qm.mu.Lock()
		delete(qm.pending, questionID)
		delete(qm.responseCh, questionID)
		qm.mu.Unlock()
		return result, nil

	case <-time.After(qm.timeout):
		// Таймаут
		qm.mu.Lock()
		delete(qm.pending, questionID)
		delete(qm.responseCh, questionID)
		qm.mu.Unlock()
		return NewTimeoutResult(qm.timeout), nil

	case <-ctx.Done():
		// Context cancellation
		return NewCancelledResult("context_cancelled"), ctx.Err()
	}
}

// GetQuestion возвращает ожидающий вопрос по ID.
//
// Используется TUI для получения вопроса и отображения его пользователю.
// Возвращает (nil, false) если вопрос не найден.
func (qm *QuestionManager) GetQuestion(id string) (*PendingQuestion, bool) {
	qm.mu.RLock()
	defer qm.mu.RUnlock()
	pq, ok := qm.pending[id]
	return pq, ok
}

// SubmitAnswer отправляет ответ пользователя на вопрос.
//
// Вызывается из TUI когда пользователь сделал выбор.
// Разблокирует WaitForAnswer() в Tool.
func (qm *QuestionManager) SubmitAnswer(questionID string, answer QuestionAnswer) error {
	qm.mu.RLock()
	ch, ok := qm.responseCh[questionID]
	pq, hasQuestion := qm.pending[questionID]
	qm.mu.RUnlock()

	if !ok || !hasQuestion {
		return fmt.Errorf("question not found: %s", questionID)
	}

	// Валидация индекса
	if !pq.IsValidIndex(answer.Index) {
		return fmt.Errorf("invalid index: %d (valid: 0-%d)", answer.Index, len(pq.Options)-1)
	}

	// Отправляем ответ (неблокирующая отправка в buffered channel)
	select {
	case ch <- NewAnsweredResult(answer):
		return nil
	default:
		return fmt.Errorf("response channel closed or full")
	}
}

// Cancel отменяет вопрос без ответа.
//
// Используется при Ctrl+C или других прерываниях.
func (qm *QuestionManager) Cancel(questionID string, reason string) {
	qm.mu.RLock()
	ch, ok := qm.responseCh[questionID]
	qm.mu.RUnlock()

	if ok {
		select {
		case ch <- NewCancelledResult(reason):
		default:
		}
	}
}

// HasPendingQuestions проверяет есть ли ожидающие вопросы.
func (qm *QuestionManager) HasPendingQuestions() bool {
	qm.mu.RLock()
	defer qm.mu.RUnlock()
	return len(qm.pending) > 0
}

// GetPendingCount возвращает количество ожидающих вопросов.
func (qm *QuestionManager) GetPendingCount() int {
	qm.mu.RLock()
	defer qm.mu.RUnlock()
	return len(qm.pending)
}

// GetFirstPendingID возвращает ID первого ожидающего вопроса.
//
// Используется TUI для получения вопроса через GetQuestion().
// Возвращает пустую строку если нет ожидающих вопросов.
func (qm *QuestionManager) GetFirstPendingID() string {
	qm.mu.RLock()
	defer qm.mu.RUnlock()

	for id := range qm.pending {
		return id // Возвращаем первый (map iteration order)
	}
	return ""
}

// GetAllPendingIDs возвращает ID всех ожидающих вопросов.
//
// Возвращает слайс ID (может быть пустым).
func (qm *QuestionManager) GetAllPendingIDs() []string {
	qm.mu.RLock()
	defer qm.mu.RUnlock()

	ids := make([]string, 0, len(qm.pending))
	for id := range qm.pending {
		ids = append(ids, id)
	}
	return ids
}

// generateQuestionID генерирует уникальный ID для вопроса.
func generateQuestionID() string {
	return fmt.Sprintf("q_%d", time.Now().UnixNano())
}
