package sema_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10tables"
	"github.com/greg2010/ic11c/internal/parser"
	"github.com/greg2010/ic11c/internal/sema"
)

// fixtureDir holds the programs the parser tests exercise. They are whole
// MicroC programs, so they are the natural end-to-end check that analysis
// accepts what the language admits.
const fixtureDir = "../parser/testdata"

// TestAnalyzeParserFixtures checks every program the parser accepts cleanly.
//
// It resolves machine names through the generated tables rather than through
// the handful testTables carries. A corpus program names whatever property the
// device it drives actually has, so a stub would decide which programs may join
// the corpus rather than checking the ones that are in it.
func TestAnalyzeParserFixtures(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.c"))
	if err != nil {
		t.Fatalf("globbing %s: %v", fixtureDir, err)
	}
	if len(paths) == 0 {
		t.Fatalf("no programs found in %s", fixtureDir)
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			file, diags := parser.Parse(path, string(src))
			if len(diags) != 0 {
				t.Fatalf("%s did not parse cleanly:\n%s", path, diags.String())
			}
			_, diags, err = sema.Analyze(context.Background(), file, ic10tables.Tables{})
			if err != nil {
				t.Fatalf("analyzing %s: %v", path, err)
			}
			if len(diags) != 0 {
				t.Errorf("%s did not analyze cleanly:\n%s", path, diags.String())
			}
		})
	}
}
