package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// The public origin of the tests, and its host.
const (
	testOrigin = "http://vibrance.test:8090"
	testHost   = "vibrance.test:8090"
)

// logBuffer is an io.Writer safe for the concurrent writes of a logger and
// the reads of a test.
type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// events parses the JSON log lines.
func (l *logBuffer) events(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.Lines(l.String()) {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("log line %q is not JSON: %v", line, err)
		}
		out = append(out, ev)
	}
	return out
}

// newLog returns a logger at DEBUG and what it writes.
func newLog() (*slog.Logger, *logBuffer) {
	logs := &logBuffer{}
	return slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})), logs
}

// chain is the boundary as the server composes it, around next.
func chain(t *testing.T, log *slog.Logger, quiet []string, next http.Handler) http.Handler {
	t.Helper()
	boundary, err := Boundary(log, testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	return Recover(log)(RequestID(log)(AccessLog(log, quiet)(SecurityHeaders(boundary(next)))))
}

// request is a request for the public origin of the tests.
func request(method, target string, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = testHost
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set(RequestHeader, "1")
	}
	return req
}

func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// wantError checks that a response is the error model of the API, exactly:
// the status, application/json, and a body with code, message and details
// and nothing else. It returns the message.
func wantError(t *testing.T, where string, rec *httptest.ResponseRecorder, status int, code string) string {
	t.Helper()
	if rec.Code != status {
		t.Errorf("%s: status %d, want %d (body %q)", where, rec.Code, status, rec.Body.String())
		return ""
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("%s: Content-Type %q, want application/json", where, got)
	}
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	dec.DisallowUnknownFields()
	var body struct {
		Code    *string         `json:"code"`
		Message *string         `json:"message"`
		Details *map[string]any `json:"details"`
	}
	if err := dec.Decode(&body); err != nil {
		t.Errorf("%s: the body is not {code, message, details}: %v (%q)", where, err, rec.Body.String())
		return ""
	}
	if body.Code == nil || body.Message == nil || body.Details == nil {
		t.Errorf("%s: code, message and details must all be present: %q", where, rec.Body.String())
		return ""
	}
	if *body.Code != code {
		t.Errorf("%s: code %q, want %q (message %q)", where, *body.Code, code, *body.Message)
	}
	if *body.Message == "" {
		t.Errorf("%s: the message is empty", where)
	}
	return *body.Message
}

// wantBoundaryHeaders checks what every response that crossed the whole
// boundary has (§7.6, §8.1): the id of the request, a UUIDv7; nosniff and
// no referrer; on JSON, no-store and the closed CSP; and never a CORS
// header.
func wantBoundaryHeaders(t *testing.T, where string, h http.Header) {
	t.Helper()
	ids := h.Values("X-Request-Id")
	if len(ids) != 1 {
		t.Errorf("%s: X-Request-Id %q, want exactly one", where, ids)
	} else if id, err := uuid.Parse(ids[0]); err != nil || id.Version() != 7 || id.String() != ids[0] {
		t.Errorf("%s: X-Request-Id %q is not a UUIDv7 in canonical form", where, ids[0])
	}
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("%s: X-Content-Type-Options %q, want nosniff", where, got)
	}
	if got := h.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("%s: Referrer-Policy %q, want no-referrer", where, got)
	}
	if strings.HasPrefix(h.Get("Content-Type"), "application/json") {
		if got := h.Get("Cache-Control"); got != "private, no-store" {
			t.Errorf("%s: Cache-Control %q on JSON, want private, no-store", where, got)
		}
		if got := h.Get("Content-Security-Policy"); got != "default-src 'none'; frame-ancestors 'none'" {
			t.Errorf("%s: Content-Security-Policy %q on JSON", where, got)
		}
	}
	wantNoCORS(t, where, h)
}

// wantNoCORS: no CORS header, in any response (I4).
func wantNoCORS(t *testing.T, where string, h http.Header) {
	t.Helper()
	for name := range h {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			t.Errorf("%s: the response has the CORS header %s: %q", where, name, h.Values(name))
		}
	}
}

// ok is a handler that answers 204.
var ok = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
