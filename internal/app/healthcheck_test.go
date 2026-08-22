package app

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestHealthcheck_SucceedsAgainstAHealthyServer(t *testing.T) {
	listen := freeAddress(t)
	dir := writeConfigDir(t, "tcp://broker.example:1883", listen,
		map[string]string{"zwave.yaml": zwaveSource})

	server := newServer(listen, prometheus.NewRegistry(), func() bool { return true })

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- serve(ctx, server) }()

	require.Eventually(t, func() bool {
		_, _, err := tryGet(t, "http://"+listen+"/healthz")

		return err == nil
	}, 5*time.Second, 10*time.Millisecond)

	require.NoError(t, Healthcheck(dir))

	cancel()
	require.NoError(t, <-done)
}

func TestHealthcheck_FailsWhenNothingIsListening(t *testing.T) {
	dir := writeConfigDir(t, "tcp://broker.example:1883", freeAddress(t),
		map[string]string{"zwave.yaml": zwaveSource})

	require.ErrorContains(t, Healthcheck(dir), "probing")
}

func TestHealthcheck_ReportsANonOKStatus(t *testing.T) {
	listen := freeAddress(t)
	dir := writeConfigDir(t, "tcp://broker.example:1883", listen,
		map[string]string{"zwave.yaml": zwaveSource})

	server := &http.Server{
		Addr:              listen,
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}),
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- serve(ctx, server) }()

	require.Eventually(t, func() bool {
		_, _, err := tryGet(t, "http://"+listen+"/healthz")

		return err == nil
	}, 5*time.Second, 10*time.Millisecond)

	require.ErrorContains(t, Healthcheck(dir), "health endpoint returned 500")

	cancel()
	require.NoError(t, <-done)
}

func TestHealthcheck_ReportsABrokenConfig(t *testing.T) {
	require.Error(t, Healthcheck(filepath.Join(t.TempDir(), "absent")))
}

func TestHealthURL(t *testing.T) {
	require.Equal(t, "http://127.0.0.1:9000/healthz", healthURL(":9000"))
	require.Equal(t, "http://127.0.0.1:9100/healthz", healthURL("127.0.0.1:9100"))
	require.Equal(t, "http://127.0.0.1:9000/healthz", healthURL("nonsense"))
}

func TestHealthcheck_ReportsAnUnusableListenAddress(t *testing.T) {
	dir := writeConfigDir(t, "tcp://broker.example:1883", "not a host:9000",
		map[string]string{"zwave.yaml": zwaveSource})

	require.ErrorContains(t, Healthcheck(dir), "building probe")
}
