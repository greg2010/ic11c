package chip

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestStartRefusesAnUnusableConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		options Options
		wants   string
	}{
		{name: "no image", options: Options{BinDir: t.TempDir()}, wants: "no image given"},
		{
			name:    "an unpinned image",
			options: Options{Image: "mono:6.12", BinDir: t.TempDir()},
			wants:   "not pinned by digest",
		},
		{
			name:    "no chip binary",
			options: Options{Image: "mono:6.12@sha256:" + strings.Repeat("0", 64), BinDir: t.TempDir()},
			wants:   "no chip.exe under",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Both constructors, because a permissive process is started by a
			// different function and would otherwise be free to accept an image
			// the faithful one refuses.
			harness, err := Start(t.Context(), tt.options)
			check(t, "Start", harness == nil, err, tt.options, tt.wants)
			fixtures, err := StartFixtures(t.Context(), tt.options)
			check(t, "StartFixtures", fixtures == nil, err, tt.options, tt.wants)
		})
	}
}

func check(t *testing.T, name string, nothingReturned bool, err error, options Options, wants string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s accepted %+v", name, options)
	}
	if !nothingReturned {
		t.Errorf("%s returned a harness alongside an error", name)
	}
	if !strings.Contains(err.Error(), wants) {
		t.Errorf("%s error = %q, want it to mention %q", name, err, wants)
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("%s error = %q, want it to wrap ErrUnavailable", name, err)
	}
}

// TestABrokenExchangePoisonsTheHarness checks that a refused command takes
// the harness out of service, rather than leaving its unread replies to be
// handed to whatever is asked next — the queued "ok" is the second half of the
// batch, still in flight, and a driver that carried on would read it as its
// next command's answer.
func TestABrokenExchangePoisonsTheHarness(t *testing.T) {
	h := &Harness{
		stdin:   bufio.NewWriter(io.Discard),
		lines:   make(chan string, 4),
		stderr:  &tailBuffer{limit: stderrTail},
		timeout: time.Second,
		log:     func(string, ...any) {},
	}
	h.lines <- "err unknown command"
	h.lines <- "ok"

	if err := h.do(t.Context(), cmdReset, cmdReset); err == nil {
		t.Fatal("do accepted an err reply")
	}
	for _, exchange := range []struct {
		name string
		run  func() error
	}{
		{"do", func() error { return h.do(t.Context(), cmdReset) }},
		{"State", func() error { _, err := h.State(t.Context()); return err }},
		{"Step", func() error { _, err := h.Step(t.Context(), 1); return err }},
	} {
		err := exchange.run()
		if err == nil {
			t.Errorf("%s ran on a harness whose stream is out of step", exchange.name)
			continue
		}
		if !strings.Contains(err.Error(), "out of step") {
			t.Errorf("%s error = %q, want it to say the stream is out of step", exchange.name, err)
		}
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s error = %q, want it to wrap ErrUnavailable", exchange.name, err)
		}
	}
}

// TestAValueNoConstructorBuiltRefusesEveryVerb covers the one way a caller
// outside this package can hold a harness that never started: neither type has
// an exported field, but both can be written down empty, and [FixtureHarness]
// reaches every verb through a nil embedded pointer. Every verb must answer
// cleanly, since a panic there names a line in this package, not the
// constructor the caller skipped.
func TestAValueNoConstructorBuiltRefusesEveryVerb(t *testing.T) {
	ctx := t.Context()
	fixtures := &FixtureHarness{}
	var faithful Harness

	verbs := []struct {
		name string
		run  func() error
	}{
		{"Harness.Reset", func() error { return faithful.Reset(ctx) }},
		{"Harness.Load", func() error { return faithful.Load(ctx, "yield") }},
		{"Harness.Seed", func() error { return faithful.Seed(ctx, State{}) }},
		{"Harness.SetClock", func() error { return faithful.SetClock(ctx, 0, 1) }},
		{"Harness.SetRandomSeed", func() error { return faithful.SetRandomSeed(ctx, 1) }},
		{"Harness.SetRegister", func() error { return faithful.SetRegister(ctx, 0, 1) }},
		{"Harness.SetRegisters", func() error { return faithful.SetRegisters(ctx, 1) }},
		{"Harness.SetAddress", func() error { return faithful.SetAddress(ctx, 0) }},
		{"Harness.FillStack", func() error { return faithful.FillStack(ctx, 1) }},
		{"Harness.State", func() error { _, err := faithful.State(ctx); return err }},
		{"Harness.Step", func() error { _, err := faithful.Step(ctx, 1); return err }},
		{"Harness.Run", func() error { _, err := faithful.Run(ctx, Request{}); return err }},
		{"Harness.Property", func() error { _, err := faithful.Property(ctx, Housing, 0); return err }},
		{"Harness.SlotProperty", func() error { _, err := faithful.SlotProperty(ctx, Housing, 0, 0); return err }},
		{"Harness.Close", func() error { return faithful.Close() }},

		{"FixtureHarness.Reset", func() error { return fixtures.Reset(ctx) }},
		{"FixtureHarness.Step", func() error { _, err := fixtures.Step(ctx, 1); return err }},
		{"FixtureHarness.SeedWorld", func() error { return fixtures.SeedWorld(ctx, &Seeding{}) }},
		{"FixtureHarness.AddDevice", func() error { return fixtures.AddDevice(ctx, 0) }},
		{"FixtureHarness.AddDevices", func() error { return fixtures.AddDevices(ctx, 1) }},
		{"FixtureHarness.SetProperty", func() error { return fixtures.SetProperty(ctx, 0, 0, 1) }},
		{"FixtureHarness.SetProperties", func() error { return fixtures.SetProperties(ctx, 0, nil, 1) }},
		{"FixtureHarness.SetSlotProperty", func() error { return fixtures.SetSlotProperty(ctx, 0, 0, 0, 1) }},
		{"FixtureHarness.SetHashes", func() error { return fixtures.SetHashes(ctx, 0, 1, 2) }},
		{"FixtureHarness.Trace", func() error { _, err := fixtures.Trace(ctx); return err }},
		{"FixtureHarness.Close", func() error { return fixtures.Close() }},
	}
	for _, verb := range verbs {
		t.Run(verb.name, func(t *testing.T) {
			err := verb.run()
			if err == nil {
				t.Fatalf("%s answered a value no constructor built", verb.name)
			}
			if !errors.Is(err, ErrUnavailable) {
				t.Errorf("%s error = %q, want it to wrap ErrUnavailable", verb.name, err)
			}
			if !strings.Contains(err.Error(), "StartFixtures") {
				t.Errorf("%s error = %q, want it to name the constructor to use", verb.name, err)
			}
		})
	}
}

// TestStepRefusesAnEmptyBudget checks the one argument Step can reject without
// a process behind it. A budget of zero retires nothing and the harness would
// answer ok, so a caller stepping with one would loop forever reading the same
// state.
func TestStepRefusesAnEmptyBudget(t *testing.T) {
	for _, budget := range []int{0, -1} {
		var h Harness
		if _, err := h.Step(context.Background(), budget); err == nil {
			t.Errorf("Step accepted a budget of %d", budget)
		}
	}
}
