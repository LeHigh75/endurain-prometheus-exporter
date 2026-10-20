package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testToken = "development-only-metrics-token-change-me"

func testConfig() config {
	return config{token: testToken, timeout: time.Second, maxSeries: 20000}
}

func cleanEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"DATABASE_URL", "DATABASE_URL_FILE", "METRICS_TOKEN", "METRICS_TOKEN_FILE", "LISTEN_ADDRESS", "SCRAPE_TIMEOUT", "MAX_SERIES"} {
		t.Setenv(name, "")
	}
	t.Setenv("DATABASE_URL", "postgres://user:development-password@localhost/database?sslmode=disable")
	t.Setenv("METRICS_TOKEN", testToken)
}

func TestConfig(t *testing.T) {
	cleanEnvironment(t)
	c, err := loadConfig()
	if err != nil || c.listen != ":9808" || c.timeout != 5*time.Second || c.maxSeries != 20000 {
		t.Fatalf("unexpected defaults or configuration error: %v", err)
	}
	for _, tc := range []struct{ name, value string }{
		{"DATABASE_URL", ""}, {"METRICS_TOKEN", "too-short"},
		{"METRICS_TOKEN", testToken + "\n"}, {"METRICS_TOKEN", testToken + "\x7f"},
		{"SCRAPE_TIMEOUT", "0s"}, {"SCRAPE_TIMEOUT", "-1s"}, {"SCRAPE_TIMEOUT", "2m"}, {"SCRAPE_TIMEOUT", "invalid"},
		{"MAX_SERIES", "0"}, {"MAX_SERIES", "-1"}, {"MAX_SERIES", "1000001"}, {"MAX_SERIES", "invalid"},
	} {
		t.Run(tc.name+"_"+tc.value, func(t *testing.T) {
			t.Setenv(tc.name, tc.value)
			if _, err := loadConfig(); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	t.Setenv("SCRAPE_TIMEOUT", "2s")
	t.Setenv("MAX_SERIES", "100")
	t.Setenv("LISTEN_ADDRESS", "127.0.0.1:9809")
	c, err = loadConfig()
	if err != nil || c.timeout != 2*time.Second || c.maxSeries != 100 || c.listen != "127.0.0.1:9809" {
		t.Fatal("overrides not applied")
	}
}

func TestTokenIsOptional(t *testing.T) {
	cleanEnvironment(t)
	t.Setenv("METRICS_TOKEN", "")
	c, err := loadConfig()
	if err != nil || c.token != "" {
		t.Fatal("configuration without a token should be accepted")
	}
}

func TestFileSecrets(t *testing.T) {
	cleanEnvironment(t)
	for _, name := range []string{"METRICS_TOKEN", "DATABASE_URL"} {
		t.Run(name, func(t *testing.T) {
			value := os.Getenv(name)
			path := filepath.Join(t.TempDir(), "secret")
			if err := os.WriteFile(path, []byte(value+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(name+"_FILE", path)
			if _, err := loadConfig(); err == nil {
				t.Fatal("ambiguous secret sources accepted")
			}
			t.Setenv(name, "")
			if _, err := loadConfig(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadConfig(); err == nil {
				t.Fatal("empty secret accepted")
			}
			t.Setenv(name+"_FILE", path+"-missing")
			if _, err := loadConfig(); err == nil {
				t.Fatal("missing secret accepted")
			}
		})
	}
}

func TestInvalidDatabaseURLDoesNotLeakSecret(t *testing.T) {
	c := testConfig()
	c.databaseURL = "postgres://user:do-not-print-this@host/%zz"
	_, err := newPool(context.Background(), c)
	if err == nil || strings.Contains(err.Error(), "do-not-print-this") {
		t.Fatal("unsafe connection parsing error")
	}
}
