// Package emit turns machine IR into IC10 assembly text and accounts for its
// size.
//
// Three limits apply: 4096 bytes, 128 lines, and 90 characters on a line. The
// two budgets balance at 32 bytes a line, counting the separator the editor
// charges after each, and most programs sit well under that and run out of
// lines first. The report states what the program spends against all three and
// names the size budget that binds from the numbers rather than assuming; the
// line width is reported and not ranked, because it bounds one line's
// formatting rather than how much program fits. It also accounts for the 512
// slot memory array, which
// no emitted line reveals. An address past the end of that array faults; what
// goes untrapped is a call frame growing down through it into the data region,
// overwriting a global with nothing to say so.
//
// Attribution is by construct, not by function. Calls are inlined by default,
// so a function called from three places pays for itself three times and a
// per-function total hides which of the three to delete; the unit is the inline
// site. Output that exceeds a limit is still returned with its report, because
// a refusal would withhold exactly the information needed to get under budget.
//
// Nothing this package emits is a comment, a blank line, an alias or a define.
// Each costs bytes and an execution slot against a 128 instruction tick, and a
// blank line counts against both the line limit and the tick budget.
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
	// Readable emits a label definition line per block with symbolic branch
	// targets, instead of resolving every target to a line number.
	//
	// The result is a debugging view, not the shipped program: a label line is
	// a line, so it changes the numbering and both budgets, and its report
	// describes itself rather than the default output. What it costs a program
	// is lines, which is the budget that binds.
	Readable bool
	// Numeric emits the integer behind every logic type, slot type, batch mode
	// and reagent mode instead of its name.
	//
	// A name and its integer resolve identically on the chip and occupy one
	// operand either way, so the choice is bytes against legibility with no
	// line in it. Names are the default because lines are what bind: every
	// corpus fixture spends a larger share of the line limit than of the byte
	// budget, which is the relation cmd/ic11c's corpus test holds the documents
	// to. docs/compiler.md carries the measured figures. This is the escape
	// hatch for a program that does run out of bytes.
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

// Emit renders prog as IC10 assembly.
//
// It returns an error only for a program that cannot be emitted at all: one
// that fails mir.Program.Validate, one still holding virtual registers, one
// branching to a label that names no block. Exceeding a target limit is not an
// error; it appears in Output.Report.
func Emit(prog *mir.Program, opts Options) (Output, error) {
	if prog == nil {
		return Output{}, errors.New("emit: program is nil")
	}
	if err := prog.Validate(); err != nil {
		return Output{}, fmt.Errorf("emit: %w", err)
	}
	render := renderer{
		readable: opts.Readable,
		numeric:  opts.Numeric,
		lineOf:   resolveLabels(prog),
	}
	if opts.Readable {
		render.names = mangleLabels(prog)
	}
	lines, funcs, err := layout(prog, render)
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

// resolveLabels maps every block label to the line a branch to it should
// reach.
//
// One pass is exact. Each instruction occupies exactly one line, so nothing a
// later pass renders can change a line number: the width of a resolved target
// affects the byte count only. A block with no instructions resolves to the
// next block's first line, and a trailing empty block resolves one past the
// end.
//
// Readable output numbers its own label lines differently and does not care: a
// symbolic target is the block's name, and the map serves only to reject a
// label naming no block.
func resolveLabels(prog *mir.Program) map[string]int {
	lineOf := make(map[string]int)
	next := 0
	for _, fn := range prog.Funcs {
		for _, block := range fn.Blocks {
			lineOf[block.Label] = next
			next += len(block.Instrs)
		}
	}
	return lineOf
}

func layout(prog *mir.Program, render renderer) ([]line, []FuncReport, error) {
	var lines []line
	funcs := make([]FuncReport, 0, len(prog.Funcs))
	for ordinal, fn := range prog.Funcs {
		start := len(lines)
		for _, block := range fn.Blocks {
			if render.readable {
				name, err := render.name(block.Label)
				if err != nil {
					return nil, nil, fmt.Errorf("emit: %s at %s: %w", fn.Name, block.Pos, err)
				}
				lines = append(lines, line{text: name + ":", pos: block.Pos, fn: fn.Name, fnOrdinal: ordinal})
			}
			for _, instr := range block.Instrs {
				text, err := render.instr(instr)
				if err != nil {
					return nil, nil, fmt.Errorf("emit: %s at %s: %w", fn.Name, instr.Pos, err)
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
