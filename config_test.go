package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testConfig() config {
	return config{timeout: time.Second, maxSeries: 20000}
}

func cleanEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"DATABASE_URL", "DATABASE_URL_FILE", "METRICS_TOKEN", "METRICS_TOKEN_FILE", "LISTEN_ADDRESS", "SCRAPE_TIMEOUT", "MAX_SERIES", "ACTIVITY_DETAILS", "WEB_CONFIG_FILE"} {
		t.Setenv(name, "")
	}
	t.Setenv("DATABASE_URL", "postgres://user:development-password@localhost/database?sslmode=disable")
}

func TestConfig(t *testing.T) {
	cleanEnvironment(t)
	c, err := loadConfig()
	if err != nil || c.listen != ":9808" || c.timeout != 5*time.Second || c.maxSeries != 20000 || c.activityDetails {
		t.Fatalf("unexpected defaults or configuration error: %v", err)
	}
	for _, tc := range []struct{ name, value string }{
		{"ACTIVITY_DETAILS", "invalid"}, {"DATABASE_URL", ""},
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
	t.Setenv("ACTIVITY_DETAILS", "true")
	t.Setenv("LISTEN_ADDRESS", "127.0.0.1:9809")
	t.Setenv("WEB_CONFIG_FILE", "/run/config/web.yml")
	c, err = loadConfig()
	if err != nil || c.timeout != 2*time.Second || c.maxSeries != 100 || !c.activityDetails || c.webConfigFile != "/run/config/web.yml" || c.listen != "127.0.0.1:9809" {
		t.Fatal("overrides not applied")
	}
}

func TestRemovedBearerSettingsFailClosed(t *testing.T) {
	for _, name := range []string{"METRICS_TOKEN", "METRICS_TOKEN_FILE"} {
		t.Run(name, func(t *testing.T) {
			cleanEnvironment(t)
			t.Setenv(name, "obsolete-setting-must-not-be-ignored")
			if _, err := loadConfig(); err == nil {
				t.Fatal("removed Bearer setting silently disabled access control")
			}
		})
	}
}

func TestFileSecrets(t *testing.T) {
	cleanEnvironment(t)
	for _, name := range []string{"DATABASE_URL"} {
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
