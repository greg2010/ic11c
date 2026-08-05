package ic10_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	// rosterPackage is the leaf tools/isagen writes, and rosterFile the file in
	// it that holds the prefab roster. Everything that file declares is reserved.
	rosterPackage = "github.com/greg2010/ic11c/internal/isa"
	rosterFile    = "devices.gen.go"

	modulePath = "github.com/greg2010/ic11c"
)

// allowedPackages may name a roster declaration: the leaf declares them, and
// internal/ic10 is the one consumer.
var allowedPackages = []string{rosterPackage, modulePath + "/internal/ic10"}

// TestOnlyIC10ReachesTheRoster fails if any other package in the module
// names a declaration of the prefab roster. The roster answers what the
// game allows, not what it refuses, and many properties are settled from
// live state: a consumer comparing an access pair directly
// (`allows == isa.AccessReadWrite`) reads every one of those as a denial
// and rejects working programs. internal/ic10's four refusal queries
// cannot express that mistake; the instruction tables are not covered
// since an opcode carries no answer a caller can read the wrong way round.
func TestOnlyIC10ReachesTheRoster(t *testing.T) {
	packages := modulePackages(t)
	reserved := rosterDeclarations(t, packages)

	for _, pkg := range packages {
		if slices.Contains(allowedPackages, pkg.importPath) {
			continue
		}
		for _, file := range goFiles(t, pkg.dir) {
			for _, use := range rosterUses(t, file, reserved) {
				t.Errorf("%s names %s.%s, which only internal/ic10 may: ask internal/ic10 what a property refuses instead",
					use.position, use.qualifier, use.name)
			}
		}
	}
}

type modulePackage struct {
	importPath string
	dir        string
}

// modulePackages lists every package of this module the build reaches,
// including the ones only a test reaches. Shells the toolchain rather than
// walking the tree, so what is scanned is what is built.
func modulePackages(t *testing.T) []modulePackage {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", "list", "-deps", "-test",
		"-f", "{{if .Module}}{{.Module.Path}}\t{{.ImportPath}}\t{{.Dir}}{{end}}", modulePath+"/...")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("list the module's packages: %v", err)
	}

	seen := make(map[string]bool)
	var packages []modulePackage
	for line := range strings.Lines(string(out)) {
		fields := strings.Split(strings.TrimRight(line, "\n"), "\t")
		if len(fields) != 3 || fields[0] != modulePath {
			continue
		}
		// A package and its two test variants share one directory and one set of
		// files, so scanning it once covers all three. The import path a test
		// variant carries is the package's own with a suffix, which the allow
		// list would not match.
		if seen[fields[2]] {
			continue
		}
		seen[fields[2]] = true
		packages = append(packages, modulePackage{
			importPath: strings.TrimSuffix(fields[1], ".test"),
			dir:        fields[2],
		})
	}
	if len(packages) == 0 {
		t.Fatal("the toolchain listed no package of this module, so this scanned nothing")
	}
	return packages
}

// rosterDeclarations answers every exported name the roster file
// declares, read out of the file rather than listed here so a
// re-extraction that introduces a type or constant reserves it too.
func rosterDeclarations(t *testing.T, packages []modulePackage) map[string]bool {
	t.Helper()
	index := slices.IndexFunc(packages, func(p modulePackage) bool { return p.importPath == rosterPackage })
	if index < 0 {
		t.Fatalf("the toolchain does not list %s, so there is no roster to reserve", rosterPackage)
	}

	path := filepath.Join(packages[index].dir, rosterFile)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	reserved := make(map[string]bool)
	for _, decl := range file.Decls {
		general, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range general.Specs {
			switch spec := spec.(type) {
			case *ast.TypeSpec:
				addExported(reserved, spec.Name)
			case *ast.ValueSpec:
				for _, name := range spec.Names {
					addExported(reserved, name)
				}
			}
		}
	}
	if len(reserved) == 0 {
		t.Fatalf("%s declares nothing exported, so this reserves nothing", path)
	}
	return reserved
}

func addExported(reserved map[string]bool, name *ast.Ident) {
	if name.IsExported() {
		reserved[name.Name] = true
	}
}

// goFiles lists the Go source in one directory, tests included and without
// regard to build tags: a reference the build excludes today is one that
// compiles the moment a tag changes.
func goFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	return files
}

type rosterUse struct {
	position  string
	qualifier string
	name      string
}

// rosterUses reports every selector in one file that names a reserved
// declaration through the roster's import, under whatever name the file imports
// it as.
func rosterUses(t *testing.T, path string, reserved map[string]bool) []rosterUse {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	qualifier := rosterQualifier(t, path, file)
	if qualifier == "" {
		return nil
	}

	var uses []rosterUse
	ast.Inspect(file, func(n ast.Node) bool {
		selector, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		if !ok || ident.Name != qualifier || !reserved[selector.Sel.Name] {
			return true
		}
		uses = append(uses, rosterUse{
			position:  fset.Position(selector.Pos()).String(),
			qualifier: qualifier,
			name:      selector.Sel.Name,
		})
		return true
	})
	return uses
}

// rosterQualifier answers the name a file reaches the roster's package by, and
// is empty for a file that does not import it. A dot import is refused rather
// than resolved: every reserved name would then be reachable unqualified, and
// this scan would report none of them.
func rosterQualifier(t *testing.T, path string, file *ast.File) string {
	t.Helper()
	for _, spec := range file.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil || imported != rosterPackage {
			continue
		}
		switch {
		case spec.Name == nil:
			return "isa"
		case spec.Name.Name == ".":
			t.Errorf("%s dot-imports %s, which puts every reserved name in scope unqualified", path, rosterPackage)
			return ""
		default:
			return spec.Name.Name
		}
	}
	return ""
}
