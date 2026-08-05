package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gameSource is the decompile the slice is cut from, relative to this package.
const gameSource = "../../" + defaultSourcePath

// The tests below that read the decompile, named as functions so that deleting or
// renaming one is a compile error. A gate that is gone skips nothing and appears
// in no listing, so the CI job that refuses a skip cannot see it go. A gate added
// below belongs here too.
var _ = []func(*testing.T){
	TestSliceTheGameSource,
	TestReagentSetterCoversTheLiftedLookup,
	TestKeepListsMatchTheGameSource,
	TestSliceLeavesOnlyWhatItWrote,
	TestTheDigestNoticesAChangedBody,
}

// requireDecompile skips a gate with no decompile to read, naming a file the
// slice needs rather than the tree, so a partial decompile skips rather than
// failing three frames down.
func requireDecompile(t *testing.T, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(gameSource, filepath.FromSlash(rel))); err != nil {
		t.Skipf("no decompile under %s", gameSource)
	}
}

func TestReagentRoster(t *testing.T) {
	const reagentFile = `namespace Reagents;

public class Reagent
{
	public static List<Reagent> AllReagents;

	static Reagent()
	{
		AllReagents = new List<Reagent>
		{
			new Iron(0.0),
			new Gold(0.0)
		};
	}
}
`
	tests := []struct {
		name    string
		text    string
		want    []string
		wantErr string
	}{
		{name: "roster in source order", text: reagentFile, want: []string{"Iron", "Gold"}},
		{
			name:    "a repeated reagent refuses",
			text:    strings.Replace(reagentFile, "new Gold(0.0)", "new Iron(0.0)", 1),
			wantErr: "appears twice",
		},
		{
			name:    "an empty roster refuses",
			text:    strings.Replace(reagentFile, "\t\t\tnew Iron(0.0),\n\t\t\tnew Gold(0.0)\n", "", 1),
			wantErr: "names no reagents",
		},
		{
			name:    "a roster built somewhere else refuses",
			text:    strings.Replace(reagentFile, "AllReagents = new List<Reagent>", "AllReagentsLater = new List<Reagent>", 1),
			wantErr: "not found",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			src, err := writeTree(t, map[string]string{reagentPath: test.text}).read(reagentPath)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			reagent, err := src.topLevelType("Reagent")
			if err != nil {
				t.Fatalf("topLevelType: %v", err)
			}
			got, err := reagentRoster(src, reagent)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("reagentRoster error = %v, want one containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("reagentRoster: %v", err)
			}
			if strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Errorf("reagentRoster = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSliceRefusesAnAbsentDecompile(t *testing.T) {
	if _, err := slice(t.TempDir(), filepath.Join(t.TempDir(), "out"), ""); !errors.Is(err, errNotFound) {
		t.Errorf("slice over an empty tree error = %v, want errNotFound", err)
	}
}

// The slice against the real decompile, the only test exercising the keep lists
// against the game as it is today. It is skipped rather than failed when the tree
// is absent, since a working copy that has not fetched it is not a broken slicer.
func TestSliceTheGameSource(t *testing.T) {
	requireDecompile(t, chipPath)
	out := t.TempDir()
	summary, err := slice(gameSource, out, "")
	if err != nil {
		t.Fatalf("slice: %v", err)
	}

	for _, name := range []string{chipFile, supportFile, deviceFile, reagentFile, harnessFile} {
		info, err := os.Stat(filepath.Join(out, name))
		if err != nil {
			t.Errorf("stat %s: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
	}

	// hcf is the one operation with no standalone definition; anything else
	// dropping means an operation stopped compiling unnoticed.
	if !strings.Contains(summary, hcfOperation) {
		t.Errorf("summary does not report the dropped operation:\n%s", summary)
	}

	chip, err := os.ReadFile(filepath.Join(out, chipFile))
	if err != nil {
		t.Fatalf("read %s: %v", chipFile, err)
	}
	text := string(chip)
	absent := []struct {
		what string
		text string
	}{
		{"the base list", "class ProgrammableChip : "},
		{"the parent-slot refresh", "base.ParentSlot"},
		{"the unreferenced device setter", "_SetDeviceValue("},
		{"the dropped operation", "_HCF_Operation"},
		{"the base-qualified network flag", "base.NetworkUpdateFlags"},
		{"a help-page renderer", "MakePage"},
		{"the unseeded random source", "_RandomNumberGenerator"},
	}
	for _, check := range absent {
		if strings.Contains(text, check.text) {
			t.Errorf("%s survives into %s", check.what, chipFile)
		}
	}
	present := []string{
		"public void SetSourceCode(string sourceCode)",
		"public void Execute(int runCount)",
		"public ICircuitHolder CircuitHousing;",
		"AllConstants = new Constant[",
		// Why the tick ended, which the chip otherwise destroys, and the draw
		// redirected onto a source a run can arm.
		"public bool HarnessBudgetExhausted;",
		executeRecord,
		randRedirect,
	}
	for _, want := range present {
		if !strings.Contains(text, want) {
			t.Errorf("%s does not contain %q", chipFile, want)
		}
	}

	support, err := os.ReadFile(filepath.Join(out, supportFile))
	if err != nil {
		t.Fatalf("read %s: %v", supportFile, err)
	}
	// The housing derives from the device shim and answers db with itself, neither
	// of which is visible in a slice that merely compiles.
	housing := []string{
		"public class CircuitHousing : Device, ICircuitHolder",
		"if (deviceIndex == int.MaxValue)",
		"public override bool CanLogicRead(LogicType logicType)",
		"public override void SetLogicValue(LogicType logicType, double value)",
		// The two statics a run arms: the clock a reading advances, and the
		// generator the redirected draw comes out of.
		"public static void SetClock(float now, float step)",
		"public static class HarnessRandom",
	}
	for _, want := range housing {
		if !strings.Contains(string(support), want) {
			t.Errorf("%s does not contain %q", supportFile, want)
		}
	}

	// The device an lr Required or Recipe and the whole of rmap dispatches through,
	// plus the write side of the two per-reagent tables it holds.
	device, err := os.ReadFile(filepath.Join(out, deviceFile))
	if err != nil {
		t.Fatalf("read %s: %v", deviceFile, err)
	}
	if !strings.Contains(string(device), "public class ReagentUser : Device, IRequireReagent") {
		t.Errorf("%s declares no reagent user", deviceFile)
	}

	reagents, err := os.ReadFile(filepath.Join(out, reagentFile))
	if err != nil {
		t.Fatalf("read %s: %v", reagentFile, err)
	}
	if setters := strings.Count(string(reagents), "public bool HarnessSet(Reagent reagent, double quantity)"); setters != len(reagentContainers) {
		t.Errorf("%s carries %d per-reagent setters, want one per container (%d)",
			reagentFile, setters, len(reagentContainers))
	}
}

// The generated setter has to name the same reagents the lifted lookup beside it
// does. A setter with a shorter chain writes a seed nowhere and reads back zero.
func TestReagentSetterCoversTheLiftedLookup(t *testing.T) {
	requireDecompile(t, reagentPath)
	text, err := sliceReagents(newSlicing(gameSource))
	if err != nil {
		t.Fatalf("sliceReagents: %v", err)
	}
	for _, container := range reagentContainers {
		body, ok := containerBody(text, container.name)
		if !ok {
			t.Errorf("no %s in the emitted reagents", container.name)
			continue
		}
		setter := strings.Index(body, "public bool HarnessSet(Reagent reagent, double quantity)")
		if setter < 0 {
			t.Errorf("%s has no generated setter", container.name)
			continue
		}
		read, written := reagentArms(body[:setter]), reagentArms(body[setter:])
		if len(read) == 0 {
			t.Errorf("%s has no lifted reagent lookup at all", container.name)
			continue
		}
		if strings.Join(read, " ") != strings.Join(written, " ") {
			t.Errorf("%s reads %d reagents and writes %d, and the two do not agree:\n  read:    %s\n  written: %s",
				container.name, len(read), len(written), strings.Join(read, " "), strings.Join(written, " "))
		}
		for _, reagent := range written {
			if arm := container.assign(reagent); !strings.Contains(body[setter:], arm) {
				t.Errorf("%s's setter tests for %s without writing %q", container.name, reagent, arm)
			}
		}
	}
}

// reagentArms lists the reagents a chain of type tests names, in order.
func reagentArms(text string) []string {
	const marker = "if (reagent is "
	var names []string
	for _, after := range strings.Split(text, marker)[1:] {
		name, _, ok := strings.Cut(after, ")")
		if !ok {
			continue
		}
		names = append(names, name)
	}
	return names
}

// containerBody returns the text of one emitted top-level type.
func containerBody(text, name string) (string, bool) {
	start := strings.Index(text, "public struct "+name+"\n")
	if start < 0 {
		start = strings.Index(text, "public class "+name+"\n")
	}
	if start < 0 {
		return "", false
	}
	body, _, err := matchDelim(text[start:], 0, '{', '}')
	return body, err == nil
}

// Every keep list entry has to match exactly one declaration. Nothing else holds
// the lists to the source, so a stale entry sits there until a compile fails.
func TestKeepListsMatchTheGameSource(t *testing.T) {
	requireDecompile(t, chipPath)
	tests := []struct {
		name string
		path string
		typ  string
		want []string
	}{
		{name: "chip", path: chipPath, typ: "ProgrammableChip", want: chipMembers},
		{name: "reagent", path: reagentPath, typ: "Reagent", want: reagentMembers},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			src, err := newSlicing(gameSource).read(test.path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			container, err := src.topLevelType(test.typ)
			if err != nil {
				t.Fatalf("topLevelType: %v", err)
			}
			for _, signature := range test.want {
				if _, err := src.top().scopeOf(container).member(signature); err != nil {
					t.Errorf("%s.%v", test.typ, err)
				}
			}
		})
	}
}
