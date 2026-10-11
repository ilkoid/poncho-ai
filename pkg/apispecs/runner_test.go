package apispecs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPFetcher_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte("openapi: 3.0.1\n"))
	}))
	defer srv.Close()

	f := NewHTTPFetcher(HTTPConfig{TimeoutSeconds: 5, Retries: 2})
	body, err := f.Get(context.Background(), srv.URL+"/02-items.yaml")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(body) != "openapi: 3.0.1\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestHTTPFetcher_ChallengeNotRetried(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>Подозрительная активность</html>"))
	}))
	defer srv.Close()

	f := NewHTTPFetcher(HTTPConfig{TimeoutSeconds: 5, Retries: 3})
	_, err := f.Get(context.Background(), srv.URL+"/x.yaml")
	if !errors.Is(err, ErrChallenge) {
		t.Fatalf("want ErrChallenge, got %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("челлендж отретраился %d раз (хотим 0 повторов)", got-1)
	}
}

func TestHTTPFetcher_RetriesTransient(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"openapi":"3.0.1"}`))
	}))
	defer srv.Close()

	f := NewHTTPFetcher(HTTPConfig{TimeoutSeconds: 5, Retries: 3})
	f.BackoffBase = 5 * time.Millisecond
	// Проверяем сам факт успеха после 5xx.
	body, err := f.Get(context.Background(), srv.URL+"/x.json")
	if err != nil {
		t.Fatalf("Get after retries: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("empty body")
	}
	if got := hits.Load(); got < 3 {
		t.Fatalf("ожидали ≥3 попыток, было %d", got)
	}
}

func TestHTTPFetcher_404Permanent(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	f := NewHTTPFetcher(HTTPConfig{TimeoutSeconds: 5, Retries: 3})
	_, err := f.Get(context.Background(), srv.URL+"/missing.yaml")
	if err == nil || errors.Is(err, ErrChallenge) {
		t.Fatalf("want plain permanent error, got %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("404 отретраился %d раз (хотим 0)", got-1)
	}
}

func TestRunner_HTTPOnlyWithChallenge(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/good.yaml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("openapi: 3.0.1\ninfo: {title: x}\n"))
	})
	mux.HandleFunc("/gated.yaml", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>__wbaas challenge</html>"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	root := t.TempDir()
	factory := func() (FileGetter, Capturer, func(), error) {
		return nil, nil, nil, errors.New("no browser in unit test")
	}

	r := &Runner{
		HTTP: &HTTPFetcher{
			Client:      &http.Client{Timeout: 5 * time.Second},
			UserAgent:   DefaultUserAgent,
			MaxAttempts: 2,
		},
		CDP:      factory,
		RepoRoot: root,
		HTTPOnly: true,
	}
	report, err := r.Run(context.Background(), []Target{{
		Name: "test",
		Files: []FileSpec{
			{URL: srv.URL + "/good.yaml", Path: "docs/x/good.yaml"},
			{URL: srv.URL + "/gated.yaml", Path: "docs/x/gated.yaml"},
		},
	}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if report.Updated() != 1 || report.Failed() != 0 {
		t.Fatalf("want 1 updated + 0 failed, got %+v", report.Results)
	}
	if report.Results[1].Status != StatusSkipped {
		t.Fatalf("gated.yaml в --http-only должен быть skipped, получен %s", report.Results[1].Status)
	}
	if _, err := os.Stat(filepath.Join(root, "docs/x/good.yaml")); err != nil {
		t.Fatalf("good.yaml не записан: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("gated.yaml запрошен %d раз (ретрай против WAF недопустим)", calls.Load())
	}
}

func TestRunner_UnchangedDetection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("stable: true\n"))
	}))
	defer srv.Close()

	root := t.TempDir()
	r := &Runner{
		HTTP: NewHTTPFetcher(HTTPConfig{TimeoutSeconds: 5, Retries: 1, PauseMs: 0}),
		CDP: func() (FileGetter, Capturer, func(), error) {
			return nil, nil, nil, errors.New("no browser")
		},
		RepoRoot: root,
	}
	targets := []Target{{
		Name:   "test",
		Files:  []FileSpec{{URL: srv.URL + "/s.yaml", Path: "docs/s.yaml"}},
	}}

	first, err := r.Run(context.Background(), targets)
	if err != nil || first.Updated() != 1 {
		t.Fatalf("first run: updated=%d err=%v", first.Updated(), err)
	}
	second, err := r.Run(context.Background(), targets)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Results[0].Status != StatusUnchanged {
		t.Fatalf("второй прогон должен быть unchanged, получен %s", second.Results[0].Status)
	}
}
