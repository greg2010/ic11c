package isel

import (
	"maps"
	"math"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/llvmir"
	"github.com/greg2010/ic11c/internal/valueflow"
	"tinygo.org/x/go-llvm"
)

// planInt32Range answers which values selection cannot show lie inside the
// signed 32-bit range, which is what a narrowed operand position needs and
// [planPlacement] does not say.
func planInt32Range(m llvm.Module) map[llvm.Value]bool {
	return newInt32Range().run(m)
}

// int32Range is one answer to that question in progress, holding the
// interval reading of every value the rules have asked about — recorded so
// [valueflow.Run]'s sweep-until-fixpoint can't unmark what an earlier
// sweep settled. A reading under a test is held per test, not per module,
// since one value can read as a different range under a different test.
type int32Range struct {
	module *guarded
	// budget is how many more values every reading together may visit. What it
	// caps is nested tests, whose readings the module's own cannot be shared
	// with.
	budget int
}

// maxRangeNodes is the budget one module's readings start with. Exhausting it
// leaves the values still unread to [int32Range.carriesInt32], which decides
// every value no reading covers anyway; it is set well clear of the operand
// graph a program that fits on the chip is written out of.
const maxRangeNodes = 1 << 16

func newInt32Range() *int32Range {
	return &int32Range{module: &guarded{read: make(map[llvm.Value]reading)}, budget: maxRangeNodes}
}

func (r *int32Range) run(m llvm.Module) map[llvm.Value]bool {
	rules := valueflow.Rules{Stops: r.int32Result, Carries: r.carriesInt32}
	return valueflow.Run(m, rules, valueflow.Seed{Values: unrangedConstants(m), Objects: openObjects(m)})
}

// guarded is one interval reading in progress: the values the tests enclosing
// it hold between two numbers, and what the reading has made of each value
// under them. The module's own reading is the one enclosed by no test.
type guarded struct {
	bounds map[llvm.Value]span
	read   map[llvm.Value]reading
}

// reading is what the walk made of one value: the range it computes, and
// whether the walk could read it at all.
type reading struct {
	reach span
	read  bool
}

// unrangedConstants are the constant operands the module holds that lie
// outside the signed 32-bit range, seeding the walk since [valueflow.Run]
// visits instructions, not constants. This lets a literal past the range
// be refused where written and carried where read.
func unrangedConstants(m llvm.Module) map[llvm.Value]bool {
	values := make(map[llvm.Value]bool)
	for _, in := range llvmir.ModuleInstrs(m) {
		for i := range in.OperandsCount() {
			operand := in.Operand(i)
			if operand.IsNil() {
				continue
			}
			if value, isNumber := constantNumber(operand); isNumber && !insideInt32(value) {
				values[operand] = true
			}
		}
	}
	return values
}

// int32Result reports whether v's result lies inside the signed 32-bit
// range regardless of its operands. Like [placedResult] but without the
// bitwise arm: the machine's bitwise conversion bounds by ±2^63, not this
// position's 2^31. Everything else falls to [int32Range.spanOf], then
// [int32Range.carriesInt32].
func (r *int32Range) int32Result(v llvm.Value) bool {
	// Default is the rule: only the two value types hold a value a machine
	// instruction narrows, so nothing else has a range to be inside of.
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
	// Default is the rule: an instruction not named here is left to the
	// reading, and one the reading cannot read to the operands it holds.
	//exhaustive:ignore
	switch v.InstructionOpcode() {
	case llvm.ICmp, llvm.FCmp, llvm.PtrToInt, llvm.ZExt, llvm.SExt:
		return true
	}
	reach, read := r.spanOf(v)
	return read && reach.insideInt32()
}

// carriesInt32 reports whether an instruction answers inside the signed
// 32-bit range exactly when its operands do — asked only for values
// [int32Range.spanOf] found no range for. This is a concession, not a fact:
// a phi is the one such shape, and what actually holds a loop counter in
// range is the loop's own exit test, which this reading doesn't model —
// refusing the concession would refuse every counted loop that names a
// batch per turn. srl is absent since it reads its value unsigned, masking
// the residue to 54 bits (so -1 arrives as 2^54-1).
func (r *int32Range) carriesInt32(in llvm.Value) bool {
	if _, read := r.spanOf(in); read {
		return false
	}
	// Default is the rule: an instruction not named here is a shape this stage
	// did not produce or an opening of its own, and both are refused.
	//exhaustive:ignore
	switch in.InstructionOpcode() {
	case llvm.Add, llvm.Sub, llvm.Mul, llvm.FAdd, llvm.FSub, llvm.FMul,
		llvm.And, llvm.Or, llvm.Xor, llvm.AShr,
		llvm.PHI, llvm.Select, llvm.SIToFP, llvm.UIToFP, llvm.FPToSI,
		opcodeFreeze, opcodeFNeg:
		return true
	case llvm.Shl:
		return in.OperandsCount() == 2 && r.shiftCarries(in.Operand(1))
	case llvm.Call:
		return r.rangePreservingCall(in)
	default:
		return false
	}
}

// shapingOps are the machine instructions that reshape a value without
// inventing or removing a number, mapped to what each does to a range. Read
// off [intrinsicForms] by opcode so the two fold spellings (llvm.fabs.f64,
// __ic_abs) can't disagree. min/max and sqrt/log/exp/trig are absent: both
// can turn a finite argument into a NaN or an infinity, one way or another.
var shapingOps = map[ic10.Opcode]func(span) span{
	isa.OpAbs:   absSpan,
	isa.OpTrunc: roundedSpan(math.Trunc, math.Trunc),
	isa.OpCeil:  roundedSpan(math.Ceil, math.Ceil),
	isa.OpFloor: roundedSpan(math.Floor, math.Floor),
	// Which way the machine breaks a tie is not settled here, so round is
	// bounded by the two neighbouring integers rather than applied. The
	// difference is a whole integer at either end of the range.
	isa.OpRound: roundedSpan(math.Floor, math.Ceil),
}

// scalingOps are the two-operand instructions whose result a range over
// each operand bounds. The left shift is the only one: its result is its
// left operand scaled by a power of two, where the rest of the bitwise
// family answers per bit (not being a conduit — see [int32Range.shiftCarries]).
var scalingOps = map[ic10.Opcode]func(left, right span) (span, bool){
	isa.OpSll: shiftedSpan,
}

// roundedSpan carries a span through a non-decreasing form, which maps an
// interval's ends to the result's ends.
func roundedSpan(low, high func(float64) float64) func(span) span {
	return func(reach span) span { return span{lo: low(reach.lo), hi: high(reach.hi)} }
}

// absSpan is the range a magnitude takes, which is not its ends' magnitudes
// wherever the argument straddles zero.
func absSpan(reach span) span {
	switch {
	case reach.lo >= 0:
		return reach
	case reach.hi <= 0:
		return span{lo: -reach.hi, hi: -reach.lo}
	default:
		return span{lo: 0, hi: math.Max(-reach.lo, reach.hi)}
	}
}

// rangePreservingCall reports whether a call to a declaration answers
// inside the signed 32-bit range exactly when its arguments do: the
// bitwise and shaping forms qualify for the reasons on [shapingOps] and
// [scalingOps]; the right shift qualifies because it keeps the low
// [distanceBits] bits of a value already conversion-bound to ±2^31.
func (r *int32Range) rangePreservingCall(in llvm.Value) bool {
	form, known := intrinsicFormOf(in)
	if !known {
		return false
	}
	if form.op == isa.OpSll {
		distance, filled := valueOperand(in, form, shiftDistance)
		return filled && r.shiftCarries(distance)
	}
	if _, isShaping := shapingOps[form.op]; isShaping {
		return true
	}
	return bitwiseIntrinsics[in.CalledValue().Name()]
}

// shiftDistance is which of a shift's value positions holds the distance. The
// machine reads the value it shifts first and the distance second, and
// [intrinsicForms] gives __ic_shl its arguments in that order.
const shiftDistance = 1

// valueOperand answers the operand a call fills the machine instruction's nth
// value position from, and reports false where the arguments do not reach it.
// Which arguments carry a value is [intrinsicForm.roles]' rather than the
// call's, an intrinsic carrying arguments the instruction has no operand for.
func valueOperand(in llvm.Value, form intrinsicForm, n int) (llvm.Value, bool) {
	arguments := min(len(form.roles), in.OperandsCount()-1)
	for i := range arguments {
		if form.roles[i] != roleValue {
			continue
		}
		if n == 0 {
			return in.Operand(i), true
		}
		n--
	}
	return llvm.Value{}, false
}

// shiftCarries reports whether a left shift whose own range this reading
// couldn't state may inherit its operands' status instead. A distance
// outside a reading's bound isn't a concession but a wrong answer: a
// distance of -24 scales by 2^40, not by the operand's own power, so
// there's nothing left for the operands' range to carry.
func (r *int32Range) shiftCarries(distance llvm.Value) bool {
	reach, read := r.spanOf(distance)
	return !read || shiftsBy(reach)
}

// selectSpan is the range a select computes — the one shape here that
// reads its condition. It exists so the narrowed-operand refusal's advice
// is advice this stage actually accepts: the taken arm is read under the
// condition's bounds as a range, not a yes/no, since a magnitude the
// narrowing doesn't carry is as much a miss as a NaN.
func (r *int32Range) selectSpan(v llvm.Value, g *guarded) (span, bool) {
	taken := g
	if stated := r.collectBounds(v.Operand(0)); len(stated) > 0 {
		taken = &guarded{bounds: tightest(g.bounds, stated), read: make(map[llvm.Value]reading)}
	}
	admitted, read := r.spanFrom(v.Operand(1), taken)
	if !read {
		return span{}, false
	}
	excluded, read := r.spanFrom(v.Operand(2), g)
	if !read {
		return span{}, false
	}
	return hull(admitted, excluded), true
}

// tightest is what a select's condition adds to the bounds already in force.
// Both tests hold on the arm the condition admits, so a value they both name is
// held to the narrower of the two ranges.
func tightest(held, stated map[llvm.Value]span) map[llvm.Value]span {
	if len(held) == 0 {
		return stated
	}
	bounds := make(map[llvm.Value]span, len(held)+len(stated))
	maps.Copy(bounds, held)
	for v, reach := range stated {
		if outer, both := bounds[v]; both {
			reach = span{lo: math.Max(outer.lo, reach.lo), hi: math.Min(outer.hi, reach.hi)}
		}
		bounds[v] = reach
	}
	return bounds
}

// span is a closed range of numbers a value is known to lie in, and is how much
// a condition states about the arm it governs.
type span struct{ lo, hi float64 }

// insideInt32 reports whether every number the span admits reaches a narrowed
// operand as itself. It is false for a span an overflow left non-finite, since
// no comparison against an end of the range holds for one.
func (s span) insideInt32() bool { return insideInt32(s.lo) && insideInt32(s.hi) }

// bound is one end a condition pins a value to. stated is what says limit means
// anything: an end no comparison named is not an end at zero.
type bound struct {
	limit  float64
	stated bool
}

// sides records the ends a condition pins a value to. A value pinned on both is
// a number wherever the condition holds, an ordered comparison being false for a
// NaN and each bound excluding the infinity on its own side; whether the
// narrowing carries it through as itself is what the two limits answer.
type sides struct{ lower, upper bound }

// collectBounds reads a condition for the values it holds between two finite
// constants, and answers the span each is held to. An and of two conditions is
// the asymmetric spelling and contributes both sides; a comparison against a
// magnitude is the symmetric one, |v| < c being -c < v < c.
func (r *int32Range) collectBounds(cond llvm.Value) map[llvm.Value]span {
	pinned := make(map[llvm.Value]sides)
	r.readBounds(cond, pinned, make(map[llvm.Value]bool))
	bounded := make(map[llvm.Value]span, len(pinned))
	for v, side := range pinned {
		if side.lower.stated && side.upper.stated {
			bounded[v] = span{lo: side.lower.limit, hi: side.upper.limit}
		}
	}
	return bounded
}

// readBounds adds what one condition pins to what its enclosing conjunction
// already pinned. A condition read twice pins nothing the first reading did
// not, so read is what keeps a conjunction the optimizer left as a DAG from
// being covered once per path through it.
func (r *int32Range) readBounds(cond llvm.Value, pinned map[llvm.Value]sides, read map[llvm.Value]bool) {
	if read[cond] || r.budget <= 0 || cond.IsAInstruction().IsNil() {
		return
	}
	read[cond] = true
	r.budget--
	// Default is the rule: a condition with no arm here pins nothing, which
	// leaves the select to whatever its operands hold.
	//exhaustive:ignore
	switch cond.InstructionOpcode() {
	case llvm.And:
		r.readBounds(cond.Operand(0), pinned, read)
		r.readBounds(cond.Operand(1), pinned, read)
	case llvm.Select:
		// A conjunction of more than two comparisons reaches here as a select
		// over truth values rather than as an and, and is a conjunction only
		// where the arm the test fails on is false. The same shape with a true
		// arm is the disjunction, which pins nothing.
		if isFalse(cond.Operand(2)) {
			r.readBounds(cond.Operand(0), pinned, read)
			r.readBounds(cond.Operand(1), pinned, read)
		}
	case llvm.FCmp:
		readComparisonBound(cond, pinned)
	}
}

// isFalse reports whether a value is the truth value a conjunction's failing
// arm holds.
func isFalse(v llvm.Value) bool {
	if v.Type().TypeKind() != llvm.IntegerTypeKind || v.Type().IntTypeWidth() != predicateWidth {
		return false
	}
	value, isNumber := constantNumber(v)
	return isNumber && value == 0
}

// upperBound names the ordered comparisons that state a bound at all, and which
// end of its left operand each one states. Only the ordered predicates are here:
// an unordered one holds for a NaN, so a value it admits is not a number and the
// bound it appears to state is not one.
var upperBound = map[llvm.FloatPredicate]bool{
	llvm.FloatOLT: true,
	llvm.FloatOLE: true,
	llvm.FloatOGT: false,
	llvm.FloatOGE: false,
}

// strictBound names the two ordered predicates a value may not equal its bound
// under. The difference is a whole integer at the ends of the range, and the
// advice this stage gives rests on it: 2^31 is not a signed 32-bit integer, so a
// value held strictly under it narrows as itself where one held at it does not.
var strictBound = map[llvm.FloatPredicate]bool{
	llvm.FloatOLT: true,
	llvm.FloatOGT: true,
}

// readComparisonBound reads one ordered comparison against a finite constant.
func readComparisonBound(cmp llvm.Value, pinned map[llvm.Value]sides) {
	above, stated := upperBound[cmp.FloatPredicate()]
	if !stated {
		return
	}
	strict := strictBound[cmp.FloatPredicate()]
	left, right := cmp.Operand(0), cmp.Operand(1)
	value := left
	limit, isConstant := finiteConstant(right)
	if !isConstant {
		// The constant is on the left, which puts the value on the right and
		// reads the comparison the other way round.
		if limit, isConstant = finiteConstant(left); !isConstant {
			return
		}
		value, above = right, !above
	}
	if magnitude, isMagnitude := magnitudeOf(value); isMagnitude {
		// A magnitude held below a finite constant bounds its argument at both
		// ends. Held above one it bounds nothing, since every large value and
		// both infinities satisfy it.
		if above {
			under := largestUnder(limit, strict)
			pinUpper(pinned, magnitude, under)
			pinLower(pinned, magnitude, -under)
		}
		return
	}
	if above {
		pinUpper(pinned, value, largestUnder(limit, strict))
		return
	}
	pinLower(pinned, value, smallestOver(limit, strict))
}

// largestUnder is the largest number a value held under limit can be, and
// smallestOver the smallest one a value held over it can be. A strict
// comparison excludes the limit itself, which is one ulp.
func largestUnder(limit float64, strict bool) float64 {
	if strict {
		return math.Nextafter(limit, math.Inf(-1))
	}
	return limit
}

func smallestOver(limit float64, strict bool) float64 {
	if strict {
		return math.Nextafter(limit, math.Inf(1))
	}
	return limit
}

// pinUpper records that value is held at or under limit, and pinLower that it
// is held at or over one. Two conditions naming the same end both hold, so the
// tighter of the two is kept.
func pinUpper(pinned map[llvm.Value]sides, value llvm.Value, limit float64) {
	side := pinned[value]
	if !side.upper.stated || limit < side.upper.limit {
		side.upper = bound{limit: limit, stated: true}
	}
	pinned[value] = side
}

func pinLower(pinned map[llvm.Value]sides, value llvm.Value, limit float64) {
	side := pinned[value]
	if !side.lower.stated || limit > side.lower.limit {
		side.lower = bound{limit: limit, stated: true}
	}
	pinned[value] = side
}

// magnitudeOf answers the argument of a call to the machine's abs, which is what
// a symmetric range test folds to.
func magnitudeOf(v llvm.Value) (llvm.Value, bool) {
	if v.IsAInstruction().IsNil() || v.InstructionOpcode() != llvm.Call {
		return llvm.Value{}, false
	}
	form, known := intrinsicFormOf(v)
	if !known || form.op != isa.OpAbs {
		return llvm.Value{}, false
	}
	return v.Operand(0), true
}

// spanOf answers the range a value computes with no test enclosing it, which is
// the question both of this walk's rules ask.
func (r *int32Range) spanOf(v llvm.Value) (span, bool) {
	return r.spanFrom(v, r.module)
}

// spanFrom answers the range a value computes out of the values g holds
// bounded and out of constants, falling through to the per-opcode rules
// below. Arithmetic is read as a range, not a yes/no, refusing a post-test
// addition that moves a value back out of range.
func (r *int32Range) spanFrom(v llvm.Value, g *guarded) (span, bool) {
	if got, done := g.read[v]; done {
		return got.reach, got.read
	}
	if r.budget <= 0 {
		return span{}, false
	}
	r.budget--
	// Recorded before the operands are read rather than after, so that an
	// operand graph with a cycle in it — which is not one this compiler emits,
	// since none of the shapes below reads a phi — is read as a value this says
	// nothing about instead of being followed round.
	g.read[v] = reading{}
	reach, read := r.readSpan(v, g)
	g.read[v] = reading{reach: reach, read: read}
	return reach, read
}

func (r *int32Range) readSpan(v llvm.Value, g *guarded) (span, bool) {
	if reach, isBounded := g.bounds[v]; isBounded {
		return reach, true
	}
	if value, isConstant := finiteConstant(v); isConstant {
		return span{lo: value, hi: value}, true
	}
	if v.IsAInstruction().IsNil() {
		return span{}, false
	}
	// Default is the rule: a value this cannot read is one it says nothing
	// about, and saying nothing is what leaves the operands to decide.
	//exhaustive:ignore
	switch v.InstructionOpcode() {
	case llvm.FAdd:
		return r.joinSpans(v, g, sumSpan)
	case llvm.FSub:
		return r.joinSpans(v, g, differenceSpan)
	case llvm.FMul:
		return r.joinSpans(v, g, productSpan)
	case llvm.FDiv:
		return r.joinSpans(v, g, quotientSpan)
	case llvm.Shl:
		// The optimizer's own spelling of the shift [scalingOps] names. A fold
		// reaches selection as one or the other — a value doubled by a multiply
		// as this, a source-written shift as the call — so covering one only
		// would refuse a program for how it was folded.
		return r.joinSpans(v, g, shiftedSpan)
	case llvm.SIToFP, llvm.UIToFP, llvm.FPToSI, opcodeFreeze:
		return r.spanFrom(v.Operand(0), g)
	case opcodeFNeg:
		reach, read := r.spanFrom(v.Operand(0), g)
		if !read {
			return span{}, false
		}
		return negatedSpan(reach), true
	case llvm.Select:
		return r.selectSpan(v, g)
	case llvm.Call:
		return r.shapedSpan(v, g)
	default:
		return span{}, false
	}
}

// joinSpans reads both operands of a two-operand instruction and puts their
// ranges through join, which reports false where the operands' own ranges leave
// the result unbounded.
func (r *int32Range) joinSpans(v llvm.Value, g *guarded, join func(left, right span) (span, bool)) (span, bool) {
	left, read := r.spanFrom(v.Operand(0), g)
	if !read {
		return span{}, false
	}
	right, read := r.spanFrom(v.Operand(1), g)
	if !read {
		return span{}, false
	}
	return join(left, right)
}

// shapedSpan carries the ranges of a call's value arguments through the
// machine instruction it stands for. The right-shift half of
// [int32Range.rangePreservingCall] is deliberately outside both
// [shapingOps] and [scalingOps]: its conversion answers a NaN with zero, so
// reading it as a range would be wrong.
func (r *int32Range) shapedSpan(v llvm.Value, g *guarded) (span, bool) {
	form, known := intrinsicFormOf(v)
	if !known {
		return span{}, false
	}
	reach, read := r.argumentSpans(v, form, g)
	if !read {
		return span{}, false
	}
	if shape, isShaping := shapingOps[form.op]; isShaping && len(reach) == 1 {
		return shape(reach[0]), true
	}
	if scale, isScaling := scalingOps[form.op]; isScaling && len(reach) == 2 {
		return scale(reach[0], reach[1])
	}
	return span{}, false
}

// argumentSpans reads the range of every operand a call fills the machine
// instruction's value positions from, in the order the instruction reads them,
// and reports false where any of them is a value the walk says nothing about.
func (r *int32Range) argumentSpans(v llvm.Value, form intrinsicForm, g *guarded) ([]span, bool) {
	// The callee is the last operand and is not one the call was handed.
	arguments := min(len(form.roles), v.OperandsCount()-1)
	reach := make([]span, 0, arguments)
	for i := range arguments {
		if form.roles[i] != roleValue {
			continue
		}
		argument, read := r.spanFrom(v.Operand(i), g)
		if !read {
			return nil, false
		}
		reach = append(reach, argument)
	}
	return reach, true
}

// intrinsicFormOf answers the machine instruction a call selects, and reports
// false for a call to anything [intrinsicForms] does not name.
func intrinsicFormOf(v llvm.Value) (intrinsicForm, bool) {
	callee := v.CalledValue()
	if callee.IsNil() {
		return intrinsicForm{}, false
	}
	form, known := intrinsicForms[callee.Name()]
	return form, known
}

// sumSpan, and every interval form below it, answers a range holding every value
// the operation takes over its operands' ranges, and none narrows: an end an
// overflow left non-finite, and the NaN a zero times an infinity leaves, both
// reach [span.insideInt32] as themselves and are refused there.
func sumSpan(left, right span) (span, bool) {
	return span{lo: left.lo + right.lo, hi: left.hi + right.hi}, true
}

func differenceSpan(left, right span) (span, bool) {
	return sumSpan(left, negatedSpan(right))
}

func negatedSpan(reach span) span { return span{lo: -reach.hi, hi: -reach.lo} }

func productSpan(left, right span) (span, bool) {
	return cornerSpan(left, right, func(a, b float64) float64 { return a * b }), true
}

// quotientSpan reports false for a divisor whose range holds zero, which is the
// reason the divisions are no conduit: a zero divisor answers with an infinity
// and a zero over zero with a NaN, out of operands that were numbers. A divisor
// the reading holds clear of zero has neither.
func quotientSpan(left, right span) (span, bool) {
	if !(right.lo > 0 || right.hi < 0) {
		return span{}, false
	}
	return cornerSpan(left, right, func(a, b float64) float64 { return a / b }), true
}

// shiftedSpan is the range a left shift takes: its left operand scaled by a
// power of two, widened to the integers either side of the distance's own
// ends since the tie-break direction isn't settled here. It reports false
// outside 0 through [maxDistance]: the machine masks the distance to its
// low [distanceBits] bits there, so e.g. -24 shifts left by 40, not by a
// negative amount.
func shiftedSpan(left, right span) (span, bool) {
	if !shiftsBy(right) {
		return span{}, false
	}
	scale := span{lo: math.Exp2(math.Floor(right.lo)), hi: math.Exp2(math.Ceil(right.hi))}
	return productSpan(left, scale)
}

// shiftsBy reports whether every distance a range admits is one the machine
// shifts left by as written, which is what makes the power of the distance the
// scale. It is false for a range an overflow or a NaN left non-finite, since
// neither end of the window holds for one.
func shiftsBy(distance span) bool {
	return math.Floor(distance.lo) >= 0 && math.Ceil(distance.hi) <= maxDistance
}

// cornerSpan is the range a two-operand form takes where its extremes are at
// its operands' ends, which the product and the quotient both are.
func cornerSpan(left, right span, apply func(a, b float64) float64) span {
	lowLow, lowHigh := apply(left.lo, right.lo), apply(left.lo, right.hi)
	highLow, highHigh := apply(left.hi, right.lo), apply(left.hi, right.hi)
	return span{
		lo: math.Min(math.Min(lowLow, lowHigh), math.Min(highLow, highHigh)),
		hi: math.Max(math.Max(lowLow, lowHigh), math.Max(highLow, highHigh)),
	}
}

// hull is the range holding both of a select's arms.
func hull(left, right span) span {
	return span{lo: math.Min(left.lo, right.lo), hi: math.Max(left.hi, right.hi)}
}

// finiteConstant answers the number a constant stands for, and reports false for
// a value that is not a constant and for one standing for no number: LLVM's own
// folding of a division by zero writes an infinity or a NaN into one, and
// neither bounds anything or reaches a narrowed operand as itself.
func finiteConstant(v llvm.Value) (float64, bool) {
	value, isNumber := constantNumber(v)
	if !isNumber || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, false
	}
	return value, true
}

// constantNumber answers the number a constant stands for, non-finite ones
// included, or false for a non-constant. An integer constant reads as
// signed whatever its width: an i64 in this module carries whatever double
// it was written from, and the two spellings of one number must answer
// alike.
func constantNumber(v llvm.Value) (float64, bool) {
	if !v.IsAConstantInt().IsNil() {
		return float64(v.SExtValue()), true
	}
	if v.IsAConstantFP().IsNil() {
		return 0, false
	}
	value, _ := v.DoubleValue()
	return value, true
}

// The ends of the signed 32-bit range every narrowed operand position
// converts into. Outside it — a NaN and both infinities included — the
// conversion answers some integer the program never computed. Which one is
// measured, not specified: the game runs Mono on x86-64, where conv.i4
// lowers to cvttsd2si, answering the smallest int32 for every case.
const (
	minInt32 = -2147483648.0
	maxInt32 = 2147483647.0
)

// insideInt32 reports whether a value the chip narrows arrives as itself. It is
// false for a NaN, which compares false against either end.
func insideInt32(value float64) bool {
	return value >= minInt32 && value <= maxInt32
}
