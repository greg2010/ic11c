package peephole

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/chiptest"
	"github.com/greg2010/ic11c/internal/emit"
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/mir"
)

// wantBranchComplementPairs is how many unordered pairs the branch table holds,
// pinned for the reason wantComplementPairs is: a pair removed from the map
// would otherwise reduce what this file executes without failing anything.
const wantBranchComplementPairs = 3

// branchTarget is the line every branch under test names, and
// branchProgramLines how many lines the program holds. The two arms each store
// a mark of their own, so which one control reached is a value the finished run
// left rather than a program counter read between two instructions.
const (
	branchTarget       = 3
	branchProgramLines = 4
)

// arm is what each arm of the two-way programs under test stores, so that the
// line control reached is readable from a register afterwards.
const (
	armTaken = 1
	armFalse = 2
)

// branchOperandRoles describes a conditional branch's operand positions:
// every position but the last is an input, and the last is the target.
// It reports false for a shape no fixture here drives, which
// [branchTablePairs] treats as a failure rather than a reason to skip.
func branchOperandRoles(op ic10.Opcode) ([]operandRole, bool) {
	instruction, ok := op.Instruction()
	if !ok {
		return nil, false
	}
	roles := make([]operandRole, 0, len(instruction.Operands)-1)
	for _, operand := range instruction.Operands[:len(instruction.Operands)-1] {
		switch {
		case operand.Accepts(ic10.OperandDevice):
			roles = append(roles, roleDevice)
		case operand.Accepts(ic10.OperandNumber):
			roles = append(roles, roleValue)
		default:
			return nil, false
		}
	}
	return roles, true
}

// branchLine renders the one-branch program's first line, with any device
// position filled by deviceText.
func branchLine(op ic10.Opcode, roles []operandRole, deviceText string) string {
	tokens := make([]string, 0, len(roles)+2)
	tokens = append(tokens, op.String())
	value := 0
	for _, role := range roles {
		switch role {
		case roleDevice:
			tokens = append(tokens, deviceText)
		case roleValue, roleDestination:
			tokens = append(tokens, valueRegisters[value].String())
			value++
		}
	}
	return strings.Join(tokens, " ") + " " + strconv.Itoa(branchTarget)
}

// branchProgram wraps the branch under test in the two arms that record which
// way it went: the line below it marks a fall-through and jumps clear of the
// target, and the target marks a branch taken. Both arms run off the end of the
// program, so a finished run has reached exactly one of them.
func branchProgram(line string) string {
	lines := make([]string, branchProgramLines)
	lines[0] = line
	lines[1] = mark(armFalse)
	lines[2] = "j " + strconv.Itoa(branchProgramLines)
	lines[branchTarget] = mark(armTaken)
	return strings.Join(lines, "\n")
}

// mark writes an arm's store into the result register.
func mark(value int) string {
	return "move " + resultRegister.String() + " " + strconv.Itoa(value)
}

// takes runs the branch under test over inputs and reports whether it branched.
func takes(ctx context.Context, t *testing.T, h *chip.Harness, line string, inputs []float64) bool {
	t.Helper()
	got := runProgram(ctx, t, h, branchProgram(line), inputs)
	return armReached(t, got.Registers[resultRegister], line)
}

// takesOnDevicePin is [takes] for a branch that asks a pin rather than a value.
func takesOnDevicePin(ctx context.Context, t *testing.T, h *chip.FixtureHarness, line string, attach bool) bool {
	t.Helper()
	got := runOnDevicePin(ctx, t, h, branchProgram(line), attach)
	return armReached(t, got.Registers[resultRegister], line)
}

// armReached reads which arm ran off the mark the run left. A value
// that is neither mark fails rather than reading as a fall-through:
// the result register starts as NaN, so a run that reached no arm is
// distinguishable from one that reached either.
func armReached(t *testing.T, got float64, line string) bool {
	t.Helper()
	switch got {
	case armTaken:
		return true
	case armFalse:
		return false
	}
	t.Fatalf("running %q left %v where an arm's mark should be, so control reached neither arm", line, got)
	return false
}

// branchPair is one unordered entry of the branch complement table, with the
// operand shape both members share.
type branchPair struct {
	op, complement ic10.Opcode
	roles          []operandRole
}

// branchTablePairs derives every pair from branchComplements, each once and in
// opcode order.
func branchTablePairs(t *testing.T) []branchPair {
	t.Helper()
	var pairs []branchPair
	for op, complement := range branchComplements {
		if op >= complement {
			continue
		}
		roles, ok := branchOperandRoles(op)
		if !ok {
			t.Errorf("no fixture drives the operands of %v and %v, so the pair is not executed", op, complement)
			continue
		}
		pairs = append(pairs, branchPair{op: op, complement: complement, roles: roles})
	}
	slices.SortFunc(pairs, func(a, b branchPair) int { return int(a.op) - int(b.op) })
	if 2*len(pairs) != len(branchComplements) {
		t.Errorf("the branch table holds %d entries but yields %d pairs; an opcode maps to itself or is unpaired",
			len(branchComplements), len(pairs))
	}
	return pairs
}

// TestBranchComplementsAreSubstitutable asserts that rewriting one member's
// opcode into the other leaves an instruction the machine accepts: the two take
// the same number of operands, each position accepting the same kinds, and the
// target is last.
func TestBranchComplementsAreSubstitutable(t *testing.T) {
	for op, complement := range branchComplements {
		if back, ok := branchComplements[complement]; !ok || back != op {
			t.Errorf("%v maps to %v, which does not map back", op, complement)
		}
		first, ok := op.Instruction()
		if !ok {
			t.Errorf("%v is not in the instruction table", op)
			continue
		}
		second, ok := complement.Instruction()
		if !ok {
			t.Errorf("%v is not in the instruction table", complement)
			continue
		}
		if len(first.Operands) != len(second.Operands) {
			t.Errorf("%s takes %d operands and %s takes %d, so one cannot replace the other",
				first.Mnemonic, len(first.Operands), second.Mnemonic, len(second.Operands))
			continue
		}
		for i := range first.Operands {
			if !slices.Equal(first.Operands[i].Kinds, second.Operands[i].Kinds) {
				t.Errorf("%s and %s accept different kinds at operand %d", first.Mnemonic, second.Mnemonic, i)
			}
		}
		last := first.Operands[len(first.Operands)-1]
		if !last.Accepts(ic10.OperandNumber) && !last.Accepts(ic10.OperandInteger) {
			t.Errorf("%s does not take its target last, so the rewrite retargets the wrong operand", first.Mnemonic)
		}
	}
}

// TestBranchComplementsTakeOppositeBranchesOnTheChip executes both
// members of every branch pair and asserts that exactly one branches
// for each input — the property the fold rests on, rewriting a branch
// into its pair member and sending it where the jump behind it went.
func TestBranchComplementsTakeOppositeBranchesOnTheChip(t *testing.T) {
	pairs := branchTablePairs(t)
	if len(pairs) != wantBranchComplementPairs {
		t.Fatalf("the branch table yields %d pairs, want %d", len(pairs), wantBranchComplementPairs)
	}
	for _, pair := range pairs {
		t.Run(fmt.Sprintf("%v and %v", pair.op, pair.complement), func(t *testing.T) {
			if slices.Contains(pair.roles, roleDevice) {
				runDeviceBranchPair(t, pair)
				return
			}
			runValueBranchPair(t, pair)
		})
	}
}

// runValueBranchPair executes one pair over the whole cross product of the grid.
func runValueBranchPair(t *testing.T, pair branchPair) {
	t.Helper()
	ctx, harness := chiptest.Harness(t)
	firstLine := branchLine(pair.op, pair.roles, "")
	secondLine := branchLine(pair.complement, pair.roles, "")

	arity := countValues(pair.roles)
	values := make([]float64, arity)
	executed, mismatches := 0, 0
	forEachInput(arity, func(inputs []gridValue) {
		for i, in := range inputs {
			values[i] = in.value
		}
		executed++
		a := takes(ctx, t, harness, firstLine, values)
		b := takes(ctx, t, harness, secondLine, values)
		if a != b {
			return
		}
		mismatches++
		if mismatches <= maxReportedMismatches {
			t.Errorf("for (%s) %q and %q both %s, want exactly one to branch",
				inputNames(inputs), firstLine, secondLine, branchedText(a))
		}
	})
	if mismatches > maxReportedMismatches {
		t.Errorf("%v and %v agree on %d of %d inputs", pair.op, pair.complement, mismatches, executed)
	}
	if want := gridSize(arity); executed != want {
		t.Errorf("executed %d inputs over %d operands, want %d", executed, arity, want)
	}
}

func branchedText(branched bool) string {
	if branched {
		return "branched"
	}
	return "fell through"
}

// runDeviceBranchPair executes the device pair, which asks a housing rather
// than a value.
func runDeviceBranchPair(t *testing.T, pair branchPair) {
	t.Helper()
	ctx, harness := chiptest.Fixtures(t)
	firstLine := branchLine(pair.op, pair.roles, fixturePinText)
	secondLine := branchLine(pair.complement, pair.roles, fixturePinText)
	answers := make([]bool, len(devicePresence))
	for i, world := range devicePresence {
		a := takesOnDevicePin(ctx, t, harness, firstLine, world.attach)
		if a == takesOnDevicePin(ctx, t, harness, secondLine, world.attach) {
			t.Errorf("for %s %q and %q both %s, want exactly one to branch",
				world.name, firstLine, secondLine, branchedText(a))
		}
		answers[i] = a
	}
	if len(devicePresence) != 2 || answers[0] == answers[1] {
		t.Errorf("%q answered %v across the presence fixtures, which do not distinguish a populated pin from an empty one",
			firstLine, answers)
	}
}

// nonComplementBranchPairs are the branches that look like
// complements and are not, absent from branchComplements on purpose
// (see [complements] for why). onlyNaN marks a pair whose members
// fall through together only for NaN inputs; the approximate branches do it for more.
var nonComplementBranchPairs = []struct {
	first, second ic10.Opcode
	onlyNaN       bool
}{
	{first: isa.OpBlt, second: isa.OpBge, onlyNaN: true},
	{first: isa.OpBgt, second: isa.OpBle, onlyNaN: true},
	{first: isa.OpBltz, second: isa.OpBgez, onlyNaN: true},
	{first: isa.OpBgtz, second: isa.OpBlez, onlyNaN: true},
	{first: isa.OpBap, second: isa.OpBna},
	{first: isa.OpBapz, second: isa.OpBnaz},
}

// TestNonComplementBranchesAreNotComplements executes the exclusion
// the branch table rests on rather than asserting it in prose: each
// pair branches on opposite inputs for most of the grid and falls
// through together for the rest, which inverting one into the other would miss.
func TestNonComplementBranchesAreNotComplements(t *testing.T) {
	if len(nonComplementBranchPairs) == 0 {
		t.Fatal("no branch pair is checked, so the exclusion is not established")
	}
	for _, pair := range nonComplementBranchPairs {
		t.Run(fmt.Sprintf("%v and %v", pair.first, pair.second), func(t *testing.T) {
			if complement, listed := branchComplements[pair.first]; listed {
				t.Fatalf("%v is in the branch complement table, paired with %v", pair.first, complement)
			}
			roles, ok := branchOperandRoles(pair.first)
			if !ok {
				t.Fatalf("no fixture drives the operands of %v", pair.first)
			}
			ctx, harness := chiptest.Harness(t)
			firstLine := branchLine(pair.first, roles, "")
			secondLine := branchLine(pair.second, roles, "")

			arity := countValues(roles)
			values := make([]float64, arity)
			sawAgreement, sawOpposite := false, false
			forEachInput(arity, func(inputs []gridValue) {
				anyNaN := false
				for i, in := range inputs {
					values[i] = in.value
					anyNaN = anyNaN || math.IsNaN(in.value)
				}
				a := takes(ctx, t, harness, firstLine, values)
				b := takes(ctx, t, harness, secondLine, values)
				switch {
				case a && b:
					t.Errorf("for (%s) %q and %q both branched, want at most one to",
						inputNames(inputs), firstLine, secondLine)
				case a != b:
					sawOpposite = true
					if pair.onlyNaN && anyNaN {
						t.Errorf("for (%s) %q %s and %q %s, want neither to branch",
							inputNames(inputs), firstLine, branchedText(a), secondLine, branchedText(b))
					}
				default:
					sawAgreement = true
					if pair.onlyNaN && !anyNaN {
						t.Errorf("for (%s) %q and %q both fell through, want exactly one to branch",
							inputNames(inputs), firstLine, secondLine)
					}
				}
			})
			if !sawAgreement {
				t.Errorf("no input made %q and %q both fall through, so the exclusion the pair is kept out of the table for was not executed",
					firstLine, secondLine)
			}
			if !sawOpposite {
				t.Errorf("no input separated %q from %q, so nothing established that they look like complements", firstLine, secondLine)
			}
		})
	}
}

// TestRunInvertsABranchOverItsFallthrough drives the rewrite over the block
// shapes selection produces.
func TestRunInvertsABranchOverItsFallthrough(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T, fn *mir.Func)
		want  []string
	}{
		{
			name: "a branch onto the following block",
			build: func(t *testing.T, fn *mir.Func) {
				t.Helper()
				entry := fn.NewBlock("main.entry", pos)
				taken := fn.NewBlock("main.taken", pos)
				fallen := fn.NewBlock("main.fallen", pos)
				entry.Append(
					instr(t, isa.OpBnez, phys(0), mir.Label{Name: "main.taken"}),
					instr(t, isa.OpJ, mir.Label{Name: "main.fallen"}),
				)
				taken.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 1}))
				fallen.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 2}))
			},
			want: []string{"beqz r0 main.fallen", "move r1 1", "move r1 2"},
		},
		{
			name: "a branch onto a block reached across an emptied one",
			build: func(t *testing.T, fn *mir.Func) {
				t.Helper()
				entry := fn.NewBlock("main.entry", pos)
				empty := fn.NewBlock("main.empty", pos)
				taken := fn.NewBlock("main.taken", pos)
				fallen := fn.NewBlock("main.fallen", pos)
				entry.Append(
					instr(t, isa.OpBeq, phys(0), phys(1), mir.Label{Name: "main.taken"}),
					instr(t, isa.OpJ, mir.Label{Name: "main.fallen"}),
				)
				empty.Append(instr(t, isa.OpMove, phys(2), phys(2)))
				taken.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 1}))
				fallen.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 2}))
			},
			want: []string{"bne r0 r1 main.fallen", "move r1 1", "move r1 2"},
		},
		{
			name: "only the last branch of a plan is inverted",
			build: func(t *testing.T, fn *mir.Func) {
				t.Helper()
				entry := fn.NewBlock("main.entry", pos)
				taken := fn.NewBlock("main.taken", pos)
				fallen := fn.NewBlock("main.fallen", pos)
				entry.Append(
					instr(t, isa.OpBlt, phys(0), phys(1), mir.Label{Name: "main.fallen"}),
					instr(t, isa.OpBnez, phys(2), mir.Label{Name: "main.taken"}),
					instr(t, isa.OpJ, mir.Label{Name: "main.fallen"}),
				)
				taken.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 1}))
				fallen.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 2}))
			},
			want: []string{"blt r0 r1 main.fallen", "beqz r2 main.fallen", "move r1 1", "move r1 2"},
		},
		{
			name: "an ordered branch is left alone",
			build: func(t *testing.T, fn *mir.Func) {
				t.Helper()
				entry := fn.NewBlock("main.entry", pos)
				taken := fn.NewBlock("main.taken", pos)
				fallen := fn.NewBlock("main.fallen", pos)
				entry.Append(
					instr(t, isa.OpBlt, phys(0), phys(1), mir.Label{Name: "main.taken"}),
					instr(t, isa.OpJ, mir.Label{Name: "main.fallen"}),
				)
				taken.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 1}))
				fallen.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 2}))
			},
			want: []string{"blt r0 r1 main.taken", "j main.fallen", "move r1 1", "move r1 2"},
		},
		{
			name: "a NaN test is left alone",
			build: func(t *testing.T, fn *mir.Func) {
				t.Helper()
				entry := fn.NewBlock("main.entry", pos)
				taken := fn.NewBlock("main.taken", pos)
				fallen := fn.NewBlock("main.fallen", pos)
				entry.Append(
					instr(t, isa.OpBnan, phys(0), mir.Label{Name: "main.taken"}),
					instr(t, isa.OpJ, mir.Label{Name: "main.fallen"}),
				)
				taken.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 1}))
				fallen.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 2}))
			},
			want: []string{"bnan r0 main.taken", "j main.fallen", "move r1 1", "move r1 2"},
		},
		{
			// The block between holds an instruction, so neither target is
			// where control goes without a jump: the branch has nothing to
			// invert over and the jump is not one this drops either.
			name: "a branch onto a block that is not the fallthrough",
			build: func(t *testing.T, fn *mir.Func) {
				t.Helper()
				entry := fn.NewBlock("main.entry", pos)
				between := fn.NewBlock("main.between", pos)
				fallen := fn.NewBlock("main.fallen", pos)
				taken := fn.NewBlock("main.taken", pos)
				entry.Append(
					instr(t, isa.OpBnez, phys(0), mir.Label{Name: "main.taken"}),
					instr(t, isa.OpJ, mir.Label{Name: "main.fallen"}),
				)
				between.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 3}))
				fallen.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 2}))
				taken.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 1}))
			},
			want: []string{"bnez r0 main.taken", "j main.fallen", "move r1 3", "move r1 2", "move r1 1"},
		},
		{
			name: "a return through the link register is not a jump to a label",
			build: func(t *testing.T, fn *mir.Func) {
				t.Helper()
				entry := fn.NewBlock("main.entry", pos)
				taken := fn.NewBlock("main.taken", pos)
				entry.Append(
					instr(t, isa.OpBnez, phys(0), mir.Label{Name: "main.taken"}),
					instr(t, isa.OpJ, mir.PhysReg{Reg: ic10.RegRA}),
				)
				taken.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 1}))
			},
			want: []string{"bnez r0 main.taken", "j ra", "move r1 1"},
		},
		{
			// A call carries its target in the same position a jump does, so
			// reading the last instruction by its operands alone sends the
			// branch where the call went and drops the call — turning a call
			// into a branch, which runs and never returns to the line after it.
			name: "a call after the branch is not a jump",
			build: func(t *testing.T, fn *mir.Func) {
				t.Helper()
				entry := fn.NewBlock("main.entry", pos)
				fallen := fn.NewBlock("main.fallen", pos)
				helper := fn.NewBlock("main.helper", pos)
				entry.Append(
					instr(t, isa.OpBeqz, phys(0), mir.Label{Name: "main.fallen"}),
					instr(t, isa.OpJal, mir.Label{Name: "main.helper"}),
				)
				fallen.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 2}))
				helper.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 3}))
			},
			want: []string{"beqz r0 main.fallen", "jal main.helper", "move r1 2", "move r1 3"},
		},
		{
			name: "a jump with no branch before it",
			build: func(t *testing.T, fn *mir.Func) {
				t.Helper()
				entry := fn.NewBlock("main.entry", pos)
				taken := fn.NewBlock("main.taken", pos)
				fallen := fn.NewBlock("main.fallen", pos)
				entry.Append(
					instr(t, isa.OpMove, phys(1), mir.Imm{Value: 3}),
					instr(t, isa.OpJ, mir.Label{Name: "main.fallen"}),
				)
				taken.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 1}))
				fallen.Append(instr(t, isa.OpMove, phys(1), mir.Imm{Value: 2}))
			},
			want: []string{"move r1 3", "j main.fallen", "move r1 1", "move r1 2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := mir.NewFunc("main", pos)
			tt.build(t, fn)
			prog := &mir.Program{Funcs: []*mir.Func{fn}}

			Run(prog)

			got := rendered(prog)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("Run left\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
			if err := prog.Validate(); err != nil {
				t.Errorf("the rewritten program does not validate: %v", err)
			}
		})
	}
}

// buildTwoWay builds the block shape the fold fires on: a branch to the block
// that follows, a jump past it, and one arm per successor.
func buildTwoWay(t *testing.T, op ic10.Opcode, args ...mir.Operand) *mir.Program {
	t.Helper()
	fn := mir.NewFunc("main", pos)
	entry := fn.NewBlock("main.entry", pos)
	taken := fn.NewBlock("main.taken", pos)
	fallen := fn.NewBlock("main.fallen", pos)
	fn.NewBlock("main.end", pos)
	entry.Append(
		instr(t, op, append(slices.Clone(args), mir.Label{Name: "main.taken"})...),
		instr(t, isa.OpJ, mir.Label{Name: "main.fallen"}),
	)
	taken.Append(
		instr(t, isa.OpMove, phys(int(resultRegister)), mir.Imm{Value: armTaken}),
		instr(t, isa.OpJ, mir.Label{Name: "main.end"}),
	)
	fallen.Append(instr(t, isa.OpMove, phys(int(resultRegister)), mir.Imm{Value: armFalse}))
	return &mir.Program{Funcs: []*mir.Func{fn}}
}

// assemble emits a program, failing the test rather than returning an error.
func assemble(t *testing.T, prog *mir.Program) string {
	t.Helper()
	out, err := emit.Emit(prog, emit.Options{})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	return out.Text
}

// TestInvertedBranchLandsWhereThePairDidOnTheChip executes the whole
// rewrite end to end, emitting the same program with and without the
// fold and running both on the chip over the value grid — what says
// the fold is sound, rather than a comparison of mnemonics.
func TestInvertedBranchLandsWhereThePairDidOnTheChip(t *testing.T) {
	tests := []struct {
		name  string
		op    ic10.Opcode
		args  []mir.Operand
		arity int
	}{
		{name: "beq", op: isa.OpBeq, args: []mir.Operand{phys(1), phys(2)}, arity: 2},
		{name: "bne", op: isa.OpBne, args: []mir.Operand{phys(1), phys(2)}, arity: 2},
		{name: "beqz", op: isa.OpBeqz, args: []mir.Operand{phys(1)}, arity: 1},
		{name: "bnez", op: isa.OpBnez, args: []mir.Operand{phys(1)}, arity: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, harness := chiptest.Harness(t)
			control := assemble(t, buildTwoWay(t, tt.op, tt.args...))
			rewritten := buildTwoWay(t, tt.op, tt.args...)
			Run(rewritten)
			folded := assemble(t, rewritten)

			controlLines := strings.Count(control, "\n") + 1
			foldedLines := strings.Count(folded, "\n") + 1
			if foldedLines != controlLines-1 {
				t.Fatalf("the fold left %d lines against the control's %d, want one fewer\ncontrol:\n%s\nfolded:\n%s",
					foldedLines, controlLines, control, folded)
			}

			values := make([]float64, tt.arity)
			mismatches := 0
			forEachInput(tt.arity, func(inputs []gridValue) {
				for i, in := range inputs {
					values[i] = in.value
				}
				want := armReached(t, runProgram(ctx, t, harness, control, values).Registers[resultRegister], control)
				got := armReached(t, runProgram(ctx, t, harness, folded, values).Registers[resultRegister], folded)
				if got == want {
					return
				}
				mismatches++
				if mismatches <= maxReportedMismatches {
					t.Errorf("for (%s) the folded form %s and the control %s",
						inputNames(inputs), branchedText(got), branchedText(want))
				}
			})
			if mismatches > maxReportedMismatches {
				t.Errorf("the two forms disagree on %d inputs", mismatches)
			}
		})
	}
}
