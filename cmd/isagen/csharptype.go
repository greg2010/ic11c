package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// csharpType is one top-level type of the decompiled tree, read far enough to
// answer the two questions the logic surface needs: what a type inherits, and
// what its members say.
//
// The decompiler writes one file per top-level type under a directory per
// namespace component, so a type's qualified name is its path and nothing has
// to be searched for. What a type's declaration names its bases is another
// matter: C# writes those the way the file's using directives resolve them, so
// they are kept as written and resolved on demand.
type csharpType struct {
	Qualified string
	Name      string
	Namespace string
	// Usings are the namespaces the file imports, in source order, and are half
	// of what a written type name resolves against.
	Usings []string
	// Aliases are the type aliases the file binds. An alias contributes no
	// namespace, so a name written through one resolves through the alias and
	// through nothing else.
	Aliases map[string]string
	// IsClass reports whether an inheritance chain can pass through the type.
	// C# writes the base class and the implemented interfaces in one comma
	// separated list, so which entry is the base class is only decidable by
	// resolving them.
	IsClass bool
	// Bases are the base class and interfaces exactly as the declaration writes
	// them, with any generic arguments dropped.
	Bases []string
	// source is the whole file, kept because an enum's members are read from it
	// rather than from the member list.
	source  string
	members []csharpDecl
}

// typeIndex reads types out of the decompiled tree on demand and remembers
// both what it found and what it did not. A name that resolves to nothing is a
// type from another assembly -- MonoBehaviour, the BCL -- which is a normal
// outcome and not an error.
type typeIndex struct {
	tree  *sourceTree
	types map[string]*csharpType
}

func newTypeIndex(tree *sourceTree) *typeIndex {
	return &typeIndex{tree: tree, types: make(map[string]*csharpType)}
}

// lookup returns the type declared under a fully qualified name, or nil when
// the tree declares no such type.
//
// An error means the file is there and could not be read, or could not be read
// as one type declaration, which is a change in the layout the whole extraction
// rests on. It is never reported as an absent type: a class read as absent
// takes every property it declares out of every prefab below it, silently.
func (x *typeIndex) lookup(qualified string) (*csharpType, error) {
	if cached, ok := x.types[qualified]; ok {
		return cached, nil
	}
	src, err := x.tree.qualified(qualified)
	switch {
	case errors.Is(err, errNotFound):
		x.types[qualified] = nil
		return nil, nil
	case err != nil:
		return nil, err
	}
	parsed, err := parseCsharpType(qualified, src)
	if err != nil {
		return nil, err
	}
	x.types[qualified] = parsed
	return parsed, nil
}

// resolve returns the type a name written inside from denotes, following the
// order C# itself searches: the enclosing namespaces innermost first, then the
// imported ones, and finally the global namespace. from may be nil, which
// searches the global namespace alone.
func (x *typeIndex) resolve(from *csharpType, written string) (*csharpType, error) {
	for _, candidate := range resolutionCandidates(from, written) {
		found, err := x.lookup(candidate)
		if err != nil {
			return nil, err
		}
		if found != nil {
			return found, nil
		}
	}
	return nil, nil
}

func resolutionCandidates(from *csharpType, written string) []string {
	var candidates []string
	if from != nil {
		for ns := from.Namespace; ns != ""; {
			candidates = append(candidates, ns+"."+written)
			if cut := strings.LastIndexByte(ns, '.'); cut >= 0 {
				ns = ns[:cut]
			} else {
				ns = ""
			}
		}
		// The enclosing namespaces are searched ahead of the aliases because
		// the aliases sit outside the file-scoped namespace, and the aliases
		// ahead of the imports for the same reason.
		head, rest, nested := strings.Cut(written, ".")
		if target, aliased := from.Aliases[head]; aliased {
			if nested {
				target += "." + rest
			}
			candidates = append(candidates, target)
		}
		for _, using := range from.Usings {
			candidates = append(candidates, using+"."+written)
		}
	}
	return append(candidates, written)
}

// maxInheritanceDepth bounds every walk up the chain baseClass follows, which
// the mode strings, the field references and the logic surface each make one
// of. The deepest real chain is a fraction of this; the bound exists so a cycle
// introduced by a misparsed base list fails instead of hanging.
const maxInheritanceDepth = 64

// classKeywords are the type keywords declaring a type an inheritance chain can
// pass through. A struct has none and an interface's base list holds only
// interfaces, so a base list entry resolving to either is not the base class.
var classKeywords = map[string]bool{"class": true, "record": true}

// baseClass returns the type t extends, or nil at the root of what the tree
// declares. The base class is the one entry of the declaration's base list that
// resolves to a class; everything else in that list is an interface.
func (x *typeIndex) baseClass(t *csharpType) (*csharpType, error) {
	for _, written := range t.Bases {
		base, err := x.resolve(t, written)
		if err != nil {
			return nil, err
		}
		if base != nil && base.IsClass {
			return base, nil
		}
	}
	return nil, nil
}

// unresolvedBase returns the first entry of t's base list that resolves to
// nothing although the tree declares a type under its leading name, and the
// empty string when every entry either resolved or names nothing the tree holds.
//
// A base the tree declares nowhere is a type from another assembly, which is
// where every inheritance chain legitimately ends. One it does declare is a
// type this program failed to place, and what that type says about a logic
// surface is unknown rather than absent.
func (x *typeIndex) unresolvedBase(t *csharpType) (string, error) {
	for _, written := range t.Bases {
		base, err := x.resolve(t, written)
		if err != nil {
			return "", err
		}
		if base == nil && x.unplaceable(written) {
			return written, nil
		}
	}
	return "", nil
}

// unplaceable reports whether a base list entry that resolved to nothing is one
// this program failed to place, which is the distinction
// [typeIndex.unresolvedBase] is built on and the one thing that separates the
// end of an inheritance chain from a hole in it.
func (x *typeIndex) unplaceable(written string) bool {
	// A nested type is declared inside its outer type's file, so the leading
	// name is the one the tree holds a file under either way.
	declaring, _, _ := strings.Cut(written, ".")
	return x.tree.declares(declaring)
}

// derivesFrom answers whether t is, extends, or implements the named type.
//
// The comparison is by bare name rather than by resolved identity, because the
// game names an interface in a base list the way its own file resolves it and
// the interesting names -- ICircuitHolder, IMemory, ILogicAtmospheric -- are
// unique across the assembly. A base the tree does not declare still counts,
// which is what keeps a type from another assembly out of the answer.
//
// A base this program could not place yields triMaybe rather than triNo, by the
// distinction [typeIndex.unplaceable] draws: the types above such a base went
// unread, so what they implement is unknown, and a denial there reports a thing
// as not being what it is.
//
// A type contributes its own answer once and is then cut off, which terminates a
// base list that cycles and visits a type two paths reach only once. That is
// sound for the answer at the root and not for the answer at every node: a
// triMaybe found on the first path has already been folded into the result of
// every type above it, and or absorbs it there, so the second path finding
// triNo where it would have found triMaybe cannot lower what the root returns.
func (x *typeIndex) derivesFrom(t *csharpType, name string) (tri, error) {
	seen := make(map[string]bool)
	var walk func(*csharpType) (tri, error)
	walk = func(t *csharpType) (tri, error) {
		if seen[t.Qualified] {
			return triNo, nil
		}
		seen[t.Qualified] = true
		if t.Name == name {
			return triYes, nil
		}
		result := triNo
		for _, written := range t.Bases {
			if bareTypeName(written) == name {
				return triYes, nil
			}
			base, err := x.resolve(t, written)
			if err != nil {
				return triNo, err
			}
			if base == nil {
				if x.unplaceable(written) {
					result = triMaybe
				}
				continue
			}
			found, err := walk(base)
			if err != nil {
				return triNo, err
			}
			if found == triYes {
				return triYes, nil
			}
			result = or(result, found)
		}
		return result, nil
	}
	return walk(t)
}

// findMember returns the first member whose normalized header the pattern
// matches, along with the submatches.
func findMember(t *csharpType, pattern *regexp.Regexp) (csharpDecl, []string, bool) {
	for _, member := range t.members {
		if m := pattern.FindStringSubmatch(member.name); m != nil {
			return member, m, true
		}
	}
	return csharpDecl{}, nil, false
}

// memberBody returns the brace block a member declaration carries.
func memberBody(decl csharpDecl) (string, error) {
	for i := 0; i < len(decl.text); i++ {
		if j := skipLiteral(decl.text, i); j != i {
			i = j - 1
			continue
		}
		if decl.text[i] == '{' {
			return braceBlockAt(decl.text[i:])
		}
	}
	return "", fmt.Errorf("member %q has no body: %w", decl.name, errNotFound)
}

// namespaceRE matches the file-scoped namespace declaration the decompiler
// writes. A type in the global namespace has none.
var namespaceRE = regexp.MustCompile(`(?m)^namespace\s+([\w.]+)\s*;`)

// usingRE matches a plain using directive. Aliases and static imports are not
// matched: neither contributes a namespace a bare type name resolves against.
var usingRE = regexp.MustCompile(`(?m)^using\s+([\w.]+)\s*;`)

// usingAliasRE matches a using alias directive, which binds one name to one
// type. A base written through an alias resolves through nothing else, and a
// base that resolves to nothing ends an inheritance chain, so a file's aliases
// have to be read or a chain ends where the game continues it.
var usingAliasRE = regexp.MustCompile(`(?m)^using\s+(\w+)\s*=\s*([\w.]+)\s*;`)

// parseCsharpType reads one decompiled file into the declaration it holds.
func parseCsharpType(qualified, src string) (*csharpType, error) {
	name := qualified
	if cut := strings.LastIndexByte(qualified, '.'); cut >= 0 {
		name = qualified[cut+1:]
	}
	decl, err := topLevelType(src, name)
	if err != nil {
		return nil, err
	}

	t := &csharpType{Qualified: qualified, Name: name, source: src}
	if m := namespaceRE.FindStringSubmatch(src); m != nil {
		t.Namespace = m[1]
	}
	for _, m := range usingRE.FindAllStringSubmatch(src, -1) {
		t.Usings = append(t.Usings, m[1])
	}
	for _, m := range usingAliasRE.FindAllStringSubmatch(src, -1) {
		if t.Aliases == nil {
			t.Aliases = make(map[string]string)
		}
		t.Aliases[m[1]] = m[2]
	}

	header, err := stripAttributes(stripLeadingComments(declHeader(decl.text)))
	if err != nil {
		return nil, fmt.Errorf("type %s: %w", qualified, err)
	}
	// The constraint clause is cut before the base list is looked for, because a
	// generic type can carry one without declaring a base and the colon in
	// `where T : IBar` would then be read as opening a base list of its own.
	if cut := strings.Index(header, " where "); cut >= 0 {
		header = header[:cut]
	}
	parts := splitTop(header, ':')
	head, err := classifyDecl(parts[0])
	if err != nil {
		return nil, fmt.Errorf("type %s: %w", qualified, err)
	}
	t.IsClass = classKeywords[head.keyword]
	if len(parts) > 1 {
		for _, written := range splitTop(parts[1], ',') {
			if bare := bareTypeName(written); bare != "" {
				t.Bases = append(t.Bases, bare)
			}
		}
	}

	t.members, err = splitDecls(decl.body)
	if err != nil {
		return nil, fmt.Errorf("type %s: %w", qualified, err)
	}
	return t, nil
}

// declHeader returns the part of a declaration ahead of its brace block.
func declHeader(text string) string {
	for i := 0; i < len(text); i++ {
		if j := skipLiteral(text, i); j != i {
			i = j - 1
			continue
		}
		if text[i] == '{' {
			return text[:i]
		}
	}
	return text
}

// bareTypeName strips the generic arguments and the whitespace from a type name
// as a declaration writes it.
func bareTypeName(written string) string {
	written = strings.TrimSpace(written)
	if cut := strings.IndexByte(written, '<'); cut >= 0 {
		written = strings.TrimSpace(written[:cut])
	}
	return written
}

// errUnsupportedForm reports a C# shape the extraction does not model. It is
// distinct from a parse failure: the source was read, and what it says is
// outside what this program claims to understand.
var errUnsupportedForm = errors.New("unsupported form")
