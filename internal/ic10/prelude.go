package ic10

import (
	_ "embed"
	"fmt"
	"strings"
)

// PreludeFileName is the name Prelude has to be written under. CompileFlags
// names it relatively, and a C driver resolves that against the directory
// holding the flags file rather than against the working directory.
const PreludeFileName = "ic10_prelude.h"

// CompileFlagsFileName is the name clangd looks for when a project has no
// compilation database.
const CompileFlagsFileName = "compile_flags.txt"

// PreludeDirName is the directory, relative to a directory holding sources,
// that Prelude is written into. It is a dot directory because what lands there
// is generated and stamped with the game version it was extracted from, so it
// is neither read nor edited beside the sources.
//
// CompileFlags does not go there: clangd finds a flags file by walking up from
// the source it opens, so one at the top of a source tree configures every
// directory under it.
const PreludeDirName = ".ic11c"

// Prelude is a C header declaring everything a MicroC program may name: the
// device pins, the four operand enums, the machine constants and the
// intrinsics. It lets a C editor parse and complete a MicroC source file.
//
// It is generated from the same ISA data as the machine tables, so it cannot
// describe a different machine from the compiler. What it describes is a
// superset of MicroC: a C toolchain accepts every program MicroC accepts, and
// some it does not.
//
//go:embed ic10_prelude.h
var Prelude string

// CompileFlags is the argument file that pairs with Prelude, one argument per
// line, in the form clangd and `clang @file` read.
//
//go:embed compile_flags.txt
var CompileFlags string

// CompileFlagsIncluding returns CompileFlags with its -include argument
// replaced by header, which names the prelude relative to the directory the
// result is written into. Every other argument is preserved verbatim, so the
// flags an editor gets are the ones the conformance gate runs.
//
// The separator is a slash whatever the host, since the argument is read by a C
// driver rather than by the operating system.
func CompileFlagsIncluding(header string) (string, error) {
	return compileFlagsIncluding(CompileFlags, header)
}

// compileFlagsIncluding substitutes the header path in the argument file flags.
//
// The generator writes the bare file name last and nothing else after it, so
// replacing that suffix is enough. A flags file in any other shape is reported
// rather than appended to, which would otherwise leave two -include arguments
// and no way to tell which one the driver used.
func compileFlagsIncluding(flags, header string) (string, error) {
	base, ends := strings.CutSuffix(flags, PreludeFileName+"\n")
	if !ends {
		return "", fmt.Errorf("%s does not end with %s, which is the argument this replaces:\n%s", CompileFlagsFileName, PreludeFileName, flags)
	}
	return base + header + "\n", nil
}
