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

// PreludeDirName is the directory, relative to a directory holding
// sources, that Prelude is written into: a dot directory because what
// lands there is generated and version-stamped. CompileFlags does not go
// there — clangd walks up from the opened source, so one at the tree's top
// configures every directory under it.
const PreludeDirName = ".ic11c"

// The prefix each operand family spells a name with, where an earlier
// family already took the bare spelling. C admits one enumerator per name
// per scope, so Prelude gives the bare name to the first family that
// carries it and prefixes every later one; see
// [TestPreludeEnumeratorsResolveAsMicroCDoes].
const (
	LogicTypePrefix   = "LogicType_"
	SlotTypePrefix    = "SlotType_"
	BatchModePrefix   = "BatchMode_"
	ReagentModePrefix = "ReagentMode_"
)

// Prelude is a C header declaring everything a MicroC program may name:
// device pins, the four operand enums, machine constants, and intrinsics —
// so a C editor can parse and complete a MicroC source file. Generated from
// the same ISA data as the machine tables, so it cannot describe a
// different machine from the compiler; what it declares is a strict
// superset of MicroC.
//
//go:embed generated/ic10_prelude.h
var Prelude string

// CompileFlags is the argument file that pairs with Prelude, one argument per
// line, in the form clangd and `clang @file` read.
//
//go:embed generated/compile_flags.txt
var CompileFlags string

// CompileFlagsIncluding returns CompileFlags with its -include argument
// replaced by header, every other argument preserved verbatim. The
// separator is always a slash, since the argument is read by a C driver
// rather than by the operating system.
func CompileFlagsIncluding(header string) (string, error) {
	return compileFlagsIncluding(CompileFlags, header)
}

// compileFlagsIncluding substitutes the header path in the argument file
// flags. The generator writes the bare file name last and nothing else
// after it, so replacing that suffix is enough; any other shape is
// reported rather than appended to, which would otherwise leave two
// -include arguments with no way to tell which the driver used.
func compileFlagsIncluding(flags, header string) (string, error) {
	base, ends := strings.CutSuffix(flags, PreludeFileName+"\n")
	if !ends {
		return "", fmt.Errorf("%s does not end with %s, which is the argument this replaces:\n%s", CompileFlagsFileName, PreludeFileName, flags)
	}
	return base + header + "\n", nil
}
