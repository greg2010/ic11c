package ic10

import (
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

// TestUnemittableCount is a tripwire: growing or shrinking this set is a
// deliberate decision about what the backend may emit, not a side effect.
func TestUnemittableCount(t *testing.T) {
	const want = 24
	if got := len(unemittableOps); got != want {
		t.Errorf("unemittable instructions = %d, want %d", got, want)
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
