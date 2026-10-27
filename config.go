package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type config struct {
	databaseURL     string
	token           string
	listen          string
	timeout         time.Duration
	maxSeries       int
	activityDetails bool
	webConfigFile   string
}

func secret(name string, required bool) (string, error) {
	value, file := os.Getenv(name), os.Getenv(name+"_FILE")
	if value != "" && file != "" {
		return "", fmt.Errorf("configure only %s or %s_FILE", name, name)
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("cannot read %s_FILE", name)
		}
		value = strings.TrimSpace(string(data))
	}
	if value == "" && (required || file != "") {
		return "", fmt.Errorf("%s or %s_FILE is required", name, name)
	}
	return value, nil
}

func loadConfig() (config, error) {
	c := config{listen: ":9808", timeout: 5 * time.Second, maxSeries: 20000}
	var err error
	c.webConfigFile = os.Getenv("WEB_CONFIG_FILE")
	if c.databaseURL, err = secret("DATABASE_URL", true); err != nil {
		return c, err
	}
	if c.token, err = secret("METRICS_TOKEN", false); err != nil {
		return c, err
	}
	if c.token != "" && (len(c.token) < 32 || len(c.token) > 4096 || strings.IndexFunc(c.token, func(r rune) bool { return r < 33 || r > 126 }) >= 0) {
		return c, errors.New("METRICS_TOKEN must contain 32 to 4096 characters without whitespace")
	}
	if value := os.Getenv("LISTEN_ADDRESS"); value != "" {
		c.listen = value
	}
	if value := os.Getenv("SCRAPE_TIMEOUT"); value != "" {
		c.timeout, err = time.ParseDuration(value)
		if err != nil || c.timeout < time.Millisecond || c.timeout > time.Minute {
			return c, errors.New("SCRAPE_TIMEOUT must be between 1ms and 1m")
		}
	}
	if value := os.Getenv("MAX_SERIES"); value != "" {
		c.maxSeries, err = strconv.Atoi(value)
		if err != nil || c.maxSeries < 1 || c.maxSeries > 1000000 {
			return c, errors.New("MAX_SERIES must be between 1 and 1000000")
		}
	}
	if value := os.Getenv("ACTIVITY_DETAILS"); value != "" {
		c.activityDetails, err = strconv.ParseBool(value)
		if err != nil {
			return c, errors.New("ACTIVITY_DETAILS must be a boolean")
		}
	}
	return c, nil
}
