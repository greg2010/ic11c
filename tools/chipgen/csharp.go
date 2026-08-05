package main

import (
	"errors"
	"fmt"
	"strings"
)

// errNotFound reports that a construct the slicer depends on is absent from
// the decompiled source. The slicer never proceeds past one: a missing
// landmark means the game moved something, and a unit assembled around the
// gap would be wrong in a way nothing downstream could notice.
var errNotFound = errors.New("not found")

// skipLiteral advances past the C# lexical element starting at i — a
// comment, string, character, verbatim, or interpolated literal — and
// reports the index just past it (or i for ordinary code). Every brace
// match below goes through this, so punctuation inside a literal (e.g. $"d{int.MaxValue}") never moves the nesting depth.
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
// quote is an escaped quote.
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

// declKind classifies a declaration. Only the distinction that changes how a
// declaration is treated is drawn: a container has a body worth descending
// into, an enum is taken whole, and everything else is a leaf.
type declKind int

const (
	declLeaf declKind = iota
	declContainer
	declEnum
)

// decl is one C# declaration and the span of source it occupies. start and
// end are byte offsets into the body it was split from, not the file; span
// converts to file line numbers for the provenance comments in the emitted unit.
type decl struct {
	kind declKind
	// keyword is the C# keyword introducing a type declaration, empty for a
	// leaf. class and struct differ in what they may inherit, which is why the
	// distinction survives past classification.
	keyword string
	// name is the declared name for a type, and the normalized signature for a
	// leaf member. A leaf is named by its signature so that two overloads of
	// one method are distinguishable and a changed signature reads as a
	// different declaration.
	name string
	// bases is the base list as written, split on top-level commas. Empty when
	// the declaration has none.
	bases []string
	// text is the whole declaration, header and attributes included.
	text string
	// body is the brace block a container declares, empty otherwise.
	body string
	// bodyStart is the offset of body within the enclosing text passed to
	// splitDecls, so that an edit to a nested member can be located in the
	// original.
	start, bodyStart, end int
}

var typeKeywords = map[string]declKind{
	"class":     declContainer,
	"struct":    declContainer,
	"interface": declContainer,
	"record":    declContainer,
	"enum":      declEnum,
}

// splitDecls splits one C# brace body, or a whole file, into the
// declarations it contains, leaving nested bodies intact for the caller to
// recurse into. The decompiler emits one statement per declaration with
// braces on their own lines and no preprocessor directives, so brace/semicolon counting is enough.
func splitDecls(body string) ([]decl, error) {
	var (
		decls     []decl
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
		d := decl{
			kind:    head.kind,
			keyword: head.keyword,
			name:    head.name,
			bases:   head.bases,
			text:    text,
			start:   start + strings.Index(body[start:end], strings.TrimLeft(body[start:end], " \t\r\n")),
			end:     end,
		}
		if head.kind == declContainer && bodyStart >= 0 {
			d.body = body[bodyStart:bodyEnd]
			d.bodyStart = bodyStart
		}
		decls = append(decls, d)
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

func byteAt(s string, i int) byte {
	if i < 0 || i >= len(s) {
		return 0
	}
	return s[i]
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

type declHead struct {
	kind    declKind
	keyword string
	name    string
	bases   []string
}

// classifyDecl reads a declaration header into what names it and what it
// inherits.
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
		name, nameEnd, ok := nextIdent(stripped, next)
		if !ok {
			return declHead{}, fmt.Errorf("type declaration %.60q names nothing", stripped)
		}
		return declHead{kind: kind, keyword: word, name: name, bases: baseList(stripped[nameEnd:])}, nil
	}
	name := normalizeHeader(stripped)
	if name == "" {
		return declHead{}, fmt.Errorf("declaration %.60q has no header", header)
	}
	return declHead{kind: declLeaf, name: name}, nil
}

// baseList reads the base list from the remainder of a type header, which is
// everything between the first top-level colon and the end or a where clause.
func baseList(rest string) []string {
	depth := 0
	colon := -1
	for i := 0; i < len(rest); i++ {
		if j := skipLiteral(rest, i); j != i {
			i = j - 1
			continue
		}
		switch rest[i] {
		case '<', '(', '[':
			depth++
		case '>', ')', ']':
			depth--
		case ':':
			if depth == 0 {
				colon = i
			}
		}
		if colon >= 0 {
			break
		}
	}
	if colon < 0 {
		return nil
	}
	list := rest[colon+1:]
	if where := strings.Index(list, " where "); where >= 0 {
		list = list[:where]
	}
	var bases []string
	for _, part := range splitTop(list, ',') {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			bases = append(bases, trimmed)
		}
	}
	return bases
}

// splitTop splits s on the sep byte, ignoring separators nested inside
// brackets or literals.
func splitTop(s string, sep byte) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		if j := skipLiteral(s, i); j != i {
			i = j - 1
			continue
		}
		switch s[i] {
		case '(', '[', '<', '{':
			depth++
		case ')', ']', '>', '}':
			depth--
		case sep:
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// normalizeHeader reduces a stripped declaration header to the stable part
// of its signature: initializer and terminator dropped, whitespace
// collapsed. An expression body is cut with the initializer, so a member is
// named by what it is, not by what it computes — some run to hundreds of characters.
func normalizeHeader(header string) string {
	if cut := topLevelArrow(header); cut >= 0 {
		header = header[:cut]
	}
	if cut := topLevelAssign(header); cut >= 0 {
		header = header[:cut]
	}
	return strings.TrimSuffix(strings.Join(strings.Fields(header), " "), ";")
}

// topLevelArrow returns the offset of the => that opens an expression body, or
// -1 when there is none. A lambda's arrow is nested inside the parameter list
// or an initializer, so depth keeps the two apart.
func topLevelArrow(header string) int {
	depth := 0
	for i := 0; i < len(header); i++ {
		if j := skipLiteral(header, i); j != i {
			i = j - 1
			continue
		}
		switch header[i] {
		case '(', '[', '<':
			depth++
		case ')', ']', '>':
			if byteAt(header, i-1) == '=' {
				continue
			}
			depth--
		case '=':
			if depth == 0 && byteAt(header, i+1) == '>' {
				return i
			}
		}
	}
	return -1
}

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

// stripAttributes drops the bracketed attribute sections a declaration opens
// with. An attribute is not part of what names a declaration, and the
// decompiler writes each on its own line ahead of the header.
func stripAttributes(header string) (string, error) {
	for {
		header = strings.TrimLeft(header, " \t\r\n")
		if !strings.HasPrefix(header, "[") {
			return header, nil
		}
		depth := 0
		end := -1
		for i := 0; i < len(header); i++ {
			if j := skipLiteral(header, i); j != i {
				i = j - 1
				continue
			}
			switch header[i] {
			case '[':
				depth++
			case ']':
				depth--
				if depth == 0 {
					end = i + 1
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			return "", fmt.Errorf("attribute section in %.60q is unclosed", header)
		}
		header = header[end:]
	}
}

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

func isIdentByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func isIdentStart(s string, i int) bool {
	if !isIdentByte(s[i]) || (s[i] >= '0' && s[i] <= '9') {
		return false
	}
	return i == 0 || !isIdentByte(s[i-1])
}

func identAt(s string, i int) (string, int) {
	end := i
	for end < len(s) && isIdentByte(s[end]) {
		end++
	}
	return s[i:end], end
}

// nextIdent returns the first identifier at or after i and the offset just
// past it.
func nextIdent(s string, i int) (name string, end int, ok bool) {
	for ; i < len(s); i++ {
		if j := skipLiteral(s, i); j != i {
			i = j - 1
			continue
		}
		if isIdentStart(s, i) {
			name, end = identAt(s, i)
			return name, end, true
		}
	}
	return "", 0, false
}

// lineOf reports the 1-based line number of a byte offset in src.
func lineOf(src string, off int) int {
	if off > len(src) {
		off = len(src)
	}
	return 1 + strings.Count(src[:off], "\n")
}
