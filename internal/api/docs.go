package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	apispec "vibrance/api"
	"vibrance/internal/httpx"
	"vibrance/web"
)

// The documentation the server carries (DESIGN.md §8.8): the specification
// and a page that shows it. They are outside the specification, public, and
// made of nothing but what is in the binary.
const (
	// SpecPath answers api/openapi.yaml, byte for byte.
	SpecPath = "/api/openapi.yaml"
	// DocsPath answers the page of the documentation.
	DocsPath = "/api/docs"
	// DocsScriptPath answers the one script the page loads.
	DocsScriptPath = DocsPath + "/scalar.js"
)

// closedCSP is the policy of the JSON responses (§7.6): a file that is not a
// page needs nothing more.
const closedCSP = "default-src 'none'; frame-ancestors 'none'"

// DocsCSP is the policy of the page of the documentation: wider than that
// of the API, and no wider than the page needs (T23). It was tried in a
// browser, on the whole page, its search and its request client.
//
//   - script-src 'self': the one script, from this server. No inline script
//     and no eval: the script tries eval once, is refused, and works.
//   - style-src 'unsafe-inline': the script writes its style sheet into the
//     page and sets style attributes.
//   - connect-src 'self': the page reads the specification, and sends the
//     requests a reader tries, to this server only. This is what keeps every
//     other host out, whatever the script would ask for.
//
// Everything else is closed: no image, no font, no frame, no form, no
// worker, no base.
const DocsCSP = "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src 'self'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// RegisterDocs routes the specification, the page of the documentation and
// its script on mux. They take GET and HEAD, and answer 405
// method_not_allowed to anything else.
func RegisterDocs(mux *http.ServeMux, log *slog.Logger) {
	for path, file := range map[string]embedded{
		SpecPath:       newEmbedded("application/yaml", closedCSP, []byte(apispec.YAML)),
		DocsPath:       newEmbedded("text/html; charset=utf-8", DocsCSP, web.DocsPage),
		DocsScriptPath: newEmbedded("text/javascript; charset=utf-8", closedCSP, web.DocsScript),
	} {
		mux.Handle(http.MethodGet+" "+path, file)
		mux.Handle(path, httpx.MethodNotAllowed(log, "GET, HEAD"))
	}
}

// embedded is a file of the binary, served as it is.
type embedded struct {
	contentType string
	csp         string
	// etag is the SHA-256 of body, quoted: the file changes only with the
	// binary, so a browser keeps it and asks whether it is still the one.
	etag string
	body []byte
}

func newEmbedded(contentType, csp string, body []byte) embedded {
	sum := sha256.Sum256(body)
	return embedded{contentType: contentType, csp: csp, etag: `"` + hex.EncodeToString(sum[:]) + `"`, body: body}
}

func (e embedded) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", e.contentType)
	h.Set("Content-Security-Policy", e.csp)
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", e.etag)
	// No name and no time: the type is the one set above, and the ETag is
	// the only validator.
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(e.body))
}
