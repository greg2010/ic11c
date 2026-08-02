// Package source locates and reports problems in MicroC source text.
//
// It holds the position and diagnostic types every stage reports against, from
// lexing through instruction selection. It depends on nothing, so no stage has
// to import another merely to name a source location.
package source

import (
	"cmp"
	"strconv"
	"strings"
)

// Position identifies one byte of one source file. The zero value is invalid.
//
// Column counts bytes rather than runes, so a position inside a multi-byte rune
// in a string literal names the byte. Every AST node carries a Position, and
// those positions become LLVM debug locations, so a backend rejection can name
// the source line responsible.
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

// Compare orders two positions in reading order.
//
// Line and column decide it, not Offset. A position rebuilt from an LLVM debug
// location carries only those two, so ordering on Offset alone would sort every
// backend diagnostic ahead of every front-end one. Offset breaks the remaining
// ties.
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

// InlineSite is one call whose body was spliced into its caller.
//
// Calls are inlined by default, so a function called from three places
// contributes its bytes three times and a total per function says nothing about
// which of the three to cut. Attribution keyed on the site is what tells them
// apart.
type InlineSite struct {
	// Pos is the call expression, in the caller.
	Pos Position
	// Callee is the function whose body was spliced in there.
	Callee string
}

// String renders the site as "callee inlined at position".
//
// A site with no valid position is one the optimizer merged: two expansions of
// one callee that produced identical code become a single sequence, and the
// location LLVM gives it names no line. Saying so is better than naming a line
// that was chosen arbitrarily between the two.
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

// LineMap gives the byte offset of a line and column in one source file.
//
// An LLVM debug location carries a line and a column and no offset, so a
// position rebuilt from one downstream has none either. This restores it, which
// keeps Position a complete value for anything that indexes the source text by
// it. The zero value is unusable; a nil *LineMap answers zero for everything,
// which is what a stage with no source text in hand gets.
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

// Position rebuilds a full position from what an LLVM debug location carries.
//
// A location holds a line and a column and neither the file nor the byte
// offset, so the file is supplied by the caller and the offset comes from m. A
// nil m answers offset zero, which is the position a stage holding no source
// text gets.
func (m *LineMap) Position(file string, line, column int) Position {
	return Position{
		File:   file,
		Offset: m.Offset(line, column),
		Line:   line,
		Column: column,
	}
}

// Offset gives the byte offset of a 1-based line and column, or zero for a
// position outside the file. Zero is also the offset of the file's first byte,
// which is why callers treat it as "unknown" rather than branching on it.
//
// The column is bounded by the line it names, so a column past the end of a
// line answers zero rather than an offset inside the next one. The line's own
// terminator is in bounds: a position may name the end of a line.
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
