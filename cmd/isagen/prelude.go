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

// maxDevicePin is the highest pin a housing exposes, mirroring internal/sema's
// own limit. isagen does not import sema: sema is built on the tables isagen
// writes, and a broken table would then leave no working generator to fix it.
const maxDevicePin = 5

// machineConstantNames are the chip constants MicroC predeclares, in the order
// the header writes them. It mirrors internal/sema's universe, including its
// omission of nan, pinf and ninf, which the machine carries and MicroC does not
// expose.
var machineConstantNames = []string{"pi", "tau", "deg2rad", "rad2deg", "epsilon", "rgas"}

// compileFlagsArgs is the argument file a C editor reads, one argument per
// line, less the header -include takes, which depends on where the file lands.
// -include and its argument are separate lines because a driver reads the file
// as an argv rather than as a shell command line.
//
// -Wno-implicit-enum-enum-cast is what the per-family enums cost. clang warns
// by default wherever a name is spelled in a family other than the one that
// declared it, which the operand tables force: __ic_load_slot(g, 0, Pressure)
// passes an ic10_logic where an ic10_slot is expected, and no ordering of the
// families avoids that for every name. The alternative design is one enum of
// every distinct name with four typedef aliases, which warns nowhere and costs
// an editor its per-family completion.
var compileFlagsArgs = []string{
	"-std=c23",
	"-ffreestanding",
	"-Wno-implicit-enum-enum-cast",
	"-include",
}

// renderCompileFlags produces the contents of a compile_flags.txt that includes
// the header at the given path.
func renderCompileFlags(header string) []byte {
	return []byte(strings.Join(compileFlagsArgs, "\n") + "\n" + header + "\n")
}

// renderFlagsFor produces the contents of the argument file at flagsPath for
// the header at preludePath, both given relative to the same directory.
//
// The header is named relative to the flags file's own directory, which is what
// a driver resolves the argument against, so a corpus elsewhere in the tree is
// configured without a second copy of the header. The separator is a slash on
// every host, so which host ran the generator does not show in the output.
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
// An enum is what makes ` + "`const dev sensor = d0;`" + ` legal at file scope: C needs a
// constant expression there and an enumeration constant is the only one that
// names a device. C converts an enumeration to int freely, so d0 + 1 and
// d0 < d1 compile here where MicroC rejects them.
//
// Higher pins are absent because no housing has one.
`

const logicDoc = `// Device properties: the logic type argument of the direct and batch forms.
`

const slotDoc = `// Slot properties: the slot type argument of the slot forms.
//
// A name an earlier family already declared is omitted here, because C admits
// one enumerator per name in a scope and the families give the shared names
// different values. Spelling one where another family's type is expected still
// compiles, since C converts between enumeration types.
`

const batchDoc = `// Aggregation modes: the last argument of the batch load forms.
`

const reagentDoc = `// Reagent views: the mode argument of __ic_load_reagent.
`

const constantsDoc = `// The chip's own constants, which a program cannot write for itself: deg2rad
// and rad2deg are float literals widened to double, so folding pi/180 at double
// precision produces a different number from the chip.
`

const intrinsicsDoc = `// The intrinsics. Three of their rules no prototype can carry, each of them C
// admitting more than MicroC does:
//
//   - a slot index must be a constant expression, where C takes any int;
//   - __ic_hash takes a string literal, where C takes any const char *;
//   - a device argument must be a pin, a dev object or a dev parameter, and is
//     never computed.
`

// renderPrelude produces the contents of the C header that lets an editor read
// a MicroC program.
//
// The enums, the constants and the build stamp come from the ISA table; the
// intrinsics do not, because no extraction knows them. Every family the header
// declares must be present and non-empty, so a truncated table is reported here
// rather than emitted as a header that silently drops names.
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
	// kind is how a deprecation note names one of the members.
	kind    string
	doc     string
	members []EnumMember
}

// preludeEnums lists the families in the order they claim names.
func preludeEnums(isa *ISA) []preludeEnum {
	return []preludeEnum{
		{tag: "ic10_logic_e", typedef: "ic10_logic", kind: "logic type", doc: logicDoc, members: isa.LogicTypes},
		{tag: "ic10_slot_e", typedef: "ic10_slot", kind: "slot type", doc: slotDoc, members: isa.SlotTypes},
		{tag: "ic10_batch_e", typedef: "ic10_batch", kind: "batch mode", doc: batchDoc, members: isa.BatchModes},
		{tag: "ic10_reagent_e", typedef: "ic10_reagent", kind: "reagent mode", doc: reagentDoc, members: isa.ReagentModes},
	}
}

// writeOperandEnums declares the four families, giving each shared name to the
// first family that carries it and leaving a note where one is skipped.
func writeOperandEnums(b *strings.Builder, isa *ISA) error {
	type claim struct {
		typedef string
		value   int64
	}
	claimed := make(map[string]claim)

	for _, e := range preludeEnums(isa) {
		if len(e.members) == 0 {
			return fmt.Errorf("the ISA table carries no %s members", e.typedef)
		}
		b.WriteString(e.doc)
		fmt.Fprintf(b, "typedef enum %s {\n", e.tag)
		declared := 0
		for _, member := range e.members {
			if prior, taken := claimed[member.Name]; taken {
				fmt.Fprintf(b, "    // %s = %d omitted; %s declares it as %d.\n",
					member.Name, member.Value, prior.typedef, prior.value)
				continue
			}
			claimed[member.Name] = claim{typedef: e.typedef, value: member.Value}
			fmt.Fprintf(b, "    %s", member.Name)
			if member.Deprecated {
				fmt.Fprintf(b, " [[deprecated(\"the game marks this %s retired\")]]", e.kind)
			}
			fmt.Fprintf(b, " = %d,\n", member.Value)
			declared++
		}
		if declared == 0 {
			return fmt.Errorf("every %s member is declared by an earlier family, and C has no empty enum", e.typedef)
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

// cInteger is the C type MicroC's integer type is spelled as, in the header and
// in MicroC itself.
//
// int is 32 bits on every implementation that matters and the machine holds
// every integer exactly to 53, so a program naming a value past 2^31 would be
// one C rejects or narrows. long long is at least 64 bits everywhere, which
// covers the whole range MicroC admits; long is not, being 32 bits under LLP64.
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

// intrinsicPrototypes declares every intrinsic MicroC exposes, in the order and
// with the signatures internal/sema defines.
//
// isagen cannot read that table, so nothing mechanical keeps the two in step.
// What does is the conformance gate: C23 has no implicit function declaration,
// so a program calling an intrinsic this list omits fails to compile.
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
