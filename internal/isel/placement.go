package isel

import (
	"tinygo.org/x/go-llvm"

	"github.com/greg2010/ic11c/internal/valueflow"
)

// bitwiseIntrinsics names the declarations standing for partial instructions,
// derived from the selection table so a second list can't drift from it: an
// intrinsic missing from [intrinsicForms] is refused by selection outright.
var bitwiseIntrinsics = partialIntrinsics()

// partialIntrinsics returns the intrinsics whose instruction converts an
// operand. Omitting one here only costs extra instructions later, since
// [placedResult] treats only a named intrinsic as placed.
func partialIntrinsics() map[string]bool {
	names := make(map[string]bool, len(intrinsicForms))
	for name, form := range intrinsicForms {
		instruction, known := form.op.Instruction()
		if known && convertsAnOperand(instruction) {
			names[name] = true
		}
	}
	return names
}

// planPlacement returns the values selection cannot place inside the
// machine's conversion range. A bitwise or shift instruction the optimizer
// formed over one of these is refused, since that instruction halts the chip
// outside its range where the operator it replaced would not have.
func planPlacement(m llvm.Module) map[llvm.Value]bool {
	rules := valueflow.Rules{Stops: placedResult, Carries: carriesPlacement}
	return valueflow.Run(m, rules, valueflow.Seed{Objects: openObjects(m)})
}

// openObjects returns the objects in the data region whose contents this
// stage cannot reason about. IR generation zero-initializes every object
// it places, writing the program's own initializers as stores, so a
// global with any other initializer is a shape it did not produce.
func openObjects(m llvm.Module) map[llvm.Value]bool {
	objects := make(map[llvm.Value]bool)
	for g := m.FirstGlobal(); !g.IsNil(); g = llvm.NextGlobal(g) {
		if init := g.Initializer(); init.IsNil() || !init.IsNull() {
			objects[g] = true
		}
	}
	return objects
}

// placedResult reports whether v's result lies inside the machine's
// conversion range regardless of its operands: truth values and comparisons
// are 0 or 1, bitwise/shift results are converted back from a signed 64-bit
// integer, and ptrtoint is a slot index (the machine has 512).
func placedResult(v llvm.Value) bool {
	// Default is the rule: only the two value types carry a number a machine
	// instruction converts, so nothing else has a range to be outside of.
	//exhaustive:ignore
	switch v.Type().TypeKind() {
	case llvm.IntegerTypeKind:
		if v.Type().IntTypeWidth() == predicateWidth {
			return true
		}
	case llvm.DoubleTypeKind:
	default:
		return true
	}
	if v.IsAInstruction().IsNil() {
		return false
	}
	// Default is the rule: an instruction not named here answers with whatever
	// its operands hold, so its range is theirs rather than its own.
	//exhaustive:ignore
	switch v.InstructionOpcode() {
	case llvm.And, llvm.Or, llvm.Xor, llvm.Shl, llvm.AShr, llvm.LShr,
		llvm.ICmp, llvm.FCmp, llvm.PtrToInt, llvm.ZExt, llvm.SExt:
		return true
	case llvm.Call:
		callee := v.CalledValue()
		return !callee.IsNil() && bitwiseIntrinsics[callee.Name()]
	default:
		return false
	}
}

// carriesPlacement reports whether an instruction's range is inherited
// from its operands rather than opened on its own. Selection treats an
// fptosi as the register it already is, so an i64 here holds whatever
// double it was written from, and an infinity passes straight through.
func carriesPlacement(in llvm.Value) bool {
	// Default is the rule: an instruction not named here either opens a range
	// of its own or is a shape this stage did not produce, and both are refused.
	//exhaustive:ignore
	switch in.InstructionOpcode() {
	case llvm.Add, llvm.Sub, llvm.Mul, llvm.PHI, llvm.Select,
		llvm.SIToFP, llvm.UIToFP, llvm.FPToSI, opcodeFreeze, opcodeFNeg:
		return true
	default:
		return false
	}
}
