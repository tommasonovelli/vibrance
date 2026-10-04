package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The matrix of DESIGN.md §7.6 (I4): Host, Origin and X-Vibrance-Request on
// GET, HEAD, POST, PUT and DELETE. Each value says by itself whether it is
// acceptable; the answer is that of the first check that fails, in the order
// Host, Origin, header, and the handler runs only when none does.
func TestBoundaryMatrix(t *testing.T) {
	hosts := []struct {
		value string
		ok    bool
	}{
		{testHost, true},
		{"VIBRANCE.TEST:8090", true}, // host names have no case
		{"vibrance.test", false},
		{"vibrance.test:80", false},
		{"vibrance.test:8091", false},
		{"127.0.0.1:8090", false},
		{"evil.example", false},
		{"vibrance.test:8090.evil.example", false},
		{"evil.example:8090", false},
		{"", false},
	}
	origins := []struct {
		values []string
		ok     bool
	}{
		{nil, true}, // a client that is not a browser
		{[]string{testOrigin}, true},
		{[]string{"null"}, false}, // a sandboxed frame, a file, a redirect
		{[]string{""}, false},
		{[]string{"http://evil.example"}, false},
		{[]string{testOrigin + "/"}, false},
		{[]string{"HTTP://VIBRANCE.TEST:8090"}, false},
		{[]string{"https://vibrance.test:8090"}, false},
		{[]string{"http://vibrance.test"}, false},
		{[]string{testOrigin + ".evil.example"}, false},
		{[]string{testOrigin, testOrigin}, false},
		{[]string{testOrigin, "http://evil.example"}, false},
		{[]string{testOrigin + ", " + testOrigin}, false},
	}
	headers := []struct {
		values []string
		ok     bool // on a request other than GET and HEAD
	}{
		{nil, false},
		{[]string{"1"}, true},
		{[]string{"0"}, false},
		{[]string{""}, false},
		{[]string{"true"}, false},
		{[]string{"11"}, false},
		{[]string{" 1"}, false},
		{[]string{"1", "1"}, false},
		{[]string{"1, 1"}, false},
	}
	methods := []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete,
		http.MethodPatch, http.MethodOptions}

	log, _ := newLog()
	reached := false
	handler := chain(t, log, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	}))

	cases := 0
	for _, method := range methods {
		safe := method == http.MethodGet || method == http.MethodHead
		for _, host := range hosts {
			for _, origin := range origins {
				for _, header := range headers {
					where := fmt.Sprintf("%s Host=%q Origin=%q %s=%q", method, host.value, origin.values, RequestHeader, header.values)
					req := httptest.NewRequest(method, "/anything?x=1", nil)
					req.Host = host.value
					for _, v := range origin.values {
						req.Header.Add("Origin", v)
					}
					for _, v := range header.values {
						req.Header.Add(RequestHeader, v)
					}
					reached = false
					rec := serve(handler, req)
					cases++

					wantBoundaryHeaders(t, where, rec.Header())
					switch {
					case !host.ok:
						wantRefusal(t, where, rec, http.StatusMisdirectedRequest, "host_not_allowed")
					case !origin.ok:
						wantRefusal(t, where, rec, http.StatusForbidden, "origin_not_allowed")
					case !safe && !header.ok:
						wantRefusal(t, where, rec, http.StatusForbidden, "request_header_required")
					default:
						if rec.Code != http.StatusNoContent || !reached {
							t.Errorf("%s: status %d, handler reached %v; want the handler's 204", where, rec.Code, reached)
						}
						continue
					}
					if reached {
						t.Errorf("%s: the handler ran behind a refusal", where)
					}
				}
			}
		}
	}
	if want := len(methods) * len(hosts) * len(origins) * len(headers); cases != want {
		t.Fatalf("%d cases, want %d", cases, want)
	}
}

// wantRefusal checks a refusal of the boundary.
func wantRefusal(t *testing.T, where string, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	message := wantError(t, where, rec, status, code)
	// The refusal does not teach the public origin to whoever asks from
	// another one.
	if strings.Contains(message, "vibrance.test") {
		t.Errorf("%s: the message names the public origin: %q", where, message)
	}
}

// The cases the design names, written out: they do not depend on the tables
// of the matrix.
func TestBoundaryNamedCases(t *testing.T) {
	log, _ := newLog()
	handler := chain(t, log, nil, ok)

	for _, tc := range []struct {
		name   string
		method string
		host   string
		header map[string]string
		status int
		code   string
	}{
		{"GET with nothing else", "GET", testHost, nil, 204, ""},
		{"HEAD with nothing else", "HEAD", testHost, nil, 204, ""},
		{"GET with the right Origin", "GET", testHost, map[string]string{"Origin": testOrigin}, 204, ""},
		{"GET with Origin: null", "GET", testHost, map[string]string{"Origin": "null"}, 403, "origin_not_allowed"},
		{"POST with Origin: null and the header", "POST", testHost, map[string]string{"Origin": "null", RequestHeader: "1"}, 403, "origin_not_allowed"},
		{"POST without the header", "POST", testHost, nil, 403, "request_header_required"},
		{"POST with the right Origin and without the header", "POST", testHost, map[string]string{"Origin": testOrigin}, 403, "request_header_required"},
		{"POST with the header", "POST", testHost, map[string]string{RequestHeader: "1"}, 204, ""},
		{"PUT without the header", "PUT", testHost, nil, 403, "request_header_required"},
		{"PUT with the header", "PUT", testHost, map[string]string{RequestHeader: "1"}, 204, ""},
		{"DELETE without the header", "DELETE", testHost, nil, 403, "request_header_required"},
		{"DELETE with the header and the right Origin", "DELETE", testHost, map[string]string{RequestHeader: "1", "Origin": testOrigin}, 204, ""},
		{"DELETE from another origin", "DELETE", testHost, map[string]string{RequestHeader: "1", "Origin": "https://evil.example"}, 403, "origin_not_allowed"},
		{"GET for another host", "GET", "evil.example", nil, 421, "host_not_allowed"},
		// The order of the checks: Host, then Origin, then the header.
		{"POST for another host, from another origin, without the header", "POST", "evil.example", map[string]string{"Origin": "null"}, 421, "host_not_allowed"},
		{"POST from another origin, without the header", "POST", testHost, map[string]string{"Origin": "null"}, 403, "origin_not_allowed"},
		// A preflight is a request like any other: no CORS, so no grant.
		{"a CORS preflight", "OPTIONS", testHost, map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "x-vibrance-request"}, 403, "origin_not_allowed"},
		{"a preflight of the same origin", "OPTIONS", testHost, map[string]string{"Origin": testOrigin, "Access-Control-Request-Method": "POST"}, 403, "request_header_required"},
		// The X-Forwarded-* headers are never read (§7.6, T24).
		{"the right host only as X-Forwarded-Host", "GET", "evil.example", map[string]string{"X-Forwarded-Host": testHost, "Forwarded": "host=" + testHost}, 421, "host_not_allowed"},
		{"another host as X-Forwarded-Host", "GET", testHost, map[string]string{"X-Forwarded-Host": "evil.example", "X-Forwarded-Proto": "https", "X-Forwarded-For": "203.0.113.9"}, 204, ""},
		// The name of the header has no case; its value is exact.
		{"the header in lower case", "POST", testHost, map[string]string{"x-vibrance-request": "1"}, 204, ""},
	} {
		req := httptest.NewRequest(tc.method, "/x", nil)
		req.Host = tc.host
		for k, v := range tc.header {
			req.Header.Set(k, v)
		}
		rec := serve(handler, req)
		wantBoundaryHeaders(t, tc.name, rec.Header())
		if tc.code == "" {
			if rec.Code != tc.status {
				t.Errorf("%s: status %d, want %d (%s)", tc.name, rec.Code, tc.status, rec.Body.String())
			}
			continue
		}
		wantError(t, tc.name, rec, tc.status, tc.code)
	}
}

// Boundary refuses an origin the configuration would have refused, without
// repeating it: it may hold a password.
func TestBoundaryRefusesAnOriginThatIsNotOne(t *testing.T) {
	log, _ := newLog()
	for _, origin := range []string{"", "vibrance.test", "http://", "http://vibrance.test/", "http://user:hunter2@vibrance.test",
		"http://vibrance.test?x", "://x", "http://vibrance.test/path"} {
		mw, err := Boundary(log, origin)
		if err == nil || mw != nil {
			t.Errorf("Boundary(%q) = %v, want an error", origin, err)
			continue
		}
		if origin != "" && strings.Contains(err.Error(), "hunter2") {
			t.Errorf("Boundary(%q): the error repeats the value: %v", origin, err)
		}
	}
	for _, origin := range []string{"http://vibrance.test", "https://vibrance.example.net", "http://127.0.0.1:8090", "http://[::1]:8090"} {
		if _, err := Boundary(log, origin); err != nil {
			t.Errorf("Boundary(%q): %v", origin, err)
		}
	}
}

// §7.6: nosniff and no referrer on every response; no-store and the closed
// CSP on JSON only, whoever wrote it, and whatever it set before.
func TestSecurityHeaders(t *testing.T) {
	for _, tc := range []struct {
		name        string
		handler     http.HandlerFunc
		contentType string
		cache       string
		csp         string
	}{
		{"JSON with WriteHeader", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
		}, "application/json", "private, no-store", "default-src 'none'; frame-ancestors 'none'"},
		{"JSON with only a Write", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, "{}")
		}, "application/json", "private, no-store", "default-src 'none'; frame-ancestors 'none'"},
		{"JSON with a charset and a cache of its own", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.WriteHeader(http.StatusOK)
		}, "application/json; charset=utf-8", "private, no-store", "default-src 'none'; frame-ancestors 'none'"},
		{"audio keeps its own cache rule and gets no CSP", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "audio/flac")
			w.Header().Set("Cache-Control", "private, no-cache")
			_, _ = io.WriteString(w, "fLaC")
		}, "audio/flac", "private, no-cache", ""},
		{"a response without a body", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}, "", "", ""},
		{"a handler that writes nothing", func(http.ResponseWriter, *http.Request) {}, "", "", ""},
	} {
		rec := serve(SecurityHeaders(tc.handler), httptest.NewRequest("GET", "/", nil))
		h := rec.Header()
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: nosniff %q, referrer %q", tc.name, h.Get("X-Content-Type-Options"), h.Get("Referrer-Policy"))
		}
		if h.Get("Content-Type") != tc.contentType || h.Get("Cache-Control") != tc.cache || h.Get("Content-Security-Policy") != tc.csp {
			t.Errorf("%s: Content-Type %q, Cache-Control %q, CSP %q; want %q, %q, %q", tc.name,
				h.Get("Content-Type"), h.Get("Cache-Control"), h.Get("Content-Security-Policy"), tc.contentType, tc.cache, tc.csp)
		}
		wantNoCORS(t, tc.name, h)
	}
}

// Every request gets its own UUIDv7, in the context and in the response,
// also when the handler writes the header itself before the response
// begins: the generated code does, with whatever it was given.
func TestRequestID(t *testing.T) {
	log, _ := newLog()
	var inContext []string
	overwrite := func(w http.ResponseWriter, r *http.Request) {
		inContext = append(inContext, requestID(r.Context()))
		w.Header().Set("X-Request-Id", uuid.Nil.String())
	}
	handlers := map[string]http.HandlerFunc{
		"WriteHeader": func(w http.ResponseWriter, r *http.Request) {
			overwrite(w, r)
			w.WriteHeader(http.StatusAccepted)
		},
		"Write alone": func(w http.ResponseWriter, r *http.Request) {
			overwrite(w, r)
			_, _ = io.WriteString(w, "x")
		},
		"nothing written": func(_ http.ResponseWriter, r *http.Request) {
			inContext = append(inContext, requestID(r.Context()))
		},
	}
	seen := map[string]bool{}
	for name, h := range handlers {
		for range 3 {
			inContext = nil
			rec := serve(RequestID(log)(h), httptest.NewRequest("GET", "/", nil))
			ids := rec.Header().Values("X-Request-Id")
			if len(ids) != 1 {
				t.Fatalf("%s: X-Request-Id %q", name, ids)
			}
			id, err := uuid.Parse(ids[0])
			if err != nil || id.Version() != 7 || id.String() != ids[0] {
				t.Errorf("%s: X-Request-Id %q is not a canonical UUIDv7", name, ids[0])
			}
			if len(inContext) != 1 || inContext[0] != ids[0] {
				t.Errorf("%s: the context has %q, the response %q", name, inContext, ids[0])
			}
			if seen[ids[0]] {
				t.Errorf("%s: the id %s was given twice", name, ids[0])
			}
			seen[ids[0]] = true
		}
	}
	if got := requestID(context.Background()); got != "" {
		t.Errorf("a context without a request has the id %q", got)
	}
}

// A client cannot choose the id of its request.
func TestRequestIDIsNotTheClients(t *testing.T) {
	log, _ := newLog()
	req := httptest.NewRequest("GET", "/", nil)
	const sent = "0199a5c8-6d3e-7f10-a2b4-c6d8e0f2a4b6"
	req.Header.Set("X-Request-Id", sent)
	rec := serve(RequestID(log)(ok), req)
	if got := rec.Header().Get("X-Request-Id"); got == sent || got == "" {
		t.Errorf("X-Request-Id %q: the id the client sent was kept", got)
	}
}

// A panic answers 500 internal with nothing of its cause, which is in the
// log with the id of the request; the response has the headers of any other.
func TestPanicAnswersInternalWithoutDetails(t *testing.T) {
	const secret = "open /var/lib/vibrance/vibrance.db: disk on fire"
	for name, value := range map[string]any{
		"a string":      secret,
		"an error":      errors.New(secret),
		"a runtime one": nil, // a nil map write, below
	} {
		log, logs := newLog()
		handler := chain(t, log, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// What the handler prepared must not describe the 500.
			w.Header().Set("Content-Type", "audio/flac")
			w.Header().Set("Content-Length", "123456")
			w.Header().Set("ETag", `"abc"`)
			if value == nil {
				var m map[string]int
				m["x"] = 1
			}
			panic(value)
		}))
		rec := serve(handler, request("GET", "/x?token=querysecret", ""))

		raw := rec.Body.String()
		wantError(t, name, rec, http.StatusInternalServerError, "internal")
		wantBoundaryHeaders(t, name, rec.Header())
		if raw != `{"code":"internal","message":"Something went wrong.","details":{}}`+"\n" {
			t.Errorf("%s: body %q", name, raw)
		}
		for _, h := range []string{"Content-Length", "ETag"} {
			if got := rec.Header().Get(h); got != "" {
				t.Errorf("%s: the 500 still has %s: %q", name, h, got)
			}
		}

		var panicked, accessed map[string]any
		for _, ev := range logs.events(t) {
			switch ev["msg"] {
			case "panic in a request":
				panicked = ev
			case "request":
				accessed = ev
			}
		}
		if panicked == nil || panicked["level"] != "ERROR" || panicked["code"] != "internal" ||
			panicked["request_id"] != rec.Header().Get("X-Request-Id") {
			t.Fatalf("%s: the panic in the log: %v", name, panicked)
		}
		if value != nil && !strings.Contains(fmt.Sprint(panicked["panic"]), secret) {
			t.Errorf("%s: the log lacks the cause: %v", name, panicked["panic"])
		}
		if stack, _ := panicked["stack"].(string); !strings.Contains(stack, "TestPanicAnswersInternalWithoutDetails") {
			t.Errorf("%s: the log lacks the stack of the panic: %q", name, stack)
		}
		// The access log says what the client got.
		if accessed == nil || accessed["status"] != float64(500) || accessed["request_id"] != panicked["request_id"] {
			t.Errorf("%s: the access log of the panic: %v", name, accessed)
		}
		if strings.Contains(logs.String(), "querysecret") {
			t.Errorf("%s: the query string is in the log: %s", name, logs)
		}
	}
}

// A panic once the response has begun cannot become a 500: the connection is
// dropped, so that the client does not take half a response for a whole one.
// http.ErrAbortHandler, the way a handler asks for exactly that, passes
// through without a log line.
func TestPanicAfterTheResponseBegan(t *testing.T) {
	log, logs := newLog()
	begun := chain(t, log, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"half":`)
		panic("late")
	}))
	aborted := chain(t, log, nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	// Called directly, the panic for net/http reaches the caller.
	recovered := func(h http.Handler) (rec *httptest.ResponseRecorder, v any) {
		rec = httptest.NewRecorder()
		defer func() { v = recover() }()
		h.ServeHTTP(rec, request("GET", "/x", ""))
		return rec, nil
	}
	rec, v := recovered(begun)
	if v != http.ErrAbortHandler {
		t.Fatalf("a late panic became %v, want http.ErrAbortHandler", v)
	}
	if rec.Code != http.StatusOK || rec.Body.String() != `{"half":` {
		t.Errorf("the response was changed after it began: %d %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), `"panic":"late"`) {
		t.Errorf("the late panic is not in the log: %s", logs)
	}

	before := logs.String()
	if _, v := recovered(aborted); v != http.ErrAbortHandler {
		t.Fatalf("http.ErrAbortHandler became %v", v)
	}
	if after := strings.TrimPrefix(logs.String(), before); strings.Contains(after, "panic in a request") {
		t.Errorf("an abort was logged as a panic: %s", after)
	}

	// Through a real server: the client sees a broken response, not a
	// complete one, and the server goes on serving.
	srv := httptest.NewServer(begun)
	defer srv.Close()
	for range 2 {
		req, err := http.NewRequest("GET", srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = testHost
		resp, err := srv.Client().Do(req)
		if err != nil {
			continue // the connection was dropped before the head arrived
		}
		_, rerr := io.ReadAll(resp.Body)
		if cerr := resp.Body.Close(); cerr != nil {
			t.Log(cerr)
		}
		if rerr == nil {
			t.Errorf("the client read a half response to its end without an error")
		}
	}
}

// The access log of §11.5: its fields, the route as the pattern the router
// matched, and nothing a client could put a secret in (T28): no path, no
// query string, no Authorization, no Cookie, no body.
func TestAccessLog(t *testing.T) {
	const (
		token  = "vb_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		cookie = "vibrance_session=vb_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	)
	log, logs := newLog()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /things/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "vibrance_session=vb_CCCC; HttpOnly")
		_, _ = io.WriteString(w, `{"a":1}`)
	})
	mux.HandleFunc("POST /things", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(15 * time.Millisecond)
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("GET /quiet/{id}", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "0123456789") })
	mux.Handle("/", NotFound(log))
	handler := chain(t, log, []string{"GET /quiet/{id}"}, mux)

	send := func(method, target, body string) *httptest.ResponseRecorder {
		req := request(method, target, body)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", cookie)
		return serve(handler, req)
	}
	got := []*httptest.ResponseRecorder{
		send("GET", "/things/secret-in-path?password=hunter2&token="+token, ""),
		send("POST", "/things?q=hunter2", `{"password":"hunter2hunter2"}`),
		send("GET", "/quiet/7?v=hunter2", ""),
		send("HEAD", "/things/9", ""),
		send("GET", "/no/such/secret-in-path?password=hunter2", ""),
	}
	// A request refused by the boundary is logged too.
	refused := request("POST", "/things?password=hunter2", "")
	refused.Header.Del(RequestHeader)
	got = append(got, serve(handler, refused))

	want := []struct {
		level  string
		method string
		route  string
		status float64
		bytes  float64
	}{
		{"INFO", "GET", "GET /things/{id}", 200, 7},
		{"INFO", "POST", "POST /things", 201, 0},
		{"DEBUG", "GET", "GET /quiet/{id}", 200, 10},
		{"INFO", "HEAD", "GET /things/{id}", 200, 7},
		{"INFO", "GET", "/", 404, float64(got[4].Body.Len())},
		{"INFO", "POST", "", 403, float64(got[5].Body.Len())},
	}
	events := logs.events(t)
	if len(events) != len(want) {
		t.Fatalf("%d log lines, want %d: %s", len(events), len(want), logs)
	}
	for i, ev := range events {
		w := want[i]
		if ev["msg"] != "request" || ev["level"] != w.level || ev["method"] != w.method || ev["route"] != w.route ||
			ev["status"] != w.status || ev["bytes"] != w.bytes {
			t.Errorf("line %d: %v, want %+v", i, ev, w)
		}
		if ev["request_id"] != got[i].Header().Get("X-Request-Id") || ev["request_id"] == "" {
			t.Errorf("line %d: request_id %v, the response has %q", i, ev["request_id"], got[i].Header().Get("X-Request-Id"))
		}
		if d, isNumber := ev["duration_ms"].(float64); !isNumber || d < 0 || d != float64(int64(d)) {
			t.Errorf("line %d: duration_ms %v, want whole milliseconds", i, ev["duration_ms"])
		}
		// Exactly the fields of §11.5 (user_id arrives with the sessions).
		for key := range ev {
			switch key {
			case "time", "level", "msg", "request_id", "method", "route", "status", "duration_ms", "bytes":
			default:
				t.Errorf("line %d: unexpected field %q", i, key)
			}
		}
	}
	if d := events[1]["duration_ms"].(float64); d < 15 {
		t.Errorf("the duration of a request that took 15 ms is %v ms", d)
	}
	for _, secret := range []string{"hunter2", token, "vb_BBBB", "vb_CCCC", "secret-in-path", "password", "Bearer", "vibrance_session", "?"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("the log holds %q:\n%s", secret, logs)
		}
	}
}

// deadlineRecorder is a ResponseWriter that, like the one of net/http, can
// be given a write deadline and flushed.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
	flushes   int
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.deadlines = append(d.deadlines, t)
	return nil
}

func (d *deadlineRecorder) FlushError() error {
	d.flushes++
	return nil
}

// T12: a handler lifts the write deadline of its request through
// http.ResponseController, which reaches the connection only if every
// wrapper of the ResponseWriter has Unwrap.
func TestEveryWrapperUnwraps(t *testing.T) {
	log, _ := newLog()
	contract := NewContract(testSpec(t), "/api", log)
	mux := http.NewServeMux()
	var controlErr error
	mux.Handle("GET /api/things/{id}", contract.Check(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rc := http.NewResponseController(w)
		controlErr = errors.Join(rc.SetWriteDeadline(time.Time{}), rc.Flush())
		w.WriteHeader(http.StatusNoContent)
	})))
	handler := chain(t, log, nil, mux)

	under := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(under, request("GET", "/api/things/"+someID, ""))
	if controlErr != nil {
		t.Fatalf("the handler could not control its response: %v", controlErr)
	}
	if len(under.deadlines) != 1 || !under.deadlines[0].IsZero() || under.flushes != 1 {
		t.Fatalf("the connection got deadlines %v and %d flushes, want one zero deadline and one flush", under.deadlines, under.flushes)
	}

	// Each wrapper by itself, so that a new one cannot hide behind another.
	for name, w := range map[string]http.ResponseWriter{
		"written":    &written{ResponseWriter: under},
		"identified": &identified{ResponseWriter: under},
		"counted":    &counted{ResponseWriter: under},
		"secured":    &secured{ResponseWriter: under},
	} {
		u, isUnwrapper := w.(interface{ Unwrap() http.ResponseWriter })
		if !isUnwrapper || u.Unwrap() != http.ResponseWriter(under) {
			t.Errorf("%s does not unwrap to the writer it wraps", name)
		}
	}
}

// The same through a real connection, where the deadline is the server's:
// with a write timeout shorter than the response, the response still
// arrives whole because the handler lifted the deadline.
func TestWriteDeadlineIsLiftedThroughTheBoundary(t *testing.T) {
	log, _ := newLog()
	handler := chain(t, log, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
			t.Errorf("lifting the deadline: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		for range 4 {
			time.Sleep(100 * time.Millisecond)
			if _, err := io.WriteString(w, strings.Repeat("x", 64<<10)); err != nil {
				t.Errorf("writing: %v", err)
				return
			}
		}
	}))
	srv := httptest.NewUnstartedServer(handler)
	srv.Config.WriteTimeout = 150 * time.Millisecond
	srv.Start()
	defer srv.Close()

	req, err := http.NewRequest("GET", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = testHost
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if cerr := resp.Body.Close(); err != nil || cerr != nil {
		t.Fatalf("reading the response: %v, %v", err, cerr)
	}
	if len(body) != 4*(64<<10) {
		t.Fatalf("the response has %d bytes, want %d", len(body), 4*(64<<10))
	}
}

// The error map: an *Error is answered as it is, wrapped or not; anything
// else is 500 internal with its cause only in the log.
func TestWriteError(t *testing.T) {
	notFound := &Error{Status: http.StatusNotFound, Code: "track_not_found", Message: "There is no such track."}
	unknown := &Error{Status: http.StatusUnprocessableEntity, Code: "unknown_track", Message: "Some tracks do not exist.",
		Details: map[string]any{"track_ids": []string{someID}}}
	const cause = "SELECT * FROM users: /var/lib/vibrance/vibrance.db is locked"

	for _, tc := range []struct {
		name    string
		err     error
		status  int
		body    string
		logged  bool
		logCode string
	}{
		{"an Error", notFound, 404, `{"code":"track_not_found","message":"There is no such track.","details":{}}`, false, ""},
		{"an Error with details", unknown, 422, `{"code":"unknown_track","message":"Some tracks do not exist.","details":{"track_ids":["` + someID + `"]}}`, false, ""},
		{"a wrapped Error", fmt.Errorf("reading the track: %w", notFound), 404, `{"code":"track_not_found","message":"There is no such track.","details":{}}`, false, ""},
		{"a joined Error", errors.Join(errors.New("first"), notFound), 404, `{"code":"track_not_found","message":"There is no such track.","details":{}}`, false, ""},
		{"an unexpected error", errors.New(cause), 500, `{"code":"internal","message":"Something went wrong.","details":{}}`, true, "internal"},
		{"a 503 of the domain is not a failure to log", &Error{Status: 503, Code: "library_changing", Message: "The library is changing. Try again shortly."},
			503, `{"code":"library_changing","message":"The library is changing. Try again shortly.","details":{}}`, false, ""},
	} {
		log, logs := newLog()
		var rec *httptest.ResponseRecorder
		handler := RequestID(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// What the handler prepared for its success.
			w.Header().Set("ETag", `"abc"`)
			w.Header().Set("Content-Length", "99")
			WriteError(w, r, log, tc.err)
		}))
		rec = serve(handler, httptest.NewRequest("GET", "/x?password=hunter2", nil))

		if rec.Code != tc.status || rec.Body.String() != tc.body+"\n" || rec.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%s: %d %q %q, want %d %q", tc.name, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String(), tc.status, tc.body)
		}
		if rec.Header().Get("ETag") != "" || rec.Header().Get("Content-Length") != "" {
			t.Errorf("%s: the error kept ETag %q, Content-Length %q", tc.name, rec.Header().Get("ETag"), rec.Header().Get("Content-Length"))
		}
		if rec.Header().Get("Cache-Control") != "private, no-store" {
			t.Errorf("%s: Cache-Control %q", tc.name, rec.Header().Get("Cache-Control"))
		}
		if strings.Contains(rec.Body.String(), "vibrance.db") || strings.Contains(rec.Body.String(), "SELECT") {
			t.Errorf("%s: the response carries the cause: %q", tc.name, rec.Body.String())
		}
		events := logs.events(t)
		if !tc.logged {
			if len(events) != 0 {
				t.Errorf("%s: logged %v", tc.name, events)
			}
			continue
		}
		if len(events) != 1 || events[0]["level"] != "ERROR" || events[0]["code"] != tc.logCode || events[0]["err"] != cause ||
			events[0]["request_id"] != rec.Header().Get("X-Request-Id") {
			t.Errorf("%s: the log has %v", tc.name, events)
		}
		if strings.Contains(logs.String(), "hunter2") {
			t.Errorf("%s: the query string is in the log", tc.name)
		}
	}
}

// When the context of the request is over the client is gone: what the
// interrupted work returned is not answered, whatever it looks like, and is
// not a failure of the server.
func TestWriteErrorAfterTheRequestEnded(t *testing.T) {
	for name, err := range map[string]error{
		"the error of the context": context.Canceled,
		"an error that looks like a missing row": fmt.Errorf("%w: %w", context.Canceled,
			&Error{Status: http.StatusNotFound, Code: "track_not_found", Message: "There is no such track."}),
		"a failure of the database": errors.New("database is locked"),
	} {
		log, logs := newLog()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		rec := httptest.NewRecorder()
		WriteError(rec, httptest.NewRequest("GET", "/", nil).WithContext(ctx), log, err)
		wantError(t, name, rec, http.StatusServiceUnavailable, "shutting_down")
		for _, ev := range logs.events(t) {
			if ev["level"] != "DEBUG" {
				t.Errorf("%s: logged %v", name, ev)
			}
		}
	}
}

// An error that arrives when the response has begun is the error of its
// writing: nothing is added to what was sent.
func TestWriteErrorAfterTheResponseBegan(t *testing.T) {
	log, logs := newLog()
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"half":`)
		WriteError(w, r, log, errors.New("write tcp: broken pipe"))
	}))
	rec := serve(handler, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != `{"half":` {
		t.Fatalf("the response became %d %q", rec.Code, rec.Body.String())
	}
	events := logs.events(t)
	if len(events) != 1 || events[0]["level"] != "DEBUG" || events[0]["err"] != "write tcp: broken pipe" {
		t.Fatalf("the log has %v", events)
	}
}

// WriteJSON never sends half a body: a value that cannot be encoded is a
// 500 internal.
func TestWriteJSONThatCannotBeEncoded(t *testing.T) {
	log, logs := newLog()
	rec := httptest.NewRecorder()
	WriteJSON(rec, log, http.StatusOK, map[string]any{"f": func() {}})
	wantError(t, "a function", rec, http.StatusInternalServerError, "internal")
	if events := logs.events(t); len(events) != 1 || events[0]["level"] != "ERROR" {
		t.Fatalf("the log has %v", events)
	}
}

// §8.1: a path that does not exist is 404 not_found and a method a path does
// not take is 405 method_not_allowed with Allow, both in JSON.
func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	log, _ := newLog()
	mux := http.NewServeMux()
	mux.Handle("GET /things", ok)
	mux.Handle("/things", MethodNotAllowed(log, "GET, HEAD"))
	mux.Handle("/", NotFound(log))
	handler := chain(t, log, nil, mux)

	for _, target := range []string{"/nothing", "/things/x", "/thing", "/THINGS", "/things%2F"} {
		rec := serve(handler, request("GET", target+"?q=<script>", ""))
		message := wantError(t, "GET "+target, rec, http.StatusNotFound, "not_found")
		wantBoundaryHeaders(t, "GET "+target, rec.Header())
		if strings.Contains(message, target) || strings.Contains(rec.Body.String(), "script") {
			t.Errorf("GET %s: the 404 repeats the request: %q", target, rec.Body.String())
		}
	}
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		rec := serve(handler, request(method, "/things", ""))
		wantError(t, method+" /things", rec, http.StatusMethodNotAllowed, "method_not_allowed")
		wantBoundaryHeaders(t, method+" /things", rec.Header())
		if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
			t.Errorf("%s /things: Allow %q, want GET, HEAD", method, got)
		}
	}
	for _, method := range []string{"GET", "HEAD"} {
		if rec := serve(handler, request(method, "/things", "")); rec.Code != http.StatusNoContent {
			t.Errorf("%s /things: status %d", method, rec.Code)
		}
	}
}

// The details of an Error are an object in the response even when the
// service gave none.
func TestErrorDetailsAreAlwaysAnObject(t *testing.T) {
	log, _ := newLog()
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest("GET", "/", nil), log, &Error{Status: 409, Code: "last_admin", Message: "m"})
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if string(body["details"]) != "{}" {
		t.Fatalf("details = %s, want {}", body["details"])
	}
	if got := (&Error{Status: 409, Code: "last_admin", Message: "m"}).Error(); got != "409 last_admin: m" {
		t.Fatalf("Error() = %q", got)
	}
}

// Many requests at once: each response has the id of its own request, and
// each line of the access log is that of one of them.
func TestBoundaryUnderConcurrentRequests(t *testing.T) {
	log, logs := newLog()
	handler := chain(t, log, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Seen-Id", requestID(r.Context()))
		if r.URL.Query().Get("panic") != "" {
			panic("boom")
		}
		_, _ = io.WriteString(w, `{}`)
	}))

	const n = 64
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			target := "/x"
			if i%4 == 0 {
				target = "/x?panic=1"
			}
			rec := serve(handler, request("GET", target, ""))
			ids[i] = rec.Header().Get("X-Request-Id")
			if i%4 != 0 && rec.Header().Get("X-Seen-Id") != ids[i] {
				t.Errorf("request %d: the handler saw the id %q, the response has %q", i, rec.Header().Get("X-Seen-Id"), ids[i])
			}
			want := http.StatusOK
			if i%4 == 0 {
				want = http.StatusInternalServerError
			}
			if rec.Code != want {
				t.Errorf("request %d: status %d, want %d", i, rec.Code, want)
			}
		})
	}
	wg.Wait()

	unique := map[string]bool{}
	for _, id := range ids {
		unique[id] = true
	}
	if len(unique) != n {
		t.Fatalf("%d ids for %d requests", len(unique), n)
	}
	logged := map[string]int{}
	for _, ev := range logs.events(t) {
		if ev["msg"] == "request" {
			logged[ev["request_id"].(string)]++
		}
	}
	for _, id := range ids {
		if logged[id] != 1 {
			t.Errorf("the request %s has %d access log lines", id, logged[id])
		}
	}
}
