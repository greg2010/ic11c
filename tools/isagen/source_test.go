package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree lays out a decompiled source tree from path-to-contents pairs and
// returns its root.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create fixture directory for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	return root
}

func TestNewSourceTreeRejectsAnEmptyTree(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{name: "no files at all", files: nil},
		{name: "nothing decompiled", files: map[string]string{"Assembly.csproj": "<Project />"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newSourceTree(writeTree(t, tt.files))
			checkErr(t, "newSourceTree", err, "no .cs files")
		})
	}
}

func TestNewSourceTreeReportsAnAbsentRoot(t *testing.T) {
	_, err := newSourceTree(filepath.Join(t.TempDir(), "absent"))
	checkErr(t, "newSourceTree", err, "index decompiled source")
}

func TestSourceTreeQualified(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Assets/Scripts/Objects/Electrical/ProgrammableChip.cs": "chip",
		"Assets/Scripts/Objects/Slot.cs":                        "slot",
	})
	tree, err := newSourceTree(root)
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}

	tests := []struct {
		name     string
		fullName string
		want     string
		wantErr  string
	}{
		{
			name:     "namespaced type",
			fullName: "Assets.Scripts.Objects.Electrical.ProgrammableChip",
			want:     "chip",
		},
		{
			name:     "absent type",
			fullName: "Assets.Scripts.Objects.Electrical.LogicBase",
			wantErr:  "type Assets.Scripts.Objects.Electrical.LogicBase under",
		},
		{
			name:     "bare name is not resolved here",
			fullName: "Slot",
			wantErr:  "type Slot under",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tree.qualified(tt.fullName)
			if !checkErr(t, "qualified", err, tt.wantErr) {
				return
			}
			if got != tt.want {
				t.Errorf("qualified(%q) = %q, want %q", tt.fullName, got, tt.want)
			}
		})
	}
}

// TestSourceTreeUnreadableFile separates a type the tree declares nothing under
// from one it declares in a file that will not open. The first ends an
// inheritance chain; reading the second as the same thing silently strips every
// property that class declares from every prefab below it.
func TestSourceTreeUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}
	root := writeTree(t, map[string]string{"Assets/Scripts/Objects/Slot.cs": "slot"})
	if err := os.Chmod(filepath.Join(root, "Assets", "Scripts", "Objects", "Slot.cs"), 0); err != nil {
		t.Fatalf("chmod fixture: %v", err)
	}
	tree, err := newSourceTree(root)
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}

	_, err = tree.qualified("Assets.Scripts.Objects.Slot")
	if !checkErr(t, "qualified", err, "read type Assets.Scripts.Objects.Slot") {
		return
	}
	if errors.Is(err, errNotFound) {
		t.Errorf("qualified reported an unreadable file as an absent type: %v", err)
	}
}

func TestSourceTreeEnumType(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Assets/Scripts/Objects/Slot.cs":                   "slot source",
		"Assets/Scripts/Objects/Motherboards/ColorType.cs": "color source",
		"Objects/Rockets/NodeType.cs":                      "one node type",
		"Assets/Scripts/NodeType.cs":                       "another node type",
	})
	tree, err := newSourceTree(root)
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}

	tests := []struct {
		name              string
		ref               string
		wantSrc, wantName string
		wantErr           string
	}{
		{name: "top level type", ref: "ColorType", wantSrc: "color source", wantName: "ColorType"},
		{name: "nested type", ref: "Slot.Class", wantSrc: "slot source", wantName: "Class"},
		{name: "absent type", ref: "SoundAlert", wantErr: "type SoundAlert under"},
		{name: "ambiguous type", ref: "NodeType", wantErr: "type NodeType is declared in"},
		{name: "namespaced type", ref: "Assets.Scripts.Objects.Slot.Class", wantErr: "only a nested type may be qualified"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, name, err := tree.enumType(tt.ref)
			if !checkErr(t, "enumType", err, tt.wantErr) {
				return
			}
			if src != tt.wantSrc || name != tt.wantName {
				t.Errorf("enumType(%q) = %q, %q, want %q, %q", tt.ref, src, name, tt.wantSrc, tt.wantName)
			}
		})
	}
}

// TestSourceTreeAmbiguityNamesBothFiles keeps the error actionable: a game
// update that adds a second type of the same name has to be resolved by hand,
// and the message is where the reader learns which two collided.
func TestSourceTreeAmbiguityNamesBothFiles(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Objects/Rockets/NodeType.cs": "one",
		"Assets/Scripts/NodeType.cs":  "another",
	})
	tree, err := newSourceTree(root)
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}
	_, _, err = tree.enumType("NodeType")
	if err == nil {
		t.Fatal("enumType accepted an ambiguous type name")
	}
	for _, want := range []string{"Assets", "Rockets"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("enumType error = %q, want it to name the %s file", err.Error(), want)
		}
	}
}
