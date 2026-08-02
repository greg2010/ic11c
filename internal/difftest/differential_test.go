package difftest

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/greg2010/ic11c/internal/oracle"
)

// Environment variables that widen a corpus run. A corpus that only runs for an
// hour is a corpus nobody runs, so the default is small enough for the ordinary
// suite and the long run is one variable away.
const (
	// envPrograms is how many programs each corpus generates.
	envPrograms = "IC11C_DIFFTEST_PROGRAMS"
	// envSeed is the first seed each corpus generates from.
	envSeed = "IC11C_DIFFTEST_SEED"
)

const (
	// defaultPrograms keeps a corpus inside the ordinary suite's budget.
	defaultPrograms = 1000
	// shortPrograms is what -short draws.
	shortPrograms = 50
	// defaultSeed is fixed so that an unconfigured run is reproducible run to
	// run, not merely reproducible from a seed it printed.
	defaultSeed = 1
	// programTimeout bounds one program on both sides.
	programTimeout = 30 * time.Second
	// maxReportedFailures stops a systematic disagreement from burying the
	// first, most useful, few.
	maxReportedFailures = 5
)

// TestValueCorpus compares terminating, fault-free programs on final machine
// state.
func TestValueCorpus(t *testing.T) {
	runCorpus(t, oracle.IC10Emu, KindValue, ValueProgram)
}

// TestFaultCorpus compares deliberately faulting programs on error type and
// faulting line.
func TestFaultCorpus(t *testing.T) {
	runCorpus(t, oracle.IC10Emu, KindFault, FaultProgram)
}

// TestReportsWhatIsNotCompared prints the disagreements the generators work
// around, so that a passing corpus does not read as a clean bill of health.
// Each one is a candidate for the divergence registry or for a harness fix.
func TestReportsWhatIsNotCompared(t *testing.T) {
	for _, divergence := range unregisteredDivergences {
		t.Logf("%s: %s\n  ours:    %s\n  harness: %s\n  program: %s",
			divergence.Harness, divergence.Summary, divergence.Ours, divergence.Theirs,
			divergence.Source)
	}
	for _, mnemonic := range ExcludedMnemonics() {
		reason, _ := Excluded(mnemonic)
		t.Logf("never generated: %s — %s", mnemonic, reason)
	}
}

func runCorpus(t *testing.T, harness oracle.Harness, kind string, generate func(uint64) Program) {
	t.Helper()
	client := oracle.Shared(t, harness)
	seed, count := corpusConfig(t)

	coverage := make(Coverage)
	failures := 0
	for i := range count {
		program := generate(seed + uint64(i))
		coverage.Add(program)
		if !compareOne(t, client, harness, program) {
			failures++
			if failures >= maxReportedFailures {
				t.Fatalf("stopping after %d disagreements out of %d programs; "+
					"rerun with %s=1 and %s set to a failing seed to work one at a time",
					failures, i+1, envPrograms, envSeed)
			}
		}
	}
	t.Logf("%s corpus, %d programs from seed %d against %s\n%s",
		kind, count, seed, harness, coverage.Report())
}

// compareOne runs one program on both implementations and reports whether the
// registry accounted for every difference.
//
// The corpus's soundness rests on the exclusion policy staying disjoint from
// the registry's triggers, and a trigger matches any bare token, operands
// included. An entry added for a token a generator emits freely would excuse
// every field of every program, so the guard runs per program rather than
// resting on the two lists having been read side by side.
func compareOne(t *testing.T, client *oracle.Client, harness oracle.Harness, program Program) bool {
	t.Helper()
	oracle.RequireComparable(t, harness, program.Source)

	ctx, cancel := context.WithTimeout(context.Background(), programTimeout)
	defer cancel()

	ours, err := Run(ctx, program.Source, program.Initial, 0)
	if err != nil {
		t.Fatalf("%s: interpreter: %v\n%s", program, err, program.Source)
	}
	theirs, err := client.Run(ctx, program.Source, program.Initial, 0)
	if err != nil {
		t.Fatalf("%s: %s: %v\n%s", program, harness, err, program.Source)
	}

	report := oracle.Check(t, harness, program.Source, ours, theirs)
	if !report.OK() {
		t.Errorf("reproduce with %s", program)
	}
	return report.OK()
}

func corpusConfig(tb testing.TB) (seed uint64, count int) {
	tb.Helper()
	seed, count = defaultSeed, defaultPrograms
	if testing.Short() {
		count = shortPrograms
	}
	if raw := os.Getenv(envSeed); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			tb.Fatalf("%s=%q: %v", envSeed, raw, err)
		}
		seed = parsed
	}
	if raw := os.Getenv(envPrograms); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			tb.Fatalf("%s=%q: want a positive count", envPrograms, raw)
		}
		count = parsed
	}
	return seed, count
}
