package chiptest

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
)

// recorder stands in for a test that reaches for the shared process while
// another one holds it. A real Fatalf ends its goroutine, so only a stand-in
// that records and returns can prove [Harness] and [Fixtures] actually take
// the process out — without one, deleting both claims would still pass.
type recorder struct {
	name string
	mu   sync.Mutex
	// fatals is every message the code under test stopped this stand-in with.
	fatals []string
	// cleanups are what the code under test registered against this stand-in
	// ending. They are kept rather than dropped because one of them is what
	// gives the shared process back, and a stand-in that took the process and
	// never released it would stop every test after it in the binary.
	cleanups []func()
}

func (r *recorder) Helper()      {}
func (r *recorder) Name() string { return r.name }

func (r *recorder) Cleanup(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanups = append(r.cleanups, f)
}

func (r *recorder) Logf(string, ...any) {}

func (r *recorder) Fatalf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fatals = append(r.fatals, fmt.Sprintf(format, args...))
}

// end runs what the code under test registered against this stand-in ending,
// in the order a test's own cleanups run.
func (r *recorder) end() {
	r.mu.Lock()
	cleanups := slices.Clone(r.cleanups)
	r.cleanups = nil
	r.mu.Unlock()
	for _, f := range slices.Backward(cleanups) {
		f()
	}
}

// only returns the one message the stand-in was stopped with.
func (r *recorder) only(tb testing.TB) string {
	tb.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.fatals) != 1 {
		tb.Fatalf("the stand-in was stopped %d times, want once: %q", len(r.fatals), r.fatals)
	}
	return r.fatals[0]
}

// TestOneTestAtATime covers the guard on the shared process — the only thing
// standing between a later t.Parallel and an oracle answering out of another
// test's command stream. No chip is started; only the bookkeeping is checked.
func TestOneTestAtATime(t *testing.T) {
	var held checkout
	if err := held.claim(t); err != nil {
		t.Fatalf("the first test to ask could not take the process: %v", err)
	}
	if err := held.claim(t); err != nil {
		t.Errorf("the test holding the process could not take it again, which is one test running a second program: %v", err)
	}

	holder := t
	t.Run("another test reaching for it from another goroutine", func(t *testing.T) {
		reached := make(chan error, 1)
		go func() { reached <- held.claim(t) }()
		// The holder goes on using it while the other reaches, which is the
		// interleaving the guard has to answer under rather than race in.
		for range 100 {
			if err := held.claim(holder); err != nil {
				t.Fatalf("the holder lost the process while another test reached for it: %v", err)
			}
		}
		err := <-reached
		if err == nil {
			t.Fatal("a second test took the process while the first still held it, so two command streams would be interleaved on one pipe")
		}
		// The holder is named rather than the refusal merely reported: the test
		// to look at is the one still using the chip, and a refusal that did not
		// say which that was would send the reader to the wrong one.
		if !strings.Contains(err.Error(), holder.Name()) {
			t.Errorf("the refusal is %q and does not name %s, which is the test holding the process", err, holder.Name())
		}
	})
}

// TestTheHandoutsTakeTheProcessOut holds [Harness] and [Fixtures] to the guard
// the two tests above only prove in isolation — deleting the claim from either
// function would otherwise leave every other test in this package green. Each
// is driven with a stand-in while a real test holds the process, and the
// refusal has to name the holder. The image is cleared so a handout that
// reached the process anyway fails on configuration, not by starting a
// container.
func TestTheHandoutsTakeTheProcessOut(t *testing.T) {
	tests := []struct {
		name  string
		hold  *checkout
		reach func(testingT)
	}{
		{
			name:  "Harness",
			hold:  &faithful.checkout,
			reach: func(t testingT) { Harness(t) },
		},
		{
			name:  "Fixtures",
			hold:  &permissive.checkout,
			reach: func(t testingT) { Fixtures(t) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(chip.EnvImage, "")
			if err := tt.hold.claim(t); err != nil {
				t.Fatalf("the process was already held before this test asked: %v", err)
			}

			other := &recorder{name: "TestSomebodyElse"}
			t.Cleanup(other.end)
			tt.reach(other)

			refusal := other.only(t)
			if !strings.Contains(refusal, t.Name()) {
				t.Errorf("%s stopped the second test with %q, which does not name %s, the test still holding the process",
					tt.name, refusal, t.Name())
			}
			if !strings.Contains(refusal, "cannot serve two tests at once") {
				t.Errorf("%s stopped the second test with %q, which is not the refusal the guard makes; a handout that reached the process would report something else",
					tt.name, refusal)
			}
		})
	}
}

// TestTheProcessGoesBackWhenItsTestEnds covers the release, which is what
// makes handing the process out once per test work at all. The handouts repeat
// because one release proves nothing: a guard that cleared the holder only on
// the first end would pass here and stop the second test in every package that
// uses this.
func TestTheProcessGoesBackWhenItsTestEnds(t *testing.T) {
	var held checkout
	for turn := range 3 {
		t.Run(strconv.Itoa(turn), func(t *testing.T) {
			if err := held.claim(t); err != nil {
				t.Fatalf("the process was not released by the test before this one: %v", err)
			}
		})
	}
}
