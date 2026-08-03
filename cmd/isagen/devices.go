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

// minPrefabs is a floor rather than an exact count. Every game update adds
// things, so pinning the roster size would fail on every update for no reason,
// but a roster that collapsed to a handful means the walk lost its way and has
// to stop. The build this was written against ships 1565.
const minPrefabs = 1000

// minLogicEntries and minSlotEntries are the same kind of floor over the logic
// surfaces, and they are what stops a wholly misread surface from being
// written.
//
// A roster of the right size with the right hashes and no properties on any of
// it is the one degenerate result the rest of this file accepts: the surface
// evaluator answers a class it cannot find the methods on with a plain no, so
// a game build that moves those declarations out of reach of the matchers
// produces empty logic lists everywhere rather than an error. Every batch
// access in every program would then draw a diagnostic saying the device
// answers nothing, which is the failure this table exists to prevent.
//
// Both counts run to five figures and only grow as the game gains devices, so
// a floor at a fraction of that trips on collapse and on nothing else. The
// build this was written against extracts 13426 and 13356.
//
// minDecidedLogicEntries and minDecidedSlotEntries are the same floors over the
// entries that say what the game does. An undecided entry counts toward the
// totals above, so the other way to misread the whole surface -- a build in
// which every chain the evaluator walks goes out of reach -- emits an entry per
// property per prefab, every one of them unknown, and clears a floor on the
// totals while answering nothing anywhere. A consumer reads unknown as "no
// diagnostic", so that roster is as silent as an empty one. The build this was
// written against decides 9926 of the 13426 and 13296 of the 13356.
//
// Neither floor gates the narrower failure of the base class alone going out of
// reach, which leaves every overriding class deciding and clears all four
// comfortably. See checkBaseSurface, which gates that where it happens rather
// than by counting what survived it.
const (
	minLogicEntries        = 3000
	minSlotEntries         = 3000
	minDecidedLogicEntries = 3000
	minDecidedSlotEntries  = 3000
)

// Devices is the canonical extracted description of the game things a program
// names by hash rather than through the instruction set. It is the on-disk
// sibling of [ISA]: written from the same assembly in the same run, carrying
// the same manifest and version, and read back by the generate stage.
//
// Field order fixes the JSON key order and every slice is emitted in game
// declaration order, so two extractions of the same build produce identical
// bytes.
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

// Prefab is one thing the game ships, described by everything a chip can reach
// about it.
//
// Every prefab is listed, including the great many with no logic surface at
// all: the name and the hash are what makes an arbitrary batch instruction
// operand checkable, and a kit or a tool having no properties is itself the
// answer to a question about it.
//
// The logic and slot surfaces describe a *completed* device. Both base
// implementations refuse every property while a structure is still being built,
// so a table that reported that state would say no to everything.
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

// access is the pair of directions the game allows on a property.
//
// The game has no name for this: it answers CanLogicRead and CanLogicWrite
// separately and calls the pair nothing. The vocabulary here is this
// program's, chosen to be the two questions and no more. In particular it is
// not "memory access", which is a third-party invention describing the same
// pair.
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

// Reagent is one member of Reagent.AllReagents.
//
// Name is the reagent's C# class name, which is what the game hashes and what
// a program spells. The game exposes no name form to a chip: lr and rmap take
// the hash as a number, so Hash is the only spelling an instruction can carry.
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
// canonical device JSON.
//
// Two joins are made before anything is written, and neither input can make
// either on its own: the roster is held to the assembly the surfaces are read
// out of, and the result is held to the ISA table recovered from that same
// assembly.
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
	assets, err := readJSON[assetPrefabs](in.prefabs)
	if err != nil {
		return err
	}
	if err := checkSameAssembly(assets, version); err != nil {
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

// extractReagents recovers Reagent.AllReagents in ReagentId order.
//
// The static initializer and the Generate(byte) switch are two independent
// statements of the same table, and the game builds its ReagentId-indexed
// lookup by walking the first, so the two agreeing is what makes an entry's
// position its ReagentId. A disagreement is reported rather than resolved in
// favour of either.
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

// stringToHash is UnityEngine.Animator.StringToHash, which is how the game
// turns a reagent's or a prefab's class name into the number an instruction
// operand carries. It is the key Reagent.Find is built on and the value a batch
// instruction selects devices by.
//
// The implementation is native to the Unity runtime and so is absent from the
// decompiled assembly. It is CRC-32/ISO-HDLC read as a signed 32 bit integer,
// which is what every published Stationeers hash matches; internal/vm computes
// the same hash for the HASH("...") preprocessor form.
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

// checkSameAssembly holds the prefab roster to the assembly the logic surfaces
// are read out of.
//
// The two reach this program by separate routes -- the roster from the game's
// serialized files, the surfaces from the decompiled assembly -- and nothing
// else joins them. The manifest both halves of the artifact are stamped with is
// a constant of the extraction recipe rather than anything read from either
// input, so a roster from another build passes [checkSameBuild] as well: it is
// the same shape, it names classes that still exist, and it carries the state
// flags those classes had in a build whose bodies are not the ones being read,
// which is the whole of what a logic surface is a function of.
//
// The assembly's file version is what both sides can be asked for. It separates
// every build that changed the code the surfaces come from, and does not
// separate two that ship the same assembly beside different assets.
func checkSameAssembly(assets *assetPrefabs, version string) error {
	if assets.AssemblyVersion == version {
		return nil
	}
	return fmt.Errorf("the prefab roster was read beside assembly version %q and the logic surfaces out of version %q: they are from different game builds",
		assets.AssemblyVersion, version)
}

// checkSameBuild holds the device tables and the ISA tables to one game build.
//
// Neither file can make this assertion alone. Device data lifted from another
// build is the same shape and passes every check either file makes on its own,
// while naming properties that resolve here to something else or to nothing:
// the widely used third-party database of this data calls the fuel gas
// Volatiles where this build calls it Methane, and lists 45 reagents where this
// build ships 46. Extraction refuses to write on a mismatch, and generation
// refuses to read one.
//
// This is the identity half of that. The other half is that every property name
// in the device tables has to resolve in the enumerations the ISA tables
// declare, which is what catches data whose build identifiers were copied along
// with it; [validateDevices] holds it to that and runs beside this on both
// paths.
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
