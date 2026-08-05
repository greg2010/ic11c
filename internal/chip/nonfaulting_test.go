package chip

import (
	"context"
	"math"
	"testing"
)

// batchReads and batchWrites are the seven forms that resolve the housing's
// batch output, over a structure hash and a name hash no device carries. This
// file settles what internal/difftest's generators rest on: a shape must run
// to the program's end, and a fault recipe must raise the exception it names —
// answers no reading of the slice settles on its own.
var (
	batchReads = []struct {
		mnemonic string
		operands string
	}{
		{mnemonic: "lb", operands: batchPrefab + " Setting"},
		{mnemonic: "lbn", operands: batchPrefab + " " + batchName + " Setting"},
		{mnemonic: "lbs", operands: batchPrefab + " 0 Quantity"},
		{mnemonic: "lbns", operands: batchPrefab + " " + batchName + " 0 Quantity"},
	}
	batchWrites = []struct {
		mnemonic string
		operands string
	}{
		{mnemonic: "sb", operands: batchPrefab + " Setting 1"},
		{mnemonic: "sbn", operands: batchPrefab + " " + batchName + " Setting 1"},
		{mnemonic: "sbs", operands: batchPrefab + " 0 Quantity 1"},
	}
)

const (
	batchPrefab = "-128473777"
	batchName   = "12345"
)

// batchModes is what each fold answers over no readings. The answers are
// asymmetric — Minimum is guarded back to zero from its +Inf seed, Maximum is
// left on its -Inf one — so each mode is named with the value it produces.
// wantNaN is separate from want since the chip's NaN carries its own payload,
// so a bit comparison against one built here would report a difference
// between two NaNs.
var batchModes = []struct {
	mode    string
	want    float64
	wantNaN bool
}{
	{mode: "Average", wantNaN: true},
	{mode: "Sum", want: 0},
	{mode: "Minimum", want: 0},
	{mode: "Maximum", want: math.Inf(-1)},
	{mode: "Count", want: 0},
}

// TestBatchFormsFaultOnAHousingWithNoDataCable holds all seven forms to
// raising DeviceListNull on the housing a reset leaves: it's on no data
// cable, and the game answers a null device list for a null network. The chip
// raises before it looks at a hash, so the fault doesn't depend on the
// operands — the same for every mode and every write, which is why the whole
// table is here rather than one representative form. This is the world
// internal/difftest builds, which is why batch forms are a fault recipe there
// and not a value shape.
func TestBatchFormsFaultOnAHousingWithNoDataCable(t *testing.T) {
	ctx, harness := liveHarness(t)

	for _, read := range batchReads {
		for _, mode := range batchModes {
			t.Run(read.mnemonic+"/"+mode.mode, func(t *testing.T) {
				faultsWith(ctx, t, harness,
					read.mnemonic+" r0 "+read.operands+" "+mode.mode+"\nmove r1 1", ExcDeviceListNull)
			})
		}
	}
	for _, write := range batchWrites {
		t.Run(write.mnemonic, func(t *testing.T) {
			faultsWith(ctx, t, harness,
				write.mnemonic+" "+write.operands+"\nmove r1 1", ExcDeviceListNull)
		})
	}
}

// TestBatchFormsAnswerOnAnEmptyDataCable holds the same seven forms to
// answering rather than faulting once the housing is wired with nothing else
// on the cable. The trailing move is the whole point of each program: a run
// that reaches it went past the batch line rather than stopping on it.
func TestBatchFormsAnswerOnAnEmptyDataCable(t *testing.T) {
	ctx, harness := liveHarness(t)

	for _, read := range batchReads {
		for _, mode := range batchModes {
			t.Run(read.mnemonic+"/"+mode.mode, func(t *testing.T) {
				got := runOnCable(ctx, t, harness,
					read.mnemonic+" r0 "+read.operands+" "+mode.mode+"\nmove r1 1")
				switch {
				case mode.wantNaN && !math.IsNaN(got.Registers[0]):
					t.Errorf("r0 = %v, want NaN", got.Registers[0])
				case !mode.wantNaN && math.Float64bits(got.Registers[0]) != math.Float64bits(mode.want):
					t.Errorf("r0 = %v, want %v", got.Registers[0], mode.want)
				}
			})
		}
	}
	for _, write := range batchWrites {
		t.Run(write.mnemonic, func(t *testing.T) {
			runOnCable(ctx, t, harness, write.mnemonic+" "+write.operands+"\nmove r1 1")
		})
	}
}

// faultsWith runs a program and fails unless it compiled and stopped on the
// exception named, on the line the offending instruction stands on.
func faultsWith(ctx context.Context, t *testing.T, h *Harness, source string, want ExceptionType) {
	t.Helper()
	got, err := h.Run(ctx, Request{Source: source})
	if err != nil {
		t.Fatalf("run %q: %v", source, err)
	}
	if got.CompileError.Type != ExcNone {
		t.Fatalf("%q did not compile: %v", source, got.CompileError)
	}
	if got.Stop != StopFaulted || got.Fault.Type != want {
		t.Fatalf("%q stopped %q with %v, want it to fault %v", source, got.Stop, got.Fault, want)
	}
	if got.Fault.Line != 0 {
		t.Errorf("%q faulted at line %d, want the line the batch form stands on", source, got.Fault.Line)
	}
}

// runOnCable runs a program on a housing wired to an empty data cable and
// fails unless it reached its own end. It cannot go through [Harness.Run],
// which resets first and so takes the cable away again; the programs here are
// two lines, so one segment retires all of them.
func runOnCable(ctx context.Context, t *testing.T, h *Harness, source string) Segment {
	t.Helper()
	if err := h.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := h.SetDataNetwork(ctx, true); err != nil {
		t.Fatalf("run a data cable to the housing: %v", err)
	}
	if err := h.Load(ctx, source); err != nil {
		t.Fatalf("load %q: %v", source, err)
	}
	got, err := h.Step(ctx, InstructionsPerTick)
	if err != nil {
		t.Fatalf("run %q: %v", source, err)
	}
	if got.CompileError.Type != ExcNone {
		t.Fatalf("%q did not compile: %v", source, got.CompileError)
	}
	if got.Stop != StopEnded {
		t.Fatalf("%q stopped %q at line %d with %v, want it to reach its own end",
			source, got.Stop, got.Address, got.Fault)
	}
	return got
}

// TestModAnswersRatherThanFaultingOnANonFiniteDividend settles what mod does
// with a dividend no finite remainder is defined for: the chip answers NaN and
// carries on, so an unbounded dividend is no reason to keep mod out of a
// program required not to fault. The dividends are computed, since the chip's
// parser has no spelling for infinity or NaN.
func TestModAnswersRatherThanFaultingOnANonFiniteDividend(t *testing.T) {
	ctx, harness := liveHarness(t)

	tests := []struct {
		name     string
		dividend string
	}{
		{name: "positive infinity", dividend: "div r0 1 0"},
		{name: "negative infinity", dividend: "div r0 -1 0"},
		{name: "nan", dividend: "div r0 0 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runToEnd(ctx, t, harness, tt.dividend+"\nmod r1 r0 3\nmove r2 1")
			if !math.IsNaN(got.Registers[1]) {
				t.Errorf("r1 = %v, want NaN", got.Registers[1])
			}
		})
	}
}

// runToEnd runs a program and fails unless it compiled and reached its own end
// without faulting.
func runToEnd(ctx context.Context, t *testing.T, h *Harness, source string) Observation {
	t.Helper()
	got, err := h.Run(ctx, Request{Source: source})
	if err != nil {
		t.Fatalf("run %q: %v", source, err)
	}
	if got.CompileError.Type != ExcNone {
		t.Fatalf("%q did not compile: %v", source, got.CompileError)
	}
	if got.Stop != StopEnded {
		t.Fatalf("%q stopped %q at line %d with %v, want it to reach its own end",
			source, got.Stop, got.Address, got.Fault)
	}
	return got
}
