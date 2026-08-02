package difftest

import (
	"context"
	"errors"
	"fmt"

	"github.com/greg2010/ic11c/internal/oracle"
	"github.com/greg2010/ic11c/internal/vm"
)

// Run statuses, drawn from the vocabulary an oracle Result reports.
const (
	// StatusEnded means the program counter left the program.
	StatusEnded = "ended"
	// StatusError means a run time fault stopped the chip.
	StatusError = "error"
	// StatusFire means hcf destroyed the chip.
	StatusFire = "fire"
	// StatusBudgetExhausted means the instruction budget ran out.
	StatusBudgetExhausted = "budget_exhausted"
	// StatusCompileError means the program never ran. No harness reports it:
	// ic10emu keeps parse failures as data and substitutes a nop, and the npm
	// package raises them part way through a run. A generator that produces one
	// therefore fails loudly instead of being quietly comparable.
	StatusCompileError = "compile_error"
)

// Run executes source on a fresh chip seeded with initial and reports the run
// as an oracle Result, so that it can be compared against a harness's.
//
// maxInstructions bounds a non-terminating program; zero means
// oracle.DefaultMaxInstructions.
//
// The returned error is reserved for conditions that are not a chip verdict: a
// cancelled context, or an instruction internal/vm does not model. A chip fault
// is part of the Result. `sleep` is not modelled here — our chip re-enters it
// until the budget runs out, where ic10emu stops and reports status "sleep" —
// so a program containing one comes back as a status mismatch rather than as a
// wrong answer.
func Run(ctx context.Context, source string, initial oracle.State, maxInstructions uint64) (oracle.Result, error) {
	if maxInstructions == 0 {
		maxInstructions = oracle.DefaultMaxInstructions
	}

	m := vm.NewMachine()
	loadErr := m.Load(ctx, source)
	// Seeding after Load, the order the oracle server uses. Load resets sp, so
	// seeding first would silently drop the caller's stack pointer.
	//
	// These two assignments are also the assertion that the oracle wire format
	// and the chip agree on the register count and the memory size: the array
	// lengths are part of the types, so a divergence is a compile error.
	m.SetRegisters(initial.Registers)
	m.SetMemory(initial.Stack)

	if loadErr != nil {
		var fault *vm.Fault
		if !errors.As(loadErr, &fault) {
			return oracle.Result{}, fmt.Errorf("load: %w", loadErr)
		}
		result := snapshot(m)
		result.Status = StatusCompileError
		result.ErrorType = HarnessErrorName(fault.Type)
		result.ErrorLine = uint32(fault.Line)
		result.ErrorMsg = fault.Error()
		return result, nil
	}

	run, err := execute(ctx, m, maxInstructions)
	if err != nil {
		return oracle.Result{}, err
	}

	result := snapshot(m)
	result.Status = run.status
	result.Instructions = run.instructions
	result.Ticks = run.ticks
	if run.fault != nil {
		result.ErrorType = HarnessErrorName(run.fault.Type)
		result.ErrorLine = uint32(run.fault.Line)
		result.ErrorMsg = run.fault.Error()
	}
	return result, nil
}

// outcome is what one tick loop produced.
type outcome struct {
	status       string
	fault        *vm.Fault
	instructions uint64
	ticks        uint64
}

// end folds a destroyed chip into the status vocabulary. hcf raises
// ExcChipCatchingFire here and leaves the chip destroyed, where an emulator
// reports a fire state carrying no error, so the fault is dropped with it.
// ic10emu reports "ended" instead; the divergence registry covers that.
func (o outcome) end(m *vm.Machine, status string, fault *vm.Fault) outcome {
	o.status, o.fault = status, fault
	if m.Destroyed() {
		o.status, o.fault = StatusFire, nil
	}
	return o
}

// execute is the tick loop the ic10oracle server runs, reproduced so that the
// instruction and tick counts mean the same thing on both sides: a tick is
// entered before its budget is spent, the instruction that faults is retired,
// and a program that ends exactly on the budget has ended rather than been cut
// off.
//
// The server also carries a tick budget, which this does not: every tick here
// retires at least one instruction, enforced below, so the instruction budget
// alone bounds the loop. The client sets the server's tick budget one past the
// instruction budget for the same reason.
func execute(ctx context.Context, m *vm.Machine, maxInstructions uint64) (outcome, error) {
	var run outcome
	for {
		if finished(m) {
			return run.end(m, StatusEnded, nil), nil
		}
		run.ticks++

		budget := uint64(vm.InstructionsPerTick)
		if remaining := maxInstructions - run.instructions; remaining < budget {
			budget = remaining
		}
		executed, err := m.Tick(ctx, int(budget))
		run.instructions += uint64(executed)
		if err != nil {
			var fault *vm.Fault
			if !errors.As(err, &fault) {
				return outcome{}, fmt.Errorf("tick at line %d: %w", m.PC(), err)
			}
			return run.end(m, StatusError, fault), nil
		}
		if executed == 0 {
			// Unreachable while the program counter is inside the program and
			// the budget is positive, both of which hold here. Returning rather
			// than looping keeps a future change from spinning silently.
			return outcome{}, fmt.Errorf("tick at line %d made no progress", m.PC())
		}
		if finished(m) {
			return run.end(m, StatusEnded, nil), nil
		}
		if run.instructions >= maxInstructions {
			return run.end(m, StatusBudgetExhausted, nil), nil
		}
	}
}

// finished reports that the program counter has left the program, which is how
// the chip ends a run.
func finished(m *vm.Machine) bool {
	pc := m.PC()
	return pc < 0 || pc >= m.LineCount()
}

func snapshot(m *vm.Machine) oracle.Result {
	pc := max(m.PC(), 0)
	return oracle.Result{
		Final:              oracle.State{Registers: m.Registers(), Stack: m.Memory()},
		InstructionPointer: uint32(pc),
	}
}
