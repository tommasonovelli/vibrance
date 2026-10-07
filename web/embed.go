// Package web holds, inside the binary, the pages the server answers that
// are not operations of the API:
//
//   - the page of the API documentation, at /api/docs (DESIGN.md §8.8): one
//     HTML page and the one script it loads, a vendored release of Scalar
//     API Reference. docs/VENDOR.md says where the script comes from, at
//     which version and with which SHA-256;
//   - the web interface, ui/, served at the root (the erratum W1–W6 of
//     DESIGN.md, proposal C1): plain HTML, CSS, ES modules, SVG images and
//     the Hanken Grotesk font, with no build step. ui/VENDOR.md records the
//     files that come from elsewhere.
//
// Nothing of either comes from another host.
package web

import (
	"embed"
	"io/fs"
)

// DocsPage is docs/index.html, the page of the API documentation.
//
//go:embed docs/index.html
var DocsPage []byte

// DocsScript is docs/scalar.js, the script the page loads.
//
//go:embed docs/scalar.js
var DocsScript []byte

// The files of the web interface a browser loads: only the types the
// server has a Content-Type for. The notes of the folder (README.md,
// VENDOR.md) and the text of the font's license (fonts/OFL.txt, also in
// licenses/ of the image) are not served.
//
//go:embed ui/*.html ui/*.js ui/*.css ui/*.svg ui/views/*.js ui/fonts/*.woff2
var ui embed.FS

// UI is the folder of the web interface, with its paths relative to ui/:
// "index.html", "views/album.js", "fonts/….woff2".
var UI = mustSub(ui, "ui")

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		// fs.Sub fails only on an invalid name, and "ui" is a valid one.
		panic(err)
	}
	return sub
}
