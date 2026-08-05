package sema_test

import (
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/sema"
)

// TestEveryEnumValueIsNamed holds the three enums analysis renders into
// diagnostics to a name for every value they declare. A value the table has no
// entry for renders as its number, which is what this detects a gap by.
func TestEveryEnumValueIsNamed(t *testing.T) {
	for k := sema.Invalid; k <= sema.Array; k++ {
		if s := k.String(); strings.Contains(s, "Kind(") {
			t.Errorf("type kind %d has no name", k)
		}
	}
	for k := sema.GlobalVar; k <= sema.FuncName; k++ {
		if s := k.String(); strings.Contains(s, "SymbolKind(") {
			t.Errorf("symbol kind %d has no name", k)
		}
	}
	for k := sema.OperandValue; k <= sema.OperandString; k++ {
		if s := k.String(); strings.Contains(s, "OperandKind(") {
			t.Errorf("operand kind %d has no name", k)
		}
	}
}

// TestAnUnnamedEnumValueRendersAsItsNumber covers the answer a value outside
// the name table gets, which is what [TestEveryEnumValueIsNamed] detects a gap
// by. Answering with an empty string instead would let a new kind ship unnamed,
// read as nothing at all in every diagnostic quoting it, and pass that test.
func TestAnUnnamedEnumValueRendersAsItsNumber(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "the last type kind named", got: sema.Array.String(), want: "array"},
		{name: "the type kind after it", got: (sema.Array + 1).String(), want: "Kind(8)"},
		{name: "a type kind far past the table", got: sema.Kind(200).String(), want: "Kind(200)"},

		{name: "the last symbol kind named", got: sema.FuncName.String(), want: "function"},
		{name: "the symbol kind after it", got: (sema.FuncName + 1).String(), want: "SymbolKind(4)"},
		{name: "a symbol kind far past the table", got: sema.SymbolKind(200).String(), want: "SymbolKind(200)"},

		{name: "the last operand kind named", got: sema.OperandString.String(), want: "string literal"},
		{name: "the operand kind after it", got: (sema.OperandString + 1).String(), want: "OperandKind(9)"},
		{name: "an operand kind far past the table", got: sema.OperandKind(200).String(), want: "OperandKind(200)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("String() = %q, want %q", tt.got, tt.want)
			}
		})
	}
}
