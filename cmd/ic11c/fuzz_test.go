package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/chiptest"
	"github.com/greg2010/ic11c/internal/devtrace"
	"github.com/greg2010/ic11c/internal/emit"
	"github.com/greg2010/ic11c/internal/ic10"
)

// Environment variables that widen a campaign. A corpus that only runs for an
// hour is a corpus nobody runs, so the default stays inside the ordinary
// suite's budget and a long run is one variable away.
const (
	// envGeneratedPrograms is how many programs the campaign generates.
	envGeneratedPrograms = "IC11C_MICROC_PROGRAMS"
	// envGeneratedSeed is the first seed it generates from.
	envGeneratedSeed = "IC11C_MICROC_SEED"
)

const (
	// generatedPrograms is what an unconfigured run compares. Each program is a
	// clang build, a native run, and two compilations and two chip runs.
	generatedPrograms = 12
	// generatedShortPrograms is what -short draws.
	generatedShortPrograms = 3
	// generatedSeed is fixed so an unconfigured run is reproducible run to run,
	// and not merely reproducible from a seed it printed.
	generatedSeed = 1
	// generatedSegments is how many turns of its control loop a generated
	// program is driven for. The stimulus period is longer, so no two segments
	// meet the same world and a program whose branches open on a reading takes
	// more than one of them.
	generatedSegments = 60
	// generatedWriteFloor is what a program has to write for its comparison to
	// have compared anything. Every generated program publishes unconditionally
	// at the end of its turn, so one write per segment is far below what any of
	// them produces and far above what a program that stopped early does.
	generatedWriteFloor = generatedSegments
	// generatedShrinkSteps bounds the reduction, so a program whose divergence
	// survives every removal still reports.
	generatedShrinkSteps = 200
	// generatedMaxReported stops a systematic disagreement from spending the
	// whole run shrinking the same defect out of every program. Each reduction
	// is a search over compilations and runs, so a campaign where everything
	// diverges costs far more than one where nothing does.
	generatedMaxReported = 2
)

// generatedConfiguration is one inlining rule a generated program is compiled
// and compared under.
type generatedConfiguration struct {
	// name distinguishes the subtest and the reproducer a divergence prints.
	name string
	opts options
}

// generatedConfigurations are the inlining rules every generated program is
// put through. The shipped rule alone would never emit a call: a generated
// function is never recursive, so every helper folds into main under it.
// The outlined rule forces a real call to exist instead.
var generatedConfigurations = []generatedConfiguration{
	{name: "inlined", opts: options{}},
	{name: "outlined", opts: options{outOfLineCallSites: generatedOutlineSites}},
}

// generatedOutlineSites is the call-site count the second configuration
// gives a function a real definition at. [options.outOfLineCallSites]
// outlines a function named by at least this many sites, so one is every
// function a program calls at all.
const generatedOutlineSites = 1

// generatedRefusal is one restriction of this compiler a generated program
// can meet: a valid MicroC program clang builds and this compiler will
// not, from the optimizer rewriting the source. Each carries a witness
// program, since the campaign's own tally can read zero at these sizes.
type generatedRefusal struct {
	// name identifies the entry in the tally, in a failure, and as the base name
	// of its witness.
	name string
	// marker is the part of the diagnostic that names it.
	marker string
	// reason is what the program met.
	reason string
	// witness is a program under [refusalWitnessDir] this compiler refuses with
	// marker. It is a reduction of a generated program rather than something
	// written by hand, since a shape the optimizer forms is not one a source can
	// ask for directly. Rederive it with [updateRefusalWitnesses].
	witness string
}

var generatedRefusals = []generatedRefusal{
	{
		name:    "two loads sunk into one selected address",
		marker:  "an address is a slot in one named object",
		reason:  "the optimizer sank two loads under a conditional into one load of a selected address, which designates either of two objects where the source designated one",
		witness: "sunk-loads.c",
	},
	{
		name:    "device operations merged over a runtime operand",
		marker:  "must be known at compile time",
		reason:  "the optimizer merged two device operations differing only in an operand the chip resolves when the line is assembled, leaving that operand a runtime value",
		witness: "merged-device-operation.c",
	},
	{
		name:    "a library intrinsic formed from written operators",
		marker:  "which the machine has no instruction for",
		reason:  "the optimizer formed a library intrinsic out of arithmetic the source wrote in operators",
		witness: "formed-intrinsic.c",
	},
}

// generatedRefusalCeiling is the reciprocal of the share of generated programs
// that may go uncompared. Past it the campaign is generating
// programs this compiler mostly cannot build, and what the corpus covers is not
// what it reports.
const generatedRefusalCeiling = 4

// classifyRefusal names the restriction a compilation failure met, if any. The
// first marker the diagnostic carries wins, which is only an answer at all
// while no marker contains another; TestRefusalMarkersTellTheEntriesApart holds
// them to that.
func classifyRefusal(err error) (generatedRefusal, bool) {
	for _, refusal := range generatedRefusals {
		if strings.Contains(err.Error(), refusal.marker) {
			return refusal, true
		}
	}
	return generatedRefusal{}, false
}

// refusals is what a campaign could not compare, and how much it tried to.
type refusals struct {
	attempted int
	byName    map[string]int
}

// record counts one program a corpus tried to build, and the restriction it
// met when this compiler refused it for one the registry names. Both
// counters move here, so a denominator maintained at the call sites cannot
// drift from a later caller adding a refusal without it.
func (r *refusals) record(err error) (generatedRefusal, bool) {
	r.attempted++
	if err == nil {
		return generatedRefusal{}, false
	}
	refusal, known := classifyRefusal(err)
	if !known {
		return refusal, false
	}
	if r.byName == nil {
		r.byName = make(map[string]int, len(generatedRefusals))
	}
	r.byName[refusal.name]++
	return refusal, true
}

func (r *refusals) total() int {
	total := 0
	for _, count := range r.byName {
		total += count
	}
	return total
}

// report names what went uncompared. The registry is printed whether or
// not anything met it, so a passing campaign does not read as a clean bill
// of health for shapes it never compared. A zero beside an entry is not
// evidence against it: its witness is what still holds the entry.
func (r *refusals) report(t *testing.T) {
	t.Helper()
	lines := make([]string, 0, len(generatedRefusals))
	for _, refusal := range generatedRefusals {
		lines = append(lines, fmt.Sprintf("%d: %s: %s", r.byName[refusal.name], refusal.name, refusal.reason))
	}
	t.Logf("%d of %d generated programs were refused by this compiler and went uncompared; a zero below says this corpus did not draw the shape, not that the shape is gone, which the witnesses settle instead:\n\t%s",
		r.total(), r.attempted, strings.Join(lines, "\n\t"))
}

// requireCeiling fails once too much of what was generated went uncompared.
// It belongs to the wide compilation gate rather than the campaign, since
// the campaign stops early on a divergence and can attempt too few
// programs for a share to mean anything.
func (r *refusals) requireCeiling(t *testing.T) {
	t.Helper()
	if r.attempted == 0 {
		t.Errorf("nothing was built, so there is no share of it to hold to the one in %d a campaign may lose; a ceiling over an empty corpus passes whatever the generator produces",
			generatedRefusalCeiling)
		return
	}
	if r.total()*generatedRefusalCeiling > r.attempted {
		t.Errorf("%d of %d programs went uncompared, over the one in %d a campaign may lose; the generator is producing programs this compiler mostly cannot build",
			r.total(), r.attempted, generatedRefusalCeiling)
	}
}

// generatedCoverageCorpus is how many programs the construct tally is taken
// over. Nothing is compiled for it, so it is large enough that a shape drawn
// one time in fifty still appears.
const generatedCoverageCorpus = 400

// refusalWitnessDir holds one program per registry entry.
const refusalWitnessDir = "testdata/refusals"

// refusalWitnessSearch is how many generated programs the rederivation looks
// through for a program meeting each entry. The widest reach any entry the
// registry carries needs is under a third of it, so an entry not met here is one
// to read the report about rather than to widen the search for.
const refusalWitnessSearch = 2000

// refusalWitnessShrinkSteps bounds the reduction of one witness. It is far above
// what any of the kept witnesses took, so a reduction that stops short of a fixed
// point is a signal rather than the budget running out.
const refusalWitnessShrinkSteps = 20000

// refusalWitnessSegments is how far a witness is driven natively. It is short
// because what the run establishes is that the program is one clang builds and
// the sanitizers accept, not what it computes: nothing compares it against, since
// this compiler refuses it.
const refusalWitnessSegments = 4

// updateRefusalWitnesses rewrites the witness programs from a live search:
// the path back to green after a shape is fixed and its entry deleted, or
// after an optimizer rewrite change leaves a witness compiling. It writes
// files and does not touch the registry.
var updateRefusalWitnesses = flag.Bool("update-refusal-witnesses", false,
	"rewrite each registry entry's witness program from a search over generated programs")

// TestRefusalRegistryStillRefuses holds every registry entry to naming
// something this compiler still refuses. The tally a campaign prints
// cannot do this: an entry that stopped firing and one the corpus happened
// not to draw print the same zero.
func TestRefusalRegistryStillRefuses(t *testing.T) {
	if *updateRefusalWitnesses {
		rewriteRefusalWitnesses(t)
		return
	}
	requireWitnessesAreTheRegistry(t)
	for _, refusal := range generatedRefusals {
		t.Run(refusal.name, func(t *testing.T) {
			path := filepath.Join(refusalWitnessDir, refusal.witness)
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("%s carries a witness nothing can be asked of: %v", refusal.name, err)
			}
			_, err := buildGenerated(t, path)
			if err == nil || !strings.Contains(err.Error(), refusal.marker) {
				t.Errorf("%s is no longer refused: %s names a restriction this compiler does not impose any more, so a campaign counting it is counting nothing.\n\tIf the shape is gone, delete the entry and its witness. If it is still formed by some other program, rederive with %s.\n\tcompiling it now %s",
					refusal.witness, refusal.name, refusalWitnessTask, buildOutcome(err))
			}
			requireWitnessIsAProgramClangBuilds(t, refusal, path)
		})
	}
}

// TestRefusalMarkersTellTheEntriesApart holds the registry to the property
// [classifyRefusal] rests on: it answers with the first entry whose marker
// the diagnostic contains, so a marker containing another entry's would
// claim every refusal that one names and leave it unmet.
func TestRefusalMarkersTellTheEntriesApart(t *testing.T) {
	for _, refusal := range generatedRefusals {
		t.Run(refusal.name, func(t *testing.T) {
			if refusal.marker == "" {
				t.Fatalf("%s names no marker, so every diagnostic classifies as it", refusal.name)
			}
			for _, other := range generatedRefusals {
				if other.name == refusal.name {
					continue
				}
				if strings.Contains(refusal.marker, other.marker) {
					t.Errorf("the marker of %s contains the marker of %s, so a diagnostic meeting the first is classified as whichever the registry carries earlier",
						refusal.name, other.name)
				}
			}
		})
	}
}

// buildOutcome reads a witness compilation for a diagnostic, since the two ways
// an entry stops holding are not the same finding: the shape is gone, or the
// program now meets a different restriction and has stopped witnessing this one.
func buildOutcome(err error) string {
	if err == nil {
		return "succeeds"
	}
	return fmt.Sprintf("fails for something else: %v", err)
}

// requireWitnessesAreTheRegistry holds the directory and the registry to
// naming the same programs: a witness left behind by a deleted entry is a
// program nothing compiles, and an entry naming a gone file would
// otherwise fail further down as a compilation error, not a missing witness.
func requireWitnessesAreTheRegistry(t *testing.T) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(refusalWitnessDir, "*.c"))
	if err != nil {
		t.Fatalf("globbing %s: %v", refusalWitnessDir, err)
	}
	present := make(map[string]bool, len(paths))
	for _, path := range paths {
		present[filepath.Base(path)] = true
	}

	named := make(map[string]string, len(generatedRefusals))
	for _, refusal := range generatedRefusals {
		if owner, taken := named[refusal.witness]; taken {
			t.Errorf("%s and %s both carry %s; one program witnessing two restrictions makes a fix to either read as a fix to both",
				owner, refusal.name, refusal.witness)
		}
		named[refusal.witness] = refusal.name
		if !present[refusal.witness] {
			t.Errorf("%s carries %s, which %s does not hold; rederive with %s",
				refusal.name, refusal.witness, refusalWitnessDir, refusalWitnessTask)
		}
	}
	for name := range present {
		if _, carried := named[name]; !carried {
			t.Errorf("%s holds %s, which no registry entry carries, so it is a program nothing compiles and nothing checks; delete it with the entry it belonged to",
				refusalWitnessDir, name)
		}
	}
}

// requireWitnessIsAProgramClangBuilds holds the other half of what the
// registry states about its entries: each is a valid MicroC program clang
// builds, rather than one this compiler happens to refuse for being
// outside the language. The native build runs under the campaign's sanitizers.
func requireWitnessIsAProgramClangBuilds(t *testing.T, refusal generatedRefusal, path string) {
	t.Helper()
	if testing.Short() {
		t.Logf("not built natively under -short: %s is only held to still being refused", refusal.witness)
		return
	}
	ctx, harness := chiptest.Fixtures(t)
	native := devtrace.RunNative(ctx, t, harness, path, devtrace.RunOptions{
		World:    populatedWorld,
		Name:     "clang",
		Segments: refusalWitnessSegments,
		Stimulus: generatedStimulus,
	})
	t.Logf("%s: clang wrote %d times over %d segments, %s",
		refusal.witness, len(native.Events), native.Segments, native.Stop)
}

// rewriteRefusalWitnesses searches generated programs for one meeting each
// entry and writes the reduction as that entry's witness. A run that
// reported anything writes nothing: an unmet entry or one whose reduction
// stopped on budget is not one this run can stand behind.
func rewriteRefusalWitnesses(t *testing.T) {
	t.Helper()
	found := make(map[string]string, len(generatedRefusals))
	for seed := uint64(1); seed <= refusalWitnessSearch && len(found) < len(generatedRefusals); seed++ {
		program := generateProgram(seed)
		path := write(t, "witness.c", program.render())
		_, err := buildGenerated(t, path)
		if err == nil {
			continue
		}
		refusal, known := classifyRefusal(err)
		if !known || found[refusal.name] != "" {
			continue
		}
		found[refusal.name] = shrinkToWitness(t, program, refusal)
	}

	// The directory is rederived as a set, for the reason a document is: a
	// rewrite that lands where the search happened to succeed is one nobody can
	// read as a whole.
	for _, refusal := range generatedRefusals {
		if found[refusal.name] == "" {
			t.Errorf("no program in %d met %s, so there is nothing to witness it with; either the shape is gone and the entry is to be deleted, or the generator no longer reaches it",
				refusalWitnessSearch, refusal.name)
		}
	}
	// The whole run is the unit, so a reduction that stopped short of a fixed
	// point holds the rewrite back as surely as an entry nothing met does. Asking
	// the run rather than the search's own tally is what covers both.
	if t.Failed() {
		t.Errorf("no witness was written: %d of %d entries were met, and this run either missed one or could not stand behind the reduction of one; a rewrite from it would leave %s derived in part and stale in the rest",
			len(found), len(generatedRefusals), refusalWitnessDir)
		return
	}

	for _, refusal := range generatedRefusals {
		source := found[refusal.name]
		out := filepath.Join(refusalWitnessDir, refusal.witness)
		if err := os.WriteFile(out, []byte(source), 0o644); err != nil {
			t.Fatalf("writing %s: %v", out, err)
		}
		t.Logf("rewrote %s, %d lines", out, strings.Count(source, "\n"))
	}
}

// shrinkToWitness reduces a program while this compiler still refuses it for the
// same reason, which is the property the witness exists to carry.
func shrinkToWitness(t *testing.T, program generated, refusal generatedRefusal) string {
	t.Helper()
	steps := 0
	reduced := shrink(program, func(candidate generated) bool {
		if steps++; steps > refusalWitnessShrinkSteps {
			return false
		}
		return refusedWith(t, candidate.render(), refusal)
	})
	if steps > refusalWitnessShrinkSteps {
		t.Errorf("reducing %s ran out of candidates at %d, so what it wrote is wherever the search stopped rather than what it converged on",
			refusal.name, refusalWitnessShrinkSteps)
	}
	return reduced.render()
}

func refusedWith(t *testing.T, source string, refusal generatedRefusal) bool {
	t.Helper()
	_, err := buildGenerated(t, write(t, "candidate.c", source))
	return err != nil && strings.Contains(err.Error(), refusal.marker)
}

// TestGeneratedProgramsAgreeWithClang is the differential fuzzer: it
// generates MicroC programs and compares clang's native build against this
// compiler's, over the same world. A generated program is inside the
// comparable domain by construction, not filtering.
func TestGeneratedProgramsAgreeWithClang(t *testing.T) {
	seed, count := generatedConfig(t)
	t.Logf("%d generated programs from seed %d, %d segments each", count, seed, generatedSegments)
	var uncompared refusals
	reported := 0
	for i := range count {
		programSeed := seed + uint64(i)
		if !t.Run("seed "+strconv.FormatUint(programSeed, 10), func(t *testing.T) {
			compareGenerated(t, programSeed, &uncompared)
		}) {
			reported++
		}
		if reported >= generatedMaxReported {
			t.Logf("stopping after %d divergences out of %d programs; each reduction is a search over compilations and runs, so set %s to a failing seed and %s=1 to work one at a time",
				reported, i+1, envGeneratedSeed, envGeneratedPrograms)
			break
		}
	}
	uncompared.report(t)
}

// compareGenerated puts one generated program through both compilers,
// under every configuration in [generatedConfigurations]. The native run
// is taken once and compared against each, since a second clang build
// would just reproduce the same trace at the highest cost in the comparison.
func compareGenerated(t *testing.T, seed uint64, uncompared *refusals) {
	t.Helper()
	program := generateProgram(seed)
	source := program.render()
	path := write(t, "generated.c", source)

	ctx, harness := chiptest.Fixtures(t)
	native := devtrace.RunNative(ctx, t, harness, path, devtrace.RunOptions{
		World:    populatedWorld,
		Name:     "clang",
		Segments: generatedSegments,
		Stimulus: generatedStimulus,
	})
	t.Logf("seed %d: %d statements, clang wrote %d times over %d segments, %s",
		seed, countNodes(program.body), len(native.Events), native.Segments, native.Stop)
	requireEvidence(t, native, generatedWriteFloor)

	for _, cfg := range generatedConfigurations {
		output, err := compileDirect(t, path, cfg.opts)
		refusal, known := uncompared.record(err)
		if err != nil {
			if !known {
				// Reported rather than fatal, so that a program one rule refuses
				// is still compared under the other.
				t.Errorf("seed %d under the %s rule: the generator produced a program this compiler refuses for a reason nothing accounts for, which is a defect in the generator rather than a finding: %v\n%s",
					seed, cfg.name, err, source)
				continue
			}
			t.Logf("seed %d under the %s rule: not compared, %s: %v", seed, cfg.name, refusal.reason, err)
			continue
		}
		ctx, harness := chiptest.Fixtures(t)
		chip := devtrace.Run(ctx, t, harness, output.Text, devtrace.RunOptions{
			World:    populatedWorld,
			Name:     "ic11c",
			Segments: generatedSegments,
			Stimulus: generatedStimulus,
		})
		if difference := devtrace.Diff(native, chip); difference != nil {
			reportDivergence(t, program, cfg, difference)
		}
	}
}

// reportDivergence shrinks a failing program and fails naming the
// reduction. The difference reported is the reduced program's own,
// recomputed after the search: the whole program's difference names a
// write the reduction may no longer produce.
func reportDivergence(t *testing.T, program generated, cfg generatedConfiguration, difference error) {
	t.Helper()
	before := countNodes(program.body) + countNodes(program.funcs) + countNodes(program.globals)

	steps := 0
	want := differenceOnAWrite(difference)
	reduced := shrink(program, func(candidate generated) bool {
		if steps++; steps > generatedShrinkSteps {
			return false
		}
		native, chip, ok := runGenerated(t, candidate, cfg)
		if !ok {
			return false
		}
		reduced := devtrace.Diff(native, chip)
		return reduced != nil && differenceOnAWrite(reduced) == want
	})
	after := countNodes(reduced.body) + countNodes(reduced.funcs) + countNodes(reduced.globals)

	source := reduced.render()
	symptom := difference
	if native, chip, ok := runGenerated(t, reduced, cfg); ok {
		symptom = fmt.Errorf("%w\n    clang %s after %d segments and %d writes, ic11c %s after %d segments and %d writes",
			devtrace.Diff(native, chip), native.Stop, native.Segments, len(native.Events),
			chip.Stop, chip.Segments, len(chip.Events))
	}
	t.Errorf(`the emitted program does not do what clang reads the same source as doing
    seed:     %d
    inlining rule: %s
    whole program: %v
    reduced program: %v
    shrunk from %d statements to %d over %d candidates
    reproduce with: %s=%d %s=1 go test -tags llvm22 ./cmd/ic11c/ -run TestGeneratedProgramsAgreeWithClang
%s`,
		program.seed, cfg.name, difference, symptom, before, after, steps,
		envGeneratedSeed, program.seed, envGeneratedPrograms, source)
}

// differenceOnAWrite reads the classification [devtrace.Diff] reports, which is
// what a reduction has to keep hold of. See [devtrace.Difference].
func differenceOnAWrite(difference error) bool {
	var d *devtrace.Difference
	return errors.As(difference, &d) && d.OnAWrite
}

// runGenerated builds one candidate and runs both sides, reporting whether
// there was anything to compare. A candidate this compiler refuses is
// dropped; a candidate clang refuses is a generator defect and fails here.
func runGenerated(t *testing.T, candidate generated, cfg generatedConfiguration) (devtrace.Trace, devtrace.Trace, bool) {
	t.Helper()
	path := write(t, "shrink.c", candidate.render())
	output, err := compileDirect(t, path, cfg.opts)
	if err != nil {
		return devtrace.Trace{}, devtrace.Trace{}, false
	}
	ctx, harness := chiptest.Fixtures(t)
	native := devtrace.RunNative(ctx, t, harness, path, devtrace.RunOptions{
		World:    populatedWorld,
		Name:     "clang",
		Segments: generatedSegments,
		Stimulus: generatedStimulus,
	})
	chipCtx, chipHarness := chiptest.Fixtures(t)
	chip := devtrace.Run(chipCtx, t, chipHarness, output.Text, devtrace.RunOptions{
		World:    populatedWorld,
		Name:     "ic11c",
		Segments: generatedSegments,
		Stimulus: generatedStimulus,
	})
	return native, chip, true
}

// buildGenerated compiles one generated program, and returns assembly the
// chip's own rules accept. It goes through the pipeline rather than the
// command, since most generated programs are over the line limit and the
// command withholds such assembly; the interpreter imposes no line cap.
func buildGenerated(t *testing.T, path string) (string, error) {
	t.Helper()
	output, err := compileDirect(t, path, options{})
	if err != nil {
		return "", err
	}
	return output.Text, nil
}

// TestGeneratedProgramsCompileAlikeThroughBothPaths holds the two ways a
// generated program is compiled to the same result under the shipped rule,
// so a divergence the campaign reports belongs to the compiler the command
// runs, not a second pipeline this file assembled.
func TestGeneratedProgramsCompileAlikeThroughBothPaths(t *testing.T) {
	for seed := uint64(1); seed <= generatedCompileCorpus; seed++ {
		source := generateProgram(seed).render()
		path := write(t, fmt.Sprintf("bothpaths-%d.c", seed), source)

		stdout, stderr, cmdErr := run(t, path)
		got, err := compileDirect(t, path, options{})
		overLimit := err == nil && len(got.Report.Violations) > 0
		if (cmdErr == nil) != (err == nil && !overLimit) {
			t.Errorf("seed %d: the command line says %v and the option struct says %v over %d limit violations, so the two do not even agree about whether this program compiles into one the editor takes",
				seed, cmdErr, err, len(got.Report.Violations))
			continue
		}
		if err != nil {
			continue
		}
		if overLimit {
			if !strings.Contains(stderr, got.Report.String()) {
				t.Errorf("seed %d: the command line reports a different size from the option struct, so the two emitted different assembly:\n%s\n%s",
					seed, strings.TrimSpace(stderr), got.Report.String())
			}
			continue
		}
		if wanted := strings.TrimSuffix(stdout, "\n"); got.Text != wanted {
			t.Errorf("seed %d: the option struct emits different assembly from the command line under the shipped rule, so a counterfactual measured through it describes a pipeline of its own:\n%s\n%s",
				seed, wanted, got.Text)
		}
	}
}

func generatedConfig(t *testing.T) (seed uint64, count int) {
	t.Helper()
	seed, count = generatedSeed, generatedPrograms
	if testing.Short() {
		count = generatedShortPrograms
	}
	if raw := os.Getenv(envGeneratedSeed); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			t.Fatalf("%s=%q: %v", envGeneratedSeed, raw, err)
		}
		seed = parsed
	}
	if raw := os.Getenv(envGeneratedPrograms); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			t.Fatalf("%s=%q: want a positive count", envGeneratedPrograms, raw)
		}
		count = parsed
	}
	return seed, count
}

// generatedReading is what a device answers on one turn. The sweep reaches
// a NaN, both infinities, both zeroes, and values either side of the
// domains the machine's inverse trig and logarithm part company over,
// arriving through the world so no construct C leaves undefined produced them.
func generatedReading(n int) float64 {
	switch n % 29 {
	case 0:
		return math.NaN()
	case 1:
		return math.Inf(1)
	case 2:
		return math.Inf(-1)
	case 3:
		return 0
	case 4:
		return math.Copysign(0, -1)
	case 5:
		return 9007199254740993
	}
	return (float64((n*37)%1997) - 998) / 8
}

// generatedStimulus is the world a generated program is driven through.
// Every property it can name is seeded, since a generator draws them
// uniformly and one left at zero would leave a whole family of reads
// answering the same number every turn.
func generatedStimulus(t *testing.T, h *chip.FixtureHarness, segment int) {
	t.Helper()
	turn := segment * 101

	for _, pin := range genReadPins {
		seedDevice(t, pinOn(t, h, pin), func(s *seeding) {
			s.hashes(ic10.HashName(genMatchedPrefab), ic10.HashName(genMatchedName))
			for i, name := range genLogicTypes {
				s.logic(t, name, generatedReading(turn+i*7+pin))
			}
			for i, name := range genSlotTypes {
				for _, slot := range genSlots {
					s.slot(t, name, slot, generatedReading(turn+i*11+slot*3+pin))
				}
			}
			// TotalContents is not seeded: the chip sums the mixture Contents
			// seeded, so a value put there directly would be a total whose own
			// parts do not add up to it.
			for i, mode := range genReagentModes {
				if mode == "TotalContents" {
					continue
				}
				s.reagent(t, mode, genReagent, generatedReading(turn+i*13+pin))
			}
		})
	}
}

// TestGeneratedCorpusCoversItsConstructs holds the generator to the
// construct set it declares, in both directions: a generator that silently
// stops emitting a shape looks exactly like one that never could. Nothing
// is compiled here, letting the corpus be large.
func TestGeneratedCorpusCoversItsConstructs(t *testing.T) {
	reached := make(map[string]int, len(genConstructs))
	unknown := make(map[string]bool)
	for i := range generatedCoverageCorpus {
		g := newMicrocGen(uint64(i) + 1)
		program := g.build()
		for _, name := range program.constructs {
			reached[name]++
		}
		for name := range g.unknown {
			unknown[name] = true
		}
	}

	if len(unknown) > 0 {
		names := make([]string, 0, len(unknown))
		for name := range unknown {
			names = append(names, name)
		}
		t.Errorf("the generator reached %d constructs the declaration does not carry, so the coverage the set states is not the coverage a campaign has:\n\t%s",
			len(names), strings.Join(names, "\n\t"))
	}

	var missing []string
	for _, name := range genConstructs {
		if reached[name] == 0 {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d declared constructs no program in %d reached, each of which is a shape the campaign claims to cover and does not:\n\t%s",
			len(missing), generatedCoverageCorpus, strings.Join(missing, "\n\t"))
	}

	rare := make([]string, 0, len(genConstructs))
	for _, name := range genConstructs {
		if reached[name] < generatedCoverageCorpus/20 {
			rare = append(rare, fmt.Sprintf("%s: %d", name, reached[name]))
		}
	}
	t.Logf("%d constructs over %d programs; the least often reached are:\n\t%s",
		len(genConstructs), generatedCoverageCorpus, strings.Join(rare, "\n\t"))
}

// TestGeneratedProgramsAreReproducible holds the seed to the program, which is
// the whole of what makes a reported failure something anyone can look at.
func TestGeneratedProgramsAreReproducible(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		first := generateProgram(seed).render()
		second := generateProgram(seed).render()
		if first != second {
			t.Fatalf("seed %d produced two different programs, so a reported seed reproduces nothing", seed)
		}
		if strings.TrimSpace(first) == "" {
			t.Fatalf("seed %d produced an empty program", seed)
		}
	}
}

// TestGeneratedOperandNamesResolveAlike holds the pools a campaign draws
// operand names from to spellings both languages give one number. The
// pools are hand-written lists, so a misspelled or retired name would
// otherwise bury everything else behind one repeated difference.
func TestGeneratedOperandNamesResolveAlike(t *testing.T) {
	prelude := preludeEnumerators(t)
	for _, tt := range []struct {
		what   string
		prefix string
		names  []string
		value  func(string) (int, bool)
	}{
		{"logic type", ic10.LogicTypePrefix, genLogicTypes, func(name string) (int, bool) {
			info, ok := ic10.LookupLogicType(name)
			return int(info.Value), ok
		}},
		{"slot type", ic10.SlotTypePrefix, genSlotTypes, func(name string) (int, bool) {
			info, ok := ic10.LookupLogicSlotType(name)
			return int(info.Value), ok
		}},
		{"batch mode", ic10.BatchModePrefix, genBatchModes, func(name string) (int, bool) {
			info, ok := ic10.LookupBatchMode(name)
			return int(info.Value), ok
		}},
		{"reagent mode", ic10.ReagentModePrefix, genReagentModes, func(name string) (int, bool) {
			info, ok := ic10.LookupReagentMode(name)
			return int(info.Value), ok
		}},
	} {
		t.Run(tt.what, func(t *testing.T) {
			for _, name := range tt.names {
				ours, known := tt.value(strings.TrimPrefix(name, tt.prefix))
				if !known {
					t.Errorf("the generator draws %s %s, which the instruction tables do not name", tt.what, name)
					continue
				}
				theirs, declared := prelude[name]
				if !declared {
					t.Errorf("the generator draws %s %s, which %s does not declare, so a native build would not compile",
						tt.what, name, ic10.PreludeFileName)
					continue
				}
				if ours != theirs {
					t.Errorf("the generator draws %s %s, which is %d to this compiler and %d to C; a campaign using it would report that difference once per program",
						tt.what, name, ours, theirs)
				}
			}
		})
	}
}

// preludeEnumeratorPattern reads one enumerator. The deprecation attribute is
// skipped over rather than matched, since it carries a bracket of its own.
var preludeEnumeratorPattern = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:\[\[.*?\]\]\s*)?=\s*(-?\d+)\s*,?\s*$`)

// preludeEnumerators is every operand spelling the prelude declares and the
// number C gives it. It is one flat map because C has one enumerator
// namespace, and so does MicroC: the prelude gives each shared name to the
// first family carrying it and prefixes it in every later one.
func preludeEnumerators(t *testing.T) map[string]int {
	t.Helper()
	names := make(map[string]int)
	inside := false
	for line := range strings.Lines(ic10.Prelude) {
		switch {
		case strings.HasPrefix(line, "typedef enum ic10_"):
			inside = true
			continue
		case strings.HasPrefix(line, "}"):
			inside = false
			continue
		}
		if !inside {
			continue
		}
		match := preludeEnumeratorPattern.FindStringSubmatch(strings.TrimRight(line, "\n"))
		if match == nil {
			continue
		}
		value, err := strconv.Atoi(match[2])
		if err != nil {
			t.Fatalf("%s declares %s as %q: %v", ic10.PreludeFileName, match[1], match[2], err)
		}
		if _, taken := names[match[1]]; taken {
			t.Fatalf("%s declares %s twice, which C would refuse", ic10.PreludeFileName, match[1])
		}
		names[match[1]] = value
	}
	if len(names) == 0 {
		t.Fatalf("no enumerators were read out of %s, so the pattern that finds them is what is wrong", ic10.PreludeFileName)
	}
	return names
}

// TestShrinkReducesToTheStatementThatMatters measures the reduction against
// an oracle whose answer is known, since a real divergence is not
// reproducible on demand: the reduction has to converge on the one
// statement the oracle turns on, plus whatever declares the names it reads.
func TestShrinkReducesToTheStatementThatMatters(t *testing.T) {
	total, shrunk := 0, 0
	for seed := uint64(1); seed <= 8; seed++ {
		program := generateProgram(seed)
		marker := lastStatement(t, program.body)

		steps := 0
		reduced := shrink(program, func(candidate generated) bool {
			steps++
			source := candidate.render()
			requireDeclared(t, seed, source)
			return strings.Contains(source, marker)
		})

		source := reduced.render()
		if !strings.Contains(source, marker) {
			t.Fatalf("seed %d: the reduction lost the statement the oracle turns on", seed)
		}
		before, after := len(strings.Split(program.render(), "\n")), len(strings.Split(source, "\n"))
		if after >= before {
			t.Errorf("seed %d: shrinking reduced nothing, %d lines to %d", seed, before, after)
		}
		t.Logf("seed %d: %d lines to %d over %d candidates", seed, before, after, steps)
		total += before
		shrunk += after
	}
	t.Logf("shrinking reduced %d lines to %d across the corpus, %.0f%% of the source removed",
		total, shrunk, 100*float64(total-shrunk)/float64(total))
}

// lastStatement is the final publish of a generated program, which is the
// marker the shrink oracle turns on. The yield after it is required and never
// removed, so it would make a marker every candidate carries.
func lastStatement(t *testing.T, body []genNode) string {
	t.Helper()
	for i := len(body) - 1; i >= 0; i-- {
		if !body[i].required && len(body[i].text) == 1 {
			return strings.TrimSpace(body[i].text[0])
		}
	}
	t.Fatalf("a generated program ended with no statement a shrink may remove")
	return ""
}

// requireDeclared holds every candidate the shrinker offers to the
// property that makes shrinking sound: a name it uses is a name something
// still declares, since a candidate failing that would be a program the
// compiler refuses, which the oracle reads as "no longer diverges."
func requireDeclared(t *testing.T, seed uint64, source string) {
	t.Helper()
	declared := regexp.MustCompile(`\b(?:long long|double|bool|dev)\s+\*?([agikptvqf]\d+)`)
	used := regexp.MustCompile(`\b([agkptv]\d+)\b`)
	known := map[string]bool{}
	for _, match := range declared.FindAllStringSubmatch(source, -1) {
		known[match[1]] = true
	}
	for _, match := range used.FindAllStringSubmatch(source, -1) {
		if !known[match[1]] {
			t.Fatalf("seed %d: a shrink candidate names %s, which nothing declares:\n%s", seed, match[1], source)
		}
	}
}

// TestGeneratedProgramCompiles is the gate that builds without running:
// every generated program has to be one this compiler accepts, or one
// refused for a reason the registry names. Its corpus is wider than the
// campaign's, since nothing here is executed.
func TestGeneratedProgramCompiles(t *testing.T) {
	var uncompared refusals
	for seed := uint64(1); seed <= generatedCompileCorpus; seed++ {
		source := generateProgram(seed).render()
		path := write(t, fmt.Sprintf("compiles-%d.c", seed), source)
		_, err := buildGenerated(t, path)
		if _, known := uncompared.record(err); err != nil && !known {
			t.Errorf("seed %d: %v\n%s", seed, err, source)
		}
	}
	uncompared.report(t)
	uncompared.requireCeiling(t)
}

// generatedCompileCorpus is how many programs the compilation gate builds. It
// is wider than the differential campaign because nothing runs here, which is
// what makes it the measurement the refusal ceiling is taken over.
const generatedCompileCorpus = 40

// callShapes are the parts of the calling convention that show in the emitted
// text, each written with the trailing space that separates it from a mnemonic
// it is a prefix of.
var callShapes = []struct {
	// what names the shape in a report and in a failure.
	what string
	// mnemonic is what an emitted line that reaches it starts with.
	mnemonic string
}{
	{"a call", "jal "},
	{"a value saved across one", "push "},
	{"the restore after it", "pop "},
	{"the stack pointer the frames grow from", "move sp "},
}

// generatedCallCorpus is how many programs the calling-convention tally is taken
// over. Each is compiled once per configuration and none is run, so it is the
// width of the compilation gate for the same reason.
const generatedCallCorpus = generatedCompileCorpus

// generatedDataRegionHeadroom is how many times the memory array has to
// stand above the widest data region a generated program reaches. It
// gates a claim about what the campaign does not cover, not a budget a
// program has to fit.
const generatedDataRegionHeadroom = 4

// callTally is what one configuration did with the corpus.
type callTally struct {
	// compiled is how many programs this configuration built at all. A program
	// it refused emits nothing and reaches no shape, so it is the denominator
	// the shapes below are read against.
	compiled int
	// reached counts the programs reaching each of [callShapes], by index.
	reached []int
	// widestData is the largest data region any program came out with, which is
	// where the first call frame starts and so how close the corpus comes to the
	// collision the array does not trap.
	widestData int
	// overLines counts the programs past the editor's line limit, and mostLines
	// is the largest. Neither stops a comparison — the interpreter imposes no
	// line cap — and both are what says the corpus covers what the chip computes
	// rather than what it can hold.
	overLines int
	mostLines int
}

// TestGeneratedCampaignReachesTheCallingConvention holds the campaign to
// comparing programs that call at runtime: argument passing, the return
// register, and the saves around a call are reached by no program unless
// a configuration puts a real call in it.
func TestGeneratedCampaignReachesTheCallingConvention(t *testing.T) {
	var uncompared refusals
	tallies := make(map[string]*callTally, len(generatedConfigurations))
	for _, cfg := range generatedConfigurations {
		tallies[cfg.name] = &callTally{reached: make([]int, len(callShapes))}
	}

	for seed := uint64(1); seed <= generatedCallCorpus; seed++ {
		source := generateProgram(seed).render()
		path := write(t, fmt.Sprintf("calls-%d.c", seed), source)
		for _, cfg := range generatedConfigurations {
			output, err := compileDirect(t, path, cfg.opts)
			refusal, known := uncompared.record(err)
			if err != nil {
				if !known {
					t.Errorf("seed %d under the %s rule: %v\n%s", seed, cfg.name, err, source)
					continue
				}
				t.Logf("seed %d under the %s rule: not compiled, %s", seed, cfg.name, refusal.reason)
				continue
			}
			tally := tallies[cfg.name]
			tally.compiled++
			for i, shape := range callShapes {
				if strings.Contains(output.Text, shape.mnemonic) {
					tally.reached[i]++
				}
			}
			tally.widestData = max(tally.widestData, output.Report.Slots.Used())
			tally.mostLines = max(tally.mostLines, output.Report.Lines)
			if output.Report.Lines > emit.MaxLines {
				tally.overLines++
			}
		}
	}
	uncompared.report(t)

	outlining := 0
	for _, cfg := range generatedConfigurations {
		tally := tallies[cfg.name]
		counts := make([]string, len(callShapes))
		for i, shape := range callShapes {
			counts[i] = fmt.Sprintf("%s: %d", shape.what, tally.reached[i])
		}
		t.Logf("the %s rule built %d of %d generated programs, of which — %s",
			cfg.name, tally.compiled, generatedCallCorpus, strings.Join(counts, ", "))
		t.Logf("the %s rule reached %d of %d slots at its widest data region, and put %d of %d programs past the %d line limit, at most %d lines",
			cfg.name, tally.widestData, emit.MaxSlots, tally.overLines, tally.compiled, emit.MaxLines, tally.mostLines)

		if cfg.opts.outOfLineCallSites == 0 {
			continue
		}
		outlining++
		if tally.compiled == 0 {
			t.Errorf("the %s rule built none of %d generated programs, so what it covers is nothing at all",
				cfg.name, generatedCallCorpus)
			continue
		}
		for i, shape := range callShapes {
			if tally.reached[i] != tally.compiled {
				t.Errorf("the %s rule outlines every function a program calls, and %d of the %d programs it built reach %s; a program it built that calls nothing is a program the campaign compiles twice and covers once",
					cfg.name, tally.reached[i], tally.compiled, shape.what)
			}
		}
		// The frames are real and what they grow into is empty: a generated
		// program allocates a handful of slots, so its stack pointer never
		// reaches a global. The gate holds this claim, so a generator change
		// bringing the two within reach would be caught here.
		if tally.widestData*generatedDataRegionHeadroom > emit.MaxSlots {
			t.Errorf("the %s rule reached %d of the %d slots, inside the %d times the corpus is documented to stay clear of the array; a generated frame can now reach a global, which is coverage this campaign is written as not having",
				cfg.name, tally.widestData, emit.MaxSlots, generatedDataRegionHeadroom)
		}
	}
	if outlining == 0 {
		t.Errorf("no configuration the campaign compiles gives a called function a real definition, so every generated helper folds into main and nothing the campaign runs passes an argument, returns through a register, or moves the stack pointer")
	}
}

// reducedDivergences are the reproducers a campaign shrank a divergence
// down to, kept so the defect stays reported after the generator moves on
// and the seeds that once found it draw something else. Each fails on
// purpose, and each is to be deleted with the defect it names.
var reducedDivergences = []struct {
	name string
	why  string
	src  string
}{}

// TestReducedProgramsStillDiverge holds each kept reproducer to still
// diverging. It fails, and is meant to: a divergence quarantined behind a
// skip is the failure mode this whole path exists to prevent, so each
// fails until the case is deleted rather than inverted.
func TestReducedProgramsStillDiverge(t *testing.T) {
	for _, tt := range reducedDivergences {
		t.Run(tt.name, func(t *testing.T) {
			path := write(t, strings.ReplaceAll(tt.name, " ", "-")+".c", tt.src)
			ctx, harness := chiptest.Fixtures(t)
			native := devtrace.RunNative(ctx, t, harness, path, devtrace.RunOptions{
				World:    populatedWorld,
				Name:     "clang",
				Segments: generatedSegments,
				Stimulus: generatedStimulus,
			})
			assembly, err := buildGenerated(t, path)
			if err != nil {
				t.Fatalf("compiling the reproducer: %v", err)
			}
			chipCtx, chipHarness := chiptest.Fixtures(t)
			chip := devtrace.Run(chipCtx, t, chipHarness, assembly, devtrace.RunOptions{
				World:    populatedWorld,
				Name:     "ic11c",
				Segments: generatedSegments,
				Stimulus: generatedStimulus,
			})
			difference := devtrace.Diff(native, chip)
			if difference == nil {
				t.Errorf("%s no longer diverges; delete the case rather than inverting it", tt.name)
				return
			}
			t.Errorf("%s: %s\n    %v\n%s\n%s", tt.name, tt.why, difference, tt.src, assembly)
		})
	}
}
