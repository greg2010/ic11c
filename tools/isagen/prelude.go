package main

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
)

// preludeFileName is the header's own file name. A flags file reaches it by a
// path relative to that file's own directory, which is what a C driver resolves
// the -include argument against. internal/ic10 spells it again as
// PreludeFileName and checks the two agree.
const preludeFileName = "ic10_prelude.h"

// includeGuard is the header's own guard macro.
const includeGuard = "IC10_PRELUDE_H"

// clangFormatOff keeps clang-format away from the header. There is no matching
// `on`: the directive holds to end of file, and every line below it is written
// here, so a reformat would only make the next regeneration undiffable.
const clangFormatOff = "// clang-format off"

// maxDevicePin is the highest pin a housing exposes. isagen does not import
// sema, which owns the same limit, since sema is built on the tables isagen
// writes; TestPreludeDeclaresThePinsMicroCResolves holds the two together.
const maxDevicePin = 5

// machineConstantNames are the chip constants MicroC predeclares, in the
// order the header writes them. The machine also carries nan, pinf and ninf,
// which MicroC does not expose.
// TestPreludeDeclaresTheConstantsMicroCPredeclares holds this list to the
// names analysis resolves, in both directions.
var machineConstantNames = []string{"pi", "tau", "deg2rad", "rad2deg", "epsilon", "rgas"}

// compileFlagsArgs is the argument file a C editor reads, one argument per
// line, less the header -include takes, which depends on where the file
// lands. -include and its argument are separate lines because a driver reads
// the file as an argv rather than as a shell command line. No warning is
// suppressed: every operand position takes an enumerator of its own family's
// type, so nothing MicroC can write reaches clang's implicit-cast warning.
var compileFlagsArgs = []string{
	"-std=c23",
	"-ffreestanding",
	"-include",
}

// renderCompileFlags produces the contents of a compile_flags.txt that includes
// the header at the given path.
func renderCompileFlags(header string) []byte {
	return []byte(strings.Join(compileFlagsArgs, "\n") + "\n" + header + "\n")
}

// renderFlagsFor produces the contents of the argument file at flagsPath for
// the header at preludePath, both given relative to the same directory. The
// header is named relative to the flags file's own directory, which is what
// a driver resolves the argument against, so a corpus elsewhere in the tree
// needs no second copy of the header. The separator is always a slash, so
// which host ran the generator does not show in the output.
func renderFlagsFor(flagsPath, preludePath string) ([]byte, error) {
	rel, err := filepath.Rel(filepath.Dir(flagsPath), preludePath)
	if err != nil {
		return nil, fmt.Errorf("name %s relative to %s: %w", preludePath, flagsPath, err)
	}
	return renderCompileFlags(filepath.ToSlash(rel)), nil
}

const preludeIntro = `// MicroC seen as C: the device pins, the four operand enums, the machine
// constants and the intrinsics. Include it with -std=c23 -ffreestanding, which
// is what the generated compile_flags.txt naming it does.
//
// C is the looser language of the two. Every program MicroC accepts compiles
// here; the reverse does not hold, and each place it does not is noted below.
// Nothing here enforces a MicroC rule, so a clean C parse is not an analysis.
`

const devDoc = `// The pins a housing exposes, and db for the housing itself.
//
// A pin is a wiring slot on the housing the chip is inserted into: the player
// picks a device for each one in the housing's own screen, and d0 through d5
// name those six choices in order. db is the housing, so a program reads and
// writes its own chip through it -- which is how one chip hands a figure to
// another with no logic memory between them. Nothing on the chip checks what a
// pin reaches, and reading or writing one the player left empty faults.
//
// An enum is what makes ` + "`const dev sensor = d0;`" + ` legal at file scope: C needs a
// constant expression there and an enumeration constant is the only one that
// names a device. C converts an enumeration to int freely, so d0 + 1 and
// d0 < d1 compile here where MicroC rejects them.
//
// Higher pins are absent because no housing has one.
`

const logicDoc = `// Device properties: the logic type argument of the direct and batch forms.
//
// One name per property the game lets a chip see on a device, and which of them
// a given device answers is the device's own affair -- a gas sensor has
// Temperature and no Setting, a vending machine the reverse. A name declared
// here is therefore not a promise that the device on the other end has it, and
// asking for one it does not answer faults the chip and loses the rest of the
// tick. Declaring the prefab a pin is wired to is what turns that into a
// warning at compile time. Every read answers a double and every write takes
// one; there is no other shape.
`

const slotDoc = `// Slot properties: the slot type argument of the slot forms.
//
// A slot is one of the inventory positions a device carries -- a furnace's
// input and output, a suit rack's helmet and suit, a printer's cartridges --
// numbered from zero in the order the device declares them. These name what a
// chip may ask about the thing in one, and a slot answers far fewer of them
// than a device answers logic types.
`

const batchDoc = `// Aggregation modes: the last argument of the batch load forms.
//
// A batch read reaches every device of one prefab on the data network at once
// and folds their answers into a single number, and this says how. What an
// empty batch answers is the thing to plan for, because it differs per mode and
// none of it is an error: Average is NaN, Sum and BatchMode_Minimum are zero,
// BatchMode_Maximum is negative infinity. Testing the result against zero does
// not tell an empty network from a real reading of zero; __ic_isnan does, for
// Average. Count is the exception and answers how many devices matched rather
// than anything about what they hold.
`

const reagentDoc = `// Reagent views: the mode argument of __ic_load_reagent.
//
// The four questions a chip may ask a machine about one ore or one chemical,
// named by hash: Contents is what the machine is holding now, Required is what
// the recipe it is running still wants, Recipe is what one unit of that recipe
// costs, and TotalContents is what the whole network holds. A reagent the
// machine has never held answers NaN rather than zero.
`

const constantsDoc = `// The chip's own constants, which a program cannot write for itself: deg2rad
// and rad2deg are float literals widened to double, so folding pi/180 at double
// precision produces a different number from the chip. epsilon is the smallest
// positive double there is, not a comparison tolerance; a tolerance a program
// wants is one it has to choose for itself.
`

const intrinsicsDoc = `// The intrinsics. Three of their rules no prototype can carry, each of them C
// admitting more than MicroC does:
//
//   - a slot index must be a constant expression, where C takes any int;
//   - __ic_hash takes a string literal, where C takes any const char *;
//   - a device argument must be a pin, a dev object or a dev parameter, and is
//     never computed.
`

// renderPrelude produces the contents of the C header that lets an editor
// read a MicroC program. The enums, constants and build stamp come from the
// ISA table; the intrinsics do not, since no extraction knows them. Every
// family the header declares must be present and non-empty.
func renderPrelude(isa *ISA) ([]byte, error) {
	var b strings.Builder
	b.WriteString(generatedHeader)
	b.WriteString("\n")
	b.WriteString(clangFormatOff)
	b.WriteString("\n\n")
	b.WriteString(preludeIntro)
	fmt.Fprintf(&b, "//\n// Extracted from Stationeers depot manifest %s, game version %s.\n\n", isa.Manifest, isa.Version)
	fmt.Fprintf(&b, "#ifndef %s\n#define %s\n\n", includeGuard, includeGuard)

	writeDevEnum(&b)
	if err := writeOperandEnums(&b, isa); err != nil {
		return nil, err
	}
	if err := writeMachineConstants(&b, isa); err != nil {
		return nil, err
	}
	writeIntrinsics(&b)

	fmt.Fprintf(&b, "#endif // %s\n", includeGuard)
	return []byte(b.String()), nil
}

func writeDevEnum(b *strings.Builder) {
	b.WriteString(devDoc)
	b.WriteString("typedef enum ic10_dev { db = -1, d0 = 0")
	for pin := 1; pin <= maxDevicePin; pin++ {
		fmt.Fprintf(b, ", d%d", pin)
	}
	b.WriteString(" } dev;\n\n")
}

// preludeEnum is one operand family as the header declares it.
type preludeEnum struct {
	// tag names the enum and typedef the alias a signature is written with.
	tag     string
	typedef string
	// prefix is what the family spells a member with where an earlier family
	// has already taken the bare name. internal/ic10 exports the same four
	// strings for the compiler to resolve an operand with, and its prelude test
	// holds the two lists together.
	prefix string
	// kind is how a deprecation note names one of the members.
	kind    string
	doc     string
	members []EnumMember
}

// preludeEnums lists the families in the order they claim names.
func preludeEnums(isa *ISA) []preludeEnum {
	return []preludeEnum{
		{tag: "ic10_logic_e", typedef: "ic10_logic", prefix: "LogicType_", kind: "logic type", doc: logicDoc, members: isa.LogicTypes},
		{tag: "ic10_slot_e", typedef: "ic10_slot", prefix: "SlotType_", kind: "slot type", doc: slotDoc, members: isa.SlotTypes},
		{tag: "ic10_batch_e", typedef: "ic10_batch", prefix: "BatchMode_", kind: "batch mode", doc: batchDoc, members: isa.BatchModes},
		{tag: "ic10_reagent_e", typedef: "ic10_reagent", prefix: "ReagentMode_", kind: "reagent mode", doc: reagentDoc, members: isa.ReagentModes},
	}
}

// operandsDoc introduces the families and the spelling rule they share. The
// prefixes are read back out of the families rather than written here, so the
// text cannot state a scheme the declarations do not follow.
func operandsDoc(families []preludeEnum) string {
	prefixes := make([]string, len(families))
	for i, e := range families {
		prefixes[i] = e.prefix
	}
	return `// The operand families, one per intrinsic parameter that names a machine
// property. C admits one enumerator per name in a scope and the families share
// several names, so a family that finds its name already declared spells it
// with a prefix of its own rather than giving the name up, in the order they
// appear below:
//
//   ` + strings.Join(prefixes, " ") + `
//
// MicroC resolves an operand name in that same single namespace: the bare
// spelling means what the enumerator declaring it means, in every position, and
// a position that had to prefix a name rejects the bare spelling there and
// names the prefixed one. Every position therefore takes an enumerator of its
// own family's type, which is what leaves nothing here for a C driver to
// report as an implicit enumeration cast.

`
}

// writeOperandEnums declares the families, giving each shared name to the first
// family that carries it and prefixing it in every later one.
func writeOperandEnums(b *strings.Builder, isa *ISA) error {
	families := preludeEnums(isa)
	b.WriteString(operandsDoc(families))

	// claimed holds the family that took each bare name, and declared every
	// enumerator written so far against the family that wrote it.
	claimed := make(map[string]string)
	declared := make(map[string]string)

	for _, e := range families {
		if len(e.members) == 0 {
			return fmt.Errorf("the ISA table carries no %s members", e.typedef)
		}
		b.WriteString(e.doc)
		fmt.Fprintf(b, "typedef enum %s {\n", e.tag)
		for _, member := range e.members {
			name := member.Name
			switch owner, taken := claimed[member.Name]; {
			case taken && owner == e.typedef:
				return fmt.Errorf("%s carries %s twice, and the compiler resolves the first of them everywhere", e.typedef, member.Name)
			case taken:
				name = e.prefix + member.Name
			default:
				claimed[member.Name] = e.typedef
			}
			if owner, twice := declared[name]; twice {
				return fmt.Errorf("%s declares the enumerator %s, which %s already declares, and C admits one per name in a scope", e.typedef, name, owner)
			}
			declared[name] = e.typedef
			fmt.Fprintf(b, "    %s", name)
			if member.Deprecated {
				fmt.Fprintf(b, " [[deprecated(\"the game marks this %s retired\")]]", e.kind)
			}
			fmt.Fprintf(b, " = %d,\n", member.Value)
		}
		fmt.Fprintf(b, "} %s;\n\n", e.typedef)
	}
	return nil
}

func writeMachineConstants(b *strings.Builder, isa *ISA) error {
	values := make(map[string]Constant, len(isa.Constants))
	for _, constant := range isa.Constants {
		values[constant.Name] = constant
	}
	b.WriteString(constantsDoc)
	for _, name := range machineConstantNames {
		constant, ok := values[name]
		if !ok {
			return fmt.Errorf("the ISA table carries no constant %q", name)
		}
		value, err := constant.Float()
		if err != nil {
			return err
		}
		literal, err := cDouble(value)
		if err != nil {
			return fmt.Errorf("constant %q: %w", name, err)
		}
		fmt.Fprintf(b, "constexpr double %s = %s;\n", name, literal)
	}
	b.WriteString("\n")
	return nil
}

// cDouble renders v as a C double literal that reads back as the same bits.
func cDouble(v float64) (string, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "", fmt.Errorf("no C literal spells %s", strconv.FormatFloat(v, 'g', -1, 64))
	}
	literal := strconv.FormatFloat(v, 'g', -1, 64)
	// An integral value formats without a point or an exponent, which C reads
	// as an int constant.
	if !strings.ContainsAny(literal, ".eE") {
		literal += ".0"
	}
	return literal, nil
}

// cInteger is the C type MicroC's integer type is spelled as, in the header
// and in MicroC itself. int is 32 bits on every implementation that matters
// and the machine holds every integer exactly to 53; long long covers that
// whole range everywhere, unlike long, which is 32 bits under LLP64.
const cInteger = "long long"

// cParam is one C parameter.
type cParam struct {
	ctype string
	name  string
}

func (p cParam) String() string {
	if strings.HasSuffix(p.ctype, "*") {
		return p.ctype + p.name
	}
	return p.ctype + " " + p.name
}

// cPrototype is one intrinsic as the header declares it. An empty params is the
// (void) form.
type cPrototype struct {
	result string
	name   string
	params []cParam
}

// The arguments the intrinsics are built from, one per operand kind
// internal/sema classifies a parameter as.
var (
	argDevice      = cParam{"dev", "d"}
	argLogicType   = cParam{"ic10_logic", "t"}
	argSlotType    = cParam{"ic10_slot", "t"}
	argBatchMode   = cParam{"ic10_batch", "m"}
	argReagentMode = cParam{"ic10_reagent", "m"}
	argSlot        = cParam{cInteger, "slot"}
	argHash        = cParam{cInteger, "hash"}
	argName        = cParam{cInteger, "name"}
	argStored      = cParam{"double", "v"}
	argString      = cParam{"const char *", "s"}
)

// intrinsicPrototypes declares every intrinsic MicroC exposes, in the order
// and with the signatures internal/sema defines. isagen cannot read that
// table, so nothing mechanical keeps the two in step; the conformance gate
// does, since C23 has no implicit function declaration.
func intrinsicPrototypes() []cPrototype {
	list := []cPrototype{
		{"double", "__ic_load", []cParam{argDevice, argLogicType}},
		{"void", "__ic_store", []cParam{argDevice, argLogicType, argStored}},
		{"double", "__ic_load_slot", []cParam{argDevice, argSlot, argSlotType}},
		{"void", "__ic_store_slot", []cParam{argDevice, argSlot, argSlotType, argStored}},
		{"double", "__ic_load_batch", []cParam{argHash, argLogicType, argBatchMode}},
		{"void", "__ic_store_batch", []cParam{argHash, argLogicType, argStored}},
		{"double", "__ic_load_batch_named", []cParam{argHash, argName, argLogicType, argBatchMode}},
		{"void", "__ic_store_batch_named", []cParam{argHash, argName, argLogicType, argStored}},
		{"double", "__ic_load_batch_slot", []cParam{argHash, argSlot, argSlotType, argBatchMode}},
		{"void", "__ic_store_batch_slot", []cParam{argHash, argSlot, argSlotType, argStored}},
		{"double", "__ic_load_batch_named_slot", []cParam{argHash, argName, argSlot, argSlotType, argBatchMode}},
		{"double", "__ic_load_reagent", []cParam{argDevice, argReagentMode, argHash}},
		{"bool", "__ic_device_present", []cParam{argDevice}},
		{cInteger, "__ic_hash", []cParam{argString}},

		{"void", "__ic_yield", nil},
		{"void", "__ic_sleep", []cParam{{"double", "seconds"}}},

		{"bool", "__ic_isnan", []cParam{argStored}},
		{"double", "__ic_rand", nil},
	}
	for _, name := range []string{
		"__ic_sqrt", "__ic_abs", "__ic_sgn", "__ic_round", "__ic_trunc", "__ic_ceil",
		"__ic_floor", "__ic_log", "__ic_exp", "__ic_sin", "__ic_cos", "__ic_tan",
		"__ic_asin", "__ic_acos", "__ic_atan",
	} {
		list = append(list, cPrototype{"double", name, []cParam{{"double", "v"}}})
	}
	for _, name := range []string{"__ic_min", "__ic_max", "__ic_pow", "__ic_atan2"} {
		list = append(list, cPrototype{"double", name, []cParam{{"double", "a"}, {"double", "b"}}})
	}
	for _, name := range []string{"__ic_clamp", "__ic_lerp"} {
		list = append(list, cPrototype{"double", name, []cParam{{"double", "a"}, {"double", "b"}, {"double", "c"}}})
	}
	return list
}

func writeIntrinsics(b *strings.Builder) {
	b.WriteString(intrinsicsDoc)
	for _, prototype := range intrinsicPrototypes() {
		params := "void"
		if len(prototype.params) > 0 {
			rendered := make([]string, len(prototype.params))
			for i, param := range prototype.params {
				rendered[i] = param.String()
			}
			params = strings.Join(rendered, ", ")
		}
		// The width is that of the widest result type, so the names line up.
		fmt.Fprintf(b, "%-*s %s(%s);\n", len(cInteger), prototype.result, prototype.name, params)
	}
	b.WriteString("\n")
}
