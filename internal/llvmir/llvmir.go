// Package llvmir reads an LLVM module the backend was handed: the walks over
// its instructions, and the source positions its debug information carries.
package llvmir

import (
	"iter"

	"tinygo.org/x/go-llvm"
)

// ModuleInstrs iterates every instruction of every function m defines, in
// layout order, skipping declarations and naming the function that holds each.
// The walk holds a position in the block it is part way through, so the
// consumer must not erase the instruction it was handed.
func ModuleInstrs(m llvm.Module) iter.Seq2[llvm.Value, llvm.Value] {
	return func(yield func(fn, in llvm.Value) bool) {
		for fn := m.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
			if fn.IsDeclaration() {
				continue
			}
			for in := range FuncInstrs(fn) {
				if !yield(fn, in) {
					return
				}
			}
		}
	}
}

// FuncInstrs iterates fn's instructions in layout order, and yields nothing for
// a declaration. It holds the position [ModuleInstrs] does.
func FuncInstrs(fn llvm.Value) iter.Seq[llvm.Value] {
	return func(yield func(llvm.Value) bool) {
		for bb := fn.FirstBasicBlock(); !bb.IsNil(); bb = llvm.NextBasicBlock(bb) {
			for in := range BlockInstrs(bb) {
				if !yield(in) {
					return
				}
			}
		}
	}
}

// BlockInstrs iterates bb's instructions in order. It holds the position
// [ModuleInstrs] does.
func BlockInstrs(bb llvm.BasicBlock) iter.Seq[llvm.Value] {
	return func(yield func(llvm.Value) bool) {
		for in := bb.FirstInstruction(); !in.IsNil(); in = llvm.NextInstruction(in) {
			if !yield(in) {
				return
			}
		}
	}
}
