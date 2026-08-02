package sema_test

import (
	"regexp"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/parser"
	"github.com/greg2010/ic11c/internal/sema"
)

// typedefPattern matches the name each typedef in the generated C prelude
// introduces, which is the closing brace of the enum it aliases followed by the
// alias. The one-line dev declaration and the multi-line operand families take
// the same shape.
var typedefPattern = regexp.MustCompile(`(?m)\}\s*([A-Za-z_][A-Za-z0-9_]*);$`)

// TestPreludeTypeNamesAreNotDeclarable derives the reserved set from the header
// the generator writes rather than trusting the list in this package.
//
// The header is what an editor reads a MicroC program as, and a program
// declaring one of its typedef names is a C redefinition — a hard error there
// and, without this, silence here. Adding a family to the generator is what
// would otherwise open the gap again, which is why the expectation is read out
// of the generated text.
//
// A rejection by the parser counts: dev is a MicroC keyword, so a declaration
// taking that spelling never reaches analysis.
func TestPreludeTypeNamesAreNotDeclarable(t *testing.T) {
	matches := typedefPattern.FindAllStringSubmatch(ic10.Prelude, -1)
	if len(matches) < 2 {
		t.Fatalf("%s declares %d typedefs, which is too few to be the generated header", ic10.PreludeFileName, len(matches))
	}
	for _, match := range matches {
		name := match[1]
		t.Run(name, func(t *testing.T) {
			src := "long long " + name + ";\nvoid main(void) { __ic_store(d0, On, 1); }\n"
			file, diags := parser.Parse("test.c", src)
			if len(diags) != 0 {
				return
			}
			_, diags, err := sema.Analyze(t.Context(), file, testTables{})
			if err != nil {
				t.Fatalf("analyzing: %v", err)
			}
			if !diags.HasErrors() {
				t.Errorf("'%s' is declarable in MicroC and is a redefinition in C, which is what %s makes it", name, ic10.PreludeFileName)
			}
		})
	}
}

// TestReservedNamesAreNotDeclarable covers the spellings the language keeps for
// the machine.
//
// A device position and a named intrinsic operand resolve their argument from
// the machine tables without consulting scope, so a declaration taking one of
// those spellings would read as though it named the thing an argument means and
// would silently mean something else. The machine's own constants are reserved
// for the same reason from the other side: they are predeclared, and a
// declaration of one would be a redeclaration.
func TestReservedNamesAreNotDeclarable(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a global taking a pin spelling",
			src: `long long /*!*/d0;
void main(void) { __ic_store(d1, On, d0); }
`,
			want: "'d0' names a device pin",
		},
		{
			name: "a global taking the housing spelling",
			src: `long long /*!*/db;
void main(void) { __ic_store(d1, On, db); }
`,
			want: "'db' names a device pin",
		},
		{
			name: "a local taking a pin spelling",
			src: `void main(void) {
    long long /*!*/d3 = 1;
    __ic_store(d1, On, d3);
}
`,
			want: "'d3' names a device pin",
		},
		{
			name: "a parameter taking a pin spelling",
			src: `void publish(long long /*!*/d2) {
    __ic_store(d1, On, d2);
}
void main(void) { publish(1); }
`,
			want: "'d2' names a device pin",
		},
		{
			name: "a function taking a pin spelling",
			src: `long long /*!*/d4(void) { return 1; }
void main(void) { __ic_store(d1, On, d4()); }
`,
			want: "'d4' names a device pin",
		},
		{
			name: "a global taking a logic type",
			src: `long long /*!*/On;
void main(void) { __ic_store(d0, Setting, On); }
`,
			want: "'On' is a logic type",
		},
		{
			name: "a local taking a slot type",
			src: `void main(void) {
    long long /*!*/Occupied = 1;
    __ic_store(d0, Setting, Occupied);
}
`,
			want: "'Occupied' is a slot type",
		},
		{
			name: "a parameter taking a batch mode",
			src: `void publish(long long /*!*/Average) {
    __ic_store(d0, Setting, Average);
}
void main(void) { publish(1); }
`,
			want: "'Average' is a batch mode",
		},
		{
			name: "a global taking a reagent mode",
			src: `long long /*!*/Contents;
void main(void) { __ic_store(d0, Setting, Contents); }
`,
			want: "'Contents' is a reagent mode",
		},
		{
			name: "a global taking a machine constant",
			src: `double /*!*/pi;
void main(void) { __ic_store(d0, Setting, pi); }
`,
			want: "'pi' is one of the machine's own constants",
		},
		{
			name: "a local taking a machine constant",
			src: `void main(void) {
    double /*!*/rgas = 1.0;
    __ic_store(d0, Setting, rgas);
}
`,
			want: "'rgas' is one of the machine's own constants",
		},
		{
			name: "a global taking a prelude type name",
			src: `long long /*!*/ic10_logic;
void main(void) { __ic_store(d0, Setting, ic10_logic); }
`,
			want: "'ic10_logic' is the name the C prelude gives",
		},
		{
			name: "a function taking a prelude type name",
			src: `void /*!*/ic10_slot(void) { __ic_store(d0, On, 1); }
void main(void) { ic10_slot(); }
`,
			want: "'ic10_slot' is the name the C prelude gives",
		},
		{
			name: "a local taking a prelude type name",
			src: `void main(void) {
    long long /*!*/ic10_batch = 1;
    __ic_store(d0, Setting, ic10_batch);
}
`,
			want: "'ic10_batch' is the name the C prelude gives",
		},
		{
			name: "a parameter taking a prelude type name",
			src: `void publish(long long /*!*/ic10_reagent) {
    __ic_store(d0, Setting, ic10_reagent);
}
void main(void) { publish(1); }
`,
			want: "'ic10_reagent' is the name the C prelude gives",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectRejected(t, tt.src, tt.want)
		})
	}
}

// TestUnreservedNamesStayDeclarable holds the other edge of the rule. The
// reserved set is exactly what the machine resolves, so a spelling that only
// resembles one is an ordinary name.
func TestUnreservedNamesStayDeclarable(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "a pin the housing does not have",
			src:  "long long d9;\nvoid main(void) { d9 = 1; __ic_store(d0, On, d9); }",
		},
		{
			name: "a two-letter name that is not the housing",
			src:  "long long dx;\nvoid main(void) { dx = 1; __ic_store(d0, On, dx); }",
		},
		{
			name: "a logic type differing in case",
			src:  "long long on;\nvoid main(void) { on = 1; __ic_store(d0, On, on); }",
		},
		{
			name: "a machine constant differing in case",
			src:  "double PI;\nvoid main(void) { PI = 3.0; __ic_store(d0, Setting, PI); }",
		},
		{
			name: "the names the fixtures use",
			src:  "long long sw;\nlong long light;\nlong long heater;\nlong long gauge;\nlong long pump;\nvoid main(void) { __ic_store(d0, On, sw + light + heater + gauge + pump); }",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectAccepted(t, tt.src)
		})
	}
}
