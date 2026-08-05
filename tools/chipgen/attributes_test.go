package main

import (
	"strings"
	"testing"
)

func TestAttributeLine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantName string
		wantOK   bool
	}{
		{name: "bare attribute", line: "[Flags]", wantName: "Flags", wantOK: true},
		{name: "indented attribute", line: "\t\t[XmlIgnore]", wantName: "XmlIgnore", wantOK: true},
		{name: "attribute with arguments", line: "[Header(\"Power\")]", wantName: "Header", wantOK: true},
		{name: "attribute sharing a line with a declaration", line: "[Flags] public enum A"},
		{name: "an indexer is not an attribute", line: "return Table[i];"},
		{name: "ordinary declaration", line: "public int A;"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			name, ok := attributeLine(test.line)
			if ok != test.wantOK || name != test.wantName {
				t.Errorf("attributeLine(%q) = %q, %v, want %q, %v", test.line, name, ok, test.wantName, test.wantOK)
			}
		})
	}
}

func TestStripAttrs(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		want        string
		wantDropped []string
	}{
		{
			name: "engine attributes go, base library attributes stay",
			text: "[Serializable]\n[SerializeField]\n[XmlIgnore]\npublic int A;",
			want: "[Serializable]\npublic int A;", wantDropped: []string{"SerializeField", "XmlIgnore"},
		},
		{
			name: "nothing to drop",
			text: "[Flags]\npublic enum A", want: "[Flags]\npublic enum A",
		},
		{
			name: "an attribute deeper in the declaration is dropped too",
			text: "public class A\n{\n\t[XmlEnum(\"B\")]\n\tB\n}",
			want: "public class A\n{\n\tB\n}", wantDropped: []string{"XmlEnum"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dropped := make(map[string]bool)
			got := stripAttrs(dropped, test.text)
			if got != test.want {
				t.Errorf("stripAttrs = %q, want %q", got, test.want)
			}
			if strings.Join(sortedKeys(dropped), ",") != strings.Join(test.wantDropped, ",") {
				t.Errorf("stripAttrs dropped %q, want %q", sortedKeys(dropped), test.wantDropped)
			}
		})
	}
}
