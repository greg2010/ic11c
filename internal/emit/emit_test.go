package emit

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
)

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
		at(t, 1, isa.OpClr, mir.NewDeviceBase()),
		at(t, 1, isa.OpMove, mir.PhysReg{Reg: ic10.RegSP}, mir.Imm{Value: 400}),
		at(t, 2, isa.OpL, r(0), pin0, mir.LogicType{Value: temperature}),
		at(t, 3, isa.OpLs, r(1), pin1, mir.Imm{Value: 2}, mir.LogicSlotType{Value: slotType}),
		at(t, 4, isa.OpLb, r(2), mir.Imm{Value: prefab}, mir.LogicType{Value: temperature}, mir.BatchMode{Value: 0}),
		at(t, 5, isa.OpLbn, r(3), mir.Imm{Value: prefab}, mir.Imm{Value: 12345}, mir.LogicType{Value: setting}, mir.BatchMode{Value: 1}),
		at(t, 6, isa.OpLr, r(4), pin0, mir.ReagentMode{Value: 3}, mir.Imm{Value: 5}),
		at(t, 6, isa.OpMove, r(5), mir.Imm{Value: 3.141592653589793}),
		at(t, 6, isa.OpMove, r(6), mir.Imm{Value: -0.5}),
		at(t, 6, isa.OpMove, r(7), r(0)),
		at(t, 7, isa.OpBeq, r(0), r(1), mir.Label{Name: "main.done"}),
		at(t, 7, isa.OpJ, mir.Label{Name: "main.loop"}),
	)
	entry.AddSucc(loop)
	entry.AddSucc(done)

	loop.Append(
		at(t, 9, isa.OpAdd, r(0), r(0), mir.Imm{Value: 1}),
		at(t, 10, isa.OpPoke, mir.Imm{Value: 0}, r(0)),
		at(t, 11, isa.OpGet, r(8), mir.NewDeviceBase(), mir.Imm{Value: 0}),
		at(t, 12, isa.OpBne, r(0), mir.Imm{Value: 10}, mir.Label{Name: "main.loop"}),
	)
	loop.AddSucc(loop)
	loop.AddSucc(done)

	done.Append(
		at(t, 15, isa.OpJal, mir.Label{Name: "helper.entry"}),
		at(t, 16, isa.OpYield),
		at(t, 16, isa.OpJ, mir.Label{Name: "main.entry"}),
	)
	done.AddSucc(entry)

	helper := mir.NewFunc("helper", position(20))
	helperEntry := helper.NewBlock("helper.entry", position(20))
	helperEntry.Append(
		at(t, 21, isa.OpSub, r(0), r(0), mir.Imm{Value: 1}),
		at(t, 22, isa.OpJ, mir.PhysReg{Reg: ic10.RegRA}),
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
		instr(t, isa.OpMove, mir.PhysReg{Reg: 0}, mir.Imm{Value: 1}),
	)
	empty := mir.NewFunc("empty", position(2))
	empty.NewBlock("empty.entry", position(2))
	tail := mir.NewFunc("tail", position(3))
	tail.NewBlock("tail.entry", position(3)).Append(instr(t, isa.OpYield))
	return &mir.Program{Funcs: []*mir.Func{main, empty, tail}}
}

// TestEmitProducesNoLineTheChipRunsForNothing is the property the whole package
// is built around: such a line costs one of the 128 lines and one of the 128
// instructions a tick runs while computing nothing. ProgrammableChip cuts a line
// at its first '#', after which a blank and a lone "word:" are both _NOOP_Operation.
func TestEmitProducesNoLineTheChipRunsForNothing(t *testing.T) {
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
				// What survives the chip's cut is checked against the default
				// form rather than only for its shape: a cut line that is
				// non-empty and is not a label is still the wrong instruction
				// if it does not match what the program compiles to.
				code := instructionsOf(t, out.Text)
				if readable {
					plain := emit(t, prog.build(t), Options{})
					if code != plain.Text {
						t.Errorf("what the chip reads is %q, want the default form %q", code, plain.Text)
					}
				} else if code != out.Text {
					t.Errorf("output carries a comment outside readable output: %q", out.Text)
				}
				for i, line := range strings.Split(code, "\n") {
					fields := strings.Fields(line)
					if len(fields) == 0 {
						t.Errorf("line %d runs as a no-op: %q", i, line)
					}
					// Looser than the chip, which wants that word at least two
					// characters: ":" alone is a line it cannot parse at all.
					// Neither may be emitted, so the two are not separated.
					if len(fields) == 1 && strings.HasSuffix(fields[0], ":") {
						t.Errorf("line %d is a label definition, which runs as a no-op: %q", i, line)
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
		instr(t, isa.OpJ, mir.Label{Name: "main.tail"}),
		instr(t, isa.OpJ, mir.Label{Name: "main.middle"}),
	)
	middle.Append(
		instr(t, isa.OpJ, mir.Label{Name: "main.entry"}),
	)
	tail.Append(
		instr(t, isa.OpJ, mir.Label{Name: "main.middle"}),
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
	entry.Append(instr(t, isa.OpJ, mir.Label{Name: "main.empty"}))
	tail.Append(instr(t, isa.OpJ, mir.Label{Name: "main.end"}))

	out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{})
	want := "j 1\nj 2"
	if out.Text != want {
		t.Errorf("Text = %q, want %q", out.Text, want)
	}
}

// TestBranchOnePastTheLastLineEndsTheProgram pins the one target outside the
// emitted lines, and pins that it is not a defect. ProgrammableChip.Execute runs
// a line only while _NextAddr is below _LinesOfCode.Count, so a branch to exactly
// the line count stops the chip silently, which resolveLabels emits deliberately.
func TestBranchOnePastTheLastLineEndsTheProgram(t *testing.T) {
	for _, readable := range []bool{false, true} {
		t.Run("readable="+strconv.FormatBool(readable), func(t *testing.T) {
			fn := mir.NewFunc("main", position(1))
			entry := fn.NewBlock("main.entry", position(1))
			fn.NewBlock("main.return", position(2))
			entry.Append(
				instr(t, isa.OpBeqz, mir.PhysReg{Reg: 0}, mir.Label{Name: "main.return"}),
				instr(t, isa.OpYield),
			)

			out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{Readable: readable})
			target := branchTarget(t, instructionsOf(t, out.Text))
			if target != out.Report.Lines {
				t.Errorf("the branch reaches line %d, want %d, one past the last line of the program",
					target, out.Report.Lines)
			}
			// Halting is the target's meaning, not an overrun of a limit the
			// editor holds, so it is not one of the three violations.
			if len(out.Report.Violations) != 0 {
				t.Errorf("Violations = %v, want none for a program that ends by branching past its last line",
					out.Report.Violations)
			}
		})
	}
}

// branchTarget reads the line number the first line of assembly branches to.
func branchTarget(t *testing.T, text string) int {
	t.Helper()
	fields := strings.Fields(strings.Split(text, "\n")[0])
	target, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil {
		t.Fatalf("the first line %q does not end in a line number: %v", fields, err)
	}
	return target
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
				fn.NewBlock("main.entry", position(1)).Append(instr(t, isa.OpJ, mir.Label{Name: "main.gone"}))
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
				fn.NewBlock("main.entry", position(1)).Append(instr(t, isa.OpMove, mir.VirtReg{ID: 2}, mir.Imm{Value: 1}))
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
				fn.NewBlock("", position(1)).Append(instr(t, isa.OpYield))
				return &mir.Program{Funcs: []*mir.Func{fn}}
			},
			mention: "no label",
		},
		{
			name:    "empty program",
			build:   func(*testing.T) *mir.Program { return &mir.Program{} },
			mention: "no functions",
		},
		{
			// mir.Program.Validate answers for a nil program too, and says it
			// holds no functions. Nil is not a program that came out empty, and
			// a caller reading that would look for the stage that dropped them.
			name:    "nil program",
			build:   func(*testing.T) *mir.Program { return nil },
			mention: "program is nil",
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

// TestLayoutRefusesAnAnnotationWithNoEmittedName covers both readers of the
// readable form's name table: the block that starts a line, and the block a
// branch on that line reaches. Emit builds the name table and the line table from
// one block list, so a missing entry is reached through layout, not through Emit.
func TestLayoutRefusesAnAnnotationWithNoEmittedName(t *testing.T) {
	tests := []struct {
		name    string
		names   map[string]string
		mention string
	}{
		{
			name:    "the block that starts the line",
			mention: "main.entry",
		},
		{
			name:    "the block the branch reaches",
			names:   map[string]string{"main.entry": "main_entry"},
			mention: "main.tail",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := mir.NewFunc("main", position(1))
			entry := fn.NewBlock("main.entry", position(1))
			tail := fn.NewBlock("main.tail", position(2))
			entry.Append(instr(t, isa.OpJ, mir.Label{Name: "main.tail"}))
			tail.Append(instr(t, isa.OpYield))
			prog := &mir.Program{Funcs: []*mir.Func{fn}}

			lineOf, count := resolveLabels(prog)
			lines, funcs, err := layout(prog, renderer{lineOf: lineOf, count: count, names: tt.names}, true)
			if !errors.Is(err, ErrUnresolvedLabel) {
				t.Fatalf("layout = %v, %v, %v, want %v", lines, funcs, err, ErrUnresolvedLabel)
			}
			if !strings.Contains(err.Error(), tt.mention) {
				t.Errorf("layout error = %q, want it to mention %q", err, tt.mention)
			}
		})
	}
}

// instructionsOf is the readable text with its trailing comments removed, which
// is what the chip actually reads: it truncates a line at the first '#'.
func instructionsOf(t *testing.T, text string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if code, _, found := strings.Cut(line, "#"); found {
			lines[i] = strings.TrimRight(code, " ")
		}
	}
	return strings.Join(lines, "\n")
}

// TestReadableAnnotatesWithoutSpendingLines is the property the readable form
// exists to hold: it names every block and every branch target while emitting the
// same instructions on the same lines as the default form. A trailing comment
// costs bytes and line width alone, where a label line would cost both budgets.
func TestReadableAnnotatesWithoutSpendingLines(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T) *mir.Program
		// plain is the default form, and readable the same program annotated.
		plain    string
		readable string
		// The readable form's spend, which moves entirely into bytes and width.
		wantLines int
		wantBytes int
		wantWidth int
	}{
		{
			name: "the only line carries its block name",
			build: func(t *testing.T) *mir.Program {
				t.Helper()
				fn := mir.NewFunc("main", position(1))
				fn.NewBlock("main.entry", position(1)).Append(instr(t, isa.OpYield))
				return &mir.Program{Funcs: []*mir.Func{fn}}
			},
			plain:     "yield",
			readable:  "yield # main_entry:",
			wantLines: 1,
			wantBytes: 19,
			wantWidth: 19,
		},
		{
			name: "a branch names its target beside the line number",
			build: func(t *testing.T) *mir.Program {
				t.Helper()
				fn := mir.NewFunc("main", position(1))
				entry := fn.NewBlock("main.entry", position(1))
				tail := fn.NewBlock("main.tail", position(2))
				entry.Append(instr(t, isa.OpJ, mir.Label{Name: "main.tail"}))
				tail.Append(instr(t, isa.OpYield))
				return &mir.Program{Funcs: []*mir.Func{fn}}
			},
			plain:     "j 1\nyield",
			readable:  "j 1 # main_entry: -> main_tail\nyield # main_tail:",
			wantLines: 2,
			wantBytes: 50,
			wantWidth: 30,
		},
		{
			name: "a block with no instructions shares the line its label resolves to",
			build: func(t *testing.T) *mir.Program {
				t.Helper()
				fn := mir.NewFunc("main", position(1))
				entry := fn.NewBlock("main.entry", position(1))
				fn.NewBlock("main.empty", position(2))
				end := fn.NewBlock("main.end", position(3))
				entry.Append(instr(t, isa.OpJ, mir.Label{Name: "main.end"}))
				end.Append(instr(t, isa.OpYield))
				return &mir.Program{Funcs: []*mir.Func{fn}}
			},
			plain:     "j 1\nyield",
			readable:  "j 1 # main_entry: -> main_end\nyield # main_empty: main_end:",
			wantLines: 2,
			wantBytes: 60,
			wantWidth: 29,
		},
		{
			name: "a trailing block with no instructions has no line to sit beside",
			build: func(t *testing.T) *mir.Program {
				t.Helper()
				fn := mir.NewFunc("main", position(1))
				fn.NewBlock("main.entry", position(1)).Append(instr(t, isa.OpYield))
				fn.NewBlock("main.tail", position(2))
				return &mir.Program{Funcs: []*mir.Func{fn}}
			},
			plain:     "yield",
			readable:  "yield # main_entry:",
			wantLines: 1,
			wantBytes: 19,
			wantWidth: 19,
		},
		{
			// The block starts one past the last line, so no line can carry its
			// name. The branch says where it went instead, which is what keeps
			// the name from appearing on the branch and nowhere else.
			name: "a branch to a trailing block with no instructions is marked as the end",
			build: func(t *testing.T) *mir.Program {
				t.Helper()
				fn := mir.NewFunc("main", position(1))
				entry := fn.NewBlock("main.entry", position(1))
				fn.NewBlock("main.return", position(2))
				entry.Append(
					instr(t, isa.OpBeqz, mir.PhysReg{Reg: 0}, mir.Label{Name: "main.return"}),
					instr(t, isa.OpYield),
				)
				return &mir.Program{Funcs: []*mir.Func{fn}}
			},
			plain:     "beqz r0 2\nyield",
			readable:  "beqz r0 2 # main_entry: -> main_return (end)\nyield",
			wantLines: 2,
			wantBytes: 51,
			wantWidth: 44,
		},
		{
			name: "a call names the function it reaches",
			build: func(t *testing.T) *mir.Program {
				t.Helper()
				main := mir.NewFunc("main", position(1))
				main.NewBlock("main.entry", position(1)).Append(
					instr(t, isa.OpJal, mir.Label{Name: "helper.entry"}),
					instr(t, isa.OpYield),
				)
				helper := mir.NewFunc("helper", position(2))
				helper.NewBlock("helper.entry", position(2)).Append(
					instr(t, isa.OpJ, mir.PhysReg{Reg: ic10.RegRA}),
				)
				return &mir.Program{Funcs: []*mir.Func{main, helper}}
			},
			plain:     "jal 2\nyield\nj ra",
			readable:  "jal 2 # main_entry: -> helper_entry\nyield\nj ra # helper_entry:",
			wantLines: 3,
			wantBytes: 64,
			wantWidth: 35,
		},
		{
			name: "a block that branches to itself names itself twice",
			build: func(t *testing.T) *mir.Program {
				t.Helper()
				fn := mir.NewFunc("main", position(1))
				entry := fn.NewBlock("main.entry", position(1))
				loop := fn.NewBlock("main.loop", position(2))
				entry.Append(instr(t, isa.OpYield))
				loop.Append(instr(t, isa.OpJ, mir.Label{Name: "main.loop"}))
				return &mir.Program{Funcs: []*mir.Func{fn}}
			},
			plain:     "yield\nj 1",
			readable:  "yield # main_entry:\nj 1 # main_loop: -> main_loop",
			wantLines: 2,
			wantBytes: 50,
			wantWidth: 29,
		},
		{
			name:      "a function that emits nothing hands its block name to the next",
			build:     buildEmptyFunctionProgram,
			plain:     "move r0 1\nyield",
			readable:  "move r0 1 # main_entry:\nyield # empty_entry: tail_entry:",
			wantLines: 2,
			wantBytes: 57,
			wantWidth: 32,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plain := emit(t, tt.build(t), Options{})
			if plain.Text != tt.plain {
				t.Fatalf("default Text = %q, want %q", plain.Text, tt.plain)
			}
			out := emit(t, tt.build(t), Options{Readable: true})
			if out.Text != tt.readable {
				t.Errorf("readable Text = %q, want %q", out.Text, tt.readable)
			}
			if got := instructionsOf(t, out.Text); got != plain.Text {
				t.Errorf("readable instructions = %q, want the default form %q", got, plain.Text)
			}
			if out.Report.Lines != plain.Report.Lines {
				t.Errorf("readable Lines = %d, default is %d; an annotation must not cost a line",
					out.Report.Lines, plain.Report.Lines)
			}
			if out.Report.Lines != tt.wantLines {
				t.Errorf("Lines = %d, want %d", out.Report.Lines, tt.wantLines)
			}
			if out.Report.Bytes != tt.wantBytes {
				t.Errorf("Bytes = %d, want %d", out.Report.Bytes, tt.wantBytes)
			}
			if out.Report.LongestLine != tt.wantWidth {
				t.Errorf("LongestLine = %d, want %d", out.Report.LongestLine, tt.wantWidth)
			}
		})
	}
}

// TestReadableBranchesToLineNumbers pins the operand the chip reads. A name is
// only ever inside the comment, so a label spelled like one of the chip's own
// names cannot be resolved to the built-in and cannot fault once a tick.
func TestReadableBranchesToLineNumbers(t *testing.T) {
	fn := mir.NewFunc("main", position(1))
	entry := fn.NewBlock("Temperature", position(1))
	entry.Append(instr(t, isa.OpJ, mir.Label{Name: "Temperature"}))

	out := emit(t, &mir.Program{Funcs: []*mir.Func{fn}}, Options{Readable: true})
	want := "j 0 # Temperature: -> Temperature"
	if out.Text != want {
		t.Errorf("Text = %q, want %q", out.Text, want)
	}
}
