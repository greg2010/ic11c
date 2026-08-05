// Package source locates and reports problems in MicroC source text. It
// holds the position and diagnostic types every stage reports against,
// from lexing through instruction selection, and depends on nothing else
// in the tree.
package source

import (
	"cmp"
	"strconv"
	"strings"
)

// Position identifies one byte of one source file. The zero value is invalid.
//
// Column counts bytes rather than runes, so a position inside a multi-byte
// rune in a string literal names the byte, not the character.
type Position struct {
	// File names the source. It is carried for diagnostic rendering and is
	// never opened.
	File string
	// Offset is the 0-based byte offset from the start of the file.
	Offset int
	// Line is the 1-based line number.
	Line int
	// Column is the 1-based byte offset from the start of the line.
	Column int
}

// IsValid reports whether p refers to a real source location.
func (p Position) IsValid() bool { return p.Line > 0 }

// Compare orders two positions in reading order. Line and column decide
// it, not Offset: a position rebuilt from an LLVM debug location carries
// only those two, so ordering on Offset alone would put every backend
// diagnostic ahead of every front-end one.
func (p Position) Compare(q Position) int {
	if c := cmp.Compare(p.Line, q.Line); c != 0 {
		return c
	}
	if c := cmp.Compare(p.Column, q.Column); c != 0 {
		return c
	}
	return cmp.Compare(p.Offset, q.Offset)
}

// LineCol reduces p to the two fields that survive a round trip through LLVM.
// It is the key a stage running after the optimizer can look a front-end fact
// up by.
func (p Position) LineCol() LineCol { return LineCol{Line: p.Line, Column: p.Column} }

// LineCol identifies a position by line and column alone.
type LineCol struct {
	// Line is the 1-based line number.
	Line int
	// Column is the 1-based byte offset from the start of the line.
	Column int
}

// InlineSite is one call whose body was spliced into its caller. Calls
// are inlined by default, so a function called from three places
// contributes its bytes three times; attribution keyed on the call site
// is what a size report can use to tell them apart.
type InlineSite struct {
	// Pos is the call expression, in the caller.
	Pos Position
	// Callee is the function whose body was spliced in there.
	Callee string
}

// String renders the site as "callee inlined at position". A site with
// no valid position is one the optimizer merged: two identical expansions
// became one, and the merged location names no line. Naming that beats
// picking an arbitrary line between the two.
func (s InlineSite) String() string {
	name := s.Callee
	if name == "" {
		name = "a call"
	}
	if !s.Pos.IsValid() {
		return name + " inlined at a site the optimizer merged with another"
	}
	return name + " inlined at " + s.Pos.String()
}

// LineMap gives the byte offset of a line and column in one source file,
// restoring what an LLVM debug location does not carry. The zero value
// is unusable; a nil *LineMap answers zero for everything, which is what
// a stage with no source text gets.
type LineMap struct {
	// starts holds the byte offset each line begins at, indexed by line-1.
	starts []int
	// size is the length of the source, which bounds the last line.
	size int
}

// NewLineMap indexes the start of every line in src.
func NewLineMap(src string) *LineMap {
	starts := make([]int, 1, strings.Count(src, "\n")+1)
	for i := range len(src) {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &LineMap{starts: starts, size: len(src)}
}

// Position rebuilds a full position from what an LLVM debug location
// carries: a line and a column, neither a file nor a byte offset. The file
// comes from the caller and the offset from m; a nil m answers offset zero.
func (m *LineMap) Position(file string, line, column int) Position {
	return Position{
		File:   file,
		Offset: m.Offset(line, column),
		Line:   line,
		Column: column,
	}
}

// Offset gives the byte offset of a 1-based line and column, or zero
// when the position is outside the file — also the offset of the file's
// first byte, so callers treat zero as "unknown". A column past a
// line's end answers zero, not an offset into the next line.
func (m *LineMap) Offset(line, column int) int {
	if m == nil || line < 1 || line > len(m.starts) || column < 1 {
		return 0
	}
	last := m.size
	if line < len(m.starts) {
		// starts[line] begins the next line, so the byte before it terminates
		// this one.
		last = m.starts[line] - 1
	}
	offset := m.starts[line-1] + column - 1
	if offset > last {
		return 0
	}
	return offset
}

// String renders p as "file:line:column", dropping the file when it is empty.
// An invalid position renders as "-".
func (p Position) String() string {
	if !p.IsValid() {
		return "-"
	}
	s := p.File
	if s != "" {
		s += ":"
	}
	s += strconv.Itoa(p.Line) + ":" + strconv.Itoa(p.Column)
	return s
}
