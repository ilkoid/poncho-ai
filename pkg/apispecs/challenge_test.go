package apispecs

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestLooksLikeChallenge(t *testing.T) {
	wbQuarantine := `<!doctype html><html><head><script src="/__wbaas/challenges/antibot/__static/v2/browser-check.js"></script>` +
		`<body><h1>Что-то не так...</h1><p>Подозрительная активность. Пожалуйста, подождите.</p></body></html>`

	tests := []struct {
		name        string
		contentType string
		body        string
		want        bool
	}{
		{"валидный yaml", "application/yaml", "openapi: 3.0.1\ninfo:\n  title: Общее\n", false},
		{"валидный json", "application/json", `{"openapi":"3.0.1"}`, false},
		{"html вместо спеки", "text/html", "<html><body>ok</body></html>", true},
		{"карантин WB по телу", "application/octet-stream", wbQuarantine, true},
		{"cloudflare", "text/html", "Just a moment...", true},
		{"пустое тело", "application/json", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LooksLikeChallenge(tt.contentType, []byte(tt.body)); got != tt.want {
				t.Fatalf("LooksLikeChallenge(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestChallengeStatus(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, 498, http.StatusServiceUnavailable} {
		if !challengeStatus(code) {
			t.Errorf("challengeStatus(%d) = false, want true", code)
		}
	}
	for _, code := range []int{http.StatusOK, http.StatusNotFound, http.StatusTooManyRequests} {
		if challengeStatus(code) {
			t.Errorf("challengeStatus(%d) = true, want false", code)
		}
	}
}

func TestErrChallengeIsPermanent(t *testing.T) {
	// Как в fetcher.getOnce: челлендж оборачивается в permanent.
	wrapped := permanent(fmt.Errorf("%w (HTTP 403 https://example.com/x.yaml)", ErrChallenge))
	if !isPermanent(wrapped) {
		t.Fatal("челлендж должен быть permanent (не ретраится)")
	}
	if !errors.Is(wrapped, ErrChallenge) {
		t.Fatal("errors.Is(wrapped, ErrChallenge) потерялся через обёртку")
	}
	if isPermanent(errors.New("network timeout")) {
		t.Fatal("сетевая ошибка должна быть transient (ретраится)")
	}
}
