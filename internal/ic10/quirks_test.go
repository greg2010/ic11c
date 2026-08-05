package ic10

import (
	"math"
	"slices"
	"strings"
	"testing"
)

// TestUnemittable covers the instructions the backend must never select. The
// four broken branches are the reason this table exists: they are uncompilable
// in this build and nothing in the extracted tables says so.
func TestUnemittable(t *testing.T) {
	tests := []struct {
		name        string
		mnemonic    string
		want        bool
		wantMention string
	}{
		{name: "brapz is uncompilable", mnemonic: "brapz", want: true, wantMention: "uncompilable"},
		{name: "brnaz is uncompilable", mnemonic: "brnaz", want: true, wantMention: "uncompilable"},
		{name: "bapzal is uncompilable", mnemonic: "bapzal", want: true, wantMention: "uncompilable"},
		{name: "bnazal is uncompilable", mnemonic: "bnazal", want: true, wantMention: "uncompilable"},
		{name: "sla duplicates sll", mnemonic: "sla", want: true, wantMention: "sll"},
		{name: "jr is relative", mnemonic: "jr", want: true, wantMention: "line offset"},
		{name: "brltz is relative", mnemonic: "brltz", want: true, wantMention: "line offset"},
		{name: "brnan is relative", mnemonic: "brnan", want: true, wantMention: "line offset"},
		{name: "hcf destroys the chip", mnemonic: "hcf", want: true, wantMention: "destroys the chip"},
		{name: "sleep is emittable off line 0", mnemonic: "sleep"},
		{name: "sll is fine", mnemonic: "sll"},
		{name: "j is absolute", mnemonic: "j"},
		{name: "bltz is absolute", mnemonic: "bltz"},
		{name: "bnan is absolute", mnemonic: "bnan"},
		{name: "add is fine", mnemonic: "add"},
		{name: "yield is fine", mnemonic: "yield"},
		{name: "deprecated but emittable", mnemonic: "ld"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instruction, ok := LookupInstruction(tt.mnemonic)
			if !ok {
				t.Fatalf("LookupInstruction(%q) found nothing", tt.mnemonic)
			}
			reason, got := Unemittable(instruction.Opcode)
			if got != tt.want {
				t.Fatalf("Unemittable(%s) = %v (%q), want %v", tt.mnemonic, got, reason, tt.want)
			}
			if !got {
				if reason != "" {
					t.Errorf("Unemittable(%s) returned reason %q for an emittable instruction", tt.mnemonic, reason)
				}
				return
			}
			if !strings.Contains(reason, tt.wantMention) {
				t.Errorf("Unemittable(%s) reason = %q, want it to mention %q", tt.mnemonic, reason, tt.wantMention)
			}
		})
	}
}

// TestEveryRelativeFormIsUnemittable catches a relative branch added by a game
// update that nobody thought to list.
func TestEveryRelativeFormIsUnemittable(t *testing.T) {
	for _, instruction := range Instructions {
		if !strings.HasPrefix(instruction.Mnemonic, "br") && instruction.Mnemonic != "jr" {
			continue
		}
		if _, ok := Unemittable(instruction.Opcode); !ok {
			t.Errorf("%s encodes a line offset but is not listed as unemittable", instruction.Mnemonic)
		}
	}
}

// TestUnemittableKeysAreRealOpcodes guards against an entry surviving a
// mnemonic that no longer exists.
func TestUnemittableKeysAreRealOpcodes(t *testing.T) {
	for op, reason := range unemittableOps {
		instruction, ok := op.Instruction()
		if !ok {
			t.Errorf("unemittable entry for %v is outside the instruction table", op)
			continue
		}
		if reason == "" {
			t.Errorf("%s is unemittable with no reason given", instruction.Mnemonic)
		}
	}
}

// TestUnemittableSet names the whole set, since growing or shrinking it is
// a deliberate decision about what the backend may emit. Written out
// rather than counted: [TestUnemittable] and
// [TestEveryRelativeFormIsUnemittable] already catch a removal, so what is
// left here is catching an addition, which a count could not name.
func TestUnemittableSet(t *testing.T) {
	want := []string{
		"bapzal", "bnazal", "brapz", "brnaz",
		"sla",
		"brap", "brdns", "brdse", "breq", "breqz", "brge", "brgez", "brgt", "brgtz",
		"brle", "brlez", "brlt", "brltz", "brna", "brnan", "brne", "brnez", "jr",
		"hcf",
	}
	slices.Sort(want)

	// String renders an entry outside the instruction table as its number, so a
	// key that no longer names anything reports as one rather than being skipped.
	got := make([]string, 0, len(unemittableOps))
	for op := range unemittableOps {
		got = append(got, op.String())
	}
	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Errorf("the backend may not select %v, and this build refuses %v", want, got)
	}
}

// TestFirstLineHazard covers the instructions that are wrong only at line 0.
// The two tables are disjoint by construction: an instruction that must never
// be emitted has no placement to be right about.
func TestFirstLineHazard(t *testing.T) {
	tests := []struct {
		name        string
		mnemonic    string
		want        bool
		wantMention string
	}{
		{name: "sleep burns the tick on line 0", mnemonic: "sleep", want: true, wantMention: "-0"},
		{name: "yield ends the tick from anywhere", mnemonic: "yield"},
		{name: "j is placement independent", mnemonic: "j"},
		{name: "add is placement independent", mnemonic: "add"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instruction, ok := LookupInstruction(tt.mnemonic)
			if !ok {
				t.Fatalf("LookupInstruction(%q) found nothing", tt.mnemonic)
			}
			reason, got := FirstLineHazard(instruction.Opcode)
			if got != tt.want {
				t.Fatalf("FirstLineHazard(%s) = %v (%q), want %v", tt.mnemonic, got, reason, tt.want)
			}
			if !got {
				if reason != "" {
					t.Errorf("FirstLineHazard(%s) returned reason %q for a placement independent instruction", tt.mnemonic, reason)
				}
				return
			}
			if !strings.Contains(reason, tt.wantMention) {
				t.Errorf("FirstLineHazard(%s) reason = %q, want it to mention %q", tt.mnemonic, reason, tt.wantMention)
			}
		})
	}
}

// TestFirstLineHazardsAreEmittable keeps the two tables from overlapping. An
// entry in both would be a placement rule on an instruction no placement makes
// legal, which says the wrong thing about why it is refused.
func TestFirstLineHazardsAreEmittable(t *testing.T) {
	for op, reason := range firstLineHazards {
		instruction, ok := op.Instruction()
		if !ok {
			t.Errorf("first line hazard entry for %v is outside the instruction table", op)
			continue
		}
		if reason == "" {
			t.Errorf("%s is a first line hazard with no reason given", instruction.Mnemonic)
		}
		if _, bad := Unemittable(op); bad {
			t.Errorf("%s is both unemittable and a first line hazard; the placement rule is unreachable", instruction.Mnemonic)
		}
	}
}

// TestUnreadable covers the values an operand literal cannot name, and the
// nearest values it can — the neighbours are the point: negative zero
// compares equal to positive zero and differs by one bit, so a classifier
// keyed on value rather than sign would miss the defect.
func TestUnreadable(t *testing.T) {
	tests := []struct {
		name  string
		value float64
		want  bool
	}{
		{name: "a NaN", value: math.NaN(), want: true},
		{name: "a NaN with a payload", value: math.Float64frombits(0x7ff8000000000042), want: true},
		{name: "a negative zero", value: math.Copysign(0, -1), want: true},
		{name: "a positive zero"},
		{name: "positive infinity", value: math.Inf(1)},
		{name: "negative infinity", value: math.Inf(-1)},
		{name: "the smallest subnormal", value: math.SmallestNonzeroFloat64},
		{name: "the smallest negative subnormal", value: -math.SmallestNonzeroFloat64},
		{name: "the largest double", value: math.MaxFloat64},
		{name: "the most negative double", value: -math.MaxFloat64},
		{name: "minus one", value: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remedy, got := Unreadable(tt.value)
			if got != tt.want {
				t.Fatalf("Unreadable(%016x) = %v, want %v", math.Float64bits(tt.value), got, tt.want)
			}
			if !got {
				if remedy != (UnreadableValue{}) {
					t.Errorf("Unreadable(%016x) returned %+v for a value the parser reads back", math.Float64bits(tt.value), remedy)
				}
				return
			}
			if remedy.Reason == "" {
				t.Errorf("Unreadable(%016x) gives no reason", math.Float64bits(tt.value))
			}
		})
	}
}

// TestEveryRemedyIsOneTheBackendCanBuild holds the table's own arithmetic
// to the rules everything else in the backend is held to. Worth stating:
// nothing about the table's shape stops a remedy from naming an operand
// the parser also cannot read, which would be a rule that cannot be
// applied, so that is asserted too.
func TestEveryRemedyIsOneTheBackendCanBuild(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Copysign(0, -1)} {
		remedy, ok := Unreadable(value)
		if !ok {
			t.Fatalf("Unreadable(%016x) found nothing to do", math.Float64bits(value))
		}
		instruction, known := remedy.Op.Instruction()
		if !known {
			t.Errorf("the remedy for %016x names %v, which is outside the instruction table", math.Float64bits(value), remedy.Op)
			continue
		}
		if reason, bad := Unemittable(remedy.Op); bad {
			t.Errorf("the remedy for %016x is %s, which the backend may not select: %s",
				math.Float64bits(value), instruction.Mnemonic, reason)
		}
		if reason, hazard := FirstLineHazard(remedy.Op); hazard {
			t.Errorf("the remedy for %016x is %s, which is wrong on line 0 and may land there: %s",
				math.Float64bits(value), instruction.Mnemonic, reason)
		}
		if len(instruction.Operands) != 3 {
			t.Errorf("the remedy for %016x is %s, which takes %d operands; the rewrite writes a destination and two sources",
				math.Float64bits(value), instruction.Mnemonic, len(instruction.Operands))
		}
		for i, operand := range []float64{remedy.Left, remedy.Right} {
			if _, unreadable := Unreadable(operand); unreadable {
				t.Errorf("the remedy for %016x names at source %d a value the parser cannot read either (%016x)",
					math.Float64bits(value), i, math.Float64bits(operand))
			}
		}
	}
}
