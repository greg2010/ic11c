package main

import (
	"fmt"
	"math"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
)

// genMaxMagnitude is the ceiling every long long in a generated program is
// held under. The machine counts consecutively to 2^53 and stops; past that
// the emitted program and the native build compute different numbers for a
// reason belonging to neither compiler.
const genMaxMagnitude = 1 << 40

// genIntCeiling bounds a written literal, and with it every fold of two
// literals: C types a bare decimal literal as int, and MicroC refuses a
// fold whose answer depends on that type, so a product of two literals has
// to stay inside 32 bits to be read the same way by both languages.
const genIntCeiling = 1000000

// genClampMagnitude is where the generated cast helper stops. A double outside
// it, an infinity and a NaN included, converts to no long long C defines, so
// the cast is guarded by a comparison rather than by hope.
const genClampMagnitude = 1000000

// genMaxNest bounds how deep one expression nests. A leaf may be a
// subscript and a subscript contains an expression, so the grammar does
// not bound itself.
const genMaxNest = 20

// genStream keeps the two halves of the PCG seed apart, which a generator
// seeded from a counter would otherwise repeat.
const genStream = 0x9e3779b97f4a7c15

// genArrayLengths are the element counts an array is declared with. Each is a
// power of two, so a computed subscript is brought into range by a mask, which
// is in range for every long long the generator can produce and needs no
// clamping statement around it.
var genArrayLengths = []int{8, 16, 32}

// genPointerLength is the length of an array a pointer parameter may receive.
// An array parameter decays and carries no bound, so the body masks with one
// length and every argument has to have it.
const genPointerLength = 16

// genArrayModulus is the modulus every long long array is held under. One
// modulus rather than one per array, since an array reaches a function
// through a decayed parameter that carries no bound and no identity.
const genArrayModulus = 65537

// genModuli are the moduli a long long object is declared under. Most are
// wide: a range narrow enough to fit an int is one the optimizer retypes to
// a form the backend refuses. The narrow ones stay, since a 0-or-1 value is
// the family four instruction-selection defects came out of.
var genModuli = []int64{101, 65537, 1 << 30, 1 << 34, 1 << 38}

// genMasks are the masks a production narrows an operand with, for the same
// reason and with the same split.
var genMasks = []int64{255, 65535, 1<<24 - 1, 1<<30 - 1}

// genLogicTypes are the logic properties a generated program reads and
// writes, each the spelling its own position resolves — checked, not
// trusted, by TestGeneratedOperandNamesResolveAlike. Deprecated names are
// left out so a native build reports nothing.
var genLogicTypes = []string{
	"Power", "Open", "Mode", "Error", "Pressure", "Temperature",
	"PressureExternal", "PressureInternal", "Activate", "Lock", "Charge",
	"Setting", "RatioOxygen", "RatioCarbonDioxide", "Horizontal", "Vertical",
	"SolarAngle", "Maximum", "Ratio", "PowerPotential", "PowerActual",
	"Quantity", "On", "RequiredPower", "Idle", "Color",
}

// genSlotTypes are the slot properties a generated program reads and
// writes. The prefixed spellings are here on purpose: a slot type whose
// bare name the logic family claims is written SlotType_ in this position
// and nowhere else.
var genSlotTypes = []string{
	"Occupied", "OccupantHash", "Damage", "Efficiency", "Health", "Growth",
	"ChargeRatio", "Class", "PressureWaste", "PressureAir", "MaxQuantity",
	"Mature", "Seeding", "SortingClass", "FilterType", "HarvestedHash",
	"MaturityRatio", "SeedingRatio", "FreeSlots", "TotalSlots",
	"SlotType_Quantity", "SlotType_Pressure", "SlotType_Temperature",
	"SlotType_Charge", "SlotType_PrefabHash", "SlotType_LineNumber",
	"SlotType_Volume", "SlotType_Open", "SlotType_On", "SlotType_Lock",
	"SlotType_ReferenceId", "SlotType_Mode",
}

// genBatchModes and genReagentModes are the aggregate and reagent views, each
// under the spelling its own position resolves.
var (
	genBatchModes   = []string{"Average", "Sum", "Count", "BatchMode_Minimum", "BatchMode_Maximum"}
	genReagentModes = []string{"Contents", "Required", "Recipe", "TotalContents"}
)

// genSlots are the slot indices a slot intrinsic names. The index has to be an
// integer constant expression, so it is a literal wherever it appears.
var genSlots = []int{0, 1, 2, 3}

// genMatchedPrefab is the structure name the generated world puts on a
// device, and genEmptyPrefab is one it puts nowhere: a batch read over the
// second matches nothing, which is where the machine answers NaN under
// Average, reaching the program through the world rather than arithmetic.
const (
	genMatchedPrefab = "StructureGasSensor"
	genEmptyPrefab   = "StructureNothingIsHere"
	genMatchedName   = "alpha"
	genReagent       = "Copper"
)

// genDevices are the pins a generated program names. db is absent: it is the
// housing rather than a device, a write to it moves the program counter instead
// of reaching a device, and no write to it appears in a trace.
var (
	genReadDevices  = []string{"in", "aux"}
	genWriteDevices = []string{"out", "sink"}
)

// genReadPins are the housing pins genReadDevices name, which is what the world
// has to seed for a read to answer anything but zero.
var genReadPins = []int{0, 2}

// genDoubleLiterals are the double operands a generated program writes.
// Fractions, a zero of each sign, and magnitudes far enough apart that a sum
// loses the smaller one.
var genDoubleLiterals = []string{
	"0.0", "-0.0", "1.0", "-1.0", "0.5", "-0.25", "2.0", "3.0", "-7.0",
	"0.1", "293.15", "-273.15", "1e9", "-1e-9", "9007199254740992.0",
	"1.0000000000000002", "6.283185307179586",
}

// genUnaryMachine and the wider arities are the machine's own functions,
// each a source of a non-finite value somewhere in its domain and answered
// by the interpreter on both sides, so a comparison establishes that the
// program reached the right function with the right operands.
var (
	genUnaryMachine   = []string{"sqrt", "abs", "sgn", "round", "trunc", "ceil", "floor", "log", "exp", "sin", "cos", "tan", "asin", "acos", "atan"}
	genBinaryMachine  = []string{"min", "max", "pow", "atan2"}
	genTernaryMachine = []string{"clamp", "lerp"}
)

// The construct set a campaign is held to, in both directions: a name reached
// that is not declared here fails, and a name declared here that a corpus never
// reaches fails too. A generator quietly losing a shape is the way a fuzzer
// stops finding anything while still passing.
const (
	conIntArith         = "integer add, subtract, multiply"
	conIntDivide        = "integer divide and remainder"
	conIntBitwise       = "integer and, or, exclusive or"
	conIntShift         = "integer shift"
	conIntUnary         = "integer negate and complement"
	conIntSelect        = "integer conditional"
	conIntLiteralFold   = "integer literal"
	conDoubleArith      = "double arithmetic"
	conDoubleByZero     = "double division whose divisor can be zero"
	conDoubleSelect     = "double conditional"
	conCastToInt        = "guarded double to long long cast"
	conCastToDouble     = "long long or bool to double cast"
	conCastToBool       = "double to bool cast"
	conBoolCompareInt   = "long long comparison"
	conBoolCompareDbl   = "double comparison"
	conBoolCompareBool  = "bool equality"
	conBoolComparePtr   = "pointer comparison"
	conBoolLogic        = "not, and, or"
	conBoolIsNaN        = "__ic_isnan"
	conBoolPresent      = "__ic_device_present"
	conBoolToInt        = "bool widened to long long"
	conArrayRead        = "array read at a computed subscript"
	conArrayWrite       = "array write at a computed subscript"
	conPointerTake      = "address of an element"
	conPointerStep      = "pointer arithmetic"
	conPointerDiff      = "pointer difference"
	conPointerDeref     = "read through a pointer"
	conPointerStore     = "write through a pointer"
	conPointerStep1     = "pointer increment"
	conLoadLogic        = "__ic_load"
	conLoadSlot         = "__ic_load_slot"
	conLoadBatch        = "__ic_load_batch"
	conLoadBatchNamed   = "__ic_load_batch_named"
	conLoadBatchSlot    = "__ic_load_batch_slot"
	conLoadReagent      = "__ic_load_reagent"
	conStoreLogic       = "__ic_store"
	conStoreSlot        = "__ic_store_slot"
	conStoreBatch       = "__ic_store_batch"
	conHash             = "__ic_hash"
	conMachine1         = "one argument machine function"
	conMachine2         = "two argument machine function"
	conMachine3         = "three argument machine function"
	conIf               = "if"
	conIfElse           = "if with an else"
	conElseIf           = "else if chain"
	conFor              = "for loop"
	conWhile            = "while loop"
	conDoWhile          = "do while loop"
	conBreak            = "break"
	conContinue         = "continue"
	conSwitch           = "switch"
	conSwitchFall       = "switch arm stacked on the one below"
	conCompoundAssign   = "compound assignment"
	conIncrement        = "increment and decrement"
	conCall             = "call a generated function"
	conCallVoid         = "call a generated function for its effect"
	conCallInLoop       = "call inside a loop body"
	conCallOnArm        = "call on a branch arm"
	conCallNested       = "a generated function whose own body calls one"
	conParamPointer     = "pointer parameter"
	conParamArray       = "array parameter"
	conParamDev         = "device parameter"
	conGlobalInt        = "long long global"
	conGlobalDouble     = "double global"
	conGlobalBool       = "bool global"
	conGlobalIntArr     = "long long array"
	conGlobalDblArr     = "double array"
	conGlobalBoolArr    = "bool array"
	conConstexpr        = "constexpr object"
	conConditionalWrite = "device write under a condition"
)

// genConstructs is that set. Adding a shape to the generator without adding it
// here fails, which is what keeps the declaration from drifting behind the
// generator.
var genConstructs = []string{
	conIntArith, conIntDivide, conIntBitwise, conIntShift, conIntUnary,
	conIntSelect, conIntLiteralFold, conDoubleArith, conDoubleByZero,
	conDoubleSelect, conCastToInt, conCastToDouble, conCastToBool,
	conBoolCompareInt, conBoolCompareDbl, conBoolCompareBool, conBoolComparePtr,
	conBoolLogic, conBoolIsNaN, conBoolPresent, conBoolToInt,
	conArrayRead, conArrayWrite, conPointerTake, conPointerStep, conPointerDiff,
	conPointerDeref, conPointerStore, conPointerStep1,
	conLoadLogic, conLoadSlot, conLoadBatch, conLoadBatchNamed, conLoadBatchSlot,
	conLoadReagent, conStoreLogic, conStoreSlot, conStoreBatch, conHash,
	conMachine1, conMachine2, conMachine3,
	conIf, conIfElse, conElseIf, conFor, conWhile, conDoWhile, conBreak,
	conContinue, conSwitch, conSwitchFall, conCompoundAssign, conIncrement,
	conCall, conCallVoid, conCallInLoop, conCallOnArm, conCallNested,
	conParamPointer, conParamArray, conParamDev,
	conGlobalInt, conGlobalDouble, conGlobalBool, conGlobalIntArr,
	conGlobalDblArr, conGlobalBoolArr, conConstexpr, conConditionalWrite,
}

// genKind is the type of a generated value.
type genKind int

const (
	genInt genKind = iota
	genDouble
	genBool
	genPointer
	genVoid
)

func (k genKind) spelling() string {
	switch k {
	case genInt:
		return "long long"
	case genDouble:
		return "double"
	case genBool:
		return "bool"
	case genVoid:
		return "void"
	case genPointer:
		return "long long *"
	}
	return "long long *"
}

// genExpr is one generated expression and what the generator has to know about
// it to keep building inside the comparable domain.
type genExpr struct {
	text string
	kind genKind
	// bound is the largest magnitude a genInt expression can hold. Every
	// production computes it from its operands, and nothing is admitted whose
	// bound passes genMaxMagnitude.
	bound int64
	// literal reports that the whole expression folds. Two of them never meet
	// under an operator, because C would fold the pair in the type of its
	// literals and MicroC refuses a fold whose answer depends on that.
	literal bool
	// memory reports that the expression is a read of the data region. Two of
	// them never become the arms of a conditional: the optimizer sinks the pair
	// under it into one read of a selected address, which designates either of
	// two objects where the source designated one, and the backend refuses that.
	memory bool
}

// genVar is one object in scope.
type genVar struct {
	name string
	kind genKind
	// modulus reduces every store to a long long object, so what it holds stays
	// under it whatever a loop or a further turn of the control loop does.
	modulus int64
	// length is an array's element count, or zero for a scalar.
	length int
	// element is the kind an array holds.
	element genKind
	// global marks an object in the data region, whose every read is a load.
	global bool
	// constant marks a constexpr object, whose every reference is the value
	// itself. Two of them never meet under a double operator: the fold can
	// produce a NaN or infinity the chip has no literal for, which the
	// compiler refuses to emit — a refusal rather than a finding.
	constant bool
	// readOnly marks a loop counter or a constexpr object, which nothing
	// assigns to: a counter the body wrote would stop the loop terminating, and
	// a constexpr object is not assignable at all.
	readOnly bool
	// object is the array a pointer variable designates.
	object *genVar
}

// genParam is one parameter of a generated function.
type genParam struct {
	name string
	kind genKind
	// modulus is the parameter's own bound. The call site reduces its argument
	// by it, which is what lets the body treat the parameter as bounded.
	modulus int64
	// arraySyntax renders a pointer parameter as an unbounded array, which is
	// the decay spelling.
	arraySyntax bool
	// dev marks a device parameter, which takes a pin name at the call site.
	dev bool
}

// genFunc is one generated function's interface.
type genFunc struct {
	name   string
	result genKind
	// modulus bounds an integer result, which every return statement reduces by.
	modulus int64
	params  []genParam
	// device is the pin every call passes to the function's device
	// parameter. One pin per function, not one per site: two sites passing
	// different pins is the shape the optimizer merges into one store with
	// a selected device operand, which the backend refuses.
	device string
}

// genNode is one statement in a generated program, and the unit shrinking
// removes. A node is removed whole, so a construct whose parts hold each
// other up — a temporary and the compound assignments that narrow it — is
// one node rather than several.
type genNode struct {
	// head opens a block. Several lines are permitted, because a counter
	// declaration and the loop reading it are one node.
	head []string
	body []genNode
	// mid closes the first block and opens the second, as "} else {" does.
	mid []string
	alt []genNode
	// tail closes the block.
	tail []string
	// text is a statement, or several only valid together.
	text []string
	// defines are the names the node introduces. It is removable only while
	// nothing else in the program names one of them.
	defines []string
	// required marks a node the program is not a program without: the yield its
	// control loop turns on, and the step that ends a counted loop.
	required bool
	// terminates marks a break or a continue, after which the block it stands
	// in ends. IR generation emits a statement following one into a block it has
	// already terminated and produces a module that does not verify, so a
	// generated program never writes one.
	terminates bool
}

// generated is one program, in the form shrinking works on.
type generated struct {
	seed    uint64
	globals []genNode
	funcs   []genNode
	body    []genNode
	// constructs is what this program reached, which a campaign tallies.
	constructs []string
}

// microcGen builds one program.
type microcGen struct {
	rng        *rand.Rand
	constructs map[string]bool
	scope      []*genVar
	arrays     []*genVar
	funcs      []*genFunc
	seq        int
	seed       uint64
	// nest is the expression nesting a production is at.
	nest int
	// calls counts the call expressions written so far. A production brackets
	// the block it builds with it to tell a call in a loop from one beside
	// it — a distinction the emitted text no longer carries.
	calls int
	// unknown collects a construct name the declaration does not carry, which
	// the coverage test reports rather than the generator deciding about.
	unknown map[string]bool
}

func newMicrocGen(seed uint64) *microcGen {
	return &microcGen{
		seed:       seed,
		rng:        rand.New(rand.NewPCG(seed, seed^genStream)),
		constructs: make(map[string]bool),
		unknown:    make(map[string]bool),
	}
}

var genDeclared = func() map[string]bool {
	set := make(map[string]bool, len(genConstructs))
	for _, name := range genConstructs {
		set[name] = true
	}
	return set
}()

// reached records that the program built one construct.
func (g *microcGen) reached(name string) {
	if !genDeclared[name] {
		g.unknown[name] = true
	}
	g.constructs[name] = true
}

// descend enters one level of expression nesting, reporting whether the
// generator is still above the floor. Every production that can reach a leaf
// calls it.
func (g *microcGen) descend() bool {
	g.nest++
	return g.nest <= genMaxNest
}

func (g *microcGen) ascend() { g.nest-- }

func (g *microcGen) name(prefix string) string {
	g.seq++
	return prefix + strconv.Itoa(g.seq)
}

func genPick[T any](rng *rand.Rand, values []T) T { return values[rng.IntN(len(values))] }

func (g *microcGen) pickLogic() string    { return genPick(g.rng, genLogicTypes) }
func (g *microcGen) pickSlotType() string { return genPick(g.rng, genSlotTypes) }
func (g *microcGen) readDevice() string   { return genPick(g.rng, genReadDevices) }
func (g *microcGen) writeDevice() string  { return genPick(g.rng, genWriteDevices) }

// boundAdd and boundMul saturate rather than wrapping, so a production whose
// bound overflows is rejected instead of appearing admissible.
func boundAdd(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func boundMul(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// scopeMark and scopeReset bracket a block, so a name declared inside one is
// out of scope after it.
func (g *microcGen) scopeMark() int      { return len(g.scope) }
func (g *microcGen) scopeReset(mark int) { g.scope = g.scope[:mark] }

// vars returns every object in scope of one kind that a caller may read.
func (g *microcGen) vars(kind genKind) []*genVar {
	var out []*genVar
	for _, v := range g.scope {
		if v.kind == kind && v.length == 0 {
			out = append(out, v)
		}
	}
	return out
}

// assignable returns the scalars a statement may store to.
func (g *microcGen) assignable(kind genKind) []*genVar {
	var out []*genVar
	for _, v := range g.vars(kind) {
		if !v.readOnly {
			out = append(out, v)
		}
	}
	return out
}

// arraysOf returns the arrays of one element kind in scope.
func (g *microcGen) arraysOf(element genKind) []*genVar {
	var out []*genVar
	for _, v := range g.arrays {
		if v.element == element {
			out = append(out, v)
		}
	}
	return out
}

// index renders a subscript that is in range for every long long a
// generated program can produce. The mask is the whole of the range check:
// an array length is a power of two, and a mask by one less lands inside
// it for a negative operand as much as a positive one.
func (g *microcGen) index(depth int, length int) string {
	raw := g.intExpr(depth, genMaxMagnitude)
	if raw.literal {
		raw = g.nonLiteralInt(genMaxMagnitude)
	}
	return fmt.Sprintf("(%s) & %d", raw.text, length-1)
}

// intLiteral is a written integer operand.
func (g *microcGen) intLiteral(ceiling int64) genExpr {
	g.reached(conIntLiteralFold)
	if ceiling > genIntCeiling {
		ceiling = genIntCeiling
	}
	if ceiling < 1 {
		ceiling = 1
	}
	v := g.rng.Int64N(ceiling + 1)
	if g.rng.IntN(2) == 0 {
		v = -v
	}
	return genExpr{text: strconv.FormatInt(v, 10), kind: genInt, bound: absInt64(v), literal: true}
}

// reading is the integer source every scope has: a device reading put
// through the guarded cast, used when a production needs an operand that
// does not fold. A mask, not a remainder, brings it under a caller's
// ceiling, landing inside it for a negative reading as much as a positive one.
func (g *microcGen) reading(ceiling int64) genExpr {
	g.reached(conCastToInt)
	g.reached(conLoadLogic)
	g.calls++
	text := fmt.Sprintf("toInt(__ic_load(%s, %s))", g.readDevice(), g.pickLogic())
	if ceiling >= genClampMagnitude {
		return genExpr{text: text, kind: genInt, bound: genClampMagnitude}
	}
	mask := int64(1)
	for mask*2+1 <= ceiling {
		mask = mask*2 + 1
	}
	g.reached(conIntBitwise)
	return genExpr{text: fmt.Sprintf("(%s & %d)", text, mask), kind: genInt, bound: mask}
}

// intLeaf is an integer operand with no operator in it.
func (g *microcGen) intLeaf(ceiling int64) genExpr {
	if !g.descend() {
		g.ascend()
		return g.intLiteral(min(ceiling, 7))
	}
	defer g.ascend()
	choices := make([]func() genExpr, 0, 8)
	choices = append(choices, func() genExpr { return g.intLiteral(ceiling) })
	for _, v := range g.vars(genInt) {
		if v.modulus <= ceiling {
			choices = append(choices, func() genExpr {
				return genExpr{text: v.name, kind: genInt, bound: v.modulus, literal: v.constant, memory: v.global}
			})
		}
	}
	for _, a := range g.arraysOf(genInt) {
		if a.modulus <= ceiling {
			choices = append(choices, func() genExpr {
				g.reached(conArrayRead)
				return genExpr{
					text:   fmt.Sprintf("%s[%s]", a.name, g.index(1, a.length)),
					kind:   genInt,
					bound:  a.modulus,
					memory: true,
				}
			})
		}
	}
	for _, p := range g.vars(genPointer) {
		if p.object != nil && p.object.element == genInt && p.object.modulus <= ceiling {
			choices = append(choices, func() genExpr {
				g.reached(conPointerDeref)
				return genExpr{text: "(*" + p.name + ")", kind: genInt, bound: p.object.modulus, memory: true}
			})
		}
		if p.object != nil && int64(p.object.length) <= ceiling {
			choices = append(choices, func() genExpr {
				g.reached(conPointerDiff)
				return genExpr{
					text:  fmt.Sprintf("(%s - %s)", p.name, p.object.name),
					kind:  genInt,
					bound: int64(p.object.length),
				}
			})
		}
	}
	if bools := g.vars(genBool); len(bools) > 0 && ceiling >= 1 {
		choices = append(choices, func() genExpr {
			g.reached(conBoolToInt)
			return genExpr{text: fmt.Sprintf("(%s ? 1 : 0)", genPick(g.rng, bools).name), kind: genInt, bound: 1}
		})
	}
	for _, fn := range g.funcs {
		if fn.result == genInt && fn.modulus <= ceiling {
			choices = append(choices, func() genExpr {
				g.reached(conCall)
				return genExpr{text: g.callText(fn, 1), kind: genInt, bound: fn.modulus}
			})
		}
	}
	if genClampMagnitude <= ceiling {
		choices = append(choices, func() genExpr { return g.reading(ceiling) })
	}
	return choices[g.rng.IntN(len(choices))]()
}

// nonLiteralInt is an operand that does not fold, which is what the second
// operand of a production has to be when the first one did.
func (g *microcGen) nonLiteralInt(ceiling int64) genExpr {
	for range 4 {
		if e := g.intLeaf(ceiling); !e.literal {
			return e
		}
	}
	return g.reading(ceiling)
}

// divisor is an operand that cannot be zero. A literal says so outright;
// anything else is made odd, which no long long the generator can produce
// leaves at zero.
func (g *microcGen) divisor(depth int) genExpr {
	if g.rng.IntN(2) == 0 {
		v := g.rng.Int64N(999) + 1
		if g.rng.IntN(2) == 0 {
			v = -v
		}
		return genExpr{text: strconv.FormatInt(v, 10), kind: genInt, bound: v, literal: true}
	}
	e := g.intExpr(depth, genMaxMagnitude/4)
	g.reached(conIntBitwise)
	return genExpr{text: fmt.Sprintf("(%s | 1)", e.text), kind: genInt, bound: boundAdd(boundMul(e.bound, 2), 1)}
}

// intExpr is an integer expression whose value is under ceiling in
// magnitude for every input the program can meet. Every production
// computes its own bound from its operands rather than checking the result
// afterwards, since the intermediate is what has to stay representable.
func (g *microcGen) intExpr(depth int, ceiling int64) genExpr {
	if ceiling > genMaxMagnitude {
		ceiling = genMaxMagnitude
	}
	if !g.descend() {
		g.ascend()
		return g.intLiteral(min(ceiling, 7))
	}
	defer g.ascend()
	if depth <= 0 || ceiling < 8 {
		return g.intLeaf(ceiling)
	}
	switch g.rng.IntN(16) {
	case 0, 1, 2:
		return g.intBinary(depth, ceiling, genPick(g.rng, []string{"+", "-"}), func(a, b int64) int64 {
			return boundAdd(a, b)
		}, ceiling/2, ceiling/2, conIntArith)
	case 3, 4:
		return g.intBinary(depth, ceiling, "*", boundMul, 1000, ceiling/1000, conIntArith)
	case 5:
		a := g.intExpr(depth-1, ceiling)
		d := g.divisor(depth - 1)
		if a.literal && d.literal {
			a = g.nonLiteralInt(ceiling)
		}
		g.reached(conIntDivide)
		return genExpr{text: fmt.Sprintf("(%s / %s)", a.text, d.text), kind: genInt, bound: a.bound}
	case 6:
		a := g.intExpr(depth-1, genMaxMagnitude)
		d := g.divisor(depth - 1)
		if a.literal && d.literal {
			a = g.nonLiteralInt(genMaxMagnitude)
		}
		g.reached(conIntDivide)
		return genExpr{text: fmt.Sprintf("(%s %% %s)", a.text, d.text), kind: genInt, bound: min(a.bound, d.bound)}
	case 7, 8:
		op := genPick(g.rng, []string{"&", "|", "^"})
		half := (ceiling - 1) / 2
		return g.intBinary(depth, ceiling, op, func(a, b int64) int64 {
			return boundAdd(boundMul(max64(a, b), 2), 1)
		}, half, half, conIntBitwise)
	case 9:
		// The left operand is masked before it shifts. That makes it
		// non-negative, which is where C leaves a left shift defined, and fixes
		// the bound at a number the mask and the distance decide between them.
		mask := genPick(g.rng, genMasks)
		shift := g.rng.IntN(21)
		bound := mask << shift
		if bound > ceiling {
			return g.intLeaf(ceiling)
		}
		a := g.nonLiteralInt(genMaxMagnitude)
		g.reached(conIntShift)
		g.reached(conIntBitwise)
		return genExpr{
			text:  fmt.Sprintf("(((%s) & %d) << %d)", a.text, mask, shift),
			kind:  genInt,
			bound: bound,
		}
	case 10:
		a := g.nonLiteralInt(ceiling)
		g.reached(conIntShift)
		return genExpr{
			text:  fmt.Sprintf("(%s >> %d)", a.text, g.rng.IntN(21)),
			kind:  genInt,
			bound: a.bound,
		}
	case 11:
		a := g.intExpr(depth-1, ceiling-1)
		g.reached(conIntUnary)
		if g.rng.IntN(2) == 0 {
			// The operand is parenthesized because a negated negative
			// literal would otherwise lex as a decrement.
			return genExpr{text: "(-(" + a.text + "))", kind: genInt, bound: a.bound, literal: a.literal}
		}
		return genExpr{text: "(~" + a.text + ")", kind: genInt, bound: boundAdd(a.bound, 1), literal: a.literal}
	case 12:
		c := g.boolExpr(depth - 1)
		a := g.intExpr(depth-1, ceiling)
		b := g.intExpr(depth-1, ceiling)
		if a.memory && b.memory {
			b = g.reading(ceiling)
		}
		g.reached(conIntSelect)
		return genExpr{
			text:    fmt.Sprintf("(%s ? %s : %s)", c.text, a.text, b.text),
			kind:    genInt,
			bound:   max64(a.bound, b.bound),
			literal: c.literal && a.literal && b.literal,
		}
	case 13:
		if genClampMagnitude <= ceiling {
			g.reached(conCastToInt)
			operand := g.doubleExpr(depth - 1)
			return genExpr{
				text:    fmt.Sprintf("toInt(%s)", operand.text),
				kind:    genInt,
				bound:   genClampMagnitude,
				literal: operand.literal,
			}
		}
	}
	return g.intLeaf(ceiling)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// intBinary builds one binary production and falls back to a leaf where the
// bound it computes does not fit.
func (g *microcGen) intBinary(depth int, ceiling int64, op string, combine func(a, b int64) int64, leftMax, rightMax int64, construct string) genExpr {
	if leftMax < 1 || rightMax < 1 {
		return g.intLeaf(ceiling)
	}
	a := g.intExpr(depth-1, leftMax)
	b := g.intExpr(depth-1, rightMax)
	if a.literal && b.literal {
		b = g.nonLiteralInt(rightMax)
	}
	bound := combine(a.bound, b.bound)
	if bound > ceiling {
		return g.intLeaf(ceiling)
	}
	g.reached(construct)
	return genExpr{text: fmt.Sprintf("(%s %s %s)", a.text, op, b.text), kind: genInt, bound: bound}
}

// doubleExpr is a double expression. Nothing bounds one: a double holds every
// value the machine does, infinities and NaN included, and the two sides
// compute it identically because both are IEEE doubles with contraction off.
func (g *microcGen) doubleExpr(depth int) genExpr {
	if !g.descend() {
		g.ascend()
		return genExpr{text: genPick(g.rng, genDoubleLiterals), kind: genDouble, literal: true}
	}
	defer g.ascend()
	if depth <= 0 {
		return g.doubleLeaf()
	}
	switch g.rng.IntN(14) {
	case 0, 1, 2:
		op := genPick(g.rng, []string{"+", "-", "*"})
		a := g.doubleExpr(depth - 1)
		b := g.doubleExpr(depth - 1)
		if a.literal && b.literal {
			b = g.doubleReading()
		}
		g.reached(conDoubleArith)
		return genExpr{text: fmt.Sprintf("(%s %s %s)", a.text, op, b.text), kind: genDouble}
	case 3:
		// A double divisor is left alone where an integer one is made odd:
		// zero over zero is a NaN C defines and the machine produces, a
		// source of the values the optimizer's integer identities are false for.
		a := g.doubleExpr(depth - 1)
		b := g.doubleExpr(depth - 1)
		if a.literal && b.literal {
			b = g.doubleReading()
		}
		g.reached(conDoubleByZero)
		return genExpr{text: fmt.Sprintf("(%s / %s)", a.text, b.text), kind: genDouble}
	case 4:
		g.reached(conMachine1)
		return genExpr{
			text: fmt.Sprintf("__ic_%s(%s)", genPick(g.rng, genUnaryMachine), g.doubleExpr(depth-1).text),
			kind: genDouble,
		}
	case 5:
		g.reached(conMachine2)
		return genExpr{
			text: fmt.Sprintf("__ic_%s(%s, %s)", genPick(g.rng, genBinaryMachine),
				g.doubleExpr(depth-1).text, g.doubleExpr(depth-1).text),
			kind: genDouble,
		}
	case 6:
		g.reached(conMachine3)
		return genExpr{
			text: fmt.Sprintf("__ic_%s(%s, %s, %s)", genPick(g.rng, genTernaryMachine),
				g.doubleExpr(depth-1).text, g.doubleExpr(depth-1).text, g.doubleExpr(depth-1).text),
			kind: genDouble,
		}
	case 7:
		c := g.boolExpr(depth - 1)
		a := g.doubleExpr(depth - 1)
		b := g.doubleExpr(depth - 1)
		if a.memory && b.memory {
			b = g.doubleReading()
		}
		g.reached(conDoubleSelect)
		return genExpr{
			text:    fmt.Sprintf("(%s ? %s : %s)", c.text, a.text, b.text),
			kind:    genDouble,
			literal: c.literal && a.literal && b.literal,
		}
	case 8:
		g.reached(conCastToDouble)
		if bools := g.vars(genBool); len(bools) > 0 && g.rng.IntN(2) == 0 {
			return genExpr{text: fmt.Sprintf("((double)%s)", genPick(g.rng, bools).name), kind: genDouble}
		}
		operand := g.intExpr(depth-1, genMaxMagnitude)
		return genExpr{text: fmt.Sprintf("((double)%s)", operand.text), kind: genDouble, literal: operand.literal}
	case 9:
		return g.batchRead()
	}
	return g.doubleLeaf()
}

// batchRead is a read across every device of a prefab type: one hash
// matches a device the world lays out, and the other matches nothing,
// which is where the machine answers NaN with no arithmetic involved.
func (g *microcGen) batchRead() genExpr {
	hash := fmt.Sprintf("__ic_hash(%q)", genEmptyPrefab)
	if g.rng.IntN(2) == 0 {
		hash = fmt.Sprintf("__ic_hash(%q)", genMatchedPrefab)
	}
	g.reached(conHash)
	switch g.rng.IntN(4) {
	case 0:
		g.reached(conLoadBatch)
		return genExpr{text: fmt.Sprintf("__ic_load_batch(%s, %s, %s)",
			hash, g.pickLogic(), genPick(g.rng, genBatchModes)), kind: genDouble}
	case 1:
		g.reached(conLoadBatchNamed)
		return genExpr{text: fmt.Sprintf("__ic_load_batch_named(%s, __ic_hash(%q), %s, %s)",
			hash, genMatchedName, g.pickLogic(), genPick(g.rng, genBatchModes)), kind: genDouble}
	case 2:
		g.reached(conLoadBatchSlot)
		return genExpr{text: fmt.Sprintf("__ic_load_batch_slot(%s, %d, %s, %s)",
			hash, genPick(g.rng, genSlots), g.pickSlotType(), genPick(g.rng, genBatchModes)), kind: genDouble}
	default:
		g.reached(conLoadReagent)
		return genExpr{text: fmt.Sprintf("__ic_load_reagent(%s, %s, __ic_hash(%q))",
			g.readDevice(), genPick(g.rng, genReagentModes), genReagent), kind: genDouble}
	}
}

// doubleReading is the double source every scope has, and what a production
// falls back to when it needs an operand that does not fold.
func (g *microcGen) doubleReading() genExpr {
	g.reached(conLoadLogic)
	return genExpr{
		text: fmt.Sprintf("__ic_load(%s, %s)", g.readDevice(), g.pickLogic()),
		kind: genDouble,
	}
}

func (g *microcGen) doubleLeaf() genExpr {
	if !g.descend() {
		g.ascend()
		return genExpr{text: genPick(g.rng, genDoubleLiterals), kind: genDouble, literal: true}
	}
	defer g.ascend()
	choices := make([]func() genExpr, 0, 8)
	choices = append(choices, func() genExpr {
		return genExpr{text: genPick(g.rng, genDoubleLiterals), kind: genDouble, literal: true}
	})
	choices = append(choices, func() genExpr {
		g.reached(conLoadLogic)
		return genExpr{text: fmt.Sprintf("__ic_load(%s, %s)", g.readDevice(), g.pickLogic()), kind: genDouble}
	})
	choices = append(choices, func() genExpr {
		g.reached(conLoadSlot)
		return genExpr{text: fmt.Sprintf("__ic_load_slot(%s, %d, %s)",
			g.readDevice(), genPick(g.rng, genSlots), g.pickSlotType()), kind: genDouble}
	})
	for _, v := range g.vars(genDouble) {
		choices = append(choices, func() genExpr {
			return genExpr{text: v.name, kind: genDouble, literal: v.constant, memory: v.global}
		})
	}
	for _, a := range g.arraysOf(genDouble) {
		choices = append(choices, func() genExpr {
			g.reached(conArrayRead)
			return genExpr{text: fmt.Sprintf("%s[%s]", a.name, g.index(1, a.length)), kind: genDouble, memory: true}
		})
	}
	for _, p := range g.vars(genPointer) {
		if p.object != nil && p.object.element == genDouble {
			choices = append(choices, func() genExpr {
				g.reached(conPointerDeref)
				return genExpr{text: "(*" + p.name + ")", kind: genDouble, memory: true}
			})
		}
	}
	for _, fn := range g.funcs {
		if fn.result == genDouble {
			choices = append(choices, func() genExpr {
				g.reached(conCall)
				return genExpr{text: g.callText(fn, 1), kind: genDouble}
			})
		}
	}
	return choices[g.rng.IntN(len(choices))]()
}

// boolExpr is a condition. The comparison operators are the productions
// this exists for: an ordered comparison is false for a NaN operand on
// both sides, so a condition and its negation can both be false — the
// family several defects in this project came from.
func (g *microcGen) boolExpr(depth int) genExpr {
	if !g.descend() {
		g.ascend()
		return genExpr{text: "true", kind: genBool, literal: true}
	}
	defer g.ascend()
	if depth <= 0 {
		return g.boolLeaf()
	}
	switch g.rng.IntN(12) {
	case 0, 1, 2:
		op := genPick(g.rng, []string{"<", "<=", ">", ">=", "==", "!="})
		g.reached(conBoolCompareDbl)
		return genExpr{
			text: fmt.Sprintf("(%s %s %s)", g.doubleExpr(depth-1).text, op, g.doubleExpr(depth-1).text),
			kind: genBool,
		}
	case 3, 4:
		op := genPick(g.rng, []string{"<", "<=", ">", ">=", "==", "!="})
		g.reached(conBoolCompareInt)
		return genExpr{
			text: fmt.Sprintf("(%s %s %s)", g.intExpr(depth-1, genMaxMagnitude).text, op,
				g.intExpr(depth-1, genMaxMagnitude).text),
			kind: genBool,
		}
	case 5:
		g.reached(conBoolIsNaN)
		return genExpr{text: fmt.Sprintf("__ic_isnan(%s)", g.doubleExpr(depth-1).text), kind: genBool}
	case 6:
		g.reached(conBoolLogic)
		if g.rng.IntN(3) == 0 {
			return genExpr{text: "(!" + g.boolExpr(depth-1).text + ")", kind: genBool}
		}
		op := genPick(g.rng, []string{"&&", "||"})
		return genExpr{
			text: fmt.Sprintf("(%s %s %s)", g.boolExpr(depth-1).text, op, g.boolExpr(depth-1).text),
			kind: genBool,
		}
	case 7:
		g.reached(conCastToBool)
		return genExpr{text: fmt.Sprintf("((bool)%s)", g.doubleExpr(depth-1).text), kind: genBool}
	case 8:
		if bools := g.vars(genBool); len(bools) > 0 {
			g.reached(conBoolCompareBool)
			return genExpr{
				text: fmt.Sprintf("(%s %s %s)", genPick(g.rng, bools).name,
					genPick(g.rng, []string{"==", "!="}), g.boolExpr(depth-1).text),
				kind: genBool,
			}
		}
	case 9:
		var pointers []*genVar
		for _, p := range g.vars(genPointer) {
			if p.object != nil {
				pointers = append(pointers, p)
			}
		}
		if len(pointers) > 0 {
			p := genPick(g.rng, pointers)
			g.reached(conBoolComparePtr)
			return genExpr{
				text: fmt.Sprintf("(%s %s (%s + %d))", p.name,
					genPick(g.rng, []string{"<", "<=", ">", ">=", "==", "!="}),
					p.object.name, g.rng.IntN(p.object.length)),
				kind: genBool,
			}
		}
	}
	return g.boolLeaf()
}

func (g *microcGen) boolLeaf() genExpr {
	if !g.descend() {
		g.ascend()
		return genExpr{text: "true", kind: genBool, literal: true}
	}
	defer g.ascend()
	choices := make([]func() genExpr, 0, 6)
	choices = append(choices, func() genExpr {
		return genExpr{text: genPick(g.rng, []string{"true", "false"}), kind: genBool, literal: true}
	})
	choices = append(choices, func() genExpr {
		g.reached(conBoolPresent)
		return genExpr{text: fmt.Sprintf("__ic_device_present(%s)", g.readDevice()), kind: genBool}
	})
	for _, v := range g.vars(genBool) {
		choices = append(choices, func() genExpr { return genExpr{text: v.name, kind: genBool} })
	}
	for _, a := range g.arraysOf(genBool) {
		choices = append(choices, func() genExpr {
			g.reached(conArrayRead)
			return genExpr{text: fmt.Sprintf("%s[%s]", a.name, g.index(1, a.length)), kind: genBool}
		})
	}
	for _, fn := range g.funcs {
		if fn.result == genBool {
			choices = append(choices, func() genExpr {
				g.reached(conCall)
				return genExpr{text: g.callText(fn, 1), kind: genBool}
			})
		}
	}
	return choices[g.rng.IntN(len(choices))]()
}

// expr is one expression of the given kind.
func (g *microcGen) expr(kind genKind, depth int, ceiling int64) genExpr {
	if kind == genInt {
		return g.intExpr(depth, ceiling)
	}
	if kind == genDouble {
		return g.doubleExpr(depth)
	}
	return g.boolExpr(depth)
}

// callText renders a call, reducing every integer argument by the parameter's
// own modulus. That is what lets a function body treat its parameters as
// bounded without knowing anything about its callers.
func (g *microcGen) callText(fn *genFunc, depth int) string {
	g.calls++
	args := make([]string, len(fn.params))
	for i, p := range fn.params {
		switch {
		case p.dev:
			args[i] = fn.device
		case p.kind == genPointer:
			args[i] = genPick(g.rng, g.pointerArguments()).name
		case p.kind == genInt:
			args[i] = fmt.Sprintf("(%s) %% %d", g.intExpr(depth, genMaxMagnitude).text, p.modulus)
		default:
			args[i] = g.expr(p.kind, depth, genMaxMagnitude).text
		}
	}
	return fn.name + "(" + strings.Join(args, ", ") + ")"
}

// pointerArguments are the arrays a pointer parameter accepts: those of the one
// length its body masks with, since a decayed array parameter carries no bound.
func (g *microcGen) pointerArguments() []*genVar {
	var out []*genVar
	for _, a := range g.arrays {
		if a.length == genPointerLength && a.element == genInt {
			out = append(out, a)
		}
	}
	return out
}

// genCtx is what a statement may do where it stands.
type genCtx struct {
	// inLoop admits break, which only a for sets. A while and a do advance
	// their counter after the body, so a break would leave the step unreachable
	// and IR generation emits an unreachable statement into a block it has
	// already terminated.
	inLoop bool
	// inFor admits continue, for the same reason narrowed further: only a loop
	// whose header advances the counter still advances it past one.
	inFor bool
	// depth bounds statement nesting.
	depth int
}

func (g *microcGen) block(ctx genCtx, count int) []genNode {
	mark := g.scopeMark()
	nodes := make([]genNode, 0, count)
	for range count {
		node := g.statement(ctx)
		nodes = append(nodes, node)
		if node.terminates {
			break
		}
	}
	g.scopeReset(mark)
	return nodes
}

func (g *microcGen) statement(ctx genCtx) genNode {
	if ctx.depth <= 0 {
		return g.simpleStatement(ctx)
	}
	switch g.rng.IntN(20) {
	case 0, 1, 2:
		return g.ifStatement(ctx)
	case 3:
		return g.forStatement(ctx)
	case 4:
		return g.whileStatement(ctx)
	case 5:
		return g.doStatement(ctx)
	case 6:
		return g.switchStatement(ctx)
	}
	return g.simpleStatement(ctx)
}

func (g *microcGen) simpleStatement(ctx genCtx) genNode {
	switch g.rng.IntN(22) {
	case 0, 1, 2:
		return g.declareScalar()
	case 3:
		return g.declarePointer()
	case 4, 5, 6:
		return g.assignStatement()
	case 7:
		return g.compoundChain()
	case 8, 9:
		return g.arrayWrite()
	case 10:
		return g.pointerWrite()
	case 11:
		return g.callStatement()
	case 12, 13:
		// A loop body is short and reached from a production that is itself
		// rare, so leaving these to a further draw inside one puts them out of
		// reach of a campaign that compiles what it generates.
		if ctx.inFor && g.rng.IntN(2) == 0 {
			g.reached(conContinue)
			return genNode{text: []string{"continue;"}, terminates: true}
		}
		if ctx.inLoop {
			g.reached(conBreak)
			return genNode{text: []string{"break;"}, terminates: true}
		}
	}
	return g.storeStatement()
}

// declareScalar introduces a local with an initializer, which is what definite
// assignment requires of a register-resident local and what keeps a shrink from
// producing a program that reads one before it is written.
func (g *microcGen) declareScalar() genNode {
	switch g.rng.IntN(3) {
	case 0:
		m := genPick(g.rng, genModuli)
		name := g.name("v")
		text := fmt.Sprintf("long long %s = (%s) %% %d;", name, g.intExpr(2, genMaxMagnitude).text, m)
		g.scope = append(g.scope, &genVar{name: name, kind: genInt, modulus: m})
		return genNode{text: []string{text}, defines: []string{name}}
	case 1:
		name := g.name("v")
		text := fmt.Sprintf("double %s = %s;", name, g.doubleExpr(2).text)
		g.scope = append(g.scope, &genVar{name: name, kind: genDouble})
		return genNode{text: []string{text}, defines: []string{name}}
	default:
		name := g.name("v")
		text := fmt.Sprintf("bool %s = %s;", name, g.boolExpr(2).text)
		g.scope = append(g.scope, &genVar{name: name, kind: genBool})
		return genNode{text: []string{text}, defines: []string{name}}
	}
}

// declarePointer takes the address of an element and steps it. The step is
// masked to half the array, so the pointer stays inside the object it
// designates however the reading it was computed from moves.
func (g *microcGen) declarePointer() genNode {
	arrays := g.arrays
	if len(arrays) == 0 {
		return g.storeStatement()
	}
	a := genPick(g.rng, arrays)
	name := g.name("p")
	g.reached(conPointerTake)
	lines := []string{fmt.Sprintf("%s *%s = &%s[%s];", a.element.spelling(), name, a.name, g.index(1, a.length/2))}
	if g.rng.IntN(2) == 0 {
		g.reached(conPointerStep)
		lines = append(lines, fmt.Sprintf("%s = %s + ((%s) & %d);", name, name,
			g.intExpr(1, genMaxMagnitude).text, a.length/2-1))
	} else {
		g.reached(conPointerStep1)
		lines = append(lines, name+"++;")
	}
	g.scope = append(g.scope, &genVar{name: name, kind: genPointer, object: a})
	return genNode{text: lines, defines: []string{name}}
}

func (g *microcGen) assignStatement() genNode {
	switch g.rng.IntN(3) {
	case 0:
		if vars := g.assignable(genInt); len(vars) > 0 {
			v := genPick(g.rng, vars)
			return genNode{text: []string{fmt.Sprintf("%s = (%s) %% %d;", v.name,
				g.intExpr(2, genMaxMagnitude).text, v.modulus)}}
		}
	case 1:
		if vars := g.assignable(genDouble); len(vars) > 0 {
			v := genPick(g.rng, vars)
			return genNode{text: []string{fmt.Sprintf("%s = %s;", v.name, g.doubleExpr(2).text)}}
		}
	default:
		if vars := g.assignable(genBool); len(vars) > 0 {
			v := genPick(g.rng, vars)
			return genNode{text: []string{fmt.Sprintf("%s = %s;", v.name, g.boolExpr(2).text)}}
		}
	}
	return g.storeStatement()
}

// compoundChain is a temporary and the compound assignments that narrow and
// widen it, as one node: the bound is tracked through the chain, and
// several operators only keep it because an earlier one reduced it, so the
// chain is atomic to the shrinker.
func (g *microcGen) compoundChain() genNode {
	name := g.name("t")
	start := g.intExpr(2, 1<<20)
	lines := []string{fmt.Sprintf("long long %s = %s;", name, start.text)}
	bound := start.bound
	g.reached(conCompoundAssign)
	for range g.rng.IntN(3) + 1 {
		switch g.rng.IntN(7) {
		case 0:
			e := g.intExpr(1, 1<<20)
			lines = append(lines, fmt.Sprintf("%s += %s;", name, e.text))
			bound = boundAdd(bound, e.bound)
		case 1:
			e := g.intExpr(1, 1<<20)
			lines = append(lines, fmt.Sprintf("%s -= %s;", name, e.text))
			bound = boundAdd(bound, e.bound)
		case 2:
			k := int64(g.rng.IntN(9) + 2)
			if boundMul(bound, k) > genMaxMagnitude {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s *= %d;", name, k))
			bound = boundMul(bound, k)
		case 3:
			m := genPick(g.rng, genModuli)
			lines = append(lines, fmt.Sprintf("%s %%= %d;", name, m))
			bound = min(bound, m)
		case 4:
			mask := genPick(g.rng, genMasks)
			lines = append(lines, fmt.Sprintf("%s &= %d;", name, mask))
			bound = min(bound, mask)
		case 5:
			lines = append(lines, fmt.Sprintf("%s >>= %d;", name, g.rng.IntN(8)+1))
		default:
			g.reached(conIncrement)
			lines = append(lines, name+genPick(g.rng, []string{"++;", "--;"}))
			bound = boundAdd(bound, 1)
		}
	}
	m := genPick(g.rng, genModuli)
	lines = append(lines, fmt.Sprintf("%s %%= %d;", name, m))
	g.scope = append(g.scope, &genVar{name: name, kind: genInt, modulus: m})
	return genNode{text: lines, defines: []string{name}}
}

func (g *microcGen) arrayWrite() genNode {
	if len(g.arrays) == 0 {
		return g.storeStatement()
	}
	a := genPick(g.rng, g.arrays)
	g.reached(conArrayWrite)
	return genNode{text: []string{fmt.Sprintf("%s[%s] = %s;",
		a.name, g.index(2, a.length), g.elementValue(a))}}
}

func (g *microcGen) pointerWrite() genNode {
	var pointers []*genVar
	for _, p := range g.vars(genPointer) {
		if p.object != nil {
			pointers = append(pointers, p)
		}
	}
	if len(pointers) == 0 {
		return g.storeStatement()
	}
	p := genPick(g.rng, pointers)
	g.reached(conPointerStore)
	return genNode{text: []string{fmt.Sprintf("*%s = %s;", p.name, g.elementValue(p.object))}}
}

// elementValue is a value the array's element type admits, reduced by the
// array's own modulus where that type is an integer.
func (g *microcGen) elementValue(a *genVar) string {
	if a.element == genInt {
		return fmt.Sprintf("(%s) %% %d", g.intExpr(2, genMaxMagnitude).text, a.modulus)
	}
	if a.element == genDouble {
		return g.doubleExpr(2).text
	}
	return g.boolExpr(2).text
}

func (g *microcGen) callStatement() genNode {
	var voids []*genFunc
	for _, fn := range g.funcs {
		if fn.result == genVoid {
			voids = append(voids, fn)
		}
	}
	if len(voids) == 0 {
		return g.storeStatement()
	}
	g.reached(conCallVoid)
	return genNode{text: []string{g.callText(genPick(g.rng, voids), 2) + ";"}}
}

// storeStatement publishes a value to a device, which is what a run has to
// compare. Every other statement is only visible through one of these.
func (g *microcGen) storeStatement() genNode {
	switch g.rng.IntN(8) {
	case 0:
		g.reached(conStoreSlot)
		return genNode{text: []string{fmt.Sprintf("__ic_store_slot(%s, %d, %s, %s);",
			g.writeDevice(), genPick(g.rng, genSlots), g.pickSlotType(), g.doubleExpr(2).text)}}
	case 1:
		g.reached(conStoreBatch)
		g.reached(conHash)
		return genNode{text: []string{fmt.Sprintf("__ic_store_batch(__ic_hash(%q), %s, %s);",
			genMatchedPrefab, g.pickLogic(), g.doubleExpr(2).text)}}
	default:
		g.reached(conStoreLogic)
		return genNode{text: []string{fmt.Sprintf("__ic_store(%s, %s, %s);",
			g.writeDevice(), g.pickLogic(), g.doubleExpr(2).text)}}
	}
}

// ifStatement is the shape SimplifyCFG rewrites: a store under a condition, a
// pair of arms that merge, and a chain whose last test decides nothing.
func (g *microcGen) ifStatement(ctx genCtx) genNode {
	inner := ctx
	inner.depth--
	cond := g.boolExpr(2)
	// Marked after the condition, because a call the test itself makes is
	// evaluated before the branch and stands on no arm; deferred because an arm
	// is built inside the composite literal each case returns.
	calls := g.calls
	defer func() { g.reachedCallsSince(calls, conCallOnArm) }()
	body := g.block(inner, g.rng.IntN(3)+1)
	switch g.rng.IntN(4) {
	case 0, 1:
		g.reached(conIf)
		if len(body) == 1 && len(body[0].text) == 1 && strings.HasPrefix(body[0].text[0], "__ic_store") {
			g.reached(conConditionalWrite)
		}
		return genNode{head: []string{"if (" + cond.text + ") {"}, body: body, tail: []string{"}"}}
	case 2:
		g.reached(conIfElse)
		return genNode{
			head: []string{"if (" + cond.text + ") {"},
			body: body,
			mid:  []string{"} else {"},
			alt:  g.block(inner, g.rng.IntN(3)+1),
			tail: []string{"}"},
		}
	default:
		g.reached(conElseIf)
		return genNode{
			head: []string{"if (" + cond.text + ") {"},
			body: body,
			mid:  []string{"} else if (" + g.boolExpr(2).text + ") {"},
			alt:  g.block(inner, g.rng.IntN(3)+1),
			tail: []string{"}"},
		}
	}
}

// forStatement counts a fixed number of turns. The counter is declared in the
// header and advanced there, so no statement the shrinker can remove decides
// whether the loop ends.
func (g *microcGen) forStatement(ctx genCtx) genNode {
	inner := ctx
	inner.depth--
	inner.inLoop = true
	inner.inFor = true
	name := g.name("i")
	turns := g.rng.IntN(6) + 2

	mark := g.scopeMark()
	g.scope = append(g.scope, &genVar{name: name, kind: genInt, modulus: int64(turns), readOnly: true})
	// The counter is published first, for the reason whileStatement gives.
	// A for counter leaves scope with the loop, so the read has to be inside it.
	calls := g.calls
	body := append([]genNode{{text: []string{g.counterPublish(name)}, required: true}},
		g.block(inner, g.rng.IntN(3)+1)...)
	g.scopeReset(mark)

	g.reached(conFor)
	g.reachedCallsSince(calls, conCallInLoop)
	return genNode{
		head:    []string{fmt.Sprintf("for (long long %s = 0; %s < %d; %s++) {", name, name, turns, name)},
		body:    body,
		tail:    []string{"}"},
		defines: []string{name},
	}
}

// whileStatement and doStatement advance their counter after the body, so
// neither carries a break, and the step is a node the shrinker may not
// remove — a loop whose step went would not terminate.
func (g *microcGen) whileStatement(ctx genCtx) genNode {
	inner := ctx
	inner.depth--
	inner.inLoop = false
	inner.inFor = false
	name := g.name("i")
	turns := g.rng.IntN(6) + 2

	mark := g.scopeMark()
	g.scope = append(g.scope, &genVar{name: name, kind: genInt, modulus: int64(turns), readOnly: true})
	calls := g.calls
	body := g.block(inner, g.rng.IntN(2)+1)
	g.scopeReset(mark)
	body = append(body, genNode{text: []string{name + "++;"}, required: true})

	g.reached(conWhile)
	g.reachedCallsSince(calls, conCallInLoop)
	return genNode{
		head:    []string{fmt.Sprintf("long long %s = 0;", name), fmt.Sprintf("while (%s < %d) {", name, turns)},
		body:    body,
		tail:    []string{"}", g.counterPublish(name)},
		defines: []string{name},
	}
}

func (g *microcGen) doStatement(ctx genCtx) genNode {
	inner := ctx
	inner.depth--
	inner.inLoop = false
	inner.inFor = false
	name := g.name("i")
	turns := g.rng.IntN(6) + 2

	mark := g.scopeMark()
	g.scope = append(g.scope, &genVar{name: name, kind: genInt, modulus: int64(turns), readOnly: true})
	calls := g.calls
	body := g.block(inner, g.rng.IntN(2)+1)
	g.scopeReset(mark)
	body = append(body, genNode{text: []string{name + "++;"}, required: true})

	g.reached(conDoWhile)
	g.reachedCallsSince(calls, conCallInLoop)
	return genNode{
		head:    []string{fmt.Sprintf("long long %s = 0;", name), "do {"},
		body:    body,
		tail:    []string{fmt.Sprintf("} while (%s < %d);", name, turns), g.counterPublish(name)},
		defines: []string{name},
	}
}

// reachedCallsSince records the placement a call written since mark stands
// in. Where a call stands is not something the emitted text gives back — a
// call is a jal in a loop, on a branch arm, and in a straight line alike —
// so placements are recorded where the generator knows them.
func (g *microcGen) reachedCallsSince(mark int, placement string) {
	if g.calls > mark {
		g.reached(placement)
	}
}

// counterPublish is the read that keeps a loop counter 64 bits wide.
func (g *microcGen) counterPublish(name string) string {
	g.reached(conStoreLogic)
	g.reached(conCastToDouble)
	return fmt.Sprintf("__ic_store(%s, %s, (double)%s);", g.writeDevice(), g.pickLogic(), name)
}

// switchStatement is one node rather than a block with removable arms: an
// arm with a body must terminate and the arm above it stacks its label
// only while its own body is empty, so removing part of one produces a
// program MicroC rejects rather than a smaller reproducer.
func (g *microcGen) switchStatement(genCtx) genNode {
	tag := g.intExpr(2, genMaxMagnitude)
	lines := []string{fmt.Sprintf("switch ((%s) %% 4) {", tag.text)}

	// The first arm has no body, which stacks its label onto the one below. That
	// is the only fallthrough the language admits, and a duplicate block label
	// from exactly this shape has been a defect here.
	g.reached(conSwitchFall)
	calls := g.calls
	for _, labels := range [][]string{{"case 0:", "case 1:"}, {"case 2:"}, {"default:"}} {
		lines = append(lines, labels...)
		lines = append(lines, renderInto(g.effectBlock(g.rng.IntN(2)+1), 1)...)
		lines = append(lines, "    break;")
	}
	lines = append(lines, "}")

	g.reached(conSwitch)
	g.reachedCallsSince(calls, conCallOnArm)
	return genNode{text: lines}
}

// effectBlock is statements that declare nothing, for the positions a
// declaration cannot stand in.
func (g *microcGen) effectBlock(count int) []genNode {
	nodes := make([]genNode, 0, count)
	for range count {
		switch g.rng.IntN(5) {
		case 0:
			nodes = append(nodes, g.assignStatement())
		case 1:
			nodes = append(nodes, g.arrayWrite())
		case 2:
			nodes = append(nodes, g.pointerWrite())
		case 3:
			nodes = append(nodes, g.callStatement())
		default:
			nodes = append(nodes, g.storeStatement())
		}
	}
	return nodes
}

// pureBlock is statements with no effect outside the block: locals, and the
// pointers into one object that reading through needs. It is the body of every
// function that returns a value, so that no expression a call stands in can
// depend on when the call ran.
func (g *microcGen) pureBlock(count int) []genNode {
	nodes := make([]genNode, 0, count)
	for range count {
		if g.rng.IntN(4) == 0 {
			nodes = append(nodes, g.declarePointer())
			continue
		}
		nodes = append(nodes, g.declareScalar())
	}
	return nodes
}

func (g *microcGen) globalDeclarations() []genNode {
	nodes := []genNode{
		{text: []string{"const dev in = d0;"}, defines: []string{"in"}},
		{text: []string{"const dev out = d1;"}, defines: []string{"out"}},
		{text: []string{"const dev aux = d2;"}, defines: []string{"aux"}},
		{text: []string{"const dev sink = d3;"}, defines: []string{"sink"}},
	}

	// One array of each element kind is always declared, so a campaign reaches
	// the bool array and the pointer parameter every time rather than only when
	// a draw happens to produce one.
	for _, element := range []genKind{genInt, genDouble, genBool} {
		name := g.name("a")
		length := genPointerLength
		if element != genInt || g.rng.IntN(2) == 0 {
			length = genPick(g.rng, genArrayLengths)
		}
		g.reached(map[genKind]string{
			genInt:    conGlobalIntArr,
			genDouble: conGlobalDblArr,
			genBool:   conGlobalBoolArr,
		}[element])
		v := &genVar{name: name, kind: element, element: element, length: length, modulus: genArrayModulus, global: true}
		g.arrays = append(g.arrays, v)
		nodes = append(nodes, genNode{
			text:    []string{fmt.Sprintf("%s %s[%d];", element.spelling(), name, length)},
			defines: []string{name},
		})
	}
	// A pointer parameter needs an array of the one length its body masks with.
	if len(g.pointerArguments()) == 0 {
		name := g.name("a")
		v := &genVar{name: name, kind: genInt, element: genInt, length: genPointerLength, modulus: genArrayModulus, global: true}
		g.arrays = append(g.arrays, v)
		g.reached(conGlobalIntArr)
		nodes = append(nodes, genNode{
			text:    []string{fmt.Sprintf("long long %s[%d];", name, genPointerLength)},
			defines: []string{name},
		})
	}

	for range g.rng.IntN(3) + 2 {
		switch g.rng.IntN(3) {
		case 0:
			name := g.name("g")
			m := genPick(g.rng, genModuli)
			g.reached(conGlobalInt)
			g.scope = append(g.scope, &genVar{name: name, kind: genInt, modulus: m, global: true})
			nodes = append(nodes, genNode{text: []string{"long long " + name + ";"}, defines: []string{name}})
		case 1:
			name := g.name("g")
			g.reached(conGlobalDouble)
			g.scope = append(g.scope, &genVar{name: name, kind: genDouble, global: true})
			nodes = append(nodes, genNode{text: []string{"double " + name + ";"}, defines: []string{name}})
		default:
			name := g.name("g")
			g.reached(conGlobalBool)
			g.scope = append(g.scope, &genVar{name: name, kind: genBool, global: true})
			nodes = append(nodes, genNode{text: []string{"bool " + name + ";"}, defines: []string{name}})
		}
	}

	name := g.name("k")
	value := g.rng.Int64N(1000) - 500
	g.reached(conConstexpr)
	g.scope = append(g.scope, &genVar{name: name, kind: genInt, modulus: absInt64(value) + 1, readOnly: true, constant: true})
	nodes = append(nodes, genNode{
		text:    []string{fmt.Sprintf("constexpr long long %s = %d;", name, value)},
		defines: []string{name},
	})

	dbl := g.name("k")
	g.scope = append(g.scope, &genVar{name: dbl, kind: genDouble, readOnly: true, constant: true})
	nodes = append(nodes, genNode{
		text:    []string{fmt.Sprintf("constexpr double %s = %s;", dbl, genPick(g.rng, genDoubleLiterals))},
		defines: []string{dbl},
	})
	return nodes
}

// toIntSource is the one cast from a double to a long long a generated
// program contains: a double outside the integer type's range converts to
// no value C defines, and infinity and NaN are both outside every range, so
// the guarding comparison is false for all three.
const toIntSource = `long long toInt(double v) {
    if (v > -1000000.0 && v < 1000000.0) {
        return (long long)v;
    }
    return 0;
}`

func (g *microcGen) functionDeclarations() []genNode {
	nodes := []genNode{{
		text:    strings.Split(toIntSource, "\n"),
		defines: []string{"toInt"},
	}}
	for range g.rng.IntN(3) + 1 {
		nodes = append(nodes, g.function())
	}
	return nodes
}

// function builds one helper. Its body sees the globals and its own
// parameters and nothing else, and every call it makes is to a function
// already declared, so no generated program can recurse. A function that
// returns a value writes nothing observable.
func (g *microcGen) function() genNode {
	fn := &genFunc{
		name:   g.name("f"),
		result: genPick(g.rng, []genKind{genInt, genDouble, genBool, genVoid}),
	}
	if fn.result == genInt {
		fn.modulus = genPick(g.rng, genModuli)
	}
	fn.device = g.writeDevice()

	for range g.rng.IntN(3) + 1 {
		p := genParam{name: g.name("q")}
		switch g.rng.IntN(6) {
		case 0:
			p.kind = genInt
			p.modulus = genPick(g.rng, genModuli)
		case 1, 2:
			p.kind = genDouble
		case 3:
			p.kind = genBool
		case 4:
			p.kind = genPointer
			p.arraySyntax = g.rng.IntN(2) == 0
			if p.arraySyntax {
				g.reached(conParamArray)
			} else {
				g.reached(conParamPointer)
			}
		default:
			// A device reaches a function only where the function writes
			// through it, which is a function returning nothing.
			if fn.result != genVoid {
				p.kind = genDouble
				break
			}
			p.kind = genVoid
			p.dev = true
			g.reached(conParamDev)
		}
		fn.params = append(fn.params, p)
	}
	mark := g.scopeMark()
	arrayMark := len(g.arrays)
	var decls []string
	device := ""
	for _, p := range fn.params {
		switch {
		case p.dev:
			decls = append(decls, "dev "+p.name)
			device = p.name
		case p.kind == genPointer:
			if p.arraySyntax {
				decls = append(decls, "long long "+p.name+"[]")
			} else {
				decls = append(decls, "long long *"+p.name)
			}
			g.arrays = append(g.arrays, &genVar{
				name: p.name, kind: genInt, element: genInt,
				length: genPointerLength, modulus: genArrayModulus,
			})
		default:
			decls = append(decls, p.kind.spelling()+" "+p.name)
			m := p.modulus
			if m == 0 {
				m = 1
			}
			g.scope = append(g.scope, &genVar{name: p.name, kind: p.kind, modulus: m})
		}
	}

	calls := g.calls
	var body []genNode
	switch {
	case device != "":
		// A function taking a device is compiled by splicing its body into
		// every call, substituting the pin the site named. One statement
		// keeps every site able to do that: a larger body would leave the
		// pin travelling in a register the chip does not read as a device.
		body = []genNode{{text: []string{fmt.Sprintf("__ic_store(%s, %s, %s);",
			device, g.pickLogic(), g.doubleExpr(1).text)}}}
	case fn.result == genVoid:
		body = g.block(genCtx{depth: 1}, g.rng.IntN(3)+1)
	default:
		body = g.pureBlock(g.rng.IntN(3))
	}
	if fn.result == genInt {
		body = append(body, genNode{
			text:     []string{fmt.Sprintf("return (%s) %% %d;", g.intExpr(2, genMaxMagnitude).text, fn.modulus)},
			required: true,
		})
	}
	if fn.result == genDouble {
		body = append(body, genNode{text: []string{"return " + g.doubleExpr(2).text + ";"}, required: true})
	}
	if fn.result == genBool {
		body = append(body, genNode{text: []string{"return " + g.boolExpr(2).text + ";"}, required: true})
	}
	g.scopeReset(mark)
	g.arrays = g.arrays[:arrayMark]
	g.reachedCallsSince(calls, conCallNested)

	g.funcs = append(g.funcs, fn)
	return genNode{
		head:    []string{fmt.Sprintf("%s %s(%s) {", fn.result.spelling(), fn.name, strings.Join(decls, ", "))},
		body:    body,
		tail:    []string{"}"},
		defines: []string{fn.name},
	}
}

// generateProgram builds one program from one seed.
func generateProgram(seed uint64) generated { return newMicrocGen(seed).build() }

// build is the whole of one program: the declarations, then the control loop
// every MicroC program that keeps running owns.
func (g *microcGen) build() generated {
	globals := g.globalDeclarations()
	funcs := g.functionDeclarations()

	body := append(g.seedGlobals(), g.block(genCtx{depth: 2}, g.rng.IntN(6)+6)...)
	// Every program publishes at the end of its turn, so a run has something to
	// compare whatever the shrinker took out of the body above.
	for range g.rng.IntN(2) + 2 {
		body = append(body, g.storeStatement())
	}
	body = append(body, genNode{text: []string{"__ic_yield();"}, required: true})

	constructs := make([]string, 0, len(g.constructs))
	for name := range g.constructs {
		constructs = append(constructs, name)
	}
	return generated{seed: g.seed, globals: globals, funcs: funcs, body: body, constructs: constructs}
}

// seedGlobals writes every object the program declares in the data region.
// A global nothing writes holds its zero for the whole run, which the
// optimizer proves and folds; two folded zeroes under a division are a
// constant NaN, which the compiler refuses to emit at all.
func (g *microcGen) seedGlobals() []genNode {
	var nodes []genNode
	for _, v := range g.scope {
		if v.readOnly || v.length != 0 {
			continue
		}
		value := g.boolExpr(1).text
		if v.kind == genInt {
			value = fmt.Sprintf("(%s) %% %d", g.intExpr(1, genMaxMagnitude).text, v.modulus)
		}
		if v.kind == genDouble {
			value = g.doubleReading().text
		}
		nodes = append(nodes, genNode{text: []string{fmt.Sprintf("%s = %s;", v.name, value)}})
	}
	for _, a := range g.arrays {
		nodes = append(nodes, genNode{text: []string{
			fmt.Sprintf("%s[%s] = %s;", a.name, g.index(1, a.length), g.elementValue(a))}})
	}
	return nodes
}

const genIndent = "    "

func renderInto(nodes []genNode, depth int) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n.render(depth)...)
	}
	return out
}

func (n genNode) render(depth int) []string {
	pad := strings.Repeat(genIndent, depth)
	var out []string
	if len(n.head) == 0 {
		for _, line := range n.text {
			out = append(out, pad+line)
		}
		return out
	}
	for _, line := range n.head {
		out = append(out, pad+line)
	}
	out = append(out, renderInto(n.body, depth+1)...)
	for _, line := range n.mid {
		out = append(out, pad+line)
	}
	if len(n.mid) > 0 {
		out = append(out, renderInto(n.alt, depth+1)...)
	}
	for _, line := range n.tail {
		out = append(out, pad+line)
	}
	return out
}

// render is the C23 translation unit, which is also the MicroC program.
func (p generated) render() string {
	var b strings.Builder
	for _, n := range p.globals {
		for _, line := range n.render(0) {
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("\n")
	for _, n := range p.funcs {
		for _, line := range n.render(0) {
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("void main(void) {\n    while (true) {\n")
	for _, line := range renderInto(p.body, 2) {
		b.WriteString(line + "\n")
	}
	b.WriteString("    }\n}\n")
	return b.String()
}

// countNodes is how many nodes a shrink may address.
func countNodes(nodes []genNode) int {
	total := 0
	for _, n := range nodes {
		total += 1 + countNodes(n.body) + countNodes(n.alt)
	}
	return total
}

// removeNode takes the target-th node out in pre-order, returning what is left
// and the node removed.
func removeNode(nodes []genNode, target int) ([]genNode, genNode, bool) {
	index := 0
	var removed genNode
	found := false
	var walk func([]genNode) []genNode
	walk = func(in []genNode) []genNode {
		out := make([]genNode, 0, len(in))
		for _, n := range in {
			here := index
			index++
			if here == target {
				removed = n
				found = true
				continue
			}
			n.body = walk(n.body)
			n.alt = walk(n.alt)
			out = append(out, n)
		}
		return out
	}
	result := walk(nodes)
	return result, removed, found
}

// references reports whether the rendered program names any of the given
// identifiers. It is the whole of the validity check a shrink needs: a
// node is removable only while nothing left names what it declared, so a
// removal cannot leave a use with no declaration.
func references(source string, names []string) bool {
	for _, name := range names {
		if identifierPattern(name).MatchString(source) {
			return true
		}
	}
	return false
}

var identifierPatterns = map[string]*regexp.Regexp{}

func identifierPattern(name string) *regexp.Regexp {
	if re, ok := identifierPatterns[name]; ok {
		return re
	}
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	identifierPatterns[name] = re
	return re
}

// shrink reduces a program while diverges still holds of it. Removal is
// the only move, and only whole nodes that are not required and no longer
// named. The scan restarts after every accepted removal, since removing a
// node can make an earlier one removable.
func shrink(p generated, diverges func(generated) bool) generated {
	best := p
	for again := true; again; {
		again = false
		for _, list := range []struct {
			get func(generated) []genNode
			set func(generated, []genNode) generated
		}{
			{func(p generated) []genNode { return p.body }, func(p generated, n []genNode) generated { p.body = n; return p }},
			{func(p generated) []genNode { return p.funcs }, func(p generated, n []genNode) generated { p.funcs = n; return p }},
			{func(p generated) []genNode { return p.globals }, func(p generated, n []genNode) generated { p.globals = n; return p }},
		} {
			for i := countNodes(list.get(best)) - 1; i >= 0; i-- {
				reduced, removed, ok := removeNode(list.get(best), i)
				if !ok || removed.required {
					continue
				}
				candidate := list.set(best, reduced)
				if references(candidate.render(), removed.defines) {
					continue
				}
				if !diverges(candidate) {
					continue
				}
				best = candidate
				again = true
				break
			}
			if again {
				break
			}
		}
	}
	return best
}
