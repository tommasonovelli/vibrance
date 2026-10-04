package httpx

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// A UUID in the canonical form, for the ids of the paths.
const someID = "0199a5c0-7b1e-7c3a-9d2f-4b6a8c0e1f23"

// testSpecYAML is a small specification with what the API has: a list with
// an enum and a limit, an id in the path, a closed request schema with
// nested objects and arrays, operations without a body, and a security
// requirement.
const testSpecYAML = `
openapi: 3.0.3
info: {title: test, version: "1"}
security:
  - bearer: []
paths:
  /things:
    get:
      operationId: listThings
      parameters:
        - name: sort
          in: query
          schema: {type: string, enum: [title, year], default: title}
        - name: limit
          in: query
          schema: {type: integer, minimum: 1, maximum: 200, default: 50}
        - name: q
          in: query
          schema: {type: string, minLength: 1, maxLength: 5}
      responses:
        "200": {description: ok}
    post:
      operationId: createThing
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: "#/components/schemas/Thing"}
      responses:
        "201": {description: ok}
  /things/{id}:
    get:
      operationId: getThing
      parameters:
        - $ref: "#/components/parameters/Id"
      responses:
        "200": {description: ok}
    delete:
      operationId: deleteThing
      parameters:
        - $ref: "#/components/parameters/Id"
        - name: If-Match
          in: header
          schema: {type: string, maxLength: 10}
      responses:
        "204": {description: ok}
  /session:
    post:
      operationId: endSession
      responses:
        "204": {description: ok}
components:
  securitySchemes:
    bearer: {type: http, scheme: bearer}
  parameters:
    Id:
      name: id
      in: path
      required: true
      schema:
        type: string
        pattern: '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  schemas:
    Thing:
      type: object
      additionalProperties: false
      required: [name, password]
      properties:
        name: {type: string}
        password: {type: string}
        count: {type: integer, default: 7}
        note: {type: string, nullable: true}
        role: {type: string, enum: [admin, user]}
        tags:
          type: array
          maxItems: 3
          items: {type: string}
        owner:
          type: object
          additionalProperties: false
          properties:
            id: {type: string}
            labels:
              type: array
              items:
                type: object
                additionalProperties: false
                properties:
                  key: {type: string}
`

func testSpec(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData([]byte(testSpecYAML))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return doc
}

// reached is what the handler behind the contract saw.
type reached struct {
	count int
	body  string
	query string
}

// contractHandler serves the test specification under /api, as the
// generated router does: one pattern per operation, each behind Check.
func contractHandler(t *testing.T) (http.Handler, *reached, *logBuffer) {
	t.Helper()
	log, logs := newLog()
	seen := &reached{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("the handler could not read the body: %v", err)
		}
		seen.count++
		seen.body = string(b)
		seen.query = r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	})
	contract := NewContract(testSpec(t), "/api", log)
	mux := http.NewServeMux()
	for _, pattern := range []string{"GET /api/things", "POST /api/things", "GET /api/things/{id}", "DELETE /api/things/{id}", "POST /api/session"} {
		mux.Handle(pattern, contract.Check(next))
	}
	// A route the specification does not have.
	mux.Handle("GET /api/extra", contract.Check(next))
	mux.Handle("/", NotFound(log))
	return chain(t, log, nil, mux), seen, logs
}

func jsonRequest(method, target, body string) *http.Request {
	req := request(method, target, body)
	req.Header.Set("Content-Type", "application/json")
	return req
}

// The secret every body of these tests carries: no answer and no log line
// may repeat it (I5).
const bodySecret = "hunter2-correct-horse"

const validThing = `{"name":"a","password":"` + bodySecret + `"}`

// wantRefused checks a 400 invalid_request that did not reach the handler
// and repeats nothing of the request.
func wantRefused(t *testing.T, where string, rec *httptest.ResponseRecorder, seen *reached, logs *logBuffer) {
	t.Helper()
	wantError(t, where, rec, http.StatusBadRequest, "invalid_request")
	wantBoundaryHeaders(t, where, rec.Header())
	if seen.count != 0 {
		t.Errorf("%s: the handler ran", where)
	}
	for _, leaked := range []string{bodySecret, "hunter2", "wrongvalue"} {
		if strings.Contains(rec.Body.String(), leaked) {
			t.Errorf("%s: the response repeats the request: %q", where, rec.Body.String())
		}
		if strings.Contains(logs.String(), leaked) {
			t.Errorf("%s: the log repeats the request: %s", where, logs)
		}
	}
	for _, ev := range logs.events(t) {
		if ev["level"] == "ERROR" || ev["level"] == "WARN" {
			t.Errorf("%s: a refusal was logged as a failure: %v", where, ev)
		}
	}
}

// §8.1: a body of 1 MiB is read, one byte more is 413 body_too_large,
// whether the request declares its length or not, and before its JSON is
// looked at.
func TestContractBodyLimit(t *testing.T) {
	pad := func(size int) string {
		const head, tail = `{"name":"`, `","password":"` + bodySecret + `"}`
		return head + strings.Repeat("a", size-len(head)-len(tail)) + tail
	}
	exact, over := pad(MaxBodyBytes), pad(MaxBodyBytes+1)
	if len(exact) != 1<<20 || len(over) != 1<<20+1 {
		t.Fatalf("bodies of %d and %d bytes", len(exact), len(over))
	}

	for _, tc := range []struct {
		name    string
		body    string
		chunked bool
		status  int
	}{
		{"1 MiB, declared", exact, false, 204},
		{"1 MiB, chunked", exact, true, 204},
		{"1 MiB + 1 byte, declared", over, false, 413},
		{"1 MiB + 1 byte, chunked", over, true, 413},
		{"1 MiB + 1 byte that is not JSON, declared", strings.Repeat("{", MaxBodyBytes+1), false, 413},
		{"1 MiB + 1 byte that is not JSON, chunked", strings.Repeat("{", MaxBodyBytes+1), true, 413},
		{"16 MiB, chunked", strings.Repeat("a", 16<<20), true, 413},
	} {
		handler, seen, _ := contractHandler(t)
		req := jsonRequest("POST", "/api/things", tc.body)
		if tc.chunked {
			req.ContentLength = -1
		}
		rec := serve(handler, req)
		wantBoundaryHeaders(t, tc.name, rec.Header())
		if tc.status == 204 {
			if rec.Code != 204 || seen.count != 1 || seen.body != tc.body {
				t.Errorf("%s: status %d, handler ran %d times, body intact %v", tc.name, rec.Code, seen.count, seen.body == tc.body)
			}
			continue
		}
		message := wantError(t, tc.name, rec, http.StatusRequestEntityTooLarge, "body_too_large")
		if seen.count != 0 || !strings.Contains(message, "1 MiB") {
			t.Errorf("%s: handler ran %d times, message %q", tc.name, seen.count, message)
		}
	}
}

// countingReader counts the bytes read from it.
type countingReader struct {
	r    io.Reader
	read int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += n
	return n, err
}

// An operation that takes no body does not read one: whatever is sent, of
// any size, is not looked at, and no 400 or 413 the specification does not
// declare for it is answered.
func TestContractLeavesTheBodyOfBodylessOperations(t *testing.T) {
	log, _ := newLog()
	contract := NewContract(testSpec(t), "/api", log)
	for _, tc := range []struct{ method, pattern, target string }{
		{"POST", "POST /api/session", "/api/session"},
		{"DELETE", "DELETE /api/things/{id}", "/api/things/" + someID},
		{"GET", "GET /api/things", "/api/things"},
	} {
		for name, body := range map[string]string{
			"garbage":           "not json at all",
			"duplicate keys":    `{"a":1,"a":2}`,
			"2 MiB":             strings.Repeat("x", 2<<20),
			"2 MiB of brackets": strings.Repeat("[", 2<<20),
		} {
			counter := &countingReader{r: strings.NewReader(body)}
			req := request(tc.method, tc.target, "")
			req.Body = io.NopCloser(counter)
			req.ContentLength = int64(len(body))
			req.Header.Set("Content-Type", "application/json")

			readBefore := -1
			mux := http.NewServeMux()
			mux.Handle(tc.pattern, contract.Check(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				readBefore = counter.read
				w.WriteHeader(http.StatusNoContent)
			})))
			rec := serve(mux, req)
			if rec.Code != http.StatusNoContent || readBefore != 0 {
				t.Errorf("%s with %s: status %d (%s), %d bytes of the body read before the handler",
					tc.pattern, name, rec.Code, rec.Body.String(), readBefore)
			}
		}
	}
}

// §8.1, T23: the body is strict JSON. Duplicate keys at every level and
// more than one value are refused by CheckJSON; unknown keys, wrong types,
// missing fields and null where it is not allowed by the validator, against
// the closed schema of the request.
func TestContractBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		ok   bool
	}{
		{"the smallest valid body", validThing, true},
		{"every field", `{"name":"a","password":"` + bodySecret + `","count":3,"note":null,"role":"admin","tags":["x","y"],"owner":{"id":"o","labels":[{"key":"k"},{"key":"k"}]}}`, true},
		{"white space around", "\n  " + validThing + "\n", true},

		{"a duplicate key at the top", `{"name":"a","password":"` + bodySecret + `","name":"b"}`, false},
		{"the password twice", `{"name":"a","password":"` + bodySecret + `","password":"` + bodySecret + `"}`, false},
		{"a duplicate key in a nested object", `{"name":"a","password":"` + bodySecret + `","owner":{"id":"1","id":"2"}}`, false},
		{"a duplicate key in an object of an array", `{"name":"a","password":"` + bodySecret + `","owner":{"labels":[{"key":"a"},{"key":"a","key":"b"}]}}`, false},
		{"a duplicate key written with an escape", `{"name":"a","password":"` + bodySecret + `","name":"b"}`, false},

		{"an unknown key at the top", `{"name":"a","password":"` + bodySecret + `","extra":"wrongvalue"}`, false},
		{"an unknown key in a nested object", `{"name":"a","password":"` + bodySecret + `","owner":{"extra":"wrongvalue"}}`, false},
		{"an unknown key in an object of an array", `{"name":"a","password":"` + bodySecret + `","owner":{"labels":[{"extra":"wrongvalue"}]}}`, false},
		// encoding/json matches keys whatever their case: the handler would
		// read this one as the password.
		{"a known key in another case", `{"name":"a","Password":"` + bodySecret + `"}`, false},
		{"a known key in another case, next to the right one", `{"name":"a","password":"` + bodySecret + `","PASSWORD":"wrongvalue"}`, false},

		{"two values", validThing + validThing, false},
		{"two values on two lines", validThing + "\n" + validThing, false},
		{"a value and garbage", validThing + " wrongvalue", false},

		{"a number for a string", `{"name":7,"password":"` + bodySecret + `"}`, false},
		{"a string for a number", `{"name":"a","password":"` + bodySecret + `","count":"wrongvalue"}`, false},
		{"a fraction for an integer", `{"name":"a","password":"` + bodySecret + `","count":1.5}`, false},
		{"a string for an array", `{"name":"a","password":"` + bodySecret + `","tags":"wrongvalue"}`, false},
		{"a number in an array of strings", `{"name":"a","password":"` + bodySecret + `","tags":["x",7]}`, false},
		{"an array for an object", `{"name":"a","password":"` + bodySecret + `","owner":["wrongvalue"]}`, false},
		{"null where it is not allowed", `{"name":null,"password":"` + bodySecret + `"}`, false},
		{"an array as the body", `[` + validThing + `]`, false},
		{"a string as the body", `"wrongvalue"`, false},
		{"null as the body", `null`, false},

		{"a missing field", `{"password":"` + bodySecret + `"}`, false},
		{"an empty object", `{}`, false},
		{"an enum with a value it does not have", `{"name":"a","password":"` + bodySecret + `","role":"wrongvalue"}`, false},
		{"an array over its limit", `{"name":"a","password":"` + bodySecret + `","tags":["1","2","3","4"]}`, false},

		{"no body", ``, false},
		{"only white space", "  \n", false},
		{"not JSON", `name=a&password=` + bodySecret, false},
		{"half a body", `{"name":"a","password":"` + bodySecret, false},
		{"bytes that are not UTF-8", "{\"name\":\"\xff\",\"password\":\"" + bodySecret + "\"}", false},
		{"a lone surrogate", `{"name":"\ud800","password":"` + bodySecret + `"}`, false},
		{"nested too deep", `{"name":"a","password":"` + bodySecret + `","owner":` + nested("[", "]", maxJSONDepth) + `}`, false},
	} {
		handler, seen, logs := contractHandler(t)
		rec := serve(handler, jsonRequest("POST", "/api/things", tc.body))
		if tc.ok {
			// The handler reads the body the client sent, byte for byte:
			// the validator fills in no default.
			if rec.Code != http.StatusNoContent || seen.count != 1 || seen.body != tc.body {
				t.Errorf("%s: status %d (%s), handler ran %d times with body %q", tc.name, rec.Code, rec.Body.String(), seen.count, seen.body)
			}
			if strings.Contains(logs.String(), bodySecret) {
				t.Errorf("%s: the log holds the body: %s", tc.name, logs)
			}
			continue
		}
		wantRefused(t, tc.name, rec, seen, logs)
	}
}

// The body must be declared as JSON.
func TestContractContentType(t *testing.T) {
	for _, tc := range []struct {
		contentType string
		ok          bool
	}{
		{"application/json", true},
		{"application/json; charset=utf-8", true},
		{"", false},
		{"text/plain", false},
		{"application/x-www-form-urlencoded", false},
		{"multipart/form-data; boundary=x", false},
		{"application/xml", false},
		{"text/json", false},
		{"application/jsonwrongvalue", false},
		{"wrongvalue", false},
	} {
		handler, seen, logs := contractHandler(t)
		req := request("POST", "/api/things", validThing)
		if tc.contentType != "" {
			req.Header.Set("Content-Type", tc.contentType)
		}
		rec := serve(handler, req)
		if tc.ok {
			if rec.Code != http.StatusNoContent || seen.count != 1 {
				t.Errorf("Content-Type %q: status %d (%s)", tc.contentType, rec.Code, rec.Body.String())
			}
			continue
		}
		wantRefused(t, "Content-Type "+tc.contentType, rec, seen, logs)
	}

	// Whatever the Content-Type says, a body that is read is checked as
	// strict JSON first: no spelling of the header lets a duplicate key
	// through to the handler.
	for _, contentType := range []string{"application/json; =", "application/json;charset=utf-8;x", "APPLICATION/JSON", "application/json ", "text/plain"} {
		handler, seen, logs := contractHandler(t)
		req := request("POST", "/api/things", `{"name":"a","password":"`+bodySecret+`","password":"wrongvalue"}`)
		req.Header.Set("Content-Type", contentType)
		wantRefused(t, "a duplicate key with Content-Type "+contentType, serve(handler, req), seen, logs)
	}
}

// Parameters of the query, of the path and of the headers: an enum with a
// value it does not have, a number out of its limits, an id that is not
// one. The answer names the parameter, never its value.
func TestContractParameters(t *testing.T) {
	for _, tc := range []struct {
		method, target string
		header         map[string]string
		parameter      string // "" when the request is valid
	}{
		{"GET", "/api/things", nil, ""},
		{"GET", "/api/things?sort=year&limit=200&q=abcde", nil, ""},
		{"GET", "/api/things?unknown=wrongvalue", nil, ""}, // a parameter the operation does not have is ignored
		{"GET", "/api/things?sort=wrongvalue", nil, "sort"},
		{"GET", "/api/things?sort=TITLE", nil, "sort"},
		{"GET", "/api/things?sort=", nil, "sort"},
		{"GET", "/api/things?limit=0", nil, "limit"},
		{"GET", "/api/things?limit=201", nil, "limit"},
		{"GET", "/api/things?limit=-1", nil, "limit"},
		{"GET", "/api/things?limit=1.5", nil, "limit"},
		{"GET", "/api/things?limit=wrongvalue", nil, "limit"},
		{"GET", "/api/things?limit=99999999999999999999", nil, "limit"},
		{"GET", "/api/things?q=", nil, "q"},
		{"GET", "/api/things?q=abcdef", nil, "q"},
		{"HEAD", "/api/things?sort=year", nil, ""},
		// A HEAD is checked as the GET it is answered by.
		{"HEAD", "/api/things?sort=wrongvalue", nil, "sort"},
		{"GET", "/api/things/" + someID, nil, ""},
		{"GET", "/api/things/wrongvalue", nil, "id"},
		{"GET", "/api/things/" + strings.ToUpper(someID), nil, "id"},
		{"GET", "/api/things/" + strings.ReplaceAll(someID, "-", ""), nil, "id"},
		{"GET", "/api/things/%7B" + someID + "%7D", nil, "id"},
		{"GET", "/api/things/urn:uuid:" + someID, nil, "id"},
		{"GET", "/api/things/" + someID + "%00", nil, "id"},
		{"GET", "/api/things/..%2F" + someID, nil, "id"},
		{"DELETE", "/api/things/" + someID, map[string]string{"If-Match": "short"}, ""},
		{"DELETE", "/api/things/" + someID, map[string]string{"If-Match": "wrongvalue-too-long"}, "If-Match"},
	} {
		handler, seen, logs := contractHandler(t)
		req := request(tc.method, tc.target, "")
		for k, v := range tc.header {
			req.Header.Set(k, v)
		}
		rec := serve(handler, req)
		where := tc.method + " " + tc.target
		if tc.parameter == "" {
			if rec.Code != http.StatusNoContent || seen.count != 1 {
				t.Errorf("%s: status %d (%s)", where, rec.Code, rec.Body.String())
			}
			// The handler sees the query as it was sent: no default added.
			if _, query, _ := strings.Cut(tc.target, "?"); seen.query != query {
				t.Errorf("%s: the handler saw the query %q", where, seen.query)
			}
			continue
		}
		if tc.method == "HEAD" {
			if rec.Code != http.StatusBadRequest || seen.count != 0 {
				t.Errorf("%s: status %d, handler ran %d times", where, rec.Code, seen.count)
			}
			continue
		}
		wantRefused(t, where, rec, seen, logs)
		if want := `The parameter "` + tc.parameter + `" is not valid.`; !strings.Contains(rec.Body.String(), strings.ReplaceAll(want, `"`, `\"`)) {
			t.Errorf("%s: body %q, want the message %q", where, rec.Body.String(), want)
		}
	}
}

// Authentication is not the validator's: an operation with a security
// requirement passes the contract without credentials, and the middleware of
// the sessions refuses it after.
func TestContractDoesNotAuthenticate(t *testing.T) {
	handler, seen, _ := contractHandler(t)
	if rec := serve(handler, request("POST", "/api/session", "")); rec.Code != http.StatusNoContent || seen.count != 1 {
		t.Fatalf("status %d (%s), handler ran %d times", rec.Code, rec.Body.String(), seen.count)
	}
}

// A route that is served and is not in the specification is refused, not
// served unchecked.
func TestContractRefusesARouteOutsideTheSpecification(t *testing.T) {
	handler, seen, logs := contractHandler(t)
	rec := serve(handler, request("GET", "/api/extra", ""))
	wantError(t, "GET /api/extra", rec, http.StatusInternalServerError, "internal")
	if seen.count != 0 {
		t.Fatal("the handler ran")
	}
	if !strings.Contains(logs.String(), `"level":"ERROR"`) || !strings.Contains(logs.String(), "GET /api/extra") {
		t.Fatalf("the log does not say which route: %s", logs)
	}
}

// The operations are found by the pattern of the router under the base
// path, method and all.
func TestNewContractOperations(t *testing.T) {
	log, _ := newLog()
	contract := NewContract(testSpec(t), "/api", log)
	want := map[string][]string{
		"GET /api/things":         nil,
		"POST /api/things":        nil,
		"GET /api/things/{id}":    {"id"},
		"DELETE /api/things/{id}": {"id"},
		"POST /api/session":       nil,
	}
	if len(contract.operations) != len(want) {
		t.Fatalf("%d operations, want %d", len(contract.operations), len(want))
	}
	for pattern, params := range want {
		op, found := contract.operations[pattern]
		if !found {
			t.Errorf("no operation for %q", pattern)
			continue
		}
		if strings.Join(op.params, ",") != strings.Join(params, ",") {
			t.Errorf("%s: path parameters %v, want %v", pattern, op.params, params)
		}
	}
}

// A body that cannot be read to its end is a 400, not a 500.
func TestContractBodyThatBreaks(t *testing.T) {
	handler, seen, logs := contractHandler(t)
	req := jsonRequest("POST", "/api/things", "")
	req.Body = io.NopCloser(io.MultiReader(bytes.NewReader([]byte(`{"name":"a",`)), brokenReader{}))
	req.ContentLength = -1
	wantRefused(t, "a broken body", serve(handler, req), seen, logs)
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
