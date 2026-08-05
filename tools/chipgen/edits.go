package main

import (
	"fmt"
	"slices"
	"strings"
)

// applyChipEdits renders the kept declarations with the changes the
// standalone unit needs, and nothing else. Each edit is enumerated here
// rather than spread through the emitter, so a reader checking the
// difference between the unit and the game's own code has one place to read.
func applyChipEdits(body scope, kept []decl, s *slicing) (string, error) {
	parts := make([]string, 0, len(kept)+1)
	for _, d := range kept {
		text, err := editDecl(body, d, s)
		if err != nil {
			return "", err
		}
		if text == "" {
			continue
		}
		parts = append(parts, "\t// "+body.span(d)+"\n"+nest(text))
	}
	parts = append(parts, injectedChipState)
	return strings.Join(parts, "\n\n"), nil
}

// injectedChipState is the state the dropped base class used to carry.
// NetworkUpdateFlags is where the four error properties record a dirty
// flag, unread here, so their bodies stay as they are. CircuitHousing
// replaces an expression body that walked the inventory graph to find the housing.
const injectedChipState = `	// Injected: the state ProgrammableChip reached through Item.
	private int NetworkUpdateFlags;

	public ICircuitHolder CircuitHousing;

	// Injected: read-only views onto the machine state. The chip keeps all of
	// this private and exposes it to the game only as tooltip text, lossy where
	// a conformance run cares: an error type reads as a localized string, a
	// register as a rounded decimal.
	public double[] HarnessRegisters
	{
		get { return _Registers; }
	}

	public double[] HarnessStack
	{
		get { return _Stack; }
	}

	public int HarnessNextAddress
	{
		get { return _NextAddr; }
	}

	public int HarnessLineCount
	{
		get { return _LinesOfCode.Count; }
	}

	public ProgrammableChipException.ICExceptionType HarnessErrorType
	{
		get { return _ErrorType; }
	}

	public ushort HarnessErrorLine
	{
		get { return _ErrorLineNumber; }
	}

	public ProgrammableChipException.ICExceptionType HarnessCompileErrorType
	{
		get { return CompileErrorType; }
	}

	public ushort HarnessCompileErrorLine
	{
		get { return CompileErrorLineNumber; }
	}

	// Injected: why the last Execute stopped. True only when the tick ran out
	// of its instruction budget with the program still inside itself; every
	// other ending leaves it false. Nothing else on this class can tell a spent
	// budget from a yield — both leave the same address, error state and line count.
	public bool HarnessBudgetExhausted;`

func editDecl(body scope, d decl, s *slicing) (string, error) {
	d.text = s.strip(d.text)
	switch d.name {
	case "public void SetSourceCode(string sourceCode)":
		return editSetSourceCode(d)
	case "public void Execute(int runCount)":
		return editExecute(d)
	case "_RAND_Operation":
		return editRandOperation(d)
	case "_Operation":
		return dropMembers(d, setDeviceValueSignature)
	case "ScriptEnum", "BasicEnum":
		return dropHelpRendering(d)
	case "static ProgrammableChip()":
		return editStaticConstructor(d)
	case "_LineOfCode":
		return dropParseArm(d, hcfCommand)
	case circuitHousingSignature:
		// The declaration is kept only so that its absence from a future
		// decompile is noticed; injectedChipState declares the field that
		// replaces it.
		return "", nil
	}
	if strings.Contains(d.text, networkFlagRef) {
		return replaceExactly(d.text, networkFlagRef, networkFlagField,
			strings.Count(d.text, networkFlagRef), body.file.rel+": "+d.name)
	}
	return d.text, nil
}

const (
	// setDeviceValueSignature names the one member of _Operation that reaches
	// UnityEngine. It compares a Device against null through the Unity object
	// lifetime operator, which is the last thing in the chip that needs
	// UnityEngine.Object, and nothing calls it.
	setDeviceValueSignature = "protected void _SetDeviceValue(Device device, LogicType logicType, double value)"

	// circuitHousingSignature is the property that walked the inventory graph
	// to the housing.
	circuitHousingSignature = "private ICircuitHolder CircuitHousing"

	// networkFlagRef is the base-qualified flag the four error properties set.
	// Redirecting it to a field of the same name is the only token that
	// changes in those four bodies.
	networkFlagRef   = "base.NetworkUpdateFlags"
	networkFlagField = "NetworkUpdateFlags"

	// parentSlotRefresh is the last statement of SetSourceCode. It tells the
	// inventory slot the item's quantity display is stale, and there is no
	// slot here.
	parentSlotRefresh = "\n\t\tbase.ParentSlot?.RefreshQuantity();"

	// hcfCommand is the parse arm that constructs the one dropped operation.
	hcfCommand = "hcf"
)

func editSetSourceCode(d decl) (string, error) {
	return cutOnce(d.text, parentSlotRefresh, "SetSourceCode: drop the parent-slot refresh")
}

const (
	// executeGuard is the test Execute refuses a run on, and the anchor the
	// injected flag is cleared ahead of.
	executeGuard = "if (_NextAddr < 0 || _NextAddr >= _LinesOfCode.Count || _LinesOfCode.Count == 0)"

	// executeExit is how each of the tick loop's three early endings leaves it,
	// and executeReturn is what they leave it as instead. Since the loop is the
	// method's last statement, this makes the statement after it run only when
	// the loop ended by evaluating its condition — separating a spent budget from every other ending.
	executeExit   = "break;"
	executeReturn = "return;"

	// executeExits is how many of those endings there are.
	executeExits = 3

	// executeLoop opens the tick loop; the injected statement goes after its
	// closing brace. The loop must be the method's last statement for that to
	// mean anything, or a game statement placed after it would run on every ending instead.
	executeLoop = "while (num-- > 0 && _NextAddr >= 0 && _NextAddr < _LinesOfCode.Count)"

	// executeRecord decides the injected flag, reached only when the loop's own
	// condition fails: either the budget ran out with the program still inside
	// itself, or the counter left the program on the instruction just run.
	executeRecord = "HarnessBudgetExhausted = _NextAddr >= 0 && _NextAddr < _LinesOfCode.Count;"
)

// editExecute makes the tick loop record why it ended. See
// HarnessBudgetExhausted in injectedChipState for what the game destroys.
func editExecute(d decl) (string, error) {
	text, err := replaceExactly(d.text, executeGuard, "HarnessBudgetExhausted = false;\n\t\t"+executeGuard,
		1, "Execute(int runCount): the guard")
	if err != nil {
		return "", err
	}
	text, err = replaceExactly(text, executeExit, executeReturn, executeExits,
		"Execute(int runCount): the tick loop's early endings")
	if err != nil {
		return "", err
	}
	start := strings.Index(text, executeLoop)
	if start < 0 {
		return "", fmt.Errorf("Execute(int runCount): tick loop %q: %w", executeLoop, errNotFound)
	}
	_, end, err := matchDelim(text[start:], 0, '{', '}')
	if err != nil {
		return "", fmt.Errorf("Execute(int runCount): tick loop body: %w", err)
	}
	at := start + end
	if rest := strings.TrimSpace(text[at:]); rest != "}" {
		return "", fmt.Errorf("Execute(int runCount): the tick loop is followed by %.60q rather than "+
			"closing the method, so the injected record would not name a spent budget", rest)
	}
	return text[:at] + "\n\t\t" + executeRecord + text[at:], nil
}

const (
	// randSource is the process-global generator _RAND_Operation draws from,
	// built by randSourceCtor with no seed. Both are dropped: a sequence
	// nothing can arm is one two runs of a program would not share.
	randSource     = "private static readonly Random _RandomNumberGenerator"
	randSourceCtor = "static _RAND_Operation()"

	// randDraw is the one call the operation body makes, redirected onto the
	// source the harness arms. Nothing else about the body changes: what a chip
	// can observe of a draw is the double it lands in a register.
	randDraw     = "_RandomNumberGenerator.NextDouble()"
	randRedirect = "HarnessRandom.NextDouble()"
)

func editRandOperation(d decl) (string, error) {
	text, err := dropMembers(d, randSource, randSourceCtor)
	if err != nil {
		return "", err
	}
	return replaceExactly(text, randDraw, randRedirect, 1, "_RAND_Operation: the draw")
}

// dropMembers removes members from a container declaration by signature.
// Every signature must name exactly one member. Removals are made
// back-to-front so none of them shift another, letting a caller name them in whatever order reads best.
func dropMembers(d decl, signatures ...string) (string, error) {
	body := scope{body: d.body}
	spans := make([][2]int, 0, len(signatures))
	for _, signature := range signatures {
		m, err := body.member(signature)
		if err != nil {
			return "", fmt.Errorf("%s: %w", d.name, err)
		}
		spans = append(spans, [2]int{m.start, m.end})
	}
	slices.SortFunc(spans, func(a, b [2]int) int { return b[0] - a[0] })

	offset := d.bodyStart - d.start
	text := d.text
	previous := len(d.body)
	for _, span := range spans {
		if span[1] > previous {
			return "", fmt.Errorf("%s: two of the signatures name overlapping declarations", d.name)
		}
		previous = span[0]
		text = text[:offset+span[0]] + text[offset+span[1]:]
	}
	return trimBlankRun(text), nil
}

// dropHelpRendering removes the two members of the enum wrappers that
// render a Stationpedia page and colourize a help string. Both reach
// UnityEngine, and neither is on Execute's path, which is kept as-is.
func dropHelpRendering(d decl) (string, error) {
	decls, err := splitDecls(d.body)
	if err != nil {
		return "", fmt.Errorf("%s: %w", d.name, err)
	}
	var spans [][2]int
	for _, m := range decls {
		if strings.HasPrefix(m.name, "public HelpReference MakePage(") || m.name == "public void Parse(ref string masterString)" {
			spans = append(spans, [2]int{m.start, m.end})
		}
	}
	if len(spans) != 2 {
		return "", fmt.Errorf("%s: expected a MakePage and a Parse to drop, found %d: %w", d.name, len(spans), errNotFound)
	}
	offset := d.bodyStart - d.start
	text := d.text
	for i := len(spans) - 1; i >= 0; i-- {
		text = text[:offset+spans[i][0]] + text[offset+spans[i][1]:]
	}
	return trimBlankRun(text), nil
}

const (
	// exceptionNamesStatement builds a display-name table for the exception
	// enum out of a game collection type. Only the tooltip text reads it.
	exceptionNamesStatement = "_exceptionTypes = "

	// internalEnumsStatement builds the roster of enums an operand may name.
	// The game's roster covers three dozen enums from all over the assembly;
	// the unit lifts four, so the roster is rewritten to those.
	internalEnumsStatement = "InternalEnums = new List<IScriptEnum>"
)

// internalEnumsRoster is what replaces the roster. The two delegates the
// game passes are a deprecation predicate and a description lookup, from a
// type this unit does not lift; neither is read by Execute, so both are null.
const internalEnumsRoster = `InternalEnums = new List<IScriptEnum>
		{
			new ScriptEnum<LogicType>(InstructionInclude.LogicType, null),
			new ScriptEnum<LogicSlotType>(InstructionInclude.LogicSlotType, null),
			new ScriptEnum<LogicReagentMode>(InstructionInclude.LogicReagentMode, null),
			new ScriptEnum<LogicBatchMethod>(InstructionInclude.LogicBatchMethod, null),
			new BasicEnum<LogicType>("LogicType", null),
			new BasicEnum<LogicSlotType>("LogicSlotType", null),
			new BasicEnum<Slot.Class>("SlotClass", null)
		}`

func editStaticConstructor(d decl) (string, error) {
	text, err := cutStatement(d.text, exceptionNamesStatement)
	if err != nil {
		return "", fmt.Errorf("static ProgrammableChip(): %w", err)
	}
	start := strings.Index(text, internalEnumsStatement)
	if start < 0 {
		return "", fmt.Errorf("static ProgrammableChip(): statement %q: %w", internalEnumsStatement, errNotFound)
	}
	_, end, err := matchDelim(text[start:], 0, '{', '}')
	if err != nil {
		return "", fmt.Errorf("static ProgrammableChip(): enum roster initializer: %w", err)
	}
	return text[:start] + internalEnumsRoster + text[start+end:], nil
}

// cutStatement removes the statement beginning with prefix, up to and including
// the semicolon that closes it at its own nesting depth.
func cutStatement(src, prefix string) (string, error) {
	start := strings.Index(src, prefix)
	if start < 0 {
		return "", fmt.Errorf("statement %q: %w", prefix, errNotFound)
	}
	if strings.Contains(src[start+len(prefix):], prefix) {
		return "", fmt.Errorf("statement %q appears more than once", prefix)
	}
	depth := 0
	for i := start; i < len(src); i++ {
		if j := skipLiteral(src, i); j != i {
			i = j - 1
			continue
		}
		switch src[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ';':
			if depth == 0 {
				return trimBlankRun(src[:lineStart(src, start)] + src[lineEnd(src, i):]), nil
			}
		}
	}
	return "", fmt.Errorf("statement %q is unterminated", prefix)
}

// dropParseArm removes one switch arm from the tokenizer's ScriptCommand
// switch, which is what keeps the parser from constructing an operation the
// unit does not define.
func dropParseArm(d decl, command string) (string, error) {
	label := "case ScriptCommand." + command + ":"
	start := strings.Index(d.text, label)
	if start < 0 {
		return "", fmt.Errorf("%s: parse arm %q: %w", d.name, label, errNotFound)
	}
	if strings.Contains(d.text[start+len(label):], label) {
		return "", fmt.Errorf("%s: parse arm %q appears more than once", d.name, label)
	}
	// The arm runs to the next label written at the same indentation. Reading
	// the indentation off the label rather than assuming it is what keeps this
	// working when the switch moves to a different nesting depth.
	lineAt := lineStart(d.text, start)
	indentation := d.text[lineAt:start]
	if strings.TrimLeft(indentation, " \t") != "" {
		return "", fmt.Errorf("%s: parse arm %q does not start its line", d.name, label)
	}
	rest := d.text[start+len(label):]
	next := -1
	for _, marker := range []string{"\n" + indentation + "case ", "\n" + indentation + "default:"} {
		if at := strings.Index(rest, marker); at >= 0 && (next < 0 || at < next) {
			next = at
		}
	}
	if next < 0 {
		return "", fmt.Errorf("%s: parse arm %q is not followed by another arm: %w", d.name, label, errNotFound)
	}
	return d.text[:lineAt] + d.text[start+len(label)+next+1:], nil
}

// lineStart returns the offset of the start of the line containing off,
// including its leading indentation, so a removed declaration takes its own
// indentation with it.
func lineStart(src string, off int) int {
	if nl := strings.LastIndexByte(src[:off], '\n'); nl >= 0 {
		return nl + 1
	}
	return 0
}

// lineEnd returns the offset just past the newline that ends the line
// containing off, so that a removed statement takes its line break with it
// rather than leaving a blank line where it stood.
func lineEnd(src string, off int) int {
	if nl := strings.IndexByte(src[off:], '\n'); nl >= 0 {
		return off + nl + 1
	}
	return len(src)
}

// trimBlankRun collapses runs of blank lines left behind by a removal, so the
// emitted unit reads the way the decompiler wrote it.
func trimBlankRun(text string) string {
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	return text
}
