package main

import (
	"strings"
	"testing"
)

func TestSplitDecls(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		kinds []declKind
		names []string
	}{
		{
			name:  "using and file scoped namespace are leaves",
			src:   "using System;\n\nnamespace A.B;\n",
			kinds: []declKind{declLeaf, declLeaf},
			names: []string{"using System", "namespace A.B"},
		},
		{
			name:  "class body carries a nested class",
			src:   "public class Outer\n{\n\tprivate class Inner : Base\n\t{\n\t}\n}\n",
			kinds: []declKind{declContainer},
			names: []string{"Outer"},
		},
		{
			name:  "field initializer keeps its brace block",
			src:   "private static readonly int[] Table = new int[2] { 1, 2 };\n",
			kinds: []declKind{declLeaf},
			names: []string{"private static readonly int[] Table"},
		},
		{
			name:  "auto property with a default is one declaration",
			src:   "public int Count { get; set; } = 3;\n",
			kinds: []declKind{declLeaf},
			names: []string{"public int Count { get; set; }"},
		},
		{
			name:  "auto property without a default keeps only its signature",
			src:   "public int Count { get; set; }\npublic int Other;\n",
			kinds: []declKind{declLeaf, declLeaf},
			names: []string{"public int Count", "public int Other"},
		},
		{
			name:  "expression bodied member is a leaf",
			src:   "public float MemoryUsed => SourceCode.Length;\n",
			kinds: []declKind{declLeaf},
			names: []string{"public float MemoryUsed => SourceCode.Length"},
		},
		{
			name:  "generic constraint does not read as a nested type",
			src:   "public T Find<T>(int id) where T : class\n{\n\treturn null;\n}\n",
			kinds: []declKind{declLeaf},
			names: []string{"public T Find<T>(int id) where T : class"},
		},
		{
			name:  "attribute section is dropped from the path",
			src:   "[Serializable]\npublic enum Mode : byte\n{\n\tA,\n\tB\n}\n",
			kinds: []declKind{declEnum},
			names: []string{"Mode"},
		},
		{
			name:  "braces inside literals do not move the depth",
			src:   "public string Pattern = \"{0}:([\\\\S]+?)}\";\n",
			kinds: []declKind{declLeaf},
			names: []string{"public string Pattern"},
		},
		{
			name:  "leading comment does not name the declaration",
			src:   "// leading\npublic int A;\n/* block */\npublic int B;\n",
			kinds: []declKind{declLeaf, declLeaf},
			names: []string{"public int A", "public int B"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decls, err := splitDecls(tt.src)
			if err != nil {
				t.Fatalf("splitDecls: %v", err)
			}
			if len(decls) != len(tt.kinds) {
				t.Fatalf("got %d declarations, want %d: %+v", len(decls), len(tt.kinds), decls)
			}
			for i, decl := range decls {
				if decl.kind != tt.kinds[i] {
					t.Errorf("declaration %d kind = %d, want %d", i, decl.kind, tt.kinds[i])
				}
				if decl.name != tt.names[i] {
					t.Errorf("declaration %d name = %q, want %q", i, decl.name, tt.names[i])
				}
			}
		})
	}
}

func TestSplitDeclsRejectsMalformed(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "unclosed block", src: "class A\n{\n", want: "unclosed"},
		{name: "extra closer", src: "class A\n{\n}\n}\n", want: "no opener"},
		{name: "trailing text", src: "public int A;\npublic int B", want: "trailing text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := splitDecls(tt.src)
			if err == nil {
				t.Fatalf("splitDecls(%q) succeeded, want an error", tt.src)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestWalkDeclsPaths(t *testing.T) {
	src := `public class Chip
{
	private class _ADD_Operation : _Operation
	{
		public override int Execute(int index)
		{
			return index + 1;
		}
	}

	public enum Mode
	{
		A,
		B
	}

	private readonly double[] _Registers = new double[18];
}
`
	decl, err := topLevelType(src, "Chip")
	if err != nil {
		t.Fatalf("topLevelType: %v", err)
	}
	var records []declRecord
	if err := walkDecls("", decl.body, &records); err != nil {
		t.Fatalf("walkDecls: %v", err)
	}

	want := []string{
		"_ADD_Operation",
		"_ADD_Operation/public override int Execute(int index)",
		"Mode",
		"private readonly double[] _Registers",
	}
	got := make([]string, len(records))
	for i, record := range records {
		got[i] = record.Path
		if len(record.Digest) != 2*digestBytes {
			t.Errorf("record %q digest %q is not %d hex characters", record.Path, record.Digest, 2*digestBytes)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("got paths %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("path %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDeclDigestIgnoresLayoutOnly(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		same bool
	}{
		{
			name: "reindented body",
			a:    "void F()\n{\n\treturn;\n}",
			b:    "\t\tvoid F()\n\t\t{\n\t\t\treturn;\n\t\t}",
			same: true,
		},
		{
			name: "blank lines added",
			a:    "void F()\n{\n\treturn;\n}",
			b:    "void F()\n\n{\n\n\treturn;\n}",
			same: true,
		},
		{
			name: "changed literal",
			a:    "int x = 1;",
			b:    "int x = 2;",
			same: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := declDigest(tt.a) == declDigest(tt.b); got != tt.same {
				t.Errorf("declDigest(a) == declDigest(b) = %v, want %v", got, tt.same)
			}
		})
	}
}

func TestTopLevelTypeRejectsUnexpectedLayout(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "no type", src: "using System;\n", want: "declares no type"},
		{name: "two types", src: "class A\n{\n}\nclass B\n{\n}\n", want: "A and B"},
		{name: "wrong type", src: "class B\n{\n}\n", want: "declares B instead"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := topLevelType(tt.src, "A")
			if err == nil {
				t.Fatalf("topLevelType(%q) succeeded, want an error", tt.src)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}
