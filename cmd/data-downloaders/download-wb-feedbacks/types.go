// Package main provides types for WB Feedbacks API responses.
//
// Response wrappers for the Feedbacks API endpoints.
// Full data types (FeedbackFull, QuestionFull) live in pkg/wb/feedbacks_full.go
// and are shared with pkg/storage/sqlite for direct INSERT.
package main

import (
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ============================================================================
// API Response Wrappers
// ============================================================================

// FeedbacksResponse — ответ API /api/v1/feedbacks.
type FeedbacksResponse struct {
	Data struct {
		CountUnanswered int              `json:"countUnanswered"`
		CountArchive    int              `json:"countArchive"`
		Feedbacks       []wb.FeedbackFull `json:"feedbacks"`
	} `json:"data"`
	Error           bool     `json:"error"`
	ErrorText       string   `json:"errorText"`
	AdditionalErrors []string `json:"additionalErrors"`
}

// QuestionsResponse — ответ API /api/v1/questions.
type QuestionsResponse struct {
	Data struct {
		CountUnanswered int               `json:"countUnanswered"`
		CountArchive    int               `json:"countArchive"`
		Questions       []wb.QuestionFull `json:"questions"`
	} `json:"data"`
	Error           bool     `json:"error"`
	ErrorText       string   `json:"errorText"`
	AdditionalErrors []string `json:"additionalErrors"`
}
