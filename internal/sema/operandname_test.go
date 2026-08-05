package sema_test

import (
	"testing"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/sema"
)

// TestOperandNamesResolveInOneNamespace covers the rule that makes MicroC a C
// subset: an operand name means what the generated prelude declares it as,
// wherever it stands, and a family whose member the prelude had to prefix
// resolves the prefixed spelling in that position and nothing else.
//
// The stub's Lock is a logic type numbered 10 and a slot type numbered 23, and
// its Minimum is a logic type numbered 277 and a batch mode numbered 2, which is
// the shape the game's own tables have.
func TestOperandNamesResolveInOneNamespace(t *testing.T) {
	tests := []struct {
		name  string
		call  string
		index int
		want  sema.Operand
	}{
		{
			name:  "a name only one family carries",
			call:  "__ic_load_slot(d0, 0, Occupied)",
			index: 2,
			want:  sema.Operand{Kind: sema.OperandSlotType, Name: "Occupied", Value: 0, Resolved: true},
		},
		{
			name:  "a shared name in the family that claims it",
			call:  "__ic_load(d0, Lock)",
			index: 1,
			want:  sema.Operand{Kind: sema.OperandLogicType, Name: "Lock", Value: 10, Resolved: true},
		},
		{
			name:  "the prefixed spelling of a name the position gave up",
			call:  "__ic_load_slot(d0, 0, SlotType_Lock)",
			index: 2,
			want:  sema.Operand{Kind: sema.OperandSlotType, Name: "SlotType_Lock", Value: 23, Resolved: true},
		},
		{
			name:  "the prefixed spelling of a batch mode",
			call:  "__ic_load_batch(1, Temperature, BatchMode_Minimum)",
			index: 2,
			want:  sema.Operand{Kind: sema.OperandBatchMode, Name: "BatchMode_Minimum", Value: 2, Resolved: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "double v;\nvoid main(void) {\n    v = " + tt.call + ";\n}\n"
			prog, _ := analyze(t, src)
			got := onlyIntrinsic(t, prog).Args[tt.index]
			if got != tt.want {
				t.Errorf("%s operand %d = %+v, want %+v", tt.call, tt.index, got, tt.want)
			}
		})
	}
}

// TestOperandNamesRejectedInOneNamespace covers what the same rule refuses, and
// that each refusal names the spelling that would have worked. A program written
// against the old position-based resolution is what these are for: it compiles as
// C and means something else there, so the diagnostic is the migration path.
func TestOperandNamesRejectedInOneNamespace(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a bare slot type an earlier family claims",
			src: `void main(void) {
    __ic_store_slot(d0, 0, /*!*/Lock, 1);
}
`,
			want: "'Lock' is a logic type; an operand name resolves in one namespace, as it does in C, so the bare spelling means that in every position — the slot type of the same name is spelled 'SlotType_Lock'",
		},
		{
			name: "a bare batch mode an earlier family claims",
			src: `double v;
void main(void) {
    v = __ic_load_batch(__ic_hash("StructureStubSensor"), Temperature, /*!*/Minimum);
}
`,
			want: "the batch mode of the same name is spelled 'BatchMode_Minimum'",
		},
		{
			name: "a prefixed spelling for a name the position already owns",
			src: `double v;
void main(void) {
    v = __ic_load_slot(d0, 0, /*!*/SlotType_Occupied);
}
`,
			want: "'SlotType_Occupied' is not a slot type; only a name an earlier family has already taken is spelled with 'SlotType_', so write 'Occupied'",
		},
		{
			name: "a prefixed spelling in another family's position",
			src: `double v;
void main(void) {
    v = __ic_load(d0, /*!*/SlotType_Lock);
}
`,
			want: "'SlotType_Lock' is not a logic type",
		},
		{
			name: "a prefixed spelling of a name no family carries",
			src: `double v;
void main(void) {
    v = __ic_load_slot(d0, 0, /*!*/SlotType_Nonsense);
}
`,
			want: "'SlotType_Nonsense' is not a slot type",
		},
		{
			name: "a declaration taking a prefixed spelling",
			src: `long long /*!*/SlotType_Lock;
void main(void) {
}
`,
			want: "'SlotType_Lock' is a slot type; the spelling is reserved",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejected(t, tt.src, tt.want)
		})
	}
}

// TestPrefixedSpellingTheHeaderDoesNotDeclareIsAnOrdinaryName covers the other
// side of the reservation. Only a name an earlier family took is prefixed, so
// every other prefixed spelling is an identifier the prelude never declares and
// a program is free to use for something of its own.
func TestPrefixedSpellingTheHeaderDoesNotDeclareIsAnOrdinaryName(t *testing.T) {
	expectAccepted(t, `long long SlotType_Occupied;
void main(void) {
    SlotType_Occupied = 1;
}
`)
}

// onlyIntrinsic returns the one intrinsic call a program makes.
func onlyIntrinsic(t *testing.T, prog *sema.Program) *sema.IntrinsicCall {
	t.Helper()
	var only *ast.CallExpr
	for call := range prog.Intrinsics {
		if only != nil {
			t.Fatalf("the program records %d intrinsic calls, want exactly 1", len(prog.Intrinsics))
		}
		only = call
	}
	if only == nil {
		t.Fatal("the program records no intrinsic call")
	}
	return prog.Intrinsics[only]
}
