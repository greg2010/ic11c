// Package valueflow propagates a property of values over a whole LLVM
// module to a fixed point, given where the property starts and what
// each instruction does with it. Memory is modelled by object, not
// location: a pointer [MemoryObject] cannot place is taken to reach every object.
package valueflow

import (
	"github.com/greg2010/ic11c/internal/llvmir"
	"tinygo.org/x/go-llvm"
)

// Rules say what one instruction does with the property. Stops is
// asked about every instruction whose result the walk decides itself
// (a load, a call into a definition); Carries is asked about the rest,
// and holds the property exactly when a marked operand does.
type Rules struct {
	Stops   func(in llvm.Value) bool
	Carries func(in llvm.Value) bool
}

// Seed is what holds the property before propagation begins: the
// values an analysis knows are sources, and the memory objects already
// written into. Either may be nil. Run propagates into these maps
// directly, so a Seed used for two runs must be built twice.
type Seed struct {
	Values  map[llvm.Value]bool
	Objects map[llvm.Value]bool
}

// Run walks m to a fixed point and answers every value the property
// reaches. A caller whose seed is empty and whose rules leave nothing
// unstopped or uncarried can skip the call, since nothing can come to hold
// the property — but deciding that is the caller's, not Run's.
func Run(m llvm.Module, rules Rules, seed Seed) map[llvm.Value]bool {
	r := &run{
		rules:   rules,
		values:  seed.Values,
		objects: seed.Objects,
		returns: make(map[llvm.Value]bool),
	}
	if r.values == nil {
		r.values = make(map[llvm.Value]bool)
	}
	if r.objects == nil {
		r.objects = make(map[llvm.Value]bool)
	}
	for r.changed = true; r.changed; {
		r.changed = false
		for fn, in := range llvmir.ModuleInstrs(m) {
			r.step(fn, in)
		}
	}
	return r.values
}

// run is one propagation in progress. changed is what closes the fixed point.
type run struct {
	rules   Rules
	values  map[llvm.Value]bool
	objects map[llvm.Value]bool
	returns map[llvm.Value]bool
	// unknown records a store through a pointer the object walk could not place,
	// after which every load may answer a value holding the property.
	unknown bool
	changed bool
}

func (r *run) mark(v llvm.Value) {
	if !r.values[v] {
		r.values[v] = true
		r.changed = true
	}
}

// pointerReaders are the opcodes that read a pointer operand and
// write nothing through it, named as an allowlist so an unrecognized
// opcode defaults to conservative: treated as writing the property
// into every object at once.
var pointerReaders = map[llvm.Opcode]bool{
	llvm.GetElementPtr: true,
	llvm.PtrToInt:      true,
	llvm.ICmp:          true,
	llvm.PHI:           true,
	llvm.Select:        true,
}

func (r *run) step(fn, in llvm.Value) {
	// Default is the rule: an instruction with no arm here is decided by its own
	// result and the operands it reads, and is taken to have written through any
	// pointer [pointerReaders] does not account for.
	//exhaustive:ignore
	switch opcode := in.InstructionOpcode(); opcode {
	case llvm.Store:
		r.store(in)
	case llvm.Load:
		r.load(in)
	case llvm.Call, llvm.Invoke:
		r.call(in)
	case llvm.Ret:
		r.ret(fn, in)
	default:
		if !pointerReaders[opcode] {
			r.escape(in, in.OperandsCount())
		}
		r.plain(in)
	}
}

// plain decides an instruction's own result from the rules and the operands it
// reads. What the instruction did to memory is settled before it is called and
// nowhere here.
func (r *run) plain(in llvm.Value) {
	if r.rules.Stops(in) {
		return
	}
	if !r.rules.Carries(in) || anyMarked(in, r.values) {
		r.mark(in)
	}
}

func (r *run) store(in llvm.Value) {
	if !r.values[in.Operand(0)] {
		return
	}
	base := MemoryObject(in.Operand(1))
	if base.IsNil() {
		if !r.unknown {
			r.unknown = true
			r.changed = true
		}
		return
	}
	if !r.objects[base] {
		r.objects[base] = true
		r.changed = true
	}
}

// load carries the property back out of memory. It reaches no operand of the
// load, so the rules decide only whether the result can hold it at all.
func (r *run) load(in llvm.Value) {
	if r.rules.Stops(in) {
		return
	}
	base := MemoryObject(in.Operand(0))
	if !r.unknown && !base.IsNil() && !r.objects[base] {
		return
	}
	r.mark(in)
}

// call crosses a call boundary in both directions, for the call and
// invoke spellings alike. A declaration has no body to walk, so the
// rules alone decide its result; a definition is walked into, and its
// result is marked unless [Rules.Stops] says otherwise.
func (r *run) call(in llvm.Value) {
	callee := in.CalledValue()
	if callee.IsNil() || callee.IsAFunction().IsNil() || callee.IsDeclaration() {
		// The callee is the last operand, and a pointer to code is not a
		// pointer the call was handed.
		r.escape(in, in.OperandsCount()-1)
		r.plain(in)
		return
	}
	for i := range min(callee.ParamsCount(), in.OperandsCount()-1) {
		if r.values[in.Operand(i)] {
			r.mark(callee.Param(i))
		}
	}
	if r.returns[callee] && !r.rules.Stops(in) {
		r.mark(in)
	}
}

// escape records that an instruction may have written the property into
// whatever object it was handed a pointer to, checking its first operands.
// With nothing recorded about what was written, the pointee is taken to be
// every object at once, which is what unknown means.
func (r *run) escape(in llvm.Value, operands int) {
	if r.unknown {
		return
	}
	for i := range operands {
		if in.Operand(i).Type().TypeKind() == llvm.PointerTypeKind {
			r.unknown = true
			r.changed = true
			return
		}
	}
}

func (r *run) ret(fn, in llvm.Value) {
	if in.OperandsCount() != 1 || !r.values[in.Operand(0)] {
		return
	}
	if !r.returns[fn] {
		r.returns[fn] = true
		r.changed = true
	}
}

// anyMarked reports whether any operand of in is in marked.
func anyMarked(in llvm.Value, marked map[llvm.Value]bool) bool {
	for i := range in.OperandsCount() {
		if operand := in.Operand(i); !operand.IsNil() && marked[operand] {
			return true
		}
	}
	return false
}

// maxObjectWalk bounds how much address arithmetic one lookup walks
// back through, not for termination (each step moves closer to a
// definition) but to cap the work one lookup can do. Exhausting it
// answers "every object", the conservative direction.
const maxObjectWalk = 16

// MemoryObject walks a pointer back to the alloca or global it
// addresses, answering nil for one it cannot place. A nil answer is
// not "no object": it is read as every object at once. [internal/pointers]
// walks further (through a phi, a select) for a different, incompatible question.
func MemoryObject(ptr llvm.Value) llvm.Value {
	for range maxObjectWalk {
		switch {
		case !ptr.IsAAllocaInst().IsNil(), !ptr.IsAGlobalVariable().IsNil():
			return ptr
		case !ptr.IsAGetElementPtrInst().IsNil():
			ptr = ptr.Operand(0)
		case !ptr.IsAConstantExpr().IsNil() && ptr.Opcode() == llvm.GetElementPtr:
			ptr = ptr.Operand(0)
		default:
			return llvm.Value{}
		}
	}
	return llvm.Value{}
}
