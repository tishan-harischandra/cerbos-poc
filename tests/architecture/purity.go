package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// PureEvaluationPackage is the repository-relative directory holding the
// composite UI capability evaluator (§12.2). It folds leaf outcomes the caller
// already obtained from the PDP into one composite verdict, and that is all it
// is allowed to do.
const PureEvaluationPackage = "libs/capabilityeval"

// ioPackages are the standard library packages through which a dependency on
// the outside world enters. The list is deliberately a denylist of entry
// points rather than an allowlist of pure packages: a new pure helper should
// not need this file edited, but a new socket should.
var ioPackages = []string{
	"os",
	"os/exec",
	"os/signal",
	"io",
	"io/fs",
	"io/ioutil",
	"bufio",
	"net",
	"net/http",
	"net/url",
	"database/sql",
	"log",
	"log/slog",
	"path/filepath",
	"syscall",
	"embed",
	"time",
	"math/rand",
	"crypto/rand",
}

// ioAllowedPrefixes are import paths that begin with a forbidden name but are
// not that package.
var ioAllowedPrefixes = []string{
	"golang.org/x/",
}

// catalogLoaderPrefix names the half of the capability catalog package that
// reads the filesystem. The evaluator may take the catalog's vocabulary - the
// expression and definition types - but calling a loader would put a file read
// back inside a function whose whole value is that it has none.
const catalogLoaderPrefix = "Load"

const catalogPackageName = "capabilitycatalog"

// ScanForIODependency reports every way a file in the pure evaluation package
// reaches for the outside world: an I/O standard library import, or a call into
// the capability catalog's file-loading half.
func ScanForIODependency(relativePath, source string) ([]Finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relativePath, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", relativePath, err)
	}

	forbidden := make(map[string]struct{}, len(ioPackages))
	for _, name := range ioPackages {
		forbidden[name] = struct{}{}
	}

	var findings []Finding
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			continue
		}
		if isIOAllowed(path) {
			continue
		}
		if _, isForbidden := forbidden[path]; !isForbidden {
			continue
		}
		findings = append(findings, Finding{
			File:   relativePath,
			Line:   fset.Position(imported.Pos()).Line,
			Symbol: path,
			Message: "the composite capability evaluator must stay a pure function of its " +
				"arguments; move anything that touches the outside world to a caller",
		})
	}

	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || pkg.Name != catalogPackageName {
			return true
		}
		if !strings.HasPrefix(selector.Sel.Name, catalogLoaderPrefix) {
			return true
		}
		findings = append(findings, Finding{
			File:   relativePath,
			Line:   fset.Position(selector.Sel.Pos()).Line,
			Symbol: pkg.Name + "." + selector.Sel.Name,
			Message: "the catalog's loaders read the filesystem; the evaluator takes an " +
				"already-loaded catalog as an argument instead",
		})
		return true
	})

	return findings, nil
}

func isIOAllowed(path string) bool {
	for _, prefix := range ioAllowedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
