package sema_test

import (
	"context"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/corpus"
	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/source"
	"github.com/greg2010/ic11c/internal/tsparse"
)

// corpusWarnings is every warning the corpus produces, by fixture and by a
// fragment of the message. A warning rejects nothing, so every one of these
// programs still compiles; they are written down because a fixture the compiler
// has something to say about is a finding about that fixture, and one nobody
// recorded is one nobody acts on.
//
// It is empty, which is the state the corpus is meant to be in: every fixture
// drives a device the game says answers what the fixture names. An entry here is
// a finding to act on rather than a category to fill.
var corpusWarnings = map[string][]string{}

// TestAnalyzeCorpus checks every whole program the compiler ships, which is the
// natural end-to-end check that analysis accepts what the language admits.
//
// It resolves machine names through the generated tables rather than through
// the handful testTables carries. A corpus program names whatever property the
// device it drives actually has, so a stub would decide which programs may join
// the corpus rather than checking the ones that are in it.
func TestAnalyzeCorpus(t *testing.T) {
	programs, err := corpus.Programs()
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}

	for _, program := range programs {
		t.Run(program.Name, func(t *testing.T) {
			file, diags, err := tsparse.Parse(program.Name, program.Source)
			if err != nil {
				t.Fatalf("parsing %s: %v", program.Name, err)
			}
			if len(diags) != 0 {
				t.Fatalf("%s did not parse cleanly:\n%s", program.Name, diags.String())
			}
			diags = analyzeFixture(t, file)
			if diags.HasErrors() {
				t.Fatalf("%s was rejected:\n%s", program.Name, diags.String())
			}
			checkWarnings(t, program.Name, diags)
		})
	}
}

func analyzeFixture(t *testing.T, file *ast.File) source.DiagnosticList {
	t.Helper()
	_, diags, err := sema.Analyze(context.Background(), file, sema.Shipped{})
	if err != nil {
		t.Fatalf("analyzing %s: %v", file.Name, err)
	}
	return diags
}

// checkWarnings holds a fixture to the warnings corpusWarnings records for it,
// so a new one fails rather than joining an accepted background.
func checkWarnings(t *testing.T, name string, diags source.DiagnosticList) {
	t.Helper()
	want := corpusWarnings[name]
	if len(diags) != len(want) {
		t.Fatalf("%s produced %d diagnostics, want %d:\n%s", name, len(diags), len(want), diags.String())
	}
	for i, fragment := range want {
		if !strings.Contains(diags[i].Msg, fragment) {
			t.Errorf("%s warning %d is %q, which does not name %q", name, i, diags[i].Msg, fragment)
		}
	}
}
