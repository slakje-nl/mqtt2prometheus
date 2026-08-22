package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func freeAddress(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	return address
}

func tryGet(t *testing.T, url string) (int, string, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		return 0, "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", err
	}

	return resp.StatusCode, string(body), nil
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { require.NoError(t, resp.Body.Close()) }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, string(body)
}

func TestServe_ServesMetricsHealthAndReadiness(t *testing.T) {
	address := freeAddress(t)
	registry := prometheus.NewRegistry()
	ready := false

	server := newServer(address, registry, func() bool { return ready })

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- serve(ctx, server) }()

	base := "http://" + address
	require.Eventually(t, func() bool {
		status, _, err := tryGet(t, base+"/healthz")

		return err == nil && status == http.StatusOK
	}, 5*time.Second, 10*time.Millisecond)

	status, body := get(t, base+"/healthz")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ok\n", body)

	status, body = get(t, base+"/readyz")
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Equal(t, "not ready\n", body)

	ready = true
	status, body = get(t, base+"/readyz")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "ready\n", body)

	status, _ = get(t, base+"/metrics")
	require.Equal(t, http.StatusOK, status)

	cancel()
	require.NoError(t, <-done)
}

func TestServe_ReportsAnUnusableListenAddress(t *testing.T) {
	server := newServer("127.0.0.1:-1", prometheus.NewRegistry(), func() bool { return true })

	err := serve(t.Context(), server)

	require.Error(t, err)
}

func TestWritePlain_ToleratesAFailedWrite(t *testing.T) {
	writePlain(failingWriter{}, http.StatusOK, "ok")
}

type failingWriter struct{}

func (failingWriter) Header() http.Header       { return http.Header{} }
func (failingWriter) WriteHeader(int)           {}
func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
