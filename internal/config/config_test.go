package config

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// validEnv is a complete, valid environment; cases override single keys.
func validEnv() map[string]string {
	return map[string]string{
		envPublicOrigin: "http://127.0.0.1:8090",
		envHTTPAddr:     ":8080",
		envScanInterval: "10m",
		envWorkers:      "2",
	}
}

func TestLoadValid(t *testing.T) {
	base := Config{PublicOrigin: "http://127.0.0.1:8090", HTTPAddr: ":8080", ScanInterval: 10 * time.Minute, Workers: 2}
	with := func(change func(*Config)) Config {
		c := base
		change(&c)
		return c
	}
	for _, tc := range []struct {
		name string
		set  map[string]string
		cpus int
		want Config
	}{
		{name: "all set", cpus: 8, want: base},
		{name: "defaults", set: map[string]string{envHTTPAddr: "", envScanInterval: "", envWorkers: ""}, cpus: 8,
			want: Config{PublicOrigin: "http://127.0.0.1:8090", HTTPAddr: ":8080", ScanInterval: 5 * time.Minute, Workers: 4}},
		{name: "workers default, 2 cpus", set: map[string]string{envWorkers: ""}, cpus: 2,
			want: with(func(c *Config) { c.Workers = 2 })},
		{name: "workers default, 1 cpu", set: map[string]string{envWorkers: ""}, cpus: 1,
			want: with(func(c *Config) { c.Workers = 1 })},
		{name: "workers 1", set: map[string]string{envWorkers: "1"}, cpus: 8,
			want: with(func(c *Config) { c.Workers = 1 })},
		{name: "workers 16", set: map[string]string{envWorkers: "16"}, cpus: 8,
			want: with(func(c *Config) { c.Workers = 16 })},
		{name: "scan interval at the minimum", set: map[string]string{envScanInterval: "30s"}, cpus: 8,
			want: with(func(c *Config) { c.ScanInterval = 30 * time.Second })},
		{name: "scan interval with two units", set: map[string]string{envScanInterval: "1h30m"}, cpus: 8,
			want: with(func(c *Config) { c.ScanInterval = 90 * time.Minute })},
		{name: "https origin with port", set: map[string]string{envPublicOrigin: "https://vibrance.lan:8443"}, cpus: 8,
			want: with(func(c *Config) { c.PublicOrigin = "https://vibrance.lan:8443" })},
		{name: "https origin without port", set: map[string]string{envPublicOrigin: "https://vibrance.example.net"}, cpus: 8,
			want: with(func(c *Config) { c.PublicOrigin = "https://vibrance.example.net" })},
		{name: "http origin on the https port", set: map[string]string{envPublicOrigin: "http://host:443"}, cpus: 8,
			want: with(func(c *Config) { c.PublicOrigin = "http://host:443" })},
		{name: "ipv6 origin", set: map[string]string{envPublicOrigin: "http://[::1]:8090"}, cpus: 8,
			want: with(func(c *Config) { c.PublicOrigin = "http://[::1]:8090" })},
		{name: "addr with host", set: map[string]string{envHTTPAddr: "127.0.0.1:9000"}, cpus: 8,
			want: with(func(c *Config) { c.HTTPAddr = "127.0.0.1:9000" })},
		{name: "addr with ipv6 host", set: map[string]string{envHTTPAddr: "[::]:8080"}, cpus: 8,
			want: with(func(c *Config) { c.HTTPAddr = "[::]:8080" })},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := validEnv()
			for k, v := range tc.set {
				e[k] = v
			}
			cfg, err := Load(env(e), tc.cpus)
			if err != nil {
				t.Fatal(err)
			}
			if cfg != tc.want {
				t.Fatalf("config %+v, want %+v", cfg, tc.want)
			}
		})
	}
}

// Each invalid value is refused with an *Error that names its variable,
// and only that one, and says what is wrong.
func TestLoadInvalid(t *testing.T) {
	for _, tc := range []struct {
		name     string
		variable string
		value    string
		want     string // substring of the problem
	}{
		{"origin missing", envPublicOrigin, "", "required"},
		{"origin trailing slash", envPublicOrigin, "http://127.0.0.1:8090/", "no path"},
		{"origin path", envPublicOrigin, "http://host/app", "no path"},
		{"origin query", envPublicOrigin, "http://host?x=1", "no path"},
		{"origin empty query", envPublicOrigin, "http://host?", "no path"},
		{"origin fragment", envPublicOrigin, "http://host#x", "no path"},
		{"origin empty fragment", envPublicOrigin, "http://host#", "canonical"},
		{"origin scheme", envPublicOrigin, "ftp://host", "http or https"},
		{"origin no scheme", envPublicOrigin, "127.0.0.1:8090", "is not a URL"},
		{"origin host only", envPublicOrigin, "vibrance.lan", "http or https"},
		{"origin uppercase scheme", envPublicOrigin, "HTTP://host", "canonical"},
		{"origin uppercase host", envPublicOrigin, "http://Host", "lowercase ASCII"},
		{"origin unicode host", envPublicOrigin, "http://müsic.lan", "lowercase ASCII"},
		{"origin escaped host", envPublicOrigin, "http://%61bc", "is not a URL"},
		{"origin userinfo", envPublicOrigin, "http://u:p@host", "user information"},
		{"origin opaque", envPublicOrigin, "http:host", "user information"},
		{"origin default port", envPublicOrigin, "http://host:80", "default port"},
		{"origin default https port", envPublicOrigin, "https://host:443", "default port"},
		{"origin port too big", envPublicOrigin, "http://host:65536", "1..65535"},
		{"origin port zero", envPublicOrigin, "http://host:0", "1..65535"},
		{"origin empty port", envPublicOrigin, "http://host:", "1..65535"},
		{"origin leading zero port", envPublicOrigin, "http://host:08090", "1..65535"},
		{"origin no host", envPublicOrigin, "http://", "host is missing"},
		{"origin port without host", envPublicOrigin, "http://:8090", "host is missing"},
		{"origin with spaces", envPublicOrigin, " http://host", "is not a URL"},

		{"addr no port", envHTTPAddr, "127.0.0.1", "not host:port"},
		{"addr port zero", envHTTPAddr, ":0", "1..65535"},
		{"addr port name", envHTTPAddr, ":http", "1..65535"},
		{"addr port too big", envHTTPAddr, ":65536", "1..65535"},
		{"addr leading zero port", envHTTPAddr, ":08080", "1..65535"},
		{"addr empty port", envHTTPAddr, "127.0.0.1:", "1..65535"},
		{"addr two colons", envHTTPAddr, "::1:8080", "not host:port"},

		{"scan interval number", envScanInterval, "300", "not a Go duration"},
		{"scan interval word", envScanInterval, "five minutes", "not a Go duration"},
		{"scan interval space", envScanInterval, "5 m", "not a Go duration"},
		{"scan interval too short", envScanInterval, "29s", "shorter than 30s"},
		{"scan interval just too short", envScanInterval, "29999ms", "shorter than 30s"},
		{"scan interval zero", envScanInterval, "0", "shorter than 30s"},
		{"scan interval negative", envScanInterval, "-5m", "shorter than 30s"},
		{"scan interval overflow", envScanInterval, "99999999999h", "not a Go duration"},

		{"workers 0", envWorkers, "0", "outside 1..16"},
		{"workers 17", envWorkers, "17", "outside 1..16"},
		{"workers negative", envWorkers, "-1", "outside 1..16"},
		{"workers plus", envWorkers, "+2", "plain decimal"},
		{"workers leading zero", envWorkers, "02", "plain decimal"},
		{"workers space", envWorkers, " 2", "plain decimal"},
		{"workers word", envWorkers, "four", "plain decimal"},
		{"workers fraction", envWorkers, "2.0", "plain decimal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := validEnv()
			e[tc.variable] = tc.value
			cfg, err := Load(env(e), 8)
			var ce *Error
			if !errors.As(err, &ce) {
				t.Fatalf("Load = %+v, %v; want an *Error", cfg, err)
			}
			if cfg != (Config{}) {
				t.Fatalf("a refused configuration is returned: %+v", cfg)
			}
			if len(ce.problems) != 1 || !strings.HasPrefix(ce.problems[0], tc.variable+": ") ||
				!strings.Contains(ce.problems[0], tc.want) {
				t.Fatalf("problems %q, want one for %s containing %q", ce.problems, tc.variable, tc.want)
			}
			if !strings.HasPrefix(err.Error(), "invalid configuration: "+tc.variable+": ") {
				t.Fatalf("message %q does not name %s", err, tc.variable)
			}
		})
	}
}

// Every invalid variable is reported at once, in the order of the
// documentation, whatever the combination.
func TestLoadReportsEveryProblem(t *testing.T) {
	bad := map[string]string{
		envPublicOrigin: "http://host/",
		envHTTPAddr:     "x",
		envScanInterval: "1s",
		envWorkers:      "99",
	}
	order := []string{envPublicOrigin, envHTTPAddr, envScanInterval, envWorkers}
	// Every subset of the four variables, from none to all of them.
	for mask := range 1 << len(order) {
		e := validEnv()
		var want []string
		for i, name := range order {
			if mask&(1<<i) != 0 {
				e[name] = bad[name]
				want = append(want, name)
			}
		}
		_, err := Load(env(e), 4)
		if len(want) == 0 {
			if err != nil {
				t.Fatalf("valid environment: %v", err)
			}
			continue
		}
		var ce *Error
		if !errors.As(err, &ce) {
			t.Fatalf("%v invalid: error %v, want an *Error", want, err)
		}
		var got []string
		for _, p := range ce.problems {
			name, _, _ := strings.Cut(p, ": ")
			got = append(got, name)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("problems for %v, want %v: %q", got, want, ce.problems)
		}
		for _, name := range want {
			if !strings.Contains(err.Error(), name+": ") {
				t.Fatalf("message %q does not name %s", err, name)
			}
		}
	}
}

// An empty environment lacks only the one required variable.
func TestLoadEmptyEnvironment(t *testing.T) {
	_, err := Load(env(nil), 4)
	var ce *Error
	if !errors.As(err, &ce) || len(ce.problems) != 1 || !strings.HasPrefix(ce.problems[0], envPublicOrigin+": required") {
		t.Fatalf("error %v, want only %s required", err, envPublicOrigin)
	}
}

// User information in the origin may be a password: the message does not
// repeat the value.
func TestLoadWithholdsUserInformation(t *testing.T) {
	e := validEnv()
	e[envPublicOrigin] = "http://admin:hunter2-secret@host"
	_, err := Load(env(e), 4)
	if err == nil || !strings.Contains(err.Error(), "user information") {
		t.Fatalf("error %v", err)
	}
	if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "admin") {
		t.Fatalf("the message repeats the user information: %v", err)
	}
}

func TestDefaultWorkers(t *testing.T) {
	for cpus, want := range map[int]int{0: 1, 1: 1, 2: 2, 3: 3, 4: 4, 5: 4, 64: 4} {
		if got := defaultWorkers(cpus); got != want {
			t.Fatalf("defaultWorkers(%d) = %d, want %d", cpus, got, want)
		}
	}
}

// HTTPAddr looks at VIBRANCE_HTTP_ADDR alone: the rest of the environment
// may be missing or invalid.
func TestHTTPAddr(t *testing.T) {
	for value, want := range map[string]string{"": ":8080", ":9000": ":9000", "127.0.0.1:8080": "127.0.0.1:8080"} {
		got, err := HTTPAddr(env(map[string]string{envHTTPAddr: value, envWorkers: "99"}))
		if err != nil || got != want {
			t.Fatalf("HTTPAddr(%q) = %q, %v; want %q", value, got, err, want)
		}
	}
	got, err := HTTPAddr(env(map[string]string{envHTTPAddr: "nonsense"}))
	var ce *Error
	if !errors.As(err, &ce) || got != "" || !strings.Contains(err.Error(), envHTTPAddr+": ") {
		t.Fatalf("HTTPAddr(nonsense) = %q, %v; want an *Error naming %s", got, err, envHTTPAddr)
	}
}

// The names of the variables are part of the contract with the operator
// and with compose.yaml (DESIGN.md §11.1, §11.6).
func TestVariableNames(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"VIBRANCE_PUBLIC_ORIGIN": "https://vibrance.example.net",
		"VIBRANCE_HTTP_ADDR":     "127.0.0.1:9001",
		"VIBRANCE_SCAN_INTERVAL": "45s",
		"VIBRANCE_WORKERS":       "7",
	}), 4)
	want := Config{PublicOrigin: "https://vibrance.example.net", HTTPAddr: "127.0.0.1:9001", ScanInterval: 45 * time.Second, Workers: 7}
	if err != nil || cfg != want {
		t.Fatalf("Load = %+v, %v; want %+v", cfg, err, want)
	}
}
