package main

import (
	"encoding/xml"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
)

// typeSlot is the game type whose nested Class enum names what a slot accepts.
// The base logic surface decides half the slot properties from it.
const typeSlot = "Assets.Scripts.Objects.Slot"

// interfaceCircuitHolder is implemented by every thing that can hold a
// programmable chip, and is what makes a prefab reachable as db rather than
// only as a pin.
const interfaceCircuitHolder = "ICircuitHolder"

// stateHasMode is the serialized flag that decides whether a thing has modes at
// all. What those modes are is a property of its class, not of the flag.
const stateHasMode = "HasModeState"

// assetPrefabs is the intermediate tools/prefabreader writes: everything about
// the prefab roster that lives in the game's serialized files rather than in
// its assembly. It is not checked in. The extraction joins it against the
// decompiled C# and writes the result.
type assetPrefabs struct {
	// AssemblyVersion is the file version of the Assembly-CSharp.dll shipped
	// beside the serialized files these prefabs were read from. It is what ties
	// the roster to the assembly the surfaces are read out of; see
	// [checkSameAssembly].
	AssemblyVersion string        `json:"assembly_version"`
	Prefabs         []assetPrefab `json:"prefabs"`
}

type assetPrefab struct {
	Name string `json:"name"`
	Hash int32  `json:"hash"`
	// Script is the namespace-qualified C# class the prefab is driven by, which
	// is what the decompiled tree is laid out by.
	Script string `json:"script"`
	// State holds the eleven serialized flags by their game names.
	State map[string]bool `json:"state"`
	// UsedPower is absent on a prefab that is not a device, and is a pointer
	// because absent and zero are different devices: the base logic surface
	// grants RequiredPower on a draw above zero, so a roster that lost the
	// field would read every device as denying that property. [checkDeclaredPower]
	// is what makes an absence mean what this says it means.
	UsedPower *float64    `json:"used_power"`
	Slots     []assetSlot `json:"slots"`
}

// assetSlot is one entry of a prefab's slot list. A slot is addressed by its
// position in that list, and the game's own Slot carries no index of its own:
// Slot.SlotIndex is computed as the parent's IndexOf, so there is nothing to
// read and nothing to cross-check against.
type assetSlot struct {
	// Class is the Slot.Class ordinal, resolved to a name against the enum in
	// the decompiled source rather than against a copy of it here.
	Class int64 `json:"class"`
}

// thingNames is the shape of the localization file the game reads a thing's
// human readable title out of. Only the title is taken; the descriptions are
// prose for the in-game encyclopedia.
type thingNames struct {
	Things struct {
		Records []struct {
			Key   string `xml:"Key"`
			Value string `xml:"Value"`
		} `xml:"RecordThing"`
	} `xml:"Things"`
}

// readThingNames reads the English titles keyed by prefab name. An absent path
// is not an error: the titles are a reading aid and every check the artifact
// supports is on the names and hashes.
func readThingNames(path string) (map[string]string, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read thing names: %w", err)
	}
	var parsed thingNames
	if err := xml.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("%s: decode: %w", path, err)
	}
	if len(parsed.Things.Records) == 0 {
		return nil, fmt.Errorf("%s: names no things: %w", path, errNotFound)
	}
	titles := make(map[string]string, len(parsed.Things.Records))
	for _, record := range parsed.Things.Records {
		titles[record.Key] = record.Value
	}
	return titles, nil
}

// checkTitleCoverage holds a localization file that was read to titling
// something on the roster. The file keys its titles by prefab name, and a
// build that changed how it keys them leaves every lookup empty, which is
// indistinguishable downstream from a game that ships no title at all. A
// floor of one title matched is all this can check, and all it needs to.
func checkTitleCoverage(titles map[string]string, prefabs []assetPrefab) error {
	if len(titles) == 0 {
		return nil
	}
	if slices.ContainsFunc(prefabs, func(asset assetPrefab) bool { return titles[asset.Name] != "" }) {
		return nil
	}
	return fmt.Errorf("the localization file carries %d titles and none of them keys one of the %d prefabs on the roster, so every title would be empty",
		len(titles), len(prefabs))
}

// extractPrefabs joins the serialized prefab roster to the decompiled C# and
// produces the artifact's prefab table. The two halves answer different
// questions and neither is sufficient alone: the assets say which things
// exist and what state each was authored with, the assembly what a class
// does with that state.
func extractPrefabs(tree *sourceTree, isa *ISA, assets *assetPrefabs, titles map[string]string) ([]Prefab, error) {
	index := newTypeIndex(tree)
	slotClasses, err := slotClassMembers(index)
	if err != nil {
		return nil, err
	}
	names, err := slotClassNames(slotClasses)
	if err != nil {
		return nil, err
	}

	surface, err := newLogicSurface(index, isa, slotClasses)
	if err != nil {
		return nil, err
	}
	if err := checkBaseSurface(index, surface, isa, slotClasses); err != nil {
		return nil, err
	}
	if err := checkModelledState(index, assets); err != nil {
		return nil, err
	}
	if err := checkTitleCoverage(titles, assets.Prefabs); err != nil {
		return nil, err
	}
	resolver := newModeResolver(index)

	prefabs := make([]Prefab, 0, len(assets.Prefabs))
	for _, asset := range assets.Prefabs {
		prefab, err := buildPrefab(index, surface, resolver, isa, names, titles, asset)
		if err != nil {
			return nil, fmt.Errorf("prefab %s: %w", asset.Name, err)
		}
		prefabs = append(prefabs, *prefab)
	}
	return prefabs, nil
}

// typeDevice is the class every prefab with a logic surface inherits its answers
// from. Nearly all of the roster overrides none of the four bodies and nothing
// else in it grants a property at all.
const typeDevice = "Assets.Scripts.Objects.Pipes.Device"

// checkBaseSurface holds that class's own four bodies to still being
// readable. The floors in validateDevices catch a roster whose surfaces
// collapsed to nothing, but not the likelier failure where typeDevice alone
// goes unread: every overriding class still decides as before and clears
// those floors, leaving the undecided base read as no diagnostic at all.
func checkBaseSurface(index *typeIndex, surface *logicSurface, isa *ISA, slotClasses []EnumMember) error {
	class, err := index.lookup(typeDevice)
	if err != nil {
		return err
	}
	if class == nil {
		return fmt.Errorf("class %s: %w", typeDevice, errNotFound)
	}
	probe := &device{class: class, state: everySerializedState(class), usedPower: 1}
	for _, member := range slotClasses {
		probe.slotClasses = append(probe.slotClasses, member.Name)
	}
	for _, form := range []struct {
		kind    surfaceKind
		members []EnumMember
	}{
		{kindReadLogic, isa.LogicTypes},
		{kindWriteLogic, isa.LogicTypes},
		{kindReadSlot, isa.SlotTypes},
		{kindWriteSlot, isa.SlotTypes},
	} {
		granted, err := grantsAnyProperty(surface, probe, form.kind, form.members)
		if err != nil {
			return err
		}
		if !granted {
			return fmt.Errorf("%s.%s grants nothing to a device with every state flag set and a slot of every class, so the body was not read",
				typeDevice, form.kind)
		}
	}
	return nil
}

// The affixes a serialized state flag is spelled with, which are the same two
// tools/prefabreader picks the flags out of the game's types by. Nothing is
// required between them: a field named exactly HasState is one the reader takes
// for a flag, and a pattern here that refused it would leave that field
// unmodelled on both sides with no count moving to say so.
const (
	serializedStatePrefix = "Has"
	serializedStateSuffix = "State"
)

// serializedStateRE matches the shape the serialized flags are spelled in.
// The word boundaries keep it to whole names: neither ThingHasPowerState nor
// HasPowerStateChanged is a flag.
var serializedStateRE = regexp.MustCompile(
	`\b` + serializedStatePrefix + `\w*` + serializedStateSuffix + `\b`)

// everySerializedState is every such flag the class's own source names, set.
func everySerializedState(class *csharpType) map[string]bool {
	state := make(map[string]bool)
	for _, name := range serializedStateRE.FindAllString(class.source, -1) {
		state[name] = true
	}
	return state
}

// checkModelledState holds the roster to carrying every serialized flag the
// game's logic surface decides a property from. tools/prefabreader models
// eleven of the sixteen flags Thing serializes; a build that moves a body to
// read one of the other five would leave that property undecided on every
// prefab with no count moving to say so, which this catches.
func checkModelledState(index *typeIndex, assets *assetPrefabs) error {
	carried := make(map[string]bool)
	for _, asset := range assets.Prefabs {
		for name := range asset.State {
			carried[name] = true
		}
	}

	classes := make([]string, 0, len(assets.Prefabs)+1)
	classes = append(classes, typeDevice)
	for _, asset := range assets.Prefabs {
		classes = append(classes, asset.Script)
	}
	checked := make(map[string]bool, len(classes))
	for _, qualified := range classes {
		if checked[qualified] {
			continue
		}
		checked[qualified] = true
		class, err := index.lookup(qualified)
		if err != nil {
			return err
		}
		if class == nil {
			return fmt.Errorf("class %s: %w", qualified, errNotFound)
		}
		reads, err := surfaceStateReads(index, class)
		if err != nil {
			return err
		}
		for _, name := range slices.Sorted(maps.Keys(reads)) {
			if !carried[name] {
				return fmt.Errorf("%s reads %s, which the roster does not carry, so the properties that flag decides would be undecided on every prefab",
					reads[name], name)
			}
		}
	}
	return nil
}

// surfaceStateReads is every serialized flag a logic surface body the class
// inherits names, against the declaration each was first found in. The flags
// come only from those four bodies, since Thing names six more of the same
// shape in animator code unrelated to the logic surface.
func surfaceStateReads(index *typeIndex, class *csharpType) (map[string]string, error) {
	kinds := []surfaceKind{kindReadLogic, kindWriteLogic, kindReadSlot, kindWriteSlot}
	reads := make(map[string]string)
	cur, depth := class, 0
	for ; cur != nil && depth < maxInheritanceDepth; depth++ {
		for _, kind := range kinds {
			decl, _, ok, err := surfaceDecl(cur, kind)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			body, err := memberBody(decl)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", cur.Qualified, decl.name, err)
			}
			for _, name := range serializedStateRE.FindAllString(body, -1) {
				if _, found := reads[name]; !found {
					reads[name] = cur.Qualified + "." + kind.String()
				}
			}
		}
		next, err := index.baseClass(cur)
		if err != nil {
			return nil, err
		}
		cur = next
	}
	if cur != nil {
		return nil, fmt.Errorf("%s: logic surface inheritance chain deeper than %d", class.Qualified, maxInheritanceDepth)
	}
	return reads, nil
}

// grantsAnyProperty reports whether one surface form answers yes to any selector
// on any of the device's slots.
func grantsAnyProperty(surface *logicSurface, dev *device, kind surfaceKind, members []EnumMember) (bool, error) {
	slots := []int{noSlot}
	if kind.slotForm() {
		slots = make([]int, len(dev.slotClasses))
		for i := range dev.slotClasses {
			slots[i] = i
		}
	}
	for _, member := range members {
		for _, slot := range slots {
			q := query{device: dev, kind: kind, selector: member.Name, selectorValue: member.Value, slotIndex: slot}
			if slot != noSlot {
				q.slotClass = dev.slotClasses[slot]
			}
			answer, err := surface.can(q)
			if err != nil {
				return false, err
			}
			if answer == triYes {
				return true, nil
			}
		}
	}
	return false, nil
}

// slotClassMembers reads the Slot.Class enum, which is what the roster's slot
// ordinals mean and what the game's own bodies compare a slot against.
func slotClassMembers(index *typeIndex) ([]EnumMember, error) {
	slot, err := index.lookup(typeSlot)
	if err != nil {
		return nil, err
	}
	if slot == nil {
		return nil, fmt.Errorf("type %s: %w", typeSlot, errNotFound)
	}
	members, err := parseEnum(slot.source, "Class")
	if err != nil {
		return nil, fmt.Errorf("%s.Class: %w", typeSlot, err)
	}
	return members, nil
}

// slotClassNames inverts that enum to the name each serialized ordinal stands
// for. C# enums alias freely (None and Default on zero is commonplace), and
// two names on one ordinal leave the inversion nothing to choose between, so
// an alias stops the extraction rather than being decided either way.
func slotClassNames(members []EnumMember) (map[int64]string, error) {
	names := make(map[int64]string, len(members))
	for _, member := range members {
		if previous, ok := names[member.Value]; ok {
			return nil, fmt.Errorf("%s.Class: %s and %s are both %d", typeSlot, previous, member.Name, member.Value)
		}
		names[member.Value] = member.Name
	}
	return names, nil
}

func buildPrefab(index *typeIndex, surface *logicSurface, resolver *modeResolver, isa *ISA,
	classNames map[int64]string, titles map[string]string, asset assetPrefab,
) (*Prefab, error) {
	class, err := index.lookup(asset.Script)
	if err != nil {
		return nil, err
	}
	if class == nil {
		return nil, fmt.Errorf("class %s: %w", asset.Script, errNotFound)
	}

	// A thing that is not a device is evaluated against bodies that never reach
	// the draw, so the zero left here for an absent one decides nothing.
	dev := &device{class: class, state: asset.State}
	if asset.UsedPower != nil {
		dev.usedPower = *asset.UsedPower
	}
	for i, slot := range asset.Slots {
		name, ok := classNames[slot.Class]
		if !ok {
			return nil, fmt.Errorf("slot %d: Slot.Class has no member %d", i, slot.Class)
		}
		dev.slotClasses = append(dev.slotClasses, name)
	}

	holder, err := index.derivesFrom(class, interfaceCircuitHolder)
	if err != nil {
		return nil, err
	}
	prefab := &Prefab{
		Name:                 asset.Name,
		Hash:                 asset.Hash,
		Title:                titles[asset.Name],
		CircuitHolder:        holder == triYes,
		CircuitHolderUnknown: holder == triMaybe,
	}

	prefab.Logic, err = deviceLogic(surface, isa, dev)
	if err != nil {
		return nil, err
	}
	prefab.Slots, err = deviceSlots(surface, isa, dev)
	if err != nil {
		return nil, err
	}
	if asset.State[stateHasMode] {
		names, ok, err := resolver.modes(class)
		if err != nil {
			return nil, err
		}
		prefab.ModesUnknown = !ok
		for i, name := range names {
			prefab.Modes = append(prefab.Modes, Mode{Value: i, Name: name})
		}
	}
	return prefab, nil
}

// noSlot marks a question about the device itself rather than about one of its
// slots, whose forms are the only ones that read the slot index.
const noSlot = -1

// deviceLogic derives the whole-device half of a prefab's logic surface, in
// LogicType declaration order.
func deviceLogic(surface *logicSurface, isa *ISA, dev *device) ([]LogicAccess, error) {
	decide, err := selectorDecider(surface, dev, kindReadLogic, kindWriteLogic, func(kind surfaceKind, selector EnumMember) query {
		return query{device: dev, kind: kind, selector: selector.Name, selectorValue: selector.Value, slotIndex: noSlot}
	})
	if err != nil {
		return nil, err
	}
	var entries []LogicAccess
	for _, logicType := range isa.LogicTypes {
		access, err := decide(logicType)
		if err != nil {
			return nil, fmt.Errorf("logic type %s: %w", logicType.Name, err)
		}
		if access != accessNone {
			entries = append(entries, LogicAccess{Name: logicType.Name, Access: access})
		}
	}
	return entries, nil
}

// deviceSlots derives the per-slot half, in slot order and then in
// LogicSlotType declaration order.
func deviceSlots(surface *logicSurface, isa *ISA, dev *device) ([]PrefabSlot, error) {
	var slots []PrefabSlot
	for index, class := range dev.slotClasses {
		decide, err := selectorDecider(surface, dev, kindReadSlot, kindWriteSlot, func(kind surfaceKind, selector EnumMember) query {
			return query{
				device: dev, kind: kind,
				selector: selector.Name, selectorValue: selector.Value,
				slotIndex: index, slotClass: class,
			}
		})
		if err != nil {
			return nil, err
		}
		slot := PrefabSlot{Index: index, Class: class}
		for _, slotType := range isa.SlotTypes {
			access, err := decide(slotType)
			if err != nil {
				return nil, fmt.Errorf("slot %d type %s: %w", index, slotType.Name, err)
			}
			if access != accessNone {
				slot.Types = append(slot.Types, LogicAccess{Name: slotType.Name, Access: access})
			}
		}
		slots = append(slots, slot)
	}
	return slots, nil
}

// selectorDecider returns the access derivation for one device, having first
// worked out which selectors can reach anything other than the default arm.
// The enums are wide (358 properties) and a class names a handful, so an
// unmentioned selector is decided once and reused -- a shortcut in cost only,
// excluded wherever a class reaches selectors by arithmetic instead.
func selectorDecider(surface *logicSurface, dev *device, read, write surfaceKind,
	build func(surfaceKind, EnumMember) query,
) (func(EnumMember) (access, error), error) {
	named, err := surface.mentions(dev.class, read)
	if err != nil {
		return nil, err
	}
	decide := func(selector EnumMember) (access, error) {
		return decideAccess(surface, build, read, write, selector)
	}
	if named == nil {
		return decide, nil
	}

	// A selector with no name and a number no enum member holds matches no case
	// label, no equality test and no folded group, so it stands for every
	// selector the bodies leave to their default arms.
	unnamed, err := decide(surface.unusedSelector(read))
	if err != nil {
		return nil, err
	}
	return func(selector EnumMember) (access, error) {
		if !named[selector.Name] {
			return unnamed, nil
		}
		return decide(selector)
	}, nil
}

func decideAccess(surface *logicSurface, build func(surfaceKind, EnumMember) query, read, write surfaceKind, selector EnumMember) (access, error) {
	readable, err := surface.can(build(read, selector))
	if err != nil {
		return accessNone, err
	}
	writable, err := surface.can(build(write, selector))
	if err != nil {
		return accessNone, err
	}
	return accessOf(readable, writable), nil
}

// accessOf reduces the read and write answers to the artifact's vocabulary.
//
// One undecided half makes the whole entry undecided. Reporting the decided
// half alone would read as an assertion about the other, and the entries this
// applies to are the ones where an assertion would be wrong.
func accessOf(readable, writable tri) access {
	switch {
	case readable == triMaybe || writable == triMaybe:
		return accessUnknown
	case readable == triYes && writable == triYes:
		return accessReadWrite
	case readable == triYes:
		return accessRead
	case writable == triYes:
		return accessWrite
	default:
		return accessNone
	}
}

// validatePrefabs enforces the shape the compiler is built against and holds
// every name in the table to the enumerations the ISA tables declare.
func validatePrefabs(prefabs []Prefab, isa *ISA) []string {
	logicTypes := enumValues(isa.LogicTypes)
	slotTypes := enumValues(isa.SlotTypes)

	var problems []string
	byName := make(map[string]bool, len(prefabs))
	byHash := make(map[int32]string, len(prefabs))
	for i, prefab := range prefabs {
		switch {
		case prefab.Name == "":
			problems = append(problems, fmt.Sprintf("prefab %d: unnamed", i))
		case byName[prefab.Name]:
			problems = append(problems, fmt.Sprintf("prefab %s: declared twice", prefab.Name))
		}
		byName[prefab.Name] = true
		if previous, ok := byHash[prefab.Hash]; ok && previous != prefab.Name {
			problems = append(problems, fmt.Sprintf("prefabs %s and %s: both hash to %d", previous, prefab.Name, prefab.Hash))
		}
		byHash[prefab.Hash] = prefab.Name

		if got := stringToHash(prefab.Name); got != prefab.Hash {
			problems = append(problems, fmt.Sprintf("prefab %s: hashes to %d, table says %d", prefab.Name, got, prefab.Hash))
		}
		problems = append(problems, checkAccessList(prefab.Name, "logic type", prefab.Logic, logicTypes)...)
		for _, slot := range prefab.Slots {
			where := fmt.Sprintf("%s slot %d", prefab.Name, slot.Index)
			problems = append(problems, checkAccessList(where, "slot type", slot.Types, slotTypes)...)
		}
		if prefab.ModesUnknown && len(prefab.Modes) > 0 {
			problems = append(problems, fmt.Sprintf("prefab %s: modes are marked unresolved and listed", prefab.Name))
		}
		if prefab.CircuitHolderUnknown && prefab.CircuitHolder {
			problems = append(problems, fmt.Sprintf("prefab %s: the circuit holder is marked unresolved and settled", prefab.Name))
		}
	}
	return problems
}

func checkAccessList(where, what string, entries []LogicAccess, known map[string]int64) []string {
	var problems []string
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		_, declared := known[entry.Name]
		switch {
		case !declared:
			problems = append(problems, fmt.Sprintf("%s: %s %s is not in the ISA tables", where, what, entry.Name))
		case seen[entry.Name]:
			problems = append(problems, fmt.Sprintf("%s: %s %s listed twice", where, what, entry.Name))
		}
		seen[entry.Name] = true
		if !entry.Access.valid() {
			problems = append(problems, fmt.Sprintf("%s: %s %s has access %q", where, what, entry.Name, entry.Access))
		}
	}
	return problems
}
