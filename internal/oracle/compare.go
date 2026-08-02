package oracle

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
)

// maxDetail caps how many differing registers or slots one mismatch spells out.
const maxDetail = 4

// Mismatch is one field on which two results disagree.
type Mismatch struct {
	Field  Field
	Detail string
}

func (m Mismatch) String() string { return string(m.Field) + ": " + m.Detail }

// Excusal pairs a mismatch with the registry entries that account for it.
type Excusal struct {
	Mismatch Mismatch
	By       []Divergence
}

// Report is the verdict of one differential comparison.
type Report struct {
	Harness Harness
	// Unexplained are mismatches no registry entry covers. Any of these is a real finding.
	Unexplained []Mismatch
	// Excused are mismatches the registry accounts for.
	Excused []Excusal
	// Advisories are entries the program triggers that do not excuse anything but bear on how a
	// failure should be read.
	Advisories []Divergence
}

// OK reports whether every mismatch, if any, was covered by the registry.
func (r Report) OK() bool { return len(r.Unexplained) == 0 }

func (r Report) String() string {
	var b strings.Builder
	if r.OK() {
		fmt.Fprintf(&b, "%s: agrees", r.Harness)
	} else {
		fmt.Fprintf(&b, "%s: %d unexplained mismatch(es)", r.Harness, len(r.Unexplained))
		for _, m := range r.Unexplained {
			fmt.Fprintf(&b, "\n  unexplained %s", m)
		}
	}
	for _, e := range r.Excused {
		ids := make([]string, len(e.By))
		for i, d := range e.By {
			ids[i] = d.ID
		}
		fmt.Fprintf(&b, "\n  excused by %s: %s", strings.Join(ids, ", "), e.Mismatch)
	}
	for _, d := range r.Advisories {
		fmt.Fprintf(&b, "\n  advisory %s: %s", d.ID, d.Summary)
	}
	return b.String()
}

// Compare checks our implementation's result for source against a harness's result for the same
// program, and classifies every disagreement against the divergence registry.
//
// ours is whatever produced the reference result — the ic11c interpreter, a second harness, a
// hand-written expectation. Compare knows nothing about it beyond the Result shape, so a
// comparison layer over the ic11c VM only has to translate its final state into a Result.
//
// CompileErrors are not compared: no two implementations here share a diagnostic vocabulary.
func Compare(h Harness, source string, ours, theirs Result) Report {
	report := Report{Harness: h}
	reachable := Reachable(h, source)

	for _, d := range reachable {
		if d.Advisory {
			report.Advisories = append(report.Advisories, d)
		}
	}

	for _, m := range mismatches(ours, theirs) {
		var by []Divergence
		for _, d := range reachable {
			if !d.Advisory && slices.Contains(d.Fields, m.Field) {
				by = append(by, d)
			}
		}
		if len(by) == 0 {
			report.Unexplained = append(report.Unexplained, m)
			continue
		}
		report.Excused = append(report.Excused, Excusal{Mismatch: m, By: by})
	}
	return report
}

// Check fails the test when Compare finds a mismatch the registry does not explain, and logs the
// ones it does so an excused run is still visible in verbose output.
func Check(tb testing.TB, h Harness, source string, ours, theirs Result) Report {
	tb.Helper()
	report := Compare(h, source, ours, theirs)
	if !report.OK() {
		tb.Errorf("%s\nprogram:\n%s", report, indent(source))
		return report
	}
	if len(report.Excused) > 0 || len(report.Advisories) > 0 {
		tb.Logf("%s", report)
	}
	return report
}

// RequireComparable fails the test when the registry would excuse a mismatch anywhere in source,
// which would leave a comparison of it asserting nothing.
//
// The registry's value-level entries cover every field, since a wrong constant moves control flow
// and can therefore move any part of the result. A program that triggers one is excluded by name
// wherever the corpus is built, and holding every other program to triggering nothing is what
// turns a generator or a lowering that starts emitting such an instruction into a failure rather
// than a silent pass. Triggers match any bare token, operands included, so an entry added for a
// common one would otherwise quietly hollow out a whole corpus.
//
// Advisory entries are left alone: they explain nothing and excuse nothing, so a program
// triggering one still has to agree.
func RequireComparable(tb testing.TB, h Harness, source string) {
	tb.Helper()
	for _, d := range Reachable(h, source) {
		if d.Advisory {
			continue
		}
		tb.Fatalf("the program triggers %s (%s), which excuses %d field(s), so nothing here is "+
			"compared; change the program or exclude it by name:\n%s",
			d.ID, d.Summary, len(d.Fields), indent(source))
	}
}

func mismatches(ours, theirs Result) []Mismatch {
	var out []Mismatch

	if detail := diffDoubles("register", ours.Final.Registers[:], theirs.Final.Registers[:], RegisterName); detail != "" {
		out = append(out, Mismatch{FieldRegisters, detail})
	}
	if detail := diffDoubles("slot", ours.Final.Stack[:], theirs.Final.Stack[:], nil); detail != "" {
		out = append(out, Mismatch{FieldStack, detail})
	}
	if ours.InstructionPointer != theirs.InstructionPointer {
		out = append(out, Mismatch{FieldInstructionPointer,
			fmt.Sprintf("ours %d, harness %d", ours.InstructionPointer, theirs.InstructionPointer)})
	}
	if ours.Status != theirs.Status {
		out = append(out, Mismatch{FieldStatus, fmt.Sprintf("ours %q, harness %q", ours.Status, theirs.Status)})
	}
	if ours.ErrorType != theirs.ErrorType {
		out = append(out, Mismatch{FieldErrorType, fmt.Sprintf("ours %q, harness %q", ours.ErrorType, theirs.ErrorType)})
	}
	if (ours.ErrorType != "" || theirs.ErrorType != "") && ours.ErrorLine != theirs.ErrorLine {
		out = append(out, Mismatch{FieldErrorLine, fmt.Sprintf("ours %d, harness %d", ours.ErrorLine, theirs.ErrorLine)})
	}
	if ours.Instructions != theirs.Instructions {
		out = append(out, Mismatch{FieldInstructions, fmt.Sprintf("ours %d, harness %d", ours.Instructions, theirs.Instructions)})
	}
	if ours.Ticks != theirs.Ticks {
		out = append(out, Mismatch{FieldTicks, fmt.Sprintf("ours %d, harness %d", ours.Ticks, theirs.Ticks)})
	}
	return out
}

// diffDoubles compares bit patterns, so NaN equals NaN with the same payload and -0 differs
// from +0. name renders an index; a nil name numbers them plainly.
func diffDoubles(kind string, ours, theirs []float64, name func(int) string) string {
	var parts []string
	extra := 0
	for i := range ours {
		o, t := math.Float64bits(ours[i]), math.Float64bits(theirs[i])
		if o == t {
			continue
		}
		if len(parts) == maxDetail {
			extra++
			continue
		}
		label := fmt.Sprintf("%d", i)
		if name != nil {
			label = name(i)
		}
		parts = append(parts, fmt.Sprintf("%s %s: ours %v (0x%016x), harness %v (0x%016x)",
			kind, label, ours[i], o, theirs[i], t))
	}
	if len(parts) == 0 {
		return ""
	}
	if extra > 0 {
		parts = append(parts, fmt.Sprintf("and %d more", extra))
	}
	return strings.Join(parts, "; ")
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = "    " + line
	}
	return strings.Join(lines, "\n")
}
