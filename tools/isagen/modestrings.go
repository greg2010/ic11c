package main

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// modeStringsRE matches the Thing.ModeStrings property declaration on a
// member's raw text. Overrides reach their strings through a literal array,
// a static field, a C# enum's names, or an EnumCollection; anything else is
// reported unresolved rather than substituting the shorter inherited default.
var modeStringsRE = regexp.MustCompile(`\bstring\[\] ModeStrings\b`)

// stringArrayFieldRE matches a field declaration of string array type on a
// member's normalized header.
var stringArrayFieldRE = regexp.MustCompile(`\bstring\[\] (\w+)$`)

// enumCollectionFieldRE matches a field declaration whose type wraps an enum,
// capturing the enum and the field name.
var enumCollectionFieldRE = regexp.MustCompile(`\bEnumCollection<([\w.]+), *\w+> (\w+)$`)

// gameStringCollectionFieldRE matches a field declaration of the localized
// collection type, capturing the enum and the field name. It takes one type
// argument where EnumCollection takes two, which is what separates the two
// spellings.
var gameStringCollectionFieldRE = regexp.MustCompile(`\bGameStringCollection<([\w.]+)> (\w+)$`)

// newGameStringCollectionRE matches its construction, capturing the enum.
var newGameStringCollectionRE = regexp.MustCompile(`^new GameStringCollection<([\w.]+)>\(.*\)$`)

// enumNamesRE matches the reflection call the game reaches an enum's names
// through.
var enumNamesRE = regexp.MustCompile(`^Enum\.GetNames\(typeof\(([\w.]+)\)\)$`)

// newStringArrayRE matches the head of a string array creation, whose element
// list follows in a brace block.
var newStringArrayRE = regexp.MustCompile(`^new string\[\d*\]$`)

// newEnumCollectionRE matches an EnumCollection construction, capturing the
// enum it wraps and the arguments that decide how it renders the names.
var newEnumCollectionRE = regexp.MustCompile(`^new EnumCollection<([\w.]+), *\w+>\((.*)\)$`)

// modeResolver recovers a class's mode strings from the decompiled tree.
type modeResolver struct {
	index *typeIndex
	cache map[string]resolvedModes
}

// resolvedModes is one class's answer. Whether the declaration resolved is
// carried beside the names rather than read off them: a class whose mode list
// is declared empty resolves to no names, and inferring "unresolved" from that
// would make a class's answer depend on whether it had been asked before.
type resolvedModes struct {
	names    []string
	resolved bool
}

func newModeResolver(index *typeIndex) *modeResolver {
	return &modeResolver{index: index, cache: make(map[string]resolvedModes)}
}

// modes returns the mode names a class exposes, or false when the declaration
// is in a form this program does not resolve.
func (r *modeResolver) modes(class *csharpType) ([]string, bool, error) {
	if cached, ok := r.cache[class.Qualified]; ok {
		return cached.names, cached.resolved, nil
	}
	names, resolved, err := r.resolveModes(class)
	if err != nil {
		return nil, false, fmt.Errorf("%s.ModeStrings: %w", class.Qualified, err)
	}
	if !resolved {
		names = nil
	}
	r.cache[class.Qualified] = resolvedModes{names: names, resolved: resolved}
	return names, resolved, nil
}

func (r *modeResolver) resolveModes(class *csharpType) ([]string, bool, error) {
	for cur, depth := class, 0; cur != nil && depth < maxInheritanceDepth; depth++ {
		for _, member := range cur.members {
			if !modeStringsRE.MatchString(member.text) {
				continue
			}
			expr, ok := initializer(member.text)
			if !ok {
				return nil, false, nil
			}
			return r.strings(cur, expr, 0)
		}
		next, err := r.index.baseClass(cur)
		if err != nil {
			return nil, false, err
		}
		cur = next
	}
	return nil, false, nil
}

// initializer returns the expression a member is defined as, whether written as
// an expression body or as an initializer.
func initializer(text string) (string, bool) {
	for _, op := range []string{"=>", "="} {
		if _, rhs, ok := cutTopOperator(text, op); ok {
			return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rhs), ";")), true
		}
	}
	return "", false
}

// maxModeIndirections bounds the chain of field references one mode list is
// reached through. C# lets two string array fields initialize from each other,
// so the chain is not guaranteed to bottom out, and a real one is two or three
// hops.
const maxModeIndirections = 16

// strings resolves an expression of string array type, following a reference to
// a field once it is resolved to the type declaring it.
func (r *modeResolver) strings(from *csharpType, expr string, depth int) ([]string, bool, error) {
	if depth >= maxModeIndirections {
		// A reference chain that does not reach a literal names a value the
		// game fills in at run time, which is the same answer as a field
		// declared here and assigned elsewhere.
		return nil, false, nil
	}
	expr = strings.TrimSpace(expr)
	if head, rest, ok := strings.Cut(expr, "{"); ok && newStringArrayRE.MatchString(strings.TrimSpace(head)) {
		return stringLiterals("{" + rest)
	}
	if m := enumNamesRE.FindStringSubmatch(expr); m != nil {
		return r.enumNames(from, m[1])
	}
	// A bare reference to an EnumCollection reaches its names through an
	// implicit conversion, so the two spellings resolve alike.
	target := strings.TrimSuffix(expr, ".Names")
	owner, field, err := r.field(from, target)
	if err != nil || owner == nil {
		return nil, false, err
	}
	if m := enumCollectionFieldRE.FindStringSubmatch(field.name); m != nil {
		return r.enumCollection(owner, field)
	}
	if m := gameStringCollectionFieldRE.FindStringSubmatch(field.name); m != nil {
		return r.gameStringCollection(owner, field)
	}
	if !stringArrayFieldRE.MatchString(field.name) {
		return nil, false, nil
	}
	value, ok := initializer(field.text)
	if !ok {
		// Declared here but filled in elsewhere, which is a runtime value.
		return nil, false, nil
	}
	return r.strings(owner, value, depth+1)
}

// field resolves a reference to a field, which the game writes either bare,
// naming a field of the class or one of its bases, or qualified by the type
// declaring it.
func (r *modeResolver) field(from *csharpType, ref string) (*csharpType, csharpDecl, error) {
	owner, name := from, ref
	if typeName, member, qualified := strings.Cut(ref, "."); qualified {
		if strings.Contains(member, ".") {
			return nil, csharpDecl{}, nil
		}
		resolved, err := r.index.resolve(from, typeName)
		if err != nil || resolved == nil {
			return nil, csharpDecl{}, err
		}
		owner, name = resolved, member
	}
	for cur, depth := owner, 0; cur != nil && depth < maxInheritanceDepth; depth++ {
		for _, member := range cur.members {
			if named, ok := fieldName(member.name); ok && named == name {
				return cur, member, nil
			}
		}
		next, err := r.index.baseClass(cur)
		if err != nil {
			return nil, csharpDecl{}, err
		}
		cur = next
	}
	return nil, csharpDecl{}, nil
}

// fieldName returns the name a field declaration declares, which is the last
// identifier of its normalized header.
func fieldName(header string) (string, bool) {
	for _, pattern := range []*regexp.Regexp{stringArrayFieldRE, enumCollectionFieldRE, gameStringCollectionFieldRE} {
		if m := pattern.FindStringSubmatch(header); m != nil {
			return m[len(m)-1], true
		}
	}
	return "", false
}

// enumCollection resolves the names an EnumCollection field exposes, which are
// its enum's, optionally with a space inserted before each interior capital.
func (r *modeResolver) enumCollection(owner *csharpType, field csharpDecl) ([]string, bool, error) {
	value, ok := initializer(field.text)
	if !ok {
		return nil, false, nil
	}
	m := newEnumCollectionRE.FindStringSubmatch(value)
	if m == nil {
		return nil, false, nil
	}
	proper, readable := rendersProper(m[2])
	if !readable {
		return nil, false, nil
	}
	names, ok, err := r.enumNames(owner, m[1])
	if err != nil || !ok {
		return nil, ok, err
	}
	if proper {
		for i, name := range names {
			names[i] = toProper(name)
		}
	}
	return names, true, nil
}

// rendersProper reads an EnumCollection constructor's argument list for the
// one argument that decides whether the names are rendered into labels, and
// reports whether the list said. The parameter defaults to true, so an empty
// list renders; any other list is a shape this program does not read.
func rendersProper(args string) (proper, readable bool) {
	switch strings.TrimSpace(args) {
	case "":
		return true, true
	case "toProper: true":
		return true, true
	case "toProper: false":
		return false, true
	}
	return false, false
}

// gameStringCollection resolves the names a localized collection field
// exposes: the enum's own, whatever the constructor's toProper argument
// says. That argument renders the display strings carried beside the names,
// while a mode list reads the names -- unlike EnumCollection, which properizes
// the names themselves and so is resolved separately.
func (r *modeResolver) gameStringCollection(owner *csharpType, field csharpDecl) ([]string, bool, error) {
	value, ok := initializer(field.text)
	if !ok {
		return nil, false, nil
	}
	m := newGameStringCollectionRE.FindStringSubmatch(value)
	if m == nil {
		return nil, false, nil
	}
	return r.enumNames(owner, m[1])
}

// enumNames returns an enum's member names in the order .NET reflection
// reports them, which is by value and is the order a mode number indexes. A
// repeated value or a set not running from zero would make the index and the
// value disagree, so resolution stops rather than name the wrong setting.
func (r *modeResolver) enumNames(from *csharpType, written string) ([]string, bool, error) {
	resolved, err := r.index.resolve(from, written)
	if err != nil {
		return nil, false, err
	}
	if resolved == nil {
		outer, inner, nested := strings.Cut(written, ".")
		if !nested {
			return nil, false, nil
		}
		// A nested enum is declared inside its outer type's file, which is the
		// one the tree holds under a name of its own.
		declaring, err := r.index.resolve(from, outer)
		if err != nil || declaring == nil {
			return nil, false, err
		}
		return enumMemberNames(declaring.source, inner)
	}
	return enumMemberNames(resolved.source, resolved.Name)
}

// enumMemberNames reads the names an enum declares out of the file declaring
// it. A file declaring no enum under the name is not an error -- the
// reference resolved to a type that is not one, the same unresolved answer
// as a runtime-filled mode list -- but any other read failure is.
func enumMemberNames(src, name string) ([]string, bool, error) {
	members, err := parseEnum(src, name)
	switch {
	case errors.Is(err, errNotFound):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	slices.SortStableFunc(members, func(a, b EnumMember) int { return cmp.Compare(a.Value, b.Value) })
	names := make([]string, len(members))
	for i, member := range members {
		if member.Value != int64(i) {
			return nil, false, nil
		}
		names[i] = member.Name
	}
	return names, true, nil
}

// stringLiterals reads the elements of a brace-delimited string array
// initializer.
func stringLiterals(src string) ([]string, bool, error) {
	body, err := braceBlockAt(src)
	if err != nil {
		return nil, false, err
	}
	var values []string
	for _, entry := range splitExprList(body) {
		value, err := stringLiteral(strings.TrimSpace(entry))
		if err != nil {
			// An element that is not a literal -- a constant, a concatenation, a
			// localization lookup -- names a value this program does not follow,
			// which is the same answer as a list the game fills in at run time.
			return nil, false, nil
		}
		values = append(values, value)
	}
	return values, true, nil
}

// toProper is the game's own rendering of an enum member name into a label: a
// space before every capital but the first.
func toProper(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		if i > 0 && name[i] >= 'A' && name[i] <= 'Z' {
			b.WriteByte(' ')
		}
		b.WriteByte(name[i])
	}
	return b.String()
}
