package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/healthz" {
				t.Error("wrong health endpoint")
			}
			w.WriteHeader(status)
		}))
		t.Setenv("LISTEN_ADDRESS", strings.TrimPrefix(server.URL, "http://"))
		if err := healthcheck(os.Getenv("LISTEN_ADDRESS"), ""); (err == nil) != (status == http.StatusOK) {
			t.Fatalf("status %d: unexpected health result", status)
		}
		server.Close()
		if err := healthcheck(os.Getenv("LISTEN_ADDRESS"), ""); err == nil {
			t.Fatal("closed endpoint reported healthy")
		}
	}
	t.Setenv("LISTEN_ADDRESS", "invalid")
	if err := healthcheck(os.Getenv("LISTEN_ADDRESS"), ""); err == nil {
		t.Fatal("invalid address accepted")
	}
}

func TestServerGracefulShutdownAndBindFailure(t *testing.T) {
	c := testConfig()
	c.databaseURL = "postgres://user:development-password@127.0.0.1:1/database?sslmode=disable"
	c.listen = "127.0.0.1:0"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := serve(ctx, c); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	c.listen = listener.Addr().String()
	if err := serve(context.Background(), c); err == nil {
		t.Fatal("occupied address accepted")
	}
	c.databaseURL = "invalid DSN with private credentials"
	if err := serve(context.Background(), c); err == nil || strings.Contains(err.Error(), "private credentials") {
		t.Fatal("unsafe invalid configuration error")
	}
}
