package emit

import (
	"strconv"
	"strings"

	"github.com/greg2010/ic11c/internal/mir"
)

// mangle turns a MIR block label into a name that reads as one word inside the comment readable
// output annotates a line with. The name never reaches the chip's assembler — ProgrammableChip cuts
// a line at its first '#' before splitting it — so this is only about keeping the label on one line:
// a space would read as two names, a newline would split the line, and non-ASCII would disagree with
// the character count the editor truncates at. Length is unbounded; a long name can push its line
// past the 90 character width, but that costs bytes, not the program.
func mangle(name string) string {
	var b strings.Builder
	b.Grow(len(name) + 1)
	for i := range len(name) {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteByte(c)
		default:
			b.WriteByte('_')
		}
	}
	// An empty label would annotate its line with a bare colon, which reads as
	// a block whose name went missing rather than as one that never had one.
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// mangleLabels assigns every block in prog an emitted name, in program order so the assignment is
// deterministic. Two distinct labels can mangle to the same name (mangling collapses every character
// outside the identifier set to an underscore), so a collision is resolved by numbering rather than reported.
func mangleLabels(prog *mir.Program) map[string]string {
	names := make(map[string]string)
	taken := make(map[string]bool)
	for _, fn := range prog.Funcs {
		for _, block := range fn.Blocks {
			candidate := mangle(block.Label)
			for suffix := 1; taken[candidate]; suffix++ {
				candidate = mangle(block.Label + "_" + strconv.Itoa(suffix))
			}
			taken[candidate] = true
			names[block.Label] = candidate
		}
	}
	return names
}
