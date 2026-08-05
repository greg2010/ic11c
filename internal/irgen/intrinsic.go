package irgen

import (
	"github.com/greg2010/ic11c/internal/sema"
	"tinygo.org/x/go-llvm"
)

// intrinsicFunc returns the declaration a machine intrinsic lowers to, creating it on first use with
// no attributes, so the optimizer treats it as opaque. Which operand is a device pin, a logic type,
// or a runtime value is not encoded here — instruction selection reads the arguments back
// positionally — only the width is, since LLVM's type system must agree with what the call carries.
func (g *generator) intrinsicFunc(in *sema.Intrinsic, args int) (llvm.Type, llvm.Value) {
	if fn, declared := g.intrinsics[in.Name]; declared {
		return fn.GlobalValueType(), fn
	}
	result := g.f64
	if in.Result.Kind() == sema.Void {
		result = g.result.Context.VoidType()
	}
	params := make([]llvm.Type, args)
	for i := range params {
		params[i] = g.intrinsicParamType(in, i)
	}
	fnType := llvm.FunctionType(result, params, false)
	fn := llvm.AddFunction(g.result.Module, in.Name, fnType)
	if in.Pure {
		attrs := g.pure
		if mayFault(in.Name) {
			attrs = g.faulting
		}
		for _, attr := range attrs {
			fn.AddFunctionAttr(attr)
		}
	}
	g.intrinsics[in.Name] = fn
	return fnType, fn
}

// intrinsicParamType gives the LLVM type of one intrinsic parameter. A compile-time operand (a
// device pin, a logic type, a slot index, an enum) stays an i64 whatever the value type is, since
// instruction selection reads it back with LLVMIsAConstantInt; only the two runtime kinds carry a value a register holds.
func (g *generator) intrinsicParamType(in *sema.Intrinsic, i int) llvm.Type {
	if i >= len(in.Params) {
		return g.i64
	}
	// Default is the rule: an operand resolved at compile time is an integer.
	//exhaustive:ignore
	switch in.Params[i] {
	case sema.OperandDouble, sema.OperandValue:
		return g.f64
	default:
		return g.i64
	}
}

// machineTruncIntrinsic is LLVM's own toward-zero rounding, spelled so the optimizer knows what the
// machine's trunc instruction does: llvm.trunc already answers a NaN with a NaN and an infinity with
// itself, so nothing it licenses is an identity the machine would refuse. Stating it as the
// intrinsic rather than an opaque call is what lets a constant one fold away.
var machineTruncIntrinsic = &sema.Intrinsic{
	Name:   "llvm.trunc.f64",
	Result: sema.DoubleType,
	Params: []sema.OperandKind{sema.OperandDouble},
	Pure:   true,
}

// machineTrunc rounds a double toward zero.
func (g *generator) machineTrunc(v llvm.Value) llvm.Value {
	fnType, fn := g.intrinsicFunc(machineTruncIntrinsic, 1)
	return g.builder.CreateCall(fnType, fn, []llvm.Value{v}, "trunc")
}
