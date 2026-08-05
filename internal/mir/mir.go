// Package mir is the machine level intermediate representation: IC10 opcodes
// carrying virtual or physical registers, arranged as functions of basic
// blocks. Instruction selection produces it with virtual registers, register
// allocation rewrites those into physical ones, and internal/emit turns the
// result into assembly text.
//
// Construction refuses anything the emitter could not turn into a working
// line, since the chip validates almost nothing at compile time and a bad
// instruction faults once per tick forever with no diagnostic beyond a line
// number.
package mir

import (
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
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

// InvariantError reports machine IR that breaks an invariant a later stage
// rests on. It renders every diagnostic it was built from, not the first and a
// count, but does not unwrap to them, since those name block labels and operand
// kinds the source never wrote — %w would print a defect as if it were a line.
type InvariantError struct{ diags source.DiagnosticList }

func (e *InvariantError) Error() string { return e.diags.String() }

// pseudoOps are assembler directives rather than instructions: absent from
// ic10's unemittable table because they compile and run correctly, but
// refused here because each consumes bytes and an execution slot against the
// 4096 byte budget while computing nothing.
var pseudoOps = map[ic10.Opcode]string{
	isa.OpAlias:  "an assembler directive that costs bytes and an execution slot; emit the register or device directly",
	isa.OpDefine: "an assembler directive that costs bytes and an execution slot; emit the literal directly",
	isa.OpLabel:  "names a device in the game UI and computes nothing",
}

// Instr is one machine instruction and one emitted line. Args is public and
// mutable so register allocation can rewrite virtual register operands in
// place; everything else is fixed at construction.
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
	// Succs lists every block control can reach from here, the fallthrough into
	// the next laid-out block included. It is set by whoever builds the block
	// rather than derived from the terminator, so liveness does not have to
	// interpret opcodes. An edge left out is a miscompile: liveness reads
	// nothing else, so the omitted edge's interval ends early and the allocator
	// gives the register away.
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

var regFormNames = [...]string{
	RegFormEmpty:    "empty",
	RegFormVirtual:  "virtual",
	RegFormPhysical: "physical",
	RegFormMixed:    "mixed",
}

func (f RegForm) String() string { return source.EnumName(regFormNames[:], int(f), "RegForm") }

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
		if instr == nil {
			continue
		}
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
// every violation rather than the first.
//
// It re-checks everything [NewInstr] already refused, since later passes
// rewrite Instr.Op and Instr.Args in place and can land a wrong opcode or
// operand kind that construction never sees again. It does not resolve branch
// targets — a Label naming no block is the emitter's to report — but does hold
// [Block.Succs] against a function's branches and layout while the function
// still carries virtual registers, since liveness is Succs' only reader and
// runs once.
func (p *Program) Validate() error {
	var diags source.DiagnosticList
	if p == nil || len(p.Funcs) == 0 {
		diags.Addf(source.Position{}, "program has no functions")
		return invariant(diags)
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
		byLabel := make(map[string]*Block, len(fn.Blocks))
		for _, block := range fn.Blocks {
			if block == nil {
				continue
			}
			owned[block] = true
			if _, taken := byLabel[block.Label]; !taken {
				byLabel[block.Label] = block
			}
		}
		// Liveness runs once, over the virtual form, and reads Succs alone. A
		// function whose registers are all physical is past that and past any
		// use for the graph.
		form := fn.RegForm()
		checkEdges := form == RegFormVirtual || form == RegFormMixed
		for i, block := range fn.Blocks {
			if block == nil {
				diags.Addf(fn.Pos, "function %s contains a nil block", fn.Name)
				continue
			}
			validateBlock(&diags, fn, block, labels)
			validateSuccs(&diags, fn, i, byLabel, owned, checkEdges)
		}
	}
	return invariant(diags)
}

// invariant answers nil for a list holding nothing that rejects, so that a
// caller's err != nil is not satisfied by an interface holding a typed nil.
func invariant(diags source.DiagnosticList) error {
	if !diags.HasErrors() {
		return nil
	}
	return &InvariantError{diags: diags}
}

// CheckPlacement reports every instruction whose hazard is where it lands
// rather than what it is: line 0 specifically, since the chip starts there and
// nothing else can hold it.
//
// This is separate from [Program.Validate] because the sequence has to be
// final — register allocation may still prepend a prologue after selection
// validates, so an instruction that is line 0 there can be line 1 by emission.
func (p *Program) CheckPlacement() error {
	var diags source.DiagnosticList
	first, fn := p.firstInstr()
	if first == nil {
		return nil
	}
	if reason, hazard := ic10.FirstLineHazard(first.Op); hazard {
		diags.Addf(first.Pos, "'%s' is the program's first instruction: %s; give it an instruction to follow by putting __ic_yield(); ahead of it in '%s', or a store to a device — a declaration or an assignment will not do it, because nothing observes one before the '%s' and the optimizer drops or sinks what nothing observes",
			first.Mnemonic(), reason, fn.Name, first.Mnemonic())
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

func validateBlock(diags *source.DiagnosticList, fn *Func, block *Block, labels map[string]*Func) {
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
		if reason, bad := ic10.Unemittable(instr.Op); bad {
			diags.Addf(instr.Pos, "block %s instruction %d is %s, which must never be emitted: %s", block.Label, i, info.Mnemonic, reason)
			continue
		}
		if reason, bad := pseudoOps[instr.Op]; bad {
			diags.Addf(instr.Pos, "block %s instruction %d is %s, which is %s", block.Label, i, info.Mnemonic, reason)
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
}
