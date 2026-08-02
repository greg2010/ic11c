package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// csharpExt is the extension the decompiler gives every file it writes.
const csharpExt = ".cs"

// sourceTree reads an assembly decompiled to one file per top-level type under
// a directory per namespace component, which is the layout
// `ilspycmd --nested-directories --project` writes.
//
// Reading a tree rather than invoking the decompiler per type is what keeps
// this program free of the .NET toolchain: the container that fetched the
// assembly runs the decompiler once and hands the result over.
type sourceTree struct {
	root string
	// byName indexes files by bare type name, so a type the game source
	// mentions without its namespace can still be found.
	byName map[string][]string
}

// newSourceTree indexes the decompiled C# under root. It fails on an empty
// tree, which is what a decompilation that produced nothing looks like from
// here.
func newSourceTree(root string) (*sourceTree, error) {
	tree := &sourceTree{root: root, byName: make(map[string][]string)}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != csharpExt {
			return nil
		}
		name := strings.TrimSuffix(filepath.Base(path), csharpExt)
		tree.byName[name] = append(tree.byName[name], path)
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

// qualified returns the source of a type named with its full namespace, which
// the layout maps directly onto a path.
func (t *sourceTree) qualified(fullName string) (string, error) {
	path := filepath.Join(t.root, filepath.Join(strings.Split(fullName, ".")...)+csharpExt)
	src, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read type %s: %w", fullName, err)
	}
	return string(src), nil
}

// enumType returns the source declaring the named enum along with the name it
// is declared under. The game source names an enum the way C# resolves it
// against the using directives of the file mentioning it, so the reference has
// no namespace and reads `Outer.Nested` when the enum is nested. A nested enum
// is declared inside its outer type's file, which is why the two differ.
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
