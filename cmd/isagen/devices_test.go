package main

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// validDevices returns device tables that satisfy every assertion in
// validateDevices, so a test can perturb one field at a time. The tables are
// synthesized rather than read: the assertions are about size and uniqueness,
// which a fixture small enough to read would not reach.
//
// Every prefab carries the whole of both fixture enumerations with a decided
// access each, which is what puts both the surface totals and the decided
// totals over their floors on a roster sitting exactly at minPrefabs.
func validDevices() *Devices {
	d := &Devices{Manifest: fixtureManifest, Version: fixtureVersion}
	for i := range wantReagents {
		name := "R" + strconv.Itoa(i)
		d.Reagents = append(d.Reagents, Reagent{Name: name, Hash: stringToHash(name)})
	}
	slotTypes := []LogicAccess{
		{Name: "Occupied", Access: accessRead},
		{Name: "Quantity", Access: accessReadWrite},
	}
	for i := range minPrefabs {
		name := "StructureP" + strconv.Itoa(i)
		d.Prefabs = append(d.Prefabs, Prefab{
			Name: name,
			Hash: stringToHash(name),
			Logic: []LogicAccess{
				{Name: "Power", Access: accessRead},
				{Name: "Open", Access: accessReadWrite},
				{Name: "Mode", Access: accessWrite},
			},
			Slots: []PrefabSlot{
				{Index: 0, Class: "Helmet", Types: slices.Clone(slotTypes)},
				{Index: 1, Class: "Suit", Types: slices.Clone(slotTypes)},
			},
		})
	}
	return d
}

// fixtureISA reads the ISA fixture the device tables are checked against.
func fixtureISA(t *testing.T) *ISA {
	t.Helper()
	isa, err := readJSON[ISA](filepath.Join("testdata", "devices_isa.json"))
	if err != nil {
		t.Fatalf("read ISA fixture: %v", err)
	}
	return isa
}

// TestExtractDevices runs the recovery over the hand-written stand-in for the
// decompiled game, small enough that the expected table can be stated outright.
func TestExtractDevices(t *testing.T) {
	tree, err := newSourceTree(filepath.Join("testdata", "decompiled"))
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}
	assets, err := readJSON[assetPrefabs](filepath.Join("testdata", "prefabs.json"))
	if err != nil {
		t.Fatalf("read prefab roster: %v", err)
	}
	got, err := extractDevices(tree, fixtureISA(t), assets, deviceInputs{
		names: filepath.Join("testdata", "names.xml"),
	})
	if err != nil {
		t.Fatalf("extractDevices: %v", err)
	}

	want := []Reagent{
		{Name: "Flour", Hash: -811006991},
		{Name: "Milk", Hash: 471085864},
		{Name: "Iron", Hash: -666742878},
	}
	if !slices.Equal(got.Reagents, want) {
		t.Errorf("reagents = %+v, want %+v", got.Reagents, want)
	}

	// The prefab half is far too wide to state inline, and it is the input the
	// generate stage is tested against, so the fixture JSON is that table
	// rather than a second hand-written copy of it.
	got.Manifest, got.Version = fixtureManifest, fixtureVersion
	encoded, err := encodeJSON(got)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	compareGolden(t, filepath.Join("testdata", "devices.json"), encoded)
}

// TestReagentHash pins the hash to values published for this game, so a change
// to the implementation cannot pass by agreeing with itself.
func TestReagentHash(t *testing.T) {
	tests := []struct {
		name string
		want int32
	}{
		{name: "Flour", want: -811006991},
		{name: "Copper", want: -1172078909},
		{name: "Milk", want: 471085864},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stringToHash(tt.name); got != tt.want {
				t.Errorf("stringToHash(%q) = %d, want %d", tt.name, got, tt.want)
			}
		})
	}
}

// TestExtractReagentsRejectsDisagreement covers the two declarations of the
// reagent table falling out of step, which is what makes an entry's position
// its ReagentId.
func TestExtractReagentsRejectsDisagreement(t *testing.T) {
	source := func(list, arms string) string {
		return "class Reagent {\n" +
			"static Reagent() { AllReagents = new List<Reagent>\n{\n" + list + "\n};\n}\n" +
			"public static Reagent Generate(byte reagentId, float quantity = 0f)\n" +
			"{\nreturn reagentId switch\n{\n" + arms + "\n_ => null,\n};\n}\n}\n"
	}
	const twoArms = "0 => new Flour(quantity),\n1 => new Milk(quantity),"

	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name:    "no list",
			src:     "class Reagent { }",
			wantErr: "initializer for AllReagents: not found",
		},
		{
			name:    "list is not an initializer",
			src:     "class Reagent { static Reagent() { AllReagents = new List<Reagent>(); } }",
			wantErr: `initializer for AllReagents: opening "{": not found`,
		},
		{
			name:    "entry is not a constructor call",
			src:     source("Reagent.Flour,", twoArms),
			wantErr: `unrecognized entry "Reagent.Flour"`,
		},
		{
			name:    "no switch",
			src:     "class Reagent { static Reagent() { AllReagents = new List<Reagent>\n{\nnew Flour(0.0)\n};\n} }",
			wantErr: "switch on reagentId: not found",
		},
		{
			name:    "list is longer than the switch",
			src:     source("new Flour(0.0),\nnew Milk(0.0),\nnew Iron(0.0)", twoArms),
			wantErr: "AllReagents holds 3 reagents but Reagent.Generate covers 2",
		},
		{
			name:    "switch skips an id",
			src:     source("new Flour(0.0),\nnew Milk(0.0)", "0 => new Flour(quantity),\n2 => new Milk(quantity),"),
			wantErr: "no arm for ReagentId 1",
		},
		{
			name:    "list and switch disagree on an id",
			src:     source("new Flour(0.0),\nnew Iron(0.0)", twoArms),
			wantErr: "ReagentId 1 is Iron in Reagent.AllReagents and Milk in Reagent.Generate",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := extractReagents(tt.src)
			checkErr(t, "extractReagents", err, tt.wantErr)
		})
	}
}

func TestValidateDevices(t *testing.T) {
	if err := validateDevices(validDevices(), fixtureISA(t)); err != nil {
		t.Fatalf("validateDevices rejected a well formed table: %v", err)
	}

	tests := []struct {
		name    string
		perturb func(*Devices)
		wantErr string
	}{
		{
			name:    "short reagent table",
			perturb: func(d *Devices) { d.Reagents = d.Reagents[:2] },
			wantErr: "reagents: got 2, want 46",
		},
		{
			name:    "unnamed reagent",
			perturb: func(d *Devices) { d.Reagents[3].Name = "" },
			wantErr: "reagent 3: unnamed",
		},
		{
			name:    "reagent declared twice",
			perturb: func(d *Devices) { d.Reagents[3].Name = d.Reagents[2].Name },
			wantErr: "reagent R2: declared twice",
		},
		{
			name:    "colliding hashes",
			perturb: func(d *Devices) { d.Reagents[3].Hash = d.Reagents[2].Hash },
			wantErr: "reagents R2 and R3: both hash to",
		},
		{
			// The whole roster with the right names and hashes and no
			// properties on any of it, which is what a game build the surface
			// matchers no longer read produces.
			name: "logic surfaces collapsed",
			perturb: func(d *Devices) {
				for i := range d.Prefabs {
					d.Prefabs[i].Logic = nil
				}
			},
			wantErr: "device properties across the roster: got 0, want at least 3000",
		},
		{
			name: "slot surfaces collapsed",
			perturb: func(d *Devices) {
				for i := range d.Prefabs {
					d.Prefabs[i].Slots = nil
				}
			},
			wantErr: "slot properties across the roster: got 0, want at least 3000",
		},
		{
			// The other way a wholly misread surface reaches the artifact: every
			// property present and none of them answered, which is what a build
			// whose bodies all went out of reach of the evaluator produces. A
			// consumer reads unknown as "no diagnostic", so this roster says as
			// little as an empty one while clearing the floor on the totals.
			name: "logic surfaces collapsed to undecided",
			perturb: func(d *Devices) {
				for i := range d.Prefabs {
					for j := range d.Prefabs[i].Logic {
						d.Prefabs[i].Logic[j].Access = accessUnknown
					}
				}
			},
			wantErr: "device properties the extraction decided: got 0, want at least 3000",
		},
		{
			name: "slot surfaces collapsed to undecided",
			perturb: func(d *Devices) {
				for i := range d.Prefabs {
					for _, slot := range d.Prefabs[i].Slots {
						for j := range slot.Types {
							slot.Types[j].Access = accessUnknown
						}
					}
				}
			},
			wantErr: "slot properties the extraction decided: got 0, want at least 3000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := validDevices()
			tt.perturb(d)
			err := validateDevices(d, fixtureISA(t))
			if err == nil {
				t.Fatalf("validateDevices accepted a perturbed table, want an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("validateDevices error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
			if !strings.Contains(err.Error(), fixtureManifest) {
				t.Errorf("validateDevices error = %q, want it to name the manifest it checked", err.Error())
			}
		})
	}
}

// TestValidateDevicesReportsADuplicateReagentOnce covers one entry listed
// twice, which is one fault. Reporting it again as a hash collision describes
// the entry as colliding with itself and sends a reader looking for two names.
func TestValidateDevicesReportsADuplicateReagentOnce(t *testing.T) {
	d := validDevices()
	d.Reagents[3] = d.Reagents[2]
	err := validateDevices(d, fixtureISA(t))
	if err == nil {
		t.Fatal("validateDevices accepted a reagent listed twice")
	}
	if !strings.Contains(err.Error(), "reagent R2: declared twice") {
		t.Errorf("error = %q, want it to report the duplicate", err)
	}
	if strings.Contains(err.Error(), "both hash to") {
		t.Errorf("error = %q, want the duplicate reported once rather than also as a collision", err)
	}
}

// TestCheckSameAssembly covers the join between the two halves of the
// extraction's input. Neither the manifest nor the version the artifact is
// stamped with is read from the roster, so nothing downstream would notice a
// roster describing another build.
func TestCheckSameAssembly(t *testing.T) {
	tests := []struct {
		name    string
		roster  string
		version string
		wantErr string
	}{
		{name: "one build", roster: fixtureVersion, version: fixtureVersion},
		{
			name:    "a roster from another build",
			roster:  "0.2.6000.0",
			version: fixtureVersion,
			wantErr: `roster was read beside assembly version "0.2.6000.0" and the logic surfaces out of version "` + fixtureVersion + `"`,
		},
		{
			// A roster written before the version was stamped into it, which is
			// the shape a stale intermediate on disk has.
			name:    "a roster naming no assembly",
			version: fixtureVersion,
			wantErr: `assembly version ""`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkSameAssembly(&assetPrefabs{AssemblyVersion: tt.roster}, tt.version)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("checkSameAssembly: unexpected error: %v", err)
				}
				return
			}
			checkErr(t, "checkSameAssembly", err, tt.wantErr)
		})
	}
}

// TestCheckSameBuild covers the assertion neither JSON file can make on its
// own: that the two describe one game build.
func TestCheckSameBuild(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		version  string
		wantErr  []string
	}{
		{name: "same build", manifest: fixtureManifest, version: fixtureVersion},
		{
			name:     "different depot manifest",
			manifest: "1234567890123456789",
			version:  fixtureVersion,
			wantErr:  []string{"different game builds", `manifest: devices "1234567890123456789", ISA "` + fixtureManifest + `"`},
		},
		{
			name:     "different assembly version",
			manifest: fixtureManifest,
			version:  "0.2.6000.0",
			wantErr:  []string{"different game builds", `version: devices "0.2.6000.0", ISA "` + fixtureVersion + `"`},
		},
		{
			name:     "both differ",
			manifest: "1234567890123456789",
			version:  "0.2.6000.0",
			wantErr:  []string{"manifest: devices", "version: devices"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := validDevices()
			d.Manifest, d.Version = tt.manifest, tt.version
			isa := fixtureISA(t)
			isa.Manifest, isa.Version = fixtureManifest, fixtureVersion
			err := checkSameBuild(d, isa)
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("checkSameBuild: unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("checkSameBuild accepted tables from two builds, want an error containing %q", tt.wantErr[0])
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("checkSameBuild error = %q, want it to contain %q", err.Error(), want)
				}
			}
		})
	}
}

// TestCheckedInTablesShareABuild is the standing form of the same assertion.
// It reads what is committed rather than a fixture, so device data taken from
// another build cannot land beside the ISA tables unnoticed.
func TestCheckedInTablesShareABuild(t *testing.T) {
	isa, err := readJSON[ISA](filepath.Join(moduleRoot, defaultJSONPath))
	if err != nil {
		t.Fatalf("read checked-in ISA: %v", err)
	}
	d, err := readJSON[Devices](filepath.Join(moduleRoot, defaultDevicesJSONPath))
	if err != nil {
		t.Fatalf("read checked-in device tables: %v", err)
	}
	if err := checkSameBuild(d, isa); err != nil {
		t.Errorf("%s and %s disagree: %v", defaultJSONPath, defaultDevicesJSONPath, err)
	}
	if err := validateDevices(d, isa); err != nil {
		t.Errorf("%s: %v", defaultDevicesJSONPath, err)
	}
}

func TestDevicesInputErrors(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join("testdata", "decompiled")
	assembly := filepath.Join(dir, "Assembly-CSharp.dll")
	if err := os.WriteFile(assembly, []byte("not a PE image"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	isa := filepath.Join("testdata", "full.json")
	prefabs := filepath.Join("testdata", "prefabs.json")

	tests := []struct {
		name     string
		source   string
		assembly string
		manifest string
		isa      string
		wantErr  string
	}{
		{name: "no source", assembly: assembly, manifest: fixtureManifest, isa: isa, wantErr: "--source"},
		{name: "no assembly", source: source, manifest: fixtureManifest, isa: isa, wantErr: "--assembly"},
		{name: "no manifest", source: source, assembly: assembly, isa: isa, wantErr: "--manifest"},
		{
			name:     "absent source",
			source:   filepath.Join(dir, "absent"),
			assembly: assembly,
			manifest: fixtureManifest,
			isa:      isa,
			wantErr:  "index decompiled source",
		},
		{
			name:     "unreadable assembly",
			source:   source,
			assembly: assembly,
			manifest: fixtureManifest,
			isa:      isa,
			wantErr:  "open PE image",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := filepath.Join(dir, "devices.json")
			in := deviceInputs{sourceDir: tt.source, assembly: tt.assembly, manifest: tt.manifest, prefabs: prefabs, isa: tt.isa}
			checkErr(t, "devices", devices(in, out), tt.wantErr)
		})
	}
}
