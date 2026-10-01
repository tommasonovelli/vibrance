package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHealthTarget(t *testing.T) {
	for addr, want := range map[string]string{
		":8080":            "127.0.0.1:8080",
		"0.0.0.0:8080":     "127.0.0.1:8080",
		"[::]:8080":        "[::1]:8080",
		"127.0.0.1:9000":   "127.0.0.1:9000",
		"192.168.1.5:8080": "192.168.1.5:8080",
		"[::1]:8080":       "[::1]:8080",
		"localhost:8080":   "localhost:8080",
		"not an address":   "not an address",
	} {
		if got := healthTarget(addr); got != want {
			t.Errorf("healthTarget(%q) = %q, want %q", addr, got, want)
		}
	}
}

// The healthcheck subcommand exits 0 only for a 200 from /health/ready on
// VIBRANCE_HTTP_ADDR (DESIGN.md §11.3); every other outcome is 1, the
// "unhealthy" of Docker, with one error line that says why.
func TestHealthcheckExitCodes(t *testing.T) {
	var status atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/ok": // the redirect target: 200 if it were followed
			w.WriteHeader(http.StatusOK)
		case r.URL.Path != "/health/ready":
			http.NotFound(w, r)
		case status.Load() == http.StatusFound:
			http.Redirect(w, r, "/ok", http.StatusFound)
		default:
			w.WriteHeader(int(status.Load()))
		}
	}))
	defer srv.Close()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		addr   string
		status int
		want   int
		code   string
	}{
		{"ready", "127.0.0.1:" + port, http.StatusOK, exitOK, ""},
		{"unspecified host", "0.0.0.0:" + port, http.StatusOK, exitOK, ""},
		{"empty host", ":" + port, http.StatusOK, exitOK, ""},
		{"not ready", "127.0.0.1:" + port, http.StatusServiceUnavailable, exitFailure, "unhealthy"},
		{"no content", "127.0.0.1:" + port, http.StatusNoContent, exitFailure, "unhealthy"},
		{"redirect not followed", "127.0.0.1:" + port, http.StatusFound, exitFailure, "unhealthy"},
		{"server error", "127.0.0.1:" + port, http.StatusInternalServerError, exitFailure, "unhealthy"},
		{"nothing listening", freeAddr(t), http.StatusOK, exitFailure, "unhealthy"},
		{"invalid address", "nonsense", http.StatusOK, exitFailure, "config_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status.Store(int64(tc.status))
			var stdout, logs bytes.Buffer
			// Only VIBRANCE_HTTP_ADDR is read: the rest of the server's
			// configuration is absent, and the uid does not matter.
			got := run([]string{"healthcheck"}, env(map[string]string{"VIBRANCE_HTTP_ADDR": tc.addr}), 0, &stdout, newLogger(&logs))
			if got != tc.want {
				t.Fatalf("exit %d, want %d; logs: %s", got, tc.want, logs.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("unexpected stdout %q", stdout.String())
			}
			if tc.want == exitOK {
				if logs.Len() != 0 {
					t.Fatalf("a successful healthcheck logged: %s", logs.String())
				}
				return
			}
			wantLog(t, logs.Bytes(), tc.code)
		})
	}
}

// A server that accepts the connection and never answers is unhealthy
// after the 2 seconds of §11.3, not when Docker's own timeout kills the
// command.
func TestHealthcheckTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)

	var stdout, logs bytes.Buffer
	start := time.Now()
	got := run([]string{"healthcheck"}, env(map[string]string{"VIBRANCE_HTTP_ADDR": srv.Listener.Addr().String()}),
		nonRoot, &stdout, newLogger(&logs))
	elapsed := time.Since(start)
	if got != exitFailure {
		t.Fatalf("exit %d, want %d", got, exitFailure)
	}
	if elapsed < 2*time.Second || elapsed > 4*time.Second {
		t.Fatalf("the healthcheck gave up after %s, want 2s", elapsed)
	}
	wantLog(t, logs.Bytes(), "unhealthy")
}
