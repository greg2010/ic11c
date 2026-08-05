package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/emit"
	"github.com/greg2010/ic11c/internal/llvmopt"
)

// measureCorpusReport asks a run to print what it measured, which is the
// whole of what `task corpus:measure` is. The measurement itself always
// runs, since the pipeline relations are argued from it; only the printing
// is optional, since a report on stdout in the middle of an ordinary run is noise.
var measureCorpusReport = flag.Bool("measure-corpus", false,
	"print what the corpus costs in every configuration this run compiles it in")

// The pipelines the shipped one is weighed against, in opt -passes syntax.
const (
	// rotatePipeline re-runs loop rotation over what the shipped pipeline
	// produced, with the header duplication Oz turns off left on. It is the
	// deviation the compiler does not take.
	rotatePipeline = llvmopt.DefaultPipeline + ",function(loop-mssa(loop-rotate))"
	// rotateFoldedPipeline adds the instcombine that folds a duplicated guard
	// into the preheader, which is what recovers part of rotation's cost.
	rotateFoldedPipeline = llvmopt.DefaultPipeline + ",function(loop-mssa(loop-rotate),instcombine)"
	// instcombinePipeline is that instcombine with no rotation in front of it.
	// It is what says the recovery belongs to the rotation rather than to the
	// pass following it, and the corpus has to come out unmoved.
	instcombinePipeline = llvmopt.DefaultPipeline + ",function(instcombine)"
	// latePipeline is default<Oz>, which runs the module optimization pipeline
	// the pre-link spelling stops before.
	latePipeline = "default<Oz>"
)

// fixtureBuild is one compilation of one fixture, as its own size report
// accounts for it. Every field is read off the report rather than
// recomputed, so what the measurement states is what the command states
// about the same program.
type fixtureBuild struct {
	// Refused reports a configuration that rejected the program. Only
	// --no-optimize does: a recursive function's local needs a data region slot
	// that only promotion removes.
	Refused bool
	Lines   int
	Bytes   int
	// LongestLine is the widest emitted line, which the line width is spent
	// against.
	LongestLine int
	// LineShare, ByteShare and WidthShare are what the build spends of each
	// limit, as a percentage, rounded the way the report rounds it.
	LineShare  int
	ByteShare  int
	WidthShare int
	// Binds names the budget the report ranks closest, which is the one that
	// decides how much program is left.
	Binds emit.ViolationKind
	// Over names each limit the build exceeds, empty for one that fits. It is
	// derived from what the build spends rather than from the report's
	// violation list, which carries one entry per offending line.
	Over string
	// Spill is the data region slots register allocation took, which is the
	// only place a program says whether it spilled at all.
	Spill int
	// Slots is the whole data region, spill included, out of [emit.MaxSlots].
	// It is the one budget the editor does not enforce and nothing traps: a
	// program past it assembles and pastes, and then a call frame overwrites
	// a global.
	Slots int
	// Frames says a real call was selected, which is the only thing that grows
	// into the slots above the data region. Without one the headroom is unspent
	// rather than merely unused, and the fixture says nothing about the budget.
	Frames bool
}

// measureBuild reads one emitted program's size report.
func measureBuild(report emit.Report) fixtureBuild {
	build := fixtureBuild{
		Lines:       report.Lines,
		Bytes:       report.Bytes,
		LongestLine: report.LongestLine,
		Binds:       report.Binding().Kind,
		Spill:       report.Slots.Spill,
		Slots:       report.Slots.Used(),
		Frames:      report.Slots.Frames,
	}
	var over []string
	for _, spend := range report.Spends() {
		switch spend.Kind {
		case emit.ViolationBytes:
			build.ByteShare = spend.Percent()
		case emit.ViolationLines:
			build.LineShare = spend.Percent()
		case emit.ViolationLineLength:
			build.WidthShare = spend.Percent()
		}
		if spend.Used > spend.Max {
			over = append(over, spend.Kind.String())
		}
	}
	build.Over = strings.Join(over, ", ")
	return build
}

// TestMeasureBuildReadsWhatAProgramSpends covers what the report's own
// arithmetic decides and the measurement only reads: the share of each limit,
// which of the two budgets binds, and which limits a program is over.
func TestMeasureBuildReadsWhatAProgramSpends(t *testing.T) {
	for _, tt := range []struct {
		name   string
		report emit.Report
		want   fixtureBuild
	}{
		{
			name:   "spending the same share of both budgets, which ties to bytes",
			report: emit.Report{Bytes: emit.MaxBytes / 2, Lines: emit.MaxLines / 2, LongestLine: 45},
			want: fixtureBuild{
				Lines: 64, Bytes: 2048, LongestLine: 45,
				LineShare: 50, ByteShare: 50, WidthShare: 50,
				Binds: emit.ViolationBytes,
			},
		},
		{
			name:   "narrow lines, so lines bind",
			report: emit.Report{Bytes: 1024, Lines: 100, LongestLine: 20},
			want: fixtureBuild{
				Lines: 100, Bytes: 1024, LongestLine: 20,
				LineShare: 78, ByteShare: 25, WidthShare: 22,
				Binds: emit.ViolationLines,
			},
		},
		{
			name:   "wide lines, so bytes bind",
			report: emit.Report{Bytes: 3865, Lines: 116, LongestLine: 88},
			want: fixtureBuild{
				Lines: 116, Bytes: 3865, LongestLine: 88,
				LineShare: 90, ByteShare: 94, WidthShare: 97,
				Binds: emit.ViolationBytes,
			},
		},
		{
			name:   "over the line budget alone",
			report: emit.Report{Bytes: 2048, Lines: 153, LongestLine: 45},
			want: fixtureBuild{
				Lines: 153, Bytes: 2048, LongestLine: 45,
				LineShare: 120, ByteShare: 50, WidthShare: 50,
				Binds: emit.ViolationLines, Over: "lines",
			},
		},
		{
			name:   "over every limit, in the order the target documents them",
			report: emit.Report{Bytes: 8192, Lines: 256, LongestLine: 180},
			want: fixtureBuild{
				Lines: 256, Bytes: 8192, LongestLine: 180,
				LineShare: 200, ByteShare: 200, WidthShare: 200,
				Binds: emit.ViolationBytes, Over: "bytes, lines, line length",
			},
		},
		{
			name:   "over the line width alone, which is never the binding budget",
			report: emit.Report{Bytes: 512, Lines: 4, LongestLine: 120},
			want: fixtureBuild{
				Lines: 4, Bytes: 512, LongestLine: 120,
				LineShare: 3, ByteShare: 12, WidthShare: 134,
				Binds: emit.ViolationBytes, Over: "line length",
			},
		},
		{
			name:   "the slots allocation spilled into",
			report: emit.Report{Bytes: 512, Lines: 32, LongestLine: 20, Slots: emit.SlotReport{Data: 4, Spill: 3}},
			want: fixtureBuild{
				Lines: 32, Bytes: 512, LongestLine: 20,
				LineShare: 25, ByteShare: 12, WidthShare: 22,
				Binds: emit.ViolationLines, Spill: 3, Slots: 7,
			},
		},
		{
			// A data region with frames above it, which is the budget nothing
			// enforces: the two share one array and a frame reaching a global
			// overwrites it with nothing trapping.
			name:   "a data region a call frame grows above",
			report: emit.Report{Bytes: 512, Lines: 32, LongestLine: 20, Slots: emit.SlotReport{Data: 64, Spill: 2, Frames: true}},
			want: fixtureBuild{
				Lines: 32, Bytes: 512, LongestLine: 20,
				LineShare: 25, ByteShare: 12, WidthShare: 22,
				Binds: emit.ViolationLines, Spill: 2, Slots: 66, Frames: true,
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := measureBuild(tt.report); got != tt.want {
				t.Errorf("measureBuild() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// configuration is the whole corpus compiled one way.
type configuration struct {
	// name is what the report calls it, and is short enough to head a column.
	name string
	// builds is what each fixture came out at.
	builds map[string]fixtureBuild
	// lines and bytes total every fixture the configuration compiled, so a
	// total covers fewer programs than the corpus wherever refused names any.
	lines int
	bytes int
	// refused names every fixture the configuration rejected, in name order.
	refused []string
}

// buildCorpus compiles every fixture one way and totals what came out.
func buildCorpus(t *testing.T, names []string, name string, opts options) configuration {
	t.Helper()
	c := configuration{name: name, builds: make(map[string]fixtureBuild, len(names))}
	for _, fixture := range names {
		build := buildFixture(t, fixture, opts)
		c.builds[fixture] = build
		if build.Refused {
			c.refused = append(c.refused, fixture)
			continue
		}
		c.lines += build.Lines
		c.bytes += build.Bytes
	}
	return c
}

// buildFixture compiles one fixture through the pipeline the command runs,
// with opts selecting the configuration. A program over a limit is not a
// failure here, any more than on the command line: it is emitted and
// reported. A program the configuration rejects is reported as refused.
func buildFixture(t *testing.T, name string, opts options) fixtureBuild {
	t.Helper()
	path := filepath.Join(fixtures, name)
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	output, diags, err := compile(t.Context(), path, string(src), opts)
	if err != nil {
		t.Fatalf("compiling %s with %+v: %v", name, opts, err)
	}
	if diags.HasErrors() {
		return fixtureBuild{Refused: true}
	}
	return measureBuild(output.Report)
}

// corpusMeasurement is what the compiler makes of the corpus, in every
// configuration a reader weighs the shipped one against. Nothing here is
// recorded: every figure is recompiled from the fixtures at the time the
// test runs, so what the report prints is what the compiler does now.
type corpusMeasurement struct {
	// names is every fixture, in the order a reader looks one up in.
	names   []string
	shipped configuration
	// numeric and readable are the two emission modes the command exposes, and
	// unoptimized the pipeline it skips.
	numeric     configuration
	readable    configuration
	unoptimized configuration
	// unrolled, late, rotated and rotatedFolded are the pass configurations the
	// optimizer's deviations are weighed against; twoSites and threeSites are
	// the inlining thresholds. None of the six is reachable from the command
	// line: each is compiled here with the one option under study set.
	unrolled      configuration
	late          configuration
	rotated       configuration
	rotatedFolded configuration
	twoSites      configuration
	threeSites    configuration
}

// weighed is every configuration the report puts beside the shipped one, in the
// order it prints them.
func (m corpusMeasurement) weighed() []configuration {
	return []configuration{m.shipped, m.unrolled, m.late, m.rotated, m.rotatedFolded, m.twoSites, m.threeSites}
}

// measureCorpus compiles the corpus in every mode and every configuration.
func measureCorpus(t *testing.T) corpusMeasurement {
	t.Helper()
	names := corpusFixtures(t)
	return corpusMeasurement{
		names:         names,
		shipped:       buildCorpus(t, names, "shipped", options{}),
		numeric:       buildCorpus(t, names, "--numeric", options{numeric: true}),
		readable:      buildCorpus(t, names, "--readable", options{readable: true}),
		unoptimized:   buildCorpus(t, names, "--no-optimize", options{skipOptimizer: true}),
		unrolled:      buildCorpus(t, names, "unrolling on", options{loopUnrolling: true}),
		late:          buildCorpus(t, names, "late pipeline", options{pipeline: latePipeline}),
		rotated:       buildCorpus(t, names, "rotated", options{pipeline: rotatePipeline}),
		rotatedFolded: buildCorpus(t, names, "rotated, folded", options{pipeline: rotateFoldedPipeline}),
		twoSites:      buildCorpus(t, names, "two call sites", options{outOfLineCallSites: 2}),
		threeSites:    buildCorpus(t, names, "three call sites", options{outOfLineCallSites: 3}),
	}
}

// TestCorpusMeasurements measures what the compiler makes of the corpus and
// holds the pipeline to the relations the compiler document argues from,
// which are properties rather than figures: a figure moves with every
// fixture and pass, unlike a relation.
func TestCorpusMeasurements(t *testing.T) {
	m := measureCorpus(t)
	checkCommandLineAgrees(t, m)
	checkPipelineRelations(t, m)
	if !*measureCorpusReport {
		return
	}
	if _, err := io.WriteString(os.Stdout, corpusReport(m)); err != nil {
		t.Fatalf("writing the report: %v", err)
	}
}

// checkCommandLineAgrees holds every mode measured through the option
// struct to the same mode reached through the command line: agreement is
// what says the struct describes the real pipeline rather than one this
// test assembled.
func checkCommandLineAgrees(t *testing.T, m corpusMeasurement) {
	t.Helper()
	for _, c := range []struct {
		configuration configuration
		args          []string
	}{
		{m.shipped, nil},
		{m.numeric, []string{"--numeric"}},
		{m.readable, []string{"--readable"}},
		{m.unoptimized, []string{"--no-optimize"}},
	} {
		for _, name := range m.names {
			want := c.configuration.builds[name]
			if want.Refused {
				continue
			}
			// Every limit the report states, not the two totals: the readable
			// form's whole difference from the shipped one is width it spends on
			// annotations, and a comparison that left width out would agree
			// about the one mode it says the most about.
			got := commandLineSize(t, name, c.args...)
			if got.Bytes != want.Bytes || got.Lines != want.Lines || got.LongestLine != want.LongestLine {
				t.Errorf("%s comes out at %d bytes over %d lines, widest %d, compiled through the option struct and %d over %d, widest %d, through %v; every configuration measured here is measured the first way and describes the second only while the two agree",
					name, want.Bytes, want.Lines, want.LongestLine,
					got.Bytes, got.Lines, got.LongestLine, append([]string{"ic11c"}, c.args...))
			}
		}
	}
}

// commandLineSize compiles one fixture through the command and reads the
// size report it printed. A non-zero status is not a failure here: a
// program over a limit is reported and then fails with its assembly withheld.
func commandLineSize(t *testing.T, name string, args ...string) fixtureBuild {
	t.Helper()
	_, stderr, err := run(t, append([]string{filepath.Join(fixtures, name)}, args...)...)
	if err != nil {
		t.Logf("%s %v exited non-zero, which the size report below is still read from: %v", name, args, err)
	}
	return sizeReport(t, stderr)
}

// checkPipelineRelations holds each configuration to the relation the compiler
// document argues from, none of which any figure carries.
func checkPipelineRelations(t *testing.T, m corpusMeasurement) {
	t.Helper()
	for _, c := range append(m.weighed(), m.numeric, m.readable) {
		if len(c.refused) > 0 {
			t.Fatalf("the %s configuration refuses %s, so its totals cover fewer programs than the shipped ones and nothing between the two compares",
				c.name, strings.Join(c.refused, ", "))
		}
	}

	// The pass that recovers part of rotation's cost has to be recovering the
	// rotation's cost rather than doing work of its own.
	folded := buildCorpus(t, m.names, "instcombine alone", options{pipeline: instcombinePipeline})
	if folded.lines != m.shipped.lines || folded.bytes != m.shipped.bytes {
		t.Errorf("a trailing instcombine with no rotation in front of it puts the corpus at %d lines over %d bytes where the shipped pipeline puts it at %d over %d; it has to change nothing at all",
			folded.lines, folded.bytes, m.shipped.lines, m.shipped.bytes)
	}

	// The margin is a fraction of a percent and rests on a couple of fixtures,
	// so it is logged whether or not it holds: a reader who sees how thin it is
	// knows what a failure here is likely to be about before reading the message.
	t.Logf("the late pipeline is %d lines and %d bytes under the shipped one over the %d fixtures in %s",
		m.shipped.lines-m.late.lines, m.shipped.bytes-m.late.bytes, len(m.names), fixtures)
	if m.late.lines >= m.shipped.lines || m.late.bytes >= m.shipped.bytes {
		t.Errorf("the late pipeline puts the corpus at %d lines over %d bytes against the shipped %d over %d, and it has to be the smaller of the two: the module optimization the pre-link spelling stops before is what costs that margin. The margin is a fraction of a percent over the %d fixtures in %s, so read what moved there first — a fixture added, removed or edited reaches this before any pass does",
			m.late.lines, m.late.bytes, m.shipped.lines, m.shipped.bytes, len(m.names), fixtures)
	}
	if m.rotatedFolded.lines <= m.shipped.lines || m.rotatedFolded.lines >= m.rotated.lines ||
		m.rotatedFolded.bytes <= m.shipped.bytes || m.rotatedFolded.bytes >= m.rotated.bytes {
		t.Errorf("folding rotation's duplicated guard puts the corpus at %d lines over %d bytes, against %d over %d rotated and %d over %d shipped; it recovers part of rotation's cost, which puts it strictly between the two",
			m.rotatedFolded.lines, m.rotatedFolded.bytes,
			m.rotated.lines, m.rotated.bytes, m.shipped.lines, m.shipped.bytes)
	}

	// A dev argument is substituted at the call site, so no call-site count may
	// give the function taking one a shared body.
	const devParam = "solar_tracker.c"
	if !slices.Contains(m.names, devParam) {
		t.Fatalf("%s is gone from the corpus, and it is the fixture a call-site threshold is held to being unable to reach", devParam)
	}
	for _, c := range []configuration{m.twoSites, m.threeSites} {
		if got, want := c.builds[devParam], m.shipped.builds[devParam]; got != want {
			t.Errorf("%s comes out at %+v under the %s configuration and %+v under the shipped rule; a threshold cannot reach its dev-parameter function",
				devParam, got, c.name, want)
		}
	}

	for _, name := range m.names {
		shipped, numeric, readable := m.shipped.builds[name], m.numeric.builds[name], m.readable.builds[name]
		if numeric.Lines != shipped.Lines {
			t.Errorf("%s emits %d lines with machine names and %d with their integers; a name and its integer occupy the same one operand, so the difference is bytes alone",
				name, shipped.Lines, numeric.Lines)
		}
		if numeric.Bytes > shipped.Bytes {
			t.Errorf("%s emits %d bytes with machine names and %d with their integers; the integer is the shorter spelling of the two",
				name, shipped.Bytes, numeric.Bytes)
		}
		if readable.Lines != shipped.Lines {
			t.Errorf("%s emits %d lines and %d under --readable; the annotation is a comment after the instruction and the chip cuts a line at its first '#', so the readable form is the shipped program on the same lines",
				name, shipped.Lines, readable.Lines)
		}
	}
}

// corpusReport renders what a measurement found, for reading and for
// diffing against another run: every list is in fixture order, no duration
// or path is printed, and every figure is measured rather than recorded, so
// a diff of two reports is what moved between them and nothing else.
func corpusReport(m corpusMeasurement) string {
	var b strings.Builder
	writeCorpusSummary(&b, m)
	writeShippedFixtures(&b, m)
	writeModeCosts(&b, m)
	writeUnoptimizedFixtures(&b, m)
	writeConfigurationTotals(&b, m)
	writeMovedFixtures(&b, m)
	return b.String()
}

// writeCorpusSummary states what the corpus costs as a whole, and which
// fixtures each of the properties a per-fixture column carries is true of.
func writeCorpusSummary(b *strings.Builder, m corpusMeasurement) {
	var over, spilling, byteBound, calling []string
	widestData := 0
	for _, name := range m.names {
		build := m.shipped.builds[name]
		if build.Over != "" {
			over = append(over, name)
		}
		if build.Spill > 0 {
			spilling = append(spilling, name)
		}
		if build.Binds == emit.ViolationBytes {
			byteBound = append(byteBound, name)
		}
		if build.Frames {
			calling = append(calling, name)
		}
		widestData = max(widestData, build.Slots)
	}
	low, high := byteBand(m.shipped, m.names)

	b.WriteString("the corpus as the compiler ships it\n")
	writeTable(b, nil, [][]string{
		{"fixtures", strconv.Itoa(len(m.names))},
		{"lines", strconv.Itoa(m.shipped.lines)},
		{"bytes", strconv.Itoa(m.shipped.bytes)},
		{"bytes an emitted line", fmt.Sprintf("%.1f to %.1f", low, high)},
		{"over an editor limit", namesOrNone(over)},
		{"spilling", namesOrNone(spilling)},
		{"bytes the closer limit", namesOrNone(byteBound)},
		{"largest data region", fmt.Sprintf("%d of %d slots", widestData, emit.MaxSlots)},
		{"growing frames into the rest", namesOrNone(calling)},
	})
}

// byteBand is the least and greatest bytes an emitted line costs across the
// fixtures. Each end is a fixture's own rate rather than a rounded-out
// number, so both ends name a program that exists — a rounded band would
// read as a measurement and not be one.
func byteBand(c configuration, names []string) (low, high float64) {
	measured := false
	for _, name := range names {
		build := c.builds[name]
		if build.Refused || build.Lines == 0 {
			continue
		}
		rate := float64(build.Bytes) / float64(build.Lines)
		if !measured || rate < low {
			low, measured = rate, true
		}
		high = max(high, rate)
	}
	return low, high
}

// writeShippedFixtures states what each fixture spends of each limit.
func writeShippedFixtures(b *strings.Builder, m corpusMeasurement) {
	rows := make([][]string, 0, len(m.names))
	for _, name := range m.names {
		build := m.shipped.builds[name]
		rows = append(rows, []string{
			name,
			strconv.Itoa(build.Lines), percent(build.LineShare),
			strconv.Itoa(build.Bytes), percent(build.ByteShare),
			strconv.Itoa(build.LongestLine), percent(build.WidthShare),
			build.Binds.String(), strconv.Itoa(build.Spill),
			strconv.Itoa(build.Slots), strconv.Itoa(emit.MaxSlots - build.Slots), framesOrNone(build.Frames),
		})
	}
	b.WriteString("\neach fixture as the compiler ships it\n")
	writeTable(b, []string{
		"fixture",
		"lines", "of " + strconv.Itoa(emit.MaxLines),
		"bytes", "of " + strconv.Itoa(emit.MaxBytes),
		"widest line", "of " + strconv.Itoa(emit.MaxLineLength),
		"binds", "spill slots",
		"slots", "left of " + strconv.Itoa(emit.MaxSlots), "grows into them",
	}, rows)
}

// framesOrNone says whether anything grows into the slots a fixture leaves.
// Without a call nothing takes a slot above the data region, so what is
// left is unspent rather than at risk — a distinction the headroom number
// alone would not make.
func framesOrNone(frames bool) string {
	if frames {
		return "call frames"
	}
	return "nothing"
}

// writeModeCosts states what the two emission modes cost against the
// shipped build. Neither moves a line, which is why the corpus totals state
// lines at all: it is the answer, not an omission. The readable width is
// given both absolute and as a delta, since both are read.
func writeModeCosts(b *strings.Builder, m corpusMeasurement) {
	b.WriteString("\nwhat --readable and --numeric cost, against the shipped build\n")
	writeTable(b, []string{"", "--readable", "--numeric"}, [][]string{
		{"lines", signed(m.readable.lines - m.shipped.lines), signed(m.numeric.lines - m.shipped.lines)},
		{"bytes", signed(m.readable.bytes - m.shipped.bytes), signed(m.numeric.bytes - m.shipped.bytes)},
	})

	rows := make([][]string, 0, len(m.names))
	for _, name := range m.names {
		shipped, numeric, readable := m.shipped.builds[name], m.numeric.builds[name], m.readable.builds[name]
		rows = append(rows, []string{
			name,
			signed(readable.Bytes - shipped.Bytes),
			strconv.Itoa(readable.LongestLine), percent(readable.WidthShare),
			signed(readable.LongestLine - shipped.LongestLine),
			signed(numeric.Bytes - shipped.Bytes),
		})
	}
	b.WriteString("\n")
	writeTable(b, []string{
		"fixture",
		"--readable bytes", "--readable widest line", "of " + strconv.Itoa(emit.MaxLineLength), "wider than shipped",
		"--numeric bytes",
	}, rows)
}

// writeUnoptimizedFixtures states what each fixture comes out at with the
// optimizer skipped, which is the only mode any fixture is over a limit in.
func writeUnoptimizedFixtures(b *strings.Builder, m corpusMeasurement) {
	rows := make([][]string, 0, len(m.names))
	for _, name := range m.names {
		build := m.unoptimized.builds[name]
		if build.Refused {
			rows = append(rows, []string{name, "refused"})
			continue
		}
		rows = append(rows, []string{
			name,
			strconv.Itoa(build.Lines), percent(build.LineShare),
			strconv.Itoa(build.Bytes), percent(build.ByteShare),
			strconv.Itoa(build.LongestLine), percent(build.WidthShare),
			orNone(build.Over),
		})
	}
	b.WriteString("\neach fixture with the optimizer skipped\n")
	writeTable(b, []string{
		"fixture",
		"lines", "of " + strconv.Itoa(emit.MaxLines),
		"bytes", "of " + strconv.Itoa(emit.MaxBytes),
		"widest line", "of " + strconv.Itoa(emit.MaxLineLength),
		"over",
	}, rows)
}

// writeConfigurationTotals states what the corpus comes out at under each
// configuration the shipped one is weighed against.
func writeConfigurationTotals(b *strings.Builder, m corpusMeasurement) {
	weighed := m.weighed()
	rows := make([][]string, 0, len(weighed))
	for _, c := range weighed {
		rows = append(rows, []string{c.name, strconv.Itoa(c.lines), strconv.Itoa(c.bytes)})
	}
	b.WriteString("\nthe corpus under each configuration\n")
	writeTable(b, []string{"configuration", "lines", "bytes"}, rows)
}

// writeMovedFixtures states where the movement between configurations sits,
// which is what says whether a total moved on one fixture or on all of them.
func writeMovedFixtures(b *strings.Builder, m corpusMeasurement) {
	weighed := m.weighed()
	header := make([]string, 0, len(weighed)+1)
	header = append(header, "fixture")
	for _, c := range weighed {
		header = append(header, c.name)
	}

	var rows [][]string
	for _, name := range m.names {
		shipped := m.shipped.builds[name].Lines
		row := []string{name}
		moved := false
		for _, c := range weighed {
			lines := c.builds[name].Lines
			moved = moved || lines != shipped
			row = append(row, strconv.Itoa(lines))
		}
		if moved {
			rows = append(rows, row)
		}
	}
	b.WriteString("\neach fixture some configuration moves, in lines\n")
	if len(rows) == 0 {
		b.WriteString("none\n")
		return
	}
	writeTable(b, header, rows)
}

// writeTable renders rows under header, the first column left-aligned and the
// rest right-aligned under theirs, so that one figure sits in one place across
// two runs. A nil header renders the rows alone, and a row shorter than the
// header ends where its cells do.
func writeTable(b *strings.Builder, header []string, rows [][]string) {
	all := rows
	if header != nil {
		all = append([][]string{header}, rows...)
	}
	var widths []int
	for _, row := range all {
		for i, cell := range row {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], len(cell))
		}
	}
	for _, row := range all {
		var line strings.Builder
		for i, cell := range row {
			if i == 0 {
				fmt.Fprintf(&line, "%-*s", widths[i], cell)
				continue
			}
			fmt.Fprintf(&line, "  %*s", widths[i], cell)
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteString("\n")
	}
}

// percent renders a share of a limit.
func percent(share int) string { return strconv.Itoa(share) + "%" }

// signed renders a movement against the shipped build, where the direction is
// what a reader is after and zero is a direction of its own.
func signed(delta int) string {
	if delta <= 0 {
		return strconv.Itoa(delta)
	}
	return "+" + strconv.Itoa(delta)
}

// namesOrNone renders a set of fixtures, saying so where it is empty rather
// than leaving a blank a reader has to interpret.
func namesOrNone(names []string) string { return orNone(strings.Join(names, ", ")) }

func orNone(text string) string {
	if text == "" {
		return "none"
	}
	return text
}
