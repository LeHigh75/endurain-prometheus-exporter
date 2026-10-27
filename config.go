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
	listen          string
	timeout         time.Duration
	maxSeries       int
	activityDetails bool
	webConfigFile   string
}

func secret(name string) (string, error) {
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
	if value == "" {
		return "", fmt.Errorf("%s or %s_FILE is required", name, name)
	}
	return value, nil
}

func loadConfig() (config, error) {
	c := config{listen: ":9808", timeout: 5 * time.Second, maxSeries: 20000}
	var err error
	c.webConfigFile = os.Getenv("WEB_CONFIG_FILE")
	if c.databaseURL, err = secret("DATABASE_URL"); err != nil {
		return c, err
	}
	if os.Getenv("METRICS_TOKEN") != "" || os.Getenv("METRICS_TOKEN_FILE") != "" {
		return c, errors.New("METRICS_TOKEN and METRICS_TOKEN_FILE were removed; configure WEB_CONFIG_FILE or an external access control")
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
