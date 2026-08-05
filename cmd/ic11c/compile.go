package main

import (
	"context"
	"fmt"

	"github.com/greg2010/ic11c/internal/emit"
	"github.com/greg2010/ic11c/internal/frames"
	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/irgen"
	"github.com/greg2010/ic11c/internal/isel"
	"github.com/greg2010/ic11c/internal/llvmopt"
	"github.com/greg2010/ic11c/internal/mir"
	"github.com/greg2010/ic11c/internal/peephole"
	"github.com/greg2010/ic11c/internal/pointers"
	"github.com/greg2010/ic11c/internal/regalloc"
	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/source"
	"github.com/greg2010/ic11c/internal/tsparse"
)

// options are the knobs the command exposes to the pipeline.
type options struct {
	readable bool
	// numeric emits the integer behind every machine name rather than the
	// name, the smallest form the chip accepts.
	numeric bool
	// skipOptimizer leaves the module as IR generation built it, for
	// comparison against the optimized form when isolating a defect to the
	// optimizer or the backend.
	skipOptimizer bool
	// pipeline, loopUnrolling and outOfLineCallSites carry no command-line
	// flag: they exist only so a test can recompute a docs/compiler.md figure
	// against a counterfactual configuration, differing from the shipped one
	// in the single field under study.
	pipeline           string
	loopUnrolling      bool
	outOfLineCallSites int
}

// compile runs the whole pipeline over one source file, returning every
// warning collected and the diagnostics of the first stage that produced any
// error-level ones. The error return is separate: a stage that could not run
// at all, a defect in the compiler rather than the source.
func compile(ctx context.Context, path, src string, opts options) (emit.Output, source.DiagnosticList, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// LLVM debug locations carry a line and column, no byte offset, so every
	// stage after IR generation rebuilds positions from this.
	lines := source.NewLineMap(src)

	var warnings source.DiagnosticList
	reject := func(diags source.DiagnosticList) (emit.Output, source.DiagnosticList, error) {
		return emit.Output{}, append(warnings, diags...), nil
	}

	file, diags, err := tsparse.Parse(path, src)
	if err != nil {
		return emit.Output{}, nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if diags.HasErrors() {
		return reject(diags)
	}

	prog, diags, err := sema.Analyze(ctx, file, sema.Shipped{})
	if err != nil {
		return emit.Output{}, nil, fmt.Errorf("analyzing %s: %w", path, err)
	}
	if diags.HasErrors() {
		return reject(diags)
	}
	warnings = append(warnings, diags...)

	module, err := irgen.Generate(ctx, prog, irgen.Options{
		ModuleName:         path,
		OutOfLineCallSites: opts.outOfLineCallSites,
	})
	if err != nil {
		if diags, ok := source.DiagnosticsIn(err); ok {
			return reject(diags)
		}
		return emit.Output{}, nil, fmt.Errorf("generating IR for %s: %w", path, err)
	}
	defer module.Dispose()

	if !opts.skipOptimizer {
		if err := llvmopt.Run(ctx, module.Module, llvmopt.Options{
			Pipeline:      opts.pipeline,
			LoopUnrolling: opts.loopUnrolling,
		}); err != nil {
			if diags, ok := source.DiagnosticsIn(err); ok {
				return reject(diags)
			}
			return emit.Output{}, nil, fmt.Errorf("optimizing %s: %w", path, err)
		}
	}

	// The pointer check follows optimization because the optimizer is what can
	// introduce a pointer the backend cannot resolve. Running it unoptimized
	// too keeps the two paths reporting the same restriction.
	if err := pointers.Check(ctx, module.Module, pointers.Options{File: path, Lines: lines}); err != nil {
		if diags, ok := source.DiagnosticsIn(err); ok {
			return reject(diags)
		}
		return emit.Output{}, nil, fmt.Errorf("checking pointers in %s: %w", path, err)
	}

	selected, err := isel.Select(ctx, module.Module, isel.Options{
		File:        path,
		Lines:       lines,
		InlineSites: module.InlineSites,
	})
	if err != nil {
		if diags, ok := source.DiagnosticsIn(err); ok {
			return reject(diags)
		}
		return emit.Output{}, nil, fmt.Errorf("selecting instructions for %s: %w", path, err)
	}

	// Before allocation, because the fresh register a value in a position other
	// than a move's source needs is one allocation has to see.
	if err := mir.MaterialiseUnreadable(selected.Program); err != nil {
		return emit.Output{}, nil, fmt.Errorf("materialising unreadable literals for %s: %w", path, err)
	}

	spills, err := allocate(selected)
	if err != nil {
		if diags, ok := source.DiagnosticsIn(err); ok {
			return reject(diags)
		}
		return emit.Output{}, nil, fmt.Errorf("allocating registers for %s: %w", path, err)
	}

	peephole.Run(selected.Program)

	// Placement is checked here because this is the first point the emitted
	// sequence is final. Allocation prepends the stack pointer initialization
	// and can insert reloads, the peephole removes lines, and any of the three
	// changes what lands on line 0.
	if err := selected.Program.CheckPlacement(); err != nil {
		if diags, ok := source.DiagnosticsIn(err); ok {
			return reject(diags)
		}
		return emit.Output{}, nil, fmt.Errorf("checking instruction placement for %s: %w", path, err)
	}

	// Frames are final once allocation has fixed the stack base and the peephole
	// has run, and the peephole leaves push and pop alone, so this is the first
	// point the depth a recursion can reach is decided.
	frameDiags, err := frames.Measure(ctx, selected.Program, frames.Options{
		StackBase: selected.DataSlots + spills,
	})
	if err != nil {
		return emit.Output{}, nil, fmt.Errorf("measuring call frames for %s: %w", path, err)
	}
	if frameDiags.HasErrors() {
		return reject(frameDiags)
	}
	warnings = append(warnings, frameDiags...)

	output, err := emit.Emit(selected.Program, emit.Options{
		Readable: opts.readable,
		Numeric:  opts.numeric,
		Slots: emit.SlotReport{
			Data:   selected.DataSlots,
			Spill:  spills,
			Frames: selected.CallingConvention,
		},
	})
	if err != nil {
		return emit.Output{}, nil, fmt.Errorf("emitting %s: %w", path, err)
	}
	return output, warnings, nil
}

// allocate rewrites every function from virtual registers to physical ones
// and fixes the boundary between the data region and the call frames. It
// returns the slots the whole program spilled into, for the size report.
//
// sp and ra are reserved only once some call was not inlined. Spill slots
// start past the globals and locals in the shared 512-slot array, and the
// frames start past the spill slots.
func allocate(selected *isel.Result) (int, error) {
	cfg := regalloc.Config{
		Scratch:       regalloc.DefaultScratch(),
		SpillSlotBase: selected.DataSlots,
	}
	if selected.CallingConvention {
		cfg.Reserved = []ic10.Register{ic10.RegSP, ic10.RegRA}
	}

	var diags source.DiagnosticList
	for _, fn := range selected.Program.Funcs {
		// One walk: RegForm visits every instruction and every operand.
		if form := fn.RegForm(); form == mir.RegFormPhysical || form == mir.RegFormEmpty {
			continue
		}
		result, err := regalloc.Allocate(fn, cfg)
		if err != nil {
			return 0, err
		}
		if selected.Recursive[fn.Name] && result.SpillSlots > 0 {
			diags.Addf(fn.Pos, "'%s' can reach itself through a call and holds more live values than the register file has room for; a spill slot is one fixed address, so the inner call would overwrite what the outer one left there", fn.Name)
		}
		// Slots a function spilled into are not reused by the next one: the
		// data region is one flat array, and a live range that crosses a
		// function boundary has no interval to say so.
		cfg.SpillSlotBase += result.SpillSlots
	}
	spills := cfg.SpillSlotBase - selected.DataSlots
	if err := diags.Err(); err != nil {
		return 0, err
	}
	if !selected.CallingConvention {
		return spills, nil
	}
	return spills, regalloc.SetStackBase(selected.Program.Funcs[0], cfg.SpillSlotBase)
}
