package library

import (
	"io/fs"
	"strings"
)

// validRelPath reports whether p is a path that may be resolved below a
// folder of the library. It is the only validation of paths (DESIGN.md
// §2.5), for the paths a receipt lists and for those the database gives
// back: relative, slash-separated, valid UTF-8, with no empty, "." or ".."
// element (fs.ValidPath), and with no backslash and no NUL byte.
//
// MusicLib replaces the backslash in every name it writes, so a path with
// one is not a path of the library, and on another system it would be a
// separator. "." is valid for fs.ValidPath, where it names the root, but it
// names no file.
func validRelPath(p string) bool {
	return p != "." && fs.ValidPath(p) && !strings.ContainsAny(p, "\\\x00")
}
