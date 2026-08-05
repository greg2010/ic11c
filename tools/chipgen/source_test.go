package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree writes a fixture decompile and returns a slice over it.
func writeTree(t testing.TB, files map[string]string) *slicing {
	t.Helper()
	root := t.TempDir()
	for rel, text := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return newSlicing(root)
}

func TestReadSourceReportsAMissingFileAsNotFound(t *testing.T) {
	s := writeTree(t, map[string]string{"A/B.cs": "public class B\n{\n}\n"})
	if _, err := s.read("A/C.cs"); !errors.Is(err, errNotFound) {
		t.Errorf("read error = %v, want errNotFound", err)
	}
	if _, err := s.read("A/B.cs"); err != nil {
		t.Errorf("read: %v", err)
	}
}

func TestTopLevelType(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		want    string
		wantErr string
	}{
		{
			name: "namespace-scoped declaration",
			text: "using System;\n\nnamespace A.B;\n\npublic class C\n{\n\tpublic int D;\n}\n",
			want: "C",
		},
		{name: "no type at all", text: "using System;\n", wantErr: "declares no type"},
		{name: "two types", text: "public class C\n{\n}\n\npublic class D\n{\n}\n", wantErr: "declares C and D"},
		{name: "a different type", text: "public class D\n{\n}\n", wantErr: "declares D, wanted C"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			src, err := writeTree(t, map[string]string{"C.cs": test.text}).read("C.cs")
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			got, err := src.topLevelType("C")
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("topLevelType error = %v, want one containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("topLevelType: %v", err)
			}
			if got.name != test.want {
				t.Errorf("topLevelType = %q, want %q", got.name, test.want)
			}
		})
	}
}

func TestMemberDistinguishesOverloads(t *testing.T) {
	const body = `
	public double Read(int address)
	{
		return 1.0;
	}

	public double Read(int address, int width)
	{
		return 2.0;
	}
`
	tests := []struct {
		name      string
		signature string
		wantBody  string
		wantErr   string
	}{
		{name: "one arity", signature: "public double Read(int address)", wantBody: "return 1.0;"},
		{name: "the other arity", signature: "public double Read(int address, int width)", wantBody: "return 2.0;"},
		{name: "a signature that moved", signature: "public double Read(int addr)", wantErr: "not found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := scope{body: body}.member(test.signature)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("member error = %v, want one containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("member: %v", err)
			}
			if !strings.Contains(got.text, test.wantBody) {
				t.Errorf("member %q = %q, want it to contain %q", test.signature, got.text, test.wantBody)
			}
		})
	}
}

func TestMemberRefusesADuplicate(t *testing.T) {
	const body = "\n\tpublic int A;\n\n\tpublic int A;\n"
	_, err := scope{body: body}.member("public int A")
	if err == nil || !strings.Contains(err.Error(), "2 times") {
		t.Errorf("member error = %v, want one reporting the duplicate", err)
	}
}

func TestStatementFor(t *testing.T) {
	const src = "{\n\tA = new Regex(\"a;b\");\n\tB = 1;\n}"
	tests := []struct {
		name    string
		prefix  string
		want    string
		wantErr string
	}{
		{name: "semicolon inside a literal is not the terminator", prefix: "A = ", want: `A = new Regex("a;b");`},
		{name: "plain statement", prefix: "B = ", want: "B = 1;"},
		{name: "absent", prefix: "C = ", wantErr: "not found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := statementFor(src, test.prefix)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("statementFor error = %v, want one containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("statementFor: %v", err)
			}
			if got != test.want {
				t.Errorf("statementFor = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDedentAndIndentRoundTrip(t *testing.T) {
	const nested = "\tpublic class A\n\t{\n\t\tpublic int B;\n\t}"
	if got := indent(dedent(nested)); got != nested {
		t.Errorf("indent(dedent(%q)) = %q", nested, got)
	}
}
