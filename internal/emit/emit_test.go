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
	for _, readable := range []bool{false, true} {
		t.Run("readable="+strconv.FormatBool(readable), func(t *testing.T) {
			out := emit(t, buildSampleProgram(t), Options{Readable: readable})
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

func TestEmitNilProgram(t *testing.T) {
	if _, err := Emit(nil, Options{}); err == nil {
		t.Fatal("Emit(nil) returned no error")
	}
}

// TestByteAccounting checks the report against a count done by hand. Every line
// is charged its own length plus the newline that ends it, so the emitted text
// is one byte shorter than the reported total.
func TestByteAccounting(t *testing.T) {
	main := mir.NewFunc("main", position(1))
	main.NewBlock("main.entry", position(1)).Append(
		instr(t, ic10.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1}),
		instr(t, ic10.OpJ, mir.Label{Name: "helper.entry"}),
	)
	helper := mir.NewFunc("helper", position(2))
	helper.NewBlock("helper.entry", position(2)).Append(instr(t, ic10.OpYield))

	out := emit(t, &mir.Program{Funcs: []*mir.Func{main, helper}}, Options{})

	// "move r0 1" is 9, "j 2" is 3, "yield" is 5, each plus one newline.
	const (
		wantMainBytes   = (9 + 1) + (3 + 1)
		wantHelperBytes = 5 + 1
		wantBytes       = wantMainBytes + wantHelperBytes
	)
	if out.Text != "move r0 1\nj 2\nyield" {
		t.Fatalf("Text = %q", out.Text)
	}
	if out.Report.Bytes != wantBytes {
		t.Errorf("Bytes = %d, want %d", out.Report.Bytes, wantBytes)
	}
	if got := len(out.Text); got != wantBytes-1 {
		t.Errorf("len(Text) = %d, want %d", got, wantBytes-1)
	}
	if out.Report.Lines != 3 {
		t.Errorf("Lines = %d, want 3", out.Report.Lines)
	}
	if out.Report.Bytes > MaxBytes {
		t.Errorf("Bytes = %d, over the %d byte budget for a 20 byte program", out.Report.Bytes, MaxBytes)
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

// TestFunctionBytesSumToProgramBytes is what makes attribution usable: the
// parts have to add up to the whole or the report cannot say where the size
// went.
func TestFunctionBytesSumToProgramBytes(t *testing.T) {
	for _, readable := range []bool{false, true} {
		t.Run("readable="+strconv.FormatBool(readable), func(t *testing.T) {
			out := emit(t, buildSampleProgram(t), Options{Readable: readable})
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

func TestLineCountViolation(t *testing.T) {
	fn := mir.NewFunc("long", position(1))
	block := fn.NewBlock("long.entry", position(1))
	for range MaxLines + 1 {
		block.Append(instr(t, ic10.OpYield))
	}

	out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{})
	if out.Report.Lines != MaxLines+1 {
		t.Fatalf("Lines = %d, want %d", out.Report.Lines, MaxLines+1)
	}
	if out.Report.Bytes > MaxBytes {
		t.Errorf("Bytes = %d, want it under the %d byte budget", out.Report.Bytes, MaxBytes)
	}
	if len(out.Report.Violations) != 1 || out.Report.Violations[0].Kind != ViolationLines {
		t.Fatalf("Violations = %v, want exactly the line limit", out.Report.Violations)
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
// total for it would say the code costs 26 bytes when deleting either call
// recovers only 13.
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

	const (
		move  = len("move r0 1") + 1
		yield = len("yield") + 1
		add   = len("add r0 r0 1") + 1
		sub   = len("sub r0 r0 1") + 1
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
		{Kind: ViolationBytes, Used: len("move r0 1") + 1 + len("yield") + 1, Max: MaxBytes},
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
				t.Errorf("Violations = %v, want none for a two byte program", out.Report.Violations)
			}
		})
	}
}

// TestBindingLimitIsComputed covers the reason the answer is not asserted.
// Lines bind for every real program, since an emitted instruction averages 10
// to 13 characters against the 32 the byte cap would need, but a program of
// long lines reaches 4096 bytes while still well under 128 lines.
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
