package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"github.com/getkin/kin-openapi/openapi3"

	"vibrance/internal/auth"
	"vibrance/internal/catalog"
	"vibrance/internal/covers"
	"vibrance/internal/httpx"
	"vibrance/internal/library"
	"vibrance/internal/store"
)

// A UUID in the canonical form, for the ids of the paths.
const someID = "0199a5c0-7b1e-7c3a-9d2f-4b6a8c0e1f23"

// testCost is argon2id with little memory and one pass: the tests make a
// session for every request.
var testCost = auth.Cost{MemoryKiB: 64, Time: 1, Threads: 1}

// testHandler is a router with the operations of the specification the
// binary carries, and its log. The operations are those of a Server on a
// database of its own with one admin, as wrap returns them (nil: as they
// are). Every other path is a 404, as in the server. The checks of Host,
// Origin and X-Vibrance-Request are not here: they are in front of the
// router (internal/app), so a request other than GET and HEAD is given the
// header the boundary would have asked of it. The authentication is: a
// request without credentials of its own is sent with a new session of the
// admin, so that it reaches its operation.
func testHandler(t *testing.T, wrap func(Server) StrictServerInterface) (http.Handler, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	doc, err := LoadSpec()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "vibrance.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	accounts, err := auth.NewService(st, testCost, time.Now, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	const adminPassword = "the password of the admin of the tests"
	if _, err := accounts.CreateUser(t.Context(), "admin", adminPassword, auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	published := &atomic.Pointer[auth.Service]{}
	published.Store(accounts)
	index := &atomic.Pointer[catalog.Service]{}
	index.Store(catalog.New(st, time.Now))
	// The files are in a folder without library/: every track is unknown.
	root, err := library.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	files := &atomic.Pointer[Media]{}
	files.Store(&Media{Files: catalog.NewFiles(root, st, log), Covers: covers.New(root, st, t.TempDir(), log), Recheck: func(string) {}})
	// A scanner that is never run: it is idle, and a cycle asked of it waits.
	scanner := &atomic.Pointer[library.Scanner]{}
	scanner.Store(library.NewScanner(library.NewIndexer(root, st, nil, library.NoCoverWarmer{}, time.Now), 1, time.Hour, log))
	var srv StrictServerInterface = NewServer(published, index, files, scanner, "https://vibrance.example.net")
	if wrap != nil {
		srv = wrap(srv.(Server))
	}
	mux := http.NewServeMux()
	Register(mux, doc, srv, accounts.Middleware(Access(doc), log), log)
	mux.Handle("/", httpx.NotFound(log))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" && r.Header.Get("Cookie") == "" {
			in, err := accounts.CreateToken(r.Context(), "admin", adminPassword, "tests", "")
			if err != nil {
				t.Error(err)
				return
			}
			r.Header.Set("Authorization", "Bearer "+in.Token)
		}
		// The boundary lets no request other than GET and HEAD through
		// without the header, so the operations declare it as required.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			r.Header.Set(httpx.RequestHeader, "1")
		}
		mux.ServeHTTP(w, r)
	}), logs
}

// exampleRequest builds a request for one operation of the specification
// from its examples: ids in the path, the required query parameters, and
// the example of the request schema as the body.
func exampleRequest(t *testing.T, o specOperation) *http.Request {
	t.Helper()
	target := BasePath + strings.NewReplacer("{id}", someID, "{item_id}", someID).Replace(o.path)
	if strings.ContainsAny(target, "{}") {
		t.Fatalf("%s: path parameter without a value in the test: %s", o.op.OperationID, target)
	}
	req := httptest.NewRequest(o.method, target, nil)
	if o.op.RequestBody != nil {
		example := o.op.RequestBody.Value.Content.Get("application/json").Schema.Value.Example
		body, err := json.Marshal(example)
		if err != nil {
			t.Fatalf("%s: marshaling the example: %v", o.op.OperationID, err)
		}
		req = httptest.NewRequest(o.method, target, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	query := req.URL.Query()
	for _, p := range o.op.Parameters {
		if p.Value.In == "query" && p.Value.Required {
			example, ok := p.Value.Example.(string)
			if !ok {
				t.Fatalf("%s: required parameter %q has no example", o.op.OperationID, p.Value.Name)
			}
			query.Set(p.Value.Name, example)
		}
	}
	req.URL.RawQuery = query.Encode()
	return req
}

// wantError checks that a response is the error model of the API with the
// given status and code, and returns its message.
func wantError(t *testing.T, where string, rec *httptest.ResponseRecorder, status int, code string) string {
	t.Helper()
	if rec.Code != status {
		t.Errorf("%s: status %d, want %d (body %q)", where, rec.Code, status, rec.Body.String())
		return ""
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("%s: Content-Type %q, want application/json", where, got)
	}
	dec := json.NewDecoder(rec.Body)
	dec.DisallowUnknownFields()
	var body struct {
		Code    *string         `json:"code"`
		Message *string         `json:"message"`
		Details *map[string]any `json:"details"`
	}
	if err := dec.Decode(&body); err != nil {
		t.Errorf("%s: the body is not {code, message, details}: %v", where, err)
		return ""
	}
	if body.Code == nil || body.Message == nil || body.Details == nil {
		t.Errorf("%s: code, message and details must all be present", where)
		return ""
	}
	if *body.Code != code {
		t.Errorf("%s: code %q, want %q", where, *body.Code, code)
	}
	if *body.Message == "" {
		t.Errorf("%s: the message is empty", where)
	}
	if len(*body.Details) != 0 {
		t.Errorf("%s: details %v, want an empty object", where, *body.Details)
	}
	return *body.Message
}

// Every operation of the specification is routed and implemented: none
// answers 501, and none the 404 not_found of a path the generated router
// does not know.
func TestEveryOperationIsRouted(t *testing.T) {
	doc := loadSpec(t)
	handler, _ := testHandler(t, nil)

	ops := operations(doc)
	if len(ops) != len(designOperations) {
		t.Fatalf("the specification has %d operations, the design %d", len(ops), len(designOperations))
	}
	for _, o := range ops {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, exampleRequest(t, o))
		if rec.Code == http.StatusNotImplemented || rec.Code == http.StatusNotFound && strings.Contains(rec.Body.String(), `"not_found"`) {
			t.Errorf("%s answers %d", o.op.OperationID, rec.Code)
		}
	}
}

// §8.1: HEAD is answered wherever GET is. The router of net/http sends the
// HEAD of a GET pattern to the same operation, which answers as to the GET.
func TestHeadReachesTheOperation(t *testing.T) {
	doc := loadSpec(t)
	handler, _ := testHandler(t, nil)

	heads := 0
	for _, o := range operations(doc) {
		if o.method != http.MethodGet {
			continue
		}
		get := httptest.NewRecorder()
		handler.ServeHTTP(get, exampleRequest(t, o))
		req := exampleRequest(t, o)
		req.Method = http.MethodHead
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != get.Code || rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("HEAD %s: status %d, want the %d of the GET", o.path, rec.Code, get.Code)
		}
		heads++
	}
	if heads == 0 {
		t.Fatal("no GET operation found")
	}
}

// A request the generated code cannot bind answers 400 invalid_request in
// the error model, and never reaches the operation.
func TestMalformedRequests(t *testing.T) {
	doc := loadSpec(t)
	handler, _ := testHandler(t, nil)
	errorSchema := doc.Components.Schemas["Error"].Value

	check := func(where string, req *http.Request) {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		raw := slices.Clone(rec.Body.Bytes())
		message := wantError(t, where, rec, http.StatusBadRequest, "invalid_request")
		// The answer does not repeat what the client sent.
		if strings.Contains(message, "not-a-uuid") || strings.Contains(message, "seven") {
			t.Errorf("%s: the message repeats the input: %q", where, message)
		}
		// I10: it is the Error of the specification.
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Errorf("%s: %v", where, err)
			return
		}
		if err := errorSchema.VisitJSON(value, openapi3.MultiErrors()); err != nil {
			t.Errorf("%s: the body does not match the Error schema: %v", where, err)
		}
	}

	ids, items, bodies := 0, 0, 0
	for _, o := range operations(doc) {
		id := o.op.OperationID
		if strings.Contains(o.path, "{id}") {
			req := exampleRequest(t, o)
			req.URL.Path = BasePath + strings.NewReplacer("{id}", "not-a-uuid", "{item_id}", someID).Replace(o.path)
			check(id+" with an id that is not a UUID", req)
			ids++
		}
		if strings.Contains(o.path, "{item_id}") {
			req := exampleRequest(t, o)
			req.URL.Path = BasePath + strings.NewReplacer("{id}", someID, "{item_id}", "not-a-uuid").Replace(o.path)
			check(id+" with an item id that is not a UUID", req)
			items++
		}
		if o.op.RequestBody != nil {
			req := exampleRequest(t, o)
			req.Body = http.NoBody
			check(id+" without a body", req)

			req = exampleRequest(t, o)
			req.Body = io.NopCloser(strings.NewReader(`{"truncated": `))
			check(id+" with a body that is not JSON", req)
			bodies++
		}
	}
	if ids == 0 || items == 0 || bodies == 0 {
		t.Fatalf("checked %d ids, %d item ids and %d bodies", ids, items, bodies)
	}

	for where, target := range map[string]string{
		"limit that is not a number":   "/artists?limit=seven",
		"search without q":             "/search",
		"artist that is not a UUID":    "/albums?artist=not-a-uuid",
		"a parameter given twice":      "/albums?sort=title&sort=year",
		"search limit not a number":    "/search?q=a&limit=seven",
		"favorites limit not a number": "/me/favorites/tracks?limit=seven",
	} {
		check(where, httptest.NewRequest(http.MethodGet, BasePath+target, nil))
	}
}

// failing is the Server with one operation that fails with an internal
// cause.
type failing struct {
	Server
	err error
}

func (f failing) GetServerInfo(context.Context, GetServerInfoRequestObject) (GetServerInfoResponseObject, error) {
	return nil, f.err
}

// An error the operations do not expect answers 500 internal: the cause is
// in the log and never in the response (§8.1).
func TestUnexpectedErrorHidesItsCause(t *testing.T) {
	const cause = "open /var/lib/vibrance/vibrance.db: disk on fire"
	handler, logs := testHandler(t, func(s Server) StrictServerInterface { return failing{Server: s, err: errors.New(cause)} })

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+"/server", nil))
	raw := rec.Body.String()
	wantError(t, "getServerInfo", rec, http.StatusInternalServerError, "internal")
	for _, fragment := range []string{"vibrance.db", "disk on fire", "/var/lib"} {
		if strings.Contains(raw, fragment) {
			t.Errorf("the response carries the cause: %q", raw)
		}
	}
	if !strings.Contains(logs.String(), cause) || !strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Errorf("the cause is not in the log at ERROR: %q", logs.String())
	}

	// The other operations are untouched.
	logs.Reset()
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+"/admin/library", nil))
	if rec.Code != http.StatusOK || strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Errorf("getLibraryStatus: status %d, log %q", rec.Code, logs.String())
	}
}

// The operations are served under /api/v1 and nowhere else.
func TestOperationsLiveUnderTheBasePath(t *testing.T) {
	handler, _ := testHandler(t, nil)
	for _, target := range []string{"/server", "/api/server", "/api/v2/server", "/api/v1", "/api/v1/", "/api/v1/nothing"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", target, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, BasePath+"/server", nil))
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") == "" {
		t.Errorf("DELETE /server: status %d, Allow %q, want 405 with Allow", rec.Code, rec.Header().Get("Allow"))
	}
}

// The generated interface and the Server have exactly the operations of
// the specification: the operationId with its first letter in upper case.
func TestServerHasTheOperationsOfTheSpecification(t *testing.T) {
	doc := loadSpec(t)

	var want []string
	for _, o := range operations(doc) {
		id := []rune(o.op.OperationID)
		id[0] = unicode.ToUpper(id[0])
		want = append(want, string(id))
	}
	slices.Sort(want)

	for name, typ := range map[string]reflect.Type{
		"StrictServerInterface": reflect.TypeFor[StrictServerInterface](),
		"Server":                reflect.TypeFor[Server](),
	} {
		var got []string
		for i := range typ.NumMethod() {
			got = append(got, typ.Method(i).Name)
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("methods of %s differ from the operations of the specification:\n got  %v\n want %v", name, got, want)
		}
	}
}
