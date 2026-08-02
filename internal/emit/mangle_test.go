package emit

import (
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/mir"
)

// TestMangle covers the collision that matters most: a label spelled like one
// of the chip's own names is not rejected by the chip, it is shadowed, and
// every instruction referring to it then faults once per tick forever.
func TestMangle(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "already safe", in: "main_loop", want: "main_loop"},
		{name: "dots become underscores", in: "main.loop", want: "main_loop"},
		{name: "punctuation becomes underscores", in: "f<int>::body", want: "f_int___body"},
		{name: "logic type", in: "Temperature", want: "Temperature_"},
		{name: "logic type in another case", in: "temperature", want: "temperature_"},
		{name: "logic slot type", in: "Occupied", want: "Occupied_"},
		{name: "batch mode", in: "Average", want: "Average_"},
		{name: "reagent mode", in: "Contents", want: "Contents_"},
		{name: "mnemonic", in: "add", want: "add_"},
		{name: "register", in: "r0", want: "r0_"},
		{name: "stack pointer", in: "sp", want: "sp_"},
		{name: "device pin", in: "d0", want: "d0_"},
		{name: "base device", in: "db", want: "db_"},
		{name: "constant", in: "pi", want: "pi_"},
		{name: "empty", in: "", want: "_"},
		{name: "leading digit", in: "2fast", want: "_2fast"},
		{name: "all punctuation", in: "...", want: "___"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mangle(tt.in)
			if got != tt.want {
				t.Errorf("mangle(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if ic10.IsReservedWord(got) {
				t.Errorf("mangle(%q) = %q, which the assembler resolves to something of its own", tt.in, got)
			}
			for i := range len(got) {
				c := got[i]
				identifier := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
				if !identifier {
					t.Errorf("mangle(%q) = %q, which contains %q", tt.in, got, string(c))
				}
			}
		})
	}
}

// TestMangleNoReservedWordSurvives runs every name the assembler already knows
// through mangling, which is the property IsReservedWord exists to give.
func TestMangleNoReservedWordSurvives(t *testing.T) {
	names := []string{"db", "sp", "ra"}
	for _, instruction := range ic10.Instructions {
		names = append(names, instruction.Mnemonic)
	}
	for _, logicType := range ic10.LogicTypes {
		names = append(names, logicType.Name)
	}
	for _, slotType := range ic10.LogicSlotTypes {
		names = append(names, slotType.Name)
	}
	for _, mode := range ic10.BatchModes {
		names = append(names, mode.Name)
	}
	for _, mode := range ic10.ReagentModes {
		names = append(names, mode.Name)
	}
	for _, constant := range ic10.Constants {
		names = append(names, constant.Name)
	}
	for register := range ic10.Register(ic10.NumRegisters) {
		names = append(names, register.String())
	}
	for _, name := range names {
		if got := mangle(name); ic10.IsReservedWord(got) {
			t.Errorf("mangle(%q) = %q, still reserved", name, got)
		}
	}
}

// TestMangleLabelsResolvesCollisions covers two distinct labels that mangle to
// the same name. Mangling collapses every character outside the identifier set,
// so "a.b" and "a_b" arrive at the same candidate and one of them has to move.
func TestMangleLabelsResolvesCollisions(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
	}{
		{name: "punctuation collapses onto an existing name", labels: []string{"a.b", "a_b"}},
		{name: "three way collision", labels: []string{"a.b", "a-b", "a b"}},
		{name: "collision with the disambiguated name", labels: []string{"a.b", "a_b", "a_b_1"}},
		{name: "reserved word and its escaped form", labels: []string{"Temperature", "Temperature_"}},
		{name: "no collisions", labels: []string{"main.entry", "main.loop", "helper.entry"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog := &mir.Program{Funcs: []*mir.Func{buildLabelledFunc(t, tt.labels)}}
			names := mangleLabels(prog)
			if len(names) != len(tt.labels) {
				t.Fatalf("mangleLabels returned %d names for %d labels", len(names), len(tt.labels))
			}
			seen := make(map[string]string, len(names))
			for _, label := range tt.labels {
				emitted, ok := names[label]
				if !ok {
					t.Fatalf("mangleLabels has no name for %q", label)
				}
				if prior, dup := seen[emitted]; dup {
					t.Errorf("labels %q and %q both emit as %q", prior, label, emitted)
				}
				seen[emitted] = label
				if ic10.IsReservedWord(emitted) {
					t.Errorf("label %q emits as %q, which is reserved", label, emitted)
				}
			}
		})
	}
}

// TestMangleLabelsIsDeterministic keeps the emitted names stable across runs,
// which is what makes the golden files meaningful.
func TestMangleLabelsIsDeterministic(t *testing.T) {
	labels := []string{"a.b", "a_b", "Temperature", "add", "2fast"}
	first := mangleLabels(&mir.Program{Funcs: []*mir.Func{buildLabelledFunc(t, labels)}})
	for range 16 {
		again := mangleLabels(&mir.Program{Funcs: []*mir.Func{buildLabelledFunc(t, labels)}})
		for label, name := range first {
			if again[label] != name {
				t.Fatalf("label %q mangled to %q and then to %q", label, name, again[label])
			}
		}
	}
}

func buildLabelledFunc(t *testing.T, labels []string) *mir.Func {
	t.Helper()
	fn := mir.NewFunc("f", position(1))
	for i, label := range labels {
		block := fn.NewBlock(label, position(i+1))
		block.Append(instr(t, ic10.OpYield))
	}
	return fn
}
