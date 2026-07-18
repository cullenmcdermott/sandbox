// CANONICAL TEST — do not weaken.
package chat

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ORACLE: no lipgloss.NewStyle() call site lives inside a method whose name contains
// "Render" (per-frame render paths must use pre-built styles).
func TestNoNewStyleInRenderPaths(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(here), "..")
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, root, nil, 0)
	if err != nil {
		t.Fatalf("parse dir: %v", err)
	}
	for _, pkg := range pkgs {
		for fname, f := range pkg.Files {
			if strings.HasSuffix(fname, "_test.go") {
				continue
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if fn, ok := n.(*ast.FuncDecl); ok {
					if !strings.Contains(fn.Name.Name, "Render") {
						return true
					}
					ast.Inspect(fn.Body, func(m ast.Node) bool {
						if call, ok := m.(*ast.CallExpr); ok {
							if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
								if sel.Sel.Name == "NewStyle" {
									if id, ok := sel.X.(*ast.Ident); ok && id.Name == "lipgloss" {
										t.Errorf("%s: %s calls lipgloss.NewStyle()", fname, fn.Name.Name)
									}
								}
							}
						}
						return true
					})
				}
				return true
			})
		}
	}
}
