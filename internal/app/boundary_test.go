package app

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"vibrance/internal/api"
	"vibrance/internal/auth"
	"vibrance/internal/catalog"
	"vibrance/internal/httpx"
	"vibrance/internal/store"
)

// The public origin of the servers of these tests, and its host.
const (
	apiOrigin = "https://vibrance.example.net"
	apiHost   = "vibrance.example.net"
)

// A UUID in the canonical form, for the ids of the paths.
const someID = "0199a5c0-7b1e-7c3a-9d2f-4b6a8c0e1f23"

// apiHandler is the handler of a server for apiOrigin, as net/http would call
// it, and its log. The server is not started: it has only its service of
// the sessions, on a database of its own, with one admin. A request that
// carries no credentials of its own is sent with a new bearer token of that
// admin, so that it reaches its operation whatever the requests before it
// did (a sign-out revokes its session); one that carries some is sent as it
// is.
func apiHandler(t *testing.T) (http.Handler, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	s := mustServer(t, newLogger(logs), apiOrigin, t.TempDir(), t.TempDir())
	sessions := publishSessions(t, s)
	h := s.http.Handler
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" && r.Header.Get("Cookie") == "" {
			r.Header.Set("Authorization", "Bearer "+adminToken(t, sessions))
		}
		h.ServeHTTP(w, r)
	}), logs
}

// adminToken signs the first admin in with a new token.
func adminToken(t *testing.T, sessions *auth.Service) string {
	t.Helper()
	in, err := sessions.CreateToken(t.Context(), adminName, adminPassword, "tests", "")
	if err != nil {
		t.Fatal(err)
	}
	return in.Token
}

// publishSessions gives s, which is not started, the service of the
// sessions and the catalog that its startup would publish, on a new
// database with one admin and an empty index, and returns the first. What
// the service logs is not in the log of s.
func publishSessions(t *testing.T, s *server) *auth.Service {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), databaseFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	sessions, err := auth.NewService(st, testCost, time.Now, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.Bootstrap(t.Context(), env(adminEnv)); err != nil {
		t.Fatal(err)
	}
	s.catalog.Store(catalog.New(st))
	s.sessions.Store(sessions)
	return sessions
}

// operation is one operation of the specification.
type operation struct {
	method string
	path   string
	item   *openapi3.PathItem
	op     *openapi3.Operation
}

// specOperations lists the operations of the specification the binary
// carries, ordered by path and method.
func specOperations(t *testing.T) (*openapi3.T, []operation) {
	t.Helper()
	doc, err := api.LoadSpec()
	if err != nil {
		t.Fatal(err)
	}
	var ops []operation
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			ops = append(ops, operation{method: method, path: path, item: item, op: op})
		}
	}
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].path != ops[j].path {
			return ops[i].path < ops[j].path
		}
		return ops[i].method < ops[j].method
	})
	if len(ops) != 37 {
		t.Fatalf("the specification has %d operations, want the 37 of DESIGN.md §8.3", len(ops))
	}
	return doc, ops
}

// example builds a request the boundary accepts for one operation, from the
// examples of the specification: ids in the path, the required query
// parameters, the example of the request schema as the body, the right Host
// and, where the method needs it, X-Vibrance-Request.
func (o operation) example(t *testing.T) *http.Request {
	t.Helper()
	target := api.BasePath + strings.NewReplacer("{id}", someID, "{item_id}", someID).Replace(o.path)
	var body io.Reader
	if o.op.RequestBody != nil {
		b, err := json.Marshal(o.op.RequestBody.Value.Content.Get("application/json").Schema.Value.Example)
		if err != nil {
			t.Fatalf("%s: marshaling the example: %v", o.op.OperationID, err)
		}
		body = bytes.NewReader(b)
	}
	req := httptest.NewRequest(o.method, target, body)
	req.Host = apiHost
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if o.method != http.MethodGet {
		req.Header.Set(httpx.RequestHeader, "1")
	}
	query := req.URL.Query()
	for _, p := range o.op.Parameters {
		if p.Value.In == "query" && p.Value.Required {
			query.Set(p.Value.Name, p.Value.Example.(string))
		}
	}
	req.URL.RawQuery = query.Encode()
	return req
}

func send(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// wantHeaders checks what every response of the server has (§7.6, §8.1):
// the id of the request, a UUIDv7; nosniff and no referrer; on JSON,
// no-store and the closed CSP; and never a CORS header (I4).
func wantHeaders(t *testing.T, where string, h http.Header) {
	t.Helper()
	ids := h.Values("X-Request-Id")
	if len(ids) != 1 {
		t.Errorf("%s: X-Request-Id %q, want exactly one", where, ids)
	} else if id, err := uuid.Parse(ids[0]); err != nil || id.Version() != 7 || id.String() != ids[0] {
		t.Errorf("%s: X-Request-Id %q is not a UUIDv7 in canonical form", where, ids[0])
	}
	if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("%s: X-Content-Type-Options %q, Referrer-Policy %q", where, h.Get("X-Content-Type-Options"), h.Get("Referrer-Policy"))
	}
	if h.Get("Content-Type") == "application/json" {
		if h.Get("Cache-Control") != "private, no-store" || h.Get("Content-Security-Policy") != "default-src 'none'; frame-ancestors 'none'" {
			t.Errorf("%s: on JSON, Cache-Control %q and Content-Security-Policy %q", where, h.Get("Cache-Control"), h.Get("Content-Security-Policy"))
		}
	}
	for name := range h {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			t.Errorf("%s: the response has the CORS header %s", where, name)
		}
	}
}

// errorBody decodes a response in the error model, exactly: code, message
// and details, and nothing else.
func errorBody(t *testing.T, where string, rec *httptest.ResponseRecorder) (code, message string) {
	t.Helper()
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
	if err := dec.Decode(&body); err != nil || body.Code == nil || body.Message == nil || body.Details == nil {
		t.Errorf("%s: the body is not {code, message, details}: %v (%q)", where, err, redacted(rec))
		return "", ""
	}
	return *body.Code, *body.Message
}

// wantCode checks the status and the code of an error.
func wantCode(t *testing.T, where string, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("%s: status %d, want %d (%s)", where, rec.Code, status, redacted(rec))
		return
	}
	if got, _ := errorBody(t, where, rec); got != code {
		t.Errorf("%s: code %q, want %q", where, got, code)
	}
}

// refusals are the codes with which the boundary, the validation and the
// authentication refuse a request before its operation runs.
var refusals = []string{"host_not_allowed", "origin_not_allowed", "request_header_required", "body_too_large",
	"invalid_request", "login_required", "forbidden", "not_found", "method_not_allowed", "not_ready", "shutting_down"}

// wantReached checks that a request reached its operation: whatever the
// operation answered, it is not a refusal of what is in front of it, nor
// a failure.
func wantReached(t *testing.T, where string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code >= 500 && rec.Code != http.StatusNotImplemented {
		t.Errorf("%s: status %d (%s)", where, rec.Code, redacted(rec))
		return
	}
	if rec.Code >= 400 {
		if code, _ := errorBody(t, where, rec); slices.Contains(refusals, code) {
			t.Errorf("%s: refused with %d %s before the operation", where, rec.Code, code)
		}
	}
}

// Every operation of the specification is behind the whole boundary, and
// every answer of the boundary is one the specification declares for that
// operation (I10): the refusals of Host, Origin and X-Vibrance-Request, the
// body limit, strict JSON and the validation.
func TestEveryOperationBehindTheBoundary(t *testing.T) {
	_, ops := specOperations(t)
	handler, logs := apiHandler(t)

	check := func(o operation, what string, req *http.Request, status int, code string) {
		t.Helper()
		where := o.op.OperationID + ": " + what
		rec := send(handler, req)
		if status == 0 {
			wantReached(t, where, rec)
		} else {
			wantCode(t, where, rec, status, code)
		}
		assertConforms(t, where, req, rec)
	}
	ids, bodies, writes := 0, 0, 0
	for _, o := range ops {
		// As it should be asked: it reaches the operation.
		check(o, "a valid request", o.example(t), 0, "")

		req := o.example(t)
		req.Host = "evil.example"
		check(o, "another Host", req, http.StatusMisdirectedRequest, "host_not_allowed")

		req = o.example(t)
		req.Host = "127.0.0.1:8080"
		check(o, "the address of the container as Host", req, http.StatusMisdirectedRequest, "host_not_allowed")

		req = o.example(t)
		req.Header.Set("Origin", "null")
		check(o, "Origin: null", req, http.StatusForbidden, "origin_not_allowed")

		req = o.example(t)
		req.Header.Set("Origin", "https://evil.example")
		check(o, "another Origin", req, http.StatusForbidden, "origin_not_allowed")

		req = o.example(t)
		req.Header.Set("Origin", apiOrigin)
		check(o, "the right Origin", req, 0, "")

		if o.method != http.MethodGet {
			writes++
			req = o.example(t)
			req.Header.Del(httpx.RequestHeader)
			check(o, "without X-Vibrance-Request", req, http.StatusForbidden, "request_header_required")

			req = o.example(t)
			req.Header.Set(httpx.RequestHeader, "true")
			check(o, "X-Vibrance-Request: true", req, http.StatusForbidden, "request_header_required")
		}
		if strings.Contains(o.path, "{id}") {
			ids++
			req = o.example(t)
			req.URL.Path = strings.Replace(req.URL.Path, someID, strings.ToUpper(someID), 1)
			check(o, "an id in upper case", req, http.StatusBadRequest, "invalid_request")

			req = o.example(t)
			req.URL.Path = strings.Replace(req.URL.Path, someID, "1", 1)
			check(o, "an id that is not a UUID", req, http.StatusBadRequest, "invalid_request")
		}
		if o.op.RequestBody != nil {
			bodies++
			req = o.example(t)
			req.Body = io.NopCloser(strings.NewReader(`{"a":1,"a":2}`))
			check(o, "a duplicate key", req, http.StatusBadRequest, "invalid_request")

			req = o.example(t)
			req.Body = io.NopCloser(strings.NewReader(`{"unknown":true}`))
			check(o, "an unknown key", req, http.StatusBadRequest, "invalid_request")

			req = o.example(t)
			req.Header.Set("Content-Type", "text/plain")
			check(o, "a body that is not declared as JSON", req, http.StatusBadRequest, "invalid_request")

			req = o.example(t)
			req.Body = http.NoBody
			req.ContentLength = 0
			check(o, "no body", req, http.StatusBadRequest, "invalid_request")

			req = o.example(t)
			req.Body = io.NopCloser(strings.NewReader(strings.Repeat(" ", httpx.MaxBodyBytes) + "1"))
			req.ContentLength = -1
			check(o, "a body of 1 MiB + 1 byte", req, http.StatusRequestEntityTooLarge, "body_too_large")
		}
	}
	if ids == 0 || bodies != 10 || writes == 0 {
		t.Fatalf("checked %d operations with an id, %d with a body, %d that write", ids, bodies, writes)
	}
	for _, ev := range logs.events(t) {
		if ev["msg"] != "request" {
			t.Fatalf("the boundary logged more than the accesses: %v", ev)
		}
	}
}

// HEAD is answered wherever GET is, behind the same boundary, as the GET is;
// its answers have the headers of a GET and nothing a HEAD must not have.
func TestHeadBehindTheBoundary(t *testing.T) {
	_, ops := specOperations(t)
	handler, _ := apiHandler(t)
	for _, o := range ops {
		if o.method != http.MethodGet {
			continue
		}
		get := send(handler, o.example(t))
		req := o.example(t)
		req.Method = http.MethodHead
		rec := send(handler, req)
		// The recorder keeps what the handler wrote; net/http sends no body
		// with the answer to a HEAD (TestRoutes asks one over the network).
		if rec.Code != get.Code {
			t.Errorf("HEAD %s: status %d, want the %d of the GET", o.path, rec.Code, get.Code)
		}
		assertConforms(t, "HEAD "+o.path, req, rec)

		req = o.example(t)
		req.Method = http.MethodHead
		req.Host = "evil.example"
		if rec := send(handler, req); rec.Code != http.StatusMisdirectedRequest {
			t.Errorf("HEAD %s for another host: status %d, want 421", o.path, rec.Code)
		}
	}
}

// §7.6, §11.3: the health endpoints are outside the checks of Host, Origin
// and X-Vibrance-Request, because a probe asks the container by its address;
// everything else is behind them.
func TestHealthIsOutsideTheChecks(t *testing.T) {
	handler, _ := apiHandler(t)
	for _, path := range []string{livePath, readyPath} {
		for _, host := range []string{apiHost, "127.0.0.1:8080", "evil.example", ""} {
			req := httptest.NewRequest("GET", path, nil)
			req.Host = host
			req.Header.Set("Origin", "null")
			rec := send(handler, req)
			where := "GET " + path + " with Host " + host
			wantHeaders(t, where, rec.Header())
			if path == livePath && (rec.Code != 200 || rec.Body.String() != liveJSON+"\n") {
				t.Errorf("%s: %d %q", where, rec.Code, rec.Body.String())
			}
			// The server of this test was never started.
			if path == readyPath && (rec.Code != 503 || rec.Body.String() != notReadyJSON+"\n") {
				t.Errorf("%s: %d %q", where, rec.Code, rec.Body.String())
			}
		}
		// A method they do not take is a 405, not the 403 of a request
		// without X-Vibrance-Request.
		for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
			req := httptest.NewRequest(method, path, nil)
			req.Host = "evil.example"
			rec := send(handler, req)
			wantCode(t, method+" "+path, rec, http.StatusMethodNotAllowed, "method_not_allowed")
			wantHeaders(t, method+" "+path, rec.Header())
			if rec.Header().Get("Allow") != "GET, HEAD" {
				t.Errorf("%s %s: Allow %q", method, path, rec.Header().Get("Allow"))
			}
		}
	}

	// A path that only looks like one of the two is behind the checks.
	for _, path := range []string{"/health", "/health/", "/health/live/", "/health/ready/x", "/health/live%2F", "/", "/api/v1/server", "/nothing"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "evil.example"
		rec := send(handler, req)
		wantCode(t, "GET "+path+" for another host", rec, http.StatusMisdirectedRequest, "host_not_allowed")
		wantHeaders(t, "GET "+path, rec.Header())
	}
}

// §8.1: outside the operations, a path that does not exist is a JSON 404 and
// a method a path does not take a JSON 405 with Allow. A path that is not in
// its canonical form is a 404 too: the router is never left to redirect.
func TestPathsOutsideTheOperations(t *testing.T) {
	handler, _ := apiHandler(t)
	get := func(method, target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/", nil)
		// The target as the client wrote it, not cleaned.
		req.URL.Path, req.URL.RawPath = target, ""
		if path, query, found := strings.Cut(target, "?"); found {
			req.URL.Path, req.URL.RawQuery = path, query
		}
		req.RequestURI = target
		req.Host = apiHost
		if method != "GET" && method != "HEAD" {
			req.Header.Set(httpx.RequestHeader, "1")
		}
		return send(handler, req)
	}

	for _, target := range []string{
		"/nothing", "/index.html", "/api", "/api/", "/api/v1", "/api/v1/", "/api/v2/server", "/api/v1/nothing",
		"/health", "/health/", "/health/live/", "/health/ready/x", "/favicon.ico", "/api/v1/server/",
		// Not canonical: the router of net/http would answer 301 to the
		// cleaned path.
		"//", "//api/v1/server", "/api//v1/server", "/api/v1/./server", "/api/v1/x/../server", "/./", "/../", "/health//live",
		"/api/v1/server/.", "/api/v1/server/..",
	} {
		for _, method := range []string{"GET", "POST"} {
			rec := get(method, target)
			where := method + " " + target
			wantCode(t, where, rec, http.StatusNotFound, "not_found")
			wantHeaders(t, where, rec.Header())
			if rec.Header().Get("Location") != "" {
				t.Errorf("%s: redirected to %q", where, rec.Header().Get("Location"))
			}
		}
	}

	for _, method := range []string{"GET", "HEAD"} {
		rec := get(method, "/?x=1")
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/api/docs" {
			t.Errorf("%s /: %d to %q, want 302 to /api/docs", method, rec.Code, rec.Header().Get("Location"))
		}
		wantHeaders(t, method+" /", rec.Header())
	}
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		rec := get(method, "/")
		wantCode(t, method+" /", rec, http.StatusMethodNotAllowed, "method_not_allowed")
		if rec.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s /: Allow %q", method, rec.Header().Get("Allow"))
		}
		rec = get(method, api.BasePath+"/server")
		wantCode(t, method+" /api/v1/server", rec, http.StatusMethodNotAllowed, "method_not_allowed")
		wantHeaders(t, method+" /api/v1/server", rec.Header())
	}
}

// §11.5: the access log is at INFO, but for the audio, the covers and the
// health, which are asked all the time.
func TestAccessLogLevels(t *testing.T) {
	handler, logs := apiHandler(t)
	for _, target := range []string{
		api.BasePath + "/tracks/" + someID + "/audio",
		api.BasePath + "/albums/" + someID + "/cover",
		livePath,
		readyPath,
		api.BasePath + "/tracks/" + someID,
		api.BasePath + "/tracks/" + someID + "/lyrics",
		"/",
		"/nothing",
	} {
		req := httptest.NewRequest("GET", target, nil)
		req.Host = apiHost
		send(handler, req)
	}
	want := []struct{ level, route string }{
		{"DEBUG", "GET /api/v1/tracks/{id}/audio"},
		{"DEBUG", "GET /api/v1/albums/{id}/cover"},
		{"DEBUG", "GET /health/live"},
		{"DEBUG", "GET /health/ready"},
		{"INFO", "GET /api/v1/tracks/{id}"},
		{"INFO", "GET /api/v1/tracks/{id}/lyrics"},
		{"INFO", "GET /{$}"},
		{"INFO", "/"},
	}
	events := logs.events(t)
	if len(events) != len(want) {
		t.Fatalf("%d log lines, want %d:\n%s", len(events), len(want), logs)
	}
	for i, ev := range events {
		if ev["msg"] != "request" || ev["level"] != want[i].level || ev["route"] != want[i].route {
			t.Errorf("line %d: %v, want %+v", i, ev, want[i])
		}
	}
}

// T28: the log of a whole exchange with the server holds none of the
// secrets that crossed it: no Authorization, no Cookie, no body of a
// sign-in, no query string, no path.
func TestLogHoldsNoSecrets(t *testing.T) {
	const (
		password = "correct horse battery staple"
		token    = "vb_QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU2"
		session  = "vb_c2Vzc2lvbi1jb29raWUtc2VjcmV0LXZhbHVlLTAxMjM"
	)
	handler, logs := apiHandler(t)
	do := func(method, target, body string, header map[string]string) {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Host = apiHost
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "vibrance_session="+session)
		if method != "GET" && method != "HEAD" {
			req.Header.Set(httpx.RequestHeader, "1")
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range header {
			if k == "Host" {
				req.Host = v
				continue
			}
			req.Header.Set(k, v)
		}
		send(handler, req)
	}
	login := `{"username":"anna","password":"` + password + `"}`
	do("POST", "/api/v1/auth/login", login, nil)
	do("POST", "/api/v1/auth/login", `{"username":"anna","password":"`+password+`","password":"`+password+`"}`, nil)
	do("POST", "/api/v1/auth/login", `{"username":"anna","password":"`+password+`","extra":"`+password+`"}`, nil)
	do("POST", "/api/v1/auth/login", `{"username":"anna","password":["`+password+`"]}`, nil)
	do("POST", "/api/v1/auth/login", login, map[string]string{"Content-Type": "text/" + password})
	do("POST", "/api/v1/auth/tokens", `{"username":"anna","password":"`+password+`","device_name":"phone"}`, nil)
	do("PUT", "/api/v1/me/password", `{"current_password":"`+password+`","new_password":"`+password+`"}`, nil)
	do("GET", "/api/v1/search?q=private-search-words&token="+token, "", nil)
	do("GET", "/api/v1/albums?sort="+token, "", nil)
	do("GET", "/api/v1/tracks/"+token, "", nil)
	do("GET", "/api/v1/tracks/"+someID+"/audio?access_token="+token, "", nil)
	do("GET", "/"+token+"?password="+url.QueryEscape(password), "", nil)
	do("GET", "/health/ready?token="+token, "", nil)
	do("GET", "/api/v1/me", "", map[string]string{"Host": "evil.example"})
	do("POST", "/api/v1/auth/logout", "", map[string]string{"Origin": "https://" + token + ".example"})
	do("DELETE", "/api/v1/me/sessions/"+someID, "", map[string]string{httpx.RequestHeader: token})
	do("DELETE", "/api/v1/playlists/"+someID, "", map[string]string{"If-Match": token})

	text := logs.String()
	if len(logs.events(t)) != 17 {
		t.Fatalf("%d log lines, want one per request", len(logs.events(t)))
	}
	// The message names the forbidden value by its position: printing it, or
	// the log that holds it, would put a credential in the output of the
	// tests (I5).
	for i, secret := range []string{password, "correct", "battery", token, session, "Bearer", "vibrance_session", "private-search-words", "anna", "?", "evil"} {
		if strings.Contains(text, secret) {
			t.Errorf("the log holds forbidden value #%d", i)
		}
	}
	for _, ev := range logs.events(t) {
		for key := range ev {
			switch key {
			case "time", "level", "msg", "request_id", "method", "route", "status", "duration_ms", "bytes":
			default:
				t.Errorf("the access log has the field %q", key)
			}
		}
	}
}

// The boundary through a real connection, as a client meets it: the Host of
// the request is the address the server listens on only because the test
// configured that as its public origin.
func TestBoundaryOverTheNetwork(t *testing.T) {
	r := startServer(t, nil)
	base := "http://" + r.addr
	waitReady(t, base)

	type answer struct {
		status int
		header http.Header
		body   string
	}
	ask := func(method, path, host string, header map[string]string, body string) answer {
		t.Helper()
		req, err := http.NewRequest(method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		req.Close = true
		for k, v := range header {
			req.Header.Set(k, v)
		}
		client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, rerr := io.ReadAll(resp.Body)
		if cerr := resp.Body.Close(); rerr != nil || cerr != nil {
			t.Fatal(rerr, cerr)
		}
		return answer{resp.StatusCode, resp.Header, string(b)}
	}
	code := func(a answer) string {
		var body struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal([]byte(a.body), &body); err != nil {
			t.Errorf("the body is not JSON: %q", a.body)
		}
		return body.Code
	}
	write := map[string]string{httpx.RequestHeader: "1", "Content-Type": "application/json"}

	for _, tc := range []struct {
		name         string
		method, path string
		host         string
		header       map[string]string
		body         string
		status       int
		code         string
	}{
		{"an operation", "GET", "/api/v1/server", r.addr, nil, "", 200, ""},
		{"an operation for another host", "GET", "/api/v1/server", "evil.example", nil, "", 421, "host_not_allowed"},
		{"an operation for the host without its port", "GET", "/api/v1/server", "127.0.0.1", nil, "", 421, "host_not_allowed"},
		{"an operation from Origin: null", "GET", "/api/v1/server", r.addr, map[string]string{"Origin": "null"}, "", 403, "origin_not_allowed"},
		{"an operation from its own origin", "GET", "/api/v1/server", r.addr, map[string]string{"Origin": base}, "", 200, ""},
		{"a sign-in without the header", "POST", "/api/v1/auth/login", r.addr, map[string]string{"Content-Type": "application/json"}, `{"username":"anna","password":"correct horse battery"}`, 403, "request_header_required"},
		{"a sign-in of nobody", "POST", "/api/v1/auth/login", r.addr, write, `{"username":"anna","password":"correct horse battery"}`, 401, "invalid_credentials"},
		{"a sign-in with the password twice", "POST", "/api/v1/auth/login", r.addr, write, `{"username":"anna","password":"a","password":"b"}`, 400, "invalid_request"},
		{"a sign-in with an unknown key", "POST", "/api/v1/auth/login", r.addr, write, `{"username":"anna","password":"a","admin":true}`, 400, "invalid_request"},
		{"a sign-in of 1 MiB + 1 byte", "POST", "/api/v1/auth/login", r.addr, write, strings.Repeat(" ", httpx.MaxBodyBytes) + "1", 413, "body_too_large"},
		{"an enum with a value it does not have", "GET", "/api/v1/albums?sort=size", r.addr, nil, "", 400, "invalid_request"},
		{"a path that does not exist", "GET", "/api/v1/nothing", r.addr, nil, "", 404, "not_found"},
		{"a method the path does not take", "DELETE", "/api/v1/server", r.addr, write, "", 405, "method_not_allowed"},
		{"the health for another host", "GET", "/health/live", "evil.example", nil, "", 200, ""},
	} {
		got := ask(tc.method, tc.path, tc.host, tc.header, tc.body)
		wantHeaders(t, tc.name, got.header)
		if got.status != tc.status || (tc.code != "" && code(got) != tc.code) {
			t.Errorf("%s: %d %q, want %d %s", tc.name, got.status, got.body, tc.status, tc.code)
		}
	}

	if err := r.stop(t); err != nil {
		t.Fatal(err)
	}
	refused := 0
	for _, ev := range r.logs.events(t) {
		if ev["msg"] == "sign-in refused" && ev["level"] == "WARN" {
			// The refused sign-in of anna, who has no account.
			refused++
			continue
		}
		if ev["level"] == "ERROR" || ev["level"] == "WARN" {
			t.Errorf("the run logged %v", ev)
		}
	}
	if refused != 1 {
		t.Errorf("%d refused sign-ins logged, want 1", refused)
	}
}
