package app

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"

	"vibrance/internal/api"
)

// spec is the specification the binary carries, parsed once for the
// helpers of the tests. It is only read.
var spec = sync.OnceValues(api.LoadSpec)

// The files the API serves are binary strings of the specification:
// kin-openapi checks them as such once it knows their types.
func init() {
	for _, mime := range []string{"audio/flac", "audio/mpeg", "audio/mp4", "image/jpeg", "image/png", "multipart/byteranges"} {
		openapi3filter.RegisterBodyDecoder(mime, openapi3filter.FileBodyDecoder)
	}
}

// assertConforms checks a response of the API against the specification
// (I10), with kin-openapi: the status is one the operation declares, the
// headers it declares as required are there, and the body matches the
// schema of that status. It also checks the headers every response has
// (wantHeaders). req is the request that was answered; the operation is the
// one of the specification its method and path name, and a request that
// names none fails the test. The body of the answer to a HEAD is not
// checked: it has none.
//
// It is the helper of every test of the API: a test that looks at a
// response passes it here. No status is checked by hand: one the
// specification does not declare for the operation, 501 included, fails.
func assertConforms(t *testing.T, where string, req *http.Request, rec *httptest.ResponseRecorder) {
	t.Helper()
	wantHeaders(t, where, rec.Header())
	doc, err := spec()
	if err != nil {
		t.Fatal(err)
	}
	route, ok := routeOf(doc, req)
	if !ok {
		t.Fatalf("%s: %s %s is not an operation of the specification", where, req.Method, req.URL.Path)
	}
	input := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req, Route: route},
		Status:                 rec.Code,
		Header:                 rec.Header(),
		// A status the operation does not declare does not conform.
		Options: &openapi3filter.Options{IncludeResponseStatus: true, ExcludeResponseBody: req.Method == http.MethodHead},
	}
	input.SetBodyBytes(rec.Body.Bytes())
	if err := openapi3filter.ValidateResponse(t.Context(), input); err != nil {
		t.Errorf("%s: the response %d does not conform to the specification: %s\n%s", where, rec.Code, redact(err.Error()), redacted(rec))
	}
}

// tokenText is the form of a token (§7.3).
var tokenText = regexp.MustCompile(`vb_[A-Za-z0-9_-]{43}`)

// redact is s without the tokens it may hold: a message of a test never
// shows one (I5). Passwords are never in a response.
func redact(s string) string { return tokenText.ReplaceAllString(s, "vb_<redacted>") }

// redacted is the body of rec, for a message of a test, without tokens.
func redacted(rec *httptest.ResponseRecorder) string { return redact(rec.Body.String()) }

// routeOf finds the operation of doc that req asks for: the path under
// /api/v1 is compared segment by segment, a {parameter} matching any
// segment, and HEAD is the GET of the path. It is false unless exactly one
// path matches and it has the method.
func routeOf(doc *openapi3.T, req *http.Request) (*routers.Route, bool) {
	path, ok := strings.CutPrefix(req.URL.Path, api.BasePath)
	if !ok {
		return nil, false
	}
	method := req.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	var found *routers.Route
	for template, item := range doc.Paths.Map() {
		op := item.GetOperation(method)
		if op == nil || !matches(template, path) {
			continue
		}
		if found != nil {
			return nil, false
		}
		found = &routers.Route{Spec: doc, Path: template, PathItem: item, Method: method, Operation: op}
	}
	return found, found != nil
}

// matches tells whether path is an instance of the path template of the
// specification.
func matches(template, path string) bool {
	want, got := strings.Split(template, "/"), strings.Split(path, "/")
	if len(want) != len(got) {
		return false
	}
	for i, segment := range want {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			if got[i] == "" {
				return false
			}
			continue
		}
		if segment != got[i] {
			return false
		}
	}
	return true
}

// The helper finds the operation of each path of the specification, and of
// nothing else.
func TestRouteOf(t *testing.T) {
	doc, ops := specOperations(t)
	for _, o := range ops {
		req := o.example(t)
		route, ok := routeOf(doc, req)
		if !ok || route.Operation != o.op {
			t.Errorf("%s %s: found %v", o.method, req.URL.Path, route)
		}
		if o.method == http.MethodGet {
			req.Method = http.MethodHead
			if route, ok := routeOf(doc, req); !ok || route.Operation != o.op {
				t.Errorf("HEAD %s: found %v", req.URL.Path, route)
			}
		}
	}
	for _, target := range []string{"/server", "/api/v1/nothing", "/api/v1/tracks//audio", "/api/v1/tracks/" + someID + "/x"} {
		if route, ok := routeOf(doc, httptest.NewRequest(http.MethodGet, target, nil)); ok {
			t.Errorf("GET %s: found %s", target, route.Path)
		}
	}
	if route, ok := routeOf(doc, httptest.NewRequest(http.MethodPatch, api.BasePath+"/server", nil)); ok {
		t.Errorf("PATCH /server: found %s", route.Path)
	}
}
