package vm

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// instructionInclude is ProgrammableChip's InstructionInclude flag set. It
// selects which resolution steps an operand is allowed to take, and the masks
// below are the exact combinations the game constructs operands with. The
// combinations are not tidy: a store operand cannot see defines, and a value
// operand can, which is why `define x 5` works as an argument and not as a
// destination.
type instructionInclude uint32

const (
	includeRegisterIndex instructionInclude = 1 << iota
	includeAlias
	includeValue
	includeJumpTag
	includeDeviceIndex
	includeDefine
	includeEnum
	includeLogicType
	includeLogicSlotType
	includeLogicReagentMode
	includeLogicBatchMethod
	includeNetworkIndex
)

const (
	maskDefineValue = includeValue | includeDefine | includeEnum
	maskDeviceIndex = includeAlias | includeDeviceIndex | includeNetworkIndex
	maskStoreIndex  = includeRegisterIndex | includeAlias
	maskDoubleValue = includeRegisterIndex | includeAlias | includeValue |
		includeJumpTag | includeDefine | includeEnum

	// literalMask is the set of flags that lets an operand fall back to the
	// value it parsed while compiling. Every resolution chain tests it last, so
	// a define, an alias, a jump tag and a register all shadow a literal.
	literalMask = includeValue | includeEnum | includeLogicType |
		includeLogicSlotType | includeLogicReagentMode | includeLogicBatchMethod
)

// aliasTarget is ProgrammableChip's _AliasTarget. An alias names either a
// register or a device pin, and the two namespaces do not mix: resolving a
// device alias as a register value fails rather than reinterpreting the index.
type aliasTarget uint32

const (
	targetNone     aliasTarget = 0
	targetRegister aliasTarget = 1
	targetDevice   aliasTarget = 2
)

type aliasValue struct {
	target aliasTarget
	index  int
}

// variable is ProgrammableChip's _Operation.Variable together with
// ValueVariable, whose only additions are the two resolution helpers below.
//
// It is embedded by every concrete operand type rather than used directly.
type variable struct {
	m     *Machine
	line  int
	props instructionInclude

	regIndex   int
	regRecurse int

	// alias is the operand text with any `:network` suffix removed, and is only
	// populated when the operand is allowed to name an alias or a jump tag. The
	// game leaves it null otherwise and would throw on a dictionary lookup;
	// aliasSet is what keeps that path distinguishable.
	alias    string
	aliasSet bool
}

// newVariable builds the shared part of every operand form.
//
// The game's helpers all take a throwException flag that turns a malformed
// operand into a compile time fault. Every _Operation but _DEFINE_Operation
// constructs them with it off, and the mask _DEFINE_Operation uses carries no
// register index, so no throwing branch is reachable and none is modelled: the
// chip validates operands at run time and not before.
func newVariable(m *Machine, line int, code string, props instructionInclude) (variable, error) {
	v := variable{m: m, line: line, props: props, regIndex: -1, regRecurse: -1}
	if props&includeRegisterIndex != 0 {
		index, recurse, err := v.parseIndex(code, 'r')
		if err != nil {
			return v, err
		}
		v.regIndex, v.regRecurse = index, recurse
	}
	if props&(includeAlias|includeJumpTag) != 0 {
		if i := strings.IndexByte(code, ':'); i >= 0 {
			code = code[:i]
		}
		v.alias, v.aliasSet = code, true
	}
	return v, nil
}

// parseIndex is Variable._GetIndex. It counts a run of the prefix letter, then
// parses what follows as the index, so `r0` gives index 0 with one level of
// indirection and `rr0` gives index 0 with two.
//
// A code that is nothing but the prefix letter runs the loop off the end of the
// string, which the game surfaces as ExcUnknown: `move r 1` is a compile error
// with no useful type.
func (v *variable) parseIndex(code string, prefix byte) (index, recurse int, err error) {
	i := 0
	for {
		if i >= len(code) {
			return 0, 0, hostErrorf("operand %q is only %q characters", code, prefix)
		}
		if code[i] != prefix {
			break
		}
		i++
	}
	if i == 0 {
		return -1, -1, nil
	}
	text := code[i:]
	if j := strings.IndexByte(text, ':'); j >= 0 {
		text = text[:j]
	}
	n, ok := tryParseInt(text)
	if !ok {
		return -1, -1, nil
	}
	return n, i, nil
}

// parseDeviceIndex is Variable._GetDeviceIndex. `db` resolves to the housing's
// own index without inspecting the rest of the string, `dr...` recurses through
// registers, and everything else is a direct pin.
func (v *variable) parseDeviceIndex(code string) (index, recurse int, err error) {
	if strings.HasPrefix(code, "db") {
		return BaseUnitIndex, 0, nil
	}
	if len(code) >= 2 && code[1] == 'r' {
		return v.parseIndex(code[1:], 'r')
	}
	// The game assigns a zero recursion count before this call and the call
	// itself discards its own, so a direct pin never indirects.
	index, _, err = v.parseIndex(code, 'd')
	return index, 0, err
}

// parseNetworkIndex is Variable._GetNetworkIndex, the `d0:1` suffix. Absent or
// unparseable, it is BaseNetworkIndex, which means the device itself.
func parseNetworkIndex(code string) int {
	_, after, ok := strings.Cut(code, ":")
	if !ok {
		return BaseNetworkIndex
	}
	n, ok := tryParseInt(after)
	if !ok {
		return BaseNetworkIndex
	}
	return n
}

// aliasTargetOf is Variable.GetAliasType.
func (v *variable) aliasTargetOf(name string) (aliasTarget, error) {
	if !v.aliasSet || v.alias == "" {
		return targetNone, newFault(ExcIncorrectVariable, v.line)
	}
	value, ok := v.m.aliases[name]
	if !ok {
		return targetNone, newFault(ExcIncorrectVariable, v.line)
	}
	return value.target, nil
}

// jumpTagValue is Variable.TryParseAliasAsJumpTagValue.
func (v *variable) jumpTagValue() (int, bool) {
	if !v.aliasSet {
		return 0, false
	}
	line, ok := v.m.jumpTags[v.alias]
	return line, ok
}

// defineValue is the _Defines lookup every value-carrying operand tries first.
// The game would throw on a null key here; an operand that cannot name an alias
// must not be asking, so reaching it is a defect in this package rather than a
// chip behaviour.
func (v *variable) defineValue() (float64, bool, error) {
	if !v.aliasSet {
		return 0, false, hostErrorf("define lookup on an operand with no alias text")
	}
	value, ok := v.m.defines[v.alias]
	return value, ok, nil
}

// aliasAsIndex is IndexVariable.TryParseAliasAsIndex.
func (v *variable) aliasAsIndex(target aliasTarget) (int, bool) {
	if !v.aliasSet {
		return -1, false
	}
	value, ok := v.m.aliases[v.alias]
	if !ok || value.target != target {
		return -1, false
	}
	return value.index, true
}

// aliasAsValue is ValueVariable.TryParseAliasAsValue. Only a register alias
// carries a value; a device alias resolves to nothing here.
func (v *variable) aliasAsValue(target aliasTarget, throwException bool) (float64, bool, error) {
	if !v.aliasSet || target != targetRegister {
		return math.NaN(), false, nil
	}
	value, ok := v.m.aliases[v.alias]
	if !ok || value.target != target {
		return math.NaN(), false, nil
	}
	if value.index < 0 || value.index >= len(v.m.registers) {
		if throwException {
			return 0, false, newFault(ExcOutOfRegisterBounds, v.line)
		}
		return math.NaN(), false, nil
	}
	return v.m.registers[value.index], true, nil
}

// registerAsIndex is IndexVariable.TryParseRegisterIndexAsIndex.
//
// Indirection stops one level short of the register count, so `r0` yields the
// literal index 0 and only `rr0` reads a register to get one. The bound applied
// at each hop is the whole file, 18 registers, so an indirect reference can
// land on sp or ra. The first hop indexes the array before any check, so an
// index like `rr99` is ExcUnknown rather than ExcOutOfRegisterBounds.
func (v *variable) registerAsIndex(throwException bool) (int, bool, error) {
	if v.regIndex < 0 || v.regRecurse < 0 {
		return -1, false, nil
	}
	index := v.regIndex
	for recurse := v.regRecurse; recurse > 1; recurse-- {
		if index < 0 || index >= len(v.m.registers) {
			return 0, false, hostErrorf("indirect register index %d outside the file", index)
		}
		index = int32Saturating(math.RoundToEven(v.m.registers[index]))
		if index < 0 || index >= len(v.m.registers) {
			if throwException {
				return 0, false, newFault(ExcOutOfRegisterBounds, v.line)
			}
			return -1, false, nil
		}
	}
	return index, true, nil
}

// registerAsValue is ValueVariable.TryParseRegisterIndexAsValue.
//
// Unlike registerAsIndex it has no bounds check after the loop, so a plain out
// of range register read such as `move r0 r99` is ExcUnknown, while the same
// text as a destination is ExcOutOfRegisterBounds.
func (v *variable) registerAsValue(throwException bool) (float64, bool, error) {
	if v.regIndex < 0 || v.regRecurse < 0 {
		return math.NaN(), false, nil
	}
	index := v.regIndex
	for recurse := v.regRecurse; recurse > 1; recurse-- {
		if index < 0 || index >= len(v.m.registers) {
			return 0, false, hostErrorf("indirect register index %d outside the file", index)
		}
		index = int32Saturating(math.RoundToEven(v.m.registers[index]))
		if index < 0 || index >= len(v.m.registers) {
			if throwException {
				return 0, false, newFault(ExcOutOfRegisterBounds, v.line)
			}
			return math.NaN(), false, nil
		}
	}
	if index < 0 || index >= len(v.m.registers) {
		return 0, false, hostErrorf("register index %d outside the file", index)
	}
	return v.m.registers[index], true, nil
}

// doubleValueVariable is _Operation.DoubleValueVariable, the operand form
// behind every `a(r?|num)` position.
type doubleValueVariable struct {
	variable
	value float64
}

func newDoubleValueVariable(m *Machine, line int, code string, props instructionInclude) (doubleValueVariable, error) {
	base, err := newVariable(m, line, code, props)
	d := doubleValueVariable{variable: base, value: math.NaN()}
	if err != nil {
		return d, err
	}
	if value, ok := resolveScriptEnum(code, props); ok {
		d.value = float64(value)
		return d, nil
	}
	if props&includeValue == 0 {
		return d, nil
	}
	if constant, ok := lookupConstant(code); ok {
		d.value = constant
		return d, nil
	}
	if value, ok := tryParseDouble(code); ok {
		d.value = value
	}
	return d, nil
}

// literal is what the parsed constant or number was, without consulting the
// machine. Only `define` uses it, to fix a value at compile time.
func (d *doubleValueVariable) literal() float64 { return d.value }

// value resolution order is Define, alias, jump tag, register, literal, which
// is why a define shadows a register alias of the same name.
//
// A literal that resolved to NaN is treated as unset, so the `nan` constant
// cannot be used as an operand directly: it falls through to
// ExcIncorrectVariable. Reaching NaN needs a define or a register.
func (d *doubleValueVariable) resolve(target aliasTarget, errorAtEnd bool) (float64, error) {
	if target&targetRegister == 0 {
		return 0, newFault(ExcIncorrectVariable, d.line)
	}
	if d.props&includeDefine != 0 {
		value, ok, err := d.defineValue()
		if err != nil {
			return 0, err
		}
		if ok {
			return value, nil
		}
	}
	if d.props&includeAlias != 0 {
		value, ok, err := d.aliasAsValue(target, errorAtEnd)
		if err != nil {
			return 0, err
		}
		if ok {
			return value, nil
		}
	}
	if d.props&includeJumpTag != 0 {
		if line, ok := d.jumpTagValue(); ok {
			return float64(line), nil
		}
	}
	if d.props&includeRegisterIndex != 0 {
		value, ok, err := d.registerAsValue(errorAtEnd)
		if err != nil {
			return 0, err
		}
		if ok {
			return value, nil
		}
	}
	if d.props&literalMask != 0 && !math.IsNaN(d.value) {
		return d.value, nil
	}
	if errorAtEnd {
		return 0, newFault(ExcIncorrectVariable, d.line)
	}
	return math.NaN(), nil
}

// resolveLong is DoubleValueVariable.GetVariableLong, the entry point every
// bitwise and shift instruction shares. The range guards fire before the
// conversion, so an operand beyond a signed 64 bit range is a shift fault
// rather than a wrapped value; everything inside the range then goes through
// the 53 bit round trip.
func (d *doubleValueVariable) resolveLong(target aliasTarget, signed bool) (int64, error) {
	value, err := d.resolve(target, true)
	if err != nil {
		return 0, err
	}
	if value < -9.223372036854776e+18 {
		return 0, newFault(ExcShiftUnderflow, d.line)
	}
	if value > 9.223372036854776e+18 {
		return 0, newFault(ExcShiftOverflow, d.line)
	}
	return DoubleToLong(value, signed), nil
}

// resolveInt is DoubleValueVariable.GetVariableInt, used for shift distances
// and bit offsets. NaN passes both range guards and converts to zero.
func (d *doubleValueVariable) resolveInt(target aliasTarget) (int, error) {
	value, err := d.resolve(target, true)
	if err != nil {
		return 0, err
	}
	if value < -2147483648.0 {
		return 0, newFault(ExcShiftUnderflow, d.line)
	}
	if value > 2147483647.0 {
		return 0, newFault(ExcShiftOverflow, d.line)
	}
	return int32Saturating(value), nil
}

// intValuedVariable is _Operation.IntValuedVariable, the operand form behind
// slot indices, reference ids, prefab hashes and name hashes.
type intValuedVariable struct {
	variable
	value int
	isSet bool
}

func newIntValuedVariable(m *Machine, line int, code string, props instructionInclude) (intValuedVariable, error) {
	base, err := newVariable(m, line, code, props)
	iv := intValuedVariable{variable: base, value: -1}
	if err != nil {
		return iv, err
	}
	if value, ok := resolveScriptEnum(code, props); ok {
		iv.value, iv.isSet = value, true
		return iv, nil
	}
	if props&includeValue != 0 {
		if constant, ok := lookupConstant(code); ok {
			// The game returns here without marking the value set, so a named
			// constant in an integer position is accepted at compile time and
			// then fails at run time with ExcInvalidInteger.
			iv.value = int32Saturating(constant)
			return iv, nil
		}
		if value, ok := tryParseInt(code); ok {
			iv.value, iv.isSet = value, true
		} else {
			iv.value = 0
		}
	}
	return iv, nil
}

// resolveNamedInt is the alias, define and jump tag prefix that
// IntValuedVariable, LineNumberVariable and EnumValuedVariable share, in the
// order all three try them. It is the opposite order to the double form, which
// is why a define shadows a register alias there and not here.
//
// Every arm truncates. Only the register arm the three take next differs, so
// that is left to each of them.
func (v *variable) resolveNamedInt(target aliasTarget, throwException bool) (int, bool, error) {
	if target&targetRegister != 0 {
		value, ok, err := v.aliasAsValue(target, throwException)
		if err != nil {
			return 0, false, err
		}
		if ok {
			return int32Saturating(value), true, nil
		}
	}
	if v.props&includeDefine != 0 {
		value, ok, err := v.defineValue()
		if err != nil {
			return 0, false, err
		}
		if ok {
			return int32Saturating(value), true, nil
		}
	}
	if v.props&includeJumpTag != 0 {
		if line, ok := v.jumpTagValue(); ok {
			return line, true, nil
		}
	}
	return 0, false, nil
}

// resolve is IntValuedVariable.GetVariableValue.
func (iv *intValuedVariable) resolve(target aliasTarget, throwException bool) (int, error) {
	value, ok, err := iv.resolveNamedInt(target, throwException)
	if err != nil {
		return 0, err
	}
	if ok {
		return value, nil
	}
	if iv.props&includeRegisterIndex != 0 {
		value, ok, err := iv.registerAsValue(throwException)
		if err != nil {
			return 0, err
		}
		if ok {
			return int32Saturating(math.RoundToEven(value)), nil
		}
	}
	if iv.props&literalMask != 0 && iv.isSet {
		return iv.value, nil
	}
	if throwException {
		return 0, newFault(ExcInvalidInteger, iv.line)
	}
	return 0, nil
}

// lineNumberVariable is _Operation.LineNumberVariable, the jump target operand.
// Unlike the other forms it parses only a plain integer literal, so a named
// constant is never a jump target.
type lineNumberVariable struct {
	variable
	value int
	isSet bool
}

func newLineNumberVariable(m *Machine, line int, code string, props instructionInclude) (lineNumberVariable, error) {
	base, err := newVariable(m, line, code, props)
	lv := lineNumberVariable{variable: base}
	if err != nil {
		return lv, err
	}
	if props&includeValue != 0 {
		if value, ok := tryParseInt(code); ok {
			lv.value, lv.isSet = value, true
		}
	}
	return lv, nil
}

// resolve is LineNumberVariable.GetVariableValue. It rounds a register to even
// the way the integer form does, and reports an unresolvable operand as
// ExcIncorrectVariable rather than ExcInvalidInteger.
func (lv *lineNumberVariable) resolve(target aliasTarget, throwException bool) (int, error) {
	value, ok, err := lv.resolveNamedInt(target, throwException)
	if err != nil {
		return 0, err
	}
	if ok {
		return value, nil
	}
	if lv.props&includeRegisterIndex != 0 {
		value, ok, err := lv.registerAsValue(throwException)
		if err != nil {
			return 0, err
		}
		if ok {
			return int32Saturating(math.RoundToEven(value)), nil
		}
	}
	if lv.props&literalMask != 0 && lv.isSet {
		return lv.value, nil
	}
	if throwException {
		return 0, newFault(ExcIncorrectVariable, lv.line)
	}
	return -1, nil
}

// enumValuedVariable is _Operation.EnumValuedVariable, the logic type, slot
// type, batch mode and reagent mode operands.
//
// It resolves its own enum first, then named constants, then a plain integer,
// and the result is never validated against the enum on the way out: an
// undefined ordinal reaches the device unchanged.
type enumValuedVariable struct {
	variable
	value int
	isSet bool
}

func newEnumValuedVariable(m *Machine, line int, code string, props instructionInclude, defined func(string) (int, bool)) (enumValuedVariable, error) {
	base, err := newVariable(m, line, code, props)
	ev := enumValuedVariable{variable: base, value: -1}
	if err != nil {
		return ev, err
	}
	if props&includeValue == 0 {
		return ev, nil
	}
	if value, ok := defined(code); ok {
		ev.value, ev.isSet = value, true
		return ev, nil
	}
	if constant, ok := lookupConstant(code); ok {
		ev.value, ev.isSet = int32Saturating(constant), true
		return ev, nil
	}
	if value, ok := tryParseInt(code); ok {
		ev.value, ev.isSet = value, true
	}
	return ev, nil
}

// resolve is EnumValuedVariable.GetVariableValue.
//
// Two things separate it from the other two integer forms and neither is a
// transcription slip. Its register arm truncates where IntValuedVariable and
// LineNumberVariable round to even, so `l r0 d0 r1` with r1 holding 12.5 reads
// property 12 rather than 13. Its literal arm tests includeValue alone rather
// than the whole literalMask, which costs nothing: every operand position built
// with an enum flag carries includeValue too.
func (ev *enumValuedVariable) resolve(target aliasTarget, throwException bool) (int, error) {
	value, ok, err := ev.resolveNamedInt(target, throwException)
	if err != nil {
		return 0, err
	}
	if ok {
		return value, nil
	}
	if ev.props&includeRegisterIndex != 0 {
		value, ok, err := ev.registerAsValue(throwException)
		if err != nil {
			return 0, err
		}
		if ok {
			return int32Saturating(value), nil
		}
	}
	if ev.props&includeValue != 0 && ev.isSet {
		return ev.value, nil
	}
	if throwException {
		return 0, newFault(ExcIncorrectVariable, ev.line)
	}
	return 0, nil
}

// indexVariable is _Operation.IndexVariable, the destination register operand.
type indexVariable struct {
	intValuedVariable
}

func newIndexVariable(m *Machine, line int, code string, props instructionInclude) (indexVariable, error) {
	base, err := newIntValuedVariable(m, line, code, props)
	return indexVariable{intValuedVariable: base}, err
}

// resolveIndex is IndexVariable.GetVariableIndex. The trailing range checks
// only run when throwError is set, which is how a device operand can carry an
// out of range index all the way to the housing and fault there instead.
func (iv *indexVariable) resolveIndex(target aliasTarget, throwError bool) (int, error) {
	index := 0
	resolved := false
	if iv.props&includeDefine != 0 {
		value, ok, err := iv.defineValue()
		if err != nil {
			return 0, err
		}
		if ok {
			index, resolved = int32Saturating(value), true
		}
	}
	if !resolved && iv.props&includeAlias != 0 {
		if value, ok := iv.aliasAsIndex(target); ok {
			index, resolved = value, true
		}
	}
	if !resolved && iv.props&includeJumpTag != 0 {
		if value, ok := iv.jumpTagValue(); ok {
			index, resolved = value, true
		}
	}
	if !resolved && iv.props&includeRegisterIndex != 0 {
		value, ok, err := iv.registerAsIndex(throwError)
		if err != nil {
			return 0, err
		}
		if ok {
			index, resolved = value, true
		}
	}
	if !resolved && throwError {
		return 0, newFault(ExcIncorrectVariable, iv.line)
	}
	if throwError {
		if target&targetRegister != 0 && (index < 0 || index >= len(iv.m.registers)) {
			return 0, newFault(ExcOutOfRegisterBounds, iv.line)
		}
		if target&targetDevice != 0 && !iv.m.housing.isValidIndex(index) {
			return 0, newFault(ExcOutOfDeviceBounds, iv.line)
		}
	}
	return index, nil
}

// deviceOperand is _Operation.IDeviceVariable: the three shapes a `device`
// position can take, unified by how they find a device.
type deviceOperand interface {
	// device resolves the operand against the housing. A nil device with a nil
	// error is the game's null, which most instructions turn into
	// ExcDeviceNotFound and sdse/sdns turn into a zero or one.
	device(m *Machine) (Device, error)
}

// directDeviceVariable is _Operation.DirectDeviceVariable: an operand holding a
// reference id rather than a pin.
type directDeviceVariable struct {
	intValuedVariable
	network int
}

func (d *directDeviceVariable) device(m *Machine) (Device, error) {
	id, err := d.resolve(targetRegister, true)
	if err != nil {
		return nil, err
	}
	return m.housing.logicableFromID(id, d.network), nil
}

// deviceIndexVariable is _Operation.DeviceIndexVariable: `db`, `d0` through
// `d5`, and the indirect `dr` forms.
type deviceIndexVariable struct {
	indexVariable
	deviceIndex   int
	deviceRecurse int
	network       int
}

func (d *deviceIndexVariable) device(m *Machine) (Device, error) {
	index, err := d.resolveIndex(targetDevice, false)
	if err != nil {
		return nil, err
	}
	return m.housing.logicableFromIndex(index, d.network)
}

// resolveIndex overrides IndexVariable.GetVariableIndex. It never consults
// defines and falls back to the network number when there is no pin, which is
// what makes a bare `:1` style operand resolve to a network rather than fail.
func (d *deviceIndexVariable) resolveIndex(target aliasTarget, throwError bool) (int, error) {
	if d.props&includeAlias != 0 {
		if index, ok := d.aliasAsIndex(target); ok {
			return index, nil
		}
	}
	if d.props&includeDeviceIndex != 0 {
		index, ok, err := d.deviceIndexAsIndex(throwError)
		if err != nil {
			return 0, err
		}
		if ok {
			return index, nil
		}
	}
	if d.props&includeNetworkIndex != 0 {
		return d.network, nil
	}
	if throwError {
		return 0, newFault(ExcIncorrectVariable, d.line)
	}
	return 0, nil
}

// deviceIndexAsIndex is DeviceIndexVariable.TryParseDeviceIndexAsIndex. Its
// loop runs one more time than the register one, because a direct pin carries a
// recursion count of zero rather than one.
func (d *deviceIndexVariable) deviceIndexAsIndex(throwException bool) (int, bool, error) {
	if d.deviceIndex < 0 || d.deviceRecurse < 0 {
		return -1, false, nil
	}
	index := d.deviceIndex
	for recurse := d.deviceRecurse; recurse > 0; recurse-- {
		if index < 0 || index >= len(d.m.registers) {
			return 0, false, hostErrorf("indirect device index %d outside the register file", index)
		}
		index = int32Saturating(math.RoundToEven(d.m.registers[index]))
		if index < 0 || index >= len(d.m.registers) {
			if throwException {
				return 0, false, newFault(ExcOutOfDeviceBounds, d.line)
			}
			return -1, false, nil
		}
	}
	return index, true, nil
}

func newDeviceIndexVariable(m *Machine, line int, code string, props instructionInclude) (*deviceIndexVariable, error) {
	base, err := newIndexVariable(m, line, code, props)
	d := &deviceIndexVariable{indexVariable: base, deviceIndex: -1, deviceRecurse: -1, network: BaseNetworkIndex}
	if err != nil {
		return d, err
	}
	if props&includeDeviceIndex != 0 {
		index, recurse, err := d.parseDeviceIndex(code)
		if err != nil {
			return d, err
		}
		d.deviceIndex, d.deviceRecurse = index, recurse
	}
	if props&includeNetworkIndex != 0 {
		d.network = parseNetworkIndex(code)
	}
	return d, nil
}

// deviceAliasVariable is _Operation.DeviceAliasVariable: an operand naming an
// alias, which may point at either a pin or a register holding a reference id.
type deviceAliasVariable struct {
	indexVariable
	network int
}

func (d *deviceAliasVariable) device(m *Machine) (Device, error) {
	if !d.aliasSet || d.alias == "" {
		index, err := d.resolveIndex(targetDevice, false)
		if err != nil {
			return nil, err
		}
		return m.housing.logicableFromIndex(index, d.network)
	}
	target, err := d.aliasTargetOf(d.alias)
	if err != nil {
		return nil, err
	}
	switch target {
	case targetDevice:
		index, err := d.resolveIndex(target, false)
		if err != nil {
			return nil, err
		}
		return m.housing.logicableFromIndex(index, d.network)
	case targetRegister:
		id, err := d.resolve(target, false)
		if err != nil {
			return nil, err
		}
		return m.housing.logicableFromID(id, d.network), nil
	case targetNone:
	}
	// The game's switch default. GetAliasType reports an unknown name as
	// ExcIncorrectVariable before reaching here and no alias is ever recorded
	// with target None, so nothing gets this far.
	return nil, newFault(ExcAliasNotFound, d.line)
}

func newDeviceAliasVariable(m *Machine, line int, code string, props instructionInclude) (*deviceAliasVariable, error) {
	base, err := newIndexVariable(m, line, code, props)
	d := &deviceAliasVariable{indexVariable: base, network: BaseNetworkIndex}
	if err != nil {
		return d, err
	}
	if props&includeNetworkIndex != 0 {
		d.network = parseNetworkIndex(code)
	}
	return d, nil
}

func newDirectDeviceVariable(m *Machine, line int, code string, props instructionInclude) (*directDeviceVariable, error) {
	base, err := newIntValuedVariable(m, line, code, props)
	d := &directDeviceVariable{intValuedVariable: base, network: BaseNetworkIndex}
	if err != nil {
		return d, err
	}
	if props&includeNetworkIndex != 0 {
		d.network = parseNetworkIndex(code)
	}
	return d, nil
}

// deviceRegisterPattern, devicePinPattern and the ordering below are
// _Operation._MakeDeviceVariable. Which of the three operand shapes a device
// position gets is decided by the text alone, at compile time, and the shapes
// resolve very differently: only the pin form reaches a housing socket.
var (
	deviceRegisterPattern = regexp.MustCompile(`^r+(?:1[0-5]|[0-9])$`)
	devicePinPattern      = regexp.MustCompile(`^(d[0-9]|dr*[r0-9][0-9])$`)
)

func newDeviceOperand(m *Machine, line int, code string) (deviceOperand, error) {
	if deviceRegisterPattern.MatchString(code) {
		return newDirectDeviceVariable(m, line, code, maskDoubleValue|includeDeviceIndex)
	}
	if _, ok := m.defines[code]; ok {
		return newDirectDeviceVariable(m, line, code, maskDoubleValue)
	}
	if code != "" && (code[0] == '$' || code[0] == '%' || (code[0] >= '0' && code[0] <= '9')) {
		return newDirectDeviceVariable(m, line, code, maskDoubleValue|includeDeviceIndex|includeNetworkIndex)
	}
	if len(code) > 1 && code[0] == 'r' && code[1] >= '0' && code[1] <= '9' {
		return newDirectDeviceVariable(m, line, code, maskDoubleValue|includeDeviceIndex|includeNetworkIndex)
	}
	head, _, _ := strings.Cut(code, ":")
	if strings.HasPrefix(head, "d") && (head == "db" || devicePinPattern.MatchString(head)) {
		return newDeviceIndexVariable(m, line, code, maskDeviceIndex)
	}
	return newDeviceAliasVariable(m, line, code, maskDoubleValue|includeDeviceIndex|includeNetworkIndex)
}

// tryParseInt is C# int.TryParse with the default NumberStyles.Integer: an
// optional leading sign, decimal digits, and a signed 32 bit range. It accepts
// no separators, no decimal point and no exponent, which is narrower than Go's
// own parsing.
func tryParseInt(s string) (int, bool) {
	s = strings.TrimSpace(s)
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, false
	}
	return int(n), true
}

// tryParseDouble is C# double.TryParse with NumberStyles.Number and the
// invariant culture. That style allows a leading or trailing sign, a decimal
// point and thousands separators, and notably does not allow an exponent: `1e5`
// is not a number to the chip's assembler. Values beyond the double range parse
// as an infinity rather than failing, matching .NET Core.
func tryParseDouble(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	negative := false
	switch {
	case s[0] == '+' || s[0] == '-':
		negative = s[0] == '-'
		s = s[1:]
	case s[len(s)-1] == '+' || s[len(s)-1] == '-':
		negative = s[len(s)-1] == '-'
		s = s[:len(s)-1]
	}
	var digits strings.Builder
	seenPoint, seenDigit := false, false
	for i := range len(s) {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			seenDigit = true
			digits.WriteByte(c)
		case c == ',':
			// A thousands separator is allowed only among the integral digits.
			if seenPoint {
				return 0, false
			}
		case c == '.':
			if seenPoint {
				return 0, false
			}
			seenPoint = true
			digits.WriteByte(c)
		default:
			return 0, false
		}
	}
	if !seenDigit {
		return 0, false
	}
	value, err := strconv.ParseFloat(digits.String(), 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, false
	}
	if negative {
		value = -value
	}
	return value, true
}
