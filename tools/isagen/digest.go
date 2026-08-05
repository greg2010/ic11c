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

// gameTypes maps the game's C# onto the Go that transliterates it: game-derived
// behaviour encoded as Go control flow rather than as a table, so `isa:extract`
// cannot reach it and a game update that changes it is invisible to the
// build. See docs/game-updates.md for what this does and does not cover.
var gameTypes = []gameType{
	{
		name: "Assets.Scripts.Objects.Electrical.ProgrammableChip",
		backs: []string{
			"internal/emit/render.go",
			"internal/ic10/ic10.go",
			"internal/ic10/quirks.go",
		},
		note: "_Registers and _Stack fix the register and memory counts, _LineOfCode is the compile-time arity check the never-emit list is derived from, and the Variable family is what decides which device and number spellings the emitter may write",
	},
	{
		name:  "Assets.Scripts.Objects.Electrical.ProgrammableChipException",
		backs: []string{"internal/chip/errors.go"},
		note:  "ICExceptionType ordinals are the ExceptionType constants and are what a differential comparison matches on",
	},
	{
		name: "Assets.Scripts.Objects.Electrical.CircuitHousing",
		backs: []string{
			"internal/chip/run.go",
			"internal/emit/render.go",
			"internal/ic10/ic10.go",
		},
		note: "RUN_COUNT is the per-tick instruction budget, the Devices array fixes the pin count, and GetLogicableFromIndex is what decides which connection and pin indices an emitted device operand may carry",
	},
	{
		name: "Assets.Scripts.UI.InputSourceCode",
		backs: []string{
			"internal/difftest/generate.go",
			"internal/emit/report.go",
		},
		note: "MAX_FILE_SIZE, MAX_LINES and LINE_LENGTH_LIMIT are the editor's program budget, which the chip itself does not enforce, and UpdateFileSize is how the byte budget is charged",
	},
	{
		name: "Assets.Scripts.Objects.Electrical.AsciiString",
		backs: []string{
			"internal/difftest/generate.go",
			"internal/emit/report.go",
		},
		note: "ParseLine is the cut every line reaches the editor's grid through, so it is what a line wider than the limit is charged and reported at",
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
// outDir, laid out by namespace the way it was decompiled. This is what a
// reviewer diffs once the digest has told them a declaration moved. It is
// deliberately not checked in: the container can produce it on demand.
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
