package media

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The Runner is the only way the product starts a process (DESIGN.md I8):
// nothing else can then run a tool without a context, a timeout, a bounded
// standard error and a process group of its own. The test reads the
// sources of the product (cmd and internal, tests excluded) and refuses
// os/exec, and the calls of os and syscall that start a process, anywhere
// but in the two files of the Runner.
//
// It also holds this package to T2: the adapter never asks a file for its
// name, so a name cannot become an argument of a tool.
func TestOnlyTheRunnerStartsProcesses(t *testing.T) {
	runner := map[string]bool{
		filepath.Join("internal", "media", "runner.go"):       true,
		filepath.Join("internal", "media", "runner_linux.go"): true,
	}
	starts := map[string]map[string]bool{
		"os":      {"StartProcess": true},
		"syscall": {"ForkExec": true, "StartProcess": true, "Exec": true},
	}
	root := filepath.Join("..", "..")
	checked, sawRunner := 0, 0
	fset := token.NewFileSet()
	for _, tree := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			checked++
			if runner[rel] {
				sawRunner++
			}
			inMedia := filepath.Dir(rel) == filepath.Join("internal", "media")
			for _, imp := range file.Imports {
				name, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				if name == "os/exec" && !runner[rel] {
					t.Errorf("%s imports os/exec: only the Runner of internal/media starts processes", rel)
				}
			}
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && starts[pkg.Name][sel.Sel.Name] && !runner[rel] {
					t.Errorf("%s uses %s.%s: only the Runner of internal/media starts processes",
						fset.Position(sel.Pos()), pkg.Name, sel.Sel.Name)
				}
				if inMedia && sel.Sel.Name == "Name" {
					t.Errorf("%s reads a name: a tool is given a descriptor, never the name of a file", fset.Position(sel.Pos()))
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked < 10 || sawRunner != len(runner) {
		t.Fatalf("%d source files were checked, %d of them the Runner's: the test does not see the sources", checked, sawRunner)
	}
}
