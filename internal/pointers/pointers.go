// Package pointers enforces the restriction the backend rests on: every
// load and store must reach one statically known object. It runs after
// optimization, since the optimizer can introduce a pointer phi or
// select a source-level check would not see.
package pointers

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/greg2010/ic11c/internal/llvmir"
	"github.com/greg2010/ic11c/internal/source"
	"tinygo.org/x/go-llvm"
)

// unresolved is the phrase for a pointer that reaches something which is
// neither an object nor a step toward one.
const unresolved = "does not name a local or a global"

// Options configures verification.
type Options struct {
	// File names the source. LLVM debug locations carry a line and a column
	// but no file, so it is restored here.
	File string
	// Lines restores the byte offset a debug location does not carry. It may be
	// nil, which leaves every reconstructed position at offset zero.
	Lines *source.LineMap
}

// Check reports every load and store in m whose pointer does not trace
// to one alloca or one global. Failures come back as a
// [source.DiagnosticList] positioned at the line the instruction's
// debug location names. m is not modified.
func Check(ctx context.Context, m llvm.Module, opts Options) error {
	if m.IsNil() {
		return errors.New("pointers: nil module")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("pointers: %w", err)
	}

	v := &verifier{pos: llvmir.Positions{File: opts.File, Lines: opts.Lines}}
	for fn := m.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		if fn.IsDeclaration() {
			continue
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("pointers: %w", err)
		}
		v.function(fn)
	}
	v.diags.Sort()
	return v.diags.Err()
}

type verifier struct {
	pos llvmir.Positions
	// enclosing is the first line the function being checked still carries a
	// location for, which is what a diagnostic about an instruction carrying
	// none falls back to.
	enclosing source.Position
	diags     source.DiagnosticList
}

func (v *verifier) function(fn llvm.Value) {
	v.enclosing = v.firstLocated(fn)
	for in := range llvmir.FuncInstrs(fn) {
		// Default is the rule: only an access through a pointer is checked
		// here, and no other instruction is one.
		//exhaustive:ignore
		switch in.InstructionOpcode() {
		case llvm.Load:
			v.access(in, "reads", in.Operand(0))
		case llvm.Store:
			v.access(in, "writes", in.Operand(1))
		default:
		}
	}
}

// access checks one memory access. The verb distinguishes a read from a write
// because a diagnostic naming neither leaves the reader looking at the wrong
// half of the statement.
func (v *verifier) access(in llvm.Value, verb string, ptr llvm.Value) {
	_, why := resolve(ptr, make(map[llvm.Value]bool))
	if why == "" {
		return
	}
	v.diags.Addf(v.position(in), "this statement %s through a pointer that %s, and an address is a slot in one named object", verb, why)
}

// position recovers the source location an instruction was generated
// from. The instructions this package reports on have no usable
// location — the optimizer drops metadata from an instruction it forms,
// or leaves line 0 on one it merged — so both fall back to the function.
func (v *verifier) position(in llvm.Value) source.Position {
	if pos, located := v.pos.Instr(in); located {
		return pos
	}
	if v.enclosing.IsValid() {
		return v.enclosing
	}
	return source.Position{File: v.pos.File}
}

// firstLocated is the narrowest place a diagnostic about an instruction with no
// location of its own can name: the first line the function still carries one
// for. A function the optimizer stripped entirely has none, which leaves the
// position invalid and is what the file-only fallback covers.
func (v *verifier) firstLocated(fn llvm.Value) source.Position {
	for in := range llvmir.FuncInstrs(fn) {
		if pos, located := v.pos.Instr(in); located {
			return pos
		}
	}
	return source.Position{}
}

// resolve walks a pointer back to the object it designates, returning
// that object, or an empty value and a phrase naming why there is not
// exactly one: a phi or select is a failure only on a merge of two
// distinct objects. visited breaks a loop-carried phi's cycle.
func resolve(ptr llvm.Value, visited map[llvm.Value]bool) (llvm.Value, string) {
	if !ptr.IsAAllocaInst().IsNil() || !ptr.IsAGlobalVariable().IsNil() {
		return ptr, ""
	}
	if visited[ptr] {
		return llvm.Value{}, ""
	}
	visited[ptr] = true

	switch {
	case !ptr.IsAArgument().IsNil():
		return fromCallSites(ptr, visited)
	case !ptr.IsAInstruction().IsNil():
		return through(ptr, ptr.InstructionOpcode(), visited)
	case !ptr.IsAConstantExpr().IsNil():
		return through(ptr, ptr.Opcode(), visited)
	default:
		return llvm.Value{}, unresolved
	}
}

// through follows one step of a pointer computation toward its object.
func through(ptr llvm.Value, op llvm.Opcode, visited map[llvm.Value]bool) (llvm.Value, string) {
	// Default is the rule: a step this walk does not understand leaves the
	// pointer unresolved, which reports rather than passes.
	//exhaustive:ignore
	switch op {
	case llvm.GetElementPtr:
		// Address arithmetic keeps the object it started from; whether the
		// offset folds to a slot index is decided where slots are assigned.
		// Pointer casts need no case of their own: pointers are opaque, so
		// there is no bitcast between them and no addrspacecast.
		return resolve(ptr.Operand(0), visited)
	case llvm.PHI:
		return merge(incoming(ptr), "merges", visited)
	case llvm.Select:
		return merge([]llvm.Value{ptr.Operand(1), ptr.Operand(2)}, "chooses between", visited)
	case llvm.Load:
		return fromStores(ptr, visited)
	default:
		return llvm.Value{}, unresolved
	}
}

// fromStores resolves a pointer read out of memory to the object every
// value written into that memory designates: the shape every pointer
// has before SROA runs. Which store reaches this load is not asked,
// since the restriction is that one object is named.
func fromStores(load llvm.Value, visited map[llvm.Value]bool) (llvm.Value, string) {
	object, why := resolve(load.Operand(0), visited)
	if why != "" || object.IsNil() {
		return llvm.Value{}, why
	}
	stored := storedInto(object, make(map[llvm.Value]bool))
	if initial, has := initializer(object); has {
		// The initializer is what the memory holds before any store runs, so
		// it joins the list at the head.
		stored = append([]llvm.Value{initial}, stored...)
	}
	if len(stored) == 0 {
		return llvm.Value{}, unresolved
	}
	return merge(stored, "is assigned", visited)
}

// initializer is the pointer a global holds before the program's
// first store, or false where there is none: only a global whose own
// value is a pointer is answered, and a null initializer — which LLVM
// gives every global — is not an assignment either.
func initializer(object llvm.Value) (llvm.Value, bool) {
	if object.IsAGlobalVariable().IsNil() || object.GlobalValueType().TypeKind() != llvm.PointerTypeKind {
		return llvm.Value{}, false
	}
	initial := object.Initializer()
	if initial.IsNil() || !initial.IsAConstantPointerNull().IsNil() {
		return llvm.Value{}, false
	}
	return initial, true
}

// storedInto lists every value written into an object, following each way
// a pointer to it reaches a store: address arithmetic and the phi/select
// merges an optimized program carries the pointer through.
func storedInto(object llvm.Value, seen map[llvm.Value]bool) []llvm.Value {
	// A value reached twice contributes its writers only the first time; a
	// self-feeding phi is the only cycle this can hit.
	if seen[object] {
		return nil
	}
	seen[object] = true

	var values []llvm.Value
	for _, user := range users(object) {
		switch {
		case !user.IsAStoreInst().IsNil():
			// The store is the one user with two live positions: check the
			// value operand, not the pointer operand, or a pointer written
			// into another pointer reads as the object holding itself.
			if user.Operand(1) == object {
				values = append(values, user.Operand(0))
			}
		case !user.IsACallInst().IsNil():
			values = append(values, storedByCallee(user, object, seen)...)
		case !user.IsAPHINode().IsNil():
			// A rotated loop spells a pointer walk as a phi; the store it
			// feeds names the phi, not the object, so this must be followed.
			values = append(values, storedInto(user, seen)...)
		case !user.IsASelectInst().IsNil():
			// The condition is an i1, so only the two value arms can hold
			// the object.
			if user.Operand(1) == object || user.Operand(2) == object {
				values = append(values, storedInto(user, seen)...)
			}
		case !user.IsAGetElementPtrInst().IsNil():
			// LLVM's verifier holds an instruction's indices to integers, so
			// only the base operand can be the object.
			if user.Operand(0) == object {
				values = append(values, storedInto(user, seen)...)
			}
		case !user.IsAConstantExpr().IsNil() && user.Opcode() == llvm.GetElementPtr:
			// Unlike the instruction case above, LLVM does not check a
			// constant expression's indices, so this still has a case to decide.
			if user.Operand(0) == object {
				values = append(values, storedInto(user, seen)...)
			}
		}
	}
	return values
}

// storedByCallee lists what a call writes into the object it was
// handed, by following each parameter the object arrives as into the
// callee's body. A callee with no body, or reached other than by name,
// resolves to the call itself, naming no object rather than no writer.
func storedByCallee(call, object llvm.Value, seen map[llvm.Value]bool) []llvm.Value {
	fn := call.CalledValue()
	if fn.IsNil() || fn.IsAFunction().IsNil() || fn.IsDeclaration() {
		return []llvm.Value{call}
	}
	var values []llvm.Value
	for i := range fn.ParamsCount() {
		// The callee is the last operand, so arguments are every operand
		// before it. A call may pass fewer arguments than the callee defines
		// parameters for — LLVM does not check the two against each other —
		// so the index bound keeps this inside the operand list.
		if i < call.OperandsCount()-1 && call.Operand(i) == object {
			values = append(values, storedInto(fn.Param(i), seen)...)
		}
	}
	return values
}

// users lists the values that use v, oldest first. LLVM threads a use list in
// the reverse of that, and a diagnostic naming two objects reads as the program
// does only in the order the program reached them.
func users(v llvm.Value) []llvm.Value {
	var found []llvm.Value
	for use := v.FirstUse(); !use.IsNil(); use = use.NextUse() {
		found = append(found, use.User())
	}
	slices.Reverse(found)
	return found
}

// fromCallSites resolves a pointer parameter to the object every call
// passes. A parameter of a function compiled out of line may recurse,
// so the recursive call site contributes nothing — its argument leads
// back through this same parameter — leaving the calls from outside to name it.
func fromCallSites(param llvm.Value, visited map[llvm.Value]bool) (llvm.Value, string) {
	fn := param.ParamParent()
	if fn.IsNil() {
		return llvm.Value{}, unresolved
	}
	index := -1
	for i := range fn.ParamsCount() {
		if fn.Param(i) == param {
			index = i
			break
		}
	}
	if index < 0 {
		return llvm.Value{}, unresolved
	}
	var args []llvm.Value
	for _, call := range users(fn) {
		// The callee is the last operand, so the arguments are every operand
		// before it and a parameter past them names none.
		if call.IsACallInst().IsNil() || call.CalledValue() != fn || index >= call.OperandsCount()-1 {
			continue
		}
		args = append(args, call.Operand(index))
	}
	if len(args) == 0 {
		return llvm.Value{}, unresolved
	}
	return merge(args, "is passed", visited)
}

// merge resolves the arms of a pointer phi or select to the one object they all
// designate.
func merge(arms []llvm.Value, verb string, visited map[llvm.Value]bool) (llvm.Value, string) {
	var base llvm.Value
	for _, arm := range arms {
		resolved, why := resolve(arm, visited)
		if why != "" {
			return llvm.Value{}, why
		}
		if resolved.IsNil() {
			continue
		}
		if base.IsNil() {
			base = resolved
			continue
		}
		if resolved != base {
			return llvm.Value{}, fmt.Sprintf("%s %s and %s", verb, objectName(base), objectName(resolved))
		}
	}
	// A base of nothing means every arm led back through the merge itself, so
	// no object reaches it and the access is unreachable. There is nothing here
	// to report.
	return base, ""
}

func incoming(phi llvm.Value) []llvm.Value {
	arms := make([]llvm.Value, 0, phi.IncomingCount())
	for i := range phi.IncomingCount() {
		arms = append(arms, phi.IncomingValue(i))
	}
	return arms
}

// objectName names an object the way the source does. An SSA value number would
// name the module rather than the program the user wrote.
func objectName(base llvm.Value) string {
	if name := base.Name(); name != "" {
		return name
	}
	return "an unnamed variable"
}
