package emit

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/mir"
)

// Errors reported while rendering an operand. Both mean the pipeline is in the
// wrong state rather than that the program is too big, so they are returned as
// errors instead of recorded as budget violations.
var (
	ErrVirtualRegister = errors.New("virtual register reached emission; register allocation has not run")
	// ErrUnresolvedLabel is a label naming no block, or one the readable form holds no mangled name
	// for — either way the renderer was handed a program the label tables were not built from.
	ErrUnresolvedLabel = errors.New("label names no block")
	// ErrUnspellableOperand is an operand with no text the chip's assembler reads. Nothing about an
	// operand is checked as the chip compiles — every one is built with throwException false — so a
	// bad operand pastes cleanly and faults at execution every tick, or resolves to something else entirely.
	ErrUnspellableOperand = errors.New("operand has no spelling the chip accepts")
	// ErrUnmaterialisedLiteral is a literal naming a value the chip's operand parser cannot
	// reproduce: a NaN, which it reports as no value at all, or a negative zero, which it loads as
	// +0.0. mir.MaterialiseUnreadable replaces every such operand with arithmetic that computes the
	// value, so reaching here means that pass did not run.
	ErrUnmaterialisedLiteral = errors.New("a value with no literal reached emission; materialisation has not run")
)

// renderer holds everything operand rendering needs beyond the operand itself.
type renderer struct {
	numeric bool
	// lineOf maps a MIR block label to the line its first instruction lands
	// on. Branches are absolute, so this is what a Label operand becomes.
	lineOf map[string]int
	// names maps a MIR block label to its mangled emitted name. Only the
	// readable form's comments spell a label, so it is nil otherwise.
	names map[string]string
	// count is how many lines the program emits, which is the one target no
	// line holds: a branch there ends the program.
	count int
}

// instr renders one instruction as one line: the mnemonic, then its operands
// separated by single spaces. An operandless form renders as the bare
// mnemonic with no trailing space.
func (r renderer) instr(instr *mir.Instr) (string, error) {
	var b strings.Builder
	b.WriteString(instr.Mnemonic())
	for i, arg := range instr.Args {
		text, err := r.operand(arg)
		if err != nil {
			return "", fmt.Errorf("%s operand %d: %w", instr.Mnemonic(), i, err)
		}
		b.WriteByte(' ')
		b.WriteString(text)
	}
	return b.String(), nil
}

func (r renderer) operand(arg mir.Operand) (string, error) {
	switch a := arg.(type) {
	case mir.VirtReg:
		return "", fmt.Errorf("%s: %w", a, ErrVirtualRegister)
	case mir.PhysReg:
		// mir's constructors already refuse an out-of-range register; what reaches here is a struct
		// literal built directly, which Register.String falls back to a Go debug form for.
		if a.Reg >= ic10.NumRegisters {
			return "", fmt.Errorf("%q: %w", a.Reg.String(), ErrUnspellableOperand)
		}
		return a.Reg.String(), nil
	case mir.Imm:
		return formatImm(a.Value)
	case mir.Label:
		return r.label(a)
	case mir.Device:
		return device(a)
	case mir.LogicType:
		return r.enum(int64(a.Value), logicTypeNames)
	case mir.LogicSlotType:
		return r.enum(int64(a.Value), logicSlotTypeNames)
	case mir.BatchMode:
		return r.enum(int64(a.Value), batchModeNames)
	case mir.ReagentMode:
		return r.enum(int64(a.Value), reagentModeNames)
	default:
		return "", fmt.Errorf("operand %s has no emitted form", arg)
	}
}

// maxConnection is the largest network index the chip reads back. Variable._GetNetworkIndex parses
// the ":n" suffix with int.TryParse and answers int.MinValue for anything it will not read, which
// CircuitHousing.GetLogicableFromIndex treats as no suffix at all — so an operand naming a larger
// index silently reaches the pin's device instead of the network it spells.
const maxConnection = math.MaxInt32

// device renders a device operand, refusing the ones whose emitted text would not be the operand the
// program built: a spelling Device.String drops part of, a connection index the chip's parser will
// not read back, a pin index the housing has no pin for, and a kind with no spelling at all. Whether
// a device is actually wired to a pin is decided in-game and not checked here.
func device(d mir.Device) (string, error) {
	switch d.Kind {
	case mir.DeviceBase:
		// db:n is a spelling the chip reads and resolves to the housing's own network at that index,
		// but Device.String writes bare "db" and drops Conn, so letting one through here would emit
		// an operand naming the housing where the program asked for a network on it.
		if d.Conn != mir.NoConnection {
			return "", fmt.Errorf("db:%d has no emitted form: Device.String drops the connection index and would spell a bare db: %w", d.Conn, ErrUnspellableOperand)
		}
	case mir.DevicePin:
		// A housing has six pins; CircuitHousing.Devices is a six-element array, so an out-of-range
		// pin index throws, the run loop records it as the unknown error, and the chip stops on that
		// line and raises it again every tick.
		if d.Pin >= ic10.NumDevicePins {
			return "", fmt.Errorf("d%d is outside d0-d%d: %w", d.Pin, ic10.NumDevicePins-1, ErrUnspellableOperand)
		}
		if d.Conn < mir.NoConnection || d.Conn > maxConnection {
			return "", fmt.Errorf("d%d carries network connection %d, which the chip reads as no suffix at all: %w", d.Pin, d.Conn, ErrUnspellableOperand)
		}
	default:
		return "", fmt.Errorf("%q: %w", d.String(), ErrUnspellableOperand)
	}
	return d.String(), nil
}

// label renders a branch target, which is a line number in both forms. The
// readable form spells the name in the comment beside it.
func (r renderer) label(l mir.Label) (string, error) {
	line, defined := r.lineOf[l.Name]
	if !defined {
		return "", fmt.Errorf("%q: %w", l.Name, ErrUnresolvedLabel)
	}
	return strconv.Itoa(line), nil
}

// annotate appends the trailing comment readable output carries: the blocks that start on this line,
// then the block each of the instruction's branch targets names. An instruction with nothing to say
// keeps its bare text, since a '#' with nothing after it would spend bytes on nothing. A target with
// no line of its own — a trailing empty block, which resolves one past the end where
// ProgrammableChip.Execute stops without raising — is marked "(end)" so its name still appears somewhere.
func (r renderer) annotate(text string, starting []string, instr *mir.Instr) (string, error) {
	var b strings.Builder
	for _, name := range starting {
		b.WriteString(" " + name + ":")
	}
	for _, arg := range instr.Args {
		target, ok := arg.(mir.Label)
		if !ok {
			continue
		}
		name, err := r.name(target.Name)
		if err != nil {
			return "", err
		}
		b.WriteString(" -> " + name)
		if r.lineOf[target.Name] >= r.count {
			b.WriteString(" (end)")
		}
	}
	if b.Len() == 0 {
		return text, nil
	}
	return text + " #" + b.String(), nil
}

// name gives the emitted spelling of a block label, which readable output writes into the comment on
// the line the block starts at and on every branch reaching it: the same lookup and the same
// failure, so a name the mangling missed cannot annotate a line with nothing.
func (r renderer) name(label string) (string, error) {
	name, named := r.names[label]
	if !named {
		return "", fmt.Errorf("%q has no emitted name: %w", label, ErrUnresolvedLabel)
	}
	return name, nil
}

// enum renders one of the operand enums. The name is the default; it and its integer resolve
// identically on the chip, so the difference is bytes alone. The value is formatted signed rather
// than widened to uint64 first: BatchMode and ReagentMode are backed by int32, and a negative widened
// first would spell as its twenty-digit complement, which int.TryParse will not read, leaving
// EnumValuedVariable unset until GetVariableValue raises IncorrectVariable every tick.
func (r renderer) enum(value int64, names map[uint64]string) (string, error) {
	if !r.numeric {
		if name, ok := names[uint64(value)]; ok {
			return name, nil
		}
	}
	return strconv.FormatInt(value, 10), nil
}

// immFullDigits is the width [formatImm] widens to when [spellsExactly] cannot certify the shortest
// expansion, and immDigitGate is the digit count below which widening is skipped as unneeded. The
// chip's parser, Mono's double.TryParse, is not correctly rounded, so strconv's shortest-round-trip
// expansion does not guarantee what the chip loads; these widths are empirical margins measured
// against the pinned game image, not derived. Neither saves a subnormal — see [formatImm].
const (
	immFullDigits = 17
	immDigitGate  = 12
)

// formatImm renders a double as a literal the chip's assembler accepts. Exponent notation is never
// produced: the chip parses an operand with double.TryParse under NumberStyles.Number, which admits
// no exponent, so "1e300" would raise IncorrectVariable every tick. A value with no spelling at all
// is refused rather than approximated; see [ErrUnmaterialisedLiteral]. Subnormals need at least 310
// fixed-notation characters, which [MaxLineLength] refuses for all but the smallest, held in the
// constant table as epsilon and reached through [shortestSpelling] instead.
func formatImm(value float64) (string, error) {
	// The value is named by its bits rather than by %v, which spells a negative
	// zero "-0" and would put the text this refuses inside the refusal.
	if unreadable, ok := ic10.Unreadable(value); ok {
		return "", fmt.Errorf("%016x: %s: %w", math.Float64bits(value), unreadable.Reason, ErrUnmaterialisedLiteral)
	}
	switch {
	case math.IsInf(value, 1):
		return "pinf", nil
	case math.IsInf(value, -1):
		return "ninf", nil
	}
	expansion := strconv.FormatFloat(value, 'f', -1, 64)
	if significantDigits(value) >= immDigitGate && !spellsExactly(value, expansion) {
		full, err := fixedNotation(value, immFullDigits)
		if err != nil {
			return "", err
		}
		expansion = full
	}
	return shortestSpelling(constantNames, value, expansion), nil
}

// spellsExactly reports whether text denotes value with nothing rounded away. value must be finite,
// which is what the switch opening [formatImm] leaves. The comparison runs over exact rationals and
// consults no parser: reading text back with strconv would ask whether a correctly rounded parser
// agrees with itself, the assumption under test rather than a check on it. An exact expansion needs
// none of [immFullDigits]'s widening, since there is no rounding left to get wrong — which is every
// integer and bit mask up to 2^53, the shape a MicroC program writes most of.
func spellsExactly(value float64, text string) bool {
	written, ok := new(big.Rat).SetString(text)
	if !ok {
		return false
	}
	return written.Cmp(new(big.Rat).SetFloat64(value)) == 0
}

// significantDigits counts the digits value's shortest expansion rests on, which is what
// immDigitGate is compared against. The count comes from the scientific form rather than the fixed
// one, since the fixed form of 1e11 spells twelve digit characters for one significant digit.
func significantDigits(value float64) int {
	mantissa, _, _ := strings.Cut(strconv.FormatFloat(value, 'e', -1, 64), "e")
	count := 0
	for i := range len(mantissa) {
		if mantissa[i] >= '0' && mantissa[i] <= '9' {
			count++
		}
	}
	return count
}

// fixedNotation renders value across exactly digits significant digits with no exponent, which is
// the only notation the chip reads. strconv's 'f' verb takes fractional rather than significant
// digits, so this takes the digits from the scientific form and moves the point instead. It returns
// an error rather than falling back to the shortest expansion, which would be the silent miscompile this width exists to close.
func fixedNotation(value float64, digits int) (string, error) {
	scientific := strconv.FormatFloat(value, 'e', digits-1, 64)
	mantissa, exponent, _ := strings.Cut(scientific, "e")
	power, err := strconv.Atoi(exponent)
	if err != nil {
		return "", fmt.Errorf("scientific form %q of %v carries no exponent: %w", scientific, value, err)
	}
	sign := ""
	if unsigned, negative := strings.CutPrefix(mantissa, "-"); negative {
		sign, mantissa = "-", unsigned
	}
	mantissa = strings.Replace(mantissa, ".", "", 1)
	switch {
	case power >= len(mantissa)-1:
		return sign + mantissa + strings.Repeat("0", power-len(mantissa)+1), nil
	case power >= 0:
		return sign + mantissa[:power+1] + "." + mantissa[power+1:], nil
	default:
		return sign + "0." + strings.Repeat("0", -power-1) + mantissa, nil
	}
}

// shortestSpelling picks between a literal's decimal expansion and the named constant standing for
// the same bits. Both resolve to one double on the chip, so the choice is bytes alone; a tie goes to
// the expansion, which needs no constant table to resolve.
func shortestSpelling(names map[uint64]string, value float64, text string) string {
	if name, ok := names[math.Float64bits(value)]; ok && len(name) < len(text) {
		return name
	}
	return text
}

// Reverse indexes over the target's enums, mapping each value to the name the emitter prints for it.
// A value with several spellings takes the first that is not deprecated; one whose only spelling is
// deprecated takes it anyway, since the game still resolves a deprecated member.
var (
	logicTypeNames     = reverseIndex(ic10.LogicTypes, func(t ic10.LogicTypeInfo) (uint64, string, bool) { return uint64(t.Value), t.Name, t.Deprecated })
	logicSlotTypeNames = reverseIndex(ic10.LogicSlotTypes, func(t ic10.LogicSlotTypeInfo) (uint64, string, bool) { return uint64(t.Value), t.Name, t.Deprecated })
	batchModeNames     = reverseIndex(ic10.BatchModes, func(m ic10.BatchModeInfo) (uint64, string, bool) { return uint64(m.Value), m.Name, m.Deprecated })
	reagentModeNames   = reverseIndex(ic10.ReagentModes, func(m ic10.ReagentModeInfo) (uint64, string, bool) { return uint64(m.Value), m.Name, m.Deprecated })
	constantNames      = buildConstantNames(ic10.Constants)
)

func reverseIndex[T any](entries []T, key func(T) (value uint64, name string, deprecated bool)) map[uint64]string {
	index := make(map[uint64]string, len(entries))
	current := make(map[uint64]bool, len(entries))
	for _, entry := range entries {
		value, name, deprecated := key(entry)
		if _, ok := index[value]; ok && (deprecated || current[value]) {
			continue
		}
		index[value] = name
		current[value] = !deprecated
	}
	return index
}

// buildConstantNames indexes the target's named constants by the bits of their value, so a literal
// equalling one exactly can be spelled as the name. The key is bits rather than the double: deg2rad
// and rad2deg are float precision literals widened to double, so a program that folded them at full
// double precision would not match on value alone. NaN and the infinities are left out, since
// [formatImm] answers both before reaching the table.
func buildConstantNames(constants []ic10.Constant) map[uint64]string {
	names := make(map[uint64]string, len(constants))
	for _, constant := range constants {
		if math.IsNaN(constant.Value) || math.IsInf(constant.Value, 0) {
			continue
		}
		bits := math.Float64bits(constant.Value)
		if existing, ok := names[bits]; ok && len(existing) <= len(constant.Name) {
			continue
		}
		names[bits] = constant.Name
	}
	return names
}
