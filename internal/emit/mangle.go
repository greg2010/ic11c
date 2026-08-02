package emit

import (
	"strconv"
	"strings"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/mir"
)

// mangle turns a MIR block label into a name the chip's assembler resolves to
// our label and to nothing of its own.
//
// The chip does not reject a label that shadows one of its own names. It
// resolves the name to the built-in and every instruction referring to the
// label then faults at runtime, once per tick, indefinitely. ic10.IsReservedWord
// covers the whole LogicType, LogicSlotType, batch mode, reagent mode,
// mnemonic, register, device and constant space, and compares
// case-insensitively so a differently cased near-miss is still avoided.
//
// A name that would start with a digit gains an underscore rather than a
// letter, because every single letter the assembler might be given is either a
// mnemonic or a register prefix.
func mangle(name string) string {
	var b strings.Builder
	b.Grow(len(name) + 1)
	for i := range len(name) {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
			b.WriteByte(c)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" || (out[0] >= '0' && out[0] <= '9') {
		out = "_" + out
	}
	for ic10.IsReservedWord(out) {
		out += "_"
	}
	return out
}

// mangleLabels assigns every block in prog an emitted name, in program order so
// the assignment is deterministic.
//
// Two distinct labels can mangle to the same name, since mangling collapses
// every character outside the identifier set to an underscore. A collision is
// resolved by numbering rather than reported, because the label text comes from
// the front end and a program is not wrong for containing both "a.b" and "a_b".
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
