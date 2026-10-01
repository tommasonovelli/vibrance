package main

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"vibrance/internal/config"
)

// healthcheckTimeout bounds the whole request (DESIGN.md §11.3).
const healthcheckTimeout = 2 * time.Second

// healthcheck is `vibrance healthcheck`: the runtime image has no curl. It
// queries /health/ready of the local server on VIBRANCE_HTTP_ADDR and
// returns 0 for a 200, 1 for anything else, the two values Docker defines
// for a healthcheck. It reads only VIBRANCE_HTTP_ADDR and opens nothing
// else.
func healthcheck(getenv func(string) string, log *slog.Logger) int {
	addr, err := config.HTTPAddr(getenv)
	if err != nil {
		log.Error("healthcheck: "+err.Error(), "code", config.Code)
		return exitFailure
	}
	client := &http.Client{
		Timeout: healthcheckTimeout,
		// No proxy from the environment, no keep-alive, no redirects: the
		// answer must come from this server's own endpoint.
		Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get("http://" + healthTarget(addr) + "/health/ready")
	if err != nil {
		log.Error("healthcheck: "+err.Error(), "code", "unhealthy")
		return exitFailure
	}
	_, rerr := io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if err := errors.Join(rerr, resp.Body.Close()); err != nil {
		log.Error("healthcheck: reading the response: "+err.Error(), "code", "unhealthy")
		return exitFailure
	}
	if resp.StatusCode != http.StatusOK {
		log.Error("healthcheck: "+resp.Status, "code", "unhealthy")
		return exitFailure
	}
	return exitOK
}

// healthTarget turns the listen address into the local address to connect
// to: an empty or unspecified host (":8080", "0.0.0.0:8080", "[::]:8080")
// becomes the loopback address of the same family.
func healthTarget(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	ip := net.ParseIP(host)
	switch {
	case host == "":
		host = "127.0.0.1"
	case ip != nil && ip.IsUnspecified() && ip.To4() == nil:
		host = "::1"
	case ip != nil && ip.IsUnspecified():
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
