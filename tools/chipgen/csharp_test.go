package main

import (
	"errors"
	"strings"
	"testing"
)

func TestSkipLiteral(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want int
	}{
		{name: "ordinary code is not a literal", src: "int x", want: 0},
		{name: "line comment runs to the newline", src: "// note\nx", want: 7},
		{name: "unterminated line comment runs to the end", src: "// note", want: 7},
		{name: "block comment", src: "/* note */x", want: 10},
		{name: "unterminated block comment runs to the end", src: "/* note", want: 7},
		{name: "string", src: `"ab"x`, want: 4},
		{name: "string with an escaped quote", src: `"a\"b"x`, want: 6},
		{name: "interpolated string keeps its braces inside", src: `$"d{int.MaxValue}"x`, want: 18},
		{name: "verbatim string", src: `@"a""b"x`, want: 7},
		{name: "character literal", src: `'x'y`, want: 3},
		{name: "escaped character literal", src: `'\''y`, want: 4},
		{name: "past the end", src: "", want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := skipLiteral(test.src, 0); got != test.want {
				t.Errorf("skipLiteral(%q, 0) = %d, want %d", test.src, got, test.want)
			}
		})
	}
}

func TestClassifyDecl(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		want    declHead
		wantErr bool
	}{
		{
			name:   "class with a base list",
			header: "public class ProgrammableChip : Item, ISourceCode, ILogicable",
			want:   declHead{kind: declContainer, keyword: "class", name: "ProgrammableChip", bases: []string{"Item", "ISourceCode", "ILogicable"}},
		},
		{
			name:   "generic class with a constraint",
			header: "public class ScriptEnum<T> : IScriptEnum where T : struct, Enum",
			want:   declHead{kind: declContainer, keyword: "class", name: "ScriptEnum", bases: []string{"IScriptEnum"}},
		},
		{
			name:   "struct without a base list",
			header: "private struct _AliasValue",
			want:   declHead{kind: declContainer, keyword: "struct", name: "_AliasValue"},
		},
		{
			name:   "enum with an underlying type",
			header: "public enum Class : ushort",
			want:   declHead{kind: declEnum, keyword: "enum", name: "Class", bases: []string{"ushort"}},
		},
		{
			name:   "attributes are not part of the name",
			header: "[Flags]\n\tprivate enum _AliasTarget",
			want:   declHead{kind: declEnum, keyword: "enum", name: "_AliasTarget"},
		},
		{
			name:   "method whose parameter names a type keyword",
			header: "public static T Make<T>(Func<T, bool> isClass)",
			want:   declHead{kind: declLeaf, name: "public static T Make<T>(Func<T, bool> isClass)"},
		},
		{
			name:   "field initializer is not part of the name",
			header: "private readonly double[] _Registers = new double[18];",
			want:   declHead{kind: declLeaf, name: "private readonly double[] _Registers"},
		},
		{
			name:   "expression body is not part of the name",
			header: "public double TotalReagents => Flour.Quantity + Milk.Quantity;",
			want:   declHead{kind: declLeaf, name: "public double TotalReagents"},
		},
		{
			name:   "lambda in an initializer is not an expression body",
			header: "private Func<int, int> _double = (int x) => x * 2;",
			want:   declHead{kind: declLeaf, name: "private Func<int, int> _double"},
		},
		{name: "type declaration naming nothing", header: "public class", wantErr: true},
		{name: "empty header", header: "  ", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := classifyDecl(test.header)
			if test.wantErr {
				if err == nil {
					t.Fatalf("classifyDecl(%q) = %+v, want an error", test.header, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("classifyDecl(%q): %v", test.header, err)
			}
			if got.kind != test.want.kind || got.keyword != test.want.keyword || got.name != test.want.name {
				t.Errorf("classifyDecl(%q) = %+v, want %+v", test.header, got, test.want)
			}
			if strings.Join(got.bases, ",") != strings.Join(test.want.bases, ",") {
				t.Errorf("classifyDecl(%q) bases = %q, want %q", test.header, got.bases, test.want.bases)
			}
		})
	}
}

func TestSplitDecls(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    []string
		wantErr string
	}{
		{
			name: "members and a nested type",
			body: "public int A;\n\npublic void B()\n{\n}\n\nprivate class C\n{\n\tpublic int D;\n}\n",
			want: []string{"public int A", "public void B()", "C"},
		},
		{
			name: "array initializer is not the end of the declaration",
			body: "public static readonly int[] Table = new int[2]\n{\n\t1,\n\t2\n};\n",
			want: []string{"public static readonly int[] Table"},
		},
		{
			name: "auto property keeps its accessor list in the signature",
			body: "public int A { get; set; } = 3;\n",
			want: []string{"public int A { get; set; }"},
		},
		{
			name: "braces inside an interpolated string do not move the depth",
			body: "public string A()\n{\n\treturn $\"d{int.MaxValue}\";\n}\n",
			want: []string{"public string A()"},
		},
		{name: "unbalanced closer", body: "}\n", wantErr: "has no opener"},
		{name: "unbalanced opener", body: "class A\n{\n", wantErr: "left unclosed"},
		{name: "trailing text", body: "public int A;\nnot a declaration", wantErr: "trailing text"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := splitDecls(test.body)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("splitDecls(%q) error = %v, want one containing %q", test.body, err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitDecls(%q): %v", test.body, err)
			}
			names := make([]string, len(got))
			for i, d := range got {
				names[i] = d.name
			}
			if strings.Join(names, "|") != strings.Join(test.want, "|") {
				t.Errorf("splitDecls(%q) = %q, want %q", test.body, names, test.want)
			}
		})
	}
}

// The offsets a nested edit is applied at are relative to the declaration text,
// so they have to survive the trim splitDecls does. Nothing else checks that.
func TestDeclSpansAddressTheirOwnText(t *testing.T) {
	body := "\n\nprivate class Outer\n{\n\tpublic int A;\n\n\tpublic int B;\n}\n"
	decls, err := splitDecls(body)
	if err != nil {
		t.Fatalf("splitDecls: %v", err)
	}
	outer := decls[0]
	if body[outer.start:outer.start+len(outer.text)] != outer.text {
		t.Fatalf("declaration text does not sit at its own start offset")
	}
	inner, err := scope{body: outer.body}.member("public int B")
	if err != nil {
		t.Fatalf("member: %v", err)
	}
	offset := outer.bodyStart - outer.start
	if got := outer.text[offset+inner.start : offset+inner.start+len(inner.text)]; got != inner.text {
		t.Errorf("nested declaration at %d = %q, want %q", offset+inner.start, got, inner.text)
	}
}

func TestMatchDelim(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantBody string
		wantErr  bool
	}{
		{name: "nested braces", src: "x { a { b } c } y", wantBody: " a { b } c "},
		{name: "brace inside a string", src: `x { "}" } y`, wantBody: ` "}" `},
		{name: "no opener", src: "x y", wantErr: true},
		{name: "no closer", src: "x { y", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, _, err := matchDelim(test.src, 0, '{', '}')
			if test.wantErr {
				if !errors.Is(err, errNotFound) {
					t.Fatalf("matchDelim(%q) error = %v, want errNotFound", test.src, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("matchDelim(%q): %v", test.src, err)
			}
			if body != test.wantBody {
				t.Errorf("matchDelim(%q) = %q, want %q", test.src, body, test.wantBody)
			}
		})
	}
}

func TestLineOf(t *testing.T) {
	src := "a\nb\nc"
	tests := []struct {
		name   string
		offset int
		want   int
	}{
		{name: "first line", offset: 0, want: 1},
		{name: "second line", offset: 2, want: 2},
		{name: "third line", offset: 4, want: 3},
		{name: "past the end clamps", offset: 99, want: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := lineOf(src, test.offset); got != test.want {
				t.Errorf("lineOf(%q, %d) = %d, want %d", src, test.offset, got, test.want)
			}
		})
	}
}
