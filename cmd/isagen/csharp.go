package main

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// errNotFound reports that a construct the extractor depends on is absent from
// the decompiled source. Callers wrap it with what they were looking for.
var errNotFound = errors.New("not found")

// skipLiteral advances past the C# lexical element starting at i when that
// element is a comment or a string, character, verbatim, or interpolated
// literal. It reports the index just past the element, or i itself when the
// element at i is ordinary code. Brace and paren matching relies on this so
// punctuation inside literals never affects nesting depth.
func skipLiteral(src string, i int) int {
	if i >= len(src) {
		return i
	}
	switch {
	case strings.HasPrefix(src[i:], "//"):
		if end := strings.IndexByte(src[i:], '\n'); end >= 0 {
			return i + end
		}
		return len(src)
	case strings.HasPrefix(src[i:], "/*"):
		if end := strings.Index(src[i+2:], "*/"); end >= 0 {
			return i + 2 + end + 2
		}
		return len(src)
	case strings.HasPrefix(src[i:], `@"`), strings.HasPrefix(src[i:], `$@"`), strings.HasPrefix(src[i:], `@$"`):
		return skipVerbatim(src, i+strings.IndexByte(src[i:], '"')+1)
	case src[i] == '"', strings.HasPrefix(src[i:], `$"`):
		start := i
		if src[i] == '$' {
			start = i + 1
		}
		return skipQuoted(src, start+1, '"')
	case src[i] == '\'':
		return skipQuoted(src, i+1, '\'')
	}
	return i
}

// skipQuoted scans a backslash-escaped literal whose body starts at i and
// returns the index just past its closing quote.
func skipQuoted(src string, i int, quote byte) int {
	for ; i < len(src); i++ {
		switch src[i] {
		case '\\':
			i++
		case quote:
			return i + 1
		}
	}
	return len(src)
}

// skipVerbatim scans a verbatim string whose body starts at i, where a doubled
// quote is an escaped quote, and returns the index just past its terminator.
func skipVerbatim(src string, i int) int {
	for ; i < len(src); i++ {
		if src[i] != '"' {
			continue
		}
		if i+1 < len(src) && src[i+1] == '"' {
			i++
			continue
		}
		return i + 1
	}
	return len(src)
}

// matchDelim finds the delimiter pair opened by the open byte at or after
// start and returns the text between the delimiters and the index just past
// the closer.
func matchDelim(src string, start int, open, closing byte) (body string, end int, err error) {
	i := start
	for i < len(src) && src[i] != open {
		if next := skipLiteral(src, i); next != i {
			i = next
			continue
		}
		i++
	}
	if i >= len(src) {
		return "", 0, fmt.Errorf("opening %q: %w", string(open), errNotFound)
	}
	bodyStart := i + 1
	depth := 0
	for i < len(src) {
		if next := skipLiteral(src, i); next != i {
			i = next
			continue
		}
		switch src[i] {
		case open:
			depth++
		case closing:
			depth--
			if depth == 0 {
				return src[bodyStart:i], i + 1, nil
			}
		}
		i++
	}
	return "", 0, fmt.Errorf("closing %q for delimiter opened at offset %d: %w", string(closing), bodyStart-1, errNotFound)
}

// braceBlockAt returns the body of the brace block src opens with, ignoring
// only the layout whitespace before it.
//
// Requiring the block to be the very next thing is what keeps a declaration
// written without one from being read as the block belonging to whatever
// follows it.
func braceBlockAt(src string) (string, error) {
	rest := strings.TrimLeft(src, " \t\r\n")
	if !strings.HasPrefix(rest, "{") {
		return "", fmt.Errorf(`opening "{": %w`, errNotFound)
	}
	body, _, err := matchDelim(rest, 0, '{', '}')
	return body, err
}

// splitTop splits s on the sep byte, ignoring separators nested inside
// brackets or literals. Empty trailing elements from a trailing separator are
// dropped, matching how C# initializer lists are written.
func splitTop(s string, sep byte) []string {
	var parts []string
	emit := func(part string) {
		if strings.TrimSpace(part) != "" {
			parts = append(parts, part)
		}
	}
	depth := 0
	start := 0
	for i := 0; i < len(s); {
		if next := skipLiteral(s, i); next != i {
			i = next
			continue
		}
		switch s[i] {
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}', '>':
			depth--
		case sep:
			if depth == 0 {
				emit(s[start:i])
				start = i + 1
			}
		}
		i++
	}
	emit(s[start:])
	return parts
}

// findIdent locates the first occurrence of ident in src that is not part of a
// longer identifier and not inside a literal.
func findIdent(src, ident string) int {
	for i := 0; i+len(ident) <= len(src); {
		if next := skipLiteral(src, i); next != i {
			i = next
			continue
		}
		if strings.HasPrefix(src[i:], ident) && !isIdentByte(byteAt(src, i-1)) && !isIdentByte(byteAt(src, i+len(ident))) {
			return i
		}
		i++
	}
	return -1
}

func byteAt(s string, i int) byte {
	if i < 0 || i >= len(s) {
		return 0
	}
	return s[i]
}

func isIdentByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// The integer suffix is matched and dropped: C# writes the value of an enum
// with an unsigned or long underlying type as `0x10u`, and the suffix says
// nothing the member's own type does not.
var enumEntryRE = regexp.MustCompile(`^([A-Za-z_]\w*)(?:\s*=\s*(-?(?:0[xX][0-9a-fA-F]+|\d+))[uUlL]*)?$`)

// stripAttributes drops the attribute sections that precede a declaration, so
// the enum member behind them can be matched. Members of the enums the game
// serializes carry an XmlEnum attribute each.
func stripAttributes(entry string) (string, error) {
	entry = strings.TrimSpace(entry)
	for strings.HasPrefix(entry, "[") {
		_, end, err := matchDelim(entry, 0, '[', ']')
		if err != nil {
			return "", fmt.Errorf("attribute on %q: %w", entry, err)
		}
		entry = strings.TrimSpace(entry[end:])
	}
	return entry, nil
}

// parseEnum recovers the members of the named C# enum from decompiled source,
// resolving implicit values the same way the C# compiler does. Members are
// returned in declaration order.
func parseEnum(src, name string) ([]EnumMember, error) {
	start := findIdent(src, "enum")
	for start >= 0 {
		rest := strings.TrimLeft(src[start+len("enum"):], " \t")
		if strings.HasPrefix(rest, name) && !isIdentByte(byteAt(rest, len(name))) {
			break
		}
		next := findIdent(src[start+1:], "enum")
		if next < 0 {
			start = -1
			break
		}
		start += 1 + next
	}
	if start < 0 {
		return nil, fmt.Errorf("enum %s: %w", name, errNotFound)
	}

	body, _, err := matchDelim(src, start, '{', '}')
	if err != nil {
		return nil, fmt.Errorf("enum %s body: %w", name, err)
	}

	var members []EnumMember
	next := int64(0)
	for _, entry := range splitTop(body, ',') {
		text, err := stripAttributes(entry)
		if err != nil {
			return nil, fmt.Errorf("enum %s: %w", name, err)
		}
		m := enumEntryRE.FindStringSubmatch(text)
		if m == nil {
			return nil, fmt.Errorf("enum %s: unrecognized member %q", name, text)
		}
		value := next
		if m[2] != "" {
			value, err = strconv.ParseInt(m[2], 0, 64)
			if err != nil {
				return nil, fmt.Errorf("enum %s member %s: parse value: %w", name, m[1], err)
			}
		}
		members = append(members, EnumMember{Name: m[1], Value: value})
		next = value + 1
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("enum %s: no members", name)
	}
	return members, nil
}

// parseListInitializer recovers the members named by a
// `List<elemType> field = new List<elemType> { elemType.A, ... }` declaration.
// An empty list, which decompiles without a brace block, yields no members and
// no error.
func parseListInitializer(src, field, elemType string) ([]string, error) {
	decl := fmt.Sprintf("%s = new List<%s>", field, elemType)
	_, after, ok := strings.Cut(src, decl)
	if !ok {
		return nil, fmt.Errorf("initializer for %s: %w", field, errNotFound)
	}
	rest := strings.TrimLeft(after, " \t\r\n")
	if !strings.HasPrefix(rest, "{") {
		return nil, nil
	}
	body, _, err := matchDelim(rest, 0, '{', '}')
	if err != nil {
		return nil, fmt.Errorf("initializer for %s: %w", field, err)
	}

	prefix := elemType + "."
	var members []string
	for _, entry := range splitTop(body, ',') {
		entry = strings.TrimSpace(entry)
		if !strings.HasPrefix(entry, prefix) {
			return nil, fmt.Errorf("initializer for %s: unrecognized entry %q", field, entry)
		}
		members = append(members, strings.TrimPrefix(entry, prefix))
	}
	return members, nil
}

var constructedEntryRE = regexp.MustCompile(`^new\s+([A-Za-z_]\w*)\s*\(`)

// parseConstructedList recovers the type names constructed by a
// `field = new List<elemType> { new A(...), new B(...) }` declaration, in
// source order.
//
// It is the sibling of parseListInitializer for a table the game writes as one
// class per entry rather than as enum members. An entry that is not a
// constructor call is an error: the alternative is a table silently missing
// whatever the game added.
func parseConstructedList(src, field, elemType string) ([]string, error) {
	decl := fmt.Sprintf("%s = new List<%s>", field, elemType)
	_, after, ok := strings.Cut(src, decl)
	if !ok {
		return nil, fmt.Errorf("initializer for %s: %w", field, errNotFound)
	}
	body, err := braceBlockAt(after)
	if err != nil {
		return nil, fmt.Errorf("initializer for %s: %w", field, err)
	}

	var types []string
	for _, entry := range splitTop(body, ',') {
		m := constructedEntryRE.FindStringSubmatch(strings.TrimSpace(entry))
		if m == nil {
			return nil, fmt.Errorf("initializer for %s: unrecognized entry %q", field, strings.TrimSpace(entry))
		}
		types = append(types, m[1])
	}
	if len(types) == 0 {
		return nil, fmt.Errorf("initializer for %s: no entries", field)
	}
	return types, nil
}

var switchArmRE = regexp.MustCompile(`(-?\d+)\s*=>\s*new\s+([A-Za-z_]\w*)\s*\(`)

// parseConstructorSwitch recovers the arms of a
// `subject switch { 0 => new A(x), 1 => new B(x), _ => null }` expression,
// mapping each integer label to the type it constructs. The discard arm names
// no label and is skipped.
//
// The arms are matched rather than split apart, because the `=>` of an arm
// carries a bracket the top level splitter counts as nesting. That laxness is
// answered by the caller, which holds the recovered labels to a dense range and
// to a second declaration of the same table.
func parseConstructorSwitch(src, subject string) (map[int64]string, error) {
	_, after, ok := strings.Cut(src, subject+" switch")
	if !ok {
		return nil, fmt.Errorf("switch on %s: %w", subject, errNotFound)
	}
	body, err := braceBlockAt(after)
	if err != nil {
		return nil, fmt.Errorf("switch on %s: %w", subject, err)
	}

	arms := make(map[int64]string)
	for _, m := range switchArmRE.FindAllStringSubmatch(body, -1) {
		label, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("switch on %s: parse label %q: %w", subject, m[1], err)
		}
		if previous, ok := arms[label]; ok {
			return nil, fmt.Errorf("switch on %s: label %d constructs both %s and %s", subject, label, previous, m[2])
		}
		arms[label] = m[2]
	}
	if len(arms) == 0 {
		return nil, fmt.Errorf("switch on %s: no arms", subject)
	}
	return arms, nil
}

// internalEnum is one entry of ProgrammableChip.InternalEnums, the list the
// chip's assembler walks to resolve a name an operand mentions.
type internalEnum struct {
	// basic marks a BasicEnum, whose members are named `Prefix.Member`
	// wherever an operand accepts an enum, as against a ScriptEnum, whose
	// members are named bare under one operand flag.
	basic bool
	// typeName is the C# enum as the game source spells it, so a nested type
	// reads `Outer.Nested` and carries no namespace.
	typeName string
	// prefix is what a BasicEnum member must be qualified with, empty for the
	// entry registered without one.
	prefix string
	// deprecates reports that the entry was constructed with a deprecation
	// predicate, which a plain member table cannot express.
	deprecates bool
}

var internalEnumRE = regexp.MustCompile(`^new (ScriptEnum|BasicEnum)<([\w.]+)>\s*\(`)

// parseInternalEnums recovers ProgrammableChip.InternalEnums in declaration
// order. Order is part of the data: the assembler stops at the first entry
// holding a name, so two enums sharing a member name resolve by position.
func parseInternalEnums(src string) ([]internalEnum, error) {
	const field = "InternalEnums"
	_, after, ok := strings.Cut(src, field+" = new List<IScriptEnum>")
	if !ok {
		return nil, fmt.Errorf("initializer for %s: %w", field, errNotFound)
	}
	body, _, err := matchDelim(after, 0, '{', '}')
	if err != nil {
		return nil, fmt.Errorf("initializer for %s: %w", field, err)
	}

	var entries []internalEnum
	for _, entry := range splitTop(body, ',') {
		entry = strings.TrimSpace(entry)
		m := internalEnumRE.FindStringSubmatch(entry)
		if m == nil {
			return nil, fmt.Errorf("%s: unrecognized entry %q", field, entry)
		}
		args, _, err := matchDelim(entry, 0, '(', ')')
		if err != nil {
			return nil, fmt.Errorf("%s entry %q: %w", field, entry, err)
		}
		fields := splitTop(args, ',')
		parsed := internalEnum{basic: m[1] == "BasicEnum", typeName: m[2], deprecates: len(fields) > 1}
		if parsed.basic && len(fields) > 0 {
			prefix, err := stringLiteral(fields[0])
			if err != nil {
				return nil, fmt.Errorf("%s entry %q: %w", field, entry, err)
			}
			parsed.prefix = prefix
		}
		entries = append(entries, parsed)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s: no entries", field)
	}
	return entries, nil
}

var helpStringRE = regexp.MustCompile(`(?m)^\s*([A-Z_][A-Z0-9_]*) = new HelpString\(`)

// parseHelpStrings recovers the HelpString symbol table from the static
// constructor of ProgrammableChip, mapping each symbol to the bare token it
// renders. The two-argument form is (token, color); the three-argument form is
// (name, token, color).
func parseHelpStrings(src string) (map[string]string, error) {
	tokens := make(map[string]string)
	for _, loc := range helpStringRE.FindAllStringSubmatchIndex(src, -1) {
		symbol := src[loc[2]:loc[3]]
		args, _, err := matchDelim(src, loc[1]-1, '(', ')')
		if err != nil {
			return nil, fmt.Errorf("HelpString %s arguments: %w", symbol, err)
		}
		fields := splitTop(args, ',')
		var raw string
		switch len(fields) {
		case 2:
			raw = fields[0]
		case 3:
			raw = fields[1]
		default:
			return nil, fmt.Errorf("HelpString %s: unexpected argument count %d", symbol, len(fields))
		}
		token, err := stringLiteral(raw)
		if err != nil {
			return nil, fmt.Errorf("HelpString %s: %w", symbol, err)
		}
		if prev, ok := tokens[symbol]; ok && prev != token {
			return nil, fmt.Errorf("HelpString %s: conflicting tokens %q and %q", symbol, prev, token)
		}
		tokens[symbol] = token
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("HelpString symbol table: %w", errNotFound)
	}
	return tokens, nil
}

// These three anchor at the start of the text they are applied to, so the scan
// over the switch body can test each position without rescanning the tail.
var (
	commandExampleRE = regexp.MustCompile(`\bstring GetCommandExample\s*\(`)
	commandCaseRE    = regexp.MustCompile(`^\s*case ScriptCommand\.(\w+):`)
	makeStringRE     = regexp.MustCompile(`^\s*return MakeString\s*\(`)
	operandVarRE     = regexp.MustCompile(`^\((.+)\)\.Var\("(\w+)"\)$`)
)

// parseCommandExamples recovers the operand signature of every ScriptCommand
// from ProgrammableChip.GetCommandExample. The switch groups mnemonics that
// share a signature under consecutive case labels, so labels accumulate until
// the MakeString call that serves them.
func parseCommandExamples(src string, tokens map[string]string) (map[string][]Operand, error) {
	loc := commandExampleRE.FindStringIndex(src)
	if loc == nil {
		return nil, fmt.Errorf("GetCommandExample: %w", errNotFound)
	}
	_, sigEnd, err := matchDelim(src, loc[1]-1, '(', ')')
	if err != nil {
		return nil, fmt.Errorf("GetCommandExample signature: %w", err)
	}
	body, _, err := matchDelim(src, sigEnd, '{', '}')
	if err != nil {
		return nil, fmt.Errorf("GetCommandExample body: %w", err)
	}

	signatures := make(map[string][]Operand)
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
		if m := makeStringRE.FindStringIndex(body[i:]); m != nil && m[0] == 0 {
			args, end, err := matchDelim(body, i, '(', ')')
			if err != nil {
				return nil, fmt.Errorf("MakeString arguments: %w", err)
			}
			if len(pending) == 0 {
				return nil, fmt.Errorf("MakeString call at offset %d has no preceding case label", i)
			}
			operands, err := parseOperands(args, tokens)
			if err != nil {
				return nil, fmt.Errorf("operands for %s: %w", strings.Join(pending, ", "), err)
			}
			for _, mnemonic := range pending {
				if _, dup := signatures[mnemonic]; dup {
					return nil, fmt.Errorf("duplicate case for ScriptCommand.%s", mnemonic)
				}
				signatures[mnemonic] = operands
			}
			pending = nil
			i = end
			continue
		}
		i++
	}
	if len(pending) != 0 {
		return nil, fmt.Errorf("case labels %s have no MakeString call", strings.Join(pending, ", "))
	}
	return signatures, nil
}

// makeStringFixedArgs counts the command, color, and spaceCount parameters that
// precede the operand descriptions in every MakeString call.
const makeStringFixedArgs = 3

// parseOperands turns a MakeString argument list into operand descriptions.
func parseOperands(args string, tokens map[string]string) ([]Operand, error) {
	fields := splitTop(args, ',')
	if len(fields) < makeStringFixedArgs {
		return nil, fmt.Errorf("expected at least %d arguments, got %d", makeStringFixedArgs, len(fields))
	}

	var operands []Operand
	for _, field := range fields[makeStringFixedArgs:] {
		expr := strings.TrimSpace(field)
		name := ""
		if m := operandVarRE.FindStringSubmatch(expr); m != nil {
			expr, name = m[1], m[2]
		}
		var kinds []string
		for _, symbol := range splitTop(expr, '+') {
			symbol = strings.TrimSpace(symbol)
			token, ok := tokens[symbol]
			if !ok {
				return nil, fmt.Errorf("unknown HelpString symbol %q", symbol)
			}
			kind, ok := helpTokenKinds[token]
			if !ok {
				return nil, fmt.Errorf("HelpString symbol %s renders unknown token %q", symbol, token)
			}
			kinds = append(kinds, kind)
		}
		if len(kinds) == 0 {
			return nil, fmt.Errorf("operand expression %q names no kinds", field)
		}
		operands = append(operands, Operand{Name: name, Kinds: kinds})
	}
	return operands, nil
}

// renderExample reproduces the game's help text for one instruction with the
// colour markup stripped, for example `add r? a(r?|num) b(r?|num)`.
func renderExample(mnemonic string, operands []Operand) (string, error) {
	var b strings.Builder
	b.WriteString(mnemonic)
	for _, operand := range operands {
		parts := make([]string, len(operand.Kinds))
		for i, kind := range operand.Kinds {
			token, ok := kindTokens[kind]
			if !ok {
				return "", fmt.Errorf("%s: unknown operand kind %q", mnemonic, kind)
			}
			parts[i] = token
		}
		b.WriteByte(' ')
		joined := strings.Join(parts, "|")
		if operand.Name == "" {
			b.WriteString(joined)
			continue
		}
		b.WriteString(operand.Name)
		b.WriteByte('(')
		b.WriteString(joined)
		b.WriteByte(')')
	}
	return b.String(), nil
}

var constantArrayRE = regexp.MustCompile(`\bAllConstants = new Constant\[(\d+)\]`)

// csharpDoubles maps the named double expressions the constant table uses to
// their Go equivalents. Values the game writes as decimal literals are parsed
// instead; anything else is rejected so a new spelling cannot be folded wrong.
var csharpDoubles = map[string]float64{
	"double.NaN":              dotnetNaN,
	"double.PositiveInfinity": math.Inf(1),
	"double.NegativeInfinity": math.Inf(-1),
	"double.Epsilon":          math.SmallestNonzeroFloat64,
	"Math.PI":                 math.Pi,
	"Math.PI * 2.0":           mathPiTimesTwo,
}

// dotnetNaN is double.NaN's bit pattern, which is the x86 default quiet NaN and
// not the one Go's math.NaN produces. The payload is observable: `mod`
// propagates an operand's own pattern.
var dotnetNaN = math.Float64frombits(0xfff8000000000000)

// mathPiTimesTwo is evaluated as a float64 multiply so it matches the game's
// runtime result bit for bit rather than Go's higher-precision constant
// arithmetic.
var mathPiTimesTwo = func() float64 {
	pi := math.Pi
	return pi * 2.0
}()

// parseConstants recovers ProgrammableChip.AllConstants. The declared array
// length is checked against the number of initializers so a partially matched
// block is an error rather than a short table.
func parseConstants(src string) ([]Constant, error) {
	loc := constantArrayRE.FindStringSubmatchIndex(src)
	if loc == nil {
		return nil, fmt.Errorf("AllConstants: %w", errNotFound)
	}
	declared, err := strconv.Atoi(src[loc[2]:loc[3]])
	if err != nil {
		return nil, fmt.Errorf("AllConstants length: %w", err)
	}
	body, _, err := matchDelim(src, loc[1], '{', '}')
	if err != nil {
		return nil, fmt.Errorf("AllConstants body: %w", err)
	}

	entries := splitTop(body, ',')
	if len(entries) != declared {
		return nil, fmt.Errorf("AllConstants declares %d entries but the initializer has %d", declared, len(entries))
	}

	constants := make([]Constant, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if !strings.HasPrefix(entry, "new Constant(") {
			return nil, fmt.Errorf("AllConstants: unrecognized entry %q", entry)
		}
		args, _, err := matchDelim(entry, 0, '(', ')')
		if err != nil {
			return nil, fmt.Errorf("AllConstants entry %q: %w", entry, err)
		}
		fields := splitTop(args, ',')
		if len(fields) < 3 {
			return nil, fmt.Errorf("AllConstants entry %q: expected at least 3 arguments, got %d", entry, len(fields))
		}
		name, err := stringLiteral(fields[0])
		if err != nil {
			return nil, fmt.Errorf("AllConstants entry %q: %w", entry, err)
		}
		value, err := csharpDouble(strings.TrimSpace(fields[2]))
		if err != nil {
			return nil, fmt.Errorf("constant %s: %w", name, err)
		}
		constants = append(constants, Constant{Name: name, Value: formatFloat(value)})
	}
	return constants, nil
}

// csharpDouble evaluates the closed set of double expressions the game's
// constant table uses.
func csharpDouble(expr string) (float64, error) {
	if v, ok := csharpDoubles[expr]; ok {
		return v, nil
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSuffix(expr, "d"), "D"), 64)
	if err != nil {
		return 0, fmt.Errorf("unsupported double expression %q: %w", expr, err)
	}
	return v, nil
}

// stringLiteral unquotes a C# string literal, which for the constructs the
// extractor reads is always a plain double-quoted literal.
func stringLiteral(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", fmt.Errorf("expected a string literal, got %q", s)
	}
	unquoted, err := strconv.Unquote(s)
	if err != nil {
		return "", fmt.Errorf("unquote %q: %w", s, err)
	}
	return unquoted, nil
}
