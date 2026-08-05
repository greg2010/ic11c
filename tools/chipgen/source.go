package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// slicing is what every cut needs: the decompile it reads, the attributes it
// has dropped along the way, and the ledger the fingerprints land in. It is
// threaded rather than global so a test can run two slices over two trees,
// which is how the ledger notices a body that changed.
type slicing struct {
	root    string
	dropped map[string]bool
	ledger  *ledger
}

func newSlicing(root string) *slicing {
	return &slicing{root: root, dropped: make(map[string]bool), ledger: newLedger()}
}

// strip removes the attribute lines a declaration carries that the unit does
// not keep, recording each removed name.
func (s *slicing) strip(text string) string { return stripAttrs(s.dropped, text) }

// sourceFile is one decompiled C# file, held with its text so that every
// landmark the slicer names can be reported with the line it was found at.
// The slicer reads named files rather than walking the tree, since a walk
// would only turn a moved file into a silent absence.
type sourceFile struct {
	// rel is the path under the decompiled root, which is what a refusal
	// message names. The absolute path is an artifact of where the tree was
	// unpacked and means nothing to a reader diffing a game update.
	rel  string
	text string
	// slice is the run this file was read for, so that every landmark located
	// through it is fingerprinted without the caller having to remember to.
	slice *slicing
}

// read reads one file under the decompile root. A missing file is errNotFound
// wrapped with the path, because that is what a type moved between namespaces
// looks like from here.
func (s *slicing) read(rel string) (*sourceFile, error) {
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%s: %w", rel, errNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	return &sourceFile{rel: rel, text: strings.ReplaceAll(string(data), "\r\n", "\n"), slice: s}, nil
}

// topLevelType returns the single type the file declares, checked against
// the name the caller expected. The decompiler writes one top-level type
// per file, so a file holding two is a layout change every lift depends on,
// and is refused rather than half read.
func (f *sourceFile) topLevelType(name string) (decl, error) {
	decls, err := splitDecls(f.text)
	if err != nil {
		return decl{}, fmt.Errorf("%s: %w", f.rel, err)
	}
	var types []decl
	for _, d := range decls {
		if d.kind != declLeaf {
			types = append(types, d)
		}
	}
	switch {
	case len(types) == 0:
		return decl{}, fmt.Errorf("%s declares no type, wanted %s: %w", f.rel, name, errNotFound)
	case len(types) > 1:
		names := make([]string, len(types))
		for i, d := range types {
			names[i] = d.name
		}
		return decl{}, fmt.Errorf("%s declares %s, wanted only %s", f.rel, strings.Join(names, " and "), name)
	case types[0].name != name:
		return decl{}, fmt.Errorf("%s declares %s, wanted %s", f.rel, types[0].name, name)
	}
	if err := f.recordShape(types[0]); err != nil {
		return decl{}, err
	}
	return types[0], nil
}

// recordShape fingerprints what the type declares, which is the half of a game
// update the keep lists and the collapsed class chains are blind to: a member
// none of them names, appearing or leaving.
func (f *sourceFile) recordShape(d decl) error {
	var members []decl
	if d.kind == declContainer {
		var err error
		if members, err = splitDecls(d.body); err != nil {
			return fmt.Errorf("%s: split %s body: %w", f.rel, d.name, err)
		}
	}
	return f.slice.ledger.shape(f.rel, d, members)
}

// scope is a brace body together with where it begins in its file.
// splitDecls reports offsets into whatever body it was given, so carrying
// the body's own offset is what lets every provenance comment in the
// emitted unit name real line numbers, at any nesting depth.
type scope struct {
	file *sourceFile
	base int
	body string
}

// top is the whole file as a scope.
func (f *sourceFile) top() scope {
	return scope{file: f, base: 0, body: f.text}
}

// scopeOf is the body of a declaration this scope contains.
func (s scope) scopeOf(d decl) scope {
	return scope{file: s.file, base: s.base + d.bodyStart, body: d.body}
}

// span renders the 1-based line range a declaration of this scope occupies, for
// the provenance comment the emitted unit carries above every lifted slice.
func (s scope) span(d decl) string {
	return fmt.Sprintf("%s:%d-%d", s.file.rel, lineOf(s.file.text, s.base+d.start), lineOf(s.file.text, s.base+d.end))
}

// member finds one declaration in this scope by its normalized signature.
// Matching on the signature rather than the name alone keeps an overload
// from being lifted in place of the one asked for; what it cannot catch is
// a body that changed under an unchanged signature, which the fingerprint here is for.
func (s scope) member(signature string) (decl, error) {
	decls, err := splitDecls(s.body)
	if err != nil {
		return decl{}, err
	}
	var found []decl
	for _, d := range decls {
		if d.name == signature {
			found = append(found, d)
		}
	}
	switch len(found) {
	case 0:
		return decl{}, fmt.Errorf("member %q: %w", signature, errNotFound)
	case 1:
	default:
		return decl{}, fmt.Errorf("member %q is declared %d times", signature, len(found))
	}
	// The whole declaration is fingerprinted, not the part the caller goes on
	// to emit. Where the slice keeps one branch of a member and stands a
	// constant in for the rest, the branch it left behind is a claim about what
	// that constant is, and the claim is what a rewritten body falsifies.
	if err := s.cut(found[0].name, found[0].text); err != nil {
		return decl{}, err
	}
	return found[0], nil
}

// cut fingerprints one construct taken out of this scope's file. A scope with
// no file behind it is a declaration body handed over on its own, whose
// enclosing declaration is already fingerprinted.
func (s scope) cut(name, text string) error {
	if s.file == nil {
		return nil
	}
	return s.file.slice.ledger.cut(s.file.rel+"/"+name, text)
}

// cutTree fingerprints a declaration and everything nested inside it, so that a
// change names the member it happened in.
func (s scope) cutTree(d decl) error {
	if s.file == nil {
		return nil
	}
	return s.file.slice.ledger.cutTree(s.file.rel+"/"+d.name, d)
}

// lift renders a declaration with a provenance comment naming where it came
// from, so that a reader of the emitted unit can diff any slice against the
// decompile without running the slicer.
func (s scope) lift(d decl, note string) string {
	return provenance(s.span(d), note) + "\n" + dedent(d.text)
}

// provenance renders the comment every lifted slice carries: where it came from
// and, when there is one, why it is worth reading. A note runs to as many lines
// as it was written with.
func provenance(span, note string) string {
	head := "// verbatim: " + span
	if note == "" {
		return head
	}
	for line := range strings.SplitSeq(note, "\n") {
		head += "\n// " + line
	}
	return head
}

// dedent removes the one leading tab a nested declaration carries, so it
// lands at a known indentation and can be renested at whatever depth it is
// emitted at. splitDecls has already trimmed the first line's indentation,
// so trimming a prefix that is not there leaves it alone.
func dedent(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "\t")
	}
	return strings.Join(lines, "\n")
}

// nest renders a declaration one level in, which is where every member of an
// emitted type goes.
func nest(text string) string {
	return indent(dedent(text))
}

// cutOnce removes the single occurrence of old from src, refusing when the
// count is anything but one. Every text edit here goes through this or
// replaceExactly, so a silent no-match cannot leave a construct behind and
// a double match cannot remove something nobody looked at.
func cutOnce(src, old, what string) (string, error) {
	return replaceExactly(src, old, "", 1, what)
}

func replaceExactly(src, old, replacement string, want int, what string) (string, error) {
	got := strings.Count(src, old)
	if got != want {
		return "", fmt.Errorf("%s: expected %d occurrence(s) of %.60q, found %d", what, want, old, got)
	}
	return strings.Replace(src, old, replacement, want), nil
}
