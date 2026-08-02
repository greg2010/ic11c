package vm

import "math"

// approximateEpsilon is the floor the approximate comparisons clamp their
// tolerance to, the literal the game uses in _SAP_Operation and its relatives.
// It is not double.Epsilon and not a relative tolerance on its own.
const approximateEpsilon = 1.1210387714598537e-44

// step is what one instruction hands back to the tick loop.
//
// The game packs both fields into a single signed int: a negative return ends
// the tick and its negation is the line to resume at. That encoding cannot
// represent a jump to a negative line, which the chip stops on, so the two are
// carried separately here and only yield and sleep ever set endTick.
type step struct {
	next    int
	endTick bool
}

// advance is the step of an instruction that falls through to the next line.
func advance(index int) step { return step{next: index + 1} }

// operation is one compiled line.
type operation interface {
	execute(index int) (step, error)
}

// noopOperation is _NOOP_Operation: a blank line, a comment, or a label. It
// still costs an instruction from the tick budget.
type noopOperation struct{}

func (noopOperation) execute(index int) (step, error) { return advance(index), nil }

// storeValueOp covers _Operation_1_1 through _Operation_1_3 and every
// instruction built on them: a destination register, up to three double
// arguments resolved left to right, and a pure function of them.
//
// Resolution order is load bearing. The destination index resolves first, so a
// bad destination faults before a bad argument does.
type storeValueOp struct {
	m     *Machine
	store indexVariable
	args  [3]doubleValueVariable
	argc  int
	apply func(a, b, c float64) float64
}

func (o *storeValueOp) execute(index int) (step, error) {
	dest, err := o.store.resolveIndex(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	var values [3]float64
	for i := range o.argc {
		value, err := o.args[i].resolve(targetRegister, true)
		if err != nil {
			return step{}, err
		}
		values[i] = value
	}
	o.m.registers[dest] = o.apply(values[0], values[1], values[2])
	return advance(index), nil
}

// storeLongOp covers _AND_Operation, _OR_Operation, _XOR_Operation,
// _NOR_Operation and _NOT_Operation.
//
// Every one of them routes its arguments through DoubleToLong and its result
// through LongToDouble, which is where the 53 bit corruption comes from: the
// operation itself is exact on 64 bits and the round trip is not.
type storeLongOp struct {
	m      *Machine
	store  indexVariable
	args   [2]doubleValueVariable
	argc   int
	signed bool
	apply  func(a, b int64) int64
}

func (o *storeLongOp) execute(index int) (step, error) {
	dest, err := o.store.resolveIndex(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	var values [2]int64
	for i := range o.argc {
		value, err := o.args[i].resolveLong(targetRegister, o.signed)
		if err != nil {
			return step{}, err
		}
		values[i] = value
	}
	o.m.registers[dest] = LongToDouble(o.apply(values[0], values[1]))
	return advance(index), nil
}

// shiftOp covers _SRL_Operation, _SRA_Operation, _SLA_SLL_Operation,
// _ROL_Operation and _ROR_Operation: a value argument converted to an integer
// and a distance argument converted to an int.
//
// signed selects which conversion the value takes. srl and the rotates ask for
// the unsigned form, which keeps 54 bits, while sra and the left shift ask for
// the signed form, which keeps 53 and sign extends. sla and sll are the same
// class in the game and both zero fill; the help text claiming sla sign fills
// is wrong.
type shiftOp struct {
	m        *Machine
	store    indexVariable
	value    doubleValueVariable
	distance doubleValueVariable
	signed   bool
	apply    func(value int64, distance int) int64
}

func (o *shiftOp) execute(index int) (step, error) {
	dest, err := o.store.resolveIndex(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	value, err := o.value.resolveLong(targetRegister, o.signed)
	if err != nil {
		return step{}, err
	}
	distance, err := o.distance.resolveInt(targetRegister)
	if err != nil {
		return step{}, err
	}
	o.m.registers[dest] = LongToDouble(o.apply(value, distance))
	return advance(index), nil
}

// extOp is _EXT_Operation: extract a bit field.
//
// The payload is capped at 53 bits, one narrower than the 54 rol and ror rotate
// over. Every guard below is checked in the game's order, so which fault a bad
// pair of offsets produces depends on that order.
type extOp struct {
	m      *Machine
	store  indexVariable
	source doubleValueVariable
	offset doubleValueVariable
	width  doubleValueVariable
	line   int
}

func (o *extOp) execute(index int) (step, error) {
	dest, err := o.store.resolveIndex(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	source, err := o.source.resolveLong(targetRegister, false)
	if err != nil {
		return step{}, err
	}
	offset, err := o.offset.resolveInt(targetRegister)
	if err != nil {
		return step{}, err
	}
	width, err := o.width.resolveInt(targetRegister)
	if err != nil {
		return step{}, err
	}
	if err := checkFieldBounds(offset, width, o.line); err != nil {
		return step{}, err
	}
	payload := source & payloadMask
	mask := uint64(fieldMask(width) << offset)
	field := (uint64(payload) & mask) >> offset
	o.m.registers[dest] = LongToDouble(int64(field))
	return advance(index), nil
}

// insOp is _INS_Operation: insert a bit field into the destination's current
// value, which it reads back through the same unsigned conversion.
type insOp struct {
	m      *Machine
	store  indexVariable
	source doubleValueVariable
	offset doubleValueVariable
	width  doubleValueVariable
	line   int
}

func (o *insOp) execute(index int) (step, error) {
	dest, err := o.store.resolveIndex(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	source, err := o.source.resolveLong(targetRegister, false)
	if err != nil {
		return step{}, err
	}
	offset, err := o.offset.resolveInt(targetRegister)
	if err != nil {
		return step{}, err
	}
	width, err := o.width.resolveInt(targetRegister)
	if err != nil {
		return step{}, err
	}
	if err := checkFieldBounds(offset, width, o.line); err != nil {
		return step{}, err
	}
	current := DoubleToLong(o.m.registers[dest], false) & payloadMask
	field := uint64(source) & uint64(payloadMask)
	widthMask := uint64(fieldMask(width))
	shifted := widthMask << offset
	kept := current &^ int64(shifted)
	inserted := (field & widthMask) << offset
	o.m.registers[dest] = LongToDouble((kept | int64(inserted)) & payloadMask)
	return advance(index), nil
}

// fieldMask is the width mask ext and ins build. checkFieldBounds has already
// held the width to 1 through payloadBits, so the shift cannot overflow.
func fieldMask(width int) int64 { return 1<<width - 1 }

// checkFieldBounds is the guard sequence ext and ins share, in the game's
// order. A zero width is an underflow, not an empty field.
func checkFieldBounds(offset, width, line int) error {
	if width <= 0 {
		return newFault(ExcShiftUnderflow, line)
	}
	if offset < 0 {
		return newFault(ExcShiftUnderflow, line)
	}
	if offset >= payloadBits {
		return newFault(ExcShiftOverflow, line)
	}
	if width > payloadBits || offset+width > payloadBits {
		return newFault(ExcPayloadOverflow, line)
	}
	return nil
}

// randOperation is _RAND_Operation. The game draws from an unseeded
// System.Random shared by every chip; this draws from the machine's own source
// so that a program using rand stays reproducible.
type randOperation struct {
	m     *Machine
	store indexVariable
}

func (o *randOperation) execute(index int) (step, error) {
	dest, err := o.store.resolveIndex(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	o.m.registers[dest] = o.m.random()
	return advance(index), nil
}

// dotnetNaN is the quiet NaN a .NET double operation produces when neither
// operand already carries one: the x86 default, which C# exposes as double.NaN.
// Go's math.NaN is 0x7ff8000000000001 and is not interchangeable with it.
var dotnetNaN = math.Float64frombits(0xfff8000000000000)

// mod is _MOD_Operation, whose entire body is `num3 = num % num2; if (num3 <
// 0.0) num3 += num2;`.
//
// The remainder is truncated, so it carries the dividend's sign, and the
// divisor is added back exactly once when that remainder came out negative. That coincides
// with a floor modulus only for a positive divisor: mod -7 -3 is -4, not -1,
// because the add-back moves a negative remainder further from zero.
func mod(a, b float64) float64 {
	r := truncatedRemainder(a, b)
	if r < 0 {
		r += b
	}
	return r
}

// truncatedRemainder is C#'s % on doubles, which bottoms out in the C library's
// fmod.
//
// fmod evaluates (x*y)/(x*y) whenever the dividend is not finite, the divisor
// is zero, or either operand is NaN. An operand that is already a NaN therefore
// propagates with its own bit pattern, the dividend winning when both are, and
// every other case yields the default NaN. Go's math.Mod substitutes a NaN of
// its own for all of them, which is the only place the two disagree.
func truncatedRemainder(a, b float64) float64 {
	switch {
	case math.IsNaN(a):
		return a
	case math.IsNaN(b):
		return b
	case math.IsInf(a, 0) || b == 0:
		return dotnetNaN
	default:
		return math.Mod(a, b)
	}
}

// approximatelyEqual is the tolerance test behind sap, sna, bap, bna and their
// relatives: a relative tolerance against the larger magnitude, floored at a
// fixed constant so that comparisons near zero still succeed.
func approximatelyEqual(a, b, tolerance float64) bool {
	return math.Abs(a-b) <= math.Max(tolerance*math.Max(math.Abs(a), math.Abs(b)), approximateEpsilon)
}

// boolValue turns a comparison into the 1 or 0 the set instructions store.
func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// lerp is RocketMath.Lerp, which clamps the interpolant rather than
// extrapolating. NaN passes both clamp comparisons unchanged and poisons the
// result.
func lerp(a, b, t float64) float64 {
	return a + (b-a)*clampDouble(t, 0, 1)
}

// clampDouble is Math.Clamp: NaN fails both comparisons and passes through.
func clampDouble(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

// sign is _SGN_Operation. NaN is neither greater nor less than zero, so it
// yields zero rather than propagating.
func sign(value float64) float64 {
	switch {
	case value > 0:
		return 1
	case value < 0:
		return -1
	default:
		return 0
	}
}

// shiftCount is how a shift distance reaches a 64 bit shift in C#: the low six
// bits only. A negative or oversized distance wraps rather than faulting, and
// reproducing that is also what keeps a fuzzed distance from panicking here,
// since Go would refuse a negative shift outright.
func shiftCount(distance int) uint { return uint(distance) & 63 }

// rotate is the shared body of _ROL_Operation and _ROR_Operation. The width is
// 54 bits, not the 53 that ext and ins cap at, and the distance is reduced into
// [0, 54) with a modulus that survives negative distances.
func rotate(value int64, distance int, left bool) int64 {
	value &= rotateMask
	distance = (distance%rotateBits + rotateBits) % rotateBits
	if left {
		return (value<<distance | value>>(rotateBits-distance)) & rotateMask
	}
	return (value>>distance | value<<(rotateBits-distance)) & rotateMask
}
