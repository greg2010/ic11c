package llvmir

import (
	"github.com/greg2010/ic11c/internal/source"
	"tinygo.org/x/go-llvm"
)

// Positions rebuilds source positions out of the debug information a module
// carries. The zero value answers positions carrying no file and no byte
// offset.
type Positions struct {
	// File names the source. An LLVM debug location carries a line and a column
	// and no file, so it is restored from here.
	File string
	// Lines restores the byte offset a location does not carry. It may be nil,
	// which leaves every position at offset zero; ordering is unaffected either
	// way, since positions compare by line and column first.
	Lines *source.LineMap
}

// Location rebuilds the position one debug location names.
func (p Positions) Location(loc llvm.Metadata) source.Position {
	return p.Lines.Position(p.File, int(loc.LocationLine()), int(loc.LocationColumn()))
}

// Instr is the location it still carries, and whether it names a line.
//
// Both failures must be checked: the optimizer drops metadata from an
// instruction it formed, and leaves line 0 on one it could not attribute.
func (p Positions) Instr(in llvm.Value) (source.Position, bool) {
	loc := in.InstructionDebugLoc()
	if loc.IsNil() {
		return source.Position{}, false
	}
	pos := p.Location(loc)
	return pos, pos.IsValid()
}

// Func is where fn was written, read from its subprogram; a definition with
// no subprogram answers line 0, which is not a valid position.
// [Positions.File] wins over the subprogram's file, which is used only when
// File is empty.
func (p Positions) Func(fn llvm.Value) source.Position {
	pos := source.Position{File: p.File}
	sp := fn.Subprogram()
	if sp.IsNil() {
		return pos
	}
	pos.Line = int(sp.SubprogramLine())
	if pos.File == "" {
		if file := sp.ScopeFile(); !file.IsNil() {
			pos.File = file.FileFilename()
		}
	}
	return pos
}
