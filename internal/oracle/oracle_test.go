package oracle

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greg2010/ic11c/internal/ic10"
)

// runTimeout bounds a single program so a wedged harness fails the test instead of the suite.
const runTimeout = 30 * time.Second

func run(t *testing.T, c *Client, source string, initial State) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	got, err := c.Run(ctx, source, initial, 100_000)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return got
}

// pinnedCommit reads the upstream revision the build script pins, so the test and the build
// cannot drift apart.
func pinnedCommit(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(toolsDir, "upstream.env"))
	if err != nil {
		t.Fatalf("read upstream.env: %v", err)
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		if commit, ok := strings.CutPrefix(line, "IC10EMU_COMMIT="); ok {
			return strings.TrimSpace(commit)
		}
	}
	t.Fatalf("upstream.env has no IC10EMU_COMMIT")
	return ""
}

// TestBannerMatchesPin guards the reproducibility claim: a binary built from a different upstream
// revision, or without our patches, must not pass silently for the pinned one.
func TestBannerMatchesPin(t *testing.T) {
	info := Shared(t, IC10Emu).Info()
	if want := pinnedCommit(t); info.IC10EmuCommit != want {
		t.Errorf("ic10emu commit = %q, want %q; rebuild with tools/oracle/build-ic10emu.sh",
			info.IC10EmuCommit, want)
	}
	if len(info.Patches) == 0 {
		t.Errorf("server reports no patches applied")
	}
	if info.InstructionsPerTick != 128 {
		t.Errorf("instructions per tick = %d, want 128", info.InstructionsPerTick)
	}
}

func TestFinalState(t *testing.T) {
	tests := []struct {
		name          string
		source        string
		initial       State
		wantRegisters map[int]float64
		wantStack     map[int]float64
		wantStatus    string
		wantErrorType string
		wantErrorLine uint32
		wantInstr     uint64
		wantTicks     uint64
	}{
		{
			name: "arithmetic and poke get db round trip",
			source: "move r0 7\nadd r1 r0 5\nmul r2 r1 3\npoke 100 r2\n" +
				"get r3 db 100\nsub r4 r3 r2\npush 42\npop r5\n",
			wantRegisters: map[int]float64{0: 7, 1: 12, 2: 36, 3: 36, 4: 0, 5: 42, 16: 0},
			wantStack:     map[int]float64{0: 42, 100: 36},
			wantStatus:    "ended",
			wantInstr:     8,
			wantTicks:     1,
		},
		{
			name:   "initial state is honoured",
			source: "get r0 db 5\nadd r1 r0 r2\n",
			initial: State{
				Registers: [RegisterCount]float64{2: 2.5},
				Stack:     [ic10.NumMemorySlots]float64{5: 1.5},
			},
			wantRegisters: map[int]float64{0: 1.5, 1: 4, 2: 2.5},
			wantStack:     map[int]float64{5: 1.5},
			wantStatus:    "ended",
			wantInstr:     2,
			wantTicks:     1,
		},
		{
			name:          "put db writes the chip's own memory",
			source:        "put db 3 99\nget r0 db 3\n",
			wantRegisters: map[int]float64{0: 99},
			wantStack:     map[int]float64{3: 99},
			wantStatus:    "ended",
			wantInstr:     2,
			wantTicks:     1,
		},
		{
			name:       "clr db zeroes the whole array",
			source:     "poke 3 99\npoke 400 1\nclr db\n",
			wantStatus: "ended",
			wantInstr:  3,
			wantTicks:  1,
		},
		{
			name:          "pop below zero leaves sp at -1 and faults",
			source:        "pop r0\n",
			wantRegisters: map[int]float64{16: -1},
			wantStatus:    "error",
			wantErrorType: "MemoryError.StackUnderflow",
			wantErrorLine: 0,
			wantInstr:     1,
			wantTicks:     1,
		},
		{
			name:          "poke past the array faults on its own line",
			source:        "move r0 1\npoke 600 r0\nmove r1 9\n",
			wantRegisters: map[int]float64{0: 1, 1: 0},
			wantStatus:    "error",
			wantErrorType: "StackIndexOutOfRange",
			wantErrorLine: 1,
			wantInstr:     2,
			wantTicks:     1,
		},
		{
			name:          "a full tick of instructions stays in one tick",
			source:        strings.Repeat("add r0 r0 1\n", 128),
			wantRegisters: map[int]float64{0: 128},
			wantStatus:    "ended",
			wantInstr:     128,
			wantTicks:     1,
		},
		{
			name:          "overrunning the tick budget continues next tick",
			source:        strings.Repeat("add r0 r0 1\n", 200),
			wantRegisters: map[int]float64{0: 200},
			wantStatus:    "ended",
			wantInstr:     200,
			wantTicks:     2,
		},
		{
			name:          "yield ends the tick early",
			source:        "add r0 r0 1\nyield\nadd r0 r0 1\n",
			wantRegisters: map[int]float64{0: 2},
			wantStatus:    "ended",
			wantInstr:     3,
			wantTicks:     2,
		},
		{
			name:          "indirect register reference resolves through r0",
			source:        "move r0 3\nmove rr0 42\nmove r1 r3\n",
			wantRegisters: map[int]float64{0: 3, 1: 42, 3: 42},
			wantStatus:    "ended",
			wantInstr:     3,
			wantTicks:     1,
		},
		{
			name:   "an endless loop stops at the instruction budget",
			source: "loop:\nadd r0 r0 1\nj loop\n",
			// The bare label costs an instruction of its own, so each pass round the loop
			// retires three.
			wantRegisters: map[int]float64{0: 33_333},
			wantStatus:    "budget_exhausted",
			wantInstr:     100_000,
			wantTicks:     782,
		},
	}

	client := Shared(t, IC10Emu)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := run(t, client, tt.source, tt.initial)
			if got.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q (error %q on line %d: %s)",
					got.Status, tt.wantStatus, got.ErrorType, got.ErrorLine, got.ErrorMsg)
			}
			if got.ErrorType != tt.wantErrorType {
				t.Errorf("error type = %q, want %q", got.ErrorType, tt.wantErrorType)
			}
			if tt.wantErrorType != "" && got.ErrorLine != tt.wantErrorLine {
				t.Errorf("error line = %d, want %d", got.ErrorLine, tt.wantErrorLine)
			}
			if got.Instructions != tt.wantInstr {
				t.Errorf("instructions = %d, want %d", got.Instructions, tt.wantInstr)
			}
			if got.Ticks != tt.wantTicks {
				t.Errorf("ticks = %d, want %d", got.Ticks, tt.wantTicks)
			}
			if len(got.CompileErrors) != 0 {
				t.Errorf("compile errors = %v, want none", got.CompileErrors)
			}

			var want State
			for i, v := range tt.wantRegisters {
				want.Registers[i] = v
			}
			for i, v := range tt.wantStack {
				want.Stack[i] = v
			}
			if detail := diffDoubles("register", want.Registers[:], got.Final.Registers[:], RegisterName); detail != "" {
				t.Errorf("registers: %s", detail)
			}
			if detail := diffDoubles("slot", want.Stack[:], got.Final.Stack[:], nil); detail != "" {
				t.Errorf("stack: %s", detail)
			}
		})
	}
}

// TestSpecialValuesSurviveTheWire is the reason doubles travel as bit patterns rather than as
// JSON numbers.
func TestSpecialValuesSurviveTheWire(t *testing.T) {
	// A NaN with a distinctive payload, which decimal round-tripping would flatten to a quiet NaN.
	payloadNaN := math.Float64frombits(0x7FF8000000000042)
	initial := State{
		Registers: [RegisterCount]float64{
			0: math.NaN(),
			1: math.Inf(1),
			2: math.Inf(-1),
			3: math.Copysign(0, -1),
			4: payloadNaN,
		},
		Stack: [ic10.NumMemorySlots]float64{7: payloadNaN},
	}

	got := run(t, Shared(t, IC10Emu), "move r5 r0\n", initial)

	tests := []struct {
		name  string
		got   float64
		want  float64
		match func(float64) bool
	}{
		{name: "r0 NaN", got: got.Final.Registers[0], match: math.IsNaN},
		{name: "r1 +Inf", got: got.Final.Registers[1], match: func(v float64) bool { return math.IsInf(v, 1) }},
		{name: "r2 -Inf", got: got.Final.Registers[2], match: func(v float64) bool { return math.IsInf(v, -1) }},
		{name: "r3 negative zero", got: got.Final.Registers[3], want: math.Copysign(0, -1)},
		{name: "r4 NaN payload", got: got.Final.Registers[4], want: payloadNaN},
		{name: "slot 7 NaN payload", got: got.Final.Stack[7], want: payloadNaN},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.match != nil {
				if !tt.match(tt.got) {
					t.Errorf("got %v (0x%016x)", tt.got, math.Float64bits(tt.got))
				}
				return
			}
			if math.Float64bits(tt.got) != math.Float64bits(tt.want) {
				t.Errorf("got 0x%016x, want 0x%016x", math.Float64bits(tt.got), math.Float64bits(tt.want))
			}
		})
	}
}

// TestCancellation covers the split in Run's contract: a request that never went out leaves the
// stream intact, while one abandoned mid-flight cannot be resynchronized and poisons the client.
func TestCancellation(t *testing.T) {
	privateClient := func(t *testing.T) *Client {
		t.Helper()
		if _, err := Locate(IC10Emu); err != nil {
			t.Skipf("skipping: %v", err)
		}
		client, err := Start(context.Background(), IC10Emu)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Cleanup(func() {
			if err := client.Close(); err != nil {
				t.Logf("close: %v", err)
			}
		})
		return client
	}

	t.Run("cancelled before the request goes out", func(t *testing.T) {
		client := privateClient(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := client.Run(ctx, "move r0 1\n", State{}, 100); err == nil {
			t.Fatalf("Run with a cancelled context returned no error")
		}
		if _, err := client.Run(context.Background(), "move r0 1\n", State{}, 100); err != nil {
			t.Errorf("client should still work after a request that never went out: %v", err)
		}
	})

	t.Run("cancelled in flight", func(t *testing.T) {
		client := privateClient(t)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		// A budget far larger than the deadline allows, so the deadline lands mid-response.
		if _, err := client.Run(ctx, "loop:\nj loop\n", State{}, 5_000_000_000); err == nil {
			t.Fatalf("Run past its deadline returned no error")
		}
		if _, err := client.Run(context.Background(), "move r0 1\n", State{}, 100); err == nil {
			t.Errorf("a later Run on the poisoned client returned no error")
		}
	})
}

func TestLocateReportsWhatIsMissing(t *testing.T) {
	t.Setenv(EnvIC10Emu, filepath.Join(t.TempDir(), "absent"))
	_, err := Locate(IC10Emu)
	if err == nil {
		t.Fatalf("Locate found a binary that does not exist")
	}
	for _, want := range []string{"absent", "build-ic10emu.sh", EnvIC10Emu} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func BenchmarkRun(b *testing.B) {
	client := Shared(b, IC10Emu)
	source := "move r0 7\nadd r1 r0 5\nmul r2 r1 3\npoke 100 r2\npush 42\npop r5\n"
	ctx := context.Background()
	for b.Loop() {
		if _, err := client.Run(ctx, source, State{}, 10_000); err != nil {
			b.Fatalf("Run: %v", err)
		}
	}
}
