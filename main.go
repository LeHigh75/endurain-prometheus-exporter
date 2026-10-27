package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/exporter-toolkit/web"
)

func healthcheck(address, webConfigFile string) error {
	if address == "" {
		address = ":9808"
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("invalid LISTEN_ADDRESS")
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	if webConfigFile != "" {
		// Toolkit auth/mTLS protects every route; TCP is process liveness only.
		connection, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 3*time.Second)
		if err != nil {
			return errors.New("healthcheck connection failed")
		}
		return connection.Close()
	}
	client := http.Client{Timeout: 3 * time.Second}
	response, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return errors.New("healthcheck connection failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("healthcheck failed")
	}
	return nil
}

func run() error {
	check := flag.Bool("healthcheck", false, "check local process liveness")
	listen := flag.String("web.listen-address", os.Getenv("LISTEN_ADDRESS"), "HTTP(S) listen address (default :9808)")
	webConfig := flag.String("web.config.file", os.Getenv("WEB_CONFIG_FILE"), "Exporter Toolkit web configuration file")
	flag.Parse()
	if *check {
		return healthcheck(*listen, *webConfig)
	}
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if *listen != "" {
		c.listen = *listen
	}
	c.webConfigFile = *webConfig
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return serve(ctx, c)
}

func serve(ctx context.Context, c config) error {
	if err := web.Validate(c.webConfigFile); err != nil {
		return errors.New("invalid exporter-toolkit web configuration")
	}
	pool, err := newPool(ctx, c)
	if err != nil {
		return err
	}
	defer pool.Close()
	server := &http.Server{
		Addr: c.listen,
		Handler: newHandler(c, func(ctx context.Context) (snapshot, error) {
			return readSnapshot(ctx, pool, c.maxSeries, c.activityDetails)
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      c.timeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8192,
	}
	listener, err := net.Listen("tcp", c.listen)
	if err != nil {
		return errors.New("cannot listen on LISTEN_ADDRESS")
	}
	defer listener.Close()
	finished := make(chan error, 1)
	go func() {
		finished <- web.Serve(listener, server, &web.FlagConfig{WebConfigFile: &c.webConfigFile}, slog.Default())
	}()
	slog.Info("exporter listening", "address", c.listen)
	select {
	case err := <-finished:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("HTTP server stopped")
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), c.timeout+time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return errors.New("HTTP shutdown timed out")
		}
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		slog.Error("exporter stopped", "error", err)
		os.Exit(1)
	}
}
