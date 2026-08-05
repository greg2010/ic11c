// Package llvmopt runs the mid-level optimizer over the in-memory
// module. The cost model is lines of IC10 text against [emit.MaxLines],
// which no stock LLVM pipeline knows about; no LLVM target is
// registered, and the mid-level pipeline runs against a zero TargetMachine.
package llvmopt

import (
	"context"
	"errors"
	"fmt"

	"github.com/greg2010/ic11c/internal/emit"
	"github.com/greg2010/ic11c/internal/llvmir"
	"github.com/greg2010/ic11c/internal/source"
	"tinygo.org/x/go-llvm"
)

// DefaultPipeline is the pass pipeline the compiler runs, in opt
// -passes syntax: -Oz because the budget is lines of emitted IC10, the
// pre-link spelling and trailing function(dce) tuned against that
// budget and verified by the tests in this package, not asserted here.
const DefaultPipeline = "thinlto-pre-link<Oz>,function(dce)"

// maxInstructions is the largest module the pipeline is run on: LLVM
// aborts the process on an allocation RunPasses cannot serve, with
// nothing returned to report, so the size must be refused before the
// pipeline runs. TestTheCorpusIsFarUnderTheInstructionCap keeps it honest.
const maxInstructions = 100_000

// Options configures a run.
type Options struct {
	// Pipeline overrides the pass pipeline, in opt -passes syntax. Empty
	// selects [DefaultPipeline].
	Pipeline string
	// LoopUnrolling re-enables the unrolling the shipped configuration turns
	// off. The zero value is what the compiler runs; it exists only as an
	// instrument for `task corpus:measure`, with no command-line flag of its
	// own.
	LoopUnrolling bool
}

// Run optimizes m in place. A module past [maxInstructions] is
// refused before the pipeline runs, as a [source.DiagnosticList]: a
// program too large to compile, not a defect in the compiler. ctx only
// decides whether the pipeline starts.
func Run(ctx context.Context, m llvm.Module, opts Options) error {
	if m.IsNil() {
		return errors.New("llvmopt: nil module")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("llvmopt: %w", err)
	}
	// Ahead of the verifier, which reports a malformation by printing the whole
	// module: on an oversized one that text is itself the size being refused.
	if err := checkSize(m); err != nil {
		return err
	}

	pipeline := opts.Pipeline
	if pipeline == "" {
		pipeline = DefaultPipeline
	}

	if err := llvm.VerifyModule(m, llvm.ReturnStatusAction); err != nil {
		return fmt.Errorf("llvmopt: module does not verify before optimization: %w\n%s", err, m.String())
	}

	pbo := llvm.NewPassBuilderOptions()
	defer pbo.Dispose()
	// Unrolling trades lines for speed, and lines are the budget: an unrolled
	// body costs lines proportional to the trip count, where refusing costs a
	// fixed couple of lines of loop overhead. `task corpus:measure` prints the
	// margin through [Options.LoopUnrolling]; no document states a figure.
	pbo.SetLoopUnrolling(opts.LoopUnrolling)

	var tm llvm.TargetMachine
	if err := m.RunPasses(pipeline, tm, pbo); err != nil {
		return fmt.Errorf("llvmopt: running pass pipeline %q: %w", pipeline, err)
	}

	if err := llvm.VerifyModule(m, llvm.ReturnStatusAction); err != nil {
		return fmt.Errorf("llvmopt: module does not verify after pass pipeline %q: %w\n%s", pipeline, err, m.String())
	}
	return nil
}

// checkSize refuses a module past [maxInstructions] as a
// [source.DiagnosticList], since the program is what is oversized, not
// the compiler. It is positioned at the function holding the most
// instructions, the narrowest honest subject.
func checkSize(m llvm.Module) error {
	var total, most int
	var largest llvm.Value
	for fn := m.FirstFunction(); !fn.IsNil(); fn = llvm.NextFunction(fn) {
		n := 0
		for range llvmir.FuncInstrs(fn) {
			n++
		}
		total += n
		if n > most {
			most, largest = n, fn
		}
	}
	if total <= maxInstructions {
		return nil
	}

	// The share is left off a module that is one function, where naming it
	// twice says nothing.
	var share string
	if most < total {
		share = fmt.Sprintf(", and this function holds %d of them", most)
	}
	// No source path reaches this package, so the position's file is the one
	// irgen recorded as the subprogram's, and a definition carrying no debug
	// information is left at no position at all.
	var diags source.DiagnosticList
	diags.Addf(llvmir.Positions{}.Func(largest),
		"this program generates %s before optimization, past the %d the optimizer is run on%s: LLVM ends the process rather than returning when it runs out of memory, so a module this size is refused before it reaches the optimizer — the chip takes %d lines, and nothing near this size compiles to a program that fits",
		source.Plural(total, "instruction"), maxInstructions, share, emit.MaxLines)
	return diags.Err()
}
