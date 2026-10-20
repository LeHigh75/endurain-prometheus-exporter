package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func withOptionalAuth(token string, next http.Handler) http.Handler {
	expected := sha256.Sum256([]byte("Bearer " + token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if token != "" && (len(r.Header.Values("Authorization")) != 1 || subtle.ConstantTimeCompare(actual[:], expected[:]) != 1) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func newHandler(c config, load func(context.Context) (snapshot, error)) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", withOptionalAuth(c.token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), c.timeout)
		defer cancel()
		registry := prometheus.NewRegistry()
		registry.MustRegister(collector{ctx: ctx, load: load, maxSeries: c.maxSeries})
		promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(w, r)
	})))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("GET /readyz", withOptionalAuth(c.token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), c.timeout)
		defer cancel()
		s, err := load(ctx)
		if err == nil {
			_, err = businessMetrics(s, c.maxSeries)
		}
		if err != nil {
			http.Error(w, "Not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ready\n"))
	})))
	return mux
}
