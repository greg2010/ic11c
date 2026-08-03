package emit

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// checkGolden compares got against testdata/name, rewriting the file when
// -update is set.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v; rerun with -update after reviewing the output", path, err)
	}
	if got != string(want) {
		t.Errorf("output does not match %s\nwant:\n%s\ngot:\n%s\nrerun with -update after reviewing the difference", path, want, got)
	}
}

func position(line int) source.Position {
	return source.Position{File: "t.mc", Offset: line, Line: line, Column: 1}
}

func instr(t *testing.T, op ic10.Opcode, args ...mir.Operand) *mir.Instr {
	t.Helper()
	return at(t, 1, op, args...)
}

func at(t *testing.T, line int, op ic10.Opcode, args ...mir.Operand) *mir.Instr {
	t.Helper()
	in, err := mir.NewInstr(op, position(line), args...)
	if err != nil {
		t.Fatalf("NewInstr(%v): %v", op, err)
	}
	return in
}

func emit(t *testing.T, prog *mir.Program, opts Options) Output {
	t.Helper()
	out, err := Emit(prog, opts)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	return out
}

// buildSampleProgram exercises every operand form the machine takes, in both a
// forward and a backward branch, across two functions.
func buildSampleProgram(t *testing.T) *mir.Program {
	t.Helper()
	temperature := lookupLogicType(t, "Temperature")
	setting := lookupLogicType(t, "Setting")
	slotType := ic10.LogicSlotTypes[2].Value

	pin0, err := mir.NewDevicePin(0, mir.NoConnection)
	if err != nil {
		t.Fatalf("NewDevicePin: %v", err)
	}
	pin1, err := mir.NewDevicePin(1, mir.NoConnection)
	if err != nil {
		t.Fatalf("NewDevicePin: %v", err)
	}

	r := func(n uint8) mir.PhysReg { return mir.PhysReg{Reg: ic10.Register(n)} }
	const prefab = -1252983604

	main := mir.NewFunc("main", position(1))
	entry := main.NewBlock("main.entry", position(1))
	loop := main.NewBlock("main.loop", position(8))
	done := main.NewBlock("main.done", position(14))

	entry.Append(
		at(t, 1, ic10.OpClr, mir.NewDeviceBase()),
		at(t, 1, ic10.OpMove, mir.PhysReg{Reg: ic10.RegSP}, mir.Imm{Value: 400}),
		at(t, 2, ic10.OpL, r(0), pin0, mir.LogicType{Value: temperature}),
		at(t, 3, ic10.OpLs, r(1), pin1, mir.Imm{Value: 2}, mir.LogicSlotType{Value: slotType}),
		at(t, 4, ic10.OpLb, r(2), mir.Imm{Value: prefab}, mir.LogicType{Value: temperature}, mir.BatchMode{Value: 0}),
		at(t, 5, ic10.OpLbn, r(3), mir.Imm{Value: prefab}, mir.Imm{Value: 12345}, mir.LogicType{Value: setting}, mir.BatchMode{Value: 1}),
		at(t, 6, ic10.OpLr, r(4), pin0, mir.ReagentMode{Value: 3}, mir.Imm{Value: 5}),
		at(t, 6, ic10.OpMove, r(5), mir.Imm{Value: 3.141592653589793}),
		at(t, 6, ic10.OpMove, r(6), mir.Imm{Value: negativeZero()}),
		at(t, 6, ic10.OpMove, r(7), r(0)),
		at(t, 7, ic10.OpBeq, r(0), r(1), mir.Label{Name: "main.done"}),
		at(t, 7, ic10.OpJ, mir.Label{Name: "main.loop"}),
	)
	entry.AddSucc(loop)
	entry.AddSucc(done)

	loop.Append(
		at(t, 9, ic10.OpAdd, r(0), r(0), mir.Imm{Value: 1}),
		at(t, 10, ic10.OpPoke, mir.Imm{Value: 0}, r(0)),
		at(t, 11, ic10.OpGet, r(8), mir.NewDeviceBase(), mir.Imm{Value: 0}),
		at(t, 12, ic10.OpBne, r(0), mir.Imm{Value: 10}, mir.Label{Name: "main.loop"}),
	)
	loop.AddSucc(loop)
	loop.AddSucc(done)

	done.Append(
		at(t, 15, ic10.OpJal, mir.Label{Name: "helper.entry"}),
		at(t, 16, ic10.OpYield),
		at(t, 16, ic10.OpJ, mir.Label{Name: "main.entry"}),
	)
	done.AddSucc(entry)

	helper := mir.NewFunc("helper", position(20))
	helperEntry := helper.NewBlock("helper.entry", position(20))
	helperEntry.Append(
		at(t, 21, ic10.OpSub, r(0), r(0), mir.Imm{Value: 1}),
		at(t, 22, ic10.OpJ, mir.PhysReg{Reg: ic10.RegRA}),
	)

	return &mir.Program{Funcs: []*mir.Func{main, helper}}
}

// buildEmptyFunctionProgram puts a function whose only block holds no
// instructions between two functions that emit. It is the shape the report
// documents but no fixture reaches: a row covering an empty range of lines,
// sitting on the first line of whatever emitted next.
func buildEmptyFunctionProgram(t *testing.T) *mir.Program {
	t.Helper()
	main := mir.NewFunc("main", position(1))
	main.NewBlock("main.entry", position(1)).Append(
		instr(t, ic10.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1}),
	)
	empty := mir.NewFunc("empty", position(2))
	empty.NewBlock("empty.entry", position(2))
	tail := mir.NewFunc("tail", position(3))
	tail.NewBlock("tail.entry", position(3)).Append(instr(t, ic10.OpYield))
	return &mir.Program{Funcs: []*mir.Func{main, empty, tail}}
}

func negativeZero() float64 {
	zero := 0.0
	return -zero
}

func TestEmitGolden(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		file string
	}{
		{name: "default", file: "sample.ic10"},
		{name: "readable", opts: Options{Readable: true}, file: "sample.readable.ic10"},
	}
	// The sample program pokes a slot and calls through jal, so its layout is
	// the one the report has the most to say about.
	slots := SlotReport{Data: 1, Spill: 1, Frames: true}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.opts.Slots = slots
			out := emit(t, buildSampleProgram(t), tt.opts)
			checkGolden(t, tt.file, out.Text)
			checkGolden(t, tt.file+".report", out.Report.String()+"\n")
		})
	}
}

// TestEmitProducesNoBlankLines is the property the whole package is built
// around: a blank line costs a line against the 128 line limit and an
// instruction against the tick budget while computing nothing.
func TestEmitProducesNoBlankLines(t *testing.T) {
	programs := []struct {
		name  string
		build func(t *testing.T) *mir.Program
	}{
		{name: "sample", build: buildSampleProgram},
		// A function and a block that emit nothing are where a stray separator
		// line would come from, since neither leaves a line of its own behind.
		{name: "a function that emits nothing", build: buildEmptyFunctionProgram},
	}
	for _, prog := range programs {
		for _, readable := range []bool{false, true} {
			t.Run(prog.name+"/readable="+strconv.FormatBool(readable), func(t *testing.T) {
				out := emit(t, prog.build(t), Options{Readable: readable})
				if strings.HasSuffix(out.Text, "\n") {
					t.Error("output ends with a newline, which is a trailing blank line")
				}
				if strings.Contains(out.Text, "\n\n") {
					t.Error("output contains a blank line")
				}
				for i, line := range strings.Split(out.Text, "\n") {
					if strings.TrimSpace(line) == "" {
						t.Errorf("line %d is blank", i)
					}
					if strings.Contains(line, "#") {
						t.Errorf("line %d is a comment: %q", i, line)
					}
					if strings.HasPrefix(line, "alias ") || strings.HasPrefix(line, "define ") {
						t.Errorf("line %d is an assembler directive: %q", i, line)
					}
				}
			})
		}
	}
}

// TestLabelResolution covers both directions. Branches are absolute, so a
// forward target is a line the emitter has not reached yet and a backward one
// is a line it already passed.
func TestLabelResolution(t *testing.T) {
	fn := mir.NewFunc("main", position(1))
	entry := fn.NewBlock("main.entry", position(1))
	middle := fn.NewBlock("main.middle", position(2))
	tail := fn.NewBlock("main.tail", position(3))
	entry.Append(
		instr(t, ic10.OpJ, mir.Label{Name: "main.tail"}),
		instr(t, ic10.OpJ, mir.Label{Name: "main.middle"}),
	)
	middle.Append(
		instr(t, ic10.OpJ, mir.Label{Name: "main.entry"}),
	)
	tail.Append(
		instr(t, ic10.OpJ, mir.Label{Name: "main.middle"}),
	)

	out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{})
	want := "j 3\nj 2\nj 0\nj 2"
	if out.Text != want {
		t.Errorf("Text = %q, want %q", out.Text, want)
	}
}

// TestLabelResolutionOfEmptyBlock pins where a label with no instructions of
// its own points: at whatever runs next, or one past the end.
func TestLabelResolutionOfEmptyBlock(t *testing.T) {
	fn := mir.NewFunc("main", position(1))
	entry := fn.NewBlock("main.entry", position(1))
	fn.NewBlock("main.empty", position(2))
	tail := fn.NewBlock("main.tail", position(3))
	fn.NewBlock("main.end", position(4))
	entry.Append(instr(t, ic10.OpJ, mir.Label{Name: "main.empty"}))
	tail.Append(instr(t, ic10.OpJ, mir.Label{Name: "main.end"}))

	out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{})
	want := "j 1\nj 2"
	if out.Text != want {
		t.Errorf("Text = %q, want %q", out.Text, want)
	}
}

func TestEmitErrors(t *testing.T) {
	tests := []struct {
		name    string
		build   func(t *testing.T) *mir.Program
		wantErr error
		mention string
	}{
		{
			name: "label naming no block",
			build: func(t *testing.T) *mir.Program {
				t.Helper()
				fn := mir.NewFunc("main", position(1))
				fn.NewBlock("main.entry", position(1)).Append(instr(t, ic10.OpJ, mir.Label{Name: "main.gone"}))
				return &mir.Program{Funcs: []*mir.Func{fn}}
			},
			wantErr: ErrUnresolvedLabel,
			mention: "main.gone",
		},
		{
			name: "virtual register survived allocation",
			build: func(t *testing.T) *mir.Program {
				t.Helper()
				fn := mir.NewFunc("main", position(1))
				fn.NewBlock("main.entry", position(1)).Append(instr(t, ic10.OpMove, mir.VirtReg{ID: 2}, mir.Imm{Value: 1}))
				return &mir.Program{Funcs: []*mir.Func{fn}}
			},
			wantErr: ErrVirtualRegister,
			mention: "vr2",
		},
		{
			name: "program that fails validation",
			build: func(t *testing.T) *mir.Program {
				t.Helper()
				fn := mir.NewFunc("main", position(1))
				fn.NewBlock("", position(1)).Append(instr(t, ic10.OpYield))
				return &mir.Program{Funcs: []*mir.Func{fn}}
			},
			mention: "no label",
		},
		{
			name:    "empty program",
			build:   func(*testing.T) *mir.Program { return &mir.Program{} },
			mention: "no functions",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := Emit(tt.build(t), Options{})
			if err == nil {
				t.Fatalf("Emit succeeded with %q, want an error", out.Text)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("Emit error = %v, want %v", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.mention) {
				t.Errorf("Emit error = %q, want it to mention %q", err, tt.mention)
			}
			if out.Text != "" {
				t.Errorf("Emit returned text %q alongside an error", out.Text)
			}
		})
	}
}

// TestLayoutRefusesALabelDefinitionWithNoEmittedName covers the second reader of
// the readable form's name table.
//
// A branch target the table has no name for is ErrUnresolvedLabel, and the
// definition line the target reaches was spelled without asking: a missing name
// emitted a bare ":", which is a line the chip's parser reads as an instruction
// it has no name for. Emit builds both tables from one block list, so the shape
// is reached through layout rather than through Emit.
func TestLayoutRefusesALabelDefinitionWithNoEmittedName(t *testing.T) {
	fn := mir.NewFunc("main", position(1))
	fn.NewBlock("main.entry", position(1)).Append(instr(t, ic10.OpYield))
	prog := &mir.Program{Funcs: []*mir.Func{fn}}

	lines, funcs, err := layout(prog, renderer{readable: true, lineOf: resolveLabels(prog)})
	if !errors.Is(err, ErrUnresolvedLabel) {
		t.Fatalf("layout = %v, %v, %v, want %v", lines, funcs, err, ErrUnresolvedLabel)
	}
	if !strings.Contains(err.Error(), "main.entry") {
		t.Errorf("layout error = %q, want it to mention %q", err, "main.entry")
	}
}

func TestEmitNilProgram(t *testing.T) {
	if _, err := Emit(nil, Options{}); err == nil {
		t.Fatal("Emit(nil) returned no error")
	}
}

// TestByteAccounting checks the report against a count done by hand. A line is
// charged its own length and a two byte separator while text follows it, so the
// emitted text, which joins lines with one byte and ends without a separator,
// is shorter than the reported total by one byte per join.
func TestByteAccounting(t *testing.T) {
	main := mir.NewFunc("main", position(1))
	main.NewBlock("main.entry", position(1)).Append(
		instr(t, ic10.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1}),
		instr(t, ic10.OpJ, mir.Label{Name: "helper.entry"}),
	)
	helper := mir.NewFunc("helper", position(2))
	helper.NewBlock("helper.entry", position(2)).Append(instr(t, ic10.OpYield))

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
// it scans backward for the last slot whose text is non-empty, sums every
// slot's text length, and charges two bytes after each slot below that index.
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
			// The backward scan reaches slot 0 and breaks there, so no slot is
			// below it and the sum loop charges no separator.
			name:  "one line pays no separator",
			texts: []string{"yield"},
			want:  5,
		},
		{
			// The scan breaks at slot 1, so slot 0 alone is charged a separator.
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
			// The scan skips the empty slot and breaks at slot 0, so the blank
			// costs neither a length nor a separator.
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
			// The scan breaks at slot 2, which puts the blank below it. Its
			// length is zero but its separator is charged like any other.
			name:  "a blank line between two lines still pays its separator",
			texts: []string{"yield", "", "yield"},
			want:  5 + 2 + 0 + 2 + 5,
		},
		{
			// Nothing breaks the backward scan, so it falls out at index 0 and
			// nothing below it exists to charge.
			name:  "a program of nothing but blanks costs nothing",
			texts: []string{"", "", ""},
			want:  0,
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

// exactBudgetText is the text a program of count non-empty lines may hold to
// land on exactly MaxBytes, once the count-1 separators below its last line of
// text are charged.
func exactBudgetText(count int) int { return MaxBytes - separatorBytes*(count-1) }

// TestByteBudgetBoundary pins the accounting against the decision it feeds.
// UpdateFileSize enables the submit button at _fileSize <= 4096, so a program
// landing on exactly 4096 is one the game accepts and the report must not call
// a violation.
func TestByteBudgetBoundary(t *testing.T) {
	tests := []struct {
		name          string
		count         int
		textBytes     int
		wantBytes     int
		wantViolation bool
	}{
		{
			name:      "exactly the budget fits",
			count:     MaxLines,
			textBytes: exactBudgetText(MaxLines),
			wantBytes: MaxBytes,
		},
		{
			// One byte of text more. Charging one byte a separator would have
			// counted this program at 3970 and called it a fit, which is the
			// disagreement with the game this accounting exists to close.
			name:          "one byte over the budget is a violation",
			count:         MaxLines,
			textBytes:     exactBudgetText(MaxLines) + 1,
			wantBytes:     MaxBytes + 1,
			wantViolation: true,
		},
		{
			// Half the slots hold text, so the separator count comes from the
			// last line of text rather than from a full grid's fixed 127. A
			// boundary case that fills the grid cannot tell the two apart.
			name:      "a shorter program reaches the budget on fewer separators",
			count:     MaxLines / 2,
			textBytes: exactBudgetText(MaxLines / 2),
			wantBytes: MaxBytes,
		},
		{
			name:          "one byte over on fewer separators is still a violation",
			count:         MaxLines / 2,
			textBytes:     exactBudgetText(MaxLines/2) + 1,
			wantBytes:     MaxBytes + 1,
			wantViolation: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := buildReport(budgetLines(t, tt.count, tt.textBytes), nil, SlotReport{})
			if report.Bytes != tt.wantBytes {
				t.Errorf("Bytes = %d, want %d", report.Bytes, tt.wantBytes)
			}
			over := slices.ContainsFunc(report.Violations, func(v Violation) bool {
				return v.Kind == ViolationBytes
			})
			if over != tt.wantViolation {
				t.Errorf("byte violation = %v, want %v; violations were %v", over, tt.wantViolation, report.Violations)
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
		// A function charged an empty range of lines has to contribute zero
		// rather than whatever the line it starts on costs its real owner.
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
		{Name: "main", Pos: position(1), Bytes: len("move r0 1") + separatorBytes, Lines: 1, FirstLine: 0},
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
		block.Append(instr(t, ic10.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1e60}))
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
	if !strings.Contains(violation.Msg, strconv.Itoa(out.Report.Bytes-MaxBytes)) {
		t.Errorf("Msg = %q, want it to say how far over the budget is", violation.Msg)
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
		name          string
		lines         int
		wantViolation bool
	}{
		{name: "one line short of the limit", lines: MaxLines - 1},
		{name: "exactly the limit fits", lines: MaxLines},
		{name: "one line over the limit", lines: MaxLines + 1, wantViolation: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := mir.NewFunc("long", position(1))
			block := fn.NewBlock("long.entry", position(1))
			for range tt.lines {
				block.Append(instr(t, ic10.OpYield))
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
			over := slices.ContainsFunc(out.Report.Violations, func(v Violation) bool {
				return v.Kind == ViolationLines
			})
			if over != tt.wantViolation {
				t.Errorf("line count violation = %v, want %v; violations were %v", over, tt.wantViolation, out.Report.Violations)
			}
			if !tt.wantViolation && len(out.Report.Violations) != 0 {
				t.Errorf("Violations = %v, want none", out.Report.Violations)
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
	block.Append(at(t, 4, ic10.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1e-100}))
	block.Append(at(t, 5, ic10.OpYield))

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

func TestLineAtExactlyTheLimitIsNotAViolation(t *testing.T) {
	padding := strings.Repeat("0", MaxLineLength-len("move r0 1"))
	value, err := strconv.ParseFloat("1"+padding, 64)
	if err != nil {
		t.Fatalf("ParseFloat: %v", err)
	}
	fn := mir.NewFunc("edge", position(1))
	fn.NewBlock("edge.entry", position(1)).Append(instr(t, ic10.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: value}))

	out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{})
	if got := len(out.Text); got != MaxLineLength {
		t.Fatalf("line is %d characters, want exactly %d", got, MaxLineLength)
	}
	if len(out.Report.Violations) != 0 {
		t.Errorf("Violations = %v, want none at exactly the limit", out.Report.Violations)
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

// TestAttributionByInlineSite is the accounting the report exists for. One
// callee spliced in at two call sites has to appear as two constructs: a single
// total for it would name a cost that deleting either call does not recover.
func TestAttributionByInlineSite(t *testing.T) {
	first := site("popcount", 30)
	second := site("popcount", 40)
	inner := site("mask", 12)

	fn := mir.NewFunc("main", position(1))
	fn.NewBlock("main.entry", position(1)).Append(
		// "move r0 1" and "yield" are the function's own, 10 and 6 bytes.
		instr(t, ic10.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1}),
		instr(t, ic10.OpYield),
		// The first expansion: one line of its own and one from a nested call.
		inlined(instr(t, ic10.OpAdd, mir.PhysReg{Reg: 0}, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1}), first),
		inlined(instr(t, ic10.OpYield), inner, first),
		// The second expansion, one line.
		inlined(instr(t, ic10.OpSub, mir.PhysReg{Reg: 0}, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1}), second),
	)

	out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{})

	// Every line but the sub pays a separator, since text follows it. The sub is
	// the last line of the program and pays only its own length.
	const (
		move  = len("move r0 1") + separatorBytes
		yield = len("yield") + separatorBytes
		add   = len("add r0 r0 1") + separatorBytes
		sub   = len("sub r0 r0 1")
	)
	want := []SiteReport{
		{Func: "main", Bytes: move + yield + add + yield + sub, Lines: 5, Own: move + yield},
		{Func: "main", Chain: []source.InlineSite{first}, Bytes: add + yield, Lines: 2, Own: add},
		{Func: "main", Chain: []source.InlineSite{first, inner}, Bytes: yield, Lines: 1, Own: yield},
		{Func: "main", Chain: []source.InlineSite{second}, Bytes: sub, Lines: 1, Own: sub},
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
	}

	if got, want := out.Report.Sites[1].Label(), "popcount inlined at t.mc:30:1"; got != want {
		t.Errorf("Label() = %q, want %q", got, want)
	}
	if got := out.Report.Sites[2].Depth(); got != 2 {
		t.Errorf("Depth() = %d, want 2 for a call inside an inlined body", got)
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
// accepts and a site table keyed by function name alone would merge. Two
// functions of one name are two constructs, and merging them produces a row
// whose bytes belong to neither of the two FuncReports the same program
// produces.
func TestSitesSeparateSameNamedFunctions(t *testing.T) {
	first := mir.NewFunc("dup", position(1))
	first.NewBlock("first.entry", position(1)).Append(instr(t, ic10.OpYield))
	second := mir.NewFunc("dup", position(2))
	second.NewBlock("second.entry", position(2)).Append(
		instr(t, ic10.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1}),
	)

	out := emit(t, &mir.Program{Funcs: []*mir.Func{first, second}}, Options{})

	const (
		yield = len("yield") + separatorBytes
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

// TestReportSpendsCoverEveryLimit checks the report accounts for all three
// limits the machine enforces, not only the byte budget.
func TestReportSpendsCoverEveryLimit(t *testing.T) {
	fn := mir.NewFunc("main", position(1))
	fn.NewBlock("main.entry", position(1)).Append(
		instr(t, ic10.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1}),
		instr(t, ic10.OpYield),
	)

	out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{})
	want := []Spend{
		{Kind: ViolationBytes, Used: len("move r0 1") + separatorBytes + len("yield"), Max: MaxBytes},
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

// TestReportAccountsForTheDataRegion covers the one budget whose overrun does
// not announce itself. The 512 slot array holds the data region and the call
// frames with nothing between them and no hardware protection, so a frame that
// reaches a global overwrites it and nothing traps. A report that named only
// the text limits would leave that unsaid.
func TestReportAccountsForTheDataRegion(t *testing.T) {
	tests := []struct {
		name         string
		slots        SlotReport
		wantUsed     int
		wantHeadroom int
		wantText     []string
	}{
		{
			name:         "a program that allocates nothing and calls nothing",
			wantUsed:     0,
			wantHeadroom: MaxSlots,
			wantText:     []string{"0 of 512 slots", "makes no call"},
		},
		{
			name:         "globals and spill slots under call frames",
			slots:        SlotReport{Data: 40, Spill: 2, Frames: true},
			wantUsed:     42,
			wantHeadroom: MaxSlots - 42,
			wantText:     []string{"42 of 512 slots", "2 of them spilled", "470 slots"},
		},
		{
			name:         "a data region filling the array",
			slots:        SlotReport{Data: MaxSlots, Frames: true},
			wantUsed:     MaxSlots,
			wantHeadroom: 0,
			wantText:     []string{"512 of 512 slots", "no slot"},
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
			wantText:     []string{"512 of 512 slots", "the array is full"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := mir.NewFunc("main", position(1))
			fn.NewBlock("main.entry", position(1)).Append(instr(t, ic10.OpYield))

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
			text := out.Report.String()
			for _, want := range tt.wantText {
				if !strings.Contains(text, want) {
					t.Errorf("report does not mention %q:\n%s", want, text)
				}
			}
			// The data region is not one of the three limits the editor
			// enforces, so it never becomes a violation of them.
			if len(out.Report.Violations) != 0 {
				t.Errorf("Violations = %v, want none for a one line program", out.Report.Violations)
			}
		})
	}
}

// TestBindingLimitIsComputed covers the reason the answer is not asserted. The
// two budgets balance at 32 bytes a line, counting the separator charged after
// each, so a program of short lines runs out of lines first and a program of
// long ones runs out of bytes while still well under 128 lines.
func TestBindingLimitIsComputed(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T) *mir.Program
		want  ViolationKind
	}{
		{
			name: "many short lines reach the line count first",
			build: func(t *testing.T) *mir.Program {
				t.Helper()
				fn := mir.NewFunc("main", position(1))
				block := fn.NewBlock("main.entry", position(1))
				for range MaxLines {
					block.Append(instr(t, ic10.OpYield))
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
					block.Append(instr(t, ic10.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1e60}))
				}
				return &mir.Program{Funcs: []*mir.Func{fn}}
			},
			want: ViolationBytes,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := emit(t, tt.build(t), Options{})
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
					instr(t, ic10.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1e80}),
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
					instr(t, ic10.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1e100}),
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

// TestOverBudgetReportNamesWhatToCut is the whole point of not refusing: the
// report has to lead with the construct that would recover the most bytes.
func TestOverBudgetReportNamesWhatToCut(t *testing.T) {
	expensive := site("expand", 20)
	cheap := site("trim", 25)

	fn := mir.NewFunc("main", position(1))
	block := fn.NewBlock("main.entry", position(1))
	for range 200 {
		block.Append(inlined(instr(t, ic10.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1e60}), expensive))
	}
	block.Append(inlined(instr(t, ic10.OpYield), cheap))

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

// TestAttributionColumnsAlignPastTheLineLimit covers the width of the
// attribution table's line count column. The width is set from the line limit,
// and this report is printed precisely when a program exceeds one, so a program
// over the line limit carries counts that column was never sized for. A cell
// too wide for it pushes the rest of its row along and the table stops reading
// as a table.
func TestAttributionColumnsAlignPastTheLineLimit(t *testing.T) {
	body := site("expand", 20)
	tail := site("trim", 25)

	fn := mir.NewFunc("main", position(1))
	block := fn.NewBlock("main.entry", position(1))
	for range MaxLines * 8 {
		block.Append(inlined(instr(t, ic10.OpYield), body))
	}
	block.Append(inlined(instr(t, ic10.OpYield), tail))

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

// TestReadableUsesMangledLabels ties the two halves together: readable output
// is the only place a label name reaches the chip, so it is the only place the
// reserved word collision can bite.
func TestReadableUsesMangledLabels(t *testing.T) {
	fn := mir.NewFunc("main", position(1))
	entry := fn.NewBlock("Temperature", position(1))
	entry.Append(instr(t, ic10.OpJ, mir.Label{Name: "Temperature"}))

	out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{Readable: true})
	want := "Temperature_:\nj Temperature_"
	if out.Text != want {
		t.Errorf("Text = %q, want %q", out.Text, want)
	}
}
