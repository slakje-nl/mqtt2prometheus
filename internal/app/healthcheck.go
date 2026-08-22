package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/slakje-nl/mqtt2prometheus/internal/config"
)

const healthcheckTimeout = 3 * time.Second

func Healthcheck(dir string) error {
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL(cfg.Server.Listen), nil)
	if err != nil {
		return fmt.Errorf("building probe: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("probing: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", resp.Status)
	}

	return nil
}

func healthURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil || host == "" {
		host = "127.0.0.1"
	}

	if err != nil {
		return "http://" + net.JoinHostPort(host, "9000") + "/healthz"
	}

	return "http://" + net.JoinHostPort(host, port) + "/healthz"
}
