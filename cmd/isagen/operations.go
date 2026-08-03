package main

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The direction of an operand is read from the _Operation subclass the chip's
// own parser builds for each mnemonic. That is a different part of the game
// source from the help text an operand list comes from, which is what makes the
// two readings worth comparing: the help text says where a bare r? sits, and
// these classes say which operand's register the instruction assigns.
//
// An instruction reaches the register an operand names when that operand's code
// reaches a variable whose index the class indexes _Chip._Registers by. Every
// such mention is classified, in either direction: the operator that follows it
// says whether the class is reading the entry, replacing it, or folding a new
// value into the old one. Nothing here infers a direction from position, and
// nothing guesses: a mention this cannot resolve to an operand's variable, one
// whose operator it has no classification for, and a nested type it does not
// descend into all leave the instruction undetermined, which extraction refuses
// to write out. A wrong direction is a miscompile register allocation gives no
// diagnostic for; an undetermined one stops the build.

// noOperand marks a constructor argument that carries no operand: the chip and
// line number every operation takes, and the literals the shorthand forms pass
// in place of an operand their long form reads.
const noOperand = -1

// maxOperationDepth bounds the walk up the operation class chain. The deepest
// real chain is a fraction of this; the bound exists so a cycle introduced by a
// misparsed base list fails instead of hanging.
const maxOperationDepth = 32

// registerUse is what an operation class does with one register file entry.
type registerUse uint8

const (
	useRead registerUse = 1 << iota
	useWrite
)

// operandUses is what one instruction's operation class does with the registers
// its operands name.
type operandUses struct {
	// uses holds, per operand position, how the class reaches the register that
	// operand names. A position absent from it is one the class never indexes
	// the register file by, which is every operand of an instruction that
	// touches no register at all.
	uses map[int]registerUse
	// undetermined explains what stopped the reading, and is empty when the
	// reading finished. The uses above mean nothing while it is set.
	undetermined string
}

// direction is what the operand at position gets written into the table as.
//
// Reading is the answer for a position the class never indexes the register
// file by, and for one it only reads: an operand whose value the instruction
// consumes through the variable classes rather than through the register file
// leaves no mention here at all, and the two are the same direction.
func (u operandUses) direction(position int) Direction {
	switch u.uses[position] {
	case useWrite:
		return DirectionWrite
	case useRead | useWrite:
		return DirectionReadWrite
	case useRead:
	}
	return DirectionRead
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
	// registerIndexRE finds every mention of the register file. Whether a
	// mention is an assignment is decided by the text after it rather than here,
	// so that a comparison is not read as a store.
	registerIndexRE = regexp.MustCompile(`_Chip\._Registers\[([^\[\]]*)\]`)
	// storeIndexRE matches a local bound to the index a variable resolves to,
	// which is how every store in these bodies names the register it writes.
	storeIndexRE = regexp.MustCompile(`(\w+)\s*=\s*(_\w+)\.GetVariableIndex\(`)
	// directIndexRE matches the same resolution used in place rather than
	// through a local.
	directIndexRE  = regexp.MustCompile(`^(_\w+)\.GetVariableIndex\(`)
	operandArgRE   = regexp.MustCompile(`^array\[(\d+)\]$`)
	operationNewRE = regexp.MustCompile(`^\s*Operation = new (_\w+)\s*\(`)
)

// fixedRegisters are the two registers an instruction can write without naming
// them: the stack pointer push and pop move, and the return address the linking
// jumps leave behind. Neither is an operand, and the compiler models both as
// part of what the mnemonic means rather than as a value it allocates.
var fixedRegisters = map[string]bool{
	"_Chip._StackPointerIndex":  true,
	"_Chip._ReturnAddressIndex": true,
}

// parseOperandUses recovers, for every ScriptCommand the chip's parser builds an
// operation for, how the operation reaches each of its operands' registers.
//
// The result covers exactly the mnemonics the parser has a case for, so a
// mnemonic missing from it is an instruction the source no longer builds and is
// reported by the caller rather than defaulted.
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
// mnemonic from the switch in _LineOfCode's constructor.
//
// Reading that switch rather than the whole file is what keeps the case labels
// of GetCommandExample, which name the same mnemonics for a different purpose,
// out of the result.
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
// operand it carries. The parser splits a line into the mnemonic followed by
// its operands, so array[1] is operand 0.
//
// An index this program matched and could not read is an error rather than an
// argument carrying no operand: the direction of an operand nothing is known
// about defaults to read, which would report an operand the instruction writes
// as one it only reads.
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
		if problem := class.collectUses(used); problem != "" {
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
	return operandUses{uses: positions}
}

func undetermined(format string, args ...any) operandUses {
	return operandUses{undetermined: fmt.Sprintf(format, args...)}
}

// bindVariables records the operand each variable this class's constructor
// builds was built from, and reports what stopped it. A variable already bound
// by a more derived class keeps that binding, which is the one an override would
// have replaced. A variable no operand-carrying parameter reaches is left
// unbound rather than bound to noOperand, so every position recorded here names
// an operand.
//
// A body that will not split binds nothing, and a class that binds nothing and
// stores no register reads as an instruction whose operands are all read. That
// is a direction rather than an absence of one, so the failure is reported here
// rather than left to surface somewhere it cannot.
//
// The parameters are searched in declaration order rather than in the order env
// enumerates them, so that a variable built from two of them binds the same one
// every run. The binding is marked ambiguous in that case and the caller
// discards the operation, but a binding that depends on map iteration order
// would put the run number into the generated tables the moment any reader of
// it stops consulting that mark.
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

// withDirections copies an operand list with each operand's direction filled
// in. The copy is what lets two mnemonics share one signature in the game's
// help text and still carry directions of their own.
//
// No operand comes out undetermined: an operation that left any mention of the
// register file unread stops the extraction before this list is written. See
// extractISA.
func withDirections(operands []Operand, uses operandUses) []Operand {
	directed := make([]Operand, len(operands))
	for i, operand := range operands {
		operand.Direction = uses.direction(i)
		directed[i] = operand
	}
	return directed
}

// isFieldName reports whether text is a bare reference to one of the variables
// an operation holds its operands in, which the decompiler names with a leading
// underscore. Anything else on the left of an assignment -- an element, a
// property of something else, a local declaration -- is not one.
func isFieldName(text string) bool {
	if !strings.HasPrefix(text, "_") {
		return false
	}
	name, end := identAt(text, 0)
	return end == len(text) && name == text
}

// operandVariableSuffix ends the name of every class _Operation nests to resolve
// an operand's code to the register it stands for. Those classes reach the
// register file themselves -- the recursion an rr? spelling asks for, and the
// fetch behind a value read -- and what they reach belongs to no one operand.
// Every operation inherits them, so collecting their mentions would leave the
// whole instruction set undetermined.
const operandVariableSuffix = "Variable"

// collectUses adds how this class's members reach the register each operand's
// variable names, and reports the first mention it cannot resolve.
//
// A mention indexed by one of the fixed registers is not an operand's and is
// skipped. A mention indexed by anything else at all is reported, in either
// direction: a store read as no store and a load read as no load are both the
// silent wrong answer, and the second is what leaves a value the instruction
// consumes looking dead.
func (c *operationClass) collectUses(used map[string]registerUse) string {
	for _, member := range c.members {
		if member.kind != declLeaf {
			if problem := c.checkNested(member); problem != "" {
				return problem
			}
			continue
		}
		bindings := storeIndexRE.FindAllStringSubmatchIndex(member.text, -1)
		for _, loc := range registerIndexRE.FindAllStringSubmatchIndex(member.text, -1) {
			index := strings.TrimSpace(member.text[loc[2]:loc[3]])
			if fixedRegisters[index] {
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
			operator := operatorAfter(member.text, loc[1])
			use, known := operatorUses[operator]
			if !known {
				return fmt.Sprintf("%s follows a register file mention with %q, which says nothing about what the mention does to the entry", c.name, operator)
			}
			used[variable] |= use
		}
	}
	return ""
}

// checkNested reports a type nested in the class that reaches the register file
// and is not one of the operand variable classes.
//
// Nothing descends into a nested type, so a mention inside one is not collected
// at all and the operand it belongs to comes out read. That is the right answer
// for the operand variable classes and a guess everywhere else, and the guess is
// the one that hides a store.
func (c *operationClass) checkNested(member csharpDecl) string {
	if strings.HasSuffix(member.name, operandVariableSuffix) || !registerIndexRE.MatchString(member.text) {
		return ""
	}
	return fmt.Sprintf("%s nests %s, which reaches the register file and is not one of the operand variable classes", c.name, member.name)
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

// operatorUses says what the operator following a register file mention does to
// the entry: a plain assignment replaces it, a compound one and the two steps
// fold a new value into the old, and everything else reads it.
//
// An operator absent from here has no classification, and collectUses stops on
// one rather than choosing between the three. The choice a reader is tempted to
// make is that anything it does not recognize only reads, which is exactly how a
// store goes missing: register allocation gives no diagnostic for a definition
// it was never told about.
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
