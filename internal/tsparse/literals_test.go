package tsparse_test

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/source"
	"github.com/greg2010/ic11c/internal/tsparse"
)

// sourceRoots are the trees whose tests are read for the MicroC written inside
// them.
var sourceRoots = []string{"..", filepath.Join("..", "..", "cmd")}

// minAccepted and minCShaped are the floors the sweep is held to, so an
// extraction that stopped finding anything reads as a failure. minCShaped
// counts only sources the C grammar reads whole, since the tests are full of
// strings — mnemonics, paths, prose — that die at the first token.
const (
	minAccepted = 300
	minCShaped  = 200
)

// TestTheMicroCTheTestsWriteIsRead widens the front end's coverage past the
// shipped corpus: every Go string literal in the tree's tests that parses as
// a MicroC translation unit is a program this front end has to answer. It
// establishes that the decision is answerable and usable, not that it is right.
func TestTheMicroCTheTestsWriteIsRead(t *testing.T) {
	accepted, refused := microCLiterals(t)
	if len(accepted) < minAccepted {
		t.Fatalf("read %d programs, want at least %d; either the extraction has stopped reading what the tests write or the front end has stopped reading MicroC", len(accepted), minAccepted)
	}
	cShaped := 0
	for _, program := range refused {
		if tsparse.ReadsAsC(program.src) {
			cShaped++
		}
	}
	if cShaped < minCShaped {
		t.Fatalf("refused %d programs the C grammar reads whole, want at least %d; the extraction has stopped reaching the constructs this front end has to refuse on its own", cShaped, minCShaped)
	}
	for _, program := range accepted {
		tree, _, err := tsparse.Parse("test.c", program.src)
		if err != nil {
			t.Errorf("tsparse.Parse failed on the program at %s: %v", program.at, err)
			continue
		}
		checkEveryPosition(t, tree)
	}
}

// program is one MicroC source found in a Go test, with where it was written.
type program struct {
	src string
	at  string
}

// microCLiterals collects every string literal in the tree's tests this
// front end has an answer for, split by what that answer was: accepted
// holds literals read as a translation unit with a declaration, refused the
// ones reported on. A literal neither read nor objected to is in neither list.
func microCLiterals(t *testing.T) (accepted, refused []program) {
	t.Helper()
	seen := map[string]bool{}
	fset := token.NewFileSet()

	for _, root := range sourceRoots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, parseErr := goparser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				return nil
			}
			ast.Inspect(file, func(n ast.Node) bool {
				lit, isLit := n.(*ast.BasicLit)
				if !isLit || lit.Kind != token.STRING {
					return true
				}
				src, unquoteErr := strconv.Unquote(lit.Value)
				if unquoteErr != nil || seen[src] {
					return true
				}
				seen[src] = true
				at := fset.Position(lit.Pos()).String()
				parsed, diags, parseErr := tsparse.Parse("test.c", src)
				if parseErr != nil {
					t.Fatalf("tsparse.Parse failed on the literal at %s: %v", at, parseErr)
				}
				checkDiagnosticsAreInSource(t, "the program at "+at, src, diags)
				found := program{src: src, at: at}
				switch {
				case len(diags) != 0:
					refused = append(refused, found)
				case len(parsed.Decls) > 0:
					accepted = append(accepted, found)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	return accepted, refused
}

// checkDiagnosticsAreInSource holds every refusal to a byte the source has:
// a position past the end names nothing a programmer can look at. what
// names the program for a reader of the failure.
func checkDiagnosticsAreInSource(t *testing.T, what, src string, diags source.DiagnosticList) {
	t.Helper()
	for _, d := range diags {
		if d.Pos.Offset < 0 || d.Pos.Offset > len(src) {
			t.Errorf("%s draws %q, which sits at offset %d of a source of %d bytes", what, d, d.Pos.Offset, len(src))
		}
	}
}
