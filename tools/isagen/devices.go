package main

import (
	"errors"
	"fmt"
	"hash/crc32"
	"slices"
	"strings"
)

// typeReagent is the fully qualified name of the game type the reagent table
// is recovered from.
const typeReagent = "Reagents.Reagent"

// wantReagents is the size the reagent table must have. As with the ISA
// tables, a game update that changes it changes what a program can name, so
// extraction stops and reports rather than writing a table the compiler's
// assumptions disagree with.
const wantReagents = 46

// minPrefabs is a floor, not an exact count, since every game update adds
// things; a roster collapsed to a handful means the walk lost its way. The
// build this was written against ships 1565.
const minPrefabs = 1000

// minLogicEntries and minSlotEntries floor the total logic-surface entries,
// catching a build that moves the surface methods out of the matchers'
// reach: the evaluator then answers every class with a plain no, degrading
// silently to empty lists rather than an error.
const (
	// This build extracts 13426 logic entries.
	minLogicEntries = 3000
	// This build extracts 13356 slot entries.
	minSlotEntries = 3000
)

// minDecidedLogicEntries and minDecidedSlotEntries floor the entries that
// actually say what the game does, catching the case where every entry
// decodes as unknown instead of empty -- equally silent downstream, since a
// consumer reads unknown as "no diagnostic".
const (
	// This build decides 9926 of its logic entries.
	minDecidedLogicEntries = 3000
	// This build decides 13296 of its slot entries.
	minDecidedSlotEntries = 3000
)

// Devices is the canonical extracted description of the game things a
// program names by hash rather than through the instruction set. It is the
// on-disk sibling of [ISA]: same assembly, same run, same manifest/version.
// Every slice is emitted in game declaration order for byte-identical reruns.
type Devices struct {
	// Manifest and Version identify the game build, and have to equal the ISA
	// table's. See [checkSameBuild].
	Manifest string `json:"manifest"`
	Version  string `json:"version"`
	// Reagents is Reagent.AllReagents in game declaration order, which is also
	// ReagentId order: an entry's index is the byte the game serializes it as.
	// Extraction proves that rather than assuming it.
	Reagents []Reagent `json:"reagents"`
	// Prefabs is WorldManager.SourcePrefabs in roster order, one entry per
	// thing the game can spawn.
	Prefabs []Prefab `json:"prefabs"`
}

// Prefab is one thing the game ships, described by everything a chip can
// reach about it. Every prefab is listed, including ones with no logic
// surface, since the name and hash alone make a batch operand checkable. The
// logic and slot surfaces describe a *completed* device, never one mid-build.
type Prefab struct {
	Name string `json:"name"`
	// Hash is Animator.StringToHash of Name, which is the number a batch
	// instruction operand carries.
	Hash int32 `json:"hash"`
	// Title is the English name the game shows, and is a reading aid only.
	Title string `json:"title,omitempty"`
	// CircuitHolder reports whether the thing can hold a programmable chip,
	// which is what makes it reachable as db rather than only through a pin.
	CircuitHolder bool `json:"circuit_holder,omitempty"`
	// CircuitHolderUnknown says the extraction could not settle whether the
	// thing holds a chip, which is distinct from its holding none. A consumer
	// must not read the CircuitHolder beside it as a denial while it is set.
	CircuitHolderUnknown bool `json:"circuit_holder_unknown,omitempty"`
	// Logic lists the properties l, s and the batch forms can reach, in
	// LogicType declaration order. A property absent here is one the device
	// answers nothing for.
	Logic []LogicAccess `json:"logic,omitempty"`
	// Slots lists the declared slots in slot order.
	Slots []PrefabSlot `json:"slots,omitempty"`
	// Modes names the settings the Mode property selects between, in the order
	// a mode number indexes them. It is empty for a thing with no mode state.
	Modes []Mode `json:"modes,omitempty"`
	// ModesUnknown says the thing has mode state whose names the extraction
	// could not resolve, which is distinct from having none.
	ModesUnknown bool `json:"modes_unknown,omitempty"`
}

// PrefabSlot is one slot a prefab declares.
type PrefabSlot struct {
	// Index is the slot number an ls or ss operand carries.
	Index int `json:"index"`
	// Class is the Slot.Class member naming what the slot accepts. Half the
	// slot properties are readable purely by virtue of it.
	Class string `json:"class"`
	// Types lists the slot properties reachable on this slot, in LogicSlotType
	// declaration order.
	Types []LogicAccess `json:"types,omitempty"`
}

// LogicAccess is one property of a device or of one of its slots, together with
// the directions the game allows.
type LogicAccess struct {
	Name   string `json:"name"`
	Access access `json:"access"`
}

// Mode is one setting the Mode property selects between. Value is the number a
// program writes, which is a position in the game's own list of mode names and
// not necessarily a member of any enum.
type Mode struct {
	Value int    `json:"value"`
	Name  string `json:"name"`
}

// access is the pair of directions the game allows on a property. The game
// has no name for this pair; it answers CanLogicRead and CanLogicWrite
// separately. This is not "memory access", a third-party term for the
// same pair.
type access string

const (
	accessNone      access = ""
	accessRead      access = "read"
	accessWrite     access = "write"
	accessReadWrite access = "readwrite"
	// accessUnknown is a property whose direction the game decides from live
	// state -- what a logic transmitter is currently pointed at, whether a pipe
	// connection is made -- which no reading of the assembly settles. A
	// consumer must treat it as "no diagnostic", never as a denial.
	accessUnknown access = "unknown"
)

func (a access) valid() bool {
	return a.decided() || a == accessUnknown
}

// decided reports whether the access says what the game does, as against
// standing for a property no reading of the assembly settles.
func (a access) decided() bool {
	switch a {
	case accessRead, accessWrite, accessReadWrite:
		return true
	case accessNone, accessUnknown:
		return false
	}
	return false
}

// Reagent is one member of Reagent.AllReagents. Name is the reagent's C#
// class name, which is what the game hashes; the game exposes no name form
// to a chip, so Hash is the only spelling an instruction can carry.
type Reagent struct {
	Name string `json:"name"`
	Hash int32  `json:"hash"`
}

// deviceInputs names everything the device extraction reads.
type deviceInputs struct {
	// sourceDir is the decompiled assembly, and assembly the image it was
	// decompiled from, read for its file version.
	sourceDir string
	assembly  string
	manifest  string
	// prefabs is the intermediate tools/prefabreader wrote from the game's
	// serialized files, which is where the prefab roster lives. The assembly
	// holds no list of the things the game ships.
	prefabs string
	// names is the English localization file, read for the titles the artifact
	// carries as a reading aid.
	names string
	// isa is the ISA table recovered from the same assembly, which the result
	// is held to before anything is written.
	isa string
}

// devices reads a decompiled assembly and the prefab roster and writes the
// canonical device JSON, holding both to one game build (see
// [checkSameAssembly] and [checkSameBuild]).
func devices(in deviceInputs, outPath string) error {
	if in.sourceDir == "" || in.assembly == "" || in.manifest == "" || in.prefabs == "" {
		return errors.New("devices needs --source, --assembly, --manifest and --prefabs")
	}
	tree, err := newSourceTree(in.sourceDir)
	if err != nil {
		return err
	}
	version, err := readAssemblyVersion(in.assembly)
	if err != nil {
		return err
	}
	isa, err := readJSON[ISA](in.isa)
	if err != nil {
		return err
	}
	assets, err := readRoster(in.prefabs, version)
	if err != nil {
		return err
	}

	extracted, err := extractDevices(tree, isa, assets, in)
	if err != nil {
		return err
	}
	extracted.Manifest = in.manifest
	extracted.Version = version

	if err := validateDevices(extracted, isa); err != nil {
		return err
	}
	if err := checkSameBuild(extracted, isa); err != nil {
		return err
	}
	return writeJSON(extracted, outPath)
}

// extractDevices reads the device tables out of the decompiled source and the
// prefab roster. Everything it returns is derived from those two inputs, so the
// result is independent of the machine the extraction ran on.
func extractDevices(tree *sourceTree, isa *ISA, assets *assetPrefabs, in deviceInputs) (*Devices, error) {
	src, err := tree.qualified(typeReagent)
	if err != nil {
		return nil, err
	}
	reagents, err := extractReagents(src)
	if err != nil {
		return nil, err
	}
	titles, err := readThingNames(in.names)
	if err != nil {
		return nil, err
	}
	prefabs, err := extractPrefabs(tree, isa, assets, titles)
	if err != nil {
		return nil, err
	}
	return &Devices{Reagents: reagents, Prefabs: prefabs}, nil
}

// extractReagents recovers Reagent.AllReagents in ReagentId order. The static
// initializer and the Generate(byte) switch state the table independently;
// the game builds its ReagentId-indexed lookup by walking the first, so their
// agreement is what makes an entry's position its ReagentId.
func extractReagents(src string) ([]Reagent, error) {
	listed, err := parseConstructedList(src, "AllReagents", "Reagent")
	if err != nil {
		return nil, err
	}
	byID, err := parseConstructorSwitch(src, "reagentId")
	if err != nil {
		return nil, err
	}
	if len(byID) != len(listed) {
		return nil, fmt.Errorf("Reagent.AllReagents holds %d reagents but Reagent.Generate covers %d", len(listed), len(byID))
	}

	reagents := make([]Reagent, len(listed))
	for i, name := range listed {
		switch constructed, ok := byID[int64(i)]; {
		case !ok:
			return nil, fmt.Errorf("Reagent.Generate has no arm for ReagentId %d", i)
		case constructed != name:
			return nil, fmt.Errorf("ReagentId %d is %s in Reagent.AllReagents and %s in Reagent.Generate", i, name, constructed)
		}
		reagents[i] = Reagent{Name: name, Hash: stringToHash(name)}
	}
	return reagents, nil
}

// stringToHash is UnityEngine.Animator.StringToHash, which turns a class
// name into the number an instruction operand carries. Native to the Unity
// runtime and absent from the decompiled assembly, it is CRC-32/ISO-HDLC read
// as a signed 32 bit integer -- the same the chip computes for HASH("...").
func stringToHash(name string) int32 {
	return int32(crc32.ChecksumIEEE([]byte(name)))
}

// validateDevices enforces the table shapes the compiler is built against. It
// reports every mismatch at once so a game update can be assessed in one pass.
func validateDevices(d *Devices, isa *ISA) error {
	var problems []string
	if len(d.Reagents) != wantReagents {
		problems = append(problems, fmt.Sprintf("reagents: got %d, want %d", len(d.Reagents), wantReagents))
	}
	if len(d.Prefabs) < minPrefabs {
		problems = append(problems, fmt.Sprintf("prefabs: got %d, want at least %d", len(d.Prefabs), minPrefabs))
	}
	counts := countSurfaces(d.Prefabs)
	for _, floor := range []struct {
		what string
		got  int
		want int
	}{
		{"device properties across the roster", counts.logic, minLogicEntries},
		{"slot properties across the roster", counts.slot, minSlotEntries},
		{"device properties the extraction decided", counts.decidedLogic, minDecidedLogicEntries},
		{"slot properties the extraction decided", counts.decidedSlot, minDecidedSlotEntries},
	} {
		if floor.got < floor.want {
			problems = append(problems, fmt.Sprintf("%s: got %d, want at least %d", floor.what, floor.got, floor.want))
		}
	}

	// The game builds its hash lookup with Dictionary.Add, so a shipping build
	// cannot hold two reagents that collide. One here means the names were
	// misread, not that the game grew an ambiguity.
	byName := make(map[string]bool, len(d.Reagents))
	byHash := make(map[int32]string, len(d.Reagents))
	for i, reagent := range d.Reagents {
		switch {
		case reagent.Name == "":
			problems = append(problems, fmt.Sprintf("reagent %d: unnamed", i))
		case byName[reagent.Name]:
			problems = append(problems, fmt.Sprintf("reagent %s: declared twice", reagent.Name))
		}
		byName[reagent.Name] = true
		if got := stringToHash(reagent.Name); got != reagent.Hash {
			problems = append(problems, fmt.Sprintf("reagent %s: hashes to %d, table says %d", reagent.Name, got, reagent.Hash))
		}
		if previous, ok := byHash[reagent.Hash]; ok && previous != reagent.Name {
			problems = append(problems, fmt.Sprintf("reagents %s and %s: both hash to %d", previous, reagent.Name, reagent.Hash))
		}
		byHash[reagent.Hash] = reagent.Name
	}
	problems = append(problems, validatePrefabs(d.Prefabs, isa)...)

	if len(problems) == 0 {
		return nil
	}
	slices.Sort(problems)
	return fmt.Errorf("extracted device tables do not match the expected shape of manifest %s (%s):\n  %s",
		d.Manifest, d.Version, strings.Join(problems, "\n  "))
}

// surfaceCounts is how much surface a roster carries: the properties it reaches
// at all, and the ones it says a direction for.
type surfaceCounts struct {
	logic, slot               int
	decidedLogic, decidedSlot int
}

// countSurfaces totals the properties the roster reaches, whole-device and
// per-slot.
func countSurfaces(prefabs []Prefab) surfaceCounts {
	var counts surfaceCounts
	for _, prefab := range prefabs {
		counts.logic += len(prefab.Logic)
		for _, entry := range prefab.Logic {
			if entry.Access.decided() {
				counts.decidedLogic++
			}
		}
		for _, s := range prefab.Slots {
			counts.slot += len(s.Types)
			for _, entry := range s.Types {
				if entry.Access.decided() {
					counts.decidedSlot++
				}
			}
		}
	}
	return counts
}

// readRoster reads the roster tools/prefabreader wrote and holds it to the
// two questions that can only be asked of the whole of it. The read and the
// checks are one call, since the read is the only way a roster enters this
// program, so an extraction that skips the checks cannot be written.
func readRoster(path, version string) (*assetPrefabs, error) {
	assets, err := readJSON[assetPrefabs](path)
	if err != nil {
		return nil, err
	}
	if err := errors.Join(checkSameAssembly(assets, version), checkDeclaredPower(assets)); err != nil {
		return nil, err
	}
	return assets, nil
}

// checkSameAssembly holds the prefab roster to the assembly the logic
// surfaces are read out of. The two reach this program by separate routes and
// nothing else joins them; a mismatched roster would still pass
// [checkSameBuild], since the manifest is a constant of the recipe.
func checkSameAssembly(assets *assetPrefabs, version string) error {
	if assets.AssemblyVersion == version {
		return nil
	}
	return fmt.Errorf("the prefab roster was read beside assembly version %q and the logic surfaces out of version %q: they are from different game builds",
		assets.AssemblyVersion, version)
}

// checkDeclaredPower holds the roster to carrying a power draw somewhere. No
// single prefab can be held to carrying one, since non-devices declare none,
// so a roster carrying it nowhere means a build moved the field or a reader
// stopped finding it -- too fine a failure for [validateDevices]'s floors.
func checkDeclaredPower(assets *assetPrefabs) error {
	if slices.ContainsFunc(assets.Prefabs, func(asset assetPrefab) bool { return asset.UsedPower != nil }) {
		return nil
	}
	return fmt.Errorf("none of the %d prefabs in the roster carries a power draw, so every device would read as drawing nothing",
		len(assets.Prefabs))
}

// checkSameBuild holds the device tables and the ISA tables to one game
// build: device data lifted from another build is the same shape and passes
// every check either file makes alone, while naming properties that resolve
// here to something else or nothing. [validateDevices] holds the other half.
func checkSameBuild(d *Devices, isa *ISA) error {
	var problems []string
	if d.Manifest != isa.Manifest {
		problems = append(problems, fmt.Sprintf("manifest: devices %q, ISA %q", d.Manifest, isa.Manifest))
	}
	if d.Version != isa.Version {
		problems = append(problems, fmt.Sprintf("version: devices %q, ISA %q", d.Version, isa.Version))
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("device tables and ISA tables are from different game builds:\n  %s", strings.Join(problems, "\n  "))
}
