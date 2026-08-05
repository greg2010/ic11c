// Package emit turns machine IR into IC10 assembly text and accounts for its size.
//
// The chip allows at most 128 lines, 4096 bytes, and 90 characters per line, and executes at most
// 128 instructions per 500 ms tick. Its 512 memory slots are shared between the data region and the
// hardware call stack, growing toward each other with nothing in between. Writing past the top
// through poke, push, or put raises StackOverFlow; reading past it through get raises Unknown,
// because _GET_Operation does not wrap ReadMemory in a try.
package emit

import (
	"errors"
	"fmt"
	"strings"

	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/source"
)

// Options configures emission.
type Options struct {
	// Readable names every block and every branch target in a trailing comment, on the line the
	// block starts at and on the branch itself. The program is unchanged: the chip cuts a line at
	// its first '#' before splitting it, so the annotation costs only bytes and line width, never a
	// line or an instruction. A line pushed past 90 characters by the annotation is not a compile
	// failure; the editor's paste truncates the comment, which Report.TruncatedComments counts.
	Readable bool
	// Numeric emits the integer behind every logic type, slot type, batch mode and reagent mode
	// instead of its name. A name and its integer resolve identically on the chip and cost the same
	// line, so this only trades bytes against legibility — an escape hatch for a program of long
	// lines that meets the byte cap before the line cap. TestCorpusMeasurements in cmd/ic11c checks that the substitution never changes a line count.
	Numeric bool
	// Slots is the memory layout the earlier stages decided. It is carried
	// through to the report rather than derived here: no emitted line names the
	// boundary between the data region and the call frames.
	Slots SlotReport
}

// Output is the emitted program and the accounting for it.
type Output struct {
	// Text is the assembly, lines joined by newlines with no trailing newline.
	// It is empty only for a program whose functions hold no instructions.
	Text string
	// Report describes Text against the target's limits, whether or not it
	// exceeds them.
	Report Report
}

// Emit renders prog as IC10 assembly. It returns an error only for a program that cannot be emitted
// at all: one that fails mir.Program.Validate, one still holding virtual registers, or one branching
// to a label that names no block. Exceeding a target limit is not an error; it appears in
// Output.Report. prog is left unmodified, which the report depends on: attribution reads each
// instruction's call chain outermost first, the opposite of the order it is stored in.
func Emit(prog *mir.Program, opts Options) (Output, error) {
	// Validate accepts a nil receiver and answers "program has no functions",
	// which sends a reader looking for the stage that dropped them. Only the
	// message differs.
	if prog == nil {
		return Output{}, errors.New("emit: program is nil")
	}
	if err := prog.Validate(); err != nil {
		return Output{}, fmt.Errorf("emit: %w", err)
	}
	lineOf, count := resolveLabels(prog)
	render := renderer{
		numeric: opts.Numeric,
		lineOf:  lineOf,
		count:   count,
	}
	// Nothing but the annotation reads a mangled name and only this form
	// annotates, so the default form computes none rather than a name per block
	// that no line will spell.
	if opts.Readable {
		render.names = mangleLabels(prog)
	}
	lines, funcs, err := layout(prog, render, opts.Readable)
	if err != nil {
		return Output{}, err
	}
	texts := make([]string, len(lines))
	for i, l := range lines {
		texts[i] = l.text
	}
	return Output{
		Text:   strings.Join(texts, "\n"),
		Report: buildReport(lines, funcs, opts.Slots),
	}, nil
}

// line is one emitted line and the source it is attributed to.
type line struct {
	text string
	pos  source.Position
	// fn names the emitted function the line landed in, and inline the calls
	// its code was spliced through, innermost first. Together they are the unit
	// byte accounting attributes to.
	fn     string
	inline []source.InlineSite
	// fnOrdinal is the function's position in the program, which is what groups
	// lines into a construct. The name cannot: mir.Program.Validate accepts two
	// functions of one name, and grouping by name would give them a single site
	// row whose bytes match neither function's own.
	fnOrdinal int
}

// resolveLabels maps every block label to the line a branch to it should reach, and returns how
// many lines the program emits. One pass is exact: each instruction occupies exactly one line, and a
// trailing empty block resolves one past the end, which is deliberate — ProgrammableChip.Execute
// stops without raising once _NextAddr reaches _LinesOfCode.Count, which is what a function
// returning to nothing means on this target.
func resolveLabels(prog *mir.Program) (lineOf map[string]int, lines int) {
	lineOf = make(map[string]int)
	for _, fn := range prog.Funcs {
		for _, block := range fn.Blocks {
			lineOf[block.Label] = lines
			lines += len(block.Instrs)
		}
	}
	return lineOf, lines
}

func layout(prog *mir.Program, render renderer, readable bool) ([]line, []FuncReport, error) {
	var lines []line
	funcs := make([]FuncReport, 0, len(prog.Funcs))
	// Block names still waiting for the line they start at; a block holding no instructions starts at
	// whatever runs next. Names still waiting when the program ends are dropped here and named on the
	// branch that reaches them instead, which renderer.annotate marks.
	var starting []string
	for ordinal, fn := range prog.Funcs {
		start := len(lines)
		for _, block := range fn.Blocks {
			if readable {
				name, err := render.name(block.Label)
				if err != nil {
					return nil, nil, fmt.Errorf("emit: %s at %s: %w", fn.Name, block.Pos, err)
				}
				starting = append(starting, name)
			}
			for _, instr := range block.Instrs {
				text, err := render.instr(instr)
				if err != nil {
					return nil, nil, fmt.Errorf("emit: %s at %s: %w", fn.Name, instr.Pos, err)
				}
				if readable {
					text, err = render.annotate(text, starting, instr)
					if err != nil {
						return nil, nil, fmt.Errorf("emit: %s at %s: %w", fn.Name, instr.Pos, err)
					}
					starting = nil
				}
				lines = append(lines, line{text: text, pos: instr.Pos, fn: fn.Name, inline: instr.Inline, fnOrdinal: ordinal})
			}
		}
		funcs = append(funcs, FuncReport{
			Name:      fn.Name,
			Pos:       fn.Pos,
			Lines:     len(lines) - start,
			FirstLine: start,
		})
	}
	return lines, funcs, nil
}
