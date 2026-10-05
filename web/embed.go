// Package web holds, inside the binary, the page of the API documentation
// that the server answers at /api/docs (DESIGN.md §8.8): one HTML page and
// the one script it loads, a vendored release of Scalar API Reference. The
// page asks the server for the specification and for nothing else, and
// nothing of it comes from another host. docs/VENDOR.md says where the
// script comes from, at which version and with which SHA-256.
package web

import _ "embed"

// DocsPage is docs/index.html, the page of the API documentation.
//
//go:embed docs/index.html
var DocsPage []byte

// DocsScript is docs/scalar.js, the script the page loads.
//
//go:embed docs/scalar.js
var DocsScript []byte
