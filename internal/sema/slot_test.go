package sema_test

import "testing"

// TestAConstantStepIsHeldToTheObjectItStartsIn covers the addresses a subscript
// does not write but the expression fixes all the same.
//
// An array name decays to the address of its first element, so 'a + k' and
// '&a[i] + k' name a slot of the data region as surely as 'a[k]' does, and '&g'
// on a scalar names the one slot it occupies, which is the array of one C reads
// it as. The number is settled here or nowhere: instruction selection has a
// literal address by then, the verifier asks which object a pointer designates
// rather than where in it, and the chip checks neither — the data region and the
// call frames share one 512-slot array with nothing between them, so a slot past
// an object is another object or the return address the next call pushes.
func TestAConstantStepIsHeldToTheObjectItStartsIn(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a write far past the array, reached by arithmetic",
			src: `long long a[4];
void main(void) {
    *(a /*!*/+ 9000) = 1;
    __ic_store(d0, Setting, a[0]);
}
`,
			want: "an address in 'a' must be between 0 and 4, one past its last slot, found 9000",
		},
		{
			name: "a read far past the array, reached by arithmetic",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, *(a /*!*/+ 9000));
}
`,
			want: "an address in 'a' must be between 0 and 4, one past its last slot, found 9000",
		},
		{
			name: "the step written on the left",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, *(9000 /*!*/+ a));
}
`,
			want: "an address in 'a' must be between 0 and 4, one past its last slot, found 9000",
		},
		{
			name: "a step back before the first element",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, *(a /*!*/- 1));
}
`,
			want: "an address in 'a' must be between 0 and 4, one past its last slot, found -1",
		},
		{
			name: "the address alone, never read through",
			src: `long long a[4];
void main(void) {
    long long *p = a /*!*/+ 9000;
    __ic_store(d0, Setting, p == a);
}
`,
			want: "an address in 'a' must be between 0 and 4, one past its last slot, found 9000",
		},
		{
			name: "a step from an element's address",
			src: `long long a[4];
void main(void) {
    long long *p = &a[2] /*!*/+ 3;
    __ic_store(d0, Setting, p == a);
}
`,
			want: "an address in 'a' must be between 0 and 4, one past its last slot, found 5",
		},
		{
			name: "a step out of an array of double",
			src: `double a[3];
void main(void) {
    __ic_store(d0, Setting, *(a /*!*/+ 9000));
}
`,
			want: "an address in 'a' must be between 0 and 3, one past its last slot, found 9000",
		},
		{
			name: "a step the source spells as an expression",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, *(a /*!*/+ 2 * 3));
}
`,
			want: "an address in 'a' must be between 0 and 4, one past its last slot, found 6",
		},
		{
			name: "a step a constexpr object names",
			src: `constexpr long long kStride = 9000;
long long a[4];
void main(void) {
    __ic_store(d0, Setting, *(a /*!*/+ kStride));
}
`,
			want: "an address in 'a' must be between 0 and 4, one past its last slot, found 9000",
		},
		{
			name: "a read through the address one past the end",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, /*!*/*(a + 4));
}
`,
			want: "a slot of 'a' must be between 0 and 3, the last it has, found 4",
		},
		{
			name: "a write through the address one past the end",
			src: `long long a[4];
void main(void) {
    /*!*/*(a + 4) = 7;
    __ic_store(d0, Setting, a[0]);
}
`,
			want: "a slot of 'a' must be between 0 and 3, the last it has, found 4",
		},
		{
			name: "a compound assignment through the address one past the end",
			src: `long long a[4];
void main(void) {
    /*!*/*(a + 4) += 7;
    __ic_store(d0, Setting, a[0]);
}
`,
			want: "a slot of 'a' must be between 0 and 3, the last it has, found 4",
		},
		{
			name: "a subscript on a pointer the expression already stepped",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, (a + 2)[/*!*/3]);
}
`,
			want: "a slot of 'a' must be between 0 and 3, the last it has, found 5",
		},
		{
			name: "the address of a subscript on a pointer already stepped",
			src: `long long a[4];
void main(void) {
    long long *p = &(a + 2)[/*!*/3];
    __ic_store(d0, Setting, p == a);
}
`,
			want: "an address in 'a' must be between 0 and 4, one past its last slot, found 5",
		},
		{
			name: "a subscript back before the first element",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, (a + 2)[/*!*/-3]);
}
`,
			want: "a slot of 'a' must be between 0 and 3, the last it has, found -1",
		},
		{
			// The address the '&' takes back is read again by the '*' above it,
			// so the slot is reached after all.
			name: "a read through the address one past the end, taken back and dereferenced",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, *&/*!*/*(a + 4));
}
`,
			want: "a slot of 'a' must be between 0 and 3, the last it has, found 4",
		},
		{
			name: "a step out of the one slot a scalar occupies",
			src: `long long g;
void main(void) {
    long long *p = &g /*!*/+ 2;
    __ic_store(d0, Setting, p == &g);
}
`,
			want: "an address in 'g' must be between 0 and 1, one past its last slot, found 2",
		},
		{
			name: "a step out of the slot a pointer object occupies",
			src: `long long g;
void main(void) {
    long long *p = &g;
    long long **pp = &p /*!*/+ 2;
    __ic_store(d0, Setting, **pp);
}
`,
			want: "an address in 'p' must be between 0 and 1, one past its last slot, found 2",
		},
		{
			name: "a step from the address an assignment answers with",
			src: `long long a[4];
void main(void) {
    long long *p = a;
    long long *q = (p = a) /*!*/+ 9000;
    __ic_store(d0, Setting, q == p);
}
`,
			want: "an address in 'a' must be between 0 and 4, one past its last slot, found 9000",
		},
		{
			name: "a step from the address both arms of a conditional agree on",
			src: `long long a[4];
long long g;
void main(void) {
    long long *p = (g ? a : a) /*!*/+ 9000;
    __ic_store(d0, Setting, *p);
}
`,
			want: "an address in 'a' must be between 0 and 4, one past its last slot, found 9000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejectedWith(t, tt.src, tt.want)
		})
	}
}

// TestAnObjectOfOneSlotIsNamedRatherThanRanged covers the object whose only
// address is 0, where a range from 0 to 0 is accurate and reads as a compiler
// fault. Only the two messages about a slot reached need the other wording: the
// two about an address admit the one past the last, so 0 to 1 is a range.
func TestAnObjectOfOneSlotIsNamedRatherThanRanged(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a subscript past an array of one",
			src: `long long a[1];
void main(void) {
    __ic_store(d0, Setting, a[/*!*/1]);
}
`,
			want: "0 is the only index 'a' has, found 1",
		},
		{
			name: "a subscript before an array of one",
			src: `long long a[1];
void main(void) {
    __ic_store(d0, Setting, a[/*!*/-1]);
}
`,
			want: "0 is the only index 'a' has, found -1",
		},
		{
			name: "a read through the address one past an array of one",
			src: `long long a[1];
void main(void) {
    __ic_store(d0, Setting, /*!*/*(a + 1));
}
`,
			want: "0 is the only slot 'a' has, found 1",
		},
		{
			name: "a read through the address one past a scalar",
			src: `long long g;
void main(void) {
    __ic_store(d0, Setting, /*!*/*(&g + 1));
}
`,
			want: "0 is the only slot 'g' has, found 1",
		},
		{
			name: "a subscript past the one slot a scalar occupies",
			src: `long long g;
void main(void) {
    __ic_store(d0, Setting, (&g)[/*!*/1]);
}
`,
			want: "0 is the only slot 'g' has, found 1",
		},
		{
			name: "a subscript past the slot a pointer object occupies",
			src: `long long g;
void main(void) {
    long long *p = &g;
    __ic_store(d0, Setting, *(&p)[/*!*/1]);
}
`,
			want: "0 is the only slot 'p' has, found 1",
		},
		{
			name: "an address two past an array of one, which is still a range",
			src: `long long a[1];
void main(void) {
    long long *p = &a[/*!*/2];
    __ic_store(d0, Setting, p == a);
}
`,
			want: "an index under '&' must be between 0 and 1, one past the last index of 'a', found 2",
		},
		{
			name: "an address two past a scalar, which is still a range",
			src: `long long g;
void main(void) {
    long long *p = &g /*!*/+ 2;
    __ic_store(d0, Setting, p == &g);
}
`,
			want: "an address in 'g' must be between 0 and 1, one past its last slot, found 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejectedWith(t, tt.src, tt.want)
		})
	}
}

// TestTheBoundDiagnosticNamesTheDeclaration holds every bound message to the
// object the programmer wrote rather than to its type. Two objects of one type
// differ by name alone, so a message naming the type names both and leaves the
// reader to work out which one it is about.
func TestTheBoundDiagnosticNamesTheDeclaration(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "one of two global arrays that share a type",
			src: `long long first[4];
long long second[4];
void main(void) {
    __ic_store(d0, Setting, second[/*!*/4]);
}
`,
			want: "an array index must be between 0 and 3, the last index of 'second', found 4",
		},
		{
			name: "a local array shadowing a global of the same name",
			src: `long long a[4];
void main(void) {
    long long a[2] = {1, 2};
    __ic_store(d0, Setting, a[/*!*/2]);
}
`,
			want: "an array index must be between 0 and 1, the last index of 'a', found 2",
		},
		{
			name: "a parameter whose address is stepped",
			src: `void f(long long v) {
    long long *p = &v /*!*/+ 2;
    __ic_store(d0, Setting, p == &v);
}
void main(void) {
    f(1);
}
`,
			want: "an address in 'v' must be between 0 and 1, one past its last slot, found 2",
		},
		{
			name: "the array both arms of a conditional agree on",
			src: `long long first[4];
long long second[4];
long long g;
void main(void) {
    long long *p = (g ? second : second) /*!*/+ 9000;
    __ic_store(d0, Setting, *p);
}
`,
			want: "an address in 'second' must be between 0 and 4, one past its last slot, found 9000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejectedWith(t, tt.src, tt.want)
		})
	}
}

// TestTheStepThatLeftTheObjectIsTheOneReported holds the rule to one message per
// mistake. Every operator around the one that stepped out addresses a slot
// outside the array too, and naming each of them would bury the step the
// programmer has to change under the ones that only carried it.
func TestTheStepThatLeftTheObjectIsTheOneReported(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a second step past the first",
			src: `long long a[4];
void main(void) {
    long long *p = a /*!*/+ 9000 + 1;
    __ic_store(d0, Setting, p == a);
}
`,
			want: "an address in 'a' must be between 0 and 4, one past its last slot, found 9000",
		},
		{
			name: "a step back to an address the array has",
			src: `long long a[4];
void main(void) {
    long long *p = a /*!*/+ 9000 - 9000;
    __ic_store(d0, Setting, p == a);
}
`,
			want: "an address in 'a' must be between 0 and 4, one past its last slot, found 9000",
		},
		{
			name: "a read through a step that already left",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, *(a /*!*/+ 9000));
}
`,
			want: "an address in 'a' must be between 0 and 4, one past its last slot, found 9000",
		},
		{
			name: "a subscript on a step that already left",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, (a /*!*/+ 9000)[0]);
}
`,
			want: "an address in 'a' must be between 0 and 4, one past its last slot, found 9000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejectedWith(t, tt.src, tt.want)
		})
	}
}

// TestTheStepsTheObjectAdmitsStandAsWritten is the other edge of the rule, and
// what makes it safe to apply. Refusing an address C defines would refuse the
// loop every walk over an array is written as, and refusing a step no constant
// fixes would refuse the arithmetic arrays exist for.
func TestTheStepsTheObjectAdmitsStandAsWritten(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "the address one past the end",
			src: `long long a[4];
void main(void) {
    long long *p = a + 4;
    __ic_store(d0, Setting, p == a);
}
`,
		},
		{
			name: "the address of the last element",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, *(a + 3));
}
`,
		},
		{
			name: "a step out and back inside the array",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, *(a + 4 - 1));
}
`,
		},
		{
			name: "a step from an element's address",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, *(&a[1] + 2));
}
`,
		},
		{
			name: "a step back from the address one past the end",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, *(&a[4] - 1));
}
`,
		},
		{
			name: "a subscript on a pointer the expression stepped",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, (a + 2)[1]);
}
`,
		},
		{
			name: "a subscript back from a pointer the expression stepped",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, (a + 4)[-1]);
}
`,
		},
		{
			name: "the address one past the end reached by a subscript on a stepped pointer",
			src: `long long a[4];
void main(void) {
    long long *p = &(a + 2)[2];
    __ic_store(d0, Setting, p == a);
}
`,
		},
		{
			// '&*E' designates E without reading it, so the address one past
			// the end survives the '*' written under the '&'.
			name: "the address one past the end taken back through a dereference",
			src: `long long a[4];
void main(void) {
    long long *p = &*(a + 4);
    __ic_store(d0, Setting, p == a);
}
`,
		},
		{
			name: "the address one past the one slot a scalar occupies",
			src: `long long g;
void main(void) {
    long long *p = &g + 1;
    __ic_store(d0, Setting, p == &g);
}
`,
		},
		{
			name: "the one slot a scalar occupies, reached by a subscript",
			src: `long long g;
void main(void) {
    __ic_store(d0, Setting, (&g)[0]);
}
`,
		},
		{
			name: "arms of a conditional that address different slots of one array",
			src: `long long a[4];
long long g;
void main(void) {
    long long *p = (g ? a : a + 1) + 9000;
    __ic_store(d0, Setting, *p);
}
`,
		},
		{
			name: "a step the program computes",
			src: `long long a[4];
long long g;
void main(void) {
    g = 9000;
    __ic_store(d0, Setting, *(a + g));
}
`,
		},
		{
			name: "a step through a pointer object, which carries no offset",
			src: `long long a[4];
void main(void) {
    long long *p = a;
    __ic_store(d0, Setting, *(p + 9000));
}
`,
		},
		{
			// The offset would be settled if the object were, so this is where
			// a message would have no declaration to name. It reports nothing
			// instead: every settled slot starts at a name.
			name: "a step from the pointer a call answered with, which traces to no declaration",
			src: `long long a[4];
long long *first(void) {
    return a;
}
void main(void) {
    long long *p = first() + 9000;
    __ic_store(d0, Setting, *p);
}
`,
		},
		{
			name: "a step on a parameter, which carries no length",
			src: `void fill(long long *p) {
    *(p + 9000) = 1;
}
void main(void) {
    long long a[4];
    fill(a);
    __ic_store(d0, Setting, a[0]);
}
`,
		},
		{
			name: "the walk the address one past the end exists for",
			src: `long long a[4];
void main(void) {
    long long *p = a;
    while (p != a + 4) {
        *p = 0;
        p = p + 1;
    }
    __ic_store(d0, Setting, a[0]);
}
`,
		},
		{
			name: "the difference of two addresses in one array",
			src: `long long a[4];
void main(void) {
    __ic_store(d0, Setting, &a[4] - a);
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectAccepted(t, tt.src)
		})
	}
}
