package main

import (
	"strings"
	"testing"
)

// TestUninitializedLocalIsRejectedRatherThanOptimizedAway is the whole-pipeline
// half of the definite assignment rule. A local read before it is written
// becomes undef, which the optimizer folds through freely — the device stores
// below would be silently deleted rather than the read being refused.
func TestUninitializedLocalIsRejectedRatherThanOptimizedAway(t *testing.T) {
	src := `void main(void) {
	double vert; double sigVert; double sigVertPrev; double vertPrev;
	while (1) {
		vert = __ic_load(d0, Vertical);
		sigVert = __ic_load(d0, SignalStrength);
		if (sigVert > sigVertPrev && vert > vertPrev) { vert -= 5.0; __ic_store(d0, Vertical, vert); sigVertPrev = sigVert; }
		if (sigVert < sigVertPrev && vert > vertPrev) { vert += 5.0; __ic_store(d0, Vertical, vert); sigVertPrev = sigVert; }
		__ic_yield();
	}
}
`
	path := write(t, "tracker.c", src)
	stdout, stderr, err := run(t, path)
	if err == nil {
		t.Fatalf("the program was accepted, and its device stores are gone:\n%s", stdout)
	}
	for _, want := range []string{"sigVertPrev", "vertPrev", "assigned"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the diagnostics do not mention %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "-: ") {
		t.Errorf("a diagnostic carries no source position:\n%s", stderr)
	}
}

// TestInitializedLocalsKeepTheirDeviceStores is the same program with the
// declarations initialized, which is what the diagnostic above asks for. The
// stores have to survive: their disappearance is the failure the rule exists to
// prevent, and an accepted program that still lost them would be no better.
func TestInitializedLocalsKeepTheirDeviceStores(t *testing.T) {
	src := `void main(void) {
	double vert = 0.0; double sigVert = 0.0; double sigVertPrev = 0.0; double vertPrev = 0.0;
	while (1) {
		vert = __ic_load(d0, Vertical);
		sigVert = __ic_load(d0, SignalStrength);
		if (sigVert > sigVertPrev && vert > vertPrev) { vert -= 5.0; __ic_store(d0, Vertical, vert); sigVertPrev = sigVert; }
		if (sigVert < sigVertPrev && vert > vertPrev) { vert += 5.0; __ic_store(d0, Vertical, vert); sigVertPrev = sigVert; }
		__ic_yield();
	}
}
`
	stdout := compileSource(t, write(t, "tracker.c", src))
	// One store, not two: the optimizer merges the two arms into a single
	// store of a value that differs by the sign of the step, which is a fold
	// that keeps the write rather than one that loses it.
	if !strings.Contains(stdout, "s d0 Vertical") {
		t.Errorf("the device store did not survive:\n%s", stdout)
	}
}

// TestPoisonOnAnUnreachableEdgeCompiles covers the other producer of an
// undefined value, which is not a program defect: loop unswitching can leave
// a phi carrying poison on an edge a constant branch never takes. Refusing
// that would reject a correct program, so the value becomes a literal instead.
func TestPoisonOnAnUnreachableEdgeCompiles(t *testing.T) {
	src := `void main(void) {
	double h = 0.0; double p = 0.0;
	while (__ic_load(d2, On) == 1.0) {
		h = __ic_load(d0, SignalStrength);
		while (h < 20.0 && __ic_load(d2, On) == 1.0) {
			if (__ic_load(d0, SignalStrength) > p) { __ic_store(d0, Vertical, 1.0); p = __ic_load(d0, SignalStrength); }
		}
	}
}
`
	stdout := compileSource(t, write(t, "unswitch.c", src))
	if !strings.Contains(stdout, "s d0 Vertical") {
		t.Errorf("the device store did not survive:\n%s", stdout)
	}

	// With the switch on and the signal above the inner loop's bound, the
	// outer loop spins and the inner one is skipped, which is the path the
	// unreachable edge hangs off. The program never ends, so it is driven for a
	// fixed number of ticks.
	housing := newChipRun(t)
	housing.populate(t, 3)
	setLogic(t, housing.device(0), "SignalStrength", 30)
	setLogic(t, housing.device(2), "On", 1)
	runTicks(t, housedChip(t, stdout, housing), 4, stdout)
}

// TestDataRegionObjectReadsAsZeroWithoutRestatingIt covers both halves of
// what the entry prologue's clr db is worth: an unwritten object is undef to
// LLVM and folds away unless stated, which a global initializer does for a
// large array at no per-element cost.
func TestDataRegionObjectReadsAsZeroWithoutRestatingIt(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// stores bounds the instructions that write the data region, which is
		// what restating the zero would cost.
		stores int
		want   float64
	}{
		{
			name: "a dereference of an address-taken local reads the zero",
			src: `void main(void) {
    long long x;
    long long *p = &x;
    if (*p == 0) { __ic_store(d1, On, 1); } else { __ic_store(d1, On, 2); }
}`,
			want: 1,
		},
		{
			name: "an element of a large array reads the zero and pays nothing for it",
			src: `void main(void) {
    long long buf[120];
    long long total = 0;
    for (long long i = 0; i < 120; i = i + 1) { total = total + buf[i]; }
    if (total == 0) { __ic_store(d1, On, 1); } else { __ic_store(d1, On, 2); }
}`,
			want: 1,
		},
		{
			name: "an element past what a brace initializer supplied reads the zero",
			src: `void main(void) {
    long long a[120] = {1, 2};
    long long total = 0;
    for (long long i = 0; i < 120; i = i + 1) { total = total + a[i]; }
    if (total == 3) { __ic_store(d1, On, 1); } else { __ic_store(d1, On, 2); }
}`,
			// The two the initializer supplied. Nothing writes the other 118.
			stores: 2,
			want:   1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assembly := compileSource(t, write(t, "zeroed.c", tt.src))
			if got := strings.Count(assembly, "poke "); got > tt.stores {
				t.Errorf("the program writes the data region %d times, want at most %d; "+
					"the entry prologue has already zeroed all 512 slots\n%s", got, tt.stores, assembly)
			}

			housing, _, actuator := devicePair(t)
			housedChip(t, assembly, housing)
			runProgram(t, housing, assembly)

			if got := logicValue(t, actuator, "On"); got != tt.want {
				t.Errorf("the program left d1 On at %v, want %v\n%s", got, tt.want, assembly)
			}
		})
	}
}
