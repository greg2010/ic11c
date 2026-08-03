package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// declKind classifies a declaration recovered from decompiled C#. Only the
// distinction that changes how a declaration is walked is drawn: a container is
// descended into, an enum is not, and everything else is a leaf.
type declKind int

const (
	declLeaf declKind = iota
	declContainer
	declEnum
)

// csharpDecl is one declaration and the source it spans.
type csharpDecl struct {
	kind declKind
	// name is the declaration's path segment: the declared name for a
	// container or an enum, and the normalized header for a leaf, so that a
	// changed signature reads as one declaration replacing another rather than
	// as the same declaration with a different fingerprint.
	name string
	// text is the whole declaration, header included, and is what is hashed.
	text string
	// body is the brace block a container declares, empty otherwise.
	body string
}

// typeKeywords are the C# keywords that introduce a type declaration. All of
// them are matched: a declaration the set misses is read as a member and never
// descended into, which loses everything nested inside it silently.
var typeKeywords = map[string]declKind{
	"class":     declContainer,
	"struct":    declContainer,
	"interface": declContainer,
	"record":    declContainer,
	"enum":      declEnum,
}

// splitDecls splits one C# brace body, or a whole file, into the declarations
// it contains. Nested bodies are left intact for the caller to recurse into.
//
// The decompiler emits one statement per declaration with braces on their own
// lines and no preprocessor directives, so brace and semicolon counting is
// enough; skipLiteral keeps punctuation inside strings, characters and comments
// from moving the depth. Anything the scan cannot account for is an error
// rather than a silently dropped declaration.
func splitDecls(body string) ([]csharpDecl, error) {
	var (
		decls     []csharpDecl
		depth     int
		start     int
		headerEnd = -1
		bodyStart = -1
		bodyEnd   = -1
	)
	reset := func(next int) {
		start, headerEnd, bodyStart, bodyEnd = next, -1, -1, -1
	}
	flush := func(end int) error {
		text := strings.TrimSpace(body[start:end])
		if text == "" {
			return nil
		}
		header := text
		if headerEnd >= 0 {
			header = body[start:headerEnd]
		}
		head, err := classifyDecl(header)
		if err != nil {
			return err
		}
		decl := csharpDecl{kind: head.kind, name: head.name, text: text}
		if head.kind == declContainer && bodyStart >= 0 {
			decl.body = body[bodyStart:bodyEnd]
		}
		decls = append(decls, decl)
		return nil
	}

	for i := 0; i < len(body); i++ {
		if j := skipLiteral(body, i); j != i {
			i = j - 1
			continue
		}
		switch body[i] {
		case '{':
			if depth == 0 {
				headerEnd, bodyStart = i, i+1
			}
			depth++
		case '}':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("closing brace at offset %d has no opener", i)
			}
			if depth > 0 {
				continue
			}
			bodyEnd = i
			// A brace block followed by a terminator or an assignment is an
			// initializer or an auto-property default, not the end of the
			// declaration, so the header claimed above is withdrawn and the
			// declaration runs on to its semicolon.
			if next := nextCodeByte(body, i+1); next == ';' || next == ',' || next == '=' {
				headerEnd, bodyStart, bodyEnd = -1, -1, -1
				continue
			}
			if err := flush(i + 1); err != nil {
				return nil, err
			}
			reset(i + 1)
		case ';':
			if depth > 0 {
				continue
			}
			if err := flush(i + 1); err != nil {
				return nil, err
			}
			reset(i + 1)
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("%d brace block(s) left unclosed", depth)
	}
	if rest := strings.TrimSpace(body[start:]); rest != "" {
		return nil, fmt.Errorf("trailing text is not a declaration: %.60q", rest)
	}
	return decls, nil
}

// nextCodeByte returns the first byte at or after i that is neither whitespace
// nor part of a comment, or zero at the end of the source.
func nextCodeByte(src string, i int) byte {
	for i < len(src) {
		if src[i] == '/' && (byteAt(src, i+1) == '/' || byteAt(src, i+1) == '*') {
			i = skipLiteral(src, i)
			continue
		}
		if !isSpace(src[i]) {
			return src[i]
		}
		i++
	}
	return 0
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

// declHead is what a declaration's header says about it: what the declaration
// is, the path segment it contributes, and the C# keyword a type declaration is
// introduced with. The keyword is empty for a leaf, and the four type keywords
// do not agree on what they can be a base of.
type declHead struct {
	kind    declKind
	name    string
	keyword string
}

// classifyDecl reads a declaration header.
func classifyDecl(header string) (declHead, error) {
	stripped, err := stripAttributes(stripLeadingComments(header))
	if err != nil {
		return declHead{}, err
	}
	for i := 0; i < len(stripped); i++ {
		if j := skipLiteral(stripped, i); j != i {
			i = j - 1
			continue
		}
		// A type keyword can only precede the parameter list of a method, so
		// stopping here keeps a `where T : class` constraint from being read as
		// a nested type.
		if stripped[i] == '(' {
			break
		}
		if !isIdentStart(stripped, i) {
			continue
		}
		word, next := identAt(stripped, i)
		kind, isType := typeKeywords[word]
		if !isType {
			i = next - 1
			continue
		}
		name, ok := nextIdent(stripped, next)
		if !ok {
			return declHead{}, fmt.Errorf("type declaration %.60q names nothing", stripped)
		}
		return declHead{kind: kind, name: name, keyword: word}, nil
	}
	name := normalizeHeader(stripped)
	if name == "" {
		return declHead{}, fmt.Errorf("declaration %.60q has no header", header)
	}
	return declHead{kind: declLeaf, name: name}, nil
}

// normalizeHeader reduces a declaration header, already stripped of its
// comments and attribute sections, to the stable part of its signature: any
// initializer and terminator are dropped and the layout whitespace is
// collapsed.
func normalizeHeader(header string) string {
	if cut := topLevelAssign(header); cut >= 0 {
		header = header[:cut]
	}
	return strings.TrimSuffix(strings.Join(strings.Fields(header), " "), ";")
}

// stripLeadingComments drops the comments and whitespace a declaration opens
// with. The decompiler writes an IL offset note ahead of some statements, and
// that note is part of what the declaration hashes to but not part of what
// names it.
func stripLeadingComments(header string) string {
	for {
		header = strings.TrimLeft(header, " \t\r\n")
		if !strings.HasPrefix(header, "//") && !strings.HasPrefix(header, "/*") {
			return header
		}
		end := skipLiteral(header, 0)
		if end == 0 {
			return header
		}
		header = header[end:]
	}
}

// topLevelAssign returns the offset of the `=` that opens an initializer, or -1
// when the header has none. Comparison and lambda operators are not
// initializers and are stepped over.
func topLevelAssign(header string) int {
	depth := 0
	for i := 0; i < len(header); i++ {
		if j := skipLiteral(header, i); j != i {
			i = j - 1
			continue
		}
		switch header[i] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case '=':
			if depth != 0 || byteAt(header, i+1) == '=' || byteAt(header, i+1) == '>' {
				continue
			}
			if prev := byteAt(header, i-1); strings.IndexByte("=!<>+-*/%&|^", prev) >= 0 {
				continue
			}
			return i
		}
	}
	return -1
}

func isIdentStart(s string, i int) bool {
	if !isIdentByte(s[i]) || (s[i] >= '0' && s[i] <= '9') {
		return false
	}
	return i == 0 || !isIdentByte(s[i-1])
}

// identAt returns the identifier starting at i and the offset just past it.
func identAt(s string, i int) (string, int) {
	end := i
	for end < len(s) && isIdentByte(s[end]) {
		end++
	}
	return s[i:end], end
}

// nextIdent returns the first identifier at or after i.
func nextIdent(s string, i int) (string, bool) {
	for ; i < len(s); i++ {
		if j := skipLiteral(s, i); j != i {
			i = j - 1
			continue
		}
		if isIdentStart(s, i) {
			name, _ := identAt(s, i)
			return name, true
		}
	}
	return "", false
}

// digestBytes is how much of the SHA-256 each record keeps. The digest is a
// change detector a human reads in a diff, not a security boundary, so eight
// bytes is enough to make an accidental collision across a few thousand
// declarations implausible while keeping a line short.
const digestBytes = 8

// declDigest fingerprints a declaration. Layout whitespace is removed first, so
// that moving a declaration to a different nesting depth does not change what it
// hashes to.
func declDigest(text string) string {
	var normalized strings.Builder
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		normalized.WriteString(line)
		normalized.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(normalized.String()))
	return hex.EncodeToString(sum[:digestBytes])
}

// declRecord is one line of the digest: a declaration's fingerprint and the
// path that names it within its top-level type.
type declRecord struct {
	Path   string
	Digest string
}

// walkDecls appends a record for every declaration under body, descending into
// nested types. prefix is the path of the declaration body belongs to, empty at
// the top of a type.
//
// An enum is recorded whole and not descended into: its members carry ordinals,
// and records sorted by name would hide a reordering, which is the change most
// worth seeing.
func walkDecls(prefix, body string, out *[]declRecord) error {
	decls, err := splitDecls(body)
	if err != nil {
		if prefix == "" {
			return err
		}
		return fmt.Errorf("%s: %w", prefix, err)
	}
	for _, decl := range decls {
		path := decl.name
		if prefix != "" {
			path = prefix + "/" + decl.name
		}
		*out = append(*out, declRecord{Path: path, Digest: declDigest(decl.text)})
		if decl.kind != declContainer {
			continue
		}
		if err := walkDecls(path, decl.body, out); err != nil {
			return err
		}
	}
	return nil
}

// topLevelType returns the single type a decompiled file declares, checked
// against the name the caller expected.
//
// The decompiler writes one file per top-level type, so a file holding two is a
// change in the layout the whole extraction depends on and is reported rather
// than half read.
func topLevelType(src, name string) (csharpDecl, error) {
	decls, err := splitDecls(src)
	if err != nil {
		return csharpDecl{}, fmt.Errorf("split %s: %w", name, err)
	}
	var types []csharpDecl
	for _, decl := range decls {
		if decl.kind != declLeaf {
			types = append(types, decl)
		}
	}
	switch {
	case len(types) == 0:
		return csharpDecl{}, fmt.Errorf("file for type %s declares no type: %w", name, errNotFound)
	case len(types) > 1:
		names := make([]string, len(types))
		for i, decl := range types {
			names[i] = decl.name
		}
		return csharpDecl{}, fmt.Errorf("file for type %s declares %s", name, strings.Join(names, " and "))
	case types[0].name != name:
		return csharpDecl{}, fmt.Errorf("file for type %s declares %s instead", name, types[0].name)
	}
	return types[0], nil
}
