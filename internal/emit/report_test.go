package emit

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
)

// editorSeparator is what UpdateFileSize charges between two lines, spelled out
// rather than read from separatorBytes: every count below is an oracle for that
// constant, and reading it would leave them agreeing with whatever it holds.
const editorSeparator = 2

// TestByteAccounting checks the report against a count done by hand.
func TestByteAccounting(t *testing.T) {
	main := mir.NewFunc("main", position(1))
	main.NewBlock("main.entry", position(1)).Append(
		instr(t, isa.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1}),
		instr(t, isa.OpJ, mir.Label{Name: "helper.entry"}),
	)
	helper := mir.NewFunc("helper", position(2))
	helper.NewBlock("helper.entry", position(2)).Append(instr(t, isa.OpYield))

	out := emit(t, &mir.Program{Funcs: []*mir.Func{main, helper}}, Options{})

	// "move r0 1" is 9, "j 2" is 3, "yield" is 5. The first two are followed by
	// text and pay a separator; the last is the end of the program and does not.
	const (
		wantMainBytes   = (9 + 2) + (3 + 2)
		wantHelperBytes = 5
		wantBytes       = wantMainBytes + wantHelperBytes
	)
	if out.Text != "move r0 1\nj 2\nyield" {
		t.Fatalf("Text = %q", out.Text)
	}
	if out.Report.Bytes != wantBytes {
		t.Errorf("Bytes = %d, want %d", out.Report.Bytes, wantBytes)
	}
	// Two joins, each one byte in Text and two in the count.
	if got := len(out.Text); got != wantBytes-2 {
		t.Errorf("len(Text) = %d, want %d", got, wantBytes-2)
	}
	if out.Report.Lines != 3 {
		t.Errorf("Lines = %d, want 3", out.Report.Lines)
	}
	if out.Report.Bytes > MaxBytes {
		t.Errorf("Bytes = %d, over the %d byte budget for a %d byte program", out.Report.Bytes, MaxBytes, wantBytes)
	}
	if len(out.Report.Violations) != 0 {
		t.Errorf("Violations = %v, want none", out.Report.Violations)
	}

	want := []FuncReport{
		{Name: "main", Pos: position(1), Bytes: wantMainBytes, Lines: 2, FirstLine: 0},
		{Name: "helper", Pos: position(2), Bytes: wantHelperBytes, Lines: 1, FirstLine: 2},
	}
	if len(out.Report.Functions) != len(want) {
		t.Fatalf("Functions = %v, want %v", out.Report.Functions, want)
	}
	for i, got := range out.Report.Functions {
		if got != want[i] {
			t.Errorf("Functions[%d] = %+v, want %+v", i, got, want[i])
		}
	}
}

// synthetic builds lines from text alone, for accounting cases no emitted
// program reaches.
func synthetic(t *testing.T, texts ...string) []line {
	t.Helper()
	lines := make([]line, len(texts))
	for i, text := range texts {
		lines[i] = line{text: text, pos: position(i + 1), fn: "main"}
	}
	return lines
}

// TestChargesMatchTheEditor pins the byte count against
// InputSourceCode.UpdateFileSize. Each case is counted by hand off that method:
// it scans backward for the last slot whose text is non-empty, sums every slot's
// text length, and charges two bytes after each slot below that index.
func TestChargesMatchTheEditor(t *testing.T) {
	tests := []struct {
		name  string
		texts []string
		want  int
	}{
		{
			name: "no lines at all",
			want: 0,
		},
		{
			name:  "one line pays no separator",
			texts: []string{"yield"},
			want:  5,
		},
		{
			name:  "two lines pay one separator",
			texts: []string{"move r0 1", "yield"},
			want:  9 + 2 + 5,
		},
		{
			name:  "three lines pay two separators",
			texts: []string{"move r0 1", "j 2", "yield"},
			want:  9 + 2 + 3 + 2 + 5,
		},
		{
			name:  "a trailing blank line is free",
			texts: []string{"yield", ""},
			want:  5,
		},
		{
			name:  "every trailing blank line is free",
			texts: []string{"yield", "", "", ""},
			want:  5,
		},
		{
			name:  "a blank line between two lines still pays its separator",
			texts: []string{"yield", "", "yield"},
			want:  5 + 2 + 0 + 2 + 5,
		},
		{
			// The scan asks whether a slot holds text, not whether it holds
			// enough of it to be an instruction.
			name:  "a one character last line still ends the program",
			texts: []string{"yield", "x"},
			want:  5 + 2 + 1,
		},
		{
			name:  "a program of nothing but blanks costs nothing",
			texts: []string{"", "", ""},
			want:  0,
		},
		{
			// Counted off the game's 90 rather than off the package's copy of it,
			// which a fixture built from that copy would agree with whatever it
			// happened to hold.
			name:  "a line over the width is charged what paste stored",
			texts: []string{strings.Repeat("x", 110), "yield"},
			want:  90 + 2 + 5,
		},
		{
			// Paste fills 128 slots and drops the rest, so the editor never
			// receives the last two. Costing only the slots that arrived would
			// tell the author to cut bytes out of a program it never got. 129
			// lines pay a separator and the last pays none.
			name:  "a program past the grid is charged every line it emits",
			texts: slices.Repeat([]string{"yield"}, 130),
			want:  129*(5+2) + 5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := synthetic(t, tt.texts...)
			if got := sum(charges(lines)); got != tt.want {
				t.Errorf("charges sum to %d, want %d", got, tt.want)
			}
		})
	}
}

// TestLastText reads the index the separator count stops before rather than
// inferring it from a total. Every grid below charges the same bytes whichever
// answer the scan gives for one holding no text at all, so nothing else in the
// package can tell the two apart.
func TestLastText(t *testing.T) {
	tests := []struct {
		name  string
		texts []string
		want  int
	}{
		{name: "no lines at all", want: 0},
		{name: "a single line", texts: []string{"yield"}, want: 0},
		{name: "the last line of three", texts: []string{"move r0 1", "j 2", "yield"}, want: 2},
		{name: "the scan runs back past trailing blanks", texts: []string{"yield", "", ""}, want: 0},
		{name: "a blank between two lines does not stop the scan", texts: []string{"yield", "", "yield"}, want: 2},
		{name: "a grid holding no text at all", texts: []string{"", "", ""}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lastText(synthetic(t, tt.texts...)); got != tt.want {
				t.Errorf("lastText = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestCodeWidth covers the width the editor's cut has to be measured against.
// Several rows are unreachable through Emit — every rendered name is printable
// ASCII holding no '#' — and are here because the function models that cut and
// not the lines Emit happens to produce.
func TestCodeWidth(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{name: "a line carrying no comment", text: "move r0 1", want: len("move r0 1")},
		{name: "an empty line", text: "", want: 0},
		{name: "the space the annotation is separated by goes with the comment", text: "yield # main_entry:", want: len("yield")},
		{name: "a comment with no space before it", text: "yield# main_entry:", want: len("yield")},
		{name: "a line that is nothing but a comment", text: "# main_entry:", want: 0},
		{name: "every space before the comment goes with it", text: "yield   # main_entry:", want: len("yield")},
		{name: "a second '#' is comment text", text: "yield # main_entry: # j 4", want: len("yield")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codeWidth(tt.text); got != tt.want {
				t.Errorf("codeWidth(%q) = %d, want %d", tt.text, got, tt.want)
			}
		})
	}
}

// budgetLines builds a 128 slot program whose first count lines carry all
// textBytes of its text and whose remaining slots are blank, so a case can land
// the total on either side of the budget without depending on what any
// instruction happens to render to.
func budgetLines(t *testing.T, count, textBytes int) []line {
	t.Helper()
	if count < 1 || count > MaxLines {
		t.Fatalf("count = %d, want 1 to %d", count, MaxLines)
	}
	texts := make([]string, MaxLines)
	for i := range count {
		width := textBytes / count
		if i < textBytes%count {
			width++
		}
		if width > MaxLineLength {
			t.Fatalf("line %d is %d characters, over the %d character limit", i, width, MaxLineLength)
		}
		texts[i] = strings.Repeat("x", width)
	}
	return synthetic(t, texts...)
}

// TestByteBudgetBoundary pins the accounting against the decision it feeds: a
// program landing on exactly 4096 is one the game accepts. Every figure is
// counted by hand rather than derived from the package's constants — a text size
// computed from the separator charge lands on 4096 whatever the charge is.
func TestByteBudgetBoundary(t *testing.T) {
	tests := []struct {
		name          string
		count         int
		textBytes     int
		wantBytes     int
		wantViolation bool
		// wantMsg is the whole violation message, checked for the rows that
		// raise one. A message stating the overage backwards reads as a program
		// under budget by as much as it is over.
		wantMsg string
	}{
		{
			// 128 lines of text charge 127 separators at two bytes each, so
			// 4096 - 254 characters of text land on exactly the budget.
			name:      "exactly the budget fits",
			count:     128,
			textBytes: 3842,
			wantBytes: 4096,
		},
		{
			// Charging one byte a separator would count this at 3843 + 127 =
			// 3970 and call it a fit, which is the disagreement with the game
			// this accounting exists to close.
			name:          "one byte over the budget is a violation",
			count:         128,
			textBytes:     3843,
			wantBytes:     4097,
			wantViolation: true,
			wantMsg:       "program is 4097 bytes, 1 over the 4096 byte budget",
		},
		{
			// Half the slots hold text, so the separator count comes from the
			// last line of text rather than a full grid's fixed 127: 63 at two
			// bytes each, leaving 4096 - 126. A case filling the grid cannot
			// tell the two apart.
			name:      "a shorter program reaches the budget on fewer separators",
			count:     64,
			textBytes: 3970,
			wantBytes: 4096,
		},
		{
			name:          "one byte over on fewer separators is still a violation",
			count:         64,
			textBytes:     3971,
			wantBytes:     4097,
			wantViolation: true,
			wantMsg:       "program is 4097 bytes, 1 over the 4096 byte budget",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := buildReport(budgetLines(t, tt.count, tt.textBytes), nil, SlotReport{})
			if report.Bytes != tt.wantBytes {
				t.Errorf("Bytes = %d, want %d", report.Bytes, tt.wantBytes)
			}
			var over []Violation
			for _, v := range report.Violations {
				if v.Kind == ViolationBytes {
					over = append(over, v)
				}
			}
			if raised := len(over) != 0; raised != tt.wantViolation {
				t.Fatalf("byte violation = %v, want %v; violations were %v", raised, tt.wantViolation, report.Violations)
			}
			if tt.wantViolation && over[0].Msg != tt.wantMsg {
				t.Errorf("Msg = %q, want %q", over[0].Msg, tt.wantMsg)
			}
		})
	}
}

// TestFunctionBytesSumToProgramBytes is what makes attribution usable: the
// parts have to add up to the whole or the report cannot say where the size
// went.
func TestFunctionBytesSumToProgramBytes(t *testing.T) {
	programs := []struct {
		name  string
		build func(t *testing.T) *mir.Program
	}{
		{name: "sample", build: buildSampleProgram},
		{name: "a function that emits nothing", build: buildEmptyFunctionProgram},
	}
	for _, prog := range programs {
		for _, readable := range []bool{false, true} {
			t.Run(prog.name+"/readable="+strconv.FormatBool(readable), func(t *testing.T) {
				out := emit(t, prog.build(t), Options{Readable: readable})
				bytes, lines := 0, 0
				for _, fn := range out.Report.Functions {
					bytes += fn.Bytes
					lines += fn.Lines
				}
				if bytes != out.Report.Bytes {
					t.Errorf("function bytes sum to %d, program is %d", bytes, out.Report.Bytes)
				}
				if lines != out.Report.Lines {
					t.Errorf("function lines sum to %d, program is %d", lines, out.Report.Lines)
				}
			})
		}
	}
}

// TestFunctionThatEmitsNothingIsStillReported covers what the report promises
// about such a function: it keeps its row, spends nothing, and carries the line
// it would have started on rather than a line another function owns.
func TestFunctionThatEmitsNothingIsStillReported(t *testing.T) {
	out := emit(t, buildEmptyFunctionProgram(t), Options{})
	if out.Text != "move r0 1\nyield" {
		t.Fatalf("Text = %q", out.Text)
	}
	want := []FuncReport{
		{Name: "main", Pos: position(1), Bytes: len("move r0 1") + editorSeparator, Lines: 1, FirstLine: 0},
		{Name: "empty", Pos: position(2), Bytes: 0, Lines: 0, FirstLine: 1},
		{Name: "tail", Pos: position(3), Bytes: len("yield"), Lines: 1, FirstLine: 1},
	}
	if len(out.Report.Functions) != len(want) {
		t.Fatalf("Functions = %+v, want %+v", out.Report.Functions, want)
	}
	for i, got := range out.Report.Functions {
		if got != want[i] {
			t.Errorf("Functions[%d] = %+v, want %+v", i, got, want[i])
		}
	}
}

// TestOverBudgetStillEmits covers the deliberate choice not to refuse: knowing
// which function accounts for the size is more useful than a rejection.
func TestOverBudgetStillEmits(t *testing.T) {
	const lines = 100
	fn := mir.NewFunc("fat", position(1))
	block := fn.NewBlock("fat.entry", position(1))
	for range lines {
		block.Append(instr(t, isa.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1e60}))
	}

	out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{})
	if out.Report.Bytes <= MaxBytes {
		t.Fatalf("Bytes = %d, want it over the %d byte budget", out.Report.Bytes, MaxBytes)
	}
	if out.Text == "" {
		t.Error("Emit returned no text for an over budget program")
	}
	if got := strings.Count(out.Text, "\n") + 1; got != lines {
		t.Errorf("emitted %d lines, want %d", got, lines)
	}
	if len(out.Report.Violations) != 1 {
		t.Fatalf("Violations = %v, want exactly the byte budget", out.Report.Violations)
	}
	violation := out.Report.Violations[0]
	if violation.Kind != ViolationBytes {
		t.Errorf("Kind = %v, want %v", violation.Kind, ViolationBytes)
	}
	if violation.Line != -1 {
		t.Errorf("Line = %d, want -1 for a whole program violation", violation.Line)
	}
	wantMsg := fmt.Sprintf("program is %d bytes, %d over the %d byte budget", out.Report.Bytes, out.Report.Bytes-MaxBytes, MaxBytes)
	if violation.Msg != wantMsg {
		t.Errorf("Msg = %q, want %q", violation.Msg, wantMsg)
	}
	if fn := out.Report.Functions; len(fn) != 1 || fn[0].Bytes != out.Report.Bytes {
		t.Errorf("Functions = %v, want all %d bytes attributed to fat", fn, out.Report.Bytes)
	}
}

// TestLineCountBoundary pins the line limit against the grid the editor holds,
// which is exactly MaxLines slots wide. A program filling it is one Paste
// receives whole; one line more is a line Paste drops with nothing said, so the
// limit has to read as exceeded at MaxLines+1 and not at MaxLines.
func TestLineCountBoundary(t *testing.T) {
	tests := []struct {
		name  string
		lines int
		// wantMsg is empty for a program inside the limit, and counted by hand
		// rather than formatted from the limit.
		wantMsg string
	}{
		{name: "one line short of the limit", lines: MaxLines - 1},
		{name: "exactly the limit fits", lines: MaxLines},
		{name: "one line over the limit", lines: MaxLines + 1, wantMsg: "program is 129 lines, 1 over the 128 line limit"},
		{name: "well over the limit", lines: MaxLines + 40, wantMsg: "program is 168 lines, 40 over the 128 line limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := mir.NewFunc("long", position(1))
			block := fn.NewBlock("long.entry", position(1))
			for range tt.lines {
				block.Append(instr(t, isa.OpYield))
			}

			out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{})
			if out.Report.Lines != tt.lines {
				t.Fatalf("Lines = %d, want %d", out.Report.Lines, tt.lines)
			}
			// A yield is short enough that no case here spends the byte budget,
			// so the line count is the only limit the row can be reporting.
			if out.Report.Bytes > MaxBytes {
				t.Fatalf("Bytes = %d, want it under the %d byte budget", out.Report.Bytes, MaxBytes)
			}
			if tt.wantMsg == "" {
				if len(out.Report.Violations) != 0 {
					t.Errorf("Violations = %v, want none", out.Report.Violations)
				}
				return
			}
			over := slices.IndexFunc(out.Report.Violations, func(v Violation) bool {
				return v.Kind == ViolationLines
			})
			if over < 0 {
				t.Fatalf("Violations = %v, want the line count among them", out.Report.Violations)
			}
			if got := out.Report.Violations[over].Msg; got != tt.wantMsg {
				t.Errorf("Msg = %q, want %q", got, tt.wantMsg)
			}
		})
	}
}

// widths builds synthetic lines of the given character counts, for violation
// combinations a real program would need thousands of instructions to reach.
func widths(t *testing.T, counts ...int) []line {
	t.Helper()
	texts := make([]string, len(counts))
	for i, count := range counts {
		texts[i] = strings.Repeat("x", count)
	}
	return synthetic(t, texts...)
}

// overWideAmong returns the widths of a count line program whose lines at the
// given indices exceed the line limit and whose others do not.
func overWideAmong(count int, at ...int) []int {
	counts := slices.Repeat([]int{MaxLineLength - 10}, count)
	for i, index := range at {
		counts[index] = MaxLineLength + 1 + i
	}
	return counts
}

// TestViolationOrder covers the order Report.Violations documents: the
// whole-program violations first, then the per-line ones ascending. Every other
// violation case exceeds one limit alone, and one limit alone cannot tell an
// order apart from whichever check happens to run first.
func TestViolationOrder(t *testing.T) {
	type reported struct {
		Kind ViolationKind
		Line int
	}
	tests := []struct {
		name   string
		widths []int
		want   []reported
	}{
		{
			name:   "a program inside every limit",
			widths: []int{5, 5, 5},
		},
		{
			name:   "the byte budget before the line count",
			widths: slices.Repeat([]int{MaxLineLength - 10}, MaxLines+1),
			want: []reported{
				{Kind: ViolationBytes, Line: -1},
				{Kind: ViolationLines, Line: -1},
			},
		},
		{
			// Under the line count, so the line width has to sort after a
			// whole-program violation rather than merely after the other line.
			name:   "a wide line after a whole-program violation",
			widths: overWideAmong(60, 59),
			want: []reported{
				{Kind: ViolationBytes, Line: -1},
				{Kind: ViolationLineLength, Line: 59},
			},
		},
		{
			name:   "two wide lines ascending",
			widths: []int{5, MaxLineLength + 1, 5, MaxLineLength + 5, 5},
			want: []reported{
				{Kind: ViolationLineLength, Line: 1},
				{Kind: ViolationLineLength, Line: 3},
			},
		},
		{
			name:   "all three limits at once",
			widths: overWideAmong(MaxLines+1, 0, MaxLines),
			want: []reported{
				{Kind: ViolationBytes, Line: -1},
				{Kind: ViolationLines, Line: -1},
				{Kind: ViolationLineLength, Line: 0},
				{Kind: ViolationLineLength, Line: MaxLines},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := buildReport(widths(t, tt.widths...), nil, SlotReport{})
			got := make([]reported, len(report.Violations))
			for i, violation := range report.Violations {
				got[i] = reported{Kind: violation.Kind, Line: violation.Line}
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Violations = %+v, want %+v; messages were %v", got, tt.want, report.Violations)
			}
		})
	}
}

func TestLineLengthViolation(t *testing.T) {
	fn := mir.NewFunc("wide", position(1))
	block := fn.NewBlock("wide.entry", position(1))
	block.Append(at(t, 4, isa.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1e-100}))
	block.Append(at(t, 5, isa.OpYield))

	out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{})
	if len(out.Report.Violations) != 1 {
		t.Fatalf("Violations = %v, want exactly the line length", out.Report.Violations)
	}
	violation := out.Report.Violations[0]
	if violation.Kind != ViolationLineLength {
		t.Errorf("Kind = %v, want %v", violation.Kind, ViolationLineLength)
	}
	if violation.Line != 0 {
		t.Errorf("Line = %d, want 0", violation.Line)
	}
	if violation.Pos != position(4) {
		t.Errorf("Pos = %v, want %v", violation.Pos, position(4))
	}
	if !strings.Contains(violation.Msg, strconv.Itoa(MaxLineLength)) {
		t.Errorf("Msg = %q, want it to name the %d character limit", violation.Msg, MaxLineLength)
	}
}

// TestAMagnitudeNoLineHoldsIsRefusedRatherThanRounded settles what happens at the
// ends of the double range. Nothing rounds, so a wide magnitude costs a character
// per power of ten, and a subnormal is 308 zeros before its first digit: no line
// holds one but the smallest, which the target's constant table names.
func TestAMagnitudeNoLineHoldsIsRefusedRatherThanRounded(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		wantWide bool
	}{
		{name: "the smallest subnormal is in the constant table", value: math.SmallestNonzeroFloat64},
		{name: "the next subnormal up has no name", value: math.Float64frombits(2), wantWide: true},
		{name: "the smallest subnormal negated has no name either", value: -math.SmallestNonzeroFloat64, wantWide: true},
		{name: "the largest subnormal", value: math.Float64frombits(0x000fffffffffffff), wantWide: true},
		{name: "the smallest normal", value: math.Float64frombits(0x0010000000000000), wantWide: true},
		{name: "the largest double", value: math.MaxFloat64, wantWide: true},
		{name: "the most negative double", value: -math.MaxFloat64, wantWide: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := mir.NewFunc("wide", position(1))
			block := fn.NewBlock("wide.entry", position(1))
			block.Append(at(t, 4, isa.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: tt.value}))
			block.Append(at(t, 5, isa.OpYield))

			out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{})
			wide := slices.ContainsFunc(out.Report.Violations, func(v Violation) bool {
				return v.Kind == ViolationLineLength && v.Line == 0
			})
			if wide != tt.wantWide {
				t.Fatalf("line 0 of %q reported over the width = %v, want %v; violations were %v",
					out.Text, wide, tt.wantWide, out.Report.Violations)
			}
			first, _, _ := strings.Cut(out.Text, "\n")
			if wide == (len(first) <= MaxLineLength) {
				t.Errorf("line 0 is %d characters and the report says it is over the width = %v", len(first), wide)
			}
		})
	}
}

// TestLineAtExactlyTheLimitIsNotAViolation covers the boundary from both sides of
// the report. TruncatedComments is asserted too: a line at the limit treated as
// over it would find no '#', measure the whole line as instruction, and report a
// comment cut on paste for a line carrying no comment at all.
func TestLineAtExactlyTheLimitIsNotAViolation(t *testing.T) {
	padding := strings.Repeat("0", MaxLineLength-len("move r0 1"))
	value, err := strconv.ParseFloat("1"+padding, 64)
	if err != nil {
		t.Fatalf("ParseFloat: %v", err)
	}
	fn := mir.NewFunc("edge", position(1))
	fn.NewBlock("edge.entry", position(1)).Append(instr(t, isa.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: value}))

	out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{})
	if got := len(out.Text); got != MaxLineLength {
		t.Fatalf("line is %d characters, want exactly %d", got, MaxLineLength)
	}
	if strings.Contains(out.Text, "#") {
		t.Fatalf("line carries a comment, which is not what this boundary is about: %q", out.Text)
	}
	if len(out.Report.Violations) != 0 {
		t.Errorf("Violations = %v, want none at exactly the limit", out.Report.Violations)
	}
	if out.Report.TruncatedComments != 0 {
		t.Errorf("TruncatedComments = %d, want none: the line is inside the limit and holds no comment", out.Report.TruncatedComments)
	}
	if out.Report.LongestLine != MaxLineLength {
		t.Errorf("LongestLine = %d, want %d", out.Report.LongestLine, MaxLineLength)
	}
	if strings.Contains(out.Report.String(), "cut on paste") {
		t.Errorf("report says the paste cut something:\n%s", out.Report.String())
	}
}

// wideValue is the immediate whose decimal expansion makes "move r0 <value>"
// exactly width characters.
func wideValue(t *testing.T, width int) float64 {
	t.Helper()
	digits := width - len("move r0 ")
	if digits < 1 {
		t.Fatalf("width = %d, too narrow to hold an immediate", width)
	}
	value, err := strconv.ParseFloat("1"+strings.Repeat("0", digits-1), 64)
	if err != nil {
		t.Fatalf("ParseFloat: %v", err)
	}
	return value
}

// TestReadableSpendsWidthRatherThanLines covers the one limit annotating a line
// can overrun. Both cuts that reach a wide line take the end of it, so one
// landing inside the annotation takes comment text alone. The annotation is not
// dropped to fit: emitting a line with no name would say the block has none.
func TestReadableSpendsWidthRatherThanLines(t *testing.T) {
	tests := []struct {
		name string
		// instruction is the width of the emitted instruction, before any
		// annotation.
		instruction int
		// wantViolation is whether the width the chip reads is over the limit,
		// which is the only cut that changes the program.
		wantViolation bool
	}{
		{name: "an instruction at the limit whose annotation runs past it", instruction: MaxLineLength},
		{name: "an instruction one short of the limit", instruction: MaxLineLength - 1},
		{name: "an instruction over the limit is still fatal", instruction: MaxLineLength + 1, wantViolation: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := wideValue(t, tt.instruction)
			build := func(t *testing.T) *mir.Program {
				t.Helper()
				fn := mir.NewFunc("main", position(1))
				fn.NewBlock("main.entry", position(1)).Append(
					at(t, 4, isa.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: value}),
				)
				return &mir.Program{Funcs: []*mir.Func{fn}}
			}

			plain := emit(t, build(t), Options{})
			if plain.Report.LongestLine != tt.instruction {
				t.Fatalf("default LongestLine = %d, want exactly %d", plain.Report.LongestLine, tt.instruction)
			}

			out := emit(t, build(t), Options{Readable: true})
			if got := instructionsOf(t, out.Text); got != plain.Text {
				t.Errorf("readable instructions = %q, want the default form %q", got, plain.Text)
			}
			if out.Report.Lines != plain.Report.Lines {
				t.Errorf("readable Lines = %d, default is %d", out.Report.Lines, plain.Report.Lines)
			}
			if want := tt.instruction + len(" # main_entry:"); out.Report.LongestLine != want {
				t.Errorf("LongestLine = %d, want %d, the annotation charged to the width", out.Report.LongestLine, want)
			}
			if out.Report.LongestLine <= MaxLineLength {
				t.Fatalf("LongestLine = %d, want a line the editor's paste cuts", out.Report.LongestLine)
			}

			if !tt.wantViolation {
				if len(out.Report.Violations) != 0 {
					t.Fatalf("Violations = %v, want none: the cut takes comment text alone", out.Report.Violations)
				}
				if out.Report.TruncatedComments != 1 {
					t.Errorf("TruncatedComments = %d, want the one line the paste cuts", out.Report.TruncatedComments)
				}
				if text := out.Report.String(); !strings.Contains(text, "cut on paste") {
					t.Errorf("the report does not say the line is cut in its comment:\n%s", text)
				}
				return
			}
			// The instruction is what has to be cut, so the line is a violation
			// and not a comment the paste trims.
			if out.Report.TruncatedComments != 0 {
				t.Errorf("TruncatedComments = %d, want none: the cut takes instruction text", out.Report.TruncatedComments)
			}
			if len(out.Report.Violations) != 1 {
				t.Fatalf("Violations = %v, want exactly the line width", out.Report.Violations)
			}
			violation := out.Report.Violations[0]
			if violation.Kind != ViolationLineLength || violation.Line != 0 {
				t.Errorf("Violations[0] = %+v, want the line width on line 0", violation)
			}
			if violation.Pos != position(4) {
				t.Errorf("Pos = %v, want %v", violation.Pos, position(4))
			}
			// The count the message states is the instruction's, not the
			// annotated line's: the annotation is not what has to be cut.
			if !strings.Contains(violation.Msg, strconv.Itoa(tt.instruction)) {
				t.Errorf("Msg = %q, want it to name the %d characters of instruction", violation.Msg, tt.instruction)
			}
		})
	}
}

// inlined copies instr with a call chain attached, innermost first, which is
// the order instruction selection reads it off a debug location.
func inlined(in *mir.Instr, chain ...source.InlineSite) *mir.Instr {
	in.Inline = chain
	return in
}

func site(callee string, line int) source.InlineSite {
	return source.InlineSite{Pos: position(line), Callee: callee}
}

// The call structure the attribution tests are built on: one callee spliced in
// at two sites, the first of which expands a further call. It is the smallest
// shape where a site's own bytes differ from the bytes it rolls up and where
// two rows carry the same callee at two depths.
var (
	outerCall  = site("popcount", 30)
	innerCall  = site("mask", 12)
	secondCall = site("popcount", 40)
)

// buildInlinedProgram lays that structure out as five lines: two the function
// owns, two from the first expansion, and one from the second.
func buildInlinedProgram(t *testing.T) *mir.Program {
	t.Helper()
	fn := mir.NewFunc("main", position(1))
	fn.NewBlock("main.entry", position(1)).Append(
		// "move r0 1" and "yield" are the function's own, 10 and 6 bytes.
		instr(t, isa.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1}),
		instr(t, isa.OpYield),
		// The first expansion: one line of its own and one from a nested call.
		inlined(instr(t, isa.OpAdd, mir.PhysReg{Reg: 0}, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1}), outerCall),
		inlined(instr(t, isa.OpYield), innerCall, outerCall),
		// The second expansion, one line.
		inlined(instr(t, isa.OpSub, mir.PhysReg{Reg: 0}, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1}), secondCall),
	)
	return &mir.Program{Funcs: []*mir.Func{fn}}
}

// TestAttributionByInlineSite is the accounting the report exists for. One
// callee spliced in at two call sites has to appear as two constructs: a single
// total for it would name a cost that deleting either call does not recover.
func TestAttributionByInlineSite(t *testing.T) {
	out := emit(t, buildInlinedProgram(t), Options{})

	// Every line but the sub pays a separator, since text follows it. The sub is
	// the last line of the program and pays only its own length.
	const (
		move  = len("move r0 1") + editorSeparator
		yield = len("yield") + editorSeparator
		add   = len("add r0 r0 1") + editorSeparator
		sub   = len("sub r0 r0 1")
	)
	want := []SiteReport{
		{Func: "main", Bytes: move + yield + add + yield + sub, Lines: 5, Own: move + yield},
		{Func: "main", Chain: []source.InlineSite{outerCall}, Bytes: add + yield, Lines: 2, Own: add},
		{Func: "main", Chain: []source.InlineSite{outerCall, innerCall}, Bytes: yield, Lines: 1, Own: yield},
		{Func: "main", Chain: []source.InlineSite{secondCall}, Bytes: sub, Lines: 1, Own: sub},
	}
	// Label names the innermost call of the chain, which is the construct the
	// row is about; every call above it is an enclosing row of its own. On the
	// nested row the two ends differ, and only there does taking the wrong one
	// show.
	wantLabels := []string{
		"main",
		"popcount inlined at t.mc:30:1",
		"mask inlined at t.mc:12:1",
		"popcount inlined at t.mc:40:1",
	}
	if len(out.Report.Sites) != len(want) {
		t.Fatalf("Sites = %+v, want %d entries", out.Report.Sites, len(want))
	}
	for i, got := range out.Report.Sites {
		if got.Func != want[i].Func || got.Bytes != want[i].Bytes || got.Lines != want[i].Lines || got.Own != want[i].Own {
			t.Errorf("Sites[%d] = %+v, want %+v", i, got, want[i])
		}
		if !slices.Equal(got.Chain, want[i].Chain) {
			t.Errorf("Sites[%d].Chain = %v, want %v", i, got.Chain, want[i].Chain)
		}
		if got.Label() != wantLabels[i] {
			t.Errorf("Sites[%d].Label() = %q, want %q", i, got.Label(), wantLabels[i])
		}
		if got.Depth() != len(want[i].Chain) {
			t.Errorf("Sites[%d].Depth() = %d, want %d", i, got.Depth(), len(want[i].Chain))
		}
	}
}

// TestNestedAttributionRowsCarryTheirDepth pins the rendered table for a call
// inside an inlined body, which no other fixture here reaches: every other stops
// at a depth of one. The whole row is pinned because what a nested row adds — the
// label, the rolled-up share, the indentation — sits past an alignment check.
func TestNestedAttributionRowsCarryTheirDepth(t *testing.T) {
	out := emit(t, buildInlinedProgram(t), Options{})

	// Counted off the byte model by hand: 11 + 7 + 13 + 7 + 11 charged bytes,
	// so the first expansion rolls up 20 of the program's 49 and the call inside
	// it 7. The line count column is as wide as "128 lines", which is what the
	// counts are right-aligned against.
	want := []string{
		"       49 bytes    5 lines  100%  main",
		"       20 bytes    2 lines   40%    popcount inlined at t.mc:30:1",
		"        7 bytes     1 line   14%      mask inlined at t.mc:12:1",
		"       11 bytes     1 line   22%    popcount inlined at t.mc:40:1",
	}
	if got := attributionRows(t, out.Report.String()); !slices.Equal(got, want) {
		t.Errorf("attribution table is\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestEmitLeavesTheProgramAlone covers the guarantee Emit's doc states. Reading a
// chain outermost first is a reversal, and doing it in place would leave every
// chain of more than one call inverted, with no line of output changing.
func TestEmitLeavesTheProgramAlone(t *testing.T) {
	prog := buildInlinedProgram(t)
	before := chainsOf(prog)

	// Checked after one emission rather than after both: reversing in place
	// twice restores the order, so a chain read back at the end would agree
	// however the reading was done.
	first := emit(t, prog, Options{})
	if got := chainsOf(prog); !slices.Equal(got, before) {
		t.Errorf("Emit left the program's call chains as\n%v\nwant\n%v", got, before)
	}

	second := emit(t, prog, Options{})
	if first.Report.String() != second.Report.String() {
		t.Errorf("emitting one program twice reported\n%s\nand then\n%s", first.Report, second.Report)
	}
}

// chainsOf renders every instruction's call chain in program order, in the
// order the instruction holds it.
func chainsOf(prog *mir.Program) []string {
	var chains []string
	for _, fn := range prog.Funcs {
		for _, block := range fn.Blocks {
			for _, in := range block.Instrs {
				for _, s := range in.Inline {
					chains = append(chains, s.String())
				}
			}
		}
	}
	return chains
}

// TestAttributionKeysSeparateChainSegments covers what keeps two different chains
// from accumulating into one construct. Each row fuses two calls with one
// candidate separator, and a key joined on that candidate collides exactly as
// running the segments together does: only a byte outside the text separates all.
func TestAttributionKeysSeparateChainSegments(t *testing.T) {
	outer := site("X", 1)
	inner := site("Y", 2)
	tests := []struct {
		name string
		// glue is what the single fused callee spells between the two calls it
		// reads as. A separator equal to it keys that chain exactly as the real
		// chain of two calls keys.
		glue string
	}{
		{name: "the two calls run together", glue: ""},
		{name: "the two calls joined by an underscore", glue: "_"},
		{name: "the two calls joined by a space", glue: " "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fused := source.InlineSite{Pos: inner.Pos, Callee: outer.String() + tt.glue + inner.Callee}
			if got, want := fused.String(), outer.String()+tt.glue+inner.String(); got != want {
				t.Fatalf("the fused site renders as %q, want %q, which is the collision this covers", got, want)
			}

			lines := []line{
				{text: "yield", pos: position(1), fn: "main", inline: []source.InlineSite{fused}},
				{text: "yield", pos: position(2), fn: "main", inline: []source.InlineSite{inner, outer}},
			}
			got := attribute(lines, []int{10, 20})

			want := []SiteReport{
				{Func: "main", Bytes: 30, Lines: 2},
				{Func: "main", Chain: []source.InlineSite{outer}, Bytes: 20, Lines: 1},
				{Func: "main", Chain: []source.InlineSite{outer, inner}, Bytes: 20, Lines: 1, Own: 20},
				{Func: "main", Chain: []source.InlineSite{fused}, Bytes: 10, Lines: 1, Own: 10},
			}
			if len(got) != len(want) {
				t.Fatalf("Sites = %+v, want %d entries", got, len(want))
			}
			for i, site := range got {
				if site.Func != want[i].Func || site.Bytes != want[i].Bytes || site.Lines != want[i].Lines || site.Own != want[i].Own {
					t.Errorf("Sites[%d] = %+v, want %+v", i, site, want[i])
				}
				if !slices.Equal(site.Chain, want[i].Chain) {
					t.Errorf("Sites[%d].Chain = %v, want %v", i, site.Chain, want[i].Chain)
				}
			}
		})
	}
}

// TestSiteChainsDoNotShareStorage covers what keeps one line's rows independent.
// Every site is built from that line's single chain, and a prefix of a slice
// keeps the whole backing array: handing the prefixes out would let an append on
// a shallow row overwrite the call the row below it names, with no byte changing.
func TestSiteChainsDoNotShareStorage(t *testing.T) {
	outer := site("outer", 1)
	inner := site("inner", 2)
	intruder := site("intruder", 3)
	chain := []source.InlineSite{outer, inner}
	lines := []line{
		// Held innermost first, which is the order an instruction carries it.
		{text: "yield", pos: position(1), fn: "main", inline: []source.InlineSite{inner, outer}},
	}

	// The deepest row has nothing below it to corrupt, so every row above it is
	// a case and it is not.
	for depth := range len(chain) {
		t.Run("extending the row at depth "+strconv.Itoa(depth), func(t *testing.T) {
			sites := attribute(lines, []int{10})
			if len(sites) != len(chain)+1 {
				t.Fatalf("Sites = %+v, want one row a depth", sites)
			}
			extended := append(sites[depth].Chain, intruder)
			for i, got := range sites {
				if !slices.Equal(got.Chain, chain[:i]) {
					t.Errorf("extending the depth %d row to %v left the depth %d row naming %v, want %v",
						depth, extended, i, got.Chain, chain[:i])
				}
			}
		})
	}
}

// TestSiteOwnBytesSumToProgramBytes is what makes the rollup honest: a parent's
// bytes include its children's, so only the exclusive numbers may be added up.
func TestSiteOwnBytesSumToProgramBytes(t *testing.T) {
	for _, readable := range []bool{false, true} {
		t.Run("readable="+strconv.FormatBool(readable), func(t *testing.T) {
			out := emit(t, buildSampleProgram(t), Options{Readable: readable})
			own := 0
			for _, s := range out.Report.Sites {
				own += s.Own
			}
			if own != out.Report.Bytes {
				t.Errorf("site Own bytes sum to %d, program is %d", own, out.Report.Bytes)
			}
		})
	}
}

// TestSitesSeparateSameNamedFunctions covers a program mir.Program.Validate
// accepts and a site table keyed by function name alone would merge. Merging them
// produces a row whose bytes belong to neither of the two FuncReports the same
// program produces.
func TestSitesSeparateSameNamedFunctions(t *testing.T) {
	first := mir.NewFunc("dup", position(1))
	first.NewBlock("first.entry", position(1)).Append(instr(t, isa.OpYield))
	second := mir.NewFunc("dup", position(2))
	second.NewBlock("second.entry", position(2)).Append(
		instr(t, isa.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1}),
	)

	out := emit(t, &mir.Program{Funcs: []*mir.Func{first, second}}, Options{})

	const (
		yield = len("yield") + editorSeparator
		move  = len("move r0 1")
	)
	// Largest first, so the second function leads despite emitting later.
	want := []SiteReport{
		{Func: "dup", Bytes: move, Lines: 1, Own: move},
		{Func: "dup", Bytes: yield, Lines: 1, Own: yield},
	}
	if len(out.Report.Sites) != len(want) {
		t.Fatalf("Sites = %+v, want %+v", out.Report.Sites, want)
	}
	for i, got := range out.Report.Sites {
		if got.Func != want[i].Func || got.Bytes != want[i].Bytes || got.Lines != want[i].Lines || got.Own != want[i].Own {
			t.Errorf("Sites[%d] = %+v, want %+v", i, got, want[i])
		}
	}
}

// TestSameNamedSitesOfEqualSizeKeepEmissionOrder covers the one tie neither sort
// key breaks: two functions of a single name and size compare equal on bytes and
// on label, so nothing but the sort's stability decides which leads. The order is
// visible only in the call each function brought with it.
func TestSameNamedSitesOfEqualSizeKeepEmissionOrder(t *testing.T) {
	// Go's unstable sort returns a slice it finds already ordered exactly as it
	// was, so a fixture of nothing but ties cannot tell the two sorts apart.
	// This one has to be sorted, and holds its ties inside a sequence that is
	// not. Charged directly, since the last line of a program pays no separator.
	const funcs = 16
	lines := make([]line, funcs)
	costs := make([]int, funcs)
	for i := range lines {
		lines[i] = line{
			text:      "yield",
			pos:       position(i + 1),
			fn:        "dup",
			fnOrdinal: i,
			inline:    []source.InlineSite{site("callee"+strconv.Itoa(i), i+1)},
		}
		costs[i] = 10 * (1 + (i*7)%4)
	}
	ordinals := make(map[string]int, funcs)
	for i, l := range lines {
		ordinals[l.inline[0].String()] = i
	}

	sites := attribute(lines, costs)
	if len(sites) != 2*funcs {
		t.Fatalf("got %d sites, want a row for each of the %d functions and one for the call it holds", len(sites), funcs)
	}
	// The row after each function is the single call it holds, which is the only
	// thing telling two otherwise identical rows apart.
	emitted := make(map[int]int, funcs)
	for i := 0; i < len(sites); i += 2 {
		fn, call := sites[i], sites[i+1]
		if fn.Depth() != 0 || fn.Label() != "dup" {
			t.Fatalf("Sites[%d] = %+v, want the row of a function", i, fn)
		}
		ordinal, named := ordinals[call.Label()]
		if !named || call.Depth() != 1 {
			t.Fatalf("Sites[%d] = %+v, want the call the row above it holds", i+1, call)
		}
		if i > 0 && fn.Bytes > sites[i-2].Bytes {
			t.Errorf("Sites[%d] holds %d bytes, above the %d of the function before it", i, fn.Bytes, sites[i-2].Bytes)
		}
		if before, tied := emitted[fn.Bytes]; tied && before > ordinal {
			t.Errorf("the %d byte functions report the one emitted %d ahead of the one emitted %d, want the order they were emitted in",
				fn.Bytes, before, ordinal)
		}
		emitted[fn.Bytes] = ordinal
	}
}

// TestReportStringRendersEveryLine pins the whole of what Report.String writes
// for a program that fits. Every other test here reaches the report through a
// substring or a parsed column, so a line naming a figure and its limit the wrong
// way round satisfies all of them: "longest line 90 of 37" still parses.
func TestReportStringRendersEveryLine(t *testing.T) {
	// Written out rather than read from a file, so that no flag can bless a
	// wrong report by rewriting the expectation from the code that produced it.
	// Both forms, because the annotation is charged bytes and nothing else.
	tests := []struct {
		name string
		opts Options
		want string
	}{
		{
			name: "default",
			want: `program: 316 of 4096 bytes (7%), 21 of 128 lines (16%), longest line 37 of 90 characters (41%)
  closest limit: the line count, at 16% spent
  data region: 2 of 512 slots, 1 of them spilled; 510 slots for call frames, and the push past the last of them stops the chip with a stack overflow
  bytes by construct, largest first; an indented call's bytes are counted in the line above it:
      299 bytes   19 lines   94%  main
       17 bytes    2 lines    5%  helper
  main: 299 bytes over 19 lines from emitted line 0, declared at t.mc:1:1
  helper: 17 bytes over 2 lines from emitted line 19, declared at t.mc:20:1`,
		},
		{
			name: "readable",
			opts: Options{Readable: true},
			want: `program: 449 of 4096 bytes (10%), 21 of 128 lines (16%), longest line 37 of 90 characters (41%)
  closest limit: the line count, at 16% spent
  data region: 2 of 512 slots, 1 of them spilled; 510 slots for call frames, and the push past the last of them stops the chip with a stack overflow
  bytes by construct, largest first; an indented call's bytes are counted in the line above it:
      416 bytes   19 lines   92%  main
       33 bytes    2 lines    7%  helper
  main: 416 bytes over 19 lines from emitted line 0, declared at t.mc:1:1
  helper: 33 bytes over 2 lines from emitted line 19, declared at t.mc:20:1`,
		},
	}
	// The sample program pokes a slot and calls through jal, so its layout is the
	// one the report has the most to say about.
	slots := SlotReport{Data: 1, Spill: 1, Frames: true}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.opts.Slots = slots
			out := emit(t, buildSampleProgram(t), tt.opts)
			if got := out.Report.String(); got != tt.want {
				t.Errorf("Report.String() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// TestReportSpendsCoverEveryLimit checks the report accounts for all three
// limits the machine enforces, not only the byte budget.
func TestReportSpendsCoverEveryLimit(t *testing.T) {
	fn := mir.NewFunc("main", position(1))
	fn.NewBlock("main.entry", position(1)).Append(
		instr(t, isa.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1}),
		instr(t, isa.OpYield),
	)

	out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{})
	want := []Spend{
		{Kind: ViolationBytes, Used: len("move r0 1") + editorSeparator + len("yield"), Max: MaxBytes},
		{Kind: ViolationLines, Used: 2, Max: MaxLines},
		{Kind: ViolationLineLength, Used: len("move r0 1"), Max: MaxLineLength},
	}
	got := out.Report.Spends()
	if len(got) != len(want) {
		t.Fatalf("Spends() = %+v, want %d entries", got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Spends()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if out.Report.LongestLine != len("move r0 1") {
		t.Errorf("LongestLine = %d, want %d", out.Report.LongestLine, len("move r0 1"))
	}
	text := out.Report.String()
	for _, want := range []string{"bytes", "lines", "characters", "closest limit"} {
		if !strings.Contains(text, want) {
			t.Errorf("report does not mention %q:\n%s", want, text)
		}
	}
}

// TestReportAccountsForTheDataRegion covers the one budget no emitted line names.
// The chip stops on the access that runs off the top of the array: push and poke
// raise StackOverFlow past slot 511, and get answers the unknown error there. A
// report naming only the text limits would leave that unsaid.
func TestReportAccountsForTheDataRegion(t *testing.T) {
	tests := []struct {
		name         string
		slots        SlotReport
		wantUsed     int
		wantHeadroom int
		// wantLine is the report's whole memory line, past its label. A
		// substring of it is satisfied by a count reported with the wrong sign,
		// which reads as a program short of the array's end by exactly as far as
		// it runs past it.
		wantLine string
	}{
		{
			name:         "a program that allocates nothing and calls nothing",
			wantUsed:     0,
			wantHeadroom: MaxSlots,
			wantLine:     "0 of 512 slots, 0 of them spilled; the program makes no call, so nothing grows into the remaining 512",
		},
		{
			name:         "globals and spill slots under call frames",
			slots:        SlotReport{Data: 40, Spill: 2, Frames: true},
			wantUsed:     42,
			wantHeadroom: MaxSlots - 42,
			wantLine:     "42 of 512 slots, 2 of them spilled; 470 slots for call frames, and the push past the last of them stops the chip with a stack overflow",
		},
		{
			// One slot left is the singular, and is the reading a count taken
			// from the wrong end of the subtraction cannot produce.
			name:         "a single slot left for call frames",
			slots:        SlotReport{Data: MaxSlots - 1, Frames: true},
			wantUsed:     MaxSlots - 1,
			wantHeadroom: 1,
			wantLine:     "511 of 512 slots, 0 of them spilled; 1 slot for call frames, and the push past the last of them stops the chip with a stack overflow",
		},
		{
			// sp initialises to 512 for this layout, so the very first push
			// faults: the program cannot make a call that saves anything at
			// all, which is a stronger statement than having no room to grow.
			name:         "a data region filling the array",
			slots:        SlotReport{Data: MaxSlots, Frames: true},
			wantUsed:     MaxSlots,
			wantHeadroom: 0,
			wantLine:     "512 of 512 slots, 0 of them spilled; no slot is left for a call frame, and the first push a frame makes stops the chip with a stack overflow",
		},
		{
			// Exhaustion and the absence of calls are separate facts, and this
			// program has both. Reporting only the second says nothing grows
			// into a headroom of zero, which describes the array as spare
			// rather than as full.
			name:         "a data region filling the array in a program that makes no call",
			slots:        SlotReport{Data: MaxSlots},
			wantUsed:     MaxSlots,
			wantHeadroom: 0,
			wantLine:     "512 of 512 slots, 0 of them spilled; the array is full, and the program makes no call, so nothing needs a slot above it",
		},
		{
			// Not a program with no headroom left but one whose own globals do
			// not fit, so saying no slot is left for a call frame describes the
			// wrong failure. The layout stages reject this, but SlotReport is an
			// input and the report has to describe what it was handed.
			name:         "a data region past the end of the array",
			slots:        SlotReport{Data: MaxSlots + 8, Frames: true},
			wantUsed:     MaxSlots + 8,
			wantHeadroom: -8,
			wantLine:     "520 of 512 slots, 0 of them spilled; the data region runs 8 slots past the end of the array, and a load past that end stops the chip with the unknown error and a store with a stack overflow",
		},
		{
			name:         "a data region one slot past the end of the array",
			slots:        SlotReport{Data: MaxSlots + 1, Frames: true},
			wantUsed:     MaxSlots + 1,
			wantHeadroom: -1,
			wantLine:     "513 of 512 slots, 0 of them spilled; the data region runs 1 slot past the end of the array, and a load past that end stops the chip with the unknown error and a store with a stack overflow",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := mir.NewFunc("main", position(1))
			fn.NewBlock("main.entry", position(1)).Append(instr(t, isa.OpYield))

			out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{Slots: tt.slots})
			if out.Report.Slots != tt.slots {
				t.Errorf("Slots = %+v, want %+v", out.Report.Slots, tt.slots)
			}
			if got := out.Report.Slots.Used(); got != tt.wantUsed {
				t.Errorf("Used() = %d, want %d", got, tt.wantUsed)
			}
			if got := out.Report.Slots.Headroom(); got != tt.wantHeadroom {
				t.Errorf("Headroom() = %d, want %d", got, tt.wantHeadroom)
			}
			if got := dataRegionLine(t, out.Report.String()); got != tt.wantLine {
				t.Errorf("the memory line reads\n%s\nwant\n%s", got, tt.wantLine)
			}
			// The data region is not one of the three limits the editor
			// enforces, so it never becomes a violation of them.
			if len(out.Report.Violations) != 0 {
				t.Errorf("Violations = %v, want none for a one line program", out.Report.Violations)
			}
		})
	}
}

// dataRegionLine is what the report says about the memory array, past the label
// the line carries.
func dataRegionLine(t *testing.T, report string) string {
	t.Helper()
	const prefix = "  data region: "
	for line := range strings.SplitSeq(report, "\n") {
		if described, found := strings.CutPrefix(line, prefix); found {
			return described
		}
	}
	t.Fatalf("the report carries no %q line:\n%s", prefix, report)
	return ""
}

// TestBindingLimitIsComputed covers the reason the answer is not asserted. The
// two budgets balance at about 30 characters of text a line, once the 254 bytes
// of separator a full grid charges come off the 4096, so short lines run out of
// lines first and long ones run out of bytes while well under 128.
func TestBindingLimitIsComputed(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T) *mir.Program
		want  ViolationKind
		// wantBytes pins where the fixture sits, for the rows placed against
		// the balance point rather than well to one side of it. Zero skips the
		// check.
		wantBytes int
	}{
		{
			name: "many short lines reach the line count first",
			build: func(t *testing.T) *mir.Program {
				t.Helper()
				fn := mir.NewFunc("main", position(1))
				block := fn.NewBlock("main.entry", position(1))
				for range MaxLines {
					block.Append(instr(t, isa.OpYield))
				}
				return &mir.Program{Funcs: []*mir.Func{fn}}
			},
			want: ViolationLines,
		},
		{
			name: "few long lines reach the byte budget first",
			build: func(t *testing.T) *mir.Program {
				t.Helper()
				fn := mir.NewFunc("main", position(1))
				block := fn.NewBlock("main.entry", position(1))
				for range 60 {
					block.Append(instr(t, isa.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1e60}))
				}
				return &mir.Program{Funcs: []*mir.Func{fn}}
			},
			want: ViolationBytes,
		},
		{
			// The balance point itself: 4096 bytes over 128 lines is 32 bytes a
			// line, so a program of exactly that ratio spends the same share of
			// both and the answer comes from the order Spends lists them in
			// rather than from the numbers.
			name:      "a program on the balance point takes the first of the two",
			build:     balancedProgram(0),
			want:      ViolationBytes,
			wantBytes: 128,
		},
		{
			name:      "one byte under the balance point the line count binds",
			build:     balancedProgram(-1),
			want:      ViolationLines,
			wantBytes: 127,
		},
		{
			name:      "one byte over it the byte budget binds",
			build:     balancedProgram(1),
			want:      ViolationBytes,
			wantBytes: 129,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := emit(t, tt.build(t), Options{})
			if tt.wantBytes != 0 && out.Report.Bytes != tt.wantBytes {
				t.Fatalf("Bytes = %d, want %d; the fixture no longer sits where the row places it", out.Report.Bytes, tt.wantBytes)
			}
			binding := out.Report.Binding()
			if binding.Kind != tt.want {
				t.Errorf("Binding() = %+v, want kind %v; spends were %+v", binding, tt.want, out.Report.Spends())
			}
			if !strings.Contains(out.Report.String(), binding.Kind.Noun()) {
				t.Errorf("report does not name the binding limit:\n%s", out.Report.String())
			}
		})
	}
}

// balancedProgram builds a four line program whose byte count sits offset bytes
// from the point where both budgets are spent in equal share. 4096 over 128 is 32
// bytes a line, so four lines balance at 128; six of those are separators, which
// leaves 122 characters of instruction text to spread over the four.
func balancedProgram(offset int) func(t *testing.T) *mir.Program {
	return func(t *testing.T) *mir.Program {
		t.Helper()
		widths := []int{30, 30, 31, 31}
		widths[0] += offset
		fn := mir.NewFunc("main", position(1))
		block := fn.NewBlock("main.entry", position(1))
		for _, width := range widths {
			block.Append(instr(t, isa.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: wideValue(t, width)}))
		}
		return &mir.Program{Funcs: []*mir.Func{fn}}
	}
}

// TestLineWidthIsReportedNotRanked pins that the 90 character line width never
// becomes the closest limit. It bounds one line's formatting, and a program
// cannot outgrow it, so a program spending most of it is one line from being
// too wide rather than close to being too big.
func TestLineWidthIsReportedNotRanked(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T) *mir.Program
	}{
		{
			name: "one line spends most of the width and almost none of either budget",
			build: func(t *testing.T) *mir.Program {
				t.Helper()
				fn := mir.NewFunc("main", position(1))
				fn.NewBlock("main.entry", position(1)).Append(
					instr(t, isa.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1e80}),
				)
				return &mir.Program{Funcs: []*mir.Func{fn}}
			},
		},
		{
			name: "a line over the width limit still does not bind",
			build: func(t *testing.T) *mir.Program {
				t.Helper()
				fn := mir.NewFunc("main", position(1))
				fn.NewBlock("main.entry", position(1)).Append(
					instr(t, isa.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1e100}),
				)
				return &mir.Program{Funcs: []*mir.Func{fn}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := emit(t, tt.build(t), Options{})
			if got := out.Report.Binding().Kind; got == ViolationLineLength {
				t.Errorf("Binding() named the line width; spends were %+v", out.Report.Spends())
			}
			text := out.Report.String()
			if !strings.Contains(text, "characters") {
				t.Errorf("report does not state the line width:\n%s", text)
			}
			if strings.Contains(text, "closest limit: "+ViolationLineLength.Noun()) {
				t.Errorf("report ranked the line width as the closest limit:\n%s", text)
			}
		})
	}
}

// TestSitesOfEqualSizeAreOrderedByLabel pins the attribution table's second sort
// key. The stable sort already fixes two equal rows at the order they were built
// in; what the label adds is that the order does not depend on emission at all,
// so the same two constructs sort alike whichever was written first.
func TestSitesOfEqualSizeAreOrderedByLabel(t *testing.T) {
	// Different widths on purpose: a line is charged a separator only while a
	// line below it holds text, so two one-line functions cost the same only
	// when the earlier line is two characters the narrower.
	zed := mir.NewFunc("zed", position(1))
	zed.NewBlock("zed.entry", position(1)).Append(instr(t, isa.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: wideValue(t, 20)}))
	alpha := mir.NewFunc("alpha", position(2))
	alpha.NewBlock("alpha.entry", position(2)).Append(instr(t, isa.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: wideValue(t, 22)}))

	out := emit(t, &mir.Program{Funcs: []*mir.Func{zed, alpha}}, Options{})
	if len(out.Report.Sites) != 2 {
		t.Fatalf("Sites = %+v, want one per function", out.Report.Sites)
	}
	if a, b := out.Report.Sites[0], out.Report.Sites[1]; a.Bytes != b.Bytes {
		t.Fatalf("sites cost %d and %d bytes, want the tie the label is there to break", a.Bytes, b.Bytes)
	}
	got := []string{out.Report.Sites[0].Label(), out.Report.Sites[1].Label()}
	want := []string{"alpha", "zed"}
	if !slices.Equal(got, want) {
		t.Errorf("Sites = %v, want %v: zed is emitted first, so only the label puts alpha above it", got, want)
	}
}

// TestOverBudgetReportNamesWhatToCut is the whole point of not refusing: the
// report has to lead with the construct that would recover the most bytes.
func TestOverBudgetReportNamesWhatToCut(t *testing.T) {
	expensive := site("expand", 20)
	cheap := site("trim", 25)

	fn := mir.NewFunc("main", position(1))
	block := fn.NewBlock("main.entry", position(1))
	for range 200 {
		block.Append(inlined(instr(t, isa.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1e60}), expensive))
	}
	block.Append(inlined(instr(t, isa.OpYield), cheap))

	out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{})
	if out.Report.Bytes <= MaxBytes {
		t.Fatalf("Bytes = %d, want it over the %d byte budget", out.Report.Bytes, MaxBytes)
	}
	if out.Text == "" {
		t.Error("an over budget program emitted no text; the report is worth nothing without it")
	}
	if len(out.Report.Sites) != 3 {
		t.Fatalf("Sites = %+v, want the function and its two call sites", out.Report.Sites)
	}
	if got := out.Report.Sites[1].Label(); got != expensive.String() {
		t.Errorf("the largest construct after the function is %q, want %q", got, expensive.String())
	}
	text := out.Report.String()
	if !strings.Contains(text, "over limit") || !strings.Contains(text, expensive.String()) {
		t.Errorf("the report does not say what to cut:\n%s", text)
	}
}

// TestAttributionColumnsAlignPastTheLineLimit covers the width of the attribution
// table's line count column. The width is set from the line limit, and this
// report is printed precisely when a program exceeds one, so a cell too wide for
// the column pushes its row along and the table stops reading as one.
func TestAttributionColumnsAlignPastTheLineLimit(t *testing.T) {
	body := site("expand", 20)
	tail := site("trim", 25)

	fn := mir.NewFunc("main", position(1))
	block := fn.NewBlock("main.entry", position(1))
	for range MaxLines * 8 {
		block.Append(inlined(instr(t, isa.OpYield), body))
	}
	block.Append(inlined(instr(t, isa.OpYield), tail))

	out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{})
	if out.Report.Lines <= MaxLines {
		t.Fatalf("Lines = %d, want it over the %d line limit", out.Report.Lines, MaxLines)
	}
	rows := attributionRows(t, out.Report.String())
	if len(rows) != 3 {
		t.Fatalf("got %d attribution rows, want the function and its two call sites:\n%s", len(rows), out.Report)
	}
	// The share column is fixed width and follows the line count, so its offset
	// is the same on every row exactly when the counts share a column.
	want := strings.Index(rows[0], "%")
	for _, row := range rows[1:] {
		if got := strings.Index(row, "%"); got != want {
			t.Errorf("the share column starts at %d on %q and at %d on %q", got, row, want, rows[0])
		}
	}
}

// TestReportOfAProgramWithNoLines covers the attribution header's guard: a
// program that emitted no line has no construct to attribute, so printing the
// header would announce an accounting that nothing follows. A function whose only
// block holds no instructions is how such a program is reached.
func TestReportOfAProgramWithNoLines(t *testing.T) {
	fn := mir.NewFunc("main", position(1))
	fn.NewBlock("main.entry", position(1))

	out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{})
	if out.Text != "" {
		t.Fatalf("Text = %q, want nothing for a program holding no instruction", out.Text)
	}
	if out.Report.Bytes != 0 || out.Report.Lines != 0 {
		t.Errorf("Report is %d bytes over %d lines, want nothing spent", out.Report.Bytes, out.Report.Lines)
	}
	if len(out.Report.Sites) != 0 {
		t.Fatalf("Sites = %+v, want none", out.Report.Sites)
	}
	text := out.Report.String()
	if strings.Contains(text, "bytes by construct") {
		t.Errorf("the report announces an attribution table with no row under it:\n%s", text)
	}
	if rows := attributionRows(t, text); len(rows) != 0 {
		t.Errorf("attribution rows = %q, want none", rows)
	}
}

// TestPad covers the column the attribution table's line counts sit in. The cell
// wider than its column is the case no report reaches, and is checked because a
// pad that cut it to fit would take a digit off a count — in a report printed
// precisely when a program is over a limit.
func TestPad(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		columns int
		want    string
	}{
		{name: "narrower than the column", text: "1 line", columns: 9, want: "   1 line"},
		{name: "exactly the column", text: "128 lines", columns: 9, want: "128 lines"},
		{name: "wider than the column", text: "1024 lines", columns: 9, want: "1024 lines"},
		{name: "no column to fill", text: "1 line", columns: 0, want: "1 line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pad(tt.text, tt.columns); got != tt.want {
				t.Errorf("pad(%q, %d) = %q, want %q", tt.text, tt.columns, got, tt.want)
			}
		})
	}
}

// attributionRows returns the rows of the report's per-construct table, which
// are the indented lines under its header.
func attributionRows(t *testing.T, report string) []string {
	t.Helper()
	const header = "  bytes by construct,"
	var rows []string
	inTable := false
	for line := range strings.SplitSeq(report, "\n") {
		switch {
		case strings.HasPrefix(line, header):
			inTable = true
		case inTable && strings.HasPrefix(line, "    "):
			rows = append(rows, line)
		case inTable:
			return rows
		}
	}
	return rows
}

func TestViolationKindString(t *testing.T) {
	tests := []struct {
		kind ViolationKind
		want string
	}{
		{kind: ViolationBytes, want: "bytes"},
		{kind: ViolationLines, want: "lines"},
		{kind: ViolationLineLength, want: "line length"},
		{kind: ViolationKind(7), want: "ViolationKind(7)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.kind.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestViolationKindNoun(t *testing.T) {
	tests := []struct {
		kind ViolationKind
		want string
	}{
		{kind: ViolationBytes, want: "the byte budget"},
		{kind: ViolationLines, want: "the line count"},
		{kind: ViolationLineLength, want: "the line width"},
		{kind: ViolationKind(7), want: "ViolationKind(7)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.kind.Noun(); got != tt.want {
				t.Errorf("Noun() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEveryViolationKindIsNamed(t *testing.T) {
	for k := ViolationBytes; k <= ViolationLineLength; k++ {
		if s := k.String(); strings.Contains(s, "ViolationKind(") {
			t.Errorf("ViolationKind %d has no name", k)
		}
		if s := k.Noun(); strings.Contains(s, "ViolationKind(") {
			t.Errorf("ViolationKind %d has no noun", k)
		}
	}
	if s := (ViolationLineLength + 1).String(); !strings.Contains(s, "ViolationKind(") {
		t.Errorf("ViolationKind(%d) is named %q, so the loop above stops short of the last kind", ViolationLineLength+1, s)
	}
}

func TestSpendPercent(t *testing.T) {
	tests := []struct {
		name  string
		spend Spend
		want  int
	}{
		{name: "half", spend: Spend{Used: 64, Max: 128}, want: 50},
		{name: "over", spend: Spend{Used: 256, Max: 128}, want: 200},
		{name: "rounds down", spend: Spend{Used: 1, Max: 128}, want: 0},
		{name: "no limit", spend: Spend{Used: 5}, want: 0},
		{name: "exactly the limit", spend: Spend{Used: MaxBytes, Max: MaxBytes}, want: 100},
		// The two readings either side of the limit are the ones that have to
		// differ from it: a share rounded down both ways calls a program one byte
		// over budget and one byte short of it 100% alike.
		{name: "one short of the limit", spend: Spend{Used: MaxBytes - 1, Max: MaxBytes}, want: 99},
		{name: "one over the limit", spend: Spend{Used: MaxBytes + 1, Max: MaxBytes}, want: 101},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.spend.Percent(); got != tt.want {
				t.Errorf("Percent() = %d, want %d", got, tt.want)
			}
		})
	}
}
