package vm

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/greg2010/ic11c/internal/ic10"
)

// Preprocessor forms, from Assets.Scripts.Util.Regexes. They run over the line
// before it is split into tokens, so an operand never sees a `$`, a `%`, or a
// quoted string.
var (
	preprocessStrings = regexp.MustCompile(`STR\("([^"]+)"\)`)
	preprocessHashes  = regexp.MustCompile(`HASH\("([^"]+)"\)`)
	preprocessBinary  = regexp.MustCompile(`%([01_]+)`)
	preprocessHex     = regexp.MustCompile(`\$([0-9A-Fa-f_]+)`)
)

// compileLine is _LineOfCode's constructor: strip the comment, run the
// preprocessor, split into tokens, and build the operation.
//
// Only five things are rejected here: a bad preprocessor form, a duplicate
// label, an unknown mnemonic, a wrong operand count, and a duplicate define.
// Everything else about an operand is the run time's problem, which is why an
// impossible register or an out of range device pin compiles cleanly.
func compileLine(m *Machine, text string, line int) (operation, error) {
	if i := strings.IndexByte(text, '#'); i >= 0 {
		text = text[:i]
	}
	text, err := preprocess(text, line)
	if err != nil {
		return nil, err
	}

	args := strings.Fields(text)
	if len(args) == 0 {
		return noopOperation{}, nil
	}
	if len(args) == 1 && len(args[0]) >= 2 && strings.HasSuffix(args[0], ":") {
		name := args[0][:len(args[0])-1]
		if _, exists := m.jumpTags[name]; exists {
			return nil, newFault(ExcJumpTagDuplicate, line)
		}
		m.jumpTags[name] = line
		return noopOperation{}, nil
	}

	instruction, ok := ic10.LookupInstruction(args[0])
	if !ok {
		return nil, newFault(ExcUnrecognisedInstruction, line)
	}
	spec, ok := instructions[instruction.Opcode]
	if !ok {
		return nil, newFault(ExcUnrecognisedInstruction, line)
	}
	if len(args) != spec.tokens {
		return nil, newFault(ExcIncorrectArgumentCount, line)
	}
	return spec.build(m, line, args)
}

// preprocess applies the four textual substitutions in the game's order:
// packed strings, name hashes, binary literals, then hex literals.
func preprocess(text string, line int) (string, error) {
	for _, match := range preprocessStrings.FindAllStringSubmatch(text, -1) {
		packed, err := packASCII6(match[1], line)
		if err != nil {
			return "", err
		}
		text = strings.ReplaceAll(text, match[0], strconv.FormatInt(int64(packed), 10))
	}
	for _, match := range preprocessHashes.FindAllStringSubmatch(text, -1) {
		text = strings.ReplaceAll(text, match[0], strconv.Itoa(hashName(match[1])))
	}
	for _, match := range preprocessBinary.FindAllStringSubmatch(text, -1) {
		value, err := parseRadix(strings.ReplaceAll(match[1], "_", ""), 2)
		if err != nil {
			return "", newFault(ExcInvalidProcessBinary, line)
		}
		text = strings.ReplaceAll(text, match[0], strconv.FormatInt(value, 10))
	}
	for _, match := range preprocessHex.FindAllStringSubmatch(text, -1) {
		value, err := parseRadix(strings.ReplaceAll(match[1], "_", ""), 16)
		if err != nil {
			return "", newFault(ExcInvalidPreprocessHex, line)
		}
		text = strings.ReplaceAll(text, match[0], strconv.FormatInt(value, 10))
	}
	return text, nil
}

// parseRadix is Convert.ToInt64(string, base) for the two bases the
// preprocessor uses: the digits are read as an unsigned 64 bit pattern and
// reinterpreted as signed, so a full width literal comes out negative. An empty
// string is zero, and anything wider than 64 bits overflows.
func parseRadix(digits string, base int) (int64, error) {
	if digits == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(digits, base, 64)
	if err != nil {
		return 0, err
	}
	return int64(value), nil
}

// instructionSpec is one row of the game's compile switch: how many tokens the
// line must have, counting the mnemonic, and how to build the operation.
//
// class names the C# operation class the builder is transliterated from. It is
// the anchor for diffing this package against a future game build, and the
// coverage test requires every mnemonic to have one.
type instructionSpec struct {
	class  string
	tokens int
	build  func(m *Machine, line int, args []string) (operation, error)
}

// token reads one positional operand. It exists so that the four instructions
// whose token count check disagrees with their operand read fail the way the
// game fails them, with a host exception rather than an arity error.
func token(args []string, i int) (string, error) {
	if i >= len(args) {
		return "", hostErrorf("operand %d is past the end of a %d token line", i, len(args))
	}
	return args[i], nil
}

// Operand constructors, each pinned to the include mask the game builds that
// operand position with. The masks are not interchangeable: a store operand
// cannot see defines and a value operand can.

func storeOperand(m *Machine, line int, code string) (indexVariable, error) {
	return newIndexVariable(m, line, code, maskStoreIndex)
}

func valueOperand(m *Machine, line int, code string) (doubleValueVariable, error) {
	return newDoubleValueVariable(m, line, code, maskDoubleValue)
}

func intOperand(m *Machine, line int, code string) (intValuedVariable, error) {
	return newIntValuedVariable(m, line, code, maskDoubleValue)
}

func jumpOperand(m *Machine, line int, code string) (lineNumberVariable, error) {
	return newLineNumberVariable(m, line, code, maskDoubleValue)
}

func logicTypeOperand(m *Machine, line int, code string) (enumValuedVariable, error) {
	return newEnumValuedVariable(m, line, code, maskDoubleValue|includeLogicType, lookupLogicTypeValue)
}

// slotTypeOperand builds a slot type position. The game passes
// InstructionInclude.LogicType here for ss and sbs and LogicSlotType for ls,
// lbs and lbns, but the enum operand form never reads that flag, so the
// difference has no effect and is not reproduced.
func slotTypeOperand(m *Machine, line int, code string) (enumValuedVariable, error) {
	return newEnumValuedVariable(m, line, code, maskDoubleValue|includeLogicSlotType, lookupLogicSlotTypeValue)
}

func batchModeOperand(m *Machine, line int, code string) (enumValuedVariable, error) {
	return newEnumValuedVariable(m, line, code, maskDoubleValue|includeLogicBatchMethod, lookupBatchModeValue)
}

func reagentModeOperand(m *Machine, line int, code string) (enumValuedVariable, error) {
	return newEnumValuedVariable(m, line, code, maskDoubleValue|includeLogicReagentMode, lookupReagentModeValue)
}
