package emit

import (
	"errors"
	"fmt"
	"math"
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
	ErrUnresolvedLabel = errors.New("label names no block")
	// ErrUnspellableOperand is an operand with no text the chip's assembler
	// reads. The chip catches malformed register text and nothing else, so an
	// operand that renders as a Go debug form would either fail to compile on
	// the chip or, worse, resolve to something else entirely.
	ErrUnspellableOperand = errors.New("operand has no spelling the chip accepts")
)

// renderer holds everything operand rendering needs beyond the operand itself.
type renderer struct {
	readable bool
	numeric  bool
	// lineOf maps a MIR block label to the line its first instruction lands
	// on. Branches are absolute, so this is what a Label operand becomes.
	lineOf map[string]int
	// names maps a MIR block label to its mangled emitted name. Readable output
	// is the only thing that spells a label, so it is nil otherwise.
	names map[string]string
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
		return spelled(a.Reg.String(), a.Reg < ic10.NumRegisters)
	case mir.Imm:
		return formatImm(a.Value)
	case mir.Label:
		return r.label(a)
	case mir.Device:
		return spelled(a.String(), spellableDevice(a))
	case mir.LogicType:
		return r.enum(uint64(a.Value), logicTypeNames), nil
	case mir.LogicSlotType:
		return r.enum(uint64(a.Value), logicSlotTypeNames), nil
	case mir.BatchMode:
		return r.enum(uint64(a.Value), batchModeNames), nil
	case mir.ReagentMode:
		return r.enum(uint64(a.Value), reagentModeNames), nil
	default:
		return "", fmt.Errorf("operand %s has no emitted form", arg)
	}
}

// spelled returns text when it is one the chip's assembler reads.
//
// mir's constructors already refuse an out-of-range register or device pin, so
// what reaches here is an operand built as a struct literal — which the register
// allocator and every test that assembles machine IR by hand can do. Their
// String methods fall back to a Go debug form for a value outside the machine's
// range, and emitting one would put text on a line that either fails to compile
// on the chip or resolves to something else.
func spelled(text string, ok bool) (string, error) {
	if !ok {
		return "", fmt.Errorf("%q: %w", text, ErrUnspellableOperand)
	}
	return text, nil
}

// spellableDevice reports whether a device operand has a spelling the chip
// resolves. A housing has six pins; d6 through d9 match the chip's own operand
// pattern and then index past the end of a six element array, faulting once per
// tick with nothing to say so.
func spellableDevice(d mir.Device) bool {
	switch d.Kind {
	case mir.DeviceBase:
		return d.Conn == mir.NoConnection
	case mir.DevicePin:
		return d.Pin < ic10.NumDevicePins && d.Conn >= mir.NoConnection
	default:
		return false
	}
}

func (r renderer) label(l mir.Label) (string, error) {
	line, ok := r.lineOf[l.Name]
	if !ok {
		return "", fmt.Errorf("%q: %w", l.Name, ErrUnresolvedLabel)
	}
	if r.readable {
		return r.names[l.Name], nil
	}
	return strconv.Itoa(line), nil
}

// enum renders one of the operand enums.
//
// The name is the default. It and its integer resolve identically on the chip
// and take one operand either way, so the difference is bytes alone against a
// program the reader can follow. Lines are the budget that binds and neither
// form spends one. A value the tables carry no name for still renders as its
// integer, which is what a program built by hand out of struct literals can
// hold.
func (r renderer) enum(value uint64, names map[uint64]string) string {
	if !r.numeric {
		if name, ok := names[value]; ok {
			return name
		}
	}
	return strconv.FormatUint(value, 10)
}

// formatImm renders a double as a literal the chip's assembler accepts.
//
// Exponent notation is never produced. Whether the chip's number parser accepts
// it is not established by docs/target.md, and a decimal expansion is always
// correct, so the risk is not worth the bytes. A value that expands to
// something longer than the line limit shows up as a line length violation
// rather than as silently wrong output.
func formatImm(value float64) (string, error) {
	switch {
	case math.IsNaN(value):
		// The chip's operand parser reads a NaN literal as unset, so `move r0
		// nan` raises IncorrectVariable. NaN reaches a register only through a
		// define or another register, neither of which is an immediate.
		return "", fmt.Errorf("NaN has no literal spelling the chip accepts")
	case math.IsInf(value, 1):
		return "pinf", nil
	case math.IsInf(value, -1):
		return "ninf", nil
	}
	text := strconv.FormatFloat(value, 'f', -1, 64)
	if name, ok := constantNames[math.Float64bits(value)]; ok && len(name) < len(text) {
		return name, nil
	}
	return text, nil
}

// Reverse indexes over the target's enums. A value with several spellings keeps
// the current one; a deprecated name still resolves but is not what the
// compiler should print.
var (
	logicTypeNames     = reverseIndex(ic10.LogicTypes, func(t ic10.LogicTypeInfo) (uint64, string, bool) { return uint64(t.Value), t.Name, t.Deprecated })
	logicSlotTypeNames = reverseIndex(ic10.LogicSlotTypes, func(t ic10.LogicSlotTypeInfo) (uint64, string, bool) { return uint64(t.Value), t.Name, t.Deprecated })
	batchModeNames     = reverseIndex(ic10.BatchModes, func(m ic10.BatchModeInfo) (uint64, string, bool) { return uint64(m.Value), m.Name, m.Deprecated })
	reagentModeNames   = reverseIndex(ic10.ReagentModes, func(m ic10.ReagentModeInfo) (uint64, string, bool) { return uint64(m.Value), m.Name, m.Deprecated })
	constantNames      = buildConstantNames()
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

// buildConstantNames lets a literal that exactly equals a named constant emit
// the name when the name is shorter, which it usually is. The comparison is on
// bits, so it never conflates a recomputed value with the game's own: deg2rad
// and rad2deg are float precision literals widened to double, and a program
// that folded them at full double precision would not match.
func buildConstantNames() map[uint64]string {
	names := make(map[uint64]string, len(ic10.Constants))
	for _, constant := range ic10.Constants {
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
