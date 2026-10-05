package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	apispec "vibrance/api"
	"vibrance/internal/httpx"
)

// The specification the binary carries is the file of the repository: what
// the server validates against, and what the tests of the specification
// read.
func TestEmbeddedSpecIsTheFile(t *testing.T) {
	file, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	if apispec.YAML != string(file) {
		t.Fatal("the embedded specification differs from api/openapi.yaml")
	}
	doc, err := LoadSpec()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(operations(doc)), len(designOperations); got != want {
		t.Fatalf("the embedded specification has %d operations, want %d", got, want)
	}
}

// §8.1: a path of the specification asked with a method it does not have
// answers 405 method_not_allowed in the error model, with the methods it
// has in Allow; with one it has, the operation answers.
func TestMethodNotAllowed(t *testing.T) {
	doc := loadSpec(t)
	handler, _ := testHandler(t, nil)

	paths, refused := 0, 0
	for path, item := range doc.Paths.Map() {
		paths++
		var has []string
		for method := range item.Operations() {
			has = append(has, method)
			if method == http.MethodGet {
				has = append(has, http.MethodHead)
			}
		}
		slices.Sort(has)
		target := BasePath + strings.NewReplacer("{id}", someID, "{item_id}", someID).Replace(path)
		for _, method := range []string{"GET", "HEAD", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "TRACE"} {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
			where := method + " " + path
			if slices.Contains(has, method) {
				// A 404 of the operation (a user that does not exist) is an
				// answer of the operation; the 404 of the router says not_found.
				if rec.Code == http.StatusMethodNotAllowed || strings.Contains(rec.Body.String(), `"code":"not_found"`) {
					t.Errorf("%s: status %d for a method the path has", where, rec.Code)
				}
				continue
			}
			refused++
			wantError(t, where, rec, http.StatusMethodNotAllowed, "method_not_allowed")
			if got, want := rec.Header().Get("Allow"), strings.Join(has, ", "); got != want {
				t.Errorf("%s: Allow %q, want %q", where, got, want)
			}
		}
	}
	if paths == 0 || refused == 0 {
		t.Fatalf("%d paths, %d refusals", paths, refused)
	}
}

// §8.1: a path that is not in the specification answers 404 not_found in
// the error model, whatever the method.
func TestUnknownPaths(t *testing.T) {
	handler, _ := testHandler(t, nil)
	for _, target := range []string{"/server", "/api/server", "/api/v2/server", "/api/v1", "/api/v1/", "/api/v1/nothing",
		"/api/v1/server/", "/api/v1/tracks", "/api/v1/tracks/" + someID + "/audio/x", "/api/v1/Server"} {
		for _, method := range []string{"GET", "POST", "DELETE"} {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
			wantError(t, method+" "+target, rec, http.StatusNotFound, "not_found")
		}
	}
}

// An id has one spelling, the canonical one: lowercase, with hyphens. The
// other forms a UUID parser reads (upper case, no hyphens, braces, a URN)
// answer 400 invalid_request, in a path, in the query and in a body.
func TestIdsHaveOneSpelling(t *testing.T) {
	doc := loadSpec(t)
	handler, _ := testHandler(t, nil)

	spellings := map[string]string{
		"upper case":        strings.ToUpper(someID),
		"mixed case":        strings.ToUpper(someID[:8]) + someID[8:],
		"without hyphens":   strings.ReplaceAll(someID, "-", ""),
		"in braces":         "{" + someID + "}",
		"as a URN":          "urn:uuid:" + someID,
		"with a space":      someID + " ",
		"one character off": someID[:35],
		"not hexadecimal":   "g" + someID[1:],
	}
	check := func(where string, req *http.Request, name string) {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		message := wantError(t, where, rec, http.StatusBadRequest, "invalid_request")
		if name != "" && !strings.Contains(message, `"`+name+`"`) {
			t.Errorf("%s: the message %q does not name the parameter %q", where, message, name)
		}
	}

	paths := 0
	for _, o := range operations(doc) {
		for spelling, id := range spellings {
			escaped := strings.NewReplacer("{", "%7B", "}", "%7D", " ", "%20").Replace(id)
			if strings.Contains(o.path, "{id}") {
				req := exampleRequest(t, o)
				req.URL = mustParse(t, BasePath+strings.NewReplacer("{id}", escaped, "{item_id}", someID).Replace(o.path))
				check(o.op.OperationID+" with an id "+spelling, req, "id")
				paths++
			}
			if strings.Contains(o.path, "{item_id}") {
				req := exampleRequest(t, o)
				req.URL = mustParse(t, BasePath+strings.NewReplacer("{id}", someID, "{item_id}", escaped).Replace(o.path))
				check(o.op.OperationID+" with an item id "+spelling, req, "item_id")
				paths++
			}
		}
	}
	if paths == 0 {
		t.Fatal("no path id checked")
	}

	for spelling, id := range spellings {
		query := httptest.NewRequest(http.MethodGet, BasePath+"/albums", nil)
		q := query.URL.Query()
		q.Set("artist", id)
		query.URL.RawQuery = q.Encode()
		check("listAlbums with an artist "+spelling, query, "artist")

		body, err := json.Marshal(map[string]any{"track_ids": []string{someID, id}, "position": nil})
		if err != nil {
			t.Fatal(err)
		}
		items := httptest.NewRequest(http.MethodPost, BasePath+"/playlists/"+someID+"/items", bytes.NewReader(body))
		items.Header.Set("Content-Type", "application/json")
		check("addPlaylistItems with a track id "+spelling, items, "")
	}

	// The canonical spelling passes all three.
	for where, req := range map[string]*http.Request{
		"path":  httptest.NewRequest(http.MethodGet, BasePath+"/tracks/"+someID, nil),
		"query": httptest.NewRequest(http.MethodGet, BasePath+"/albums?artist="+someID, nil),
		"body": func() *http.Request {
			r := httptest.NewRequest(http.MethodPost, BasePath+"/playlists/"+someID+"/items",
				strings.NewReader(`{"track_ids":["`+someID+`"],"position":null}`))
			r.Header.Set("Content-Type", "application/json")
			return r
		}(),
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		wantError(t, "a canonical id in the "+where, rec, http.StatusNotImplemented, "not_implemented")
	}
}

func mustParse(t *testing.T, target string) *url.URL {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// The requests of the real operations that the validator refuses: enums,
// limits, lengths. Each answers 400 invalid_request and names the parameter.
func TestParametersOutOfTheSpecification(t *testing.T) {
	handler, _ := testHandler(t, nil)
	long := strings.Repeat("a", 101)
	for target, parameter := range map[string]string{
		"/albums?sort=wrong":                    "sort",
		"/albums?sort=Title":                    "sort",
		"/albums?order=up":                      "order",
		"/albums?limit=0":                       "limit",
		"/albums?limit=201":                     "limit",
		"/albums?limit=-5":                      "limit",
		"/artists?limit=201":                    "limit",
		"/me/favorites/tracks?limit=0":          "limit",
		"/search?q=":                            "q",
		"/search?q=" + long:                     "q",
		"/search?q=a&limit=51":                  "limit",
		"/search?q=a&types=artist,wrong":        "types",
		"/search?q=a&types=":                    "types",
		"/albums/" + someID + "/cover?size=512": "size",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+target, nil))
		message := wantError(t, "GET "+target, rec, http.StatusBadRequest, "invalid_request")
		if !strings.Contains(message, `"`+parameter+`"`) {
			t.Errorf("GET %s: the message %q does not name %q", target, message, parameter)
		}
		if strings.Contains(rec.Body.String(), "wrong") || strings.Contains(rec.Body.String(), long) {
			t.Errorf("GET %s: the answer repeats the value: %q", target, rec.Body.String())
		}
	}
	// At their limits the same requests reach the operation.
	for _, target := range []string{"/albums?sort=year&order=desc&limit=200", "/albums?limit=1", "/search?q=" + long[:100] + "&limit=50&types=artist,album,track",
		"/albums/" + someID + "/cover?size=640&v=x"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, BasePath+target, nil))
		wantError(t, "GET "+target, rec, http.StatusNotImplemented, "not_implemented")
	}
}

// The bodies of the real operations: every request schema is closed, so a
// key the operation does not know, or knows in another case, is refused; a
// duplicate key is refused at every level; and no answer or log line repeats
// the password that was in the body (I5).
func TestHostileBodies(t *testing.T) {
	const password = "correct horse battery staple"
	for _, tc := range []struct {
		name, method, target, body string
	}{
		{"login with the password twice", "POST", "/auth/login", `{"username":"anna","password":"` + password + `","password":"x"}`},
		{"login with the username twice", "POST", "/auth/login", `{"username":"anna","username":"admin","password":"` + password + `"}`},
		{"login with an unknown key", "POST", "/auth/login", `{"username":"anna","password":"` + password + `","role":"admin"}`},
		{"login with the password in another case", "POST", "/auth/login", `{"username":"anna","Password":"` + password + `"}`},
		{"login with both cases", "POST", "/auth/login", `{"username":"anna","password":"` + password + `","PASSWORD":"x"}`},
		{"login with two values", "POST", "/auth/login", `{"username":"anna","password":"` + password + `"}{"username":"admin","password":"x"}`},
		{"login with a number for the password", "POST", "/auth/login", `{"username":"anna","password":123456789012}`},
		{"login with null for the password", "POST", "/auth/login", `{"username":"anna","password":null}`},
		{"login without the password", "POST", "/auth/login", `{"username":"anna"}`},
		{"login with an array", "POST", "/auth/login", `[{"username":"anna","password":"` + password + `"}]`},
		{"login with a device name of 101 characters", "POST", "/auth/login", `{"username":"anna","password":"` + password + `","device_name":"` + strings.Repeat("d", 101) + `"}`},
		{"a token without a device name", "POST", "/auth/tokens", `{"username":"anna","password":"` + password + `"}`},
		{"a user with a role that does not exist", "POST", "/admin/users", `{"username":"anna","password":"` + password + `","role":"root"}`},
		{"a user update with an unknown key", "PUT", "/admin/users/" + someID, `{"role":"admin","disabled":false,"username":"x"}`},
		{"a user update with a string for a boolean", "PUT", "/admin/users/" + someID, `{"role":"admin","disabled":"false"}`},
		{"a new password with a duplicate key", "PUT", "/me/password", `{"current_password":"` + password + `","new_password":"` + password + `","new_password":"x"}`},
		{"playlist items with a duplicate key", "POST", "/playlists/" + someID + "/items", `{"track_ids":["` + someID + `"],"position":null,"position":0}`},
		{"playlist items without ids", "POST", "/playlists/" + someID + "/items", `{"track_ids":[],"position":null}`},
		{"playlist items with a fraction for the position", "POST", "/playlists/" + someID + "/items", `{"track_ids":["` + someID + `"],"position":0.5}`},
		{"playlist items with a position that does not fit", "POST", "/playlists/" + someID + "/items", `{"track_ids":["` + someID + `"],"position":1e400}`},
		{"playlist items with a position too large for an integer", "POST", "/playlists/" + someID + "/items", `{"track_ids":["` + someID + `"],"position":99999999999999999999}`},
		{"a move with a string for the position", "POST", "/playlists/" + someID + "/items/" + someID + "/move", `{"position":"1"}`},
		{"a playlist with a number for the name", "POST", "/playlists", `{"name":1,"description":""}`},
	} {
		handler, logs := testHandler(t, nil)
		req := httptest.NewRequest(tc.method, BasePath+tc.target, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		wantError(t, tc.name, rec, http.StatusBadRequest, "invalid_request")
		if strings.Contains(rec.Body.String(), password) || strings.Contains(logs.String(), password) {
			t.Errorf("%s: the password is in the answer or in the log:\n%s\n%s", tc.name, rec.Body.String(), logs)
		}
		if logs.Len() != 0 {
			t.Errorf("%s: a refusal was logged: %s", tc.name, logs)
		}
	}
}

// §8.1: a body is at most 1 MiB, on every operation that takes one.
func TestBodyLimitOnEveryOperationWithABody(t *testing.T) {
	doc := loadSpec(t)
	handler, _ := testHandler(t, nil)
	bodies := 0
	for _, o := range operations(doc) {
		if o.op.RequestBody == nil {
			continue
		}
		bodies++
		for name, size := range map[string]int{"1 MiB + 1 byte": httpx.MaxBodyBytes + 1, "8 MiB": 8 << 20} {
			big := httptest.NewRequest(o.method, exampleRequest(t, o).URL.String(), strings.NewReader(`{"x":"`+strings.Repeat("a", size-8)+`"}`))
			big.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, big)
			wantError(t, o.op.OperationID+" with "+name, rec, http.StatusRequestEntityTooLarge, "body_too_large")
		}
	}
	if bodies != 10 {
		t.Fatalf("%d operations with a body, want 10", bodies)
	}
}

// §11.5: the routes whose access log is at DEBUG are those of the audio and
// of the covers, as the router names them.
func TestQuietRoutes(t *testing.T) {
	doc := loadSpec(t)
	var want []string
	for _, o := range operations(doc) {
		if o.op.OperationID == "getTrackAudio" || o.op.OperationID == "getAlbumCover" {
			want = append(want, o.method+" "+BasePath+o.path)
		}
	}
	got := QuietRoutes()
	slices.Sort(got)
	slices.Sort(want)
	if len(want) != 2 || !slices.Equal(got, want) {
		t.Fatalf("QuietRoutes() = %v, want the routes of getTrackAudio and getAlbumCover: %v", got, want)
	}

	// They are the patterns the router gives a request.
	handler, _ := testHandler(t, nil)
	for target, pattern := range map[string]string{
		BasePath + "/tracks/" + someID + "/audio": got[1],
		BasePath + "/albums/" + someID + "/cover": got[0],
	} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
		if req.Pattern != pattern {
			t.Errorf("GET %s was routed as %q, want %q", target, req.Pattern, pattern)
		}
	}
}

// The generated code reports what it could not bind with its own error
// types; the answer names the parameter and never its value.
func TestParameterName(t *testing.T) {
	for want, err := range map[string]error{
		"id":       &InvalidParamFormatError{ParamName: "id"},
		"q":        &RequiredParamError{ParamName: "q"},
		"If-Match": &RequiredHeaderError{ParamName: "If-Match"},
		"sort":     &TooManyValuesForParamError{ParamName: "sort", Count: 2},
		"filter":   &UnmarshalingParamError{ParamName: "filter"},
		"session":  &UnescapedCookieParamError{ParamName: "session"},
		"":         http.ErrBodyNotAllowed,
	} {
		if got := parameterName(err); got != want {
			t.Errorf("parameterName(%T) = %q, want %q", err, got, want)
		}
	}
}
