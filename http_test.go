package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func scrape(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		r.Header.Set("Authorization", token)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestAuthenticationBeforeCollection(t *testing.T) {
	var calls atomic.Int64
	handler := newHandler(testConfig(), func(context.Context) (snapshot, error) { calls.Add(1); return snapshot{}, nil })
	for _, path := range []string{"/metrics", "/readyz", "/metrics?token=" + testToken} {
		for _, token := range []string{"", "Bearer wrong", "Basic " + testToken, "Bearer " + testToken + "extra"} {
			w := scrape(handler, path, token)
			if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != "Bearer" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unexpected rejection: %d", w.Code)
			}
		}
	}
	r := httptest.NewRequest("GET", "/metrics", nil)
	r.Header.Add("Authorization", "Bearer "+testToken)
	r.Header.Add("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 || calls.Load() != 0 {
		t.Fatal("unauthorized request reached the database")
	}
	w = scrape(handler, "/metrics", "Bearer "+testToken)
	if w.Code != 200 || calls.Load() != 1 || !strings.Contains(w.Body.String(), "endurain_up 1") {
		t.Fatal("authenticated scrape failed")
	}
}

func TestEndpointsWithoutBuiltinAuthentication(t *testing.T) {
	c := testConfig()
	c.token = ""
	var calls int
	handler := newHandler(c, func(context.Context) (snapshot, error) {
		calls++
		return snapshot{users: []int64{1}}, nil
	})
	for _, path := range []string{"/metrics", "/readyz"} {
		for _, header := range []string{"", "Bearer wrong", "Basic external-proxy-auth"} {
			w := scrape(handler, path, header)
			if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("WWW-Authenticate") != "" {
				t.Fatalf("unexpected response without builtin authentication: %d", w.Code)
			}
			if path == "/metrics" && !strings.Contains(w.Body.String(), `endurain_user_info{user_id="1"} 1`) {
				t.Fatal("metrics missing without builtin authentication")
			}
		}
	}
	if calls != 6 {
		t.Fatal("endpoints did not collect without builtin authentication")
	}
}

func TestHealthAndMethods(t *testing.T) {
	handler := newHandler(testConfig(), func(context.Context) (snapshot, error) { return snapshot{}, errors.New("private-database-password") })
	if w := scrape(handler, "/healthz", ""); w.Code != 200 || w.Body.String() != "ok\n" {
		t.Fatal("liveness depends on database")
	}
	if w := scrape(handler, "/readyz", "Bearer "+testToken); w.Code != 503 || strings.Contains(w.Body.String(), "private-database-password") {
		t.Fatal("unsafe readiness failure")
	}
	if w := scrape(handler, "/missing", "Bearer "+testToken); w.Code != 404 {
		t.Fatal("unknown route accepted")
	}
	r := httptest.NewRequest("POST", "/metrics", nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 405 {
		t.Fatal("POST metrics accepted")
	}
}

func TestFailedCollectionOmitsBusinessMetrics(t *testing.T) {
	for _, failure := range []error{errors.New("sensitive connection details"), errSeriesBudget} {
		handler := newHandler(testConfig(), func(context.Context) (snapshot, error) { return snapshot{users: []int64{1}}, failure })
		w := scrape(handler, "/metrics", "Bearer "+testToken)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "endurain_up 0") || strings.Contains(w.Body.String(), "endurain_user_info") || strings.Contains(w.Body.String(), "sensitive") {
			t.Fatal("invalid error exposition")
		}
	}
}

func TestRequestTimeoutAndCancellation(t *testing.T) {
	c := testConfig()
	c.timeout = 5 * time.Millisecond
	handler := newHandler(c, func(ctx context.Context) (snapshot, error) { <-ctx.Done(); return snapshot{}, ctx.Err() })
	start := time.Now()
	w := scrape(handler, "/metrics", "Bearer "+testToken)
	if time.Since(start) > time.Second || !strings.Contains(w.Body.String(), "endurain_up 0") {
		t.Fatal("collection deadline not enforced")
	}
	r := httptest.NewRequest("GET", "/metrics", nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r.WithContext(ctx))
	if !strings.Contains(w.Body.String(), "endurain_up 0") {
		t.Fatal("request cancellation ignored")
	}
}

func TestConsecutiveScrapesDoNotRetainDeletedUsers(t *testing.T) {
	var calls int
	handler := newHandler(testConfig(), func(context.Context) (snapshot, error) {
		calls++
		if calls == 1 {
			return snapshot{users: []int64{1}}, nil
		}
		return snapshot{}, nil
	})
	first := scrape(handler, "/metrics", "Bearer "+testToken)
	second := scrape(handler, "/metrics", "Bearer "+testToken)
	if !strings.Contains(first.Body.String(), `endurain_user_info{user_id="1"} 1`) || strings.Contains(second.Body.String(), "endurain_user_info") {
		t.Fatal("deleted user retained")
	}
}

func TestConcurrentScrapes(t *testing.T) {
	var calls atomic.Int64
	handler := newHandler(testConfig(), func(context.Context) (snapshot, error) { calls.Add(1); return snapshot{users: []int64{1, 2}}, nil })
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			w := scrape(handler, "/metrics", "Bearer "+testToken)
			if w.Code != 200 || !strings.Contains(w.Body.String(), `endurain_user_info{user_id="2"} 1`) {
				t.Error(fmt.Sprintf("concurrent scrape returned %d", w.Code))
			}
		})
	}
	wg.Wait()
	if calls.Load() != 20 {
		t.Fatal("scrapes shared unexpected state")
	}
}
