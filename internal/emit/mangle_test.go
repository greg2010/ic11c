package emit

import (
	"testing"

	"github.com/greg2010/ic11c/internal/isa"
	"github.com/greg2010/ic11c/internal/mir"
)

// TestMangle covers what the comment a name is written into cannot survive: a
// name spanning two lines, reading as two words, or counting more bytes than
// characters. A name spelled like one of the chip's own is left alone, since the
// chip cuts the line at its first '#' before it resolves anything.
func TestMangle(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "already one word", in: "main_loop", want: "main_loop"},
		{name: "dots become underscores", in: "main.loop", want: "main_loop"},
		{name: "punctuation becomes underscores", in: "f<int>::body", want: "f_int___body"},
		{name: "a space would read as two names", in: "main loop", want: "main_loop"},
		{name: "a newline would make one line two", in: "main\nloop", want: "main_loop"},
		{name: "a byte outside ASCII would cost more than a character", in: "café", want: "caf__"},
		{name: "a comment character is not one", in: "main#loop", want: "main_loop"},
		{name: "logic type", in: "Temperature", want: "Temperature"},
		{name: "mnemonic", in: "add", want: "add"},
		{name: "register", in: "r0", want: "r0"},
		{name: "constant", in: "pi", want: "pi"},
		{name: "leading digit", in: "2fast", want: "2fast"},
		{name: "empty", in: "", want: "_"},
		{name: "all punctuation", in: "...", want: "___"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mangle(tt.in)
			if got != tt.want {
				t.Errorf("mangle(%q) = %q, want %q", tt.in, got, tt.want)
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

// TestMangleLabelsResolvesCollisions covers two distinct labels that mangle to
// one name, which would annotate two lines as the same block. The emitted names
// are pinned rather than only checked for distinctness: every spelling a
// numbering scheme could produce is pairwise distinct, so that holds none of them.
func TestMangleLabelsResolvesCollisions(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		// want is the emitted name of each label in labels, in that order.
		want []string
	}{
		{
			name:   "punctuation collapses onto an existing name",
			labels: []string{"a.b", "a_b"},
			want:   []string{"a_b", "a_b_1"},
		},
		{
			name:   "three way collision",
			labels: []string{"a.b", "a-b", "a b"},
			want:   []string{"a_b", "a_b_1", "a_b_2"},
		},
		{
			// The label a collision would be numbered into is already a label of
			// its own, so the numbering has to run again on the numbered name.
			name:   "collision with the disambiguated name",
			labels: []string{"a.b", "a_b", "a_b_1"},
			want:   []string{"a_b", "a_b_1", "a_b_1_1"},
		},
		{
			name:   "no collisions",
			labels: []string{"main.entry", "main.loop", "helper.entry"},
			want:   []string{"main_entry", "main_loop", "helper_entry"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog := &mir.Program{Funcs: []*mir.Func{buildLabelledFunc(t, tt.labels)}}
			names := mangleLabels(prog)
			if len(names) != len(tt.labels) {
				t.Fatalf("mangleLabels returned %d names for %d labels", len(names), len(tt.labels))
			}
			seen := make(map[string]string, len(names))
			for i, label := range tt.labels {
				emitted, ok := names[label]
				if !ok {
					t.Fatalf("mangleLabels has no name for %q", label)
				}
				if emitted != tt.want[i] {
					t.Errorf("label %q emits as %q, want %q", label, emitted, tt.want[i])
				}
				if prior, dup := seen[emitted]; dup {
					t.Errorf("labels %q and %q both emit as %q", prior, label, emitted)
				}
				seen[emitted] = label
			}
		})
	}
}

// TestMangleLabelsIsDeterministic keeps the emitted names stable across runs.
// Numbering a collision walks a map of the names taken so far, and a name that
// moved between two runs of one program would annotate a line differently each
// time it was compiled.
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
		block.Append(instr(t, isa.OpYield))
	}
	return fn
}
