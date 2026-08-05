package sema_test

import (
	"strings"
	"testing"
)

// TestDefiniteAssignment covers the rule a local that lives in a register is
// held to: it must be written on every path reaching a read of it.
//
// Nothing zeroes a register, and the consequence of letting one through is not
// a wrong number. The read becomes undef in the IR, and the optimizer is then
// entitled to fold whatever it reaches — including deleting the device stores a
// comparison against it guarded, which leaves a program that does nothing and
// says nothing.
func TestDefiniteAssignment(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantName string
	}{
		{
			name:     "a local read before anything writes it",
			src:      "void main(void) { long long x; __ic_store(d0, Setting, x); }",
			wantName: "'x'",
		},
		{
			name:     "a local written on only one arm of an if",
			src:      "void main(void) { long long p; if ((long long)__ic_load(d0, Setting) > 20) { p = 1; } __ic_store(d1, Setting, p); }",
			wantName: "'p'",
		},
		{
			name:     "a local written only inside a loop the program may skip",
			src:      "void main(void) { long long p; while ((long long)__ic_load(d0, Setting) > 0) { p = 1; } __ic_store(d1, Setting, p); }",
			wantName: "'p'",
		},
		{
			name:     "a local written only in a switch with no default arm",
			src:      "void main(void) { long long p; switch ((long long)__ic_load(d0, Setting)) { case 1: p = 1; break; } __ic_store(d1, Setting, p); }",
			wantName: "'p'",
		},
		{
			name:     "a local read by its own compound assignment",
			src:      "void main(void) { long long x; x += 1; __ic_store(d0, Setting, x); }",
			wantName: "'x'",
		},
		{
			name:     "a local read by its own increment",
			src:      "void main(void) { long long x; x++; __ic_store(d0, Setting, x); }",
			wantName: "'x'",
		},
		{
			name:     "a local read in a loop condition before the body assigns it",
			src:      "void main(void) { long long x; while (x < 4) { x = 1; } }",
			wantName: "'x'",
		},
		{
			name:     "a local written only under the right operand of a short circuit",
			src:      "void main(void) { long long p; if ((long long)__ic_load(d0, Setting) > 0 && (p = 1) == 1) { } __ic_store(d1, Setting, p); }",
			wantName: "'p'",
		},
		{
			name:     "a local written only in the post clause of a for loop",
			src:      "void main(void) { long long p; for (long long i = 0; i < 4; p = i) { } __ic_store(d1, Setting, p); }",
			wantName: "'p'",
		},
		{
			name:     "a local written on only one arm of a conditional expression",
			src:      "void main(void) { long long x; long long y = (long long)__ic_load(d0, Setting) > 0 ? (x = 1) : 2; __ic_store(d1, Setting, x + y); }",
			wantName: "'x'",
		},
		{
			// '&p[i]' is the address arithmetic 'p + i', which reads p.
			name:     "a pointer read by the address arithmetic of a subscript",
			src:      "long long a[4];\nvoid main(void) { long long *p; long long *q = &p[0]; *q = 7; __ic_store(d0, Setting, a[0]); }",
			wantName: "'p'",
		},
		{
			name:     "a pointer read by the address of its own dereference",
			src:      "long long a[4];\nvoid main(void) { long long *p; long long *q = &*p; *q = 7; __ic_store(d0, Setting, a[0]); }",
			wantName: "'p'",
		},
		{
			name:     "the index of a subscript whose address is taken",
			src:      "long long a[8];\nvoid main(void) { long long i; long long *q = &a[i]; *q = 7; __ic_store(d0, Setting, a[0]); }",
			wantName: "'i'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := analyze(t, tt.src)
			if !diags.HasErrors() {
				t.Fatalf("the program was accepted:\n%s", tt.src)
			}
			text := diags.String()
			if !strings.Contains(text, tt.wantName) {
				t.Errorf("the diagnostic does not name %s:\n%s", tt.wantName, text)
			}
			if !strings.Contains(text, "assigned") {
				t.Errorf("the diagnostic does not say what is wrong:\n%s", text)
			}
			for _, d := range diags {
				if !d.Pos.IsValid() {
					t.Errorf("a diagnostic carries no source position: %s", d.Error())
				}
			}
		})
	}
}

// TestDefinitelyAssignedIsAccepted covers what the rule must not reject: the
// shapes that do write on every path, and the storage the entry prologue zeroes.
func TestDefinitelyAssignedIsAccepted(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "an initializer",
			src:  "void main(void) { long long x = 1; __ic_store(d0, Setting, x); }",
		},
		{
			name: "both arms of an if",
			src:  "void main(void) { long long p; if ((long long)__ic_load(d0, Setting) > 0) { p = 1; } else { p = 2; } __ic_store(d1, Setting, p); }",
		},
		{
			name: "an arm that returns instead of assigning",
			src:  "void main(void) { long long p; if ((long long)__ic_load(d0, Setting) > 0) { p = 1; } else { return; } __ic_store(d1, Setting, p); }",
		},
		{
			name: "the body of a do loop, which always runs once",
			src:  "void main(void) { long long p; do { p = 1; } while ((long long)__ic_load(d0, Setting) > 0); __ic_store(d1, Setting, p); }",
		},
		{
			name: "every arm of a switch with a default",
			src:  "void main(void) { long long p; switch ((long long)__ic_load(d0, Setting)) { case 1: p = 1; break; default: p = 2; } __ic_store(d1, Setting, p); }",
		},
		{
			name: "a break out of a loop that runs forever",
			src:  "void main(void) { long long p; while (true) { p = 1; break; } __ic_store(d1, Setting, p); }",
		},
		{
			name: "a global, which the prologue zeroes",
			src:  "long long g; void main(void) { __ic_store(d0, Setting, g); }",
		},
		{
			name: "an array element, which the prologue zeroes",
			src:  "void main(void) { long long a[4]; __ic_store(d0, Setting, a[0]); }",
		},
		{
			name: "a local whose address is taken, which moves it into the data region",
			src:  "void main(void) { long long x; long long *p = &x; __ic_store(d0, Setting, *p); }",
		},
		{
			name: "a parameter",
			src:  "long long twice(long long x) { return x + x; } void main(void) { __ic_store(d0, Setting, twice(2)); }",
		},
		{
			name: "a shadowing declaration in an inner block",
			src:  "void main(void) { long long x = 1; { long long x = 2; __ic_store(d0, Setting, x); } __ic_store(d1, Setting, x); }",
		},
		{
			name: "an assignment inside the condition it is then read by",
			src:  "void main(void) { long long p; if ((p = (long long)__ic_load(d0, Setting)) > 0) { __ic_store(d1, Setting, p); } }",
		},
		{
			name: "a continue that reaches the condition of a do loop",
			src:  "void main(void) { long long p; do { p = 1; continue; } while ((long long)__ic_load(d0, Setting) > 0); __ic_store(d1, Setting, p); }",
		},
		{
			// Exactly one arm runs, so a local both arms write is written.
			name: "both arms of a conditional expression",
			src:  "void main(void) { long long x; long long y = (long long)__ic_load(d0, Setting) > 0 ? (x = 1) : (x = 2); __ic_store(d1, Setting, x + y); }",
		},
		{
			name: "the address of an array element, whose array the prologue zeroes",
			src:  "long long a[4];\nvoid main(void) { long long *q = &a[0]; *q = 7; __ic_store(d0, Setting, a[0]); }",
		},
		{
			name: "the address of an element of an array local, which lives in the data region",
			src:  "void main(void) { long long a[4]; long long *q = &a[2]; *q = 7; __ic_store(d0, Setting, a[2]); }",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := analyze(t, tt.src)
			if diags.HasErrors() {
				t.Errorf("the program was rejected:\n%s\n%s", tt.src, diags.String())
			}
		})
	}
}
