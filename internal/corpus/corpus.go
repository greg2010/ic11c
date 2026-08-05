// Package corpus holds the whole MicroC programs the compiler is exercised
// over end to end.
//
// [Programs] returns them without touching the filesystem; [Dir] resolves the
// checked-in directory for callers that hand a program to another process.
package corpus

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

// ModulePath is the corpus directory relative to the module root, for tools and
// scripts that resolve paths from there rather than from a package directory.
const ModulePath = "internal/corpus/programs"

// programsDir is the last element of [ModulePath], which is what the embedded
// tree is rooted at.
const programsDir = "programs"

// files is the corpus as the build saw it. The pattern does not compile against
// an empty directory, which is what stops a corpus that has gone missing from
// reaching a caller as a run over no programs at all.
//
//go:embed programs/*.c
var files embed.FS

// Program is one MicroC program: the file name it is checked in under, and its
// text.
type Program struct {
	Name   string
	Source string
}

// Programs returns every program in the corpus, ordered by name.
//
// The set is fixed when this package is built, so it is the same set a caller
// globbing [Dir] sees, and it is never empty.
func Programs() ([]Program, error) {
	entries, err := fs.ReadDir(files, programsDir)
	if err != nil {
		return nil, fmt.Errorf("reading the embedded corpus: %w", err)
	}
	programs := make([]Program, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		src, err := fs.ReadFile(files, path.Join(programsDir, name))
		if err != nil {
			return nil, fmt.Errorf("reading %s from the embedded corpus: %w", name, err)
		}
		programs = append(programs, Program{Name: name, Source: string(src)})
	}
	return programs, nil
}

// Dir returns the absolute path of the directory the corpus is checked in at,
// for callers that pass a program to another process rather than reading its
// text. It errors rather than naming a directory that is not there, so a
// stale glob does not quietly return zero programs.
func Dir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locating the corpus: %w", err)
	}
	root, err := moduleRoot(wd)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, filepath.FromSlash(ModulePath))
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("locating the corpus: %w", err)
	}
	return dir, nil
}

// moduleRoot returns the nearest directory at or above start holding a go.mod.
func moduleRoot(start string) (string, error) {
	for dir := start; ; {
		_, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("looking for the module root above %s: %w", start, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("looking for the module root: no go.mod at or above %s", start)
		}
		dir = parent
	}
}
