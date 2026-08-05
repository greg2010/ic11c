// Package chiptest hands a test the game's own chip.
//
// A missing chip is a failure, not a skip: [Harness] and [Fixtures] call
// Fatalf naming the command that builds one, unlike internal/chip's own tests
// which gate behind [chip.Enabled].
//
// One process is started per test binary and shared; a package using it needs
//
//	func TestMain(m *testing.M) { chiptest.Main(m) }
//
// A handout resets the chip, except the sleep clock and the random seed, which
// [chip.Harness.Reset] deliberately keeps. A process a test left unable to
// answer is replaced at the next handout rather than passed on.
//
// The shared process serves one test at a time: a handout while another test
// still holds it fails naming that test, since two tests on one command
// stream would read each other's replies.
package chiptest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
)

// buildCommand is the task target that builds a chip and runs the tests that
// need one. Every failure to reach a chip carries it, because such a failure is
// otherwise indistinguishable from a broken test.
const buildCommand = "`task test`"

// missingChip restates a failure to reach a chip in terms of the command that
// builds one. [chip.EnvOptions] names the variable that is unset and
// [chip.Start] names what would not run; neither says that a chip is something
// this tree builds rather than something a machine has.
func missingChip(err error) error {
	return fmt.Errorf("%w; %s builds a chip and runs these tests", err, buildCommand)
}

// Main runs a package's tests and closes whatever chip process they started.
// It starts nothing itself, so a package where no test asked for a chip pays
// nothing for importing this. A process that would not close fails the run,
// since a leftover container is invisible to anything that reads only an exit
// status.
func Main(m *testing.M) {
	code := m.Run()
	if err := shutdown(); err != nil {
		fmt.Fprintf(os.Stderr, "chiptest: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

var (
	faithful   = process[*chip.Harness]{start: chip.Start}
	permissive = process[*chip.FixtureHarness]{start: chip.StartFixtures}
)

// resettable is what this package needs of a chip: something to reset before a
// test and something to close after the last one. It is deliberately not
// internal/chip's own chipProcess, which is that package's seam between a
// faithful process and a permissive one and is none of this package's business.
type resettable interface {
	Reset(context.Context) error
	Close() error
}

// testingT is the part of a test this package uses. It is declared here
// rather than [testing.TB], whose unexported method blocks any stand-in, so a
// recorder can drive the handout guard in tests. *testing.T satisfies it, so
// no caller writes anything to pass one.
type testingT interface {
	Helper()
	Name() string
	Cleanup(func())
	Logf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// process is one lazily started chip, shared by every test in the binary.
// harness and live are written by [process.get] under no lock; handout makes
// that safe by refusing a second handout already under way, so only one
// goroutine is ever inside get.
type process[T resettable] struct {
	checkout
	handout atomic.Bool
	start   func(context.Context, chip.Options) (T, error)
	harness T
	live    bool
}

// checkout records which test holds a shared chip. It is a guard, not a lock:
// serialising two tests reaching for one process would make an illegal
// t.Parallel keep working quietly instead of failing loudly. It does not make
// the process itself safe for concurrent use — a test that claims in one
// goroutine and drives the chip in another is outside what this or
// [process.get] can see.
type checkout struct {
	mu    sync.Mutex
	owner testingT
}

// claim records t as the holder until t ends, or says who holds it instead. A
// test already holding the process may claim again — sequential use, which is
// what sharing means. A different test may not, whether parallel or a subtest
// outliving its parent, since the next handout's reset would discard what the
// current holder hasn't finished reading.
func (c *checkout) claim(t testingT) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.owner == t {
		return nil
	}
	if c.owner != nil {
		return fmt.Errorf("%s holds it and has not ended", c.owner.Name())
	}
	c.owner = t
	t.Cleanup(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.owner = nil
	})
	return nil
}

// get hands out a chip that has been reset for t. A process an earlier test
// left out of step is replaced rather than handed on: the harness's queued,
// misattributed replies are not recoverable, and restarting bounds the damage
// to the test that caused it. A second handout already under way is refused
// rather than serialised, for the reason [checkout] gives — a lock would let
// the misuse work quietly instead of failing loudly.
func (p *process[T]) get(t testingT) (T, error) {
	t.Helper()
	if !p.handout.CompareAndSwap(false, true) {
		return *new(T), fmt.Errorf("a handout of this chip is already under way in another goroutine; " +
			"it is one command stream and one process record, and two callers on it read each other's replies")
	}
	defer p.handout.Store(false)
	ctx := context.Background()
	if p.live {
		err := p.harness.Reset(ctx)
		if err == nil {
			return p.harness, nil
		}
		t.Logf("an earlier test left the chip unable to answer, so this one starts its own: %v", err)
		p.live = false
		if err := p.harness.Close(); err != nil {
			t.Logf("closing the chip an earlier test broke: %v", err)
		}
	}

	var none T
	options, err := chip.EnvOptions()
	if err != nil {
		return none, missingChip(err)
	}
	harness, err := p.start(ctx, options)
	if err != nil {
		return none, missingChip(err)
	}
	p.harness, p.live = harness, true
	if err := harness.Reset(ctx); err != nil {
		return none, missingChip(fmt.Errorf("resetting a chip that had just started: %w", err))
	}
	return p.harness, nil
}

func (p *process[T]) close() error {
	if !p.live {
		return nil
	}
	p.live = false
	return p.harness.Close()
}

// shutdown closes both processes and reports what would not close. Both are
// attempted whatever the first one does: one process refusing to go is no
// reason to leave the other behind as well.
func shutdown() error {
	if err := errors.Join(faithful.close(), permissive.close()); err != nil {
		return fmt.Errorf("closing the chip: %w", err)
	}
	return nil
}

// Harness returns the shared faithful chip, reset and ready. A test must
// finish with it before another test asks for one; a handout while another
// test still holds the process fails, naming that test.
//
// The context is not the test's context, and deliberately so: it stops a
// caller reaching for t.Context(), which a cancelled test would tear down
// mid-exchange, abandoning a reply queued on the shared stream and poisoning
// the process for the next test. Command deadlines come from the harness.
func Harness(t testingT) (context.Context, *chip.Harness) {
	t.Helper()
	if !claim(t, &faithful.checkout) {
		return context.Background(), nil
	}
	harness, err := faithful.get(t)
	if err != nil {
		t.Fatalf("the chip could not be started, and these tests have no other oracle: %v", err)
	}
	return context.Background(), harness
}

// Fixtures returns the shared permissive chip, reset and ready: the process
// carrying devices that answer any property, for tracing what a program wrote.
// See the internal/chip package doc for what keeps it apart from the faithful
// kind.
//
// A handout resets the process, discarding every device and the trace, so a
// caller must read back a run's trace before any other test asks; one test may
// ask repeatedly, taking two runs one after the other rather than holding two
// processes.
//
// The context is the one [Harness] returns, for the same reason.
func Fixtures(t testingT) (context.Context, *chip.FixtureHarness) {
	t.Helper()
	if !claim(t, &permissive.checkout) {
		return context.Background(), nil
	}
	harness, err := permissive.get(t)
	if err != nil {
		t.Fatalf("a permissive chip could not be started, and these tests have no other oracle: %v", err)
	}
	return context.Background(), harness
}

// claim takes the process for t, or stops the test naming what holds it,
// reporting whether the process was taken. A real Fatalf never returns, so the
// report only matters for the recorder stand-in the guard's own tests drive,
// whose Fatalf does return.
func claim(t testingT, c *checkout) bool {
	t.Helper()
	if err := c.claim(t); err != nil {
		t.Fatalf("the shared chip cannot serve two tests at once: %v. It is one command stream, so two tests on it read each other's replies and the oracle answers a question nobody asked; t.Parallel belongs in no package that takes one, and a subtest must not take it while the test that started it is still using it", err)
		return false
	}
	return true
}
