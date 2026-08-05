package irgen

import (
	"github.com/greg2010/ic11c/internal/sema"
	"tinygo.org/x/go-llvm"
)

func (g *generator) zero() llvm.Value { return g.intVal(0) }

// intVal is a MicroC integer constant, which is the double the register holds.
//
// constInt is the raw i64 one, which the addressing machinery and the
// compile-time intrinsic operands need instead.
func (g *generator) intVal(v int64) llvm.Value { return g.constFloat(float64(v)) }

func (g *generator) constInt(v int64) llvm.Value {
	return llvm.ConstInt(g.i64, uint64(v), true)
}

func (g *generator) constFloat(v float64) llvm.Value { return llvm.ConstFloat(g.f64, v) }

// toMachineInt reads a value as the i64 LLVM's addressing and switch constructs require. It is an
// fptosi the backend selects nothing for: [selector.producesOperand] says the register already holds the whole number.
func (g *generator) toMachineInt(v llvm.Value) llvm.Value {
	if v.Type() != g.f64 {
		return v
	}
	return g.builder.CreateFPToSI(v, g.i64, "")
}

// fromMachineInt reads an i64 back as a MicroC integer, and is the inverse of
// [generator.toMachineInt].
func (g *generator) fromMachineInt(v llvm.Value) llvm.Value {
	if v.Type() == g.f64 {
		return v
	}
	return g.builder.CreateSIToFP(v, g.f64, "")
}

// constant lowers a folded compile-time value in the type analysis gave it.
func (g *generator) constant(v sema.Value) llvm.Value {
	if v.Type.Kind() == sema.Double {
		return g.constFloat(v.Float)
	}
	return g.intVal(v.Int)
}

func (g *generator) newBlock(name string) llvm.BasicBlock {
	return llvm.AddBasicBlock(g.fn, name)
}

func (g *generator) block() llvm.BasicBlock { return g.builder.GetInsertBlock() }

// setBlock continues generation at the end of bb.
func (g *generator) setBlock(bb llvm.BasicBlock) {
	g.builder.SetInsertPointAtEnd(bb)
	g.terminated = false
}

// tail moves bb to the end of the function and continues there. It is used for a join block (a loop
// exit, an if merge, an inlined call's continuation), which is created before the code it joins but
// must be laid out after it: instruction selection drops a jump to the block that immediately
// follows, so a join block left where it was created costs a branch per construct.
func (g *generator) tail(bb llvm.BasicBlock) {
	if last := g.fn.LastBasicBlock(); last != bb {
		bb.MoveAfter(last)
	}
	g.setBlock(bb)
}

// br branches to bb unless the current block already ends in a terminator,
// which is what a preceding return, break, or continue leaves behind.
func (g *generator) br(bb llvm.BasicBlock) {
	if g.terminated {
		return
	}
	g.builder.CreateBr(bb)
	g.terminated = true
}

func (g *generator) condBr(cond llvm.Value, then, els llvm.BasicBlock) {
	if g.terminated {
		return
	}
	g.builder.CreateCondBr(cond, then, els)
	g.terminated = true
}
