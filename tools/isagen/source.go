package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// csharpExt is the extension the decompiler gives every file it writes.
const csharpExt = ".cs"

// sourceTree reads an assembly decompiled to one file per top-level type
// under a directory per namespace component, the layout
// `ilspycmd --nested-directories --project` writes. Reading a tree rather
// than invoking the decompiler per type keeps this program free of the .NET
// toolchain: the container that fetched the assembly runs it once.
type sourceTree struct {
	root string
	// byName indexes files by bare type name, so a type the game source
	// mentions without its namespace can still be found.
	byName map[string][]string
	// files is every path the walk found. Whether the tree declares a type is
	// decided against this rather than against a failed read, so that a file
	// that is there and will not open cannot be mistaken for a type from
	// another assembly.
	files map[string]bool
}

// newSourceTree indexes the decompiled C# under root. It fails on an empty
// tree, which is what a decompilation that produced nothing looks like from
// here, and on a tree holding a file the decompiler did not write.
func newSourceTree(root string) (*sourceTree, error) {
	tree := &sourceTree{root: root, byName: make(map[string][]string), files: make(map[string]bool)}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != csharpExt {
			return nil
		}
		if _, named := tree.typeName(path); !named {
			return foreignFile(root, path)
		}
		name := strings.TrimSuffix(filepath.Base(path), csharpExt)
		tree.byName[name] = append(tree.byName[name], path)
		tree.files[filepath.Clean(path)] = true
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("index decompiled source under %s: %w", root, err)
	}
	if len(tree.byName) == 0 {
		return nil, fmt.Errorf("decompiled source under %s: no %s files: %w", root, csharpExt, errNotFound)
	}
	return tree, nil
}

// typeName reads off a path the full name of the type declared in it, and
// reports whether the path names one at all. The decompiler writes one file
// per top-level type under a directory per namespace component, so the two
// spellings are the same string with a different separator; nothing it
// writes carries a '.' in a component, which is what a foreign file breaks.
func (t *sourceTree) typeName(path string) (string, bool) {
	rel, err := filepath.Rel(t.root, path)
	if err != nil {
		return "", false
	}
	trimmed := strings.TrimSuffix(rel, csharpExt)
	if trimmed == rel || strings.Contains(trimmed, ".") || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return strings.ReplaceAll(trimmed, string(filepath.Separator), "."), true
}

// types names every type the tree holds, in a fixed order, so that a scan over
// the whole tree reads the index rather than walking it a second time under a
// second copy of the path mapping.
func (t *sourceTree) types() []string {
	names := make([]string, 0, len(t.files))
	for path := range t.files {
		if name, named := t.typeName(path); named {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// foreignFile reports a file under a decompiled tree that the decompiler did
// not write, naming it rather than whatever the scan reading it went on to
// fail at: a generated tree carrying foreign content answers for something
// other than the game.
func foreignFile(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	return fmt.Errorf("%s names no type, so the decompiler did not write it: the layout is one %s per type "+
		"under a directory per namespace component and no component of it carries a '.'; a `dotnet build` of "+
		"the decompiler's own assembly.csproj leaves obj/ and bin/ here, which is the usual way this happens",
		rel, csharpExt)
}

// qualified returns the source of a type named with its full namespace,
// which the layout maps directly onto a path. A name the tree holds no file
// for yields errNotFound, the ordinary outcome for a type from another
// assembly; any other error means the file is there and could not be read.
func (t *sourceTree) qualified(fullName string) (string, error) {
	path := filepath.Join(t.root, filepath.Join(strings.Split(fullName, ".")...)+csharpExt)
	if !t.files[path] {
		return "", fmt.Errorf("type %s under %s: %w", fullName, t.root, errNotFound)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read type %s: %w", fullName, err)
	}
	return string(src), nil
}

// declares reports whether the tree holds a file for a bare type name, in any
// namespace.
func (t *sourceTree) declares(name string) bool {
	return len(t.byName[name]) > 0
}

// enumType returns the source declaring the named enum along with the name
// it is declared under. The reference has no namespace, reading
// `Outer.Nested` when the enum is nested, since a nested enum is declared
// inside its outer type's file rather than one of its own.
func (t *sourceTree) enumType(ref string) (src, name string, err error) {
	outer, inner, nested := strings.Cut(ref, ".")
	name = ref
	if nested {
		if strings.Contains(inner, ".") {
			return "", "", fmt.Errorf("type %s: only a nested type may be qualified", ref)
		}
		name = inner
	}

	paths := t.byName[outer]
	switch {
	case len(paths) == 0:
		return "", "", fmt.Errorf("type %s under %s: %w", ref, t.root, errNotFound)
	case len(paths) > 1:
		return "", "", fmt.Errorf("type %s is declared in %s", ref, strings.Join(paths, " and "))
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		return "", "", fmt.Errorf("read type %s: %w", ref, err)
	}
	return string(data), name, nil
}
