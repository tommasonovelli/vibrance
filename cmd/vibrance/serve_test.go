package main

import (
	"bytes"
	"net"
	"strings"
	"testing"
)

// serveEnv is a complete, valid environment that listens on addr.
func serveEnv(addr string) map[string]string {
	return map[string]string{
		"VIBRANCE_PUBLIC_ORIGIN": "http://127.0.0.1:8090",
		"VIBRANCE_HTTP_ADDR":     addr,
	}
}

// What stops the server before it serves, and how the process ends: a
// refusal before anything is opened exits 2, a failure after that exits 1.
// Each logs one error line with its stable code. The address is taken, so
// a server that got as far as listening shows as http_listen.
func TestServeExitCodes(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := busy.Close(); err != nil {
			t.Error(err)
		}
	}()
	addr := busy.Addr().String()

	for _, tc := range []struct {
		name string
		euid int
		set  map[string]string
		exit int
		code string
		want []string // substrings of the message
	}{
		{name: "root", euid: 0, exit: exitUsage, code: "run_as_root", want: []string{"must not run as root"}},
		{name: "no origin", euid: nonRoot, set: map[string]string{"VIBRANCE_PUBLIC_ORIGIN": ""},
			exit: exitUsage, code: "config_invalid", want: []string{"VIBRANCE_PUBLIC_ORIGIN: required"}},
		{name: "everything invalid", euid: nonRoot,
			set: map[string]string{"VIBRANCE_PUBLIC_ORIGIN": "vibrance.lan", "VIBRANCE_HTTP_ADDR": "8080",
				"VIBRANCE_SCAN_INTERVAL": "5", "VIBRANCE_WORKERS": "many"},
			exit: exitUsage, code: "config_invalid",
			want: []string{"VIBRANCE_PUBLIC_ORIGIN: ", "VIBRANCE_HTTP_ADDR: ", "VIBRANCE_SCAN_INTERVAL: ", "VIBRANCE_WORKERS: "}},
		{name: "address in use", euid: nonRoot, exit: exitFailure, code: "http_listen", want: []string{"cannot listen on " + addr}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := serveEnv(addr)
			for k, v := range tc.set {
				e[k] = v
			}
			var stdout, logs bytes.Buffer
			if got := run([]string{"serve"}, env(e), tc.euid, noInput(), &stdout, newLogger(&logs)); got != tc.exit {
				t.Fatalf("exit code %d, want %d; logs:\n%s", got, tc.exit, logs.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("unexpected stdout %q", stdout.String())
			}
			// A refusal logs nothing but the error; a later failure comes
			// after the `starting` event.
			lines := strings.SplitAfter(strings.TrimSuffix(logs.String(), "\n"), "\n")
			if tc.exit == exitUsage && len(lines) != 1 {
				t.Fatalf("a refusal logged more than its error:\n%s", logs.String())
			}
			msg := wantLog(t, []byte(lines[len(lines)-1]), tc.code)
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Fatalf("the message does not contain %q: %s", want, msg)
				}
			}
		})
	}
}
