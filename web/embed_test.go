package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"strconv"
	"testing"
)

// The script in the binary is the release docs/VENDOR.md records: its
// SHA-256, its size and its version (DESIGN.md §8.8, I12). A script that
// was edited, or replaced without the record, fails here.
func TestDocsScriptIsTheVendoredRelease(t *testing.T) {
	vendor, err := os.ReadFile("docs/VENDOR.md")
	if err != nil {
		t.Fatal(err)
	}
	field := func(name, pattern string) string {
		t.Helper()
		m := regexp.MustCompile(`(?m)^\| ` + regexp.QuoteMeta(name) + ` \| ` + pattern + ` \|$`).FindSubmatch(vendor)
		if m == nil {
			t.Fatalf("docs/VENDOR.md has no row %q of the expected form", name)
		}
		return string(m[1])
	}
	sum := sha256.Sum256(DocsScript)
	if got, want := hex.EncodeToString(sum[:]), field("SHA-256", "`([0-9a-f]{64})`"); got != want {
		t.Errorf("the SHA-256 of docs/scalar.js is %s, docs/VENDOR.md records %s", got, want)
	}
	if got, want := strconv.Itoa(len(DocsScript)), field("Size", `(\d+) bytes`); got != want {
		t.Errorf("docs/scalar.js is %s bytes, docs/VENDOR.md records %s", got, want)
	}
	// The script names its own package and version.
	version := field("Version", "`([0-9]+[.][0-9]+[.][0-9]+)`")
	if !bytes.Contains(DocsScript, []byte("@scalar/api-reference@"+version)) {
		t.Errorf("docs/scalar.js does not say it is @scalar/api-reference@%s", version)
	}
}

// The page is the file of this folder.
func TestDocsPageIsEmbedded(t *testing.T) {
	page, err := os.ReadFile("docs/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(page, DocsPage) || !bytes.HasPrefix(page, []byte("<!doctype html>")) {
		t.Errorf("the embedded page is not docs/index.html")
	}
}
