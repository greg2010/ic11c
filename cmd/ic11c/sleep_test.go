package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
)

// TestSleepOnLineZeroBurnsTheTickBudget measures the hazard the placement
// guard exists for: sleep signals re-entry by returning the negation of the
// line it sits on, and -0 is not negative, so on line 0 the tick loop's test
// never fires and the chip re-runs sleep until the tick budget is gone.
func TestSleepOnLineZeroBurnsTheTickBudget(t *testing.T) {
	const (
		online0 = "sleep 1"
		online1 = "move r0 0\nsleep 1"
	)

	// One tick, on the game's own chip. The two endings leave the same address,
	// so what separates them is the flag the run answers with and never the
	// state it left; see [chip.Harness.Step].
	t.Run("one tick", func(t *testing.T) {
		cases := []struct {
			name     string
			assembly string
			wantStop chip.StopReason
			wantPC   int
		}{
			{"sleep on line 0", online0, chip.StopBudget, 0},
			{"sleep on line 1", online1, chip.StopSuspended, 1},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				housing := newChipRun(t)
				// A clock two readings cannot tell apart, so neither sleep
				// expires and the line number is the whole difference.
				if err := housing.harness.SetClock(housing.ctx, 0, 0); err != nil {
					t.Fatalf("pin the clock: %v", err)
				}
				housing.load(t, tc.assembly)
				segment := housing.step(t, chip.InstructionsPerTick)
				if segment.Stop != tc.wantStop {
					t.Errorf("the tick ended %q, want %q:\n%s", segment.Stop, tc.wantStop, tc.assembly)
				}
				if segment.Address != tc.wantPC {
					t.Errorf("the tick ended at line %d, want %d:\n%s", segment.Address, tc.wantPC, tc.assembly)
				}
			})
		}
	})

	// Several ticks catch what one tick cannot: a sleep giving back only the
	// first tick's rest would still pass there. Instruction counts aren't
	// asserted — ProgrammableChip.Execute reports none, only the stop reason;
	// see [chip.Harness.Step].
	t.Run("several ticks", func(t *testing.T) {
		const ticks = 4
		cases := []struct {
			name     string
			assembly string
			wantStop chip.StopReason
		}{
			{"sleep on line 0", online0, chip.StopBudget},
			{"sleep on line 1", online1, chip.StopSuspended},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				housing := newChipRun(t)
				if err := housing.harness.SetClock(housing.ctx, 0, 0); err != nil {
					t.Fatalf("pin the clock: %v", err)
				}
				housing.load(t, tc.assembly)
				for tick := range ticks {
					segment := housing.step(t, chip.InstructionsPerTick)
					if segment.Stop != tc.wantStop {
						t.Fatalf("tick %d ended %q, want %q every tick:\n%s",
							tick, segment.Stop, tc.wantStop, tc.assembly)
					}
				}
			})
		}
	})
}

// TestCompileRefusesSleepOnLineZero is the guard firing on the program that
// provokes it. The refusal has to name the instruction, the line it would take,
// and the source position, since on a chip that reports nothing but a line
// number the alternative is a program that silently burns every tick.
func TestCompileRefusesSleepOnLineZero(t *testing.T) {
	path := write(t, "sleep.c", `void main(void) {
    __ic_sleep(1);
    __ic_store(d1, On, 1);
}`)

	stdout, stderr, err := run(t, path)
	if err == nil {
		t.Fatalf("compiling accepted a program whose first instruction is a sleep:\n%s", stdout)
	}
	for _, want := range []string{"sleep", "first instruction", "line 0", "sleep.c:2:"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, stderr)
		}
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("assembly was written for a program that did not compile:\n%s", stdout)
	}
}

// remedyCall matches a call the sleep refusal offers as the fix. The parentheses
// are what make it a construct rather than a gesture at one.
var remedyCall = regexp.MustCompile(`__ic_[a-z_]+\([^)]*\)`)

// TestSleepRefusalOffersAFixThatCompiles holds the refusal's remedy to being
// a remedy: advice naming something the optimizer discards before selection
// sees it computes nothing observable, leaving the sleep back on line 0.
// Only compiling the suggestion itself tells a working fix from a discarded one.
func TestSleepRefusalOffersAFixThatCompiles(t *testing.T) {
	const hazard = `void main(void) {
    __ic_sleep(1);
    __ic_store(d1, On, 1);
}`
	_, stderr, err := run(t, write(t, "sleep.c", hazard))
	if err == nil {
		t.Fatal("compiling accepted a program whose first instruction is a sleep")
	}

	suggested := remedyCall.FindAllString(stderr, -1)
	if len(suggested) == 0 {
		t.Fatalf("the refusal names no call to fix the program with:\n%s", stderr)
	}

	for _, call := range suggested {
		t.Run(call, func(t *testing.T) {
			fixed := "void main(void) {\n    " + call + ";\n" + strings.TrimPrefix(hazard, "void main(void) {\n")
			stdout, stderr, err := run(t, write(t, "fixed.c", fixed))
			if err != nil {
				t.Fatalf("the refusal suggests %s, and the program it fixes does not compile:\n%s\n%s", call, fixed, stderr)
			}
			lines := assemblyLines(t, stdout)
			if len(lines) == 0 || strings.HasPrefix(lines[0], "sleep ") {
				t.Fatalf("%s left the sleep on line 0:\n%s", call, stdout)
			}
		})
	}
}

// TestCompileSleepOffLineZeroRuns is the other half: a sleep the guard
// accepts has to actually work. The program makes no real call and declares
// nothing, so it gets no prologue and the sleep is safe only because a
// device store precedes it, not because the compiler put anything there.
func TestCompileSleepOffLineZeroRuns(t *testing.T) {
	assembly := compiled(t, "sleep.c", `void main(void) {
    __ic_store(d1, On, 1);
    __ic_sleep(1);
    __ic_store(d1, Setting, 2);
}`)

	lines := assemblyLines(t, assembly)
	sleepLine := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "sleep ") {
			sleepLine = i
			break
		}
	}
	if sleepLine <= 0 {
		t.Fatalf("the sleep is on line %d, and the guard accepted the program:\n%s", sleepLine, assembly)
	}

	housing, _, out := devicePair(t)
	// A clock held still for the first tick and then moved past the duration is
	// what separates "the sleep suspended" from "the sleep was skipped": under a
	// clock that never advances the sleep re-enters itself forever.
	if err := housing.harness.SetClock(housing.ctx, 0, 0); err != nil {
		t.Fatalf("pin the clock: %v", err)
	}
	housing.load(t, assembly)

	segment := housing.step(t, chip.InstructionsPerTick)
	housing.faulted(t)
	if segment.Stop != chip.StopSuspended {
		t.Errorf("the first tick ended %q; the sleep did not return the rest of the tick:\n%s",
			segment.Stop, assembly)
	}
	if segment.Address != sleepLine {
		t.Errorf("the tick ended at line %d, want the sleep on line %d:\n%s", segment.Address, sleepLine, assembly)
	}
	if got := logicValue(t, out, "On"); got != 1 {
		t.Errorf("the store ahead of the sleep set On to %v, want 1:\n%s", got, assembly)
	}
	if got := logicValue(t, out, "Setting"); got != 0 {
		t.Errorf("the store past the sleep ran before it, setting Setting to %v:\n%s", got, assembly)
	}

	// Two seconds since the last reading, which is past the duration.
	if err := housing.harness.SetClock(housing.ctx, 2, 0); err != nil {
		t.Fatalf("advance the clock: %v", err)
	}
	runToEnd(t, housing, assembly)
	if got := logicValue(t, out, "Setting"); got != 2 {
		t.Errorf("the program did not resume past the sleep: Setting is %v, want 2:\n%s", got, assembly)
	}
}
