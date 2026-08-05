package tsparse

import (
	"fmt"
	"testing"

	"github.com/greg2010/ic11c/internal/ast"
)

// TestEveryTypeOpensAnUnbracedBody holds [typeWords] to the enumeration it
// is derived from: a type missing from the set draws no diagnostic anywhere,
// since the braces are simply not written and the grammar misparses the
// declaration silently.
func TestEveryTypeOpensAnUnbracedBody(t *testing.T) {
	for _, kind := range ast.ScalarKinds() {
		t.Run(kind.String(), func(t *testing.T) {
			src := fmt.Sprintf("void f(long long x) { if (x) %s a; }\n", kind)
			marks := newConverter("t.c", src).unbracedBodies()
			if len(marks) != 2 {
				t.Fatalf("%q needs one brace pair around the body, got %d marks", src, len(marks))
			}
			if got, want := marks[0].ch, byte('{'); got != want {
				t.Errorf("first mark is %q, want %q", got, want)
			}
			if got, want := marks[1].ch, byte('}'); got != want {
				t.Errorf("second mark is %q, want %q", got, want)
			}
			if got, want := src[marks[0].at:marks[1].at], kind.String()+" a;"; got != want {
				t.Errorf("the braces enclose %q, want %q", got, want)
			}
		})
	}
}
