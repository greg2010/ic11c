package isel

import (
	"math"
	"strconv"
	"strings"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/sema"
	"tinygo.org/x/go-llvm"
)

// operandRole says how one intrinsic argument reaches the machine. IR
// generation passes every argument as an i64, with the compile-time ones
// already resolved to their encodings, so the shape of the machine operand is
// recovered from this table rather than from the call.
type operandRole uint8

const (
	// roleValue is a position the machine fills from a runtime value, register
	// or literal. Slot indices and prefab hashes are this too — the chip reads
	// the same register for all of them, and whether the double is narrowed is
	// [narrowsOperand]'s question, not this table's.
	roleValue operandRole = iota
	// roleRegister is a bare r? position. The store forms put the value there,
	// and a literal has to be moved into a register first.
	roleRegister
	roleDevice
	roleLogicType
	roleSlotType
	roleBatchMode
	roleReagentMode
	// roleIgnored is an argument the machine instruction has no operand for.
	// llvm.abs carries a flag saying whether the most negative input is poison;
	// the machine's abs works on doubles and draws no such distinction, so the
	// flag is consumed and dropped.
	roleIgnored
)

// intrinsicForm is the machine instruction one intrinsic selects, and how
// its arguments map onto that instruction's operands. The machine operand
// order matches the MicroC argument order, so a result form's destination
// register is just prefixed onto it.
type intrinsicForm struct {
	op ic10.Opcode
	// result reports whether the instruction writes a destination register,
	// which is then the first machine operand.
	result bool
	roles  []operandRole
}

// intrinsicForms maps each intrinsic to the single instruction it is. The
// llvm.* names are ones InstCombine forms out of ordinary comparisons and
// selects; only the signed forms are here, since an unsigned extremum would
// be wrong for an operand with its top bit set.
var intrinsicForms = map[string]intrinsicForm{
	"llvm.smin.i64": {op: isa.OpMin, result: true, roles: []operandRole{roleValue, roleValue}},
	"llvm.smax.i64": {op: isa.OpMax, result: true, roles: []operandRole{roleValue, roleValue}},
	"llvm.abs.i64":  {op: isa.OpAbs, result: true, roles: []operandRole{roleValue, roleIgnored}},
	"llvm.fabs.f64": {op: isa.OpAbs, result: true, roles: []operandRole{roleValue}},

	// The machine's trunc matches llvm.trunc: NaN answers NaN, infinity answers
	// itself, so nothing here refuses an identity llvm.trunc licenses.
	// llvm.round is deliberately absent — it rounds halves away from zero
	// where the machine's round is banker's — so reportUnselectedFold refuses it.
	"llvm.trunc.f64": {op: isa.OpTrunc, result: true, roles: []operandRole{roleValue}},

	// Declarations, not LLVM's own instructions: the machine reads each
	// operand through a conversion to a signed 64-bit integer that can fault,
	// and an operation that can fault must not be hoisted above whatever
	// bounds it — only a call the optimizer may not speculate guarantees that.
	"__ic_and": {op: isa.OpAnd, result: true, roles: []operandRole{roleValue, roleValue}},
	"__ic_or":  {op: isa.OpOr, result: true, roles: []operandRole{roleValue, roleValue}},
	"__ic_xor": {op: isa.OpXor, result: true, roles: []operandRole{roleValue, roleValue}},
	"__ic_not": {op: isa.OpNot, result: true, roles: []operandRole{roleValue}},
	"__ic_shl": {op: isa.OpSll, result: true, roles: []operandRole{roleValue, roleValue}},
	"__ic_shr": {op: isa.OpSra, result: true, roles: []operandRole{roleValue, roleValue}},

	"__ic_load":                  {op: isa.OpL, result: true, roles: []operandRole{roleDevice, roleLogicType}},
	"__ic_store":                 {op: isa.OpS, roles: []operandRole{roleDevice, roleLogicType, roleRegister}},
	"__ic_load_slot":             {op: isa.OpLs, result: true, roles: []operandRole{roleDevice, roleValue, roleSlotType}},
	"__ic_store_slot":            {op: isa.OpSs, roles: []operandRole{roleDevice, roleValue, roleSlotType, roleRegister}},
	"__ic_load_batch":            {op: isa.OpLb, result: true, roles: []operandRole{roleValue, roleLogicType, roleBatchMode}},
	"__ic_store_batch":           {op: isa.OpSb, roles: []operandRole{roleValue, roleLogicType, roleRegister}},
	"__ic_load_batch_named":      {op: isa.OpLbn, result: true, roles: []operandRole{roleValue, roleValue, roleLogicType, roleBatchMode}},
	"__ic_store_batch_named":     {op: isa.OpSbn, roles: []operandRole{roleValue, roleValue, roleLogicType, roleRegister}},
	"__ic_load_batch_slot":       {op: isa.OpLbs, result: true, roles: []operandRole{roleValue, roleValue, roleSlotType, roleBatchMode}},
	"__ic_store_batch_slot":      {op: isa.OpSbs, roles: []operandRole{roleValue, roleValue, roleSlotType, roleRegister}},
	"__ic_load_batch_named_slot": {op: isa.OpLbns, result: true, roles: []operandRole{roleValue, roleValue, roleValue, roleSlotType, roleBatchMode}},
	"__ic_load_reagent":          {op: isa.OpLr, result: true, roles: []operandRole{roleDevice, roleReagentMode, roleValue}},
	"__ic_device_present":        {op: isa.OpSdse, result: true, roles: []operandRole{roleDevice}},

	"__ic_yield": {op: isa.OpYield},
	"__ic_sleep": {op: isa.OpSleep, roles: []operandRole{roleValue}},

	"__ic_isnan": {op: isa.OpSnan, result: true, roles: []operandRole{roleValue}},
	"__ic_rand":  {op: isa.OpRand, result: true},

	"__ic_sqrt":  {op: isa.OpSqrt, result: true, roles: []operandRole{roleValue}},
	"__ic_abs":   {op: isa.OpAbs, result: true, roles: []operandRole{roleValue}},
	"__ic_sgn":   {op: isa.OpSgn, result: true, roles: []operandRole{roleValue}},
	"__ic_round": {op: isa.OpRound, result: true, roles: []operandRole{roleValue}},
	"__ic_trunc": {op: isa.OpTrunc, result: true, roles: []operandRole{roleValue}},
	"__ic_ceil":  {op: isa.OpCeil, result: true, roles: []operandRole{roleValue}},
	"__ic_floor": {op: isa.OpFloor, result: true, roles: []operandRole{roleValue}},
	"__ic_log":   {op: isa.OpLog, result: true, roles: []operandRole{roleValue}},
	"__ic_exp":   {op: isa.OpExp, result: true, roles: []operandRole{roleValue}},
	"__ic_sin":   {op: isa.OpSin, result: true, roles: []operandRole{roleValue}},
	"__ic_cos":   {op: isa.OpCos, result: true, roles: []operandRole{roleValue}},
	"__ic_tan":   {op: isa.OpTan, result: true, roles: []operandRole{roleValue}},
	"__ic_asin":  {op: isa.OpAsin, result: true, roles: []operandRole{roleValue}},
	"__ic_acos":  {op: isa.OpAcos, result: true, roles: []operandRole{roleValue}},
	"__ic_atan":  {op: isa.OpAtan, result: true, roles: []operandRole{roleValue}},

	"__ic_min":   {op: isa.OpMin, result: true, roles: []operandRole{roleValue, roleValue}},
	"__ic_max":   {op: isa.OpMax, result: true, roles: []operandRole{roleValue, roleValue}},
	"__ic_pow":   {op: isa.OpPow, result: true, roles: []operandRole{roleValue, roleValue}},
	"__ic_atan2": {op: isa.OpAtan2, result: true, roles: []operandRole{roleValue, roleValue}},

	"__ic_clamp": {op: isa.OpClamp, result: true, roles: []operandRole{roleValue, roleValue, roleValue}},
	"__ic_lerp":  {op: isa.OpLerp, result: true, roles: []operandRole{roleValue, roleValue, roleValue}},
}

// optimizerPrefix marks a call no source line contains. LLVM names its own
// intrinsics this way, and a diagnostic about one has to say the optimizer
// formed it rather than blame the programmer for a call they did not write.
const optimizerPrefix = "llvm."

func (s *selector) lowerCall(info *blockInfo, in llvm.Value) {
	callee := in.CalledValue()
	name := callee.Name()
	if !callee.IsAFunction().IsNil() && !callee.IsDeclaration() {
		// A definition in this module is a real call. Everything else the
		// module calls is a declaration standing for one machine instruction.
		s.lowerDirectCall(info, in)
		return
	}
	form, known := intrinsicForms[name]
	if !known {
		if strings.HasPrefix(name, optimizerPrefix) {
			s.reportUnselectedFold(in, name)
			return
		}
		s.errorf(s.position(in), "the call to '%s' is not selected; only the machine intrinsics reach an instruction, and every other call is inlined", name)
		return
	}
	args := in.OperandsCount() - 1
	if args != len(form.roles) {
		s.errorf(s.position(in), "'%s' was called with %d arguments, and its instruction takes %d", name, args, len(form.roles))
		return
	}
	// Before any operand is built, since building one can emit a move: an
	// instruction this refuses must leave nothing behind that was only there to
	// feed it.
	if !s.checkNarrowedOperands(in, form.op, intrinsicReads(in, form)...) {
		return
	}

	operands := make([]mir.Operand, 0, len(form.roles)+1)
	if form.result {
		operands = append(operands, s.def(in))
	}
	for i, role := range form.roles {
		if role == roleIgnored {
			continue
		}
		operands = append(operands, s.intrinsicOperand(info, in, i, role))
	}
	s.emit(info, in, form.op, operands...)
}

// intrinsicReads lines an intrinsic's arguments up with the operand
// positions [selector.checkNarrowedOperands] reads them against. A
// position resolved from the machine's own tables (device, enum roles) is
// filled with [blankOperand] rather than dropped, so the rest line up.
func intrinsicReads(in llvm.Value, form intrinsicForm) []llvm.Value {
	reads := make([]llvm.Value, 0, len(form.roles))
	for i, role := range form.roles {
		switch role {
		case roleValue, roleRegister:
			reads = append(reads, in.Operand(i))
		case roleDevice, roleLogicType, roleSlotType, roleBatchMode, roleReagentMode:
			reads = append(reads, blankOperand)
		case roleIgnored:
		}
	}
	return reads
}

// unsignedExtremumPrefixes name the folds a source expression can reach the
// machine's own instruction for by asking for it directly. The machine compares
// doubles, so its minimum and maximum are signed and an unsigned one is a fold
// with no instruction — but the operation the source wrote does have one.
var unsignedExtremumPrefixes = []string{optimizerPrefix + "umin.", optimizerPrefix + "umax."}

// nanBlindExtremumPrefixes name the folds whose answer differs from the
// machine's on exactly the operand this target cannot ignore: llvm.minnum
// and llvm.maxnum answer with whichever operand is not a NaN, where the
// machine's min and max propagate it.
var nanBlindExtremumPrefixes = []string{optimizerPrefix + "minnum.", optimizerPrefix + "maxnum."}

// reportUnselectedFold refuses a call no source line contains. InstCombine
// forms these expecting a target instruction for each; only the unsigned
// and NaN-blind extrema have a source-level form to advise toward, so only
// those two get specific advice below.
func (s *selector) reportUnselectedFold(in llvm.Value, name string) {
	for _, prefix := range unsignedExtremumPrefixes {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		s.errorf(s.position(in), "this expression folded into '%s', an unsigned minimum or maximum, and the machine's arithmetic is signed throughout; writing the operation as __ic_min, __ic_max, or __ic_clamp reaches the signed instruction directly instead of leaving the shape for the optimizer to choose", name)
		return
	}
	for _, prefix := range nanBlindExtremumPrefixes {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		s.errorf(s.position(in), "this expression folded into '%s', which answers with whichever operand is not a NaN; the machine's min and max propagate a NaN instead, so neither instruction computes it and selecting one would drop the NaN the program produced — write __ic_min or __ic_max where propagating it is what you want, and test for the NaN with __ic_isnan where it is not", name)
		return
	}
	s.errorf(s.position(in), "this expression folded into '%s', which the machine has no instruction for; the optimizer forms these on the expectation that a target has one, so write the expression out of the arithmetic and the __ic_ intrinsics the machine does have", name)
}

// intrinsicOperand turns one call argument into the machine operand its
// position takes. A device pin, a logic type, and the rest of the enums are
// resolved when the line is assembled, so only a constant will do there;
// analysis has already required each of them to be a compile-time constant.
func (s *selector) intrinsicOperand(info *blockInfo, in llvm.Value, i int, role operandRole) mir.Operand {
	switch role {
	case roleValue:
		return s.arg(in, i)

	case roleRegister:
		operand := s.arg(in, i)
		if _, ok := operand.(mir.Imm); !ok {
			return operand
		}
		// The store forms take a bare r?, so a literal has to land in a
		// register first.
		reg := s.fn.NewVirtReg()
		s.emit(info, in, isa.OpMove, reg, operand)
		return reg

	case roleDevice:
		code, ok := s.constantArg(in, i)
		if !ok {
			return nil
		}
		resolved, valid := sema.DecodeDevice(code)
		if !valid {
			s.errorf(s.position(in), "%s does not name a device; a device is db or d0 through d%d", s.argument(in, i), ic10.NumDevicePins-1)
			return nil
		}
		if resolved.Base {
			return mir.NewDeviceBase()
		}
		if resolved.Pin >= ic10.NumDevicePins {
			// The chip's own operand pattern admits d0 through d9 and the housing
			// has six pins, so a higher one assembles and then indexes past the
			// end of the pin array once per tick for as long as the chip runs.
			s.errorf(s.position(in), "%s names device pin d%d, and a housing has d0 through d%d", s.argument(in, i), resolved.Pin, ic10.NumDevicePins-1)
			return nil
		}
		// MicroC has no spelling for a network connection index, so every device
		// a source program names addresses the pin itself.
		device, err := mir.NewDevicePin(resolved.Pin, mir.NoConnection)
		if err != nil {
			s.errorf(s.position(in), "%s: %v", s.argument(in, i), err)
			return nil
		}
		return device

	case roleLogicType:
		value, ok := s.enumArg(in, i, math.MaxUint16, "a logic type")
		if !ok {
			return nil
		}
		return mir.LogicType{Value: ic10.LogicType(value)}

	case roleSlotType:
		value, ok := s.enumArg(in, i, math.MaxUint8, "a slot type")
		if !ok {
			return nil
		}
		return mir.LogicSlotType{Value: ic10.LogicSlotType(value)}

	case roleBatchMode:
		value, ok := s.enumArg(in, i, math.MaxInt32, "a batch mode")
		if !ok {
			return nil
		}
		return mir.BatchMode{Value: ic10.BatchMode(value)}

	case roleReagentMode:
		value, ok := s.enumArg(in, i, math.MaxInt32, "a reagent mode")
		if !ok {
			return nil
		}
		return mir.ReagentMode{Value: ic10.ReagentMode(value)}

	case roleIgnored:
	}
	s.errorf(s.position(in), "%s has no machine form", s.argument(in, i))
	return nil
}

// argument names one intrinsic argument the way the call site reads: by its
// position in the argument list and the intrinsic it was passed to.
func (s *selector) argument(in llvm.Value, i int) string {
	name := in.CalledValue().Name()
	if name == "" {
		name = "this call"
	} else {
		name = "'" + name + "'"
	}
	return "argument " + strconv.Itoa(i+1) + " of " + name
}

// enumArg resolves an operand whose machine type is narrower than the i64
// the argument arrives as, rejecting an out-of-range value rather than
// converting it: the narrowing wraps, so a batch mode of 256 would silently
// become mode 0 (Average) — reading the wrong aggregate every tick.
func (s *selector) enumArg(in llvm.Value, i int, limit int64, what string) (int64, bool) {
	value, ok := s.constantArg(in, i)
	if !ok {
		return 0, false
	}
	if value < 0 || value > limit {
		s.errorf(s.position(in), "%s is %d, outside the 0 to %d range %s is backed by", s.argument(in, i), value, limit, what)
		return 0, false
	}
	return value, true
}

func (s *selector) constantArg(in llvm.Value, i int) (int64, bool) {
	arg := in.Operand(i)
	if arg.IsAConstantInt().IsNil() {
		s.errorf(s.position(in), "%s must be known at compile time; the chip resolves it when the line is assembled, so write the name or a constant expression rather than a variable", s.argument(in, i))
		return 0, false
	}
	return arg.SExtValue(), true
}
