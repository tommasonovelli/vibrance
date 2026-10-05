package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"vibrance/internal/api"
	"vibrance/internal/httpx"
	"vibrance/web"
)

// The documentation the server carries (DESIGN.md §8.8, step S20): the
// specification, the page that shows it and the script of the page, served
// to anyone, from the binary alone.

// plain is a request as a browser that never signed in sends it, with the
// headers given ("Name: value"; Host replaces the one of the world).
func (w *world) plain(method, path string, headers ...string) *httptest.ResponseRecorder {
	w.t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Host = w.host
	for _, h := range headers {
		name, value, _ := strings.Cut(h, ": ")
		if name == "Host" {
			req.Host = value
			continue
		}
		req.Header.Set(name, value)
	}
	rec := send(w.s.http.Handler, req)
	wantHeaders(w.t, method+" "+path, rec.Header())
	return rec
}

// The policy of the page, written out: a change of it is a change of this
// test, made on purpose. It was tried in a browser (T23).
const wantDocsCSP = "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src 'self'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// The policy of everything that is not that page (§7.6).
const wantClosedCSP = "default-src 'none'; frame-ancestors 'none'"

// The three files answer without a session, each with its type and its
// policy, and each is the file the binary carries, byte for byte: the
// specification served is the one the requests are validated against and
// the one in api/openapi.yaml.
func TestDocumentationIsServed(t *testing.T) {
	w := newWorld(t, apiOrigin)
	onDisk := readFile(t, filepath.Join("..", "..", "api", "openapi.yaml"))
	for _, f := range []struct {
		path, contentType, csp string
		body                   []byte
	}{
		{"/api/openapi.yaml", "application/yaml", wantClosedCSP, onDisk},
		{"/api/docs", "text/html; charset=utf-8", wantDocsCSP, web.DocsPage},
		{"/api/docs/scalar.js", "text/javascript; charset=utf-8", wantClosedCSP, web.DocsScript},
	} {
		rec := w.plain(http.MethodGet, f.path)
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), f.body) {
			t.Fatalf("GET %s: status %d, %d bytes, want 200 and the %d bytes of the file", f.path, rec.Code, rec.Body.Len(), len(f.body))
		}
		h := rec.Header()
		if h.Get("Content-Type") != f.contentType || h.Get("Content-Security-Policy") != f.csp || h.Get("Cache-Control") != "no-cache" {
			t.Errorf("GET %s: Content-Type %q, Content-Security-Policy %q, Cache-Control %q", f.path, h.Get("Content-Type"),
				h.Get("Content-Security-Policy"), h.Get("Cache-Control"))
		}
		if len(h.Values("Set-Cookie")) != 0 {
			t.Errorf("GET %s sets a cookie", f.path)
		}
		sum := sha256.Sum256(f.body)
		etag := `"` + hex.EncodeToString(sum[:]) + `"`
		if h.Get("ETag") != etag {
			t.Errorf("GET %s: ETag %s, want the SHA-256 of the file", f.path, h.Get("ETag"))
		}
		// A browser that has the file is told so, and one that has another
		// gets this one.
		if rec := w.plain(http.MethodGet, f.path, "If-None-Match: "+etag); rec.Code != http.StatusNotModified || rec.Body.Len() != 0 ||
			rec.Header().Get("Content-Security-Policy") != f.csp {
			t.Errorf("GET %s with its ETag: status %d, %d bytes", f.path, rec.Code, rec.Body.Len())
		}
		if rec := w.plain(http.MethodGet, f.path, `If-None-Match: "another"`); rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), f.body) {
			t.Errorf("GET %s with another ETag: status %d", f.path, rec.Code)
		}
		if rec := w.plain(http.MethodHead, f.path); rec.Code != http.StatusOK || rec.Body.Len() != 0 ||
			rec.Header().Get("Content-Security-Policy") != f.csp {
			t.Errorf("HEAD %s: status %d, %d bytes", f.path, rec.Code, rec.Body.Len())
		}
		// A session, valid or not, changes nothing: nobody looks at it.
		for _, credentials := range []string{"Authorization: Bearer vb_not-a-token", "Cookie: vibrance_session=nothing"} {
			if rec := w.plain(http.MethodGet, f.path, credentials); rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), f.body) {
				t.Errorf("GET %s with %s: status %d", f.path, strings.SplitN(credentials, ":", 2)[0], rec.Code)
			}
		}
		// The files are behind the boundary like everything but the health.
		wantCode(t, "GET "+f.path+" for another host", w.plain(http.MethodGet, f.path, "Host: evil.example"), http.StatusMisdirectedRequest, "host_not_allowed")
		wantCode(t, "GET "+f.path+" from another origin", w.plain(http.MethodGet, f.path, "Origin: https://evil.example"), http.StatusForbidden, "origin_not_allowed")
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			rec := w.plain(method, f.path, httpx.RequestHeader+": 1")
			wantCode(t, method+" "+f.path, rec, http.StatusMethodNotAllowed, "method_not_allowed")
			if rec.Header().Get("Allow") != "GET, HEAD" {
				t.Errorf("%s %s: Allow %q", method, f.path, rec.Header().Get("Allow"))
			}
		}
	}
	for _, path := range []string{"/api", "/api/", "/api/docs/", "/api/docs/index.html", "/api/docs/VENDOR.md", "/api/docs/scalar.js.map",
		"/api/openapi.json", "/docs"} {
		wantCode(t, "GET "+path, w.plain(http.MethodGet, path), http.StatusNotFound, "not_found")
	}
}

// `/` leads to the page (D20), and the page is there.
func TestRootLeadsToTheDocumentation(t *testing.T) {
	w := newWorld(t, apiOrigin)
	rec := w.plain(http.MethodGet, "/")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/api/docs" {
		t.Fatalf("GET /: status %d, Location %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := w.plain(http.MethodGet, rec.Header().Get("Location")); rec.Code != http.StatusOK {
		t.Fatalf("the page `/` leads to: status %d", rec.Code)
	}
}

// Everything a URL in the page names is on this server: the script and the
// specification, by paths that answer. No other host is named, so nothing
// comes from a CDN (§8.8), and the page has no inline script, which its
// policy would refuse.
func TestDocumentationPageNamesOnlyThisServer(t *testing.T) {
	w := newWorld(t, apiOrigin)
	page := string(web.DocsPage)
	for _, forbidden := range []string{"http://", "https://", `="//`, "@import", "<link", "<style", "<iframe"} {
		if strings.Contains(page, forbidden) {
			t.Errorf("the page contains %q", forbidden)
		}
	}
	if regexp.MustCompile(`(?i)\son[a-z]+\s*=`).MatchString(page) {
		t.Errorf("the page has an event handler attribute")
	}
	urls := regexp.MustCompile(`(?:src|href|data-url)="([^"]*)"`).FindAllStringSubmatch(page, -1)
	if len(urls) != 3 {
		t.Fatalf("the page names %d URLs, want the specification twice (one for a browser without scripts) and the script: %v", len(urls), urls)
	}
	named := map[string]bool{}
	for _, u := range urls {
		named[u[1]] = true
		if rec := w.plain(http.MethodGet, u[1]); rec.Code != http.StatusOK {
			t.Errorf("the page names %s, which answers %d", u[1], rec.Code)
		}
	}
	if !named[api.SpecPath] || !named[api.DocsScriptPath] || len(named) != 2 {
		t.Errorf("the page names %v, want %s and %s", named, api.SpecPath, api.DocsScriptPath)
	}
	// Every script element either loads the script of this server or is
	// data the browser does not run.
	for _, tag := range regexp.MustCompile(`(?s)<script\b[^>]*>(.*?)</script>`).FindAllStringSubmatch(page, -1) {
		open := tag[0][:strings.Index(tag[0], ">")]
		runs := !strings.Contains(open, `type="application/json"`)
		if runs && (!strings.Contains(open, `src="`+api.DocsScriptPath+`"`) || strings.TrimSpace(tag[1]) != "") {
			t.Errorf("the page has a script that is not the one of this server: %s", open)
		}
	}
	// The page turns off what would ask another host, whatever the policy
	// then refuses.
	for _, option := range []string{`"withDefaultFonts":false`, `"telemetry":false`, `"agent":{"disabled":true}`, `"mcp":{"disabled":true}`} {
		if !strings.Contains(page, option) {
			t.Errorf("the configuration of the page lacks %s", option)
		}
	}
}

// The documentation needs nothing the startup makes: it answers while the
// operations still answer 503.
func TestDocumentationWhileTheServerStarts(t *testing.T) {
	s := mustServer(t, newLogger(&syncBuffer{}), apiOrigin, t.TempDir(), t.TempDir())
	for _, path := range []string{api.SpecPath, api.DocsPath, api.DocsScriptPath} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = apiHost
		if rec := send(s.http.Handler, req); rec.Code != http.StatusOK {
			t.Errorf("GET %s while the server starts: status %d", path, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, api.BasePath+"/server", nil)
	req.Host = apiHost
	wantCode(t, "an operation while the server starts", send(s.http.Handler, req), http.StatusServiceUnavailable, "not_ready")
}

// Step S20: every operation of the specification is called with the data
// of its examples, by an admin, on a server with the fixture library, and
// its answer is one the specification declares for it, with a body of the
// declared shape (I10). None is a failure of the server, and none is 501:
// every operation is implemented.
func TestEveryOperationAnswersItsExample(t *testing.T) {
	_, ops := specOperations(t)
	w := newWorld(t, apiOrigin)
	w.indexFixture()
	answered := map[int]int{}
	for _, o := range ops {
		req := o.example(t)
		w.as(w.admin)(req)
		rec := send(w.s.http.Handler, req)
		assertConforms(t, o.op.OperationID+" with its example", req, rec)
		if rec.Code >= 500 {
			t.Errorf("%s with its example: status %d (%s)", o.op.OperationID, rec.Code, redacted(rec))
		}
		// Whatever it answers, the operation was reached: the example is a
		// request the boundary and the validation accept.
		wantReached(t, o.op.OperationID+" with its example", rec)
		answered[rec.Code/100]++
	}
	// The examples that name nothing of the index succeed; those that name
	// an id get the 404 of their resource, and a sign-in with the example
	// credentials its refusal.
	if answered[2] < 10 || answered[2]+answered[4] != len(ops) {
		t.Fatalf("the answers to the examples, by class of status: %v", answered)
	}
}
