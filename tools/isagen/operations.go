package main

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// noOperand marks a constructor argument that is not one of the line's operands.
const noOperand = -1

// maxOperationDepth bounds the walk up the operation class chain, so a cycle
// from a misparsed base list fails instead of hanging.
const maxOperationDepth = 32

// maxValueDepth bounds the walk outward through the calls and groups enclosing
// a value, so a member this misreads fails instead of looping.
const maxValueDepth = 32

// registerUse is what an operation class does with one register file entry.
type registerUse uint8

const (
	useRead registerUse = 1 << iota
	useWrite
)

// operandUses is what one instruction's operation class does with the registers
// its operands name and with the values those registers hold.
type operandUses struct {
	// uses holds, per operand position, how the class reaches the register that
	// operand names. A position absent from it is never indexed by the register
	// file, which is every operand of an instruction touching no register.
	uses map[int]registerUse
	// conversions holds, per operand position, the conversion the class reads
	// that operand's value through; see [operandUses.conversion].
	conversions map[int]Conversion
	// implicit holds how the class reaches each register no operand can name,
	// keyed by the chip field that indexes the register file by it.
	implicit map[string]registerUse
	// undetermined explains what stopped the reading, and is empty when the
	// reading finished. Everything above means nothing while it is set.
	undetermined string
}

// direction is what the operand at position gets written into the table.
// A position the class never indexes by register reads as Read, the same
// answer as an operand read through the variable classes rather than the
// register file.
func (u operandUses) direction(position int) Direction {
	return directionOf(u.uses[position])
}

// directionOf is what one register file entry's use gets written out as.
func directionOf(use registerUse) Direction {
	switch use {
	case useWrite:
		return DirectionWrite
	case useRead | useWrite:
		return DirectionReadWrite
	case useRead:
	}
	return DirectionRead
}

// implicitUses is what the class does to the registers it reaches without an
// operand naming them, ordered by register name so two extractions of one
// build produce identical bytes.
func (u operandUses) implicitUses() []ImplicitUse {
	uses := make([]ImplicitUse, 0, len(u.implicit))
	for field, use := range u.implicit {
		uses = append(uses, ImplicitUse{Register: fixedRegisters[field], Direction: directionOf(use)})
	}
	slices.SortFunc(uses, func(a, b ImplicitUse) int { return strings.Compare(a.Register, b.Register) })
	return uses
}

// conversion is what the operand at position gets written into the table.
// ConversionNone covers both the operand read as the double its register
// holds and the operand read for nothing at all: neither goes through a bound.
func (u operandUses) conversion(position int) Conversion {
	if conversion, converted := u.conversions[position]; converted {
		return conversion
	}
	return ConversionNone
}

// operationClass is one _Operation subclass: what it extends, what its
// constructor takes and hands upward, and the members its bodies are read from.
type operationClass struct {
	name string
	// base is empty at the root of the hierarchy, which declares no base class.
	base string
	// params are the constructor parameters in declaration order, and args are
	// what the constructor hands its base. args is nil when the constructor
	// declares no base call, which only the root does.
	params []string
	args   []string
	// ctorBody is the constructor's statement list, and members every
	// declaration of the class, which the register writes are read from.
	ctorBody string
	members  []csharpDecl
}

// operationReader holds the class hierarchy one chip source declares.
type operationReader struct {
	classes map[string]*operationClass
}

var (
	operationClassRE = regexp.MustCompile(`\bclass\s+(_\w+)\s*(?::\s*(_\w+))?$`)
	operationCtorRE  = regexp.MustCompile(`^public (_\w+)\(`)
	baseCallStartRE  = regexp.MustCompile(`:\s*base\s*\(`)
	// registerIndexRE finds every mention of the register file. The index is
	// matched as a bracket-free run, so a nested subscript matches only the
	// inner mention.
	registerIndexRE = regexp.MustCompile(`_Chip\._Registers\[([^\[\]]*)\]`)
	// storeIndexRE matches a local bound to the index a variable resolves to,
	// which is how every store in these bodies names the register it writes.
	storeIndexRE = regexp.MustCompile(`(\w+)\s*=\s*(_\w+)\.GetVariableIndex\(`)
	// valueBindRE matches a local bound to what a reader hands back, capturing
	// its declared type for casts checked against it later. A binding that
	// declares no type still matches, so a cast on it is refused rather than
	// missed.
	valueBindRE = regexp.MustCompile(`(?:(\w+)\s+)?(\w+)\s*=\s*(_\w+)\.(` + operandReaderPrefix + `\w*)\s*\(`)
	// directIndexRE matches the same resolution used in place rather than
	// through a local.
	directIndexRE  = regexp.MustCompile(`^(_\w+)\.GetVariableIndex\(`)
	operandArgRE   = regexp.MustCompile(`^array\[(\d+)\]$`)
	operationNewRE = regexp.MustCompile(`^\s*Operation = new (_\w+)\s*\(`)
	// variableCallRE finds every call an operation class makes on one of the
	// variables holding its operands. The receiver must be a class field, the
	// only spelling an operand's variable has; a call on a value rather than a
	// variable is [memberScan.callOn]'s.
	variableCallRE = regexp.MustCompile(`(_\w+)\.(\w+)\s*\(`)
	// namedArgRE splits a named argument into its parameter and value. A second
	// colon ends the match rather than being read through, so a qualified name
	// (`global::` or a namespace-qualified enum member) is not mistaken for a
	// named argument.
	namedArgRE = regexp.MustCompile(`(?s)^(\w+)\s*:([^:].*)?$`)
	// typedArgRE matches the two positional shapes whose own text names what
	// they are -- an _AliasTarget member and a register file entry -- neither of
	// which can be the boolean sign a reducing call asks for.
	typedArgRE = regexp.MustCompile(`^(?:_AliasTarget\.\w+|_Chip\._Registers\[[^\[\]]*\])$`)
)

// operandReaderPrefix opens the name of every method a variable hands an
// operand's value back through. A call carrying it that operandReaders has no
// entry for stops the extraction, even on a receiver no constructor binds to
// an operand.
const operandReaderPrefix = "GetVariable"

// fixedRegisters are the two registers an instruction reaches without naming
// them: the stack pointer push/pop, and the return address a linking jump
// leaves behind.
var fixedRegisters = map[string]string{
	"_Chip._StackPointerIndex":  "sp",
	"_Chip._ReturnAddressIndex": "ra",
}

// parseOperandUses recovers, for every ScriptCommand the chip's parser builds an
// operation for, how the operation reaches each of its operands' registers.
// The result covers exactly the mnemonics the parser has a case for.
func parseOperandUses(chip string) (map[string]operandUses, error) {
	decl, err := topLevelType(chip, "ProgrammableChip")
	if err != nil {
		return nil, err
	}
	members, err := splitDecls(decl.body)
	if err != nil {
		return nil, fmt.Errorf("ProgrammableChip members: %w", err)
	}

	reader, err := newOperationReader(members)
	if err != nil {
		return nil, err
	}
	built, err := parseOperationSwitch(members)
	if err != nil {
		return nil, err
	}

	uses := make(map[string]operandUses, len(built))
	for mnemonic, call := range built {
		uses[mnemonic] = reader.read(call)
	}
	return uses, nil
}

// newOperationReader indexes the operation classes ProgrammableChip declares.
func newOperationReader(members []csharpDecl) (*operationReader, error) {
	r := &operationReader{classes: make(map[string]*operationClass)}
	for _, member := range members {
		if member.kind != declContainer {
			continue
		}
		m := operationClassRE.FindStringSubmatch(strings.TrimSpace(declHeader(member.text)))
		if m == nil {
			continue
		}
		class := &operationClass{name: m[1], base: m[2]}
		nested, err := splitDecls(member.body)
		if err != nil {
			return nil, fmt.Errorf("class %s members: %w", class.name, err)
		}
		class.members = nested
		if err := class.readConstructor(); err != nil {
			return nil, err
		}
		if previous, dup := r.classes[class.name]; dup {
			return nil, fmt.Errorf("ProgrammableChip declares %s twice", previous.name)
		}
		r.classes[class.name] = class
	}
	if len(r.classes) == 0 {
		return nil, fmt.Errorf("operation classes of ProgrammableChip: %w", errNotFound)
	}
	return r, nil
}

// readConstructor records the parameters the class's own constructor takes and
// the arguments it hands its base.
//
// A class with no constructor of its own keeps nil parameters, which the walk
// treats as a shape it cannot read rather than as an empty list.
func (c *operationClass) readConstructor() error {
	for _, member := range c.members {
		m := operationCtorRE.FindStringSubmatch(member.name)
		if m == nil || m[1] != c.name {
			continue
		}
		params, end, err := matchDelim(member.name, 0, '(', ')')
		if err != nil {
			return fmt.Errorf("%s constructor parameters: %w", c.name, err)
		}
		c.params = parameterNames(params)
		if loc := baseCallStartRE.FindStringIndex(member.name[end:]); loc != nil {
			args, _, err := matchDelim(member.name[end:], loc[1]-1, '(', ')')
			if err != nil {
				return fmt.Errorf("%s base call arguments: %w", c.name, err)
			}
			c.args = trimAll(splitTop(args, ','))
		}
		body, err := memberBody(member)
		if err != nil {
			return fmt.Errorf("%s constructor: %w", c.name, err)
		}
		c.ctorBody = body
		return nil
	}
	return nil
}

// parameterNames reduces a parameter list to the names it binds. A parameter
// with a default value keeps the name ahead of it.
func parameterNames(list string) []string {
	fields := splitTop(list, ',')
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		if cut := strings.IndexByte(field, '='); cut >= 0 {
			field = field[:cut]
		}
		parts := strings.Fields(field)
		if len(parts) == 0 {
			continue
		}
		names = append(names, parts[len(parts)-1])
	}
	return names
}

func trimAll(fields []string) []string {
	out := make([]string, len(fields))
	for i, field := range fields {
		out[i] = strings.TrimSpace(field)
	}
	return out
}

// operationCall is one construction of an operation class by the chip's parser:
// the class, and the operand each constructor argument carries.
type operationCall struct {
	class string
	// operands is parallel to the constructor's argument list and holds
	// noOperand wherever an argument is not one of the line's operands.
	operands []int
}

// parseOperationSwitch recovers the operation the chip's parser builds for each
// mnemonic from the switch in _LineOfCode's constructor. Reading only that
// switch keeps GetCommandExample's case labels, which name the same mnemonics
// for a different purpose, out of the result.
func parseOperationSwitch(members []csharpDecl) (map[string]operationCall, error) {
	body, err := lineOfCodeConstructor(members)
	if err != nil {
		return nil, err
	}

	built := make(map[string]operationCall)
	var pending []string
	for i := 0; i < len(body); {
		if next := skipLiteral(body, i); next != i {
			i = next
			continue
		}
		if m := commandCaseRE.FindStringSubmatchIndex(body[i:]); m != nil && m[0] == 0 {
			pending = append(pending, body[i+m[2]:i+m[3]])
			i += m[1]
			continue
		}
		m := operationNewRE.FindStringSubmatchIndex(body[i:])
		if m == nil || m[0] != 0 {
			i++
			continue
		}
		args, end, err := matchDelim(body, i+m[1]-1, '(', ')')
		if err != nil {
			return nil, fmt.Errorf("arguments of the operation built at offset %d: %w", i, err)
		}
		// A construction with no case label ahead of it is the empty line and
		// the label line, which the parser builds before it reaches the switch.
		if len(pending) > 0 {
			operands, err := operandPositions(args)
			if err != nil {
				return nil, fmt.Errorf("operation built at offset %d: %w", i, err)
			}
			call := operationCall{class: body[i+m[2] : i+m[3]], operands: operands}
			for _, mnemonic := range pending {
				if previous, dup := built[mnemonic]; dup {
					return nil, fmt.Errorf("ScriptCommand.%s builds both %s and %s", mnemonic, previous.class, call.class)
				}
				built[mnemonic] = call
			}
			pending = nil
		}
		i = end
	}
	if len(pending) != 0 {
		return nil, fmt.Errorf("case labels %s build no operation", strings.Join(pending, ", "))
	}
	if len(built) == 0 {
		return nil, fmt.Errorf("switch building the operations: %w", errNotFound)
	}
	return built, nil
}

// lineOfCodeConstructor returns the body of the constructor that turns one line
// of source into an operation.
func lineOfCodeConstructor(members []csharpDecl) (string, error) {
	const owner = "_LineOfCode"
	for _, member := range members {
		if member.kind != declContainer || member.name != owner {
			continue
		}
		nested, err := splitDecls(member.body)
		if err != nil {
			return "", fmt.Errorf("%s members: %w", owner, err)
		}
		for _, decl := range nested {
			if !strings.HasPrefix(decl.name, "public "+owner+"(") {
				continue
			}
			body, err := memberBody(decl)
			if err != nil {
				return "", fmt.Errorf("%s constructor: %w", owner, err)
			}
			return body, nil
		}
		return "", fmt.Errorf("%s constructor: %w", owner, errNotFound)
	}
	return "", fmt.Errorf("class %s: %w", owner, errNotFound)
}

// operandPositions maps each argument of an operation construction to the
// operand it carries; the parser splits a line into the mnemonic followed by
// its operands, so array[1] is operand 0. An index matched but unreadable is
// an error, not noOperand: defaulting would report a written operand as read.
func operandPositions(args string) ([]int, error) {
	fields := splitTop(args, ',')
	positions := make([]int, len(fields))
	for i, field := range fields {
		positions[i] = noOperand
		m := operandArgRE.FindStringSubmatch(strings.TrimSpace(field))
		if m == nil {
			continue
		}
		index, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("operand index in %s: %w", strings.TrimSpace(field), err)
		}
		if index > 0 {
			positions[i] = index - 1
		}
	}
	return positions, nil
}

// read walks an operation class and the classes it inherits from, and reports
// how the whole of it reaches each operand's register.
func (r *operationReader) read(call operationCall) operandUses {
	// operandOf is the operand each variable the chain builds was built from,
	// most derived binding first, and used how the chain reaches each one.
	operandOf := make(map[string]int)
	ambiguous := make(map[string]bool)
	used := make(map[string]registerUse)
	implicit := make(map[string]registerUse)
	reads := make(map[string][]operandRead)

	name, args := call.class, call.operands
	for depth := 0; ; depth++ {
		if depth >= maxOperationDepth {
			return undetermined("%s: operation classes nested more than %d deep", call.class, maxOperationDepth)
		}
		class := r.classes[name]
		if class == nil {
			return undetermined("%s extends %s, which ProgrammableChip does not declare", call.class, name)
		}
		if class.params == nil {
			return undetermined("%s declares no constructor", class.name)
		}
		if len(class.params) != len(args) {
			return undetermined("%s takes %d constructor arguments but is built with %d",
				class.name, len(class.params), len(args))
		}

		env := make(map[string]int, len(class.params))
		for i, param := range class.params {
			env[param] = args[i]
		}
		if problem := class.bindVariables(env, operandOf, ambiguous); problem != "" {
			return operandUses{undetermined: problem}
		}
		if problem := class.collectUses(used, implicit, reads); problem != "" {
			return operandUses{undetermined: problem}
		}

		if class.args == nil {
			break
		}
		next := make([]int, len(class.args))
		for i, arg := range class.args {
			position, ok := env[arg]
			if !ok {
				position = noOperand
			}
			next[i] = position
		}
		name, args = class.base, next
		if name == "" {
			return undetermined("%s hands arguments to a base class it does not name", class.name)
		}
	}

	positions := make(map[int]registerUse, len(used))
	// Sorted, so that a class with more than one unreadable mention names the
	// same one every run and the diagnostic can be diffed.
	for _, variable := range slices.Sorted(maps.Keys(used)) {
		position, ok := operandOf[variable]
		if !ok {
			return undetermined("%s reaches the register %s names, which no constructor binds to an operand", call.class, variable)
		}
		if ambiguous[variable] {
			return undetermined("%s binds %s from more than one operand", call.class, variable)
		}
		positions[position] |= used[variable]
	}

	conversions := make(map[int]Conversion)
	for _, variable := range slices.Sorted(maps.Keys(reads)) {
		position, bound := operandOf[variable]
		for _, read := range reads[variable] {
			conversion := read.conversion
			if read.reader != "" {
				classify, known := operandReaders[read.reader]
				if !known {
					// A call on something no constructor built out of an
					// operand is one of the chip's own fields, unless its name
					// puts it in the family every reader of an operand's value
					// belongs to.
					if !bound && !strings.HasPrefix(read.reader, operandReaderPrefix) {
						continue
					}
					return undetermined("%s reads %s through %s, which says nothing about what it converts the operand's value to", call.class, variable, read.reader)
				}
				var err error
				conversion, err = classify(read.args)
				if err != nil {
					return undetermined("%s reads %s through %s: %v", call.class, variable, read.reader, err)
				}
			}
			// A read that converts nothing changes no operand's conversion, so
			// which operand it belongs to does not have to be settled. That is
			// what lets a shorthand whose long form reads a literal, and an
			// operand a constructor builds inside a branch, keep their answer.
			if conversion == ConversionNone {
				continue
			}
			switch {
			case !bound:
				return undetermined("%s converts %s through %s, and no constructor binds %s to an operand", call.class, variable, read.reader, variable)
			case ambiguous[variable]:
				return undetermined("%s binds %s from more than one operand", call.class, variable)
			}
			if previous, converted := conversions[position]; converted && previous != conversion {
				return undetermined("%s reads operand %d through both %s and %s, and one conversion is what the table holds", call.class, position, previous, conversion)
			}
			conversions[position] = conversion
		}
	}
	return operandUses{uses: positions, conversions: conversions, implicit: implicit}
}

func undetermined(format string, args ...any) operandUses {
	return operandUses{undetermined: fmt.Sprintf(format, args...)}
}

// bindVariables records the operand each variable was built from.
// Parameters are searched in declaration order rather than map order, so a
// variable built from two of them always binds the same one and is marked
// ambiguous, rather than letting map order leak into the generated tables.
func (c *operationClass) bindVariables(env map[string]int, operandOf map[string]int, ambiguous map[string]bool) string {
	statements, err := splitDecls(c.ctorBody)
	if err != nil {
		return fmt.Sprintf("%s: constructor body will not split: %v", c.name, err)
	}
	for _, statement := range statements {
		text := strings.TrimSpace(statement.text)
		cut := topLevelAssign(text)
		if cut < 0 {
			continue
		}
		variable := strings.TrimSpace(text[:cut])
		if !isFieldName(variable) {
			continue
		}
		if _, bound := operandOf[variable]; bound {
			continue
		}
		rhs := text[cut+1:]
		position, found := noOperand, false
		for _, param := range c.params {
			carried := env[param]
			if carried == noOperand || findIdent(rhs, param) < 0 {
				continue
			}
			if found {
				ambiguous[variable] = true
			}
			position, found = carried, true
		}
		if found {
			operandOf[variable] = position
		}
	}
	return ""
}

// withUses copies an operand list with each operand's direction and conversion
// filled in, so two mnemonics sharing one signature in the game's help text
// still carry facts of their own. No operand comes out undetermined: extraction
// stops before this list is written if any is (see extractISA).
func withUses(operands []Operand, uses operandUses) []Operand {
	filled := make([]Operand, len(operands))
	for i, operand := range operands {
		operand.Direction = uses.direction(i)
		operand.Conversion = uses.conversion(i)
		filled[i] = operand
	}
	return filled
}

// isFieldName reports whether text is a bare reference to one of the variables
// an operation holds its operands in, which the decompiler names with a
// leading underscore.
func isFieldName(text string) bool {
	if !strings.HasPrefix(text, "_") {
		return false
	}
	name, end := identAt(text, 0)
	return end == len(text) && name == text
}

// operandVariableClasses are the classes _Operation nests to resolve an
// operand's code to the register it stands for. They reach the register file
// themselves on behalf of no one operand, and every operation inherits them, so
// collecting their mentions would leave the whole instruction set undetermined.
var operandVariableClasses = map[string]bool{
	"Variable":             true,
	"ValueVariable":        true,
	"IndexVariable":        true,
	"IntValuedVariable":    true,
	"DoubleValueVariable":  true,
	"EnumValuedVariable":   true,
	"LineNumberVariable":   true,
	"DeviceIndexVariable":  true,
	"DeviceAliasVariable":  true,
	"DirectDeviceVariable": true,
	"IDeviceVariable":      true,
}

// operandRead is one place an operation class reads the value an operand's
// register holds. reader and args are the call and argument list that carry
// the sign of a reduction; a read with no reader is a cast applied to an
// already-read value, whose conversion is settled where it is found instead.
type operandRead struct {
	reader     string
	args       string
	conversion Conversion
}

// collectUses adds how this class's members reach each operand's register
// and the calls that read each operand's value, reporting the first mention
// it cannot resolve. A fixed-register mention is recorded against the
// instruction; anything else unresolved is refused rather than silently missed.
func (c *operationClass) collectUses(used, implicit map[string]registerUse, reads map[string][]operandRead) string {
	for _, member := range c.members {
		if member.kind != declLeaf {
			if problem := c.checkNested(member); problem != "" {
				return problem
			}
			continue
		}
		scan := newMemberScan(member.text)
		bindings := storeIndexRE.FindAllStringSubmatchIndex(member.text, -1)
		indexLocals, indexDeclares := boundLocals(member.text, bindings, 2)
		named, problem := scan.mentions(indexLocals, indexDeclares, "a local naming the register an operand resolves to")
		if problem != "" {
			return fmt.Sprintf("%s %s", c.name, problem)
		}
		// A mention of one of these locals past an assignment the resolutions
		// don't cover names a register this cannot name, so the assignment is
		// refused where it is written rather than where the mention is used.
		for _, at := range named {
			if at.writes {
				return fmt.Sprintf("%s assigns %s, which names the register an operand resolves to, outside the resolutions this follows", c.name, at.name)
			}
		}
		for _, loc := range registerIndexRE.FindAllStringSubmatchIndex(member.text, -1) {
			index := strings.TrimSpace(member.text[loc[2]:loc[3]])
			use, problem := mentionUse(member.text, loc[0], loc[1], "a register file mention")
			if problem != "" {
				return fmt.Sprintf("%s %s", c.name, problem)
			}
			if _, fixed := fixedRegisters[index]; fixed {
				// A conditional store leaves a fixed register's old value in
				// place on the paths that skip it, so it counts as readwrite
				// rather than write -- this is why all eighteen conditional
				// linking jumps come out readwrite.
				if use&useWrite != 0 && scan.guarded(loc[0]) {
					use |= useRead
				}
				implicit[index] |= use
				continue
			}
			variable, ok := localBefore(member.text, bindings, loc[0], index)
			if !ok {
				if m := directIndexRE.FindStringSubmatch(index); m != nil {
					variable, ok = m[1], true
				}
			}
			if !ok {
				return fmt.Sprintf("%s reaches the register file at %q, which names no operand's variable", c.name, index)
			}
			used[variable] |= use
			if use&useRead == 0 {
				continue
			}
			read, problem := scan.readerBefore(loc[0], loc[1])
			if problem != "" {
				return fmt.Sprintf("%s reads the register %s names, and %s", c.name, variable, problem)
			}
			if read.reader != "" {
				reads[variable] = append(reads[variable], read)
			}
			// A call written on the entry reduces as surely as one it is handed
			// to; readerBefore above only saw the bare mention.
			calls, valueEnd := scan.callsOn(loc[1])
			if len(calls) == 0 {
				continue
			}
			reads[variable] = append(reads[variable], calls...)
			enclosing, problem := scan.readerBefore(loc[0], valueEnd)
			if problem != "" {
				return fmt.Sprintf("%s reads the register %s names through %s, and %s", c.name, variable, calls[0].reader, problem)
			}
			if enclosing.reader != "" {
				reads[variable] = append(reads[variable], enclosing)
			}
		}
		for _, loc := range variableCallRE.FindAllStringSubmatchIndex(member.text, -1) {
			args, callEnd, err := matchDelim(member.text, loc[1]-1, '(', ')')
			if err != nil {
				return fmt.Sprintf("%s: arguments of %s: %v", c.name, member.text[loc[0]:loc[1]], err)
			}
			variable, call := member.text[loc[2]:loc[3]], member.text[loc[4]:loc[5]]
			reads[variable] = append(reads[variable], operandRead{reader: call, args: args})
			if !strings.HasPrefix(call, operandReaderPrefix) {
				continue
			}
			// A call written on what the read handed back reduces the value too,
			// and is the one position nothing ahead of the read can show.
			calls, valueEnd := scan.callsOn(callEnd)
			reads[variable] = append(reads[variable], calls...)
			enclosing, problem := scan.readerBefore(loc[0], valueEnd)
			if problem != "" {
				return fmt.Sprintf("%s reads %s through %s, and %s", c.name, variable, call, problem)
			}
			if enclosing.reader != "" {
				reads[variable] = append(reads[variable], enclosing)
			}
		}
		if problem := scan.collectLocalUses(c.name, reads); problem != "" {
			return problem
		}
	}
	return ""
}

// mentionUse reports what a mention spanning start to end does to the thing it
// names, and names what stopped it where the text settles nothing. subject
// names what was mentioned, for the diagnostic. The operator after says
// replace/fold/read; a ref or out ahead of the mention writes with nothing
// after it, so both sides are read.
func mentionUse(text string, start, end int, subject string) (registerUse, string) {
	operator := operatorAfter(text, end)
	use, known := operatorUses[operator]
	if !known {
		return 0, fmt.Sprintf("follows %s with %q, which says nothing about what the mention does to it", subject, operator)
	}
	prefix, prefixed := prefixUse(text, start)
	if !prefixed {
		return use, ""
	}
	if operator == "" {
		// An empty operator is a mention standing alone, which reads only where
		// nothing ahead of it says otherwise. An out parameter is the case that
		// makes the difference: the callee assigns it without reading it.
		return prefix, ""
	}
	return use | prefix, ""
}

// prefixUse reports what the text ahead of a register file mention does to the
// entry, and whether the text says anything at all. A stepping prefix is
// matched against the whole operator run, not its last two bytes: `a-- -x` and
// `--x` end in the same pair and only the second writes.
func prefixUse(text string, offset int) (registerUse, bool) {
	i := offset
	for i > 0 && isSpace(text[i-1]) {
		i--
	}
	end := i
	for i > 0 && isOperatorByte(text[i-1]) {
		i--
	}
	if i < end {
		switch text[i:end] {
		case "++", "--":
			return useRead | useWrite, true
		}
		return 0, false
	}
	for i > 0 && isIdentByte(text[i-1]) {
		i--
	}
	switch text[i:end] {
	case "ref":
		return useRead | useWrite, true
	case "out":
		return useWrite, true
	}
	return 0, false
}

// bodyCast is a cast an operation's own body applies to a value it has already
// read out of a register: the type that value was declared with, and the type
// the cast names.
type bodyCast struct{ from, to string }

// bodyCasts is what each cast an operation applies to an already-read value
// does to it. An unlisted cast stops the walk rather than passing for no
// conversion. The pair is keyed on both types, since (int) narrows a
// register's double but does nothing to an int a reader already produced.
var bodyCasts = map[bodyCast]Conversion{
	// The reagent hash rmap resolves, which arrives as the double its register
	// holds and reaches the lookup as whatever the cast makes of it
	// (ProgrammableChip.cs:5417).
	{from: "double", to: "int"}: ConversionNarrowedInt,
	// The value ins inserts into, already reduced by GetVariableLong. Both
	// types are 64 bits wide, so the cast reinterprets the pattern that
	// reduction produced rather than changing it (ibid.:3389).
	{from: "long", to: "ulong"}: ConversionNone,
}

// bodyCalls are calls that leave an operand's value unconverted by anything
// the table tracks: several round or clamp, but a double goes in and out. An
// unlisted call stops the walk rather than assuming no conversion. The
// bare-name key lists only names whose effect the source settles unambiguously.
var bodyCalls = map[string]bool{
	// The arithmetic every computing form is built out of.
	"Abs": true, "Acos": true, "Asin": true, "Atan": true, "Atan2": true,
	"Ceiling": true, "Cos": true, "Exp": true, "Floor": true, "Lerp": true,
	"Log": true, "Max": true, "Min": true, "Pow": true, "Round": true,
	"Sin": true, "Sqrt": true, "Tan": true, "Truncate": true,
	// LongToDouble widens back to the double a register holds (ibid.:6779).
	// IsNaN is the test branch forms make before comparing (ibid.:3236).
	// ToString renders (ibid.:5478); every override hands back text, so the
	// number reaches its use unreduced.
	"LongToDouble": true, "IsNaN": true, "ToString": true,
	// The device surface a logic form hands a selector or a value to, and the
	// two lookups that resolve which device an operand names.
	"BatchRead": true, "CanLogicRead": true, "CanLogicWrite": true,
	"GetLogicValue": true, "SetLogicValue": true,
	"GetLogicableFromId": true, "GetLogicableFromIndex": true,
	// Device memory, via lr (ibid.:2884).
	// The hash rmap resolves (ibid.:5417).
	// The alias record a preprocessor form makes (ibid.:2954).
	// Excludes Get: StringManager's int overload narrows it (StringManager.cs:167).
	"ReadMemory": true, "WriteMemory": true, "Find": true,
	"GetPrefabHashFromReagentHash": true, "_AliasValue": true,
}

// collectLocalUses adds what each mention of a local the member bound to an
// operand's value does to that value, and reports the first mention it cannot
// read. A narrowing can be written as a call the value is handed to, a call on
// the value, a parenthesized value, or the result of either call, so every
// mention is read rather than only the ones a cast sits over.
func (m memberScan) collectLocalUses(class string, reads map[string][]operandRead) string {
	bindings := valueBindRE.FindAllStringSubmatchIndex(m.text, -1)
	if len(bindings) == 0 {
		return ""
	}
	bound, declares := boundLocals(m.text, bindings, 4)
	found, problem := m.mentions(bound, declares, "a local holding what it read of an operand")
	if problem != "" {
		return fmt.Sprintf("%s %s", class, problem)
	}
	// The assignment itself is not refused, only a conversion past one: the
	// game assigns over a value it read and goes on to convert nothing out of
	// it, and a mention that converts nothing settles the same answer whichever
	// value it holds.
	stale := make(map[string]bool)
	for _, at := range found {
		switch {
		case at.binds:
			delete(stale, at.name)
			continue
		case at.writes:
			stale[at.name] = true
			continue
		}
		declared, variable, ok := valueBefore(m.text, bindings, at.start, at.name)
		if !ok {
			continue
		}
		conversion, problem := m.localUseAhead(at.start, at.end, at.name, declared)
		if problem != "" {
			return fmt.Sprintf("%s %s", class, problem)
		}
		if conversion == ConversionNone {
			continue
		}
		if stale[at.name] {
			return fmt.Sprintf("%s converts %s, which it assigned over after reading it, so whether the bound is the operand's is not something this reads", class, at.name)
		}
		reads[variable] = append(reads[variable], operandRead{conversion: conversion})
	}
	return ""
}

// boundLocals reduces a member's bindings to the names they bind and the
// offsets they bind them at. group is the submatch index of the bound name.
func boundLocals(text string, bindings [][]int, group int) (names map[string]bool, declares map[int]bool) {
	names = make(map[string]bool, len(bindings))
	declares = make(map[int]bool, len(bindings))
	for _, binding := range bindings {
		names[text[binding[group]:binding[group+1]]] = true
		declares[binding[group]] = true
	}
	return names, declares
}

// mention is one place a member names a local its bindings bind, in the order
// the member writes them.
type mention struct {
	name       string
	start, end int
	// binds marks the mention one of the bindings makes, and writes one that
	// assigns to the local from something the bindings do not cover.
	binds, writes bool
}

// mentions returns every place the member names one of the locals, and reports
// what stopped it where a mention settles nothing. An assignment the bindings
// don't cover leaves such mentions only marked, not resolved; each caller
// refuses where a stale binding would otherwise become an answer.
func (m memberScan) mentions(names map[string]bool, declares map[int]bool, subject string) ([]mention, string) {
	var found []mention
	for i := 0; i < len(m.text); {
		if next := skipLiteral(m.text, i); next != i {
			i = next
			continue
		}
		if !isIdentByte(m.text[i]) || (i > 0 && isIdentByte(m.text[i-1])) {
			i++
			continue
		}
		name, end := identAt(m.text, i)
		if !names[name] {
			i = end
			continue
		}
		use, problem := mentionUse(m.text, i, end, subject)
		if problem != "" {
			return nil, problem
		}
		found = append(found, mention{
			name:   name,
			start:  i,
			end:    end,
			binds:  declares[i],
			writes: use&useWrite != 0 && !declares[i],
		})
		i = end
	}
	return found, ""
}

// localUseAhead reports what the text ahead of a mention of the local named
// name does to the value it holds. declared is the type the binding gave it;
// a cast replaces it, and it is emptied once the walk steps outward through a
// call, whose return type this cannot otherwise read.
func (m memberScan) localUseAhead(start, end int, name, declared string) (Conversion, string) {
	conversion := ConversionNone
	for depth := 0; ; depth++ {
		if depth >= maxValueDepth {
			return ConversionNone, fmt.Sprintf("encloses %s in more than %d calls", name, maxValueDepth)
		}
		if call, _, callEnd, on := m.callOn(end); on {
			if !bodyCalls[call] {
				return ConversionNone, fmt.Sprintf("reads %s through %s, which says nothing about what it converts the value to", name, call)
			}
			end, declared = callEnd, ""
			continue
		}
		i := m.spaceBefore(start)
		if i == 0 {
			return conversion, ""
		}
		switch b := m.text[i-1]; {
		case b == ')':
			cast, to, problem := m.castBefore(i-1, name, declared)
			if problem != "" {
				return ConversionNone, problem
			}
			if to == "" {
				// A keyword's clause rather than a cast, so nothing encloses
				// the value that this has not already read.
				return conversion, ""
			}
			// A cast that converts nothing leaves a bound found further in
			// standing, rather than answering over it.
			if cast != ConversionNone {
				conversion = cast
			}
			start, declared = m.opens[i-1], to
			continue
		case b == '(':
			i--
		case b == ',':
			open := m.enclosing[i-1]
			if open < 0 {
				return conversion, ""
			}
			i = open
		case b == ':':
			open, named := m.namedArgument(i - 1)
			if !named {
				return conversion, ""
			}
			i = open
		case b == ';' || b == '{' || b == '}' || b == '[' || isOperatorByte(b):
			return conversion, ""
		default:
			token := tokenEndingAt(m.text, i)
			if valueKeywords[token] || statementKeywords[token] {
				return conversion, ""
			}
			return ConversionNone, fmt.Sprintf("puts %q ahead of %s, which says nothing about what converts the value", token, name)
		}
		if !m.wholeOf(end, i) {
			return conversion, ""
		}

		call, callStart := m.callBefore(i)
		switch {
		case call == "":
			// A group rather than a call, unless a cast closes ahead of it: the
			// group itself says nothing about the type it starts from.
			if before := m.spaceBefore(callStart); before > 0 && m.text[before-1] == ')' {
				return ConversionNone, fmt.Sprintf("casts a parenthesized %s, which is not a shape this reads", name)
			}
			start, end = callStart, m.closes[i]+1
			continue
		case clauseKeywords[call]:
			return conversion, ""
		case !bodyCalls[call]:
			return ConversionNone, fmt.Sprintf("hands %s to %s, which says nothing about what it converts the value to", name, call)
		}
		start, end, declared = callStart, m.closes[i]+1, ""
	}
}

// castBefore reads the cast whose closing parenthesis sits at closing and
// reports what it does to a value the binding declared with the given type,
// together with the type the cast names. An empty type means the parentheses
// are a keyword's clause rather than a cast, and the value stands on its own.
func (m memberScan) castBefore(closing int, name, declared string) (conversion Conversion, to, problem string) {
	// A parenthesis absent from the pairing map would otherwise slice from
	// offset zero and read a cast out of the member's own header.
	open, paired := m.opens[closing]
	if !paired {
		return ConversionNone, "", fmt.Sprintf("puts %s after a parenthesis this did not pair", name)
	}
	// A keyword's parenthesized clause followed by a statement has the shape of
	// a cast and is not one.
	if clauseKeywords[tokenEndingAt(m.text, m.spaceBefore(open))] {
		return ConversionNone, "", ""
	}
	to = strings.TrimSpace(m.text[open+1 : closing])
	if !isBareIdent(to) {
		return ConversionNone, "", fmt.Sprintf("casts %s through %q, which is not a plain type name", name, to)
	}
	conversion, known := bodyCasts[bodyCast{from: declared, to: to}]
	if !known {
		return ConversionNone, "", fmt.Sprintf("casts %s, which holds what it read, from %q to %q, and what that does to the value is not something this reads", name, declared, to)
	}
	return conversion, to, ""
}

// clauseKeywords are the C# keywords whose parenthesized clause is not an
// argument list. Anything else ahead of an open parenthesis is the call being
// made, so a keyword missing from here reads as a call nothing classifies and
// stops the extraction rather than passing for no conversion.
var clauseKeywords = map[string]bool{
	"if": true, "while": true, "for": true, "foreach": true,
	"switch": true, "lock": true, "using": true, "catch": true, "fixed": true,
}

// valueKeywords are the C# keywords that can stand immediately ahead of a value
// without converting it: the one that hands it back, the two that pass the
// storage it sits in rather than the value itself, and the one that introduces
// the constructor a value is handed to.
var valueKeywords = map[string]bool{"return": true, "ref": true, "out": true, "new": true}

// statementKeywords are the two C# keywords that can stand immediately ahead
// of a statement written without braces, where a local mentioned there is
// mentioned as itself. Every other block keyword takes a parenthesized clause
// or a brace before the statement.
var statementKeywords = map[string]bool{"else": true, "do": true}

// isBareIdent reports whether text is one identifier and nothing else, which is
// the only spelling of a cast target this reads.
func isBareIdent(text string) bool {
	name, end := identAt(text, 0)
	return name != "" && end == len(text)
}

// memberScan is one class member's text together with the parenthesis pairing
// the readings of it need. Pairing every parenthesis once lets a value be
// followed back to the call it is an argument of from any argument position,
// not only the first, and lets a cast be read from its closing parenthesis.
type memberScan struct {
	text string
	// opens maps the offset of each ')' to the offset of the '(' it closes, and
	// closes the reverse.
	opens  map[int]int
	closes map[int]int
	// enclosing holds, per byte, the offset of the innermost parenthesis still
	// open there, or -1 where none is. A parenthesis byte itself carries the
	// group around it rather than its own.
	enclosing []int
	// braces holds, per byte, how many brace blocks are open there. One is the
	// member's own body, and anything deeper is a block some paths through the
	// member do not run.
	braces []int
}

// newMemberScan pairs the parentheses of a member's text. Literals are skipped,
// so a parenthesis inside a string neither opens nor closes a group.
func newMemberScan(text string) memberScan {
	scan := memberScan{
		text:      text,
		opens:     make(map[int]int),
		closes:    make(map[int]int),
		enclosing: make([]int, len(text)),
		braces:    make([]int, len(text)),
	}
	depth := 0
	var open []int
	innermost := func() int {
		if len(open) == 0 {
			return -1
		}
		return open[len(open)-1]
	}
	for i := 0; i < len(text); {
		if next := skipLiteral(text, i); next != i {
			for ; i < next && i < len(text); i++ {
				scan.enclosing[i] = innermost()
				scan.braces[i] = depth
			}
			continue
		}
		switch text[i] {
		case ')':
			if len(open) > 0 {
				scan.opens[i] = open[len(open)-1]
				scan.closes[open[len(open)-1]] = i
				open = open[:len(open)-1]
			}
		case '}':
			depth--
		}
		scan.enclosing[i] = innermost()
		scan.braces[i] = depth
		switch text[i] {
		case '(':
			open = append(open, i)
		case '{':
			depth++
		}
		i++
	}
	return scan
}

// guarded reports whether the offset sits inside a block nested within the
// member's own body, which is a statement some paths through the member do not
// reach.
func (m memberScan) guarded(offset int) bool {
	return offset < len(m.braces) && m.braces[offset] > 1
}

// spaceBefore returns the offset of the first byte at or before offset that
// layout whitespace does not fill.
func (m memberScan) spaceBefore(offset int) int {
	for offset > 0 && isSpace(m.text[offset-1]) {
		offset--
	}
	return offset
}

// spaceAfter returns the offset of the first byte at or after offset that
// layout whitespace does not fill.
func (m memberScan) spaceAfter(offset int) int {
	for offset < len(m.text) && isSpace(m.text[offset]) {
		offset++
	}
	return offset
}

// wholeOf reports whether a value ending at end fills the group the
// parenthesis at open starts, either alone or as one whole argument. A value
// that does not is one term of a larger expression: (ulong)((1L << distance) -
// 1) converts the shifted mask and not the distance.
func (m memberScan) wholeOf(end, open int) bool {
	after := m.spaceAfter(end)
	if after >= len(m.text) {
		return false
	}
	return m.text[after] == ',' || after == m.closes[open]
}

// callOn returns the call written on the value ending at end, together with
// its argument list and the offset just past it. It reports false where no
// call is written there. This is the one position a walk backwards from a
// value cannot reach, since nothing ahead of the value says the call happened.
func (m memberScan) callOn(end int) (name, args string, callEnd int, ok bool) {
	i := m.spaceAfter(end)
	if i >= len(m.text) || m.text[i] != '.' {
		return "", "", 0, false
	}
	name, next := identAt(m.text, m.spaceAfter(i+1))
	if name == "" {
		return "", "", 0, false
	}
	open := m.spaceAfter(next)
	if open >= len(m.text) || m.text[open] != '(' {
		return "", "", 0, false
	}
	closing, paired := m.closes[open]
	if !paired {
		return "", "", 0, false
	}
	return name, m.text[open+1 : closing], closing + 1, true
}

// callsOn returns every call written in sequence on the value ending at end,
// innermost first, together with the offset just past the last of them. The
// whole chain is returned rather than its first call, because each link
// reduces what the one before it handed back.
func (m memberScan) callsOn(end int) (calls []operandRead, valueEnd int) {
	valueEnd = end
	for range maxValueDepth {
		name, args, next, on := m.callOn(valueEnd)
		if !on {
			break
		}
		calls = append(calls, operandRead{reader: name, args: args})
		valueEnd = next
	}
	return calls, valueEnd
}

// callBefore returns the name of the call whose argument list the parenthesis
// at offset opens, together with the offset the call expression starts at.
// The name is empty where the parenthesis groups an expression instead, and
// the offset is then the parenthesis itself. start is the whole receiver
// chain, not just the name, so Math.Round steps outward to ahead of Math.
func (m memberScan) callBefore(offset int) (name string, start int) {
	end := m.spaceBefore(offset)
	i := end
	for i > 0 && isIdentByte(m.text[i-1]) {
		i--
	}
	if i == end {
		return "", offset
	}
	name = m.text[i:end]
	for i > 0 && (m.text[i-1] == '.' || isIdentByte(m.text[i-1])) {
		i--
	}
	if i > 0 && m.text[i] == '.' && m.text[i-1] == ')' {
		if group, paired := m.opens[i-1]; paired {
			i = group
		}
	}
	return name, i
}

// namedArgument reports the parenthesis opening the argument list the named
// argument ending at the colon stands in, and whether the colon introduces one
// at all. A colon anywhere else -- a conditional, a case label, a base call --
// names no argument.
func (m memberScan) namedArgument(colon int) (open int, named bool) {
	end := m.spaceBefore(colon)
	i := end
	for i > 0 && isIdentByte(m.text[i-1]) {
		i--
	}
	if i == end {
		return 0, false
	}
	i = m.spaceBefore(i)
	if i == 0 {
		return 0, false
	}
	switch m.text[i-1] {
	case '(':
		return i - 1, true
	case ',':
		open = m.enclosing[i-1]
		return open, open >= 0
	}
	return 0, false
}

// readerBefore returns the call whose argument the text at offset is, together
// with a problem naming what it stopped on where the text settles nothing. An
// empty reader means the value stands on its own; a cast or any other bare
// identifier ahead of it settles nothing and is refused, since a cast reduces
// as surely as a call and reading it as no conversion is how a bound goes
// missing.
func (m memberScan) readerBefore(start, end int) (operandRead, string) {
	i := m.spaceBefore(start)
	if i == 0 {
		return operandRead{}, ""
	}
	switch b := m.text[i-1]; {
	case b == '(':
		i--
	case b == ',':
		open := m.enclosing[i-1]
		if open < 0 {
			return operandRead{}, ""
		}
		i = open
	case b == ':':
		open, named := m.namedArgument(i - 1)
		if !named {
			return operandRead{}, ""
		}
		i = open
	case b == ';' || b == '{' || b == '}' || isOperatorByte(b):
		return operandRead{}, ""
	default:
		token := tokenEndingAt(m.text, i)
		if valueKeywords[token] {
			return operandRead{}, ""
		}
		return operandRead{}, fmt.Sprintf("what stands ahead of it, %q, says nothing about what converts the entry", token)
	}

	name, _ := m.callBefore(i)
	if name == "" || clauseKeywords[name] || !m.wholeOf(end, i) {
		return operandRead{}, ""
	}
	args, _, err := matchDelim(m.text, i, '(', ')')
	if err != nil {
		return operandRead{}, fmt.Sprintf("the arguments of the %s ahead of it will not parse: %v", name, err)
	}
	return operandRead{reader: name, args: args}, ""
}

// tokenEndingAt returns the identifier ending at i, or the single byte ahead of
// it where that is not an identifier. It names what a reading stopped on, and
// is empty where nothing stands ahead of i at all.
func tokenEndingAt(text string, i int) string {
	start := i
	for start > 0 && isIdentByte(text[start-1]) {
		start--
	}
	if start == i && i > 0 {
		start = i - 1
	}
	return text[start:i]
}

// operandReaders is what each call an operation makes with an operand's value
// does to that value, keyed by the call's bare name. The GetVariable family
// are methods on the operand's own variable; DoubleToLong is the chip's own
// static, used where an operation reads a register's old value directly.
var operandReaders = map[string]func(args string) (Conversion, error){
	// GetVariableIndex resolves which register an operand names, and
	// GetDevice which device; neither converts a value the instruction
	// computes with.
	"GetVariableIndex": unconverted,
	"GetDevice":        unconverted,
	// GetVariableValue hands back what the variable holds without reducing it
	// to 64 bits.
	"GetVariableValue": unconverted,
	"GetVariableInt":   func(string) (Conversion, error) { return ConversionInt, nil },
	// GetVariableLong declares signed with a default of true; DoubleToLong
	// declares it without one, so a call that names neither is a shape this
	// cannot read rather than a signed reduction.
	"GetVariableLong": reducedLong(true),
	"DoubleToLong":    reducedLong(false),
	// Find looks up by the hash already produced (ibid.:2884); IsNaN tests
	// the double as-is (ibid.:4461). Neither reduces. Both are in [bodyCalls]
	// too, consulted from the other side: this table when the call encloses
	// the read, that one when it encloses the bound local.
	"Find":  unconverted,
	"IsNaN": unconverted,
}

func unconverted(string) (Conversion, error) { return ConversionNone, nil }

// reducedLong classifies a call that reduces through DoubleToLong by the sign
// it asks for. defaulted says whether the call's signed parameter has a default.
func reducedLong(defaulted bool) func(string) (Conversion, error) {
	return func(args string) (Conversion, error) {
		signed, named, err := signedArgument(args)
		switch {
		case err != nil:
			return ConversionUnknown, err
		case !named && !defaulted:
			return ConversionUnknown, fmt.Errorf("the arguments %q name no sign, and this call declares no default for one", args)
		case signed:
			return ConversionSignedLong, nil
		}
		return ConversionUnsignedLong, nil
	}
}

// signedArgument reads the sign a reducing call asks for, and reports whether
// the call named it. The sign is read only from a named argument; every other
// field must resolve to some other parameter or the call is refused, since a
// field wrongly passed over would leave the default (signed) standing.
func signedArgument(args string) (signed, named bool, err error) {
	signed = true
	for _, field := range splitTop(args, ',') {
		field = strings.TrimSpace(field)
		m := namedArgRE.FindStringSubmatch(field)
		switch {
		case m == nil:
			if !typedArgRE.MatchString(field) {
				return false, false, fmt.Errorf("the arguments %q pass %s, which names no parameter and no type, so which parameter carries the sign is not something this reads", args, field)
			}
		case m[1] != "signed":
		case named:
			return false, false, fmt.Errorf("the arguments %q name the sign twice", args)
		default:
			value := strings.TrimSpace(m[2])
			if value != "true" && value != "false" {
				return false, false, fmt.Errorf("the arguments %q give the sign as %s, which is not a boolean literal this reads", args, value)
			}
			signed, named = value == "true", true
		}
	}
	return signed, named, nil
}

// checkNested reports a type nested in the class that could reach the
// register file or an operand's variable without this collecting it. Nothing
// descends into a nested type; enums are exempt (their members are
// constants), as are the operand variable classes, by design.
func (c *operationClass) checkNested(member csharpDecl) string {
	if member.kind == declEnum || operandVariableClasses[member.name] {
		return ""
	}
	return fmt.Sprintf("%s nests %s, which is not one of the operand variable classes, and what a nested type reaches is not collected", c.name, member.name)
}

// localBefore returns the variable the local named name was last resolved from
// ahead of offset, which keeps a member that binds one name twice from
// attributing its earlier mentions to its later binding.
func localBefore(text string, bindings [][]int, offset int, name string) (string, bool) {
	variable, found := "", false
	for _, binding := range bindings {
		if binding[1] > offset {
			break
		}
		if text[binding[2]:binding[3]] == name {
			variable, found = text[binding[4]:binding[5]], true
		}
	}
	return variable, found
}

// valueBefore returns the operand variable whose value the local named name was
// last bound to ahead of offset, together with the type that binding declared
// it with. The bindings are [valueBindRE]'s.
func valueBefore(text string, bindings [][]int, offset int, name string) (declared, variable string, found bool) {
	for _, binding := range bindings {
		if binding[1] > offset {
			break
		}
		if text[binding[4]:binding[5]] != name {
			continue
		}
		declared, variable, found = "", text[binding[6]:binding[7]], true
		if binding[2] >= 0 {
			declared = text[binding[2]:binding[3]]
		}
	}
	return declared, variable, found
}

// operatorAfter returns the operator token beginning at or after i, skipping the
// whitespace ahead of it. It is empty where nothing operator-shaped follows,
// which is the mention standing alone as an argument, a receiver or the whole
// right hand side of something else.
func operatorAfter(text string, i int) string {
	for i < len(text) && isSpace(text[i]) {
		i++
	}
	start := i
	for i < len(text) && isOperatorByte(text[i]) {
		i++
	}
	return text[start:i]
}

// operatorUses says what the operator following a register file mention does
// to the entry: `=` replaces it, a compound or stepping form folds a new
// value in, and everything else reads it. An operator absent from here stops
// collectUses rather than defaulting to read, which is how a store goes missing.
var operatorUses = map[string]registerUse{
	"": useRead,

	"+": useRead, "-": useRead, "*": useRead, "/": useRead, "%": useRead,
	"<": useRead, ">": useRead, "<<": useRead, ">>": useRead, ">>>": useRead,
	"==": useRead, "!=": useRead, "<=": useRead, ">=": useRead,
	"&": useRead, "|": useRead, "^": useRead, "&&": useRead, "||": useRead,
	"?": useRead, ":": useRead, "??": useRead,

	"=": useWrite,

	"++": useRead | useWrite, "--": useRead | useWrite,
	"+=": useRead | useWrite, "-=": useRead | useWrite, "*=": useRead | useWrite,
	"/=": useRead | useWrite, "%=": useRead | useWrite, "&=": useRead | useWrite,
	"|=": useRead | useWrite, "^=": useRead | useWrite, "<<=": useRead | useWrite,
	">>=": useRead | useWrite, ">>>=": useRead | useWrite, "??=": useRead | useWrite,
}
