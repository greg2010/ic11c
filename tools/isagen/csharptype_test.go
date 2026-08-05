package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// shapeCorpus is a C# tree written by hand to state, in as few declarations as
// possible, each source shape this program has to read. It is not a copy of the
// game: what the recovery makes of the game is held to the game itself, and this
// exists to be perturbed, which the decompiled assembly may not be.
const shapeCorpus = "shapes"

// shapeTypes indexes that corpus.
func shapeTypes(t *testing.T) *typeIndex {
	t.Helper()
	tree, err := newSourceTree(filepath.Join("testdata", shapeCorpus))
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}
	return newTypeIndex(tree)
}

// perturbedTypes indexes a copy of the shape corpus with the named files written
// over it. The corpus itself is the input every golden in this package rests on,
// so it is copied rather than edited.
func perturbedTypes(t *testing.T, files map[string]string) *typeIndex {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join("testdata", shapeCorpus))); err != nil {
		t.Fatalf("copy the shape corpus: %v", err)
	}
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create fixture directory for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	tree, err := newSourceTree(root)
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}
	return newTypeIndex(tree)
}

func shapeType(t *testing.T, qualified string) *csharpType {
	t.Helper()
	found, err := shapeTypes(t).lookup(qualified)
	if err != nil {
		t.Fatalf("lookup %s: %v", qualified, err)
	}
	if found == nil {
		t.Fatalf("lookup %s: not found", qualified)
	}
	return found
}

func TestParseCsharpType(t *testing.T) {
	tests := []struct {
		name string
		// declared is the type the source declares, which is also the last
		// segment of the qualified name the file would be found under.
		declared      string
		src           string
		wantNamespace string
		wantUsings    []string
		wantClass     bool
		wantBases     []string
		wantErr       string
	}{
		{
			name:          "class with a base and interfaces",
			declared:      "Pump",
			src:           "using System;\nusing Assets.Scripts.Objects;\n\nnamespace Objects.Pipes;\n\npublic class Pump : Device, IPowered, IDensePoolable\n{\n}\n",
			wantNamespace: "Objects.Pipes",
			wantUsings:    []string{"System", "Assets.Scripts.Objects"},
			wantClass:     true,
			wantBases:     []string{"Device", "IPowered", "IDensePoolable"},
		},
		{
			name:      "interface",
			declared:  "ICircuitHolder",
			src:       "public interface ICircuitHolder : IDensePoolable\n{\n}\n",
			wantBases: []string{"IDensePoolable"},
		},
		{
			// A struct's base list holds only interfaces.
			name:      "struct",
			declared:  "Grid",
			src:       "public struct Grid : IEquatable<Grid>\n{\n}\n",
			wantBases: []string{"IEquatable"},
		},
		{
			name:      "record",
			declared:  "Reading",
			src:       "public record Reading : Sample\n{\n}\n",
			wantClass: true,
			wantBases: []string{"Sample"},
		},
		{
			name:      "generic base with a constraint",
			declared:  "Table",
			src:       "public class Table<T> : Lookup<T, int> where T : class\n{\n}\n",
			wantClass: true,
			wantBases: []string{"Lookup"},
		},
		{
			name:      "no bases",
			declared:  "Thing",
			src:       "public abstract class Thing\n{\n}\n",
			wantClass: true,
		},
		{
			// The colon in a constraint opens no base list.
			name:      "a constraint and no base list",
			declared:  "Table",
			src:       "public class Table<T> where T : IComparable<T>\n{\n}\n",
			wantClass: true,
		},
		{
			name:     "file declaring nothing",
			declared: "Pump",
			src:      "using System;\n",
			wantErr:  "declares no type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCsharpType(tt.declared, tt.src)
			if tt.wantErr != "" {
				checkErr(t, "parseCsharpType", err, tt.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("parseCsharpType: %v", err)
			}
			if got.Namespace != tt.wantNamespace {
				t.Errorf("namespace = %q, want %q", got.Namespace, tt.wantNamespace)
			}
			if !slices.Equal(got.Usings, tt.wantUsings) {
				t.Errorf("usings = %q, want %q", got.Usings, tt.wantUsings)
			}
			if got.IsClass != tt.wantClass {
				t.Errorf("IsClass = %v, want %v", got.IsClass, tt.wantClass)
			}
			if !slices.Equal(got.Bases, tt.wantBases) {
				t.Errorf("bases = %q, want %q", got.Bases, tt.wantBases)
			}
		})
	}
}

// TestResolutionCandidates pins the order C# itself searches, which is what lets
// a base written with no namespace resolve to the right one of several.
func TestResolutionCandidates(t *testing.T) {
	from := &csharpType{
		Namespace: "Assets.Scripts.Objects.Pipes",
		Usings:    []string{"System", "Objects.Items"},
	}
	want := []string{
		"Assets.Scripts.Objects.Pipes.Device",
		"Assets.Scripts.Objects.Device",
		"Assets.Scripts.Device",
		"Assets.Device",
		"System.Device",
		"Objects.Items.Device",
		"Device",
	}
	if got := resolutionCandidates(from, "Device"); !slices.Equal(got, want) {
		t.Errorf("resolutionCandidates = %q, want %q", got, want)
	}
	if got := resolutionCandidates(nil, "Device"); !slices.Equal(got, []string{"Device"}) {
		t.Errorf("resolutionCandidates with no context = %q, want [Device]", got)
	}
}

// TestBaseClassOverTheGameSource covers the one thing a base list does not say:
// which entry is the base class. Only resolving them tells the two apart, and the
// game writes base lists whose first entry is an interface. The chain below is
// the one nearly every logic surface answer is inherited along.
func TestBaseClassOverTheGameSource(t *testing.T) {
	tests := []struct {
		class string
		want  string
	}{
		{class: "Assets.Scripts.Objects.Pipes.Device", want: "Assets.Scripts.Objects.SmallGrid"},
		{class: "Assets.Scripts.Objects.SmallGrid", want: "Assets.Scripts.Objects.Structure"},
		{class: "Assets.Scripts.Objects.Structure", want: "Assets.Scripts.Objects.Thing"},
		// Thing derives from another assembly, where every chain the game declares ends.
		{class: "Assets.Scripts.Objects.Thing", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.class, func(t *testing.T) {
			index := gameIndex(t)
			base, err := index.baseClass(gameClass(t, tt.class))
			if err != nil {
				t.Fatalf("baseClass: %v", err)
			}
			got := ""
			if base != nil {
				got = base.Qualified
			}
			if got != tt.want {
				t.Errorf("baseClass(%s) = %q, want %q", tt.class, got, tt.want)
			}
		})
	}
}

// TestBaseClassAcrossNamespaces covers a base list written in a namespace the
// declaration cannot be reached from without an alias, which the game does not ship.
func TestBaseClassAcrossNamespaces(t *testing.T) {
	tests := []struct {
		class string
		want  string
	}{
		{class: "Assets.Scripts.Objects.Electrical.Housing", want: "Assets.Scripts.Objects.Pipes.Device"},
		{class: "Objects.Structures.Panel", want: "Assets.Scripts.Objects.Pipes.Device"},
	}
	index := shapeTypes(t)
	for _, tt := range tests {
		t.Run(tt.class, func(t *testing.T) {
			base, err := index.baseClass(shapeType(t, tt.class))
			if err != nil {
				t.Fatalf("baseClass: %v", err)
			}
			got := ""
			if base != nil {
				got = base.Qualified
			}
			if got != tt.want {
				t.Errorf("baseClass(%s) = %q, want %q", tt.class, got, tt.want)
			}
		})
	}
}

// TestDerivesFromOverTheGameSource states what the walk makes of the game's own
// inheritance, which decides whether a prefab holds a chip and which logic
// surface implementation answers for it.
func TestDerivesFromOverTheGameSource(t *testing.T) {
	tests := []struct {
		name  string
		class string
		want  string
		is    tri
	}{
		{name: "itself", class: "Assets.Scripts.Objects.Pipes.Device", want: "Device", is: triYes},
		{name: "a base class four links up", class: "Assets.Scripts.Objects.Pipes.Device", want: "Thing", is: triYes},
		{name: "an interface the base list names", class: "Assets.Scripts.Objects.Pipes.Device", want: "ILogicable", is: triYes},
		{
			name:  "the interface that says a thing holds a chip",
			class: "Assets.Scripts.Objects.Electrical.CircuitHousing",
			want:  "ICircuitHolder",
			is:    triYes,
		},
		{
			name:  "a thing that holds no chip",
			class: "Assets.Scripts.Objects.Pipes.Device",
			want:  "ICircuitHolder",
			is:    triNo,
		},
		{
			// A name no file in the tree holds is a type from another assembly.
			name:  "a name the assembly does not declare",
			class: "Assets.Scripts.Objects.Thing",
			want:  "INotDeclared",
			is:    triNo,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gameIndex(t).derivesFrom(gameClass(t, tt.class), tt.want)
			if err != nil {
				t.Fatalf("derivesFrom: %v", err)
			}
			if got != tt.is {
				t.Errorf("derivesFrom(%s, %s) = %v, want %v", tt.class, tt.want, got, tt.is)
			}
		})
	}
}

// TestDerivesFromAnUnreadableBaseList states what the walk makes of base lists
// that resolve to nothing, and in particular the two that do so for opposite
// reasons: a name the tree holds nothing under is another assembly's and settles
// the answer, one it does hold leaves it open.
func TestDerivesFromAnUnreadableBaseList(t *testing.T) {
	// The corpus declares Housing under Assets.Scripts.Objects.Electrical, which
	// a base list written in Objects.Structures reaches only through the alias.
	const unplaceable = `namespace Objects.Structures;

public class Mount : Housing
{
}
`
	const aliased = `using Housing = Assets.Scripts.Objects.Electrical.Housing;

namespace Objects.Structures;

public class Mount : Housing
{
}
`
	// A diamond: Fork reaches Open by two paths, and Open's own base is one the
	// tree declares and this file cannot reach, so Open answers triMaybe. The walk
	// visits Open once, and the first path's triMaybe has to survive the second.
	const diamond = `namespace Objects.Structures;

public interface IOpen : ICircuitHolder
{
}
`
	const diamondLeft = `namespace Objects.Structures;

public interface ILeft : IOpen
{
}
`
	const diamondRight = `namespace Objects.Structures;

public interface IRight : IOpen
{
}
`
	const diamondBottom = `namespace Objects.Structures;

public class Fork : ILeft, IRight
{
}
`
	// A cycle, which a misparsed base list can produce and the walk has to answer
	// rather than follow forever. Pong also names a base it cannot place.
	const cyclePing = `namespace Objects.Structures;

public class Ping : Pong
{
}
`
	const cyclePong = `namespace Objects.Structures;

public class Pong : Ping
{
}
`
	const cyclePongOpen = `namespace Objects.Structures;

public class Pong : Ping, ICircuitHolder
{
}
`
	diamondFiles := map[string]string{
		"Objects/Structures/IOpen.cs":  diamond,
		"Objects/Structures/ILeft.cs":  diamondLeft,
		"Objects/Structures/IRight.cs": diamondRight,
		"Objects/Structures/Fork.cs":   diamondBottom,
	}
	tests := []struct {
		name  string
		files map[string]string
		class string
		want  string
		is    tri
	}{
		{
			name:  "a base the tree declares and this program cannot place",
			files: map[string]string{"Objects/Structures/Mount.cs": unplaceable},
			class: "Objects.Structures.Mount",
			want:  "ICircuitHolder",
			is:    triMaybe,
		},
		{
			name:  "the same base written through the alias C# resolves it by",
			files: map[string]string{"Objects/Structures/Mount.cs": aliased},
			class: "Objects.Structures.Mount",
			want:  "ICircuitHolder",
			is:    triYes,
		},
		{
			// The chain above an unplaceable base is unread whatever is asked of it.
			name:  "an unrelated name below an unplaceable base",
			files: map[string]string{"Objects/Structures/Mount.cs": unplaceable},
			class: "Objects.Structures.Mount",
			want:  "IMemory",
			is:    triMaybe,
		},
		{
			name:  "a type two paths reach, whose first visit is the open one",
			files: diamondFiles,
			class: "Objects.Structures.Fork",
			want:  "IMemory",
			is:    triMaybe,
		},
		{
			// A name the shared type's own base list holds settles on the first
			// path and must not be reopened by the second.
			name:  "a type two paths reach, whose base list names what is asked",
			files: diamondFiles,
			class: "Objects.Structures.Fork",
			want:  "ICircuitHolder",
			is:    triYes,
		},
		{
			name: "a base list that cycles",
			files: map[string]string{
				"Objects/Structures/Ping.cs": cyclePing,
				"Objects/Structures/Pong.cs": cyclePong,
			},
			class: "Objects.Structures.Ping",
			want:  "IMemory",
			is:    triNo,
		},
		{
			name: "a base list that cycles past a base it cannot place",
			files: map[string]string{
				"Objects/Structures/Ping.cs": cyclePing,
				"Objects/Structures/Pong.cs": cyclePongOpen,
			},
			class: "Objects.Structures.Ping",
			want:  "IMemory",
			is:    triMaybe,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index := perturbedTypes(t, tt.files)
			class, err := index.lookup(tt.class)
			if err != nil || class == nil {
				t.Fatalf("lookup %s: %v", tt.class, err)
			}
			got, err := index.derivesFrom(class, tt.want)
			if err != nil {
				t.Fatalf("derivesFrom: %v", err)
			}
			if got != tt.is {
				t.Errorf("derivesFrom(%s, %s) = %v, want %v", tt.class, tt.want, got, tt.is)
			}
		})
	}
}

// TestLookupAbsentType covers a name from another assembly, which is a normal
// outcome rather than an error: every chain the game declares ends in one.
func TestLookupAbsentType(t *testing.T) {
	found, err := gameIndex(t).lookup("UnityEngine.MonoBehaviour")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if found != nil {
		t.Errorf("lookup returned %s, want nothing", found.Qualified)
	}
}

// TestMemberBodyRejectsADeclarationWithNoBody covers the one answer memberBody
// gives that a declaration in the tree cannot produce.
func TestMemberBodyRejectsADeclarationWithNoBody(t *testing.T) {
	if _, err := memberBody(csharpDecl{name: "int Field", text: "public int Field;"}); err == nil {
		t.Error("memberBody accepted a declaration with no body")
	}
}
