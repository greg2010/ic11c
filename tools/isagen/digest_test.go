package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moduleRoot is where the paths in gameTypes are rooted, two levels above this
// package.
const moduleRoot = "../.."

func TestGameTypesMapping(t *testing.T) {
	seen := make(map[string]bool, len(gameTypes))
	for _, gt := range gameTypes {
		t.Run(gt.name, func(t *testing.T) {
			if seen[gt.name] {
				t.Errorf("type %s is mapped twice", gt.name)
			}
			seen[gt.name] = true
			if len(gt.backs) == 0 {
				t.Errorf("type %s names no Go file it backs", gt.name)
			}
			for _, path := range gt.backs {
				// A mapping that names a file which has been renamed away is
				// worse than no mapping: it reads as coverage that is not there.
				if _, err := os.Stat(filepath.Join(moduleRoot, path)); err != nil {
					t.Errorf("backs %s: %v", path, err)
				}
			}
		})
	}
}

// digestFixture writes a source tree holding every mapped type, so that
// rendering can be exercised without the decompiled assembly.
func digestFixture(t *testing.T, bodies map[string]string) *sourceTree {
	t.Helper()
	root := t.TempDir()
	for _, gt := range gameTypes {
		parts := strings.Split(gt.name, ".")
		path := filepath.Join(root, filepath.Join(parts...)+csharpExt)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create fixture directory: %v", err)
		}
		body, ok := bodies[gt.name]
		if !ok {
			body = "\tpublic int Placeholder;\n"
		}
		src := "namespace " + strings.Join(parts[:len(parts)-1], ".") + ";\n\n" +
			"public class " + parts[len(parts)-1] + "\n{\n" + body + "}\n"
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	tree, err := newSourceTree(root)
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}
	return tree
}

func TestRenderDigest(t *testing.T) {
	first := gameTypes[0].name
	tree := digestFixture(t, map[string]string{
		first: "\tprivate class _ZED_Operation\n\t{\n\t}\n\n\tprivate class _ALPHA_Operation\n\t{\n\t}\n",
	})

	data, err := renderDigest(tree, "123", "0.1.2.3")
	if err != nil {
		t.Fatalf("renderDigest: %v", err)
	}
	got := string(data)

	for _, want := range []string{
		"manifest 123\n",
		"assembly 0.1.2.3\n",
		"backs " + strings.Join(gameTypes[0].backs, " ") + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("digest does not contain %q", want)
		}
	}

	var types []string
	var records []string
	for line := range strings.SplitSeq(got, "\n") {
		switch {
		case strings.HasPrefix(line, "type "):
			types = append(types, strings.Fields(line)[2])
		case strings.HasPrefix(line, "\t"):
			records = append(records, strings.Fields(line)[1])
		}
	}
	if len(types) != len(gameTypes) {
		t.Fatalf("got %d type records, want %d", len(types), len(gameTypes))
	}
	for i, gt := range gameTypes {
		if types[i] != gt.name {
			t.Errorf("type record %d = %s, want %s", i, types[i], gt.name)
		}
	}
	// The two nested classes are written in reverse alphabetical order, so
	// their records prove the sort rather than echoing the source order.
	if len(records) < 2 || records[0] != "_ALPHA_Operation" || records[1] != "_ZED_Operation" {
		t.Errorf("member records = %q, want them sorted by path", records)
	}
}

func TestRenderDigestReportsAMissingType(t *testing.T) {
	tree := digestFixture(t, nil)
	if err := os.Remove(filepath.Join(tree.root,
		filepath.Join(strings.Split(gameTypes[0].name, ".")...)+csharpExt)); err != nil {
		t.Fatalf("remove fixture: %v", err)
	}
	if _, err := renderDigest(tree, "123", "0.1.2.3"); err == nil {
		t.Fatal("renderDigest succeeded with a mapped type missing, want an error")
	}
}

func TestWriteDigestInputErrors(t *testing.T) {
	dir := t.TempDir()
	assembly := filepath.Join(dir, "Assembly-CSharp.dll")
	if err := os.WriteFile(assembly, []byte("not a PE image"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	source := digestFixture(t, nil).root

	tests := []struct {
		name     string
		source   string
		assembly string
		manifest string
		wantErr  string
	}{
		{name: "no source", assembly: assembly, manifest: "123", wantErr: "--source"},
		{name: "no assembly", source: source, manifest: "123", wantErr: "--assembly"},
		{name: "no manifest", source: source, assembly: assembly, wantErr: "--manifest"},
		{name: "unreadable assembly", source: source, assembly: assembly, manifest: "123", wantErr: "PE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := writeDigest(tt.source, tt.assembly, tt.manifest, filepath.Join(dir, "out.digest"))
			if err == nil {
				t.Fatal("writeDigest succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestCopyGameSource(t *testing.T) {
	tree := digestFixture(t, nil)
	out := filepath.Join(t.TempDir(), "dump")
	if err := copyGameSource(tree.root, out); err != nil {
		t.Fatalf("copyGameSource: %v", err)
	}
	for _, gt := range gameTypes {
		path := filepath.Join(out, filepath.Join(strings.Split(gt.name, ".")...)+csharpExt)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		if !strings.Contains(string(data), "class "+gt.name[strings.LastIndexByte(gt.name, '.')+1:]) {
			t.Errorf("%s does not hold the type it is named for", path)
		}
	}
}

func TestCopyGameSourceInputErrors(t *testing.T) {
	tests := []struct {
		name   string
		source string
		out    string
	}{
		{name: "no source", out: t.TempDir()},
		{name: "no out", source: t.TempDir()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := copyGameSource(tt.source, tt.out); err == nil {
				t.Fatal("copyGameSource succeeded, want an error")
			}
		})
	}
}
