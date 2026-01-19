// Package tui предоставляет question handling для InterruptionModel (ask_user_question tool).
package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ilkoid/poncho-ai/pkg/questions"
)

// checkForPendingQuestions проверяет есть ли вопросы от ask_user_question tool.
func (m *InterruptionModel) checkForPendingQuestions() bool {
	if m.questionManager == nil {
		return false
	}

	qm, ok := m.questionManager.(*questions.QuestionManager)
	if !ok || !qm.HasPendingQuestions() {
		return false
	}

	id := qm.GetFirstPendingID()
	pq, ok := qm.GetQuestion(id)
	if !ok {
		return false
	}

	m.questionMode = true
	m.currentQuestionID = id
	m.renderQuestionFromData(pq.Question, pq.Options)
	return true
}

// renderQuestionFromData рендерит вопрос в TUI.
func (m *InterruptionModel) renderQuestionFromData(question string, options interface{}) {
	opts := options.([]questions.QuestionOption)
	optLen := len(opts)

	var lines []string
	lines = append(lines, "")
	// Используем "лайтовый" голубовато-серый (152) для мягкого акцента
	lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("152")).Render("🤔 Agent Question:"))
	lines = append(lines, "")
	lines = append(lines, question)
	lines = append(lines, "")

	for i, opt := range opts {
		text := opt.Label
		if opt.Description != "" {
			text = opt.Label + " — " + opt.Description
		}
		line := fmt.Sprintf("  [%d] %s", i+1, text)
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Render(line))
	}

	lines = append(lines, "")
	lines = append(lines, SystemStyle("  Нажми 1-"+fmt.Sprint(optLen)+" для выбора"))

	for _, line := range lines {
		m.appendLog(line)
	}
}

// handleQuestionKey обрабатывает нажатия клавиш в question mode.
func (m *InterruptionModel) handleQuestionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// ПРОВЕРКА: Отмена вопроса через Esc или Ctrl+C
	// ДОЛЖНА быть первой проверкой, чтобы пользователь мог выйти из question mode
	if key.Matches(msg, m.keys.Quit) {
		m.exitQuestionMode()
		m.appendLog(SystemStyle("❌ Question cancelled"))
		return m, nil
	}

	// Handle all keys in question mode - prevent any other processing
	switch msg.String() {
	case "1", "2", "3", "4", "5":
		index := int(msg.String()[0] - '1')

		qm, ok := m.questionManager.(*questions.QuestionManager)
		if !ok {
			m.appendLog(ErrorStyle("❌ QuestionManager not available"))
			m.exitQuestionMode()
			return m, nil
		}

		pq, ok := qm.GetQuestion(m.currentQuestionID)
		if !ok || !pq.IsValidIndex(index) {
			m.appendLog(ErrorStyle(fmt.Sprintf("❌ Неверный выбор: %s", msg.String())))
			return m, nil
		}

		opt := pq.Options[index]
		answer := questions.QuestionAnswer{
			Index:       index,
			Label:       opt.Label,
			Description: opt.Description,
			Timestamp:   time.Now(),
		}

		err := qm.SubmitAnswer(m.currentQuestionID, answer)
		if err != nil {
			m.appendLog(ErrorStyle(fmt.Sprintf("❌ Ошибка: %v", err)))
		} else {
			m.appendLog(SystemStyle(fmt.Sprintf("✓ Выбран: %s", opt.Label)))
		}

		m.exitQuestionMode()
		return m, nil

	default:
		// Ignore ALL other keys in question mode
		// Debug log to help track what keys are being pressed
		m.debugLogIfEnabled("handleQuestionKey: ignoring key '%s' in question mode", msg.String())
		return m, nil
	}
}

// exitQuestionMode выходит из режима вопросов.
func (m *InterruptionModel) exitQuestionMode() {
	m.questionMode = false
	m.currentQuestionID = ""
}

// SetQuestionManager устанавливает менеджер вопросов.
func (m *InterruptionModel) SetQuestionManager(qm interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.questionManager = qm
}
