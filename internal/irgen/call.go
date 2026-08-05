package irgen

import (
	"slices"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/sema"
	"tinygo.org/x/go-llvm"
)

func (g *generator) call(x *ast.CallExpr) llvm.Value {
	if in, ok := g.prog.Intrinsics[x]; ok {
		return g.intrinsicCall(x, in)
	}
	fn, ok := g.prog.Calls[x]
	if !ok {
		g.errorf(x.Pos(), "the call did not resolve to a declared function")
		return g.zero()
	}
	if fn == g.prog.Main {
		g.errorf(x.Pos(), "'%s' is the entry point, reached at line 0 rather than through jal, so it holds no return address to return through and cannot be called", fn.Name)
		return g.zero()
	}
	if llfn, outlined := g.outlined[fn]; outlined {
		return g.directCall(x, fn, llfn)
	}
	return g.inlineCall(x, fn)
}

// directCall emits a real call to a function compiled out of line: an argument move per parameter, a
// jal, and a move of the result back, with the caller's own live values and return address saved
// around it by the backend's register assignment.
func (g *generator) directCall(x *ast.CallExpr, fn *sema.Func, llfn llvm.Value) llvm.Value {
	if len(x.Args) != len(fn.Params) {
		g.errorf(x.Lparen, "'%s' takes %d arguments, found %d", fn.Name, len(fn.Params), len(x.Args))
		return g.zero()
	}
	args := make([]llvm.Value, len(x.Args))
	for i, arg := range x.Args {
		args[i] = g.value(arg)
	}
	g.setLoc(x.Pos())
	if fn.Result.Kind() == sema.Void {
		g.builder.CreateCall(llfn.GlobalValueType(), llfn, args, "")
		return llvm.Value{}
	}
	return g.builder.CreateCall(llfn.GlobalValueType(), llfn, args, fn.Name)
}

// intrinsicCall emits a call to the opaque declaration standing for one machine
// instruction. Every operand analysis resolved at compile time — a device pin,
// a logic type, a slot index, the hash of a string literal — becomes a constant
// argument, so instruction selection reads the encoding straight off the call.
func (g *generator) intrinsicCall(x *ast.CallExpr, call *sema.IntrinsicCall) llvm.Value {
	in := call.Intrinsic
	if len(call.Args) != len(in.Params) {
		g.errorf(x.Lparen, "%s did not resolve all of its arguments", in.Name)
		return g.zero()
	}
	// __ic_hash is folded at compile time and reaches no instruction: the CRC-32
	// of the literal is the value.
	if len(in.Params) == 1 && in.Params[0] == sema.OperandString {
		if !call.Args[0].Resolved {
			g.errorf(x.Args[0].Pos(), "%s did not fold its string literal", in.Name)
			return g.zero()
		}
		return g.intVal(call.Args[0].Value)
	}

	// A resolved operand is a constant in whichever type the declaration gives
	// its position, since LLVM's type system has to agree with the call.
	constArg := func(i int, v int64) llvm.Value {
		if g.intrinsicParamType(in, i) == g.f64 {
			return g.constFloat(float64(v))
		}
		return g.constInt(v)
	}
	args := make([]llvm.Value, len(call.Args))
	for i, operand := range call.Args {
		switch {
		case operand.DeviceParam != nil:
			device, bound := g.devices[operand.DeviceParam]
			if !bound {
				g.errorf(x.Args[i].Pos(), "'%s' is a dev parameter and no call site bound it; a function taking one is inlined so that each site's device is substituted here", operand.DeviceParam.Name)
				return g.zero()
			}
			args[i] = constArg(i, device.Code())
		case operand.Resolved:
			args[i] = constArg(i, operand.Value)
		case operand.Kind == sema.OperandValue || operand.Kind == sema.OperandDouble:
			args[i] = g.value(x.Args[i])
		default:
			g.errorf(x.Args[i].Pos(), "the %s argument of %s did not resolve", operand.Kind, in.Name)
			args[i] = g.zero()
		}
	}

	fnType, fn := g.intrinsicFunc(in, len(args))
	g.setLoc(x.Pos())
	if in.Result.Kind() == sema.Void {
		g.builder.CreateCall(fnType, fn, args, "")
		return llvm.Value{}
	}
	return g.builder.CreateCall(fnType, fn, args, "")
}

// inlineCall splices fn's body into the caller. Parameters become allocas the arguments are stored
// into, and a return stores through a result slot and branches to the continuation after the call
// site. The machine has one ra and no hardware call stack, so a callee that itself calls costs four
// instructions saving and restoring it rather than two — which is why inlining is the default.
func (g *generator) inlineCall(x *ast.CallExpr, fn *sema.Func) llvm.Value {
	if fn.Decl == nil || fn.Decl.Body == nil {
		g.errorf(x.Pos(), "'%s' has no definition to inline; MicroC has no linker", fn.Name)
		return g.zero()
	}
	if slices.Contains(g.inlining, fn) {
		g.errorf(x.Pos(), "'%s' reaches itself through this call and is not compiled out of line, so there is no body left to splice in", fn.Name)
		return g.zero()
	}
	if len(g.inlining) >= maxInlineDepth {
		g.errorf(x.Pos(), "inlining '%s' would nest more than %d calls deep", fn.Name, maxInlineDepth)
		return g.zero()
	}

	if len(x.Args) != len(fn.Params) {
		g.errorf(x.Lparen, "'%s' takes %d arguments, found %d", fn.Name, len(fn.Params), len(x.Args))
		return g.zero()
	}
	args := make([]llvm.Value, len(x.Args))
	bound := make(map[*sema.Symbol]sema.Device)
	for i, arg := range x.Args {
		if fn.Params[i].Type.Kind() == sema.Dev {
			device, ok := g.deviceOf(arg)
			if !ok {
				g.errorf(arg.Pos(), "the device passed as '%s' did not resolve to a pin, and the chip needs one where the line is assembled", fn.Params[i].Name)
				return g.zero()
			}
			bound[fn.Params[i]] = device
			continue
		}
		args[i] = g.value(arg)
	}

	g.setLoc(x.Pos())
	for i, param := range fn.Params {
		if param.Type.Kind() == sema.Dev {
			continue
		}
		slot, ok := g.storageOf(param)
		if !ok {
			g.errorf(x.Args[i].Pos(), "the parameter '%s' has type %s, which is not lowered yet", param.Name, param.Type)
			continue
		}
		// A parameter is rebound per call site, so the store has to precede the
		// body rather than merge with an earlier expansion's.
		g.builder.CreateStore(args[i], slot)
	}

	var retSlot llvm.Value
	if fn.Result.Kind() != sema.Void {
		result, lowered := g.llvmType(fn.Result)
		if !lowered {
			g.errorf(x.Pos(), "'%s' returns %s, which is not lowered", fn.Name, fn.Result)
			return g.zero()
		}
		retSlot = g.alloca(result, fn.Name+".result")
	}
	cont := g.newBlock(fn.Name + ".cont")

	// A loop in the caller must not be reachable by a break or continue in the
	// callee, and analysis has already rejected one; clearing the stacks makes
	// a defect here a diagnostic rather than a jump into another function.
	savedBreaks, savedContinues := g.breaks, g.continues
	g.breaks, g.continues = nil, nil
	// A dev parameter is substituted rather than passed, and the substitution
	// belongs to this expansion alone: the same function inlined at two sites
	// names two different pins.
	savedDevices := g.devices
	g.devices = bound
	for sym, device := range savedDevices {
		if _, rebound := g.devices[sym]; !rebound {
			g.devices[sym] = device
		}
	}
	g.frames = append(g.frames, frame{retSlot: retSlot, retBlock: cont})
	g.inlining = append(g.inlining, fn)
	// Everything the body generates from here is the callee's source attributed
	// to this call, which is what makes the byte report distinguish two
	// expansions of one function.
	savedInlinedAt := g.inlinedAt
	g.inlinedAt = g.enterInlineSite(x.Pos(), fn.Name)

	g.stmt(fn.Decl.Body)
	g.br(cont)

	g.inlinedAt = savedInlinedAt
	g.inlining = g.inlining[:len(g.inlining)-1]
	g.frames = g.frames[:len(g.frames)-1]
	g.devices = savedDevices
	g.breaks, g.continues = savedBreaks, savedContinues

	g.tail(cont)
	if retSlot.IsNil() {
		return llvm.Value{}
	}
	g.setLoc(x.Pos())
	return g.builder.CreateLoad(retSlot.AllocatedType(), retSlot, fn.Name)
}
