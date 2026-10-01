package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The environment variables of the server (DESIGN.md §11.1). There are no
// flags and no configuration file. VIBRANCE_ADMIN_USERNAME and
// VIBRANCE_ADMIN_PASSWORD are read by the code that creates the first
// admin, not here.
const (
	envPublicOrigin = "VIBRANCE_PUBLIC_ORIGIN"
	envHTTPAddr     = "VIBRANCE_HTTP_ADDR"
	envScanInterval = "VIBRANCE_SCAN_INTERVAL"
	envWorkers      = "VIBRANCE_WORKERS"
)

const (
	defaultHTTPAddr     = ":8080"
	defaultScanInterval = 5 * time.Minute
	minScanInterval     = 30 * time.Second
	minWorkers          = 1
	maxWorkers          = 16
)

// Code is the stable code of an invalid configuration.
const Code = "config_invalid"

// Config is the validated configuration of the server.
type Config struct {
	// PublicOrigin is the exact origin clients reach the server with,
	// scheme://host[:port] (§7.6).
	PublicOrigin string
	// HTTPAddr is the listen address, host:port with an optional host.
	HTTPAddr string
	// ScanInterval is the time between two scans of the library.
	ScanInterval time.Duration
	// Workers bounds the ffmpeg/ffprobe processes and the album
	// indexings that run in parallel.
	Workers int
}

// Error is an invalid configuration. It names every invalid variable, not
// only the first, so that the operator fixes them in one round.
type Error struct {
	problems []string // "<VARIABLE>: <what is wrong>", in the order of §11.1
}

func (e *Error) Error() string {
	return "invalid configuration: " + strings.Join(e.problems, "; ")
}

func (e *Error) add(variable string, err error) {
	e.problems = append(e.problems, variable+": "+err.Error())
}

// Load reads and validates the configuration. An empty variable is an
// unset one: Compose passes `${VAR:-}` for what the operator left out.
// cpus is the number of CPUs available to the process, for the default of
// VIBRANCE_WORKERS. The error, if any, is an *Error.
func Load(getenv func(string) string, cpus int) (Config, error) {
	var (
		cfg Config
		e   Error
		err error
	)
	if cfg.PublicOrigin, err = parseOrigin(getenv(envPublicOrigin)); err != nil {
		e.add(envPublicOrigin, err)
	}
	if cfg.HTTPAddr, err = parseHTTPAddr(getenv(envHTTPAddr)); err != nil {
		e.add(envHTTPAddr, err)
	}
	if cfg.ScanInterval, err = parseScanInterval(getenv(envScanInterval)); err != nil {
		e.add(envScanInterval, err)
	}
	if cfg.Workers, err = parseWorkers(getenv(envWorkers), cpus); err != nil {
		e.add(envWorkers, err)
	}
	if len(e.problems) > 0 {
		return Config{}, &e
	}
	return cfg, nil
}

// HTTPAddr reads and validates VIBRANCE_HTTP_ADDR alone, for the
// healthcheck subcommand, which must work whatever the rest of the
// environment says. The error, if any, is an *Error.
func HTTPAddr(getenv func(string) string) (string, error) {
	addr, err := parseHTTPAddr(getenv(envHTTPAddr))
	if err != nil {
		var e Error
		e.add(envHTTPAddr, err)
		return "", &e
	}
	return addr, nil
}

// parseOrigin accepts exactly the serialization of an HTTP origin that
// browsers send in the Origin header (§7.6): scheme "http" or "https",
// lowercase ASCII host, optional non-default port, and nothing else, not
// even a trailing slash. A non-canonical spelling is refused rather than
// normalized, so that the value the server compares is the one configured.
func parseOrigin(s string) (string, error) {
	if s == "" {
		return "", errors.New("required, for example http://127.0.0.1:8090")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("%q is not a URL", s)
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return "", fmt.Errorf("%q: the scheme must be http or https", s)
	case u.Opaque != "" || u.User != nil:
		// The value is not repeated: the user information may be a password.
		return "", errors.New("an origin has no user information (value withheld)")
	case u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return "", fmt.Errorf("%q: an origin has no path, query or fragment, not even a trailing slash", s)
	case u.Hostname() == "":
		return "", fmt.Errorf("%q: the host is missing", s)
	}
	for _, r := range u.Hostname() {
		if r > 0x7f || ('A' <= r && r <= 'Z') {
			return "", fmt.Errorf("%q: the host must be lowercase ASCII (punycode for international names)", s)
		}
	}
	if port := u.Port(); port != "" || strings.HasSuffix(u.Host, ":") {
		n, err := parsePort(port)
		if err != nil {
			return "", fmt.Errorf("%q: %w", s, err)
		}
		if (u.Scheme == "http" && n == 80) || (u.Scheme == "https" && n == 443) {
			return "", fmt.Errorf("%q: omit the default port, browsers do", s)
		}
	}
	if canonical := u.Scheme + "://" + u.Host; canonical != s {
		return "", fmt.Errorf("%q is not in canonical form (%q)", s, canonical)
	}
	return s, nil
}

// parseHTTPAddr accepts host:port with a decimal port in 1..65535; the
// host may be empty (all interfaces). Empty means the default.
func parseHTTPAddr(s string) (string, error) {
	if s == "" {
		return defaultHTTPAddr, nil
	}
	_, port, err := net.SplitHostPort(s)
	if err != nil {
		return "", fmt.Errorf("%q is not host:port", s)
	}
	if _, err := parsePort(port); err != nil {
		return "", fmt.Errorf("%q: %w", s, err)
	}
	return s, nil
}

// parsePort accepts a plain decimal number in 1..65535.
func parsePort(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || strconv.Itoa(n) != s || n < 1 || n > 65535 {
		return 0, errors.New("the port must be a number in 1..65535")
	}
	return n, nil
}

// parseScanInterval accepts a Go duration of at least 30 seconds; empty
// means the default.
func parseScanInterval(s string) (time.Duration, error) {
	if s == "" {
		return defaultScanInterval, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a Go duration such as 5m or 90s", s)
	}
	if d < minScanInterval {
		return 0, fmt.Errorf("%q is shorter than %s", s, minScanInterval)
	}
	return d, nil
}

// defaultWorkers is max(1, min(4, available CPUs)).
func defaultWorkers(cpus int) int {
	return max(1, min(4, cpus))
}

// parseWorkers accepts a plain decimal integer in 1..16; empty means the
// default. Signs, leading zeros and spaces are refused.
func parseWorkers(s string, cpus int) (int, error) {
	if s == "" {
		return defaultWorkers(cpus), nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || strconv.Itoa(n) != s {
		return 0, fmt.Errorf("%q is not a plain decimal integer", s)
	}
	if n < minWorkers || n > maxWorkers {
		return 0, fmt.Errorf("%d is outside %d..%d", n, minWorkers, maxWorkers)
	}
	return n, nil
}
