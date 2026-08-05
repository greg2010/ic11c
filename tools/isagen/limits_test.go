package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/greg2010/ic11c/internal/chip"
	"github.com/greg2010/ic11c/internal/emit"
	"github.com/greg2010/ic11c/internal/ic10"
)

// gameSourceEnv names a decompiled tree to read instead of the default, which is
// how two game builds are compared.
const gameSourceEnv = "IC11C_GAMESRC"

// gameSourceDirs are the trees this looks for, in order. The full decompiled
// assembly is first because it is the only one the recovery can run over; the
// subset `task isa:csharp:source` dumps holds only the few dozen types a
// reviewer diffs across builds, which still answers for the limits below.
var gameSourceDirs = []string{
	filepath.Join("..", "..", "gamesrc", "source-full"),
	filepath.Join("..", "..", "gamesrc", "source"),
}

// Fully qualified names of the game types the limits are read from.
const (
	typeCircuitHousing  = "Assets.Scripts.Objects.Electrical.CircuitHousing"
	typeInputSourceCode = "Assets.Scripts.UI.InputSourceCode"
)

// TestMachineLimitsMatchTheGameSource holds the machine's shape -- registers,
// memory, device pins, sp and ra, the tick budget, the editor's limits -- to the
// declarations it mirrors. Unlike every table in internal/ic10 those constants
// are hand written and derived from nothing, so this is their only gate.
func TestMachineLimitsMatchTheGameSource(t *testing.T) {
	tree := gameTree(t)

	tests := []struct {
		name string
		// typeName is the game type the declaration sits in, and pattern the
		// declaration with the value as its one capture.
		typeName string
		pattern  *regexp.Regexp
		got      int
	}{
		{
			name:     "the register file",
			typeName: typeProgrammableChip,
			pattern:  regexp.MustCompile(`\b_Registers\s*=\s*new double\[(\d+)\]`),
			got:      ic10.NumRegisters,
		},
		{
			name:     "the stack pointer",
			typeName: typeProgrammableChip,
			pattern:  regexp.MustCompile(`_StackPointerIndex\s*=\s*(\d+)`),
			got:      int(ic10.RegSP),
		},
		{
			name:     "the return address register",
			typeName: typeProgrammableChip,
			pattern:  regexp.MustCompile(`_ReturnAddressIndex\s*=\s*(\d+)`),
			got:      int(ic10.RegRA),
		},
		{
			name:     "the memory array",
			typeName: typeProgrammableChip,
			pattern:  regexp.MustCompile(`_Stack\s*=\s*new double\[(\d+)\]`),
			got:      ic10.NumMemorySlots,
		},
		{
			name:     "the device pins",
			typeName: typeCircuitHousing,
			pattern:  regexp.MustCompile(`Devices\s*=\s*new ILogicable\[(\d+)\]`),
			got:      ic10.NumDevicePins,
		},
		{
			name:     "the instruction budget a tick buys",
			typeName: typeCircuitHousing,
			pattern:  regexp.MustCompile(`const int RUN_COUNT\s*=\s*(\d+)`),
			got:      chip.InstructionsPerTick,
		},
		{
			name:     "the program size the editor submits",
			typeName: typeInputSourceCode,
			pattern:  regexp.MustCompile(`const int MAX_FILE_SIZE\s*=\s*(\d+)`),
			got:      emit.MaxBytes,
		},
		{
			name:     "the editor's line count",
			typeName: typeInputSourceCode,
			pattern:  regexp.MustCompile(`const int MAX_LINES\s*=\s*(\d+)`),
			got:      emit.MaxLines,
		},
		{
			name:     "the editor's line width",
			typeName: typeInputSourceCode,
			pattern:  regexp.MustCompile(`const int LINE_LENGTH_LIMIT\s*=\s*(\d+)`),
			got:      emit.MaxLineLength,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, err := tree.qualified(tt.typeName)
			if err != nil {
				t.Fatalf("read %s: %v", tt.typeName, err)
			}
			all := tt.pattern.FindAllStringSubmatch(src, -1)
			switch len(all) {
			case 0:
				t.Fatalf("%s declares nothing matching %s, so the constant beside it is checked against nothing", tt.typeName, tt.pattern)
			case 1:
			default:
				// Taking the first would let a second declaration decide which
				// one the constant is held to by where it happens to sit.
				t.Fatalf("%s declares %d things matching %s, and which one the constant mirrors is not something this reads", tt.typeName, len(all), tt.pattern)
			}
			m := all[0]
			want, err := strconv.Atoi(m[1])
			if err != nil {
				t.Fatalf("%s declares %q, which is not a number: %v", tt.typeName, m[1], err)
			}
			if tt.got != want {
				t.Errorf("the compiler carries %d, and %s declares %d", tt.got, tt.typeName, want)
			}
		})
	}
}

// openGameSource indexes the first decompiled tree it finds. An absent tree is
// an error rather than an absent result: a gate that stops running quietly is
// one nobody notices has stopped.
func openGameSource() (*sourceTree, error) {
	dirs := gameSourceDirs
	if named := os.Getenv(gameSourceEnv); named != "" {
		dirs = []string{named}
	}
	for _, dir := range dirs {
		tree, err := newSourceTree(dir)
		switch {
		case errors.Is(err, os.ErrNotExist), errors.Is(err, errNotFound):
			continue
		case err != nil:
			return nil, fmt.Errorf("read the decompiled source under %s: %w", dir, err)
		}
		return tree, nil
	}
	return nil, fmt.Errorf("no decompiled game source under %v: `task gamesrc` writes one, and %s names another",
		dirs, gameSourceEnv)
}
