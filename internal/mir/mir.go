// Package mir is the machine level intermediate representation: IC10 opcodes
// carrying virtual or physical registers, arranged as functions of basic
// blocks.
//
// Instruction selection produces it with virtual registers, register
// allocation rewrites those into physical ones, and internal/emit turns the
// result into assembly text.
//
// Construction refuses anything the emitter could not turn into a working
// line: an unknown opcode, one the target documents as unemittable, the wrong
// operand count, or an operand of a kind the position does not accept. The
// chip validates almost nothing at compile time, so an instruction that is
// wrong here faults once per tick forever with no diagnostic beyond a line
// number. Rejecting at the construction site names the selection pattern that
// produced it, which is the information needed to fix it.
//
// One hazard is not a property of an instruction but of the line it lands on,
// which construction cannot see. [Program.CheckPlacement] checks those against
// a layout that is final.
package mir

import (
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/source"
)

// Errors NewInstr reports. They are distinguished so a caller can tell a
// selection bug (ErrArity, ErrOperandKind) from a table decision
// (ErrUnemittable) without matching on message text.
var (
	ErrUnknownOpcode = errors.New("opcode is not in the instruction table")
	ErrUnemittable   = errors.New("opcode must never be emitted")
	ErrArity         = errors.New("wrong operand count")
	ErrOperandKind   = errors.New("operand kind not accepted in this position")
)

// pseudoOps are assembler directives rather than instructions. They are absent
// from ic10's unemittable table because they compile and run correctly; they
// are refused here because each consumes bytes and an execution slot against
// the 4096 byte budget while computing nothing, which docs/target.md lists
// alongside comments and blank lines under instructions not to emit.
var pseudoOps = map[ic10.Opcode]string{
	ic10.OpAlias:  "an assembler directive that costs bytes and an execution slot; emit the register or device directly",
	ic10.OpDefine: "an assembler directive that costs bytes and an execution slot; emit the literal directly",
	ic10.OpLabel:  "names a device in the game UI and computes nothing",
}

// Instr is one machine instruction and one emitted line.
//
// Args is public and mutable so register allocation can rewrite virtual
// register operands in place. Everything else about an instruction is fixed at
// construction.
type Instr struct {
	Op   ic10.Opcode
	Args []Operand
	// Pos is the source location the instruction is attributed to. It may be
	// invalid for compiler-introduced code such as the prologue, in which case
	// diagnostics fall back to naming the function.
	Pos source.Position
	// Inline is the chain of calls Pos was reached through, innermost first. It
	// is empty for code the function holds itself, and is what byte accounting
	// charges an inline site rather than a function: the same callee spliced in
	// at two places produces two chains and two entries in the report.
	Inline []source.InlineSite
}

// NewInstr builds an instruction after checking it against the target's
// instruction table. See the package documentation for why the check happens
// here rather than at emission.
func NewInstr(op ic10.Opcode, pos source.Position, args ...Operand) (*Instr, error) {
	info, ok := op.Instruction()
	if !ok {
		return nil, fmt.Errorf("%v: %w", op, ErrUnknownOpcode)
	}
	if reason, bad := ic10.Unemittable(op); bad {
		return nil, fmt.Errorf("%s: %w: %s", info.Mnemonic, ErrUnemittable, reason)
	}
	if reason, bad := pseudoOps[op]; bad {
		return nil, fmt.Errorf("%s: %w: %s", info.Mnemonic, ErrUnemittable, reason)
	}
	if len(args) != len(info.Operands) {
		return nil, fmt.Errorf("%s: %w: got %d, want %d (%s)", info.Mnemonic, ErrArity, len(args), len(info.Operands), info.Example)
	}
	for i, arg := range args {
		if arg == nil {
			return nil, fmt.Errorf("%s: operand %d is nil", info.Mnemonic, i)
		}
		if !accepts(info.Operands[i], arg) {
			return nil, fmt.Errorf("%s: %w: operand %d is %s, want %s (%s)",
				info.Mnemonic, ErrOperandKind, i, arg, kindList(info.Operands[i]), info.Example)
		}
	}
	return &Instr{Op: op, Args: slices.Clone(args), Pos: pos}, nil
}

// Mnemonic is the instruction's spelling, or a synthetic form for an opcode
// outside the table. NewInstr rejects those, so it appears only for an Instr
// built by hand.
func (i *Instr) Mnemonic() string { return i.Op.String() }

// String renders the instruction the way the emitter would lay it out, but
// with diagnostic operand spellings. It is for error messages, not output.
func (i *Instr) String() string {
	var s strings.Builder
	s.WriteString(i.Mnemonic())
	for _, arg := range i.Args {
		s.WriteString(" " + arg.String())
	}
	return s.String()
}

func accepts(position ic10.Operand, arg Operand) bool {
	return slices.ContainsFunc(position.Kinds, arg.Satisfies)
}

func kindList(position ic10.Operand) string {
	var s strings.Builder
	for i, kind := range position.Kinds {
		if i > 0 {
			s.WriteString("|")
		}
		s.WriteString(kind.String())
	}
	return s.String()
}

// Block is a straight-line run of instructions, entered only at the first and
// left only after the last.
type Block struct {
	// Label names the block for branch targets. It must be unique across the
	// whole program, because labels resolve into one flat space of line
	// numbers.
	Label string
	Pos   source.Position
	// Instrs is the instruction sequence in emission order.
	Instrs []*Instr
	// Succs lists the blocks control can reach from here. It is set by whoever
	// builds the block rather than derived from the terminator, so that a
	// liveness pass does not have to interpret opcodes.
	Succs []*Block
}

// Append adds instructions to the end of the block.
func (b *Block) Append(instrs ...*Instr) { b.Instrs = append(b.Instrs, instrs...) }

// AddSucc records that control can reach succ from b.
func (b *Block) AddSucc(succ *Block) { b.Succs = append(b.Succs, succ) }

// RegForm classifies the register operands a function currently carries. It is
// how a pass states whether register allocation has run.
type RegForm uint8

const (
	// RegFormEmpty means the function names no registers at all.
	RegFormEmpty RegForm = iota
	// RegFormVirtual means every register operand is virtual: the function is
	// well formed for register allocation and not for emission.
	RegFormVirtual
	// RegFormPhysical means every register operand is physical: the function is
	// well formed for emission.
	RegFormPhysical
	// RegFormMixed means both appear, which is the state during allocation and
	// is well formed for neither.
	RegFormMixed
)

func (f RegForm) String() string {
	switch f {
	case RegFormEmpty:
		return "empty"
	case RegFormVirtual:
		return "virtual"
	case RegFormPhysical:
		return "physical"
	case RegFormMixed:
		return "mixed"
	default:
		return "RegForm(" + fmt.Sprint(uint8(f)) + ")"
	}
}

// Func is a unit of emission and the unit byte accounting attributes to.
type Func struct {
	// Name identifies the function in diagnostics and in the size report. It
	// is not a label and is never emitted.
	Name string
	Pos  source.Position
	// Blocks is the layout order. Blocks[0] is the entry block, so control
	// reaches the function at its first instruction.
	Blocks []*Block

	nextVirt uint32
}

// NewFunc creates an empty function. Blocks are added with NewBlock, which
// keeps layout order and creation order the same.
func NewFunc(name string, pos source.Position) *Func {
	return &Func{Name: name, Pos: pos}
}

// NewVirtReg hands out a virtual register unique within f.
func (f *Func) NewVirtReg() VirtReg {
	v := VirtReg{ID: f.nextVirt}
	f.nextVirt++
	return v
}

// NewBlock appends a block and returns it. The first block created is the
// entry block.
func (f *Func) NewBlock(label string, pos source.Position) *Block {
	b := &Block{Label: label, Pos: pos}
	f.Blocks = append(f.Blocks, b)
	return b
}

// AllInstrs iterates every instruction in layout order, yielding the block it
// belongs to. Linear scan allocation walks this to number live intervals.
func (f *Func) AllInstrs() iter.Seq2[*Block, *Instr] {
	return func(yield func(*Block, *Instr) bool) {
		for _, block := range f.Blocks {
			for _, instr := range block.Instrs {
				if !yield(block, instr) {
					return
				}
			}
		}
	}
}

// RegForm reports whether f names virtual registers, physical ones, or both.
func (f *Func) RegForm() RegForm {
	var virtual, physical bool
	for _, instr := range f.AllInstrs() {
		for _, arg := range instr.Args {
			switch arg.(type) {
			case VirtReg:
				virtual = true
			case PhysReg:
				physical = true
			}
		}
	}
	switch {
	case virtual && physical:
		return RegFormMixed
	case virtual:
		return RegFormVirtual
	case physical:
		return RegFormPhysical
	default:
		return RegFormEmpty
	}
}

// Program is the whole compilation unit in emission order.
//
// Funcs[0] is the entry: execution starts at line 0, so whatever is emitted
// first runs first.
type Program struct {
	Funcs []*Func
}

// Validate checks the structural invariants emission depends on and reports
// every violation rather than the first, so one pass over a broken program
// names all of its problems.
//
// It re-checks the arity and the operand kinds [NewInstr] already refused.
// Instr.Args is public so that register allocation can rewrite operands in
// place, and a rewrite that puts the wrong kind in a position passes
// construction because construction ran before it. That window is the reason
// this is not redundant.
//
// It does not resolve branch targets. A Label naming no block is reported by
// the emitter, which is the stage that knows every label in scope.
func (p *Program) Validate() error {
	var diags source.DiagnosticList
	if p == nil || len(p.Funcs) == 0 {
		diags.Addf(source.Position{}, "program has no functions")
		return diags.Err()
	}
	labels := make(map[string]*Func)
	for _, fn := range p.Funcs {
		if fn == nil {
			diags.Addf(source.Position{}, "program contains a nil function")
			continue
		}
		if fn.Name == "" {
			diags.Addf(fn.Pos, "function has no name")
		}
		if len(fn.Blocks) == 0 {
			diags.Addf(fn.Pos, "function %s has no blocks", fn.Name)
		}
		owned := make(map[*Block]bool, len(fn.Blocks))
		for _, block := range fn.Blocks {
			if block != nil {
				owned[block] = true
			}
		}
		for _, block := range fn.Blocks {
			if block == nil {
				diags.Addf(fn.Pos, "function %s contains a nil block", fn.Name)
				continue
			}
			validateBlock(&diags, fn, block, labels, owned)
		}
	}
	return diags.Err()
}

// CheckPlacement reports every instruction whose hazard is where it lands
// rather than what it is.
//
// One line carries one. The chip starts at line 0, emission lays functions out
// in program order and gives each instruction exactly one line, so line 0 holds
// the program's first instruction — the first instruction of the first
// non-empty block of the first function that has one — and can hold nothing
// else. The entry prologue does not change that rule and cannot be relied on to
// satisfy it: a program that allocates in the data region leads with clr db and
// one that selected a real call leads with the sp initialization, but a program
// that does neither gets no prologue at all and its own first instruction is
// line 0. The condition is therefore stated over the emitted sequence and not
// over the entry function's body.
//
// This is separate from [Program.Validate] because the sequence has to be
// final. Selection validates a program register allocation may still prepend
// to, so an instruction that is line 0 there can be line 1 by emission.
func (p *Program) CheckPlacement() error {
	var diags source.DiagnosticList
	first, fn := p.firstInstr()
	if first == nil {
		return nil
	}
	if reason, hazard := ic10.FirstLineHazard(first.Op); hazard {
		diags.Addf(first.Pos, "'%s' is the program's first instruction: %s; give it any instruction to follow, either a statement ahead of it in '%s' or a global, an array, or an address-taken local, whose zeroing prologue takes line 0",
			first.Mnemonic(), reason, fn.Name)
	}
	return diags.Err()
}

// firstInstr is the instruction emission puts on line 0, and the function it
// belongs to. Both are nil for a program holding no instructions at all.
func (p *Program) firstInstr() (*Instr, *Func) {
	if p == nil {
		return nil, nil
	}
	for _, fn := range p.Funcs {
		if fn == nil {
			continue
		}
		for _, block := range fn.Blocks {
			if block == nil {
				continue
			}
			for _, instr := range block.Instrs {
				if instr != nil {
					return instr, fn
				}
			}
		}
	}
	return nil, nil
}

func validateBlock(diags *source.DiagnosticList, fn *Func, block *Block, labels map[string]*Func, owned map[*Block]bool) {
	if block.Label == "" {
		diags.Addf(block.Pos, "function %s has a block with no label", fn.Name)
	} else {
		if prior, dup := labels[block.Label]; dup {
			diags.Addf(block.Pos, "label %q is already defined in function %s; labels share one flat space", block.Label, prior.Name)
		}
		labels[block.Label] = fn
	}
	for i, instr := range block.Instrs {
		if instr == nil {
			diags.Addf(block.Pos, "block %s contains a nil instruction at index %d", block.Label, i)
			continue
		}
		info, known := instr.Op.Instruction()
		if !known {
			diags.Addf(instr.Pos, "block %s instruction %d has opcode %v, which is not in the instruction table", block.Label, i, instr.Op)
			continue
		}
		if len(instr.Args) != len(info.Operands) {
			diags.Addf(instr.Pos, "block %s instruction %d is %s with %d operands, and %s takes %d (%s)",
				block.Label, i, info.Mnemonic, len(instr.Args), info.Mnemonic, len(info.Operands), info.Example)
		}
		for j, arg := range instr.Args {
			if arg == nil {
				diags.Addf(instr.Pos, "block %s instruction %d has a nil operand at position %d", block.Label, i, j)
				continue
			}
			if j >= len(info.Operands) {
				continue
			}
			if !accepts(info.Operands[j], arg) {
				diags.Addf(instr.Pos, "block %s instruction %d has %s at operand %d of %s, which takes %s there (%s)",
					block.Label, i, arg, j, info.Mnemonic, kindList(info.Operands[j]), info.Example)
			}
		}
	}
	for _, succ := range block.Succs {
		if succ == nil {
			diags.Addf(block.Pos, "block %s has a nil successor", block.Label)
			continue
		}
		if !owned[succ] {
			diags.Addf(block.Pos, "block %s has successor %s, which is not a block of function %s", block.Label, succ.Label, fn.Name)
		}
	}
}
