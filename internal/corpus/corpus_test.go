package corpus_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/corpus"
)

// TestTheTwoViewsHoldTheSameCorpus holds [corpus.Programs] and a glob over
// [corpus.Dir] to the same set, so a caller of either sees the same corpus.
func TestTheTwoViewsHoldTheSameCorpus(t *testing.T) {
	programs, err := corpus.Programs()
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}

	dir := dir(t)
	paths, err := filepath.Glob(filepath.Join(dir, "*.c"))
	if err != nil {
		t.Fatalf("globbing %s: %v", dir, err)
	}

	names := make([]string, len(paths))
	for i, path := range paths {
		names[i] = filepath.Base(path)
	}
	embedded := make([]string, len(programs))
	for i, program := range programs {
		embedded[i] = program.Name
	}
	if !slices.Equal(embedded, names) {
		t.Fatalf("the build captured %v and %s holds %v", embedded, dir, names)
	}

	for _, program := range programs {
		t.Run(program.Name, func(t *testing.T) {
			path := filepath.Join(dir, program.Name)
			onDisk, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			if string(onDisk) != program.Source {
				t.Errorf("%s differs from what the build captured", path)
			}
			if len(program.Source) == 0 {
				t.Errorf("%s is empty, so compiling it asserts nothing", program.Name)
			}
		})
	}
}

// TestDirHoldsTheEditorArgumentFile checks the one corpus file that is not a
// program and so is not embedded; only a glob over [corpus.Dir] notices it
// going missing.
func TestDirHoldsTheEditorArgumentFile(t *testing.T) {
	path := filepath.Join(dir(t), "compile_flags.txt")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("an editor opening a corpus program is configured by %s: %v", path, err)
	}
}

// TestDirAgreesWithModulePath holds [corpus.Dir] and [corpus.ModulePath] to
// naming the same directory.
func TestDirAgreesWithModulePath(t *testing.T) {
	got := filepath.ToSlash(dir(t))
	if !filepath.IsAbs(dir(t)) || !strings.HasSuffix(got, corpus.ModulePath) {
		t.Errorf("Dir() = %q, want an absolute path ending in %q", got, corpus.ModulePath)
	}
}

func dir(tb testing.TB) string {
	tb.Helper()
	dir, err := corpus.Dir()
	if err != nil {
		tb.Fatalf("%v", err)
	}
	return dir
}
