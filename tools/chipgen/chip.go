package main

import (
	"fmt"
	"slices"
	"strings"
)

// chipPath is where the decompiler puts the chip. The slicer names the file
// rather than searching for the type: a search would turn a type moved to
// another namespace into a slice quietly assembled from something else.
const chipPath = "Assets/Scripts/Objects/Electrical/ProgrammableChip.cs"

// chipBase is the base list the chip is declared with. Only the first entry is
// held to, because it is the one that decides whether the class drags in the
// MonoBehaviour chain; the interfaces after it are dropped with it and name
// only contracts.
const chipBase = "Item"

// hcfOperation is the one operation class that cannot be defined outside the
// game: its body calls HaltAndCatchFire, which sets the item alight, awards
// an achievement and drives a UniTask off the main thread — none of it
// observable through the registers, so it and its parse arm are dropped.
const hcfOperation = "_HCF_Operation"

// hcfStateMachine is the compiler-generated async state machine behind
// HaltAndCatchFire. The decompiler emits it as a nested type with a mangled
// name; it exists only to drive the operation above.
const hcfStateMachine = "_003CHaltAndCatchFireFromThread_003Ed__125"

// chipMembers is every non-type declaration of ProgrammableChip the emitted
// unit keeps, named by its full signature. It is a keep list rather than a
// drop list, so a game update adding a member fails to match here instead of
// passing through silently. Nested types are kept wholesale, minus the two above.
var chipMembers = []string{
	// Machine state. Everything the operations read or write lives here.
	"private readonly double[] _Registers",
	"private readonly int _StackPointerIndex",
	"private readonly int _ReturnAddressIndex",
	"private readonly double[] _Stack",
	"private readonly Dictionary<string, _AliasValue> _Aliases",
	"private readonly Dictionary<string, int> _JumpTags",
	"private readonly Dictionary<string, double> _Defines",
	"private readonly List<_LineOfCode> _LinesOfCode",
	"private int _NextAddr",
	"public AsciiString SourceCode",

	// The backing fields of the four synced error properties below.
	"private ushort _ErrorLineNumberSynced",
	"private byte _ErrorTypeSynced",
	"private ushort _compileErrorLineNumber",
	"private ProgrammableChipException.ICExceptionType _compileErrorType",

	// The error properties themselves. Their bodies are verbatim apart from
	// the base-qualified network flag, which chipEdits redirects to a field.
	"private ushort _ErrorLineNumber",
	"private ProgrammableChipException.ICExceptionType _ErrorType",
	"private ushort CompileErrorLineNumber",
	"private ProgrammableChipException.ICExceptionType CompileErrorType",
	// What the housing's IsOperable asks, and so what decides the Error a
	// program reads back off its own db.
	"public bool CompilationError",

	// The device pin array. Kept for its declaration only: chipEdits replaces
	// the expression body, which reaches the pins through the inventory graph.
	"private ICircuitHolder CircuitHousing",

	// Compilation and execution, the two entry points the harness drives.
	"public void SetSourceCode(string sourceCode)",
	"public void Execute(int runCount)",
	"public double LineNumber",

	// The stack, addressed by the memory instructions.
	"public double ReadMemory(int address)",
	"public void WriteMemory(int address, double value)",
	"public void ClearMemory()",
	"public int GetStackSize()",

	// Numeric helpers the operation bodies call.
	"public static double LongToDouble(long l)",
	"public static long DoubleToLong(double d, bool signed)",
	"public static double PackAscii6(string text, int lineNumber)",

	// Operand tables. The constant table is what makes `nan` and `pinf`
	// literals, and the enum list is what makes a bare LogicType name one.
	"public static Constant[] AllConstants",
	"public static List<IScriptEnum> InternalEnums",
	"static ProgrammableChip()",

	// Rotate widths, read by the bit-rotate operations.
	"private const int _RotateWidth",
	"private const long _RotateMask",

	// The help-string constants and the HelpString statics they build. The
	// operation classes name these in their own declarations, so the unit does
	// not compile without them even though nothing here renders help.
	"public const string _strCommand",
	"public const string _strDevice",
	"public const string _strLogicType",
	"public const string _strNumber",
	"public const string _strInteger",
	"public const string _strOr",
	"public const string _strRegister",
	"public const string _strString",
	"public const string _strAny",
	"public const string _strRegOrDev",
	"public const string _strReagentMode",
	"public const string _strReagent",
	"public const string _strType",
	"public const string _strBatchMode",
	"public const string _strValues",
	"private const string FORMAT_VARIABLE",
	"private const string FORMAT_NUMBER",
	"private const string FORMAT_TEXT",
	"private static readonly HelpString STRING",
	"private static readonly HelpString DEVICE_INDEX",
	"private static readonly HelpString REGISTER",
	"private static readonly HelpString INTEGER",
	"private static readonly HelpString NUMBER",
	"private static readonly HelpString REF_ID",
	"private static readonly HelpString OR",
	"private static readonly HelpString LOGIC_TYPE",
	"private static readonly HelpString LOGIC_SLOT_TYPE",
	"private static readonly HelpString BATCH_MODE",
	"private static readonly HelpString DEVICE_HASH",
	"private static readonly HelpString NAME_HASH",
	"private static readonly HelpString SLOT_INDEX",
	"private static readonly HelpString REAGENT_MODE",

	// Source-text constants the tokenizer and the variable parsers compare
	// against.
	"public const char REGISTER_CHAR",
	"public const char DEVICE_CHAR",
	"public const string BASE_UNIT_STRING",
	"public const int BASE_UNIT_INDEX",
	"public const int BASE_NETWORK_INDEX",
	"public const int FIRST_AVAILABLE_NETWORK",
	"public const string RETURN_ADDRESS_STRING",
	"public const string STACK_POINTER_STRING",
	"public const char HEX_CHAR",
	"public const char BINARY_CHAR",
	"public const char COMMENT_CHAR",
	"public const char NETWORK_CHAR",
}

// chipSlice is the sliced chip class and what the slicing found.
type chipSlice struct {
	// text is the whole class declaration, ready to write.
	text string
	// operations is every operation class the unit defines, in source order.
	operations []string
	// commands is every ScriptCommand the surviving parse switch still builds
	// an operation for.
	commands []string
	// droppedTypes names the nested types left behind, with the reason.
	droppedTypes map[string]string
}

// sliceChip reads ProgrammableChip.cs and renders the chip class as a
// standalone type. Every landmark is located through the declaration
// structure rather than by offset, so a base list, a moved signature, or an
// edit that no longer matches stops the slice with a message naming the construct.
func sliceChip(s *slicing) (*chipSlice, error) {
	src, err := s.read(chipPath)
	if err != nil {
		return nil, err
	}
	class, err := src.topLevelType("ProgrammableChip")
	if err != nil {
		return nil, err
	}
	if len(class.bases) == 0 || class.bases[0] != chipBase {
		return nil, fmt.Errorf("%s: ProgrammableChip base list is %q, expected it to start with %s",
			src.rel, strings.Join(class.bases, ", "), chipBase)
	}

	body := src.top().scopeOf(class)
	decls, err := splitDecls(body.body)
	if err != nil {
		return nil, fmt.Errorf("%s: split ProgrammableChip body: %w", src.rel, err)
	}

	slice := &chipSlice{droppedTypes: map[string]string{
		hcfOperation:    "calls ProgrammableChip.HaltAndCatchFire, which has no standalone equivalent",
		hcfStateMachine: "the async state machine behind " + hcfOperation,
	}}

	kept, err := slice.selectDecls(body, decls)
	if err != nil {
		return nil, err
	}

	text, err := applyChipEdits(body, kept, s)
	if err != nil {
		return nil, err
	}
	slice.commands, err = keptCommands(text)
	if err != nil {
		return nil, err
	}

	slice.text = chipHeader(src) + "\n{\n" + text + "\n}\n"
	return slice, nil
}

// selectDecls partitions the chip's own declarations into what the unit
// keeps. Nested types are kept wholesale except the two that cannot be
// defined; everything else is kept only if chipMembers names it exactly
// once, fingerprinted on the way through so a moved signature is caught.
func (s *chipSlice) selectDecls(body scope, decls []decl) ([]decl, error) {
	rel := body.file.rel
	wanted := make(map[string]int, len(chipMembers))
	for _, sig := range chipMembers {
		wanted[sig]++
	}

	var kept []decl
	seen := make(map[string]int, len(chipMembers))
	skipped := make(map[string]int, len(s.droppedTypes))
	for _, d := range decls {
		if d.kind != declLeaf {
			if _, dropped := s.droppedTypes[d.name]; dropped {
				skipped[d.name]++
				// Fingerprinted despite being dropped: the claim is that it has no
				// standalone equivalent, and a rewritten body is how that claim goes false.
				if err := body.cutTree(d); err != nil {
					return nil, err
				}
				continue
			}
			if isOperationClass(d.name) {
				s.operations = append(s.operations, d.name)
			}
			kept = append(kept, d)
			continue
		}
		if wanted[d.name] == 0 {
			continue
		}
		seen[d.name]++
		kept = append(kept, d)
	}
	for _, d := range kept {
		if err := body.cutTree(d); err != nil {
			return nil, err
		}
	}

	var missing, duplicated, unmatched []string
	for sig, want := range wanted {
		switch got := seen[sig]; {
		case got == 0:
			missing = append(missing, sig)
		case got != want:
			duplicated = append(duplicated, fmt.Sprintf("%s (%d times)", sig, got))
		}
	}
	for name := range s.droppedTypes {
		if skipped[name] != 1 {
			unmatched = append(unmatched, fmt.Sprintf("%s (%d times)", name, skipped[name]))
		}
	}
	slices.Sort(missing)
	slices.Sort(duplicated)
	slices.Sort(unmatched)
	if len(missing) > 0 {
		return nil, fmt.Errorf("%s: ProgrammableChip no longer declares:\n\t%s\n%w",
			rel, strings.Join(missing, "\n\t"), errNotFound)
	}
	if len(duplicated) > 0 {
		return nil, fmt.Errorf("%s: ProgrammableChip declares more than once:\n\t%s",
			rel, strings.Join(duplicated, "\n\t"))
	}
	// A drop that matches nothing is what a renamed compiler-generated type
	// looks like here: its name carries a compiler-assigned ordinal, so an edit
	// above it moves the name. Left unchecked the type would be kept, and the
	// unit would then fail three targets downstream on a UniTask reference.
	if len(unmatched) > 0 {
		return nil, fmt.Errorf("%s: ProgrammableChip declares no nested type to drop named:\n\t%s\n%w",
			rel, strings.Join(unmatched, "\n\t"), errNotFound)
	}
	return kept, nil
}

// chipHeader renders the class header with the base list removed. Item is
// the bottom of the MonoBehaviour chain, and everything the chip inherits
// through it is world state the harness injects instead.
func chipHeader(src *sourceFile) string {
	return fmt.Sprintf("// %s, sliced: %s\npublic class ProgrammableChip",
		src.rel, strings.Join(chipEditNotes, "; "))
}

// chipEditNotes is what the emitted header says was changed, so the difference
// between the unit and the decompile is written where a reader of the unit
// finds it.
var chipEditNotes = []string{
	"base list dropped",
	"ParentSlot refresh dropped from SetSourceCode",
	"_SetDeviceValue dropped",
	"network sync redirected to a plain field",
	"CircuitHousing made an injected field",
	"help-page renderers dropped",
	"static constructor's enum roster narrowed",
	"hcf parse arm dropped",
	"Execute records whether the tick spent its budget",
	"rand redirected to a source the harness seeds",
}

// keptCommands lists the ScriptCommand values the surviving parse switch still
// constructs an operation for. The caller reports it; the harness does not read
// it.
func keptCommands(body string) ([]string, error) {
	const marker = "case ScriptCommand."
	var commands []string
	for i := 0; i < len(body); {
		j := strings.Index(body[i:], marker)
		if j < 0 {
			break
		}
		i += j + len(marker)
		name, end := identAt(body, i)
		if name == "" {
			return nil, fmt.Errorf("parse switch has a case with no command name at offset %d", i)
		}
		commands = append(commands, name)
		i = end
	}
	if len(commands) == 0 {
		return nil, fmt.Errorf("parse switch: %w", errNotFound)
	}
	return commands, nil
}

// isOperationClass reports whether a nested type is one of the per-opcode
// classes, as opposed to the abstract _Operation the arity bases descend from.
func isOperationClass(name string) bool {
	return strings.HasPrefix(name, "_") && strings.HasSuffix(name, "_Operation") && name != "_Operation"
}
