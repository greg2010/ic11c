package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// gameType is one decompiled game type that hand-written Go transliterates.
type gameType struct {
	// name is the fully qualified C# type name, which the decompiled layout
	// maps onto a path.
	name string
	// backs names the hand-written Go files whose behaviour derives from this
	// type, in the order they are written to the digest.
	backs []string
	// note says what about the type is load bearing, for the cases where the
	// type name alone does not.
	note string
}

// gameTypes maps the game's C# onto the Go that transliterates it.
//
// Everything named here is game-derived behaviour encoded as Go control flow
// rather than as a table, so `isa:extract` cannot reach it and a game update
// that changes it is invisible to the build. The list is derived from the
// provenance comments the transliterations carry; see docs/game-updates.md for
// what it does and does not cover.
var gameTypes = []gameType{
	{
		name: "Assets.Scripts.Objects.Electrical.ProgrammableChip",
		backs: []string{
			"internal/ic10/ic10.go",
			"internal/ic10/quirks.go",
			"internal/vm/builders.go",
			"internal/vm/convert.go",
			"internal/vm/enums.go",
			"internal/vm/instructions.go",
			"internal/vm/machine.go",
			"internal/vm/operand.go",
			"internal/vm/ops_alu.go",
			"internal/vm/ops_branch.go",
			"internal/vm/ops_control.go",
			"internal/vm/ops_device.go",
			"internal/vm/ops_stack.go",
			"internal/vm/parse.go",
		},
		note: "the nested _*_Operation classes are the per-opcode semantics, the Variable family is operand resolution, _LineOfCode is the compile-time arity check, and _Registers and _Stack fix the register and memory counts",
	},
	{
		name:  "Assets.Scripts.Objects.Electrical.ProgrammableChipException",
		backs: []string{"internal/vm/errors.go"},
		note:  "ICExceptionType ordinals are the ExceptionType constants and are what a differential comparison matches on",
	},
	{
		name: "Assets.Scripts.Objects.Electrical.CircuitHousing",
		backs: []string{
			"internal/ic10/ic10.go",
			"internal/ic10/quirks.go",
			"internal/vm/device.go",
			"internal/vm/machine.go",
		},
		note: "GetLogicableFromIndex and GetLogicableFromId are device resolution, RUN_COUNT is the per-tick instruction budget, and the Devices array fixes the pin count",
	},
	{
		name:  "Assets.Scripts.Objects.Electrical.InstructionInclude",
		backs: []string{"internal/vm/operand.go"},
		note:  "the flag values the instructionInclude masks are built from",
	},
	{
		name:  "Assets.Scripts.Objects.Electrical.IScriptEnum",
		backs: []string{"internal/vm/enums.go", "internal/vm/operand.go"},
		note:  "the interface InternalEnums resolves an operand through",
	},
	{
		name:  "Assets.Scripts.Objects.Electrical.LogicBatchMethod",
		backs: []string{"internal/vm/device.go"},
		note:  "the ordinals batchRead switches on",
	},
	{
		name:  "Assets.Scripts.Objects.Electrical.LogicReagentMode",
		backs: []string{"internal/vm/device.go", "internal/vm/ops_device.go"},
	},
	{
		name:  "Assets.Scripts.Objects.Pipes.Device",
		backs: []string{"internal/vm/device.go", "internal/vm/ops_device.go"},
		note:  "BatchRead is the aggregation behind lb, lbn, lbs and lbns; the rest of the type is unrelated, which makes this the noisiest entry here",
	},
	{
		name:  "Assets.Scripts.Objects.Pipes.ILogicable",
		backs: []string{"internal/vm/device.go"},
		note:  "the read and write gates the Device interface models",
	},
	{
		name:  "Assets.Scripts.Objects.Pipes.ISlotWriteable",
		backs: []string{"internal/vm/device.go"},
	},
	{
		name:  "Assets.Scripts.Objects.Pipes.IMemoryReadable",
		backs: []string{"internal/vm/device.go", "internal/vm/ops_stack.go"},
	},
	{
		name:  "Assets.Scripts.Objects.Pipes.IMemoryWritable",
		backs: []string{"internal/vm/device.go", "internal/vm/ops_stack.go"},
	},
	{
		name:  "Assets.Scripts.Util.Regexes",
		backs: []string{"internal/vm/parse.go"},
		note:  "the preprocessor and comment patterns the chip rewrites a line with",
	},
	{
		name:  "Assets.Scripts.UI.InputSourceCode",
		backs: []string{"internal/emit/report.go"},
		note:  "MAX_FILE_SIZE, MAX_LINES and LINE_LENGTH_LIMIT are the editor's program budget, which the chip itself does not enforce",
	},
}

// digestHeader introduces the file for whoever opens it without having read the
// procedure first.
const digestHeader = `# Fingerprints of the game C# that hand-written Go transliterates.
#
# One record per declaration: the leading eight bytes of a SHA-256 over the
# declaration with its layout whitespace removed. A record says that something
# changed, not what; read docs/game-updates.md before acting on one.
#
# Regenerate with ` + "`task isa:csharp`" + `. Nothing builds or tests against this
# file: it is a review aid, and a change here is a prompt to read a diff.
`

// writeDigest fingerprints the mapped types in sourceDir and writes the result
// to outPath.
func writeDigest(sourceDir, assembly, manifest, outPath string) error {
	if sourceDir == "" || assembly == "" || manifest == "" {
		return errors.New("digest needs --source, --assembly and --manifest")
	}
	tree, err := newSourceTree(sourceDir)
	if err != nil {
		return err
	}
	version, err := readAssemblyVersion(assembly)
	if err != nil {
		return err
	}
	data, err := renderDigest(tree, manifest, version)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", outPath, err)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	return nil
}

// renderDigest fingerprints every mapped type in tree. Records within a type
// are sorted by path, so a declaration moving within its type is not a diff:
// declaration order is not behaviour anywhere the mapping reaches, and enums,
// where it is, are fingerprinted whole rather than split.
func renderDigest(tree *sourceTree, manifest, version string) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString(digestHeader)
	fmt.Fprintf(&out, "manifest %s\nassembly %s\n", manifest, version)

	for _, gt := range gameTypes {
		src, err := tree.qualified(gt.name)
		if err != nil {
			return nil, err
		}
		short := gt.name[strings.LastIndexByte(gt.name, '.')+1:]
		decl, err := topLevelType(src, short)
		if err != nil {
			return nil, err
		}
		var records []declRecord
		if decl.kind == declContainer {
			if err := walkDecls("", decl.body, &records); err != nil {
				return nil, fmt.Errorf("type %s: %w", gt.name, err)
			}
		}
		slices.SortFunc(records, func(a, b declRecord) int { return strings.Compare(a.Path, b.Path) })

		fmt.Fprintf(&out, "\ntype %s %s\n", declDigest(decl.text), gt.name)
		fmt.Fprintf(&out, "backs %s\n", strings.Join(gt.backs, " "))
		if gt.note != "" {
			fmt.Fprintf(&out, "note %s\n", gt.note)
		}
		for _, record := range records {
			fmt.Fprintf(&out, "\t%s %s\n", record.Digest, record.Path)
		}
	}
	return out.Bytes(), nil
}

// copyGameSource writes the decompiled source of every mapped type under
// outDir, laid out by namespace the way it was decompiled.
//
// This is what a reviewer diffs once the digest has told them a declaration
// moved. It is deliberately not checked in: it is a few hundred kilobytes of
// decompiled proprietary game code, and the container can produce it for any
// manifest on demand.
func copyGameSource(sourceDir, outDir string) error {
	if sourceDir == "" || outDir == "" {
		return errors.New("dump needs --source and --out")
	}
	tree, err := newSourceTree(sourceDir)
	if err != nil {
		return err
	}
	for _, gt := range gameTypes {
		src, err := tree.qualified(gt.name)
		if err != nil {
			return err
		}
		path := filepath.Join(outDir, filepath.Join(strings.Split(gt.name, ".")...)+csharpExt)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create directory for %s: %w", path, err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}
