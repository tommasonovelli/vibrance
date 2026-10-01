package library

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The code of this package reaches the disk only through an os.Root, and
// only to read (DESIGN.md I1, T11). The test reads the sources, tests
// excluded, and refuses what would be another way in: the packages that
// build or resolve paths on their own, every function of os that takes a
// path except OpenRoot, and every call that writes.
func TestSourcesReachTheDiskOnlyThroughRoot(t *testing.T) {
	forbiddenImports := map[string]string{
		"path/filepath": "paths are relative to the Root and joined with \"/\", never with the folder of the Root",
		"io/ioutil":     "it reads and writes by path",
		"os/exec":       "this package runs no process",
		"syscall":       "the disk is reached through os.Root",
	}
	// What the package may use of os: opening the Root, the types, and
	// comparing two entries.
	allowedOS := map[string]bool{"OpenRoot": true, "Root": true, "File": true, "SameFile": true}
	// The methods of os.Root that change the disk or open for writing,
	// whatever they are called on.
	writes := map[string]bool{
		"Create": true, "OpenFile": true, "Mkdir": true, "MkdirAll": true, "Remove": true, "RemoveAll": true,
		"Rename": true, "WriteFile": true, "Chmod": true, "Chown": true, "Lchown": true, "Chtimes": true,
		"Link": true, "Symlink": true,
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if why, forbidden := forbiddenImports[path]; forbidden {
				t.Errorf("%s imports %q: %s", fset.Position(imp.Pos()), path, why)
			}
			if imp.Name != nil && path == "os" {
				t.Errorf("%s imports os under another name", fset.Position(imp.Pos()))
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "os" && !allowedOS[sel.Sel.Name] {
				t.Errorf("%s uses os.%s: the disk is reached only through the Root", fset.Position(sel.Pos()), sel.Sel.Name)
			}
			if writes[sel.Sel.Name] {
				t.Errorf("%s calls %s: the library is never written", fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no source file was checked")
	}
}
