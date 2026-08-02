package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// moduleRoot is where the paths in gameTypes are rooted, two levels above this
// package.
const moduleRoot = "../.."

// interpreterDir holds the transliterated chip, and with it the only part of
// the mapping that can be checked in the other direction: what the interpreter
// derives from the game is written down in its own instruction table.
const interpreterDir = "internal/vm"

// TestGameTypesCoverTheInterpreter checks the mapping the other way round: that
// no file holding transliterated chip behaviour is missing from it.
//
// TestGameTypesMapping checks that every listed path exists, which a coverage
// claim needs the reverse of. What counts as transliterated is not asserted by
// hand here — a second list would drift the same way the first one can. It is
// derived from the instruction table, which already records the C# class behind
// every opcode: the files reached are the ones declaring the code the table
// dispatches into, followed transitively through the package's own
// declarations. A new file holding per-opcode semantics is reachable from the
// table by construction, so it cannot be added without appearing here.
//
// A generated file is out: isa:extract rewrites those from the game tables, so
// a game change reaches them without a fingerprint to prompt it. So is anything
// the table does not reach, which is why this is a floor rather than the whole
// mapping.
func TestGameTypesCoverTheInterpreter(t *testing.T) {
	listed := make(map[string]bool)
	for _, gt := range gameTypes {
		for _, path := range gt.backs {
			listed[path] = true
		}
	}

	reached := interpreterSources(t)
	if len(reached) == 0 {
		t.Fatal("the instruction table reached no source file, so this asserts nothing")
	}
	for _, name := range slices.Sorted(maps.Keys(reached)) {
		path := interpreterDir + "/" + name
		if !listed[path] {
			t.Errorf("%s holds code the instruction table dispatches into and no gameTypes entry names it; add it to the entry for the C# type it transliterates", path)
		}
	}
}

// instructionTable is the package level name the walk starts from. It records
// the C# class behind every opcode alongside the builder that implements it,
// which is what makes it the anchor for what counts as transliterated.
const instructionTable = "instructions"

// declSite is one top level declaration and the file it is written in.
type declSite struct {
	file string
	decl ast.Decl
}

// interpreterSources reports the file names in interpreterDir that declare code
// the instruction table reaches, transitively through package level names.
func interpreterSources(t *testing.T) map[string]bool {
	t.Helper()
	files := parseInterpreter(t)

	// A method is collected under its receiver type, so reaching a type reaches
	// the behaviour hung off it wherever that is written.
	declaredIn := make(map[string][]declSite)
	for name, file := range files {
		for _, decl := range file.Decls {
			for _, declared := range declarations(decl) {
				declaredIn[declared] = append(declaredIn[declared], declSite{file: name, decl: decl})
			}
		}
	}
	if len(declaredIn[instructionTable]) == 0 {
		t.Fatalf("%s declares no %s, which is what this test reads to decide what is transliterated", interpreterDir, instructionTable)
	}

	reached := make(map[string]bool)
	visited := make(map[string]bool)
	queue := []string{instructionTable}
	for len(queue) > 0 {
		name := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if visited[name] {
			continue
		}
		visited[name] = true
		for _, site := range declaredIn[name] {
			reached[site.file] = true
			queue = append(queue, referenced(site.decl, declaredIn)...)
		}
	}
	return reached
}

// parseInterpreter reads the package's own source, keyed by file name. Test
// files are out because a test checks behaviour rather than transliterating it,
// and generated files are out because isa:extract owns them.
func parseInterpreter(t *testing.T) map[string]*ast.File {
	t.Helper()
	dir := filepath.Join(moduleRoot, interpreterDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", interpreterDir, err)
	}
	fset := token.NewFileSet()
	files := make(map[string]*ast.File, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		if ast.IsGenerated(file) {
			continue
		}
		files[name] = file
	}
	return files
}

// declarations names what one top level declaration introduces. A method is
// named by its receiver type rather than by itself, since a bare method name is
// reached through a selector and says nothing about which type it belongs to.
func declarations(decl ast.Decl) []string {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Recv == nil {
			return []string{d.Name.Name}
		}
		if name := receiverType(d.Recv); name != "" {
			return []string{name}
		}
		return nil
	case *ast.GenDecl:
		var names []string
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				names = append(names, s.Name.Name)
			case *ast.ValueSpec:
				for _, ident := range s.Names {
					names = append(names, ident.Name)
				}
			}
		}
		return names
	default:
		return nil
	}
}

// receiverType reads the type name off a method receiver, through the pointer
// and the type parameters a declaration may spell it with.
func receiverType(recv *ast.FieldList) string {
	if len(recv.List) == 0 {
		return ""
	}
	expr := recv.List[0].Type
	for {
		switch t := expr.(type) {
		case *ast.StarExpr:
			expr = t.X
		case *ast.IndexExpr:
			expr = t.X
		case *ast.IndexListExpr:
			expr = t.X
		case *ast.Ident:
			return t.Name
		default:
			return ""
		}
	}
}

// referenced lists the package level names one declaration mentions.
func referenced(decl ast.Decl, declaredIn map[string][]declSite) []string {
	var names []string
	ast.Inspect(decl, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if _, declared := declaredIn[ident.Name]; declared {
			names = append(names, ident.Name)
		}
		return true
	})
	return names
}

func TestGameTypesMapping(t *testing.T) {
	seen := make(map[string]bool, len(gameTypes))
	for _, gt := range gameTypes {
		t.Run(gt.name, func(t *testing.T) {
			if seen[gt.name] {
				t.Errorf("type %s is mapped twice", gt.name)
			}
			seen[gt.name] = true
			if len(gt.backs) == 0 {
				t.Errorf("type %s names no Go file it backs", gt.name)
			}
			for _, path := range gt.backs {
				// A mapping that names a file which has been renamed away is
				// worse than no mapping: it reads as coverage that is not there.
				if _, err := os.Stat(filepath.Join(moduleRoot, path)); err != nil {
					t.Errorf("backs %s: %v", path, err)
				}
			}
		})
	}
}

// digestFixture writes a source tree holding every mapped type, so that
// rendering can be exercised without the decompiled assembly.
func digestFixture(t *testing.T, bodies map[string]string) *sourceTree {
	t.Helper()
	root := t.TempDir()
	for _, gt := range gameTypes {
		parts := strings.Split(gt.name, ".")
		path := filepath.Join(root, filepath.Join(parts...)+csharpExt)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create fixture directory: %v", err)
		}
		body, ok := bodies[gt.name]
		if !ok {
			body = "\tpublic int Placeholder;\n"
		}
		src := "namespace " + strings.Join(parts[:len(parts)-1], ".") + ";\n\n" +
			"public class " + parts[len(parts)-1] + "\n{\n" + body + "}\n"
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	tree, err := newSourceTree(root)
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}
	return tree
}

func TestRenderDigest(t *testing.T) {
	first := gameTypes[0].name
	tree := digestFixture(t, map[string]string{
		first: "\tprivate class _ZED_Operation\n\t{\n\t}\n\n\tprivate class _ALPHA_Operation\n\t{\n\t}\n",
	})

	data, err := renderDigest(tree, "123", "0.1.2.3")
	if err != nil {
		t.Fatalf("renderDigest: %v", err)
	}
	got := string(data)

	for _, want := range []string{
		"manifest 123\n",
		"assembly 0.1.2.3\n",
		"backs " + strings.Join(gameTypes[0].backs, " ") + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("digest does not contain %q", want)
		}
	}

	var types []string
	var records []string
	for _, line := range strings.Split(got, "\n") {
		switch {
		case strings.HasPrefix(line, "type "):
			types = append(types, strings.Fields(line)[2])
		case strings.HasPrefix(line, "\t"):
			records = append(records, strings.Fields(line)[1])
		}
	}
	if len(types) != len(gameTypes) {
		t.Fatalf("got %d type records, want %d", len(types), len(gameTypes))
	}
	for i, gt := range gameTypes {
		if types[i] != gt.name {
			t.Errorf("type record %d = %s, want %s", i, types[i], gt.name)
		}
	}
	// The two nested classes are written in reverse alphabetical order, so
	// their records prove the sort rather than echoing the source order.
	if len(records) < 2 || records[0] != "_ALPHA_Operation" || records[1] != "_ZED_Operation" {
		t.Errorf("member records = %q, want them sorted by path", records)
	}
}

func TestRenderDigestReportsAMissingType(t *testing.T) {
	tree := digestFixture(t, nil)
	if err := os.Remove(filepath.Join(tree.root,
		filepath.Join(strings.Split(gameTypes[0].name, ".")...)+csharpExt)); err != nil {
		t.Fatalf("remove fixture: %v", err)
	}
	if _, err := renderDigest(tree, "123", "0.1.2.3"); err == nil {
		t.Fatal("renderDigest succeeded with a mapped type missing, want an error")
	}
}

func TestWriteDigestInputErrors(t *testing.T) {
	dir := t.TempDir()
	assembly := filepath.Join(dir, "Assembly-CSharp.dll")
	if err := os.WriteFile(assembly, []byte("not a PE image"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	source := digestFixture(t, nil).root

	tests := []struct {
		name     string
		source   string
		assembly string
		manifest string
		wantErr  string
	}{
		{name: "no source", assembly: assembly, manifest: "123", wantErr: "--source"},
		{name: "no assembly", source: source, manifest: "123", wantErr: "--assembly"},
		{name: "no manifest", source: source, assembly: assembly, wantErr: "--manifest"},
		{name: "unreadable assembly", source: source, assembly: assembly, manifest: "123", wantErr: "PE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := writeDigest(tt.source, tt.assembly, tt.manifest, filepath.Join(dir, "out.digest"))
			if err == nil {
				t.Fatal("writeDigest succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestCopyGameSource(t *testing.T) {
	tree := digestFixture(t, nil)
	out := filepath.Join(t.TempDir(), "dump")
	if err := copyGameSource(tree.root, out); err != nil {
		t.Fatalf("copyGameSource: %v", err)
	}
	for _, gt := range gameTypes {
		path := filepath.Join(out, filepath.Join(strings.Split(gt.name, ".")...)+csharpExt)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		if !strings.Contains(string(data), "class "+gt.name[strings.LastIndexByte(gt.name, '.')+1:]) {
			t.Errorf("%s does not hold the type it is named for", path)
		}
	}
}

func TestCopyGameSourceInputErrors(t *testing.T) {
	tests := []struct {
		name   string
		source string
		out    string
	}{
		{name: "no source", out: t.TempDir()},
		{name: "no out", source: t.TempDir()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := copyGameSource(tt.source, tt.out); err == nil {
				t.Fatal("copyGameSource succeeded, want an error")
			}
		})
	}
}
