package main

import (
	"fmt"
	"maps"
	"regexp"
	"strconv"
	"strings"

	"github.com/greg2010/ic11c/internal/source"
)

// tri is a three-valued answer to "can this device do this". triMaybe is not
// a shrug: the game decides some properties from live state (what a logic
// transmitter is pointed at, whether a pipe connection is made), which no
// static reading of the assembly can settle.
type tri uint8

const (
	triNo tri = iota
	triYes
	triMaybe
)

func triOf(b bool) tri {
	if b {
		return triYes
	}
	return triNo
}

func (t tri) not() tri {
	switch t {
	case triNo:
		return triYes
	case triYes:
		return triNo
	case triMaybe:
		return triMaybe
	}
	return triMaybe
}

// and follows C#'s short-circuit: one false operand settles the result whatever
// the others are.
func and(a, b tri) tri {
	if a == triNo || b == triNo {
		return triNo
	}
	if a == triYes && b == triYes {
		return triYes
	}
	return triMaybe
}

func or(a, b tri) tri {
	if a == triYes || b == triYes {
		return triYes
	}
	if a == triNo && b == triNo {
		return triNo
	}
	return triMaybe
}

// merge is what an untaken branch costs. When a condition cannot be decided,
// both arms are evaluated and the result stands only where they agree.
func merge(a, b tri) tri {
	if a == b {
		return a
	}
	return triMaybe
}

// flow is how a statement list ended, which decides what runs after it. A
// break is distinct from running off the end: the two continue in different
// places, a break leaving the enclosing switch. Conflating them would answer
// `case X: if (!HasFoo) break; return true;` with the return unconditionally.
type flow uint8

const (
	flowFell flow = iota
	flowAnswered
	flowBroke
)

// flowNames read the way a diagnostic about a body does.
var flowNames = [...]string{
	flowFell:     "runs past its end",
	flowAnswered: "answers",
	flowBroke:    "leaves the switch",
}

func (f flow) String() string { return source.EnumName(flowNames[:], int(f), "flow") }

// surfaceKind names one of the four methods a device's logic surface is
// declared by.
type surfaceKind uint8

const (
	kindReadLogic surfaceKind = iota
	kindWriteLogic
	kindReadSlot
	kindWriteSlot
)

func (k surfaceKind) slotForm() bool { return k == kindReadSlot || k == kindWriteSlot }

var surfaceKindNames = [...]string{
	kindReadLogic:  "CanLogicRead(LogicType)",
	kindWriteLogic: "CanLogicWrite(LogicType)",
	kindReadSlot:   "CanLogicRead(LogicSlotType, int)",
	kindWriteSlot:  "CanLogicWrite(LogicSlotType, int)",
}

func (k surfaceKind) String() string {
	return source.EnumName(surfaceKindNames[:], int(k), "surfaceKind")
}

// enumPrefix is the C# enum a selector of this kind is a member of, and is what
// a case label and an equality test against the selector are written with.
func (k surfaceKind) enumPrefix() string {
	if k.slotForm() {
		return "LogicSlotType"
	}
	return "LogicType"
}

// selectorWidth is the width of the enum's underlying type, which decides what
// a subtraction against the selector wraps to.
func (k surfaceKind) selectorWidth() uint {
	if k.slotForm() {
		return 8
	}
	return 16
}

// surfaceSignatures matches the declaration of each method on a member's
// normalized header. The parameter names are captured because the bodies refer
// to the selector and the slot index by name and the game does not spell them
// the same way in every class.
var surfaceSignatures = map[surfaceKind]*regexp.Regexp{
	kindReadLogic:  regexp.MustCompile(`\bbool CanLogicRead\(LogicType (\w+)\)$`),
	kindWriteLogic: regexp.MustCompile(`\bbool CanLogicWrite\(LogicType (\w+)\)$`),
	kindReadSlot:   regexp.MustCompile(`\bbool CanLogicRead\(LogicSlotType (\w+), int (\w+)\)$`),
	kindWriteSlot:  regexp.MustCompile(`\bbool CanLogicWrite\(LogicSlotType (\w+), int (\w+)\)$`),
}

// surfaceProbes recognize a declaration of each method without modelling it,
// so a spelling [surfaceSignatures] does not reach -- an expression body, an
// explicit interface implementation -- is noticed rather than walked past to
// the base's body, which a class overriding it certainly does not answer for.
var surfaceProbes = map[surfaceKind]*regexp.Regexp{
	kindReadLogic:  regexp.MustCompile(`\bbool (?:\w+\.)*CanLogicRead\s*\([^)]*\bLogicType\b`),
	kindWriteLogic: regexp.MustCompile(`\bbool (?:\w+\.)*CanLogicWrite\s*\([^)]*\bLogicType\b`),
	kindReadSlot:   regexp.MustCompile(`\bbool (?:\w+\.)*CanLogicRead\s*\([^)]*\bLogicSlotType\b`),
	kindWriteSlot:  regexp.MustCompile(`\bbool (?:\w+\.)*CanLogicWrite\s*\([^)]*\bLogicSlotType\b`),
}

// baseCallRE matches a delegation to the implementation the class overrides.
var baseCallRE = regexp.MustCompile(`^base\.(CanLogicRead|CanLogicWrite)\(([^()]*)\)$`)

// isPatternRE matches the one type test the bodies use, which asks what the
// class is rather than what the instance currently holds.
var isPatternRE = regexp.MustCompile(`^this is (\w+)$`)

// constantBoolRE matches an expression-bodied property whose value is a literal.
// The two properties that matter -- whether a thing exposes an atmosphere and
// whether it exposes a reagent mixture -- are declared this way on every class
// that has an opinion, so a class's answer is a property of the class.
var constantBoolRE = regexp.MustCompile(`\bbool (\w+) => (true|false)$`)

// boolMemberRE matches any declaration of a bool member, so that a class
// answering one of those questions in a form this program does not model is
// noticed rather than read as the inherited default.
var boolMemberRE = regexp.MustCompile(`\bbool (\w+)\b`)

// numberSuffixes are the literal suffixes the decompiler writes a bound with.
// It writes the value of an unsigned or long expression as `3u` or `3L`, and a
// suffix dropped here rather than matched leaves the bound unreadable and the
// property undecided.
const numberSuffixes = "fFdDmMuUlL"

// numberRE matches a C# numeric literal with its optional type suffix.
var numberRE = regexp.MustCompile(`^-?\d+(?:\.\d+)?[` + numberSuffixes + `]*$`)

// localDeclRE matches a local variable declaration, which the bodies use only
// to name the slot they are deciding about.
var localDeclRE = regexp.MustCompile(`^[\w.<>\[\]]+ (\w+) = (.+)$`)

// slotClassEnum is the nested enum naming what a slot accepts. It is the one
// enum in these bodies the ISA tables do not carry, so it is read out of the
// decompiled source and its members are compared by name.
const slotClassEnum = "Slot.Class"

// enumSelectorRE matches a reference to a member of one of the enums the
// evaluator understands. Recognising an enum member by shape alone would read
// SuitSlot.SlotIndex, a reference to a runtime slot, as an enum member of a
// type called SuitSlot, so the qualifier is matched against a fixed set.
var enumSelectorRE = regexp.MustCompile(`^(LogicType|LogicSlotType|` + regexp.QuoteMeta(slotClassEnum) + `)\.(\w+)$`)

// selectorMemberREs find every member of a selector enum a body names, which is
// what decides whether a given selector can reach any arm other than the
// default one.
var selectorMemberREs = map[string]*regexp.Regexp{
	"LogicType":     regexp.MustCompile(`\bLogicType\.(\w+)`),
	"LogicSlotType": regexp.MustCompile(`\bLogicSlotType\.(\w+)`),
}

// selectorOffsetRE matches the range test the decompiler writes a contiguous
// group of switch cases as, capturing the selector and the offset subtracted
// from it. The compiler turns `case A: case B:` on adjacent enum values into
// one unsigned comparison; solar panels reach Horizontal and Vertical only
// through this shape, so both forms have to be read.
var selectorOffsetRE = regexp.MustCompile(`^(\w+) - (\d+)$`)

// device is everything about one prefab the logic surface is a function of.
type device struct {
	class *csharpType
	// state holds the eleven flags the roster carries, by their game names.
	// Eight of them are the ones a logic surface body anywhere in the game
	// reads, and only those eight are ever looked up here.
	state map[string]bool
	// usedPower is the prefab's serialized draw. The base implementation
	// exposes RequiredPower only on a device that draws something.
	usedPower float64
	// slotClasses is the Slot.Class member name of each declared slot, indexed
	// by slot number.
	slotClasses []string
}

// query is one question: can this device read or write this property, on this
// slot for the slot forms.
type query struct {
	device *device
	kind   surfaceKind
	// selector is the enum member name under question, and selectorValue its
	// number. Both are needed: the bodies test it by name and, where the
	// compiler folded adjacent cases together, by arithmetic.
	selector      string
	selectorValue int64
	slotIndex     int
	slotClass     string
}

// logicSurface answers those questions by evaluating the decompiled bodies.
// Every body it meets is a switch over the selector, an equality test, or a
// delegation upward, over a vocabulary of serialized fields and class facts.
// Anything outside that vocabulary evaluates to triMaybe rather than a guess.
type logicSurface struct {
	index *typeIndex
	// values is the number each member of the enums these bodies name resolves
	// to, by enum name. The selector enums are there because the bodies compare
	// against those numbers as well as against the member names; Slot.Class is
	// there for its names alone, so that a member of it no declaration holds is
	// read as a name this program misplaced rather than as a class no slot has.
	values map[string]map[string]int64
	// unusedLogic and unusedSlot are the representative selector of each enum,
	// which is what the shortcut in selectorDecider decides the unnamed ones
	// with. See unusedSelectorValue.
	unusedLogic, unusedSlot int64
	// methods caches the declaration each class inherits its implementation of
	// a method from, which is otherwise a regexp scan of every member per
	// question asked.
	methods map[methodKey]*surfaceMethod
	// statements and switches cache the two parses that would otherwise repeat
	// once per selector.
	statements map[string][]csharpDecl
	switches   map[string][]switchGroup
	// constants caches the class-level bool properties.
	constants map[constantKey]tri
	// chains caches, per class, whether the walk up its inheritance chain
	// reaches the end of what the assembly declares.
	chains map[string]bool
	// mentioned caches, per class and form, the selectors any body in the
	// inheritance chain names -- a selector named nowhere reaches the same
	// default arm as every other unnamed one. A nil entry means arithmetic
	// decides selectors and nothing can be skipped.
	mentioned map[mentionKey]map[string]bool
}

type methodKey struct {
	class string
	kind  surfaceKind
}

// constantKey keys the class-level bool property cache.
type constantKey struct {
	class string
	name  string
}

// mentionKey keys the mentioned cache. Both directions of a form are collected
// together, so the form alone decides the entry and the direction the question
// was asked in does not.
type mentionKey struct {
	class string
	slot  bool
}

// surfaceMethod is one declaration of a logic surface method: where it was
// found, what it calls its parameters, and its body.
type surfaceMethod struct {
	declaredBy *csharpType
	base       *csharpType
	selector   string
	slotID     string
	body       string
}

// newLogicSurface builds the evaluator over one decompiled tree. slotClasses is
// the Slot.Class enum as the assembly declares it, which nothing else in the
// artifact's inputs states.
func newLogicSurface(index *typeIndex, isa *ISA, slotClasses []EnumMember) (*logicSurface, error) {
	unusedLogic, err := unusedSelectorValue(kindReadLogic, isa.LogicTypes)
	if err != nil {
		return nil, err
	}
	unusedSlot, err := unusedSelectorValue(kindReadSlot, isa.SlotTypes)
	if err != nil {
		return nil, err
	}
	return &logicSurface{
		index: index,
		values: map[string]map[string]int64{
			"LogicType":     enumValues(isa.LogicTypes),
			"LogicSlotType": enumValues(isa.SlotTypes),
			slotClassEnum:   enumValues(slotClasses),
		},
		unusedLogic: unusedLogic,
		unusedSlot:  unusedSlot,
		methods:     make(map[methodKey]*surfaceMethod),
		statements:  make(map[string][]csharpDecl),
		switches:    make(map[string][]switchGroup),
		constants:   make(map[constantKey]tri),
		chains:      make(map[string]bool),
		mentioned:   make(map[mentionKey]map[string]bool),
	}, nil
}

// unusedSelector is the selector standing for every one a class never names: no
// member of the enum holds it, and no comparison the bodies write can mistake it
// for one that does.
func (s *logicSurface) unusedSelector(kind surfaceKind) EnumMember {
	if kind.slotForm() {
		return EnumMember{Value: s.unusedSlot}
	}
	return EnumMember{Value: s.unusedLogic}
}

// unusedSelectorValue picks a value inside the range the bodies compare at,
// not beyond it: an out-of-range value wraps back into range when a folded
// switch subtracts an offset (evaluated in the enum's unsigned underlying
// type), and can land on a real member.
func unusedSelectorValue(kind surfaceKind, members []EnumMember) (int64, error) {
	held := make(map[int64]bool, len(members))
	for _, member := range members {
		held[member.Value] = true
	}
	for value := int64(1)<<kind.selectorWidth() - 1; value >= 0; value-- {
		if !held[value] {
			return value, nil
		}
	}
	return 0, fmt.Errorf("%s: every value a %d bit selector holds is a member, so none stands for the unnamed ones",
		kind.enumPrefix(), kind.selectorWidth())
}

// enumValues inverts a member list to the number each name resolves to. Where
// only membership is in question the number goes unread, which is the same map
// with its values thrown away.
func enumValues(members []EnumMember) map[string]int64 {
	values := make(map[string]int64, len(members))
	for _, member := range members {
		values[member.Name] = member.Value
	}
	return values
}

// frame is the environment one method body is evaluated in.
type frame struct {
	q      *query
	method *surfaceMethod
	locals map[string]value
	depth  int
}

// can answers one question about one device.
func (s *logicSurface) can(q query) (tri, error) {
	return s.dispatch(q.device.class, &q, 0)
}

// mentions reports the selectors that any logic surface body a class inherits
// names; one absent reaches the same answer as every other unnamed selector.
// Both directions are collected together over the whole chain, since a class
// overriding one can delegate to the other. A nil result means no selector can
// be skipped, which is also the answer for a body that tests by arithmetic.
func (s *logicSurface) mentions(class *csharpType, kind surfaceKind) (map[string]bool, error) {
	key := mentionKey{class: class.Qualified, slot: kind.slotForm()}
	if cached, ok := s.mentioned[key]; ok {
		return cached, nil
	}
	kinds := []surfaceKind{kindReadLogic, kindWriteLogic}
	if kind.slotForm() {
		kinds = []surfaceKind{kindReadSlot, kindWriteSlot}
	}
	member := selectorMemberREs[kind.enumPrefix()]

	named := make(map[string]bool)
	cur, depth := class, 0
	for ; cur != nil && depth < maxInheritanceDepth; depth++ {
		for _, declared := range kinds {
			decl, signature, ok, err := surfaceDecl(cur, declared)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			body, err := memberBody(decl)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", cur.Qualified, decl.name, err)
			}
			if !comparedByNameOnly(body, signature[1]) {
				named = nil
				break
			}
			for _, m := range member.FindAllStringSubmatch(body, -1) {
				named[m[1]] = true
			}
		}
		if named == nil {
			break
		}
		next, err := s.index.baseClass(cur)
		if err != nil {
			return nil, err
		}
		cur = next
	}
	if named != nil && cur != nil {
		// The chain ran past the bound, so what the classes above it name is
		// unknown and a selector missing from the set may still reach an arm of
		// its own. Skipping nothing costs time and decides the same answers.
		named = nil
	}
	if named != nil {
		resolved, err := s.chainResolves(class)
		if err != nil {
			return nil, err
		}
		if !resolved {
			// The walk stopped at a base this program could not place, which
			// leaves the same gap for the same reason.
			named = nil
		}
	}
	s.mentioned[key] = named
	return named, nil
}

// comparedByNameOnly reports whether every mention of a body's selector
// parameter is one the member scan can see: a switch subject, an equality
// operand, or an argument to the overridden implementation. An unrecognized
// arrangement disables the mentions shortcut rather than risk a silent miss.
func comparedByNameOnly(body, selector string) bool {
	for i := 0; i < len(body); {
		at := findIdent(body[i:], selector)
		if at < 0 {
			return true
		}
		at += i
		end := at + len(selector)
		if !plainSelectorMention(body, at, end) {
			return false
		}
		i = end
	}
	return true
}

// plainSelectorMention reports whether the selector occupying [start, end) is
// only compared against a name, switched on, or passed along.
func plainSelectorMention(body string, start, end int) bool {
	switch operator := operatorBefore(body, start); operator {
	case "==", "!=", "||", "&&":
	case "":
		// A dot makes the name a member of something else and a closing bracket
		// makes it the target of a cast; neither is the parameter itself.
		if lead := nonSpaceBefore(body, start); lead != 0 && lead != '(' && lead != ',' &&
			lead != '{' && lead != '}' && lead != ';' && lead != ':' && !isIdentByte(lead) {
			return false
		}
	default:
		return false
	}

	rest := strings.TrimLeft(body[end:], " \t\r\n")
	switch operator := operatorAt(rest); operator {
	case "==", "!=":
		return true
	case "":
	default:
		return false
	}
	if rest == "" {
		return true
	}
	if rest[0] == ')' || rest[0] == ',' || rest[0] == ';' {
		return true
	}
	// The switch expression form, which puts the subject ahead of the keyword.
	word, _ := identAt(rest, 0)
	return word == "switch"
}

// operatorBefore is the run of operator bytes ending at i, with the whitespace
// between it and i skipped.
func operatorBefore(text string, i int) string {
	for i > 0 && isSpace(text[i-1]) {
		i--
	}
	end := i
	for i > 0 && isOperatorByte(text[i-1]) {
		i--
	}
	return text[i:end]
}

// nonSpaceBefore is the byte ahead of i once whitespace is skipped, and zero at
// the start of the text.
func nonSpaceBefore(text string, i int) byte {
	for i > 0 && isSpace(text[i-1]) {
		i--
	}
	if i == 0 {
		return 0
	}
	return text[i-1]
}

// operatorAt is the run of operator bytes text starts with.
func operatorAt(text string) string {
	i := 0
	for i < len(text) && isOperatorByte(text[i]) {
		i++
	}
	return text[:i]
}

// dispatch finds the implementation a class inherits and evaluates it.
func (s *logicSurface) dispatch(class *csharpType, q *query, depth int) (tri, error) {
	if class == nil {
		// Only a base call reaches here, and C# requires the base of that call
		// to declare the method it names. A base that resolved to nothing is
		// therefore a name this program could not place rather than the end of
		// the chain, and what it declares is unknown.
		return triMaybe, nil
	}
	if depth >= maxInheritanceDepth {
		return triMaybe, fmt.Errorf("%s: logic surface nested more than %d deep", class.Qualified, maxInheritanceDepth)
	}
	method, err := s.method(class, q.kind)
	if err != nil {
		return triMaybe, err
	}
	if method == nil {
		resolved, err := s.chainResolves(class)
		if err != nil {
			return triMaybe, err
		}
		if !resolved {
			// The walk stopped at a base the assembly declares and this program
			// could not place, so the classes above it were never read and the
			// method may well be declared there.
			return triMaybe, nil
		}
		// The chain ran out without declaring the method, so the thing is not
		// logicable at all and the property is not on its surface.
		return triNo, nil
	}
	f := &frame{q: q, method: method, locals: make(map[string]value), depth: depth}
	value, out, err := s.evalBody(f, method.body)
	if err != nil {
		return triMaybe, err
	}
	if out != flowAnswered {
		return triMaybe, fmt.Errorf("%s: body of the logic surface method %s rather than answering: %w",
			class.Qualified, out, errUnsupportedForm)
	}
	return value, nil
}

// chainResolves reports whether the walk up class's inheritance chain reaches
// the end of what the assembly declares, rather than stopping early at a base
// the tree does declare, which would otherwise be read as refusing every
// property. Exhausting the depth bound is an error for the same reason.
func (s *logicSurface) chainResolves(class *csharpType) (bool, error) {
	if cached, ok := s.chains[class.Qualified]; ok {
		return cached, nil
	}
	resolved := true
	cur, depth := class, 0
	for ; cur != nil && depth < maxInheritanceDepth; depth++ {
		unresolved, err := s.index.unresolvedBase(cur)
		if err != nil {
			return false, err
		}
		if unresolved != "" {
			resolved = false
			break
		}
		next, err := s.index.baseClass(cur)
		if err != nil {
			return false, err
		}
		cur = next
	}
	if resolved && cur != nil {
		return false, fmt.Errorf("%s: logic surface inheritance chain deeper than %d", class.Qualified, maxInheritanceDepth)
	}
	s.chains[class.Qualified] = resolved
	return resolved, nil
}

// surfaceDecl returns the declaration a class makes of one of the four methods,
// with the submatches of the signature it matched, and whether it declares one.
// A class the strict signature misses and the probe finds is an error, not a
// class declaring nothing: both callers would otherwise walk on to the base
// and read an implementation this class replaced.
func surfaceDecl(t *csharpType, kind surfaceKind) (csharpDecl, []string, bool, error) {
	if decl, m, ok := findMember(t, surfaceSignatures[kind]); ok {
		return decl, m, true, nil
	}
	if decl, _, ok := findMember(t, surfaceProbes[kind]); ok {
		return csharpDecl{}, nil, false, fmt.Errorf("%s: declares %.80q, which is a logic surface method in a form this program does not read: %w",
			t.Qualified, decl.name, errUnsupportedForm)
	}
	return csharpDecl{}, nil, false, nil
}

// method returns the implementation class inherits, walking up until a
// declaration is found. A nil method with no error means the chain ran out
// with no logic surface declared -- possibly at the edge of the assembly,
// which [logicSurface.chainResolves] distinguishes. Exhausting the depth
// bound is an error, not nil, since nil is read as a denial.
func (s *logicSurface) method(class *csharpType, kind surfaceKind) (*surfaceMethod, error) {
	key := methodKey{class: class.Qualified, kind: kind}
	if cached, ok := s.methods[key]; ok {
		return cached, nil
	}
	var found *surfaceMethod
	cur, depth := class, 0
	for ; cur != nil && depth < maxInheritanceDepth; depth++ {
		decl, m, ok, err := surfaceDecl(cur, kind)
		if err != nil {
			return nil, err
		}
		if ok {
			body, err := memberBody(decl)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", cur.Qualified, decl.name, err)
			}
			base, err := s.index.baseClass(cur)
			if err != nil {
				return nil, err
			}
			found = &surfaceMethod{declaredBy: cur, base: base, selector: m[1], body: body}
			if kind.slotForm() {
				found.slotID = m[2]
			}
			break
		}
		next, err := s.index.baseClass(cur)
		if err != nil {
			return nil, err
		}
		cur = next
	}
	if found == nil && cur != nil {
		return nil, fmt.Errorf("%s: logic surface inheritance chain deeper than %d", class.Qualified, maxInheritanceDepth)
	}
	s.methods[key] = found
	return found, nil
}

// evalBody evaluates a brace block, reporting how it ended.
func (s *logicSurface) evalBody(f *frame, body string) (tri, flow, error) {
	stmts, err := s.split(body)
	if err != nil {
		return triMaybe, flowFell, err
	}
	return s.evalStatements(f, stmts)
}

func (s *logicSurface) split(body string) ([]csharpDecl, error) {
	if cached, ok := s.statements[body]; ok {
		return cached, nil
	}
	stmts, err := splitDecls(unwrapBlock(body))
	if err != nil {
		return nil, fmt.Errorf("split statements: %w", err)
	}
	s.statements[body] = stmts
	return stmts, nil
}

// evalStatements runs a statement list until one of them decides where control
// goes, reporting how the list ended.
//
// A condition that cannot be decided is not skipped: both continuations are
// evaluated and the result survives only where the two agree.
func (s *logicSurface) evalStatements(f *frame, stmts []csharpDecl) (tri, flow, error) {
	for i, stmt := range stmts {
		text := strings.TrimSpace(stmt.text)
		switch {
		case isKeywordStatement(text, "if"):
			cond, body, err := splitIf(text)
			if err != nil {
				return triMaybe, flowFell, err
			}
			decided, err := s.evalExpr(f, cond)
			if err != nil {
				return triMaybe, flowFell, err
			}
			if decided == triNo {
				continue
			}
			// The guarded block's declarations are scoped to it, so what runs
			// after the if sees the bindings the if did not make.
			outer := maps.Clone(f.locals)
			taken, out, err := s.evalBody(f, body)
			f.locals = outer
			if err != nil {
				return triMaybe, flowFell, err
			}
			if out == flowFell {
				// Both paths converge on the statements after the if, so the
				// undecided condition costs nothing.
				continue
			}
			if decided == triYes {
				return taken, out, nil
			}
			skipped, skippedOut, err := s.evalStatements(f, stmts[i+1:])
			if err != nil {
				return triMaybe, flowFell, err
			}
			if skippedOut != out {
				// The two paths continue in different places, so there is no
				// single answer to merge them into. No body in the game is
				// written that way; reading past it would silently take the
				// branch that happens to decide.
				return triMaybe, flowFell, fmt.Errorf("%s: undecided branch %s and its alternative %s: %w",
					f.method.declaredBy.Qualified, out, skippedOut, errUnsupportedForm)
			}
			return merge(taken, skipped), out, nil
		case isKeywordStatement(text, "else"):
			// The statement split closes an if at the brace ending its block, so
			// an else arrives here as a statement of its own. Running it would
			// run it on the one path the game does not: the path the if took.
			return triMaybe, flowFell, fmt.Errorf("%s: else branch after an if: %w",
				f.method.declaredBy.Qualified, errUnsupportedForm)
		case isKeywordStatement(text, "switch"):
			value, out, err := s.evalSwitch(f, text)
			if err != nil || out != flowFell {
				return value, out, err
			}
		case isKeywordStatement(text, "break"):
			return triMaybe, flowBroke, nil
		case isKeywordStatement(text, "return"):
			value, err := s.evalReturn(f, text)
			return value, flowAnswered, err
		default:
			if err := s.declare(f, text); err != nil {
				return triMaybe, flowFell, err
			}
		}
	}
	return triMaybe, flowFell, nil
}

// declare binds a local. The bodies declare locals only to name the slot under
// decision, so an unrecognized initializer binds an unknown rather than failing.
func (s *logicSurface) declare(f *frame, text string) error {
	m := localDeclRE.FindStringSubmatch(strings.TrimSuffix(strings.TrimSpace(text), ";"))
	if m == nil {
		return fmt.Errorf("%s: unrecognized statement %.60q: %w",
			f.method.declaredBy.Qualified, text, errUnsupportedForm)
	}
	bound, err := s.value(f, m[2])
	if err != nil {
		return err
	}
	f.locals[m[1]] = bound
	return nil
}

// evalReturn evaluates a return statement, including the switch expression form
// the decompiler writes the shorter surfaces as.
func (s *logicSurface) evalReturn(f *frame, text string) (tri, error) {
	expr := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "return")), ";"))
	subject, arms, ok, err := splitSwitchExpr(expr)
	if err != nil {
		return triMaybe, err
	}
	if !ok {
		return s.evalExpr(f, expr)
	}
	if subject != f.method.selector {
		return triMaybe, nil
	}
	for _, arm := range arms {
		matches, readable := s.labelMatches(f, arm.label)
		if !readable {
			// Every later arm, the discard arm included, is reached only by not
			// matching this one, which this label does not say.
			return triMaybe, nil
		}
		if matches {
			return s.evalExpr(f, arm.result)
		}
	}
	return triMaybe, fmt.Errorf("%s: switch expression on %s has no arm for %s and no discard arm: %w",
		f.method.declaredBy.Qualified, subject, f.q.selector, errUnsupportedForm)
}

// evalSwitch evaluates a switch statement over the selector, reporting how it
// ended: it falls through when no arm applies or the matching arm breaks, and
// answers undecided on a switch over anything but the selector or one whose
// applicable arm cannot be told.
func (s *logicSurface) evalSwitch(f *frame, text string) (tri, flow, error) {
	subject, body, err := splitSwitch(text)
	if err != nil {
		return triMaybe, flowFell, err
	}
	if subject != f.method.selector {
		return triMaybe, flowAnswered, nil
	}
	groups, err := s.switchBody(body)
	if err != nil {
		return triMaybe, flowFell, err
	}
	var fallback *switchGroup
	for i, group := range groups {
		for _, label := range group.labels {
			if label == "default" {
				fallback = &groups[i]
				continue
			}
			matches, readable := s.labelMatches(f, label)
			if !readable {
				// Every later arm, the default one included, is reached only by
				// not matching this one, which this label does not say.
				return triMaybe, flowAnswered, nil
			}
			if matches {
				return s.evalArm(f, groups[i].body)
			}
		}
	}
	if fallback != nil {
		return s.evalArm(f, fallback.body)
	}
	return triMaybe, flowFell, nil
}

// evalArm evaluates the arm a switch reached. An arm that breaks leaves the
// switch, which is where the statements after it run, so it reaches the caller
// as a fall-through rather than as a break of the caller's own.
func (s *logicSurface) evalArm(f *frame, body string) (tri, flow, error) {
	value, out, err := s.evalBody(f, body)
	if err != nil || out != flowAnswered {
		return triMaybe, flowFell, err
	}
	return value, flowAnswered, nil
}

func (s *logicSurface) switchBody(body string) ([]switchGroup, error) {
	if cached, ok := s.switches[body]; ok {
		return cached, nil
	}
	groups, err := splitSwitchBody(body)
	if err != nil {
		return nil, err
	}
	s.switches[body] = groups
	return groups, nil
}

// labelMatches reports whether a case label names the selector under query,
// and whether the label could be read at all: every later arm is reached only
// by not matching this one, so a label this cannot read is not a non-match.
// Only a disjunction of patterns is read here, not a relational or negated one.
func (s *logicSurface) labelMatches(f *frame, label string) (matches, readable bool) {
	if label == "_" {
		return true, true
	}
	for alternative := range strings.SplitSeq(label, " or ") {
		m := enumSelectorRE.FindStringSubmatch(strings.TrimSpace(alternative))
		if m == nil || m[1] != f.q.kind.enumPrefix() {
			return false, false
		}
		if m[2] == f.q.selector {
			return true, true
		}
	}
	return false, true
}

// evalExpr evaluates a boolean expression over the vocabulary the bodies use.
func (s *logicSurface) evalExpr(f *frame, expr string) (tri, error) {
	expr = stripOuterParens(strings.TrimSpace(expr))
	if parts := splitTopOperator(expr, "||"); len(parts) > 1 {
		return s.fold(f, parts, or, triNo)
	}
	if parts := splitTopOperator(expr, "&&"); len(parts) > 1 {
		return s.fold(f, parts, and, triYes)
	}
	if rest, ok := strings.CutPrefix(expr, "!"); ok {
		value, err := s.evalExpr(f, rest)
		return value.not(), err
	}
	for _, op := range []string{"==", "!=", ">=", "<=", ">", "<"} {
		if lhs, rhs, ok := cutTopOperator(expr, op); ok {
			return s.compare(f, op, lhs, rhs)
		}
	}
	return s.evalAtom(f, expr)
}

func (s *logicSurface) fold(f *frame, parts []string, combine func(a, b tri) tri, unit tri) (tri, error) {
	result := unit
	for _, part := range parts {
		value, err := s.evalExpr(f, part)
		if err != nil {
			return triMaybe, err
		}
		result = combine(result, value)
	}
	return result, nil
}

// evalAtom evaluates an expression with no operator left in it.
func (s *logicSurface) evalAtom(f *frame, expr string) (tri, error) {
	switch expr {
	case "true":
		return triYes, nil
	case "false":
		return triNo, nil
	}
	if m := baseCallRE.FindStringSubmatch(expr); m != nil {
		return s.evalBaseCall(f, m[1], m[2])
	}
	if m := isPatternRE.FindStringSubmatch(expr); m != nil {
		return s.index.derivesFrom(f.q.device.class, m[1])
	}

	name := strings.TrimPrefix(expr, "base.")
	if flag, ok := f.q.device.state[name]; ok {
		return triOf(flag), nil
	}
	switch name {
	case "IsStructureCompleted":
		// The extracted table describes a finished device on purpose. The base
		// implementations open by refusing everything on a construction in
		// progress, and a table that answered for that state would say no to
		// every property of every device.
		return triYes, nil
	case "HasAnySlots":
		return triOf(len(f.q.device.slotClasses) > 0), nil
	case "HasReadableAtmosphere", "HasReadableReagentMixture":
		return s.constantBool(f.q.device.class, name)
	}
	return triMaybe, nil
}

// evalBaseCall continues the question at the class above the one that
// declared the body being evaluated. The called method decides the
// direction, since some classes' CanLogicWrite delegates to
// base.CanLogicRead. The arguments are checked too: a delegation substituting
// another selector or slot asks a different question.
func (s *logicSurface) evalBaseCall(f *frame, method, args string) (tri, error) {
	want := f.method.selector
	if f.q.kind.slotForm() {
		want += ", " + f.method.slotID
	}
	if strings.Join(strings.Fields(args), " ") != want {
		return triMaybe, nil
	}
	called := *f.q
	switch {
	case method == "CanLogicRead" && f.q.kind.slotForm():
		called.kind = kindReadSlot
	case method == "CanLogicRead":
		called.kind = kindReadLogic
	case f.q.kind.slotForm():
		called.kind = kindWriteSlot
	default:
		called.kind = kindWriteLogic
	}
	return s.dispatch(f.method.base, &called, f.depth+1)
}

// constantBool answers a class-level bool property whose overrides are
// literal. A class declaring the property in any other form yields triMaybe:
// the answer is real but not a constant of the class, so reading past the
// declaration to an inherited literal would report the wrong one.
func (s *logicSurface) constantBool(class *csharpType, name string) (tri, error) {
	key := constantKey{class: class.Qualified, name: name}
	if cached, ok := s.constants[key]; ok {
		return cached, nil
	}
	result := triMaybe
	for cur, depth := class, 0; cur != nil && depth < maxInheritanceDepth; depth++ {
		if declared, found := declaredBool(cur, name); found {
			result = declared
			break
		}
		next, err := s.index.baseClass(cur)
		if err != nil {
			return triMaybe, err
		}
		cur = next
	}
	s.constants[key] = result
	return result, nil
}

// declaredBool returns the value a class declares a bool member as, and
// whether it declares it at all. A declaration in a form other than a
// literal expression body still counts as one, since the class has an
// answer of its own that an inherited literal would misreport.
func declaredBool(t *csharpType, name string) (tri, bool) {
	for _, member := range t.members {
		declared := boolMemberRE.FindStringSubmatch(member.name)
		if declared == nil || declared[1] != name {
			continue
		}
		if constant := constantBoolRE.FindStringSubmatch(member.name); constant != nil {
			return triOf(constant[2] == "true"), true
		}
		return triMaybe, true
	}
	return triMaybe, false
}

// valueKind classifies the operands the comparisons in these bodies have.
type valueKind uint8

const (
	valUnknown valueKind = iota
	valNull
	valNumber
	// valEnumMember is a member of the selector enum the ISA tables carry, with
	// its number. A name the tables hold no member under is valUnknown instead:
	// reading a miss as zero would decide every selector against a bound the
	// body never wrote.
	valEnumMember
	// valSlotClassMember is a member of Slot.Class, compared against a slot's
	// class by name. A name the enum declares no member under is valUnknown
	// instead: reading the miss as a non-match would deny the property on every
	// slot the body actually grants it to.
	valSlotClassMember
	// valSelector is the LogicType or LogicSlotType parameter itself, and
	// valSelectorOffset that parameter with a constant subtracted from it.
	valSelector
	valSelectorOffset
	// valSlotIndex is the slot number parameter, and valSlotCount the length of
	// the slot list it is bounds-checked against.
	valSlotIndex
	valSlotCount
	// valSlot is a slot object, and valSlotClass its Slot.Class.
	valSlot
	valSlotClass
	valUsedPower
)

type value struct {
	kind   valueKind
	number float64
	text   string
}

// value classifies one side of a comparison. An expression outside the
// vocabulary is unknown rather than an error, leaving the comparison
// undecided; a literal this program matched and then could not read is an
// error, since dropping it to unknown would hide a bound the body does write.
func (s *logicSurface) value(f *frame, expr string) (value, error) {
	expr = stripOuterParens(strings.TrimSpace(expr))
	switch expr {
	case "null":
		return value{kind: valNull}, nil
	case f.method.selector:
		return value{kind: valSelector}, nil
	case "Slots.Count":
		return value{kind: valSlotCount}, nil
	case "UsedPower":
		return value{kind: valUsedPower}, nil
	}
	if f.q.kind.slotForm() && expr == f.method.slotID {
		return value{kind: valSlotIndex}, nil
	}
	if local, ok := f.locals[expr]; ok {
		return local, nil
	}
	if numberRE.MatchString(expr) {
		n, err := strconv.ParseFloat(strings.TrimRight(expr, numberSuffixes), 64)
		if err != nil {
			return value{}, fmt.Errorf("%s: numeric literal %s: %w", f.method.declaredBy.Qualified, expr, err)
		}
		return value{kind: valNumber, number: n}, nil
	}
	if m := enumSelectorRE.FindStringSubmatch(expr); m != nil {
		number, declared := s.values[m[1]][m[2]]
		switch {
		case !declared:
			return value{kind: valUnknown}, nil
		case m[1] == slotClassEnum:
			return value{kind: valSlotClassMember, text: m[2]}, nil
		}
		return value{kind: valEnumMember, text: m[2], number: float64(number)}, nil
	}
	if m := selectorOffsetRE.FindStringSubmatch(expr); m != nil && m[1] == f.method.selector {
		offset, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			return value{}, fmt.Errorf("%s: offset subtracted from %s: %w", f.method.declaredBy.Qualified, f.method.selector, err)
		}
		return value{kind: valSelectorOffset, number: float64(offset)}, nil
	}
	if owner, ok := strings.CutSuffix(expr, ".Type"); ok {
		holder, err := s.value(f, owner)
		if err != nil {
			return value{}, err
		}
		if holder.kind == valSlot {
			return value{kind: valSlotClass}, nil
		}
	}
	if f.q.kind.slotForm() {
		if expr == "GetSlot("+f.method.slotID+")" || expr == "Slots["+f.method.slotID+"]" {
			return value{kind: valSlot}, nil
		}
	}
	return value{kind: valUnknown}, nil
}

// compare evaluates a binary comparison. Only the operand pairs the bodies
// actually form are decided; each pair is written once in comparePair and
// tried in both orders, since which side a body puts the constant on is a
// matter of style.
func (s *logicSurface) compare(f *frame, op, lhsExpr, rhsExpr string) (tri, error) {
	lhs, err := s.value(f, lhsExpr)
	if err != nil {
		return triMaybe, err
	}
	rhs, err := s.value(f, rhsExpr)
	if err != nil {
		return triMaybe, err
	}
	if decided := s.comparePair(f, op, lhs, rhs); decided != triMaybe {
		return decided, nil
	}
	return s.comparePair(f, flipComparison(op), rhs, lhs), nil
}

func (s *logicSurface) comparePair(f *frame, op string, lhs, rhs value) tri {
	q := f.q
	switch {
	case lhs.kind == valSelector && rhs.kind == valEnumMember:
		return equality(op, q.selector == rhs.text)
	case lhs.kind == valSelectorOffset && rhs.kind == valEnumMember:
		// The subtraction is done in the enum's own unsigned underlying type,
		// so a selector below the offset wraps to a large number and falls
		// outside the range rather than below it.
		wrapped := (uint64(q.selectorValue) - uint64(lhs.number)) & (1<<q.kind.selectorWidth() - 1)
		return relation(op, float64(wrapped), rhs.number)
	case lhs.kind == valSlotClass && rhs.kind == valSlotClassMember:
		return equality(op, q.slotClass == rhs.text)
	case lhs.kind == valSlot && rhs.kind == valNull:
		// Only slots the prefab declares are ever asked about, so the slot the
		// index names exists.
		return equality(op, false)
	case lhs.kind == valSlotIndex && rhs.kind == valNumber:
		return relation(op, float64(q.slotIndex), rhs.number)
	case lhs.kind == valSlotIndex && rhs.kind == valSlotCount:
		return relation(op, float64(q.slotIndex), float64(len(q.device.slotClasses)))
	case lhs.kind == valUsedPower && rhs.kind == valNumber:
		return relation(op, q.device.usedPower, rhs.number)
	}
	return triMaybe
}

func equality(op string, equal bool) tri {
	switch op {
	case "==":
		return triOf(equal)
	case "!=":
		return triOf(!equal)
	}
	return triMaybe
}

func relation(op string, a, b float64) tri {
	switch op {
	case "==":
		return triOf(a == b)
	case "!=":
		return triOf(a != b)
	case "<":
		return triOf(a < b)
	case "<=":
		return triOf(a <= b)
	case ">":
		return triOf(a > b)
	case ">=":
		return triOf(a >= b)
	}
	return triMaybe
}

// flipComparison is the operator that means the same thing with the operands
// exchanged.
func flipComparison(op string) string {
	switch op {
	case "<":
		return ">"
	case "<=":
		return ">="
	case ">":
		return "<"
	case ">=":
		return "<="
	}
	return op
}
