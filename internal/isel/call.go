package isel

import (
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/llvmir"
	"github.com/greg2010/ic11c/internal/mir"
	"tinygo.org/x/go-llvm"
)

// Calling convention: arguments arrive in r0 upward and a result leaves in
// r0. jal writes the return address into ra, the machine's only such
// register; since there is no hardware call stack, a function that itself
// calls out must save and restore ra around the call ([selector.frame]).
const (
	// maxCallArgs bounds a call's argument count: eight of the sixteen general
	// registers is as much of the file as one call may take out of the calling
	// function's allocation. Only a recursive function is compiled as a real
	// call, so this is a ceiling rather than a count non-recursive code spends.
	maxCallArgs = 8
	// resultRegister is where a called function leaves its result and where the
	// caller reads it.
	resultRegister = ic10.Register(0)
)

// argRegister names the register argument i is passed in.
func argRegister(i int) ic10.Register { return ic10.Register(i) }

// assignParams gives each parameter of a called function a virtual register
// ahead of lowering. The entry-point check here is a backstop only: a valid
// program never reaches it, since sema.checkEntryPoint already refuses a
// 'main' with parameters.
func (s *selector) assignParams(fnValue llvm.Value) bool {
	params := fnValue.Params()
	if len(params) == 0 {
		return true
	}
	if s.isEntry {
		s.errorf(s.fn.Pos, "the entry point takes %d parameters, and nothing is there to supply them", len(params))
		return false
	}
	if len(params) > maxCallArgs {
		s.errorf(s.fn.Pos, "'%s' takes %d parameters, and a call passes at most %d, one argument register each held for the whole of the calling function; that count is a ceiling rather than one a program can spend, because the only functions compiled out of line are the ones that can reach themselves and one holding more live values at once than the register file has room for is refused on that instead — pass fewer, or put the rest in a global the callee reads", s.fn.Name, len(params), maxCallArgs)
		return false
	}
	for _, param := range params {
		if !s.widthOK(fnValue, param.Type()) {
			return false
		}
		s.vregs[param] = s.fn.NewVirtReg()
	}
	return true
}

// lowerParams copies each argument out of the register it arrived in, at the
// top of the entry block. The copy frees the argument register before the
// first instruction of the body, which a call the function makes itself
// could otherwise overwrite.
func (s *selector) lowerParams(fnValue llvm.Value) {
	params := fnValue.Params()
	if len(params) == 0 || len(s.order) == 0 {
		return
	}
	info := s.blocks[s.order[0]]
	for i, param := range params {
		reg, ok := s.vregs[param]
		if !ok {
			continue
		}
		instr, err := unconverted(isa.OpMove, s.fn.Pos, reg, mir.PhysReg{Reg: argRegister(i)})
		if err != nil {
			s.errorf(s.fn.Pos, "parameter %d of %s: %v", i, s.fn.Name, err)
			continue
		}
		info.body = append(info.body, instr)
	}
}

// lowerDirectCall emits a real call: the arguments into their registers, the
// jal, and the result out of the register it came back in.
func (s *selector) lowerDirectCall(info *blockInfo, in llvm.Value) {
	callee := in.CalledValue()
	entry := callee.FirstBasicBlock()
	if entry.IsNil() {
		s.errorf(s.position(in), "'%s' has no definition to call; MicroC has no linker", callee.Name())
		return
	}
	// The argument count is not checked here: sema already holds a call to
	// its declaration's parameter count, and assignParams reports a mismatch
	// against the definition, where the fix belongs.
	args := in.OperandsCount() - 1
	for i := range args {
		s.emit(info, in, isa.OpMove, mir.PhysReg{Reg: argRegister(i)}, s.arg(in, i))
	}
	// The entry is the callee's first block, so its position within the callee
	// is zero; selection of the callee itself will label it from the same pair.
	s.emit(info, in, isa.OpJal, mir.Label{Name: blockLabel(callee.Name(), entry, 0)})
	// A result nothing reads costs no line: the move out of the result register
	// exists to free that register, and nothing is holding it.
	if in.Type().TypeKind() == llvm.VoidTypeKind || in.FirstUse().IsNil() {
		return
	}
	s.emit(info, in, isa.OpMove, s.def(in), mir.PhysReg{Reg: resultRegister})
}

// frame installs the epilogue a called function owes its caller, saving
// and restoring ra around any call it makes itself. pop decrements sp
// before its bounds check and never rolls back, so an unmatched pop would
// walk sp into the data region silently.
func (s *selector) frame() {
	if s.isEntry {
		return
	}
	exit := s.blockByLabel(s.endLbl)
	if exit == nil {
		s.errorf(s.fn.Pos, "'%s' has no exit block to return through", s.fn.Name)
		return
	}
	if s.callsOut() {
		if save := s.instr(isa.OpPush, mir.PhysReg{Reg: ic10.RegRA}); save != nil {
			entry := s.fn.Blocks[0]
			entry.Instrs = append([]*mir.Instr{save}, entry.Instrs...)
		}
		if restore := s.instr(isa.OpPop, mir.PhysReg{Reg: ic10.RegRA}); restore != nil {
			exit.Append(restore)
		}
	}
	if ret := s.instr(isa.OpJ, mir.PhysReg{Reg: ic10.RegRA}); ret != nil {
		exit.Append(ret)
	}
}

// callsOut reports whether the function makes a call, which is what costs it
// its return address.
func (s *selector) callsOut() bool {
	for _, instr := range s.fn.AllInstrs() {
		if instr.Op == isa.OpJal {
			return true
		}
	}
	return false
}

func (s *selector) blockByLabel(label string) *mir.Block {
	for _, block := range s.fn.Blocks {
		if block.Label == label {
			return block
		}
	}
	return nil
}

// instr builds one instruction of the calling convention, charged to the
// function's position since no source statement asked for it. It calls
// [unconverted] rather than mir.NewInstr because the opcode is chosen here,
// not read off the machine's conversion tables.
func (s *selector) instr(op ic10.Opcode, args ...mir.Operand) *mir.Instr {
	instr, err := unconverted(op, s.fn.Pos, args...)
	if err != nil {
		s.errorf(s.fn.Pos, "the calling convention of %s: %v; this is a defect in the compiler, not in the program", s.fn.Name, err)
		return nil
	}
	return instr
}

// recursiveFunctions names every function that can reach itself in the
// module's call graph, directly or through a cycle. This is the optimized
// graph, not the source one analysis answered, and is what a fixed
// data-region slot must actually be safe against.
func recursiveFunctions(defined []llvm.Value) map[string]bool {
	callees := make(map[string][]string, len(defined))
	for _, fn := range defined {
		callees[fn.Name()] = calleeNames(fn)
	}
	recursive := make(map[string]bool, len(defined))
	for _, fn := range defined {
		start := fn.Name()
		seen := make(map[string]bool, len(defined))
		stack := append([]string(nil), callees[start]...)
		for len(stack) > 0 {
			name := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if name == start {
				recursive[start] = true
				break
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			stack = append(stack, callees[name]...)
		}
	}
	return recursive
}

// calleeNames lists the defined functions one function calls. A declaration is
// an intrinsic, which is one machine instruction and calls nothing.
func calleeNames(fn llvm.Value) []string {
	var names []string
	for in := range llvmir.FuncInstrs(fn) {
		if in.InstructionOpcode() != llvm.Call {
			continue
		}
		callee := in.CalledValue()
		if callee.IsAFunction().IsNil() || callee.IsDeclaration() {
			continue
		}
		names = append(names, callee.Name())
	}
	return names
}
