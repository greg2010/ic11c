package oracle

import (
	"context"
	"math"
	"slices"
	"testing"
	"time"
)

func TestRegistryIsWellFormed(t *testing.T) {
	fields := AllFields()
	harnesses := []Harness{IC10Emu, NPM}
	authorities := []Authority{AuthorityGame, AuthorityUnknown}

	seen := make(map[string]bool)
	for _, d := range Registry() {
		t.Run(d.ID, func(t *testing.T) {
			if seen[d.ID] {
				t.Errorf("duplicate ID")
			}
			seen[d.ID] = true

			for name, value := range map[string]string{
				"Summary": d.Summary, "Observed": d.Observed, "Correct": d.Correct, "Reason": d.Reason,
			} {
				if value == "" {
					t.Errorf("%s is empty; every entry has to record what it claims and why", name)
				}
			}
			if len(d.Harnesses) == 0 {
				t.Errorf("no harnesses")
			}
			for _, h := range d.Harnesses {
				if !slices.Contains(harnesses, h) {
					t.Errorf("unknown harness %q", h)
				}
			}
			if !slices.Contains(authorities, d.Authority) {
				t.Errorf("unknown authority %q", d.Authority)
			}
			for _, f := range d.Fields {
				if !slices.Contains(fields, f) {
					t.Errorf("unknown field %q", f)
				}
			}
			switch {
			case d.Advisory && len(d.Fields) != 0:
				t.Errorf("advisory entries excuse nothing, so Fields must be empty")
			case !d.Advisory && len(d.Fields) == 0:
				t.Errorf("no fields, so this entry can never excuse anything")
			}
		})
	}
}

func TestReachable(t *testing.T) {
	tests := []struct {
		name    string
		harness Harness
		source  string
		want    []string
	}{
		{
			name:    "a plain program triggers nothing conditional",
			harness: IC10Emu,
			source:  "move r0 1\nadd r1 r0 2\n",
		},
		{
			name:    "a constant triggers its entry",
			harness: IC10Emu,
			source:  "move r0 epsilon\n",
			want:    []string{"ic10emu/const-epsilon"},
		},
		{
			name:    "a comment does not trigger",
			harness: IC10Emu,
			source:  "move r0 1 # epsilon and deg2rad live here\n",
		},
		{
			name:    "a label with a trailing colon is tokenized",
			harness: IC10Emu,
			source:  "sleep:\nj sleep\n",
			want:    []string{"ic10emu/sleep"},
		},
		{
			name:    "unconditional entries always apply",
			harness: NPM,
			source:  "move r0 1\n",
			want:    []string{"npm/error-model", "npm/no-tick-model", "npm/instruction-count"},
		},
		{
			name:    "an entry for the other harness does not apply",
			harness: NPM,
			source:  "move r0 epsilon\n",
			want:    []string{"npm/error-model", "npm/no-tick-model", "npm/instruction-count"},
		},
		{
			name:    "a logic instruction raises the game-data advisory",
			harness: IC10Emu,
			source:  "l r0 db Setting\n",
			want:    []string{"game-data-vintage"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, d := range Reachable(tt.harness, tt.source) {
				got = append(got, d.ID)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("reachable = %v, want %v", got, tt.want)
			}
		})
	}
}

// resultCheck asserts one property of a probe's result.
type resultCheck func(t *testing.T, got Result)

// probeCase is one program that should still exhibit a registered divergence.
//
// checks all apply to the same run, so a divergence with several observable halves — a fault
// raised and a wrong value left behind — costs one program rather than one per assertion.
type probeCase struct {
	source  string
	initial State
	timeout time.Duration
	checks  []resultCheck
}

type probe struct {
	id    string
	cases []probeCase
}

func wantFault(status, errorType string) resultCheck {
	return func(t *testing.T, got Result) {
		t.Helper()
		if got.Status != status || got.ErrorType != errorType {
			t.Errorf("status %q error %q, want %q / %q (%s)",
				got.Status, got.ErrorType, status, errorType, got.ErrorMsg)
		}
	}
}

func wantStatus(status string) resultCheck {
	return func(t *testing.T, got Result) {
		t.Helper()
		if got.Status != status {
			t.Errorf("status %q, want %q (error %q: %s)", got.Status, status, got.ErrorType, got.ErrorMsg)
		}
	}
}

func wantRegister(index int, value float64) resultCheck {
	return func(t *testing.T, got Result) {
		t.Helper()
		if math.Float64bits(got.Final.Registers[index]) != math.Float64bits(value) {
			t.Errorf("%s = %v (0x%016x), want %v (0x%016x)", RegisterName(index),
				got.Final.Registers[index], math.Float64bits(got.Final.Registers[index]),
				value, math.Float64bits(value))
		}
	}
}

func wantCompileErrors(t *testing.T, got Result) {
	t.Helper()
	if len(got.CompileErrors) == 0 {
		t.Errorf("no compile errors, so the instruction now parses")
	}
}

// unimplemented builds the per-mnemonic cases for an "instruction is missing" entry.
func unimplemented(check resultCheck, sources map[string]string) []probeCase {
	cases := make([]probeCase, 0, len(sources))
	for _, source := range sources {
		cases = append(cases, probeCase{source: source, checks: []resultCheck{check}})
	}
	return cases
}

var ic10emuProbes = []probe{
	{
		id: "ic10emu/const-deg2rad-rad2deg",
		cases: []probeCase{
			{source: "move r0 deg2rad\n", checks: []resultCheck{wantFault("error", "UnknownIdentifier")}},
			{source: "move r0 rad2deg\n", checks: []resultCheck{wantFault("error", "UnknownIdentifier")}},
		},
	},
	{
		id: "ic10emu/const-tau-rgas",
		cases: []probeCase{
			{source: "move r0 tau\n", checks: []resultCheck{wantFault("error", "UnknownIdentifier")}},
			{source: "move r0 rgas\n", checks: []resultCheck{wantFault("error", "UnknownIdentifier")}},
		},
	},
	{
		id: "ic10emu/const-epsilon",
		cases: []probeCase{
			{source: "move r0 epsilon\n", checks: []resultCheck{wantRegister(0, math.Nextafter(1, 2)-1)}},
		},
	},
	{
		id: "ic10emu/nan-minmax",
		cases: []probeCase{
			{
				source:  "max r0 r1 r2\n",
				initial: State{Registers: [RegisterCount]float64{1: math.NaN(), 2: 5}},
				checks:  []resultCheck{wantRegister(0, 5)},
			},
			{
				source:  "min r0 r2 r1\n",
				initial: State{Registers: [RegisterCount]float64{1: math.NaN(), 2: 5}},
				checks:  []resultCheck{wantRegister(0, 5)},
			},
		},
	},
	{
		id: "ic10emu/empty-batch-aggregate",
		cases: []probeCase{
			{source: "lb r0 1234 On Sum\n", checks: []resultCheck{wantRegister(0, math.Copysign(0, -1))}},
			{source: "lb r0 1234 On Minimum\n", checks: []resultCheck{wantRegister(0, math.Inf(1))}},
		},
	},
	{
		id: "ic10emu/approximate-tolerance-floor",
		cases: []probeCase{
			{source: "sapz r0 0.000000000000001 0\n", checks: []resultCheck{wantRegister(0, 1)}},
			{source: "snaz r0 0.000000000000001 0\n", checks: []resultCheck{wantRegister(0, 0)}},
			// The branch is not taken, so the line after it runs.
			{source: "bnaz 0.000000000000001 0 3\nmove r0 1\n", checks: []resultCheck{wantRegister(0, 1)}},
		},
	},
	{
		id: "ic10emu/bapz-inverted",
		cases: []probeCase{
			{source: "bapz 1000 0 3\nmove r0 1\n", checks: []resultCheck{wantRegister(0, 0)}},
			{source: "bapz 0 0 3\nmove r0 1\n", checks: []resultCheck{wantRegister(0, 1)}},
			{source: "bapzal 1000 0 3\nmove r0 1\n", checks: []resultCheck{wantRegister(0, 0)}},
		},
	},
	{
		id: "ic10emu/round-midpoint",
		cases: []probeCase{
			{source: "round r0 2.5\n", checks: []resultCheck{wantRegister(0, 3)}},
			{source: "round r0 -2.5\n", checks: []resultCheck{wantRegister(0, -3)}},
		},
	},
	{
		id: "ic10emu/unimplemented-instructions",
		cases: unimplemented(wantCompileErrors, map[string]string{
			"ext":   "ext r0 255 0 4\n",
			"ins":   "ins r0 0 4 1\n",
			"rol":   "rol r0 1 1\n",
			"ror":   "ror r0 2 1\n",
			"lerp":  "lerp r0 0 10 0.5\n",
			"clamp": "clamp r0 5 1 2\n",
			"pow":   "pow r0 2 10\n",
			"sgn":   "sgn r0 -5\n",
			"bdnvl": "bdnvl d0 3\n",
			"bdnvs": "bdnvs d0 3\n",
		}),
	},
	{
		id: "ic10emu/getd-putd-db",
		cases: []probeCase{
			{source: "getd r0 db 3\n", checks: []resultCheck{wantFault("error", "IncorrectOperandType")}},
			{source: "putd db 3 99\n", checks: []resultCheck{wantFault("error", "IncorrectOperandType")}},
		},
	},
	{
		id:    "ic10emu/sleep",
		cases: []probeCase{{source: "sleep 1\nmove r0 5\n", checks: []resultCheck{wantStatus("sleep")}}},
	},
	{
		id:    "ic10emu/hcf",
		cases: []probeCase{{source: "hcf\n", checks: []resultCheck{wantStatus("ended")}}},
	},
}

var npmProbes = []probe{
	{
		id:    "npm/error-model",
		cases: []probeCase{{source: "frobnicate r0 r1\n", checks: []resultCheck{wantFault("error", "FATAL_ERROR")}}},
	},
	{
		id: "npm/no-tick-model",
		cases: []probeCase{{
			source: "add r0 r0 1\nyield\nadd r0 r0 1\n",
			checks: []resultCheck{func(t *testing.T, got Result) {
				t.Helper()
				if got.Ticks != 0 {
					t.Errorf("ticks = %d, want 0", got.Ticks)
				}
			}},
		}},
	},
	{
		id: "npm/instruction-count",
		cases: []probeCase{{
			source: "move r0 1\nmove r1 2\n",
			checks: []resultCheck{func(t *testing.T, got Result) {
				t.Helper()
				if got.Instructions != 3 {
					t.Errorf("instructions = %d, want 3 for a two-line program", got.Instructions)
				}
			}},
		}},
	},
	{
		id:    "npm/const-rgas",
		cases: []probeCase{{source: "move r0 rgas\n", checks: []resultCheck{wantRegister(0, 8.31446261815324)}}},
	},
	{
		id:    "npm/const-ninf",
		cases: []probeCase{{source: "move r0 ninf\n", checks: []resultCheck{wantRegister(0, math.Inf(1))}}},
	},
	{
		id:    "npm/const-nan",
		cases: []probeCase{{source: "move r0 nan\n", checks: []resultCheck{wantFault("error", "TYPE_ERROR")}}},
	},
	{
		id: "npm/sp-ra",
		cases: []probeCase{
			// The write faults and still lands on r0, which is one run, not two.
			{source: "move sp 5\n", checks: []resultCheck{
				wantFault("error", "TYPE_ERROR"),
				wantRegister(0, 5),
			}},
			{source: "move r0 sp\n", checks: []resultCheck{wantRegister(0, 16)}},
			{source: "move r0 ra\n", checks: []resultCheck{wantRegister(0, 17)}},
		},
	},
	{
		id: "npm/link-register",
		cases: []probeCase{
			// Both branches are taken from line 0, so the game would leave ra at 1.
			{source: "beqal 1 1 3\nmove r0 9\n", checks: []resultCheck{wantRegister(17, 0)}},
			{source: "jal 3\nmove r0 9\n", checks: []resultCheck{wantRegister(17, 0)}},
		},
	},
	{
		id: "npm/device-presence-tests",
		cases: []probeCase{
			// The right value is stored and the run then faults, which is one run, not two.
			{source: "sdns r0 d0\n", checks: []resultCheck{
				wantRegister(0, 1),
				wantFault("error", "TYPE_ERROR"),
			}},
			{source: "bdns d0 3\nmove r0 1\n", checks: []resultCheck{wantFault("error", "TYPE_ERROR")}},
		},
	},
	{
		id: "npm/define-zero",
		cases: []probeCase{
			{source: "define v0 0\n", checks: []resultCheck{wantFault("error", "TYPE_ERROR")}},
			// Any other value is accepted, which is what makes the entry value dependent.
			{source: "define v0 1\nmove r0 v0\n", checks: []resultCheck{wantRegister(0, 1)}},
		},
	},
	{
		id: "npm/unimplemented-instructions",
		cases: unimplemented(wantFault("error", "FATAL_ERROR"), map[string]string{
			"rol":   "rol r0 1 1\n",
			"ror":   "ror r0 2 1\n",
			"clamp": "clamp r0 5 1 2\n",
		}),
	},
	{
		id: "npm/getd-putd-db",
		cases: []probeCase{
			{source: "getd r0 db 3\n", checks: []resultCheck{wantStatus("error")}},
			{source: "putd db 3 99\n", checks: []resultCheck{wantStatus("error")}},
		},
	},
	{
		id: "npm/sleep",
		cases: []probeCase{{
			source:  "sleep 1\nmove r0 5\n",
			timeout: 3 * time.Second,
			checks:  []resultCheck{wantStatus("harness_timeout")},
		}},
	},
	{
		id:    "npm/hcf",
		cases: []probeCase{{source: "hcf\n", checks: []resultCheck{wantFault("error", "RUNTIME_ERROR")}}},
	},
}

// TestDivergencesStillReproduce is the reason the registry is worth keeping. Each entry is an
// allowlist hole, so a failure here means either a harness was fixed and the entry should go, or
// the entry never described the harness accurately in the first place.
func TestDivergencesStillReproduce(t *testing.T) {
	for _, group := range []struct {
		harness Harness
		probes  []probe
	}{
		{IC10Emu, ic10emuProbes},
		{NPM, npmProbes},
	} {
		t.Run(string(group.harness), func(t *testing.T) {
			client := Shared(t, group.harness)
			for _, p := range group.probes {
				t.Run(p.id, func(t *testing.T) {
					if _, ok := lookup(p.id); !ok {
						t.Fatalf("probes an ID that is not in the registry")
					}
					for _, c := range p.cases {
						if len(c.checks) == 0 {
							t.Fatalf("case %q asserts nothing", c.source)
						}
						timeout := c.timeout
						if timeout == 0 {
							timeout = runTimeout
						}
						ctx, cancel := context.WithTimeout(context.Background(), timeout)
						got, err := client.Run(ctx, c.source, c.initial, 100_000)
						cancel()
						if err != nil {
							t.Fatalf("Run %q: %v", c.source, err)
						}
						for _, check := range c.checks {
							check(t, got)
						}
					}
				})
			}
		})
	}
}

// TestEveryDivergenceHasAProbe keeps the registry from growing claims nobody checks.
func TestEveryDivergenceHasAProbe(t *testing.T) {
	probed := make(map[string]bool)
	for _, p := range slices.Concat(ic10emuProbes, npmProbes) {
		probed[p.id] = true
	}
	for _, d := range Registry() {
		if d.Advisory {
			continue
		}
		if !probed[d.ID] {
			t.Errorf("%s has no probe, so nothing would notice if the harness were fixed", d.ID)
		}
	}
}
