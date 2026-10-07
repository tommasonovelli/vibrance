package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"vibrance/internal/httpx"
	"vibrance/web"
)

// The web interface (the erratum W1–W6 of DESIGN.md, proposal C1): the
// files of web/ui, in the binary, served at the root to anyone; /login is
// the sign-in page and every other path outside /api and /health the page
// of the interface, whose router shows what the path names.

// The policy of the pages, written out: a change of it is a change of this
// test, made on purpose.
const wantUICSP = "default-src 'self'; img-src 'self' data:; media-src 'self'; frame-ancestors 'none'; " +
	"base-uri 'none'; form-action 'self'"

// The Content-Type of each kind of file the interface has (proposal C1).
var wantUITypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".svg":   "image/svg+xml",
	".woff2": "font/woff2",
}

// uiDir is web/ui in the source tree.
var uiDir = filepath.Join("..", "..", "web", "ui")

// uiPaths is the path on the server of every file the binary carries for
// the interface, sorted.
func uiPaths(t *testing.T) []string {
	t.Helper()
	var paths []string
	err := fs.WalkDir(web.UI, ".", func(name string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, "/"+name)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("the binary carries no file of the web interface")
	}
	return paths
}

// The binary carries every file of web/ui of a type the server serves, and
// nothing else: not the notes of the folder, not the text of the license.
func TestUIEmbedsTheFilesOfTheFolder(t *testing.T) {
	var want, notServed []string
	err := filepath.WalkDir(uiDir, func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(uiDir, name)
		if err != nil {
			return err
		}
		p := "/" + filepath.ToSlash(rel)
		if _, ok := wantUITypes[path.Ext(p)]; ok {
			want = append(want, p)
		} else {
			notServed = append(notServed, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(want)
	if got := uiPaths(t); !slices.Equal(got, want) {
		t.Errorf("the binary carries %v, web/ui has %v", got, want)
	}
	for _, p := range []string{"/README.md", "/VENDOR.md", "/fonts/OFL.txt"} {
		if !slices.Contains(notServed, p) {
			t.Errorf("web/ui has no %s: the test should name what is not served", p)
		}
	}
}

// Every file answers, without a session, with its bytes, its type, its
// ETag (the SHA-256 of the file, with 304 when the browser has it) and
// Cache-Control: no-cache. The pages carry their policy and refuse frames;
// the other files the closed policy of everything that is not a page.
func TestUIFilesAreServed(t *testing.T) {
	w := newWorld(t, apiOrigin)
	for _, p := range uiPaths(t) {
		body := readFile(t, filepath.Join(uiDir, filepath.FromSlash(p[1:])))
		page := path.Ext(p) == ".html"
		csp, frames := wantClosedCSP, ""
		if page {
			csp, frames = wantUICSP, "DENY"
		}
		rec := w.plain(http.MethodGet, p)
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), body) {
			t.Fatalf("GET %s: status %d, %d bytes, want 200 and the %d bytes of the file", p, rec.Code, rec.Body.Len(), len(body))
		}
		h := rec.Header()
		if h.Get("Content-Type") != wantUITypes[path.Ext(p)] || h.Get("Content-Security-Policy") != csp ||
			h.Get("X-Frame-Options") != frames || h.Get("Cache-Control") != "no-cache" {
			t.Errorf("GET %s: Content-Type %q, Content-Security-Policy %q, X-Frame-Options %q, Cache-Control %q", p,
				h.Get("Content-Type"), h.Get("Content-Security-Policy"), h.Get("X-Frame-Options"), h.Get("Cache-Control"))
		}
		if len(h.Values("Set-Cookie")) != 0 {
			t.Errorf("GET %s sets a cookie", p)
		}
		sum := sha256.Sum256(body)
		etag := `"` + hex.EncodeToString(sum[:]) + `"`
		if h.Get("ETag") != etag {
			t.Errorf("GET %s: ETag %s, want the SHA-256 of the file", p, h.Get("ETag"))
		}
		if rec := w.plain(http.MethodGet, p, "If-None-Match: "+etag); rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
			t.Errorf("GET %s with its ETag: status %d, %d bytes", p, rec.Code, rec.Body.Len())
		}
		if rec := w.plain(http.MethodGet, p, `If-None-Match: "another"`); rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), body) {
			t.Errorf("GET %s with another ETag: status %d", p, rec.Code)
		}
		if rec := w.plain(http.MethodHead, p); rec.Code != http.StatusOK || rec.Body.Len() != 0 ||
			rec.Header().Get("Content-Security-Policy") != csp || rec.Header().Get("ETag") != etag {
			t.Errorf("HEAD %s: status %d, %d bytes", p, rec.Code, rec.Body.Len())
		}
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			rec := w.plain(method, p, httpx.RequestHeader+": 1")
			wantCode(t, method+" "+p, rec, http.StatusMethodNotAllowed, "method_not_allowed")
			if rec.Header().Get("Allow") != "GET, HEAD" {
				t.Errorf("%s %s: Allow %q", method, p, rec.Header().Get("Allow"))
			}
		}
		// Behind the boundary, like everything but the health (I4).
		wantCode(t, "GET "+p+" for another host", w.plain(http.MethodGet, p, "Host: evil.example"), http.StatusMisdirectedRequest, "host_not_allowed")
		wantCode(t, "GET "+p+" from another origin", w.plain(http.MethodGet, p, "Origin: https://evil.example"), http.StatusForbidden, "origin_not_allowed")
	}
}

// /login is the sign-in page; every other path of the interface's router is
// its page, with 200, whether or not the router knows it; a path under /api
// or /health is never a page, but the 404 of the error model.
func TestUIRoutes(t *testing.T) {
	w := newWorld(t, apiOrigin)
	index := readFile(t, filepath.Join(uiDir, "index.html"))
	login := readFile(t, filepath.Join(uiDir, "login.html"))
	for target, body := range map[string][]byte{
		"/":                     index,
		"/?x=1":                 index,
		"/albums/" + someID:     index,
		"/artists":              index,
		"/playlists/" + someID:  index,
		"/nothing/at/all":       index,
		"/README.md":            index,
		"/VENDOR.md":            index,
		"/fonts/OFL.txt":        index,
		"/fonts":                index,
		"/docs":                 index,
		"/healthz":              index,
		"/apiv1":                index,
		"/login":                login,
		"/login?next=%2Falbums": login,
		"/login.html":           login,
		"/index.html":           index,
	} {
		for _, credentials := range []string{"X-Nothing: 1", "Authorization: Bearer vb_not-a-token", "Cookie: vibrance_session=nothing"} {
			rec := w.plain(http.MethodGet, target, credentials)
			if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), body) {
				t.Errorf("GET %s with %s: status %d, %d bytes, want 200 and the %d bytes of its page", target, credentials, rec.Code, rec.Body.Len(), len(body))
				continue
			}
			h := rec.Header()
			if h.Get("Content-Type") != "text/html; charset=utf-8" || h.Get("Content-Security-Policy") != wantUICSP ||
				h.Get("X-Frame-Options") != "DENY" {
				t.Errorf("GET %s: Content-Type %q, Content-Security-Policy %q, X-Frame-Options %q", target,
					h.Get("Content-Type"), h.Get("Content-Security-Policy"), h.Get("X-Frame-Options"))
			}
		}
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			rec := w.plain(method, target, httpx.RequestHeader+": 1")
			wantCode(t, method+" "+target, rec, http.StatusMethodNotAllowed, "method_not_allowed")
			if rec.Header().Get("Allow") != "GET, HEAD" {
				t.Errorf("%s %s: Allow %q", method, target, rec.Header().Get("Allow"))
			}
		}
	}
	// A path that is not in its canonical form is not a page: no path of
	// the server has an alias.
	for _, target := range []string{"/views/", "/login/", "/albums/" + someID + "/", "/./app.js", "//app.js"} {
		wantCode(t, "GET "+target, w.plain(http.MethodGet, target), http.StatusNotFound, "not_found")
	}
	// A browser asks for a page with Accept: text/html; the API still
	// answers its own 404, never the page.
	for _, target := range []string{"/api/v1/nothing", "/api/v1/albums/" + someID + "/nothing", "/api/v2/server", "/api",
		"/api/", "/api/app.js", "/api/index.html", "/api/login", "/health", "/health/", "/health/nothing", "/health/index.html"} {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
			rec := w.plain(method, target, "Accept: text/html", httpx.RequestHeader+": 1")
			if method == http.MethodHead {
				if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/json" {
					t.Errorf("HEAD %s: status %d, Content-Type %q", target, rec.Code, rec.Header().Get("Content-Type"))
				}
				continue
			}
			wantCode(t, method+" "+target, rec, http.StatusNotFound, "not_found")
		}
	}
}

// I2: the path of a request chooses among the files of the binary, and
// names no file to open. A path that climbs out of the folder, with `..`
// or a percent-encoded slash or dot, is a 404 or one of those files, never
// another file of the server.
func TestUIPathsChooseOnlyEmbeddedFiles(t *testing.T) {
	handler, _ := apiHandler(t)
	var embedded [][]byte
	for _, p := range uiPaths(t) {
		embedded = append(embedded, readFile(t, filepath.Join(uiDir, filepath.FromSlash(p[1:]))))
	}
	goMod := readFile(t, filepath.Join("..", "..", "go.mod"))
	for _, target := range []string{
		"/../go.mod", "/../../go.mod", "/views/../../go.mod", "/fonts/../../../go.mod", "/%2e%2e/go.mod", "/%2E%2E/%2E%2E/go.mod",
		"/..%2fgo.mod", "/..%2f..%2fgo.mod", "/views%2f..%2f..%2fgo.mod", "/views%2falbum.js", "/%2fetc%2fpasswd", "//etc/passwd",
		"/etc/passwd", "/web/embed.go", "/ui/index.html", "/web/ui/index.html", "/embed.go", "/go.mod", "/.git/config",
		"/views/..%5c..%5cgo.mod", "/views\\..\\..\\go.mod", "/fonts/%2e%2e/%2e%2e/go.mod", "/./app.js", "/views/./album.js",
		"/README.md", "/fonts/OFL.txt", "/index.html%00.js", "/app.js%00",
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		u, err := url.ParseRequestURI(target)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		req.URL, req.RequestURI, req.Host = u, target, apiHost
		rec := send(handler, req)
		wantHeaders(t, "GET "+target, rec.Header())
		switch rec.Code {
		case http.StatusNotFound:
			wantCode(t, "GET "+target, rec, http.StatusNotFound, "not_found")
		case http.StatusOK:
			if !slices.ContainsFunc(embedded, func(b []byte) bool { return bytes.Equal(b, rec.Body.Bytes()) }) {
				t.Errorf("GET %s: 200 with a body that is no file of the interface", target)
			}
		default:
			t.Errorf("GET %s: status %d", target, rec.Code)
		}
		if bytes.Contains(rec.Body.Bytes(), goMod[:20]) {
			t.Errorf("GET %s answers go.mod", target)
		}
	}
}

// The pages are written for their policy: no inline script, no event
// handler attribute, no style element or attribute, and nothing named on
// another host. The images have no style and no script either, so the
// closed policy they are served with refuses nothing in them.
func TestUIFilesKeepTheirPolicy(t *testing.T) {
	for _, p := range uiPaths(t) {
		ext := path.Ext(p)
		if ext != ".html" && ext != ".svg" {
			continue
		}
		text := string(readFile(t, filepath.Join(uiDir, filepath.FromSlash(p[1:]))))
		for _, forbidden := range []string{"<style", " style=", "javascript:", "@import"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s contains %q", p, forbidden)
			}
		}
		if regexp.MustCompile(`(?i)\son[a-z]+\s*=`).MatchString(text) {
			t.Errorf("%s has an event handler attribute", p)
		}
		for _, url := range regexp.MustCompile(`https?://[^\s"'<>)]+`).FindAllString(text, -1) {
			// The namespace of SVG is a name, not a request.
			if url != "http://www.w3.org/2000/svg" {
				t.Errorf("%s names %s, on another host", p, url)
			}
		}
		for _, tag := range regexp.MustCompile(`(?s)<script\b([^>]*)>(.*?)</script>`).FindAllStringSubmatch(text, -1) {
			if ext == ".svg" || !regexp.MustCompile(`\ssrc="/[a-z][a-z/-]*\.js"`).MatchString(tag[1]) || strings.TrimSpace(tag[2]) != "" {
				t.Errorf("%s has a script that is not a file of this server: %s", p, tag[0])
			}
		}
	}
}

// The interface needs nothing the startup makes: it answers while the
// operations still answer 503.
func TestUIWhileTheServerStarts(t *testing.T) {
	s := mustServer(t, newLogger(&syncBuffer{}), apiOrigin, t.TempDir(), t.TempDir())
	for _, p := range []string{"/", "/login", "/app.js", "/albums/" + someID} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.Host = apiHost
		if rec := send(s.http.Handler, req); rec.Code != http.StatusOK {
			t.Errorf("GET %s while the server starts: status %d", p, rec.Code)
		}
	}
}
