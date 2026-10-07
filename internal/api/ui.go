package api

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"vibrance/internal/httpx"
)

// The web interface (the erratum W1–W6 of DESIGN.md, proposal C1). Its
// files are not operations of the API and are not in the specification:
// like the documentation, they are routes of the infrastructure, public,
// made of nothing but what is in the binary.

// UICSP is the policy of the pages of the interface. The interface was
// written for default-src 'self': no inline script, no style attribute,
// nothing from another host. Images may be data: URIs; audio and covers
// come from this server.
const UICSP = "default-src 'self'; img-src 'self' data:; media-src 'self'; frame-ancestors 'none'; " +
	"base-uri 'none'; form-action 'self'"

// The paths of the server that are not the interface's: a path under them
// that no route takes is a 404 of the error model, never a page.
var notUIRoots = []string{"/api", "/health"}

// uiTypes are the Content-Types of the files of the interface, by
// extension. A file of another type is not served (web.UI embeds none).
var uiTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".svg":   "image/svg+xml",
	".woff2": "font/woff2",
}

// RegisterUI routes the web interface on mux, as the route of every path
// no other route takes:
//
//   - a file of files at its path (/app.js, /views/album.js, /fonts/…);
//   - /login: the sign-in page, login.html;
//   - any other path: index.html, whose router shows the page or its own
//     "not found";
//
// except a path under /api or /health, which answers 404 not_found. They
// take GET and HEAD, and answer 405 method_not_allowed to anything else.
//
// The path of a request only chooses among the files read here, at the
// start (I2): it never names a file to open.
func RegisterUI(mux *http.ServeMux, files fs.FS, log *slog.Logger) error {
	served, err := readUI(files)
	if err != nil {
		return err
	}
	index, login := served["/index.html"], served["/login.html"]
	if index == nil || login == nil {
		return errors.New("the web interface has no index.html or no login.html")
	}
	notFound := httpx.NotFound(log)
	getOnly := httpx.MethodNotAllowed(log, "GET, HEAD")
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch f := served[p]; {
		case notUI(p):
			notFound.ServeHTTP(w, r)
		case r.Method != http.MethodGet && r.Method != http.MethodHead:
			getOnly.ServeHTTP(w, r)
		case f != nil:
			f.ServeHTTP(w, r)
		case p == "/login":
			login.ServeHTTP(w, r)
		default:
			index.ServeHTTP(w, r)
		}
	}))
	return nil
}

// readUI reads every file of files, by its path on the server.
func readUI(files fs.FS) (map[string]*embedded, error) {
	served := map[string]*embedded{}
	err := fs.WalkDir(files, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		contentType, ok := uiTypes[path.Ext(name)]
		if !ok {
			return fmt.Errorf("the web interface has %s, of no type the server serves", name)
		}
		body, err := fs.ReadFile(files, name)
		if err != nil {
			return fmt.Errorf("read the web interface: %w", err)
		}
		f := newEmbedded(contentType, closedCSP, body)
		if path.Ext(name) == ".html" {
			// A page: its own policy, and never inside a frame.
			f.csp, f.denyFrames = UICSP, true
		}
		served["/"+name] = &f
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the web interface: %w", err)
	}
	return served, nil
}

// notUI says whether p is one of notUIRoots or under it.
func notUI(p string) bool {
	for _, root := range notUIRoots {
		if p == root || strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	return false
}
