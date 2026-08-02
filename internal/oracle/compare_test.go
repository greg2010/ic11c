package oracle

import (
	"math"
	"slices"
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	negativeZero := math.Copysign(0, -1)
	// Go's math.NaN() is itself 0x7FF8000000000001, so a distinct payload is needed here.
	quietNaN := math.NaN()
	payloadNaN := math.Float64frombits(0x7FF8000000000042)

	withRegister := func(index int, value float64) Result {
		var r Result
		r.Status = "ended"
		r.Final.Registers[index] = value
		return r
	}

	tests := []struct {
		name            string
		harness         Harness
		source          string
		ours, theirs    Result
		wantUnexplained []Field
		wantExcusedBy   map[Field][]string
		wantAdvisories  []string
	}{
		{
			name:    "identical results agree",
			harness: IC10Emu,
			source:  "move r0 1\n",
			ours:    withRegister(0, 1),
			theirs:  withRegister(0, 1),
		},
		{
			name:            "an unregistered register difference is a finding",
			harness:         IC10Emu,
			source:          "move r0 1\n",
			ours:            withRegister(0, 1),
			theirs:          withRegister(0, 2),
			wantUnexplained: []Field{FieldRegisters},
		},
		{
			name:          "a constant the emulator gets wrong is excused",
			harness:       IC10Emu,
			source:        "move r0 epsilon\n",
			ours:          withRegister(0, math.SmallestNonzeroFloat64),
			theirs:        withRegister(0, math.Nextafter(1, 2)-1),
			wantExcusedBy: map[Field][]string{FieldRegisters: {"ic10emu/const-epsilon"}},
		},
		{
			name:          "two entries can cover the same mismatch",
			harness:       IC10Emu,
			source:        "move r0 tau\nmax r1 r0 r2\n",
			ours:          withRegister(0, 1),
			theirs:        withRegister(0, 2),
			wantExcusedBy: map[Field][]string{FieldRegisters: {"ic10emu/const-tau-rgas", "ic10emu/nan-minmax"}},
		},
		{
			name:    "the npm tick model excuses ticks but not registers",
			harness: NPM,
			source:  "move r0 1\n",
			ours: func() Result {
				r := withRegister(0, 1)
				r.Ticks = 1
				return r
			}(),
			theirs:          withRegister(0, 2),
			wantUnexplained: []Field{FieldRegisters},
			wantExcusedBy:   map[Field][]string{FieldTicks: {"npm/no-tick-model"}},
		},
		{
			name:    "the npm error vocabulary excuses the error fields",
			harness: NPM,
			source:  "frobnicate r0\n",
			ours: func() Result {
				r := withRegister(0, 0)
				r.Status = "error"
				r.ErrorType = "ParseError"
				r.ErrorLine = 0
				return r
			}(),
			theirs: func() Result {
				r := withRegister(0, 0)
				r.Status = "ended"
				r.ErrorType = "FATAL_ERROR"
				r.ErrorLine = 3
				return r
			}(),
			wantExcusedBy: map[Field][]string{
				FieldStatus:    {"npm/error-model"},
				FieldErrorType: {"npm/error-model"},
				FieldErrorLine: {"npm/error-model"},
			},
		},
		{
			name:    "a logic program gets the data-vintage advisory and still has to agree",
			harness: IC10Emu,
			source:  "l r0 db Setting\n",
			ours:    withRegister(0, 1),
			theirs:  withRegister(0, 2),

			wantUnexplained: []Field{FieldRegisters},
			wantAdvisories:  []string{"game-data-vintage"},
		},
		{
			name:    "NaN equals NaN with the same payload",
			harness: IC10Emu,
			source:  "move r0 r1\n",
			ours:    withRegister(0, payloadNaN),
			theirs:  withRegister(0, payloadNaN),
		},
		{
			name:            "NaN payloads are compared",
			harness:         IC10Emu,
			source:          "move r0 r1\n",
			ours:            withRegister(0, quietNaN),
			theirs:          withRegister(0, payloadNaN),
			wantUnexplained: []Field{FieldRegisters},
		},
		{
			name:            "negative zero differs from positive zero",
			harness:         IC10Emu,
			source:          "move r0 r1\n",
			ours:            withRegister(0, negativeZero),
			theirs:          withRegister(0, 0),
			wantUnexplained: []Field{FieldRegisters},
		},
		{
			name:    "the stack is compared slot by slot",
			harness: IC10Emu,
			source:  "poke 5 1\n",
			ours: func() Result {
				r := withRegister(0, 0)
				r.Final.Stack[5] = 1
				return r
			}(),
			theirs:          withRegister(0, 0),
			wantUnexplained: []Field{FieldStack},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Compare(tt.harness, tt.source, tt.ours, tt.theirs)

			var unexplained []Field
			for _, m := range got.Unexplained {
				unexplained = append(unexplained, m.Field)
			}
			if !slices.Equal(unexplained, tt.wantUnexplained) {
				t.Errorf("unexplained = %v, want %v (%s)", unexplained, tt.wantUnexplained, got)
			}
			if got.OK() != (len(tt.wantUnexplained) == 0) {
				t.Errorf("OK() = %v", got.OK())
			}

			excused := make(map[Field][]string)
			for _, e := range got.Excused {
				for _, d := range e.By {
					excused[e.Mismatch.Field] = append(excused[e.Mismatch.Field], d.ID)
				}
			}
			if len(excused) != len(tt.wantExcusedBy) {
				t.Errorf("excused %v, want %v", excused, tt.wantExcusedBy)
			}
			for field, want := range tt.wantExcusedBy {
				if !slices.Equal(excused[field], want) {
					t.Errorf("%s excused by %v, want %v", field, excused[field], want)
				}
			}

			var advisories []string
			for _, d := range got.Advisories {
				advisories = append(advisories, d.ID)
			}
			if !slices.Equal(advisories, tt.wantAdvisories) {
				t.Errorf("advisories = %v, want %v", advisories, tt.wantAdvisories)
			}
		})
	}
}

func TestDiffDoublesCapsDetail(t *testing.T) {
	ours := make([]float64, 20)
	theirs := make([]float64, 20)
	for i := range theirs {
		theirs[i] = float64(i + 1)
	}
	detail := diffDoubles("slot", ours, theirs, nil)
	if detail == "" {
		t.Fatalf("no detail for 20 differing slots")
	}
	if want := "and 16 more"; !strings.Contains(detail, want) {
		t.Errorf("detail %q does not mention %q", detail, want)
	}
}
