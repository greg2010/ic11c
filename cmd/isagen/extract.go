package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Fully qualified names of the game types the tables are recovered from.
const (
	typeScriptCommand    = "Assets.Scripts.Objects.Electrical.ScriptCommand"
	typeLogicBatchMethod = "Assets.Scripts.Objects.Electrical.LogicBatchMethod"
	typeLogicReagentMode = "Assets.Scripts.Objects.Electrical.LogicReagentMode"
	typeProgrammableChip = "Assets.Scripts.Objects.Electrical.ProgrammableChip"
	typeLogicType        = "Assets.Scripts.Objects.Motherboards.LogicType"
	typeLogicSlotType    = "Assets.Scripts.Objects.Motherboards.LogicSlotType"
	typeLogicBase        = "Assets.Scripts.Objects.Motherboards.LogicBase"
)

// Sizes the extracted tables must have. A game update that changes any of them
// is a real change to the target machine, so extraction stops and reports
// rather than writing a table that silently disagrees with the compiler's
// assumptions.
const (
	wantInstructions       = 154
	wantDeprecatedCommands = 5
	wantLogicTypes         = 358
	wantDeprecatedLogic    = 23
	wantSlotTypes          = 33
	wantBatchModes         = 5
	wantReagentModes       = 4
)

// wantConstants names the constant table in declaration order.
var wantConstants = []string{"nan", "pinf", "ninf", "pi", "tau", "deg2rad", "rad2deg", "epsilon", "rgas"}

// wantEnumMembers names members whose values downstream code depends on and
// which a reordering of the enum would silently change.
var wantEnumMembers = map[string]map[string]int64{
	"LogicBatchMethod": {"Count": 4},
	"LogicReagentMode": {"TotalContents": 3},
}

// wantExamples is the manifest of instructions the compiler is built around,
// each with the signature it must still have. A count alone would accept a
// table of the right size holding the wrong instructions, and a signature that
// changed shape is a change to what the backend may emit rather than a
// cosmetic one. Between them these cover every operand kind, both device
// spellings, and each of the load, store, batch, stack, branch, and jump
// families.
var wantExamples = map[string]string{
	"add":    "add r? a(r?|num) b(r?|num)",
	"alias":  "alias str r?|d?",
	"bdse":   "bdse device(d?|r?|id) a(r?|num)",
	"beq":    "beq a(r?|num) b(r?|num) c(r?|num)",
	"define": "define str num",
	"get":    "get r? device(d?|r?|id) address(r?|num)",
	"getd":   "getd r? id(r?|id) address(r?|num)",
	"hcf":    "hcf",
	"j":      "j int",
	"jr":     "jr int",
	"l":      "l r? device(d?|r?|id) logicType",
	"lb":     "lb r? deviceHash logicType batchMode",
	"lbn":    "lbn r? deviceHash nameHash logicType batchMode",
	"lbns":   "lbns r? deviceHash nameHash slotIndex logicSlotType batchMode",
	"ld":     "ld r? id(r?|id) logicType",
	"lr":     "lr r? device(d?|r?|id) reagentMode int",
	"ls":     "ls r? device(d?|r?|id) slotIndex logicSlotType",
	"move":   "move r? a(r?|num)",
	"peek":   "peek r?",
	"poke":   "poke address(r?|num) value(r?|num)",
	"pop":    "pop r?",
	"push":   "push a(r?|num)",
	"put":    "put device(d?|r?|id) address(r?|num) value(r?|num)",
	"putd":   "putd id(r?|id) address(r?|num) value(r?|num)",
	"s":      "s device(d?|r?|id) logicType r?",
	"sb":     "sb deviceHash logicType r?",
	"sbn":    "sbn deviceHash nameHash logicType r?",
	"sd":     "sd id(r?|id) logicType r?",
	"select": "select r? a(r?|num) b(r?|num) c(r?|num)",
	"sleep":  "sleep a(r?|num)",
	"ss":     "ss device(d?|r?|id) slotIndex logicSlotType r?",
	"yield":  "yield",
}

// wantBasicEnums names the BasicEnum member tables in declaration order with
// the size each must have. Order is load-bearing rather than incidental: the
// assembler stops at the first table holding a name, so moving one entry
// changes what a member name two enums share resolves to.
var wantBasicEnums = []struct {
	prefix  string
	members int
}{
	{prefix: "Sound", members: 46},
	{prefix: "TransmitterMode", members: 2},
	{prefix: "ElevatorMode", members: 3},
	{prefix: "Color", members: 12},
	{prefix: "EntityState", members: 4},
	{prefix: "AirControl", members: 4},
	{prefix: "DaylightSensorMode", members: 3},
	{prefix: "", members: 4},
	{prefix: "AirCon", members: 2},
	{prefix: "Vent", members: 2},
	{prefix: "FiltrationMode", members: 2},
	{prefix: "PowerMode", members: 5},
	{prefix: "RobotMode", members: 7},
	{prefix: "SortingClass", members: 11},
	{prefix: "SlotClass", members: 44},
	{prefix: "GasType", members: 31},
	{prefix: "RocketMode", members: 9},
	{prefix: "ReEntryProfile", members: 5},
	{prefix: "SorterInstruction", members: 7},
	{prefix: "PrinterInstruction", members: 10},
	{prefix: "TraderInstruction", members: 19},
	{prefix: "ShuttleType", members: 9},
	{prefix: "HashType", members: 2},
	{prefix: "DisplayMode", members: 17},
	{prefix: "SettingDisplayMode", members: 2},
	{prefix: "NodeType", members: 7},
}

// extract reads a decompiled assembly and writes the canonical ISA JSON. The
// assembly itself is read only for the file version stamped into its PE
// resource, which the decompiled source does not carry.
func extract(sourceDir, assembly, manifest, outPath string) error {
	if sourceDir == "" || assembly == "" || manifest == "" {
		return errors.New("extract needs --source, --assembly and --manifest")
	}
	tree, err := newSourceTree(sourceDir)
	if err != nil {
		return err
	}
	version, err := readAssemblyVersion(assembly)
	if err != nil {
		return err
	}

	isa, err := extractISA(tree)
	if err != nil {
		return err
	}
	isa.Manifest = manifest
	isa.Version = version

	if err := validate(isa); err != nil {
		return err
	}
	return writeISA(isa, outPath)
}

// extractISA reads the game types out of the decompiled source and assembles
// the table. Everything it returns is derived from that source, so the result
// is independent of the machine the extraction ran on.
func extractISA(tree *sourceTree) (*ISA, error) {
	sources := make(map[string]string, 7)
	for _, typeName := range []string{
		typeScriptCommand, typeLogicType, typeLogicSlotType,
		typeLogicBatchMethod, typeLogicReagentMode, typeLogicBase, typeProgrammableChip,
	} {
		src, err := tree.qualified(typeName)
		if err != nil {
			return nil, err
		}
		sources[typeName] = src
	}

	chip := sources[typeProgrammableChip]
	logicBase := sources[typeLogicBase]

	commands, err := parseEnum(sources[typeScriptCommand], "ScriptCommand")
	if err != nil {
		return nil, err
	}
	deprecatedCommands, err := parseListInitializer(logicBase, "DeprecatedCommands", "ScriptCommand")
	if err != nil {
		return nil, err
	}
	tokens, err := parseHelpStrings(chip)
	if err != nil {
		return nil, err
	}
	signatures, err := parseCommandExamples(chip, tokens)
	if err != nil {
		return nil, err
	}

	deprecated := make(map[string]bool, len(deprecatedCommands))
	for _, name := range deprecatedCommands {
		deprecated[name] = true
	}
	instructions := make([]Instruction, 0, len(commands))
	for _, member := range commands {
		operands, ok := signatures[member.Name]
		if !ok {
			return nil, fmt.Errorf("ScriptCommand.%s has no case in GetCommandExample", member.Name)
		}
		example, err := renderExample(member.Name, operands)
		if err != nil {
			return nil, err
		}
		instructions = append(instructions, Instruction{
			Mnemonic:   member.Name,
			Opcode:     int(member.Value),
			Deprecated: deprecated[member.Name],
			Example:    example,
			Operands:   operands,
		})
	}
	if len(signatures) != len(instructions) {
		return nil, fmt.Errorf("GetCommandExample covers %d commands but ScriptCommand has %d members", len(signatures), len(instructions))
	}

	logicTypes, err := markDeprecated(sources[typeLogicType], "LogicType", logicBase, "Deprecated")
	if err != nil {
		return nil, err
	}
	slotTypes, err := markDeprecated(sources[typeLogicSlotType], "LogicSlotType", logicBase, "DeprecatedSlotTypes")
	if err != nil {
		return nil, err
	}
	batchModes, err := parseEnum(sources[typeLogicBatchMethod], "LogicBatchMethod")
	if err != nil {
		return nil, err
	}
	reagentModes, err := parseEnum(sources[typeLogicReagentMode], "LogicReagentMode")
	if err != nil {
		return nil, err
	}
	constants, err := parseConstants(chip)
	if err != nil {
		return nil, err
	}
	basicEnums, err := extractBasicEnums(tree, chip)
	if err != nil {
		return nil, err
	}

	return &ISA{
		Instructions: instructions,
		LogicTypes:   logicTypes,
		SlotTypes:    slotTypes,
		BatchModes:   batchModes,
		ReagentModes: reagentModes,
		Constants:    constants,
		BasicEnums:   basicEnums,
	}, nil
}

// isaEnumTypes names the BasicEnum element types the operand enum tables above
// already carry. Their members are not repeated in BasicEnums, which is what
// keeps one game enum from being generated into two Go tables that could drift.
var isaEnumTypes = map[string]bool{"LogicType": true, "LogicSlotType": true}

// extractBasicEnums recovers the member table of every BasicEnum entry of
// InternalEnums whose members the operand enum tables do not already hold, in
// declaration order.
func extractBasicEnums(tree *sourceTree, chip string) ([]BasicEnum, error) {
	entries, err := parseInternalEnums(chip)
	if err != nil {
		return nil, err
	}
	var enums []BasicEnum
	for _, entry := range entries {
		if !entry.basic || isaEnumTypes[entry.typeName] {
			continue
		}
		if entry.deprecates {
			return nil, fmt.Errorf("BasicEnum<%s> now marks members deprecated, which a member table cannot carry", entry.typeName)
		}
		src, name, err := tree.enumType(entry.typeName)
		if err != nil {
			return nil, err
		}
		members, err := parseEnum(src, name)
		if err != nil {
			return nil, err
		}
		enums = append(enums, BasicEnum{Prefix: entry.prefix, Type: entry.typeName, Members: members})
	}
	return enums, nil
}

// markDeprecated parses an operand enum and flags the members named by the
// matching LogicBase deprecation list.
func markDeprecated(enumSrc, enumName, logicBaseSrc, listField string) ([]EnumMember, error) {
	members, err := parseEnum(enumSrc, enumName)
	if err != nil {
		return nil, err
	}
	names, err := parseListInitializer(logicBaseSrc, listField, enumName)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]int, len(members))
	for i, m := range members {
		byName[m.Name] = i
	}
	for _, name := range names {
		i, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("LogicBase.%s names %s.%s, which the enum does not declare", listField, enumName, name)
		}
		members[i].Deprecated = true
	}
	return members, nil
}

// validate enforces the table sizes and member values the compiler is built
// against. It reports every mismatch at once so a game update can be assessed
// in one pass.
func validate(isa *ISA) error {
	var problems []string
	check := func(what string, got, want int) {
		if got != want {
			problems = append(problems, fmt.Sprintf("%s: got %d, want %d", what, got, want))
		}
	}

	check("instructions", len(isa.Instructions), wantInstructions)
	check("deprecated instructions", countDeprecatedInstructions(isa.Instructions), wantDeprecatedCommands)
	check("logic types", len(isa.LogicTypes), wantLogicTypes)
	check("deprecated logic types", countDeprecated(isa.LogicTypes), wantDeprecatedLogic)
	check("logic slot types", len(isa.SlotTypes), wantSlotTypes)
	check("logic batch methods", len(isa.BatchModes), wantBatchModes)
	check("logic reagent modes", len(isa.ReagentModes), wantReagentModes)

	for enumName, members := range map[string][]EnumMember{
		"LogicBatchMethod": isa.BatchModes,
		"LogicReagentMode": isa.ReagentModes,
	} {
		for name, want := range wantEnumMembers[enumName] {
			i := slices.IndexFunc(members, func(m EnumMember) bool { return m.Name == name })
			switch {
			case i < 0:
				problems = append(problems, fmt.Sprintf("%s.%s: missing", enumName, name))
			case members[i].Value != want:
				problems = append(problems, fmt.Sprintf("%s.%s: got %d, want %d", enumName, name, members[i].Value, want))
			}
		}
	}

	got := make([]string, len(isa.Constants))
	for i, c := range isa.Constants {
		got[i] = c.Name
	}
	if !slices.Equal(got, wantConstants) {
		problems = append(problems, fmt.Sprintf("constants: got [%s], want [%s]", strings.Join(got, " "), strings.Join(wantConstants, " ")))
	}
	for _, c := range isa.Constants {
		if _, err := c.Float(); err != nil {
			problems = append(problems, err.Error())
		}
	}

	problems = append(problems, checkExamples(isa.Instructions)...)
	problems = append(problems, checkOperandKinds(isa.Instructions)...)
	problems = append(problems, checkBasicEnums(isa.BasicEnums)...)

	if len(problems) == 0 {
		return nil
	}
	slices.Sort(problems)
	return fmt.Errorf("extracted tables do not match the expected shape of manifest %s (%s):\n  %s",
		isa.Manifest, isa.Version, strings.Join(problems, "\n  "))
}

// checkExamples holds the manifest instructions to their recorded signatures.
func checkExamples(instructions []Instruction) []string {
	var problems []string
	byMnemonic := make(map[string]string, len(instructions))
	for _, instruction := range instructions {
		byMnemonic[instruction.Mnemonic] = instruction.Example
	}
	for mnemonic, want := range wantExamples {
		switch example, ok := byMnemonic[mnemonic]; {
		case !ok:
			problems = append(problems, fmt.Sprintf("instruction %s: missing", mnemonic))
		case example != want:
			problems = append(problems, fmt.Sprintf("instruction %s: got %q, want %q", mnemonic, example, want))
		}
	}
	return problems
}

// checkOperandKinds requires every operand kind the extractor recognizes to be
// reachable. A kind no instruction accepts is dead weight in the compiler's
// operand model and more likely means the signatures were parsed wrong than
// that the game dropped a whole operand class.
func checkOperandKinds(instructions []Instruction) []string {
	seen := make(map[string]bool, len(helpTokenKinds))
	for _, instruction := range instructions {
		for _, operand := range instruction.Operands {
			for _, kind := range operand.Kinds {
				seen[kind] = true
			}
		}
	}
	var problems []string
	for _, kind := range helpTokenKinds {
		if !seen[kind] {
			problems = append(problems, fmt.Sprintf("operand kind %s: no instruction accepts one", kind))
		}
	}
	return problems
}

// checkBasicEnums holds the BasicEnum tables to the manifest positionally,
// since their order decides which table a shared member name resolves through.
func checkBasicEnums(enums []BasicEnum) []string {
	var problems []string
	if len(enums) != len(wantBasicEnums) {
		problems = append(problems, fmt.Sprintf("basic enums: got %d, want %d", len(enums), len(wantBasicEnums)))
	}
	for i, want := range wantBasicEnums {
		if i >= len(enums) {
			break
		}
		got := enums[i]
		// The two are reported independently: a table can be renamed and
		// resized by one game update, and validate promises every mismatch at
		// once.
		if got.Prefix != want.prefix {
			problems = append(problems, fmt.Sprintf("basic enum %d: got prefix %q, want %q", i, got.Prefix, want.prefix))
		}
		if len(got.Members) != want.members {
			problems = append(problems, fmt.Sprintf("basic enum %q: got %d members, want %d", want.prefix, len(got.Members), want.members))
		}
	}
	return problems
}

func countDeprecated(members []EnumMember) int {
	n := 0
	for _, m := range members {
		if m.Deprecated {
			n++
		}
	}
	return n
}

func countDeprecatedInstructions(instructions []Instruction) int {
	n := 0
	for _, i := range instructions {
		if i.Deprecated {
			n++
		}
	}
	return n
}

// writeISA renders the table as indented JSON with a trailing newline. HTML
// escaping is disabled so operand spellings survive verbatim.
func writeISA(isa *ISA, path string) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(isa); err != nil {
		return fmt.Errorf("encode ISA: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
