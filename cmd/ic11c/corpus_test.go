package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

// outcome is what the compiler does with a program: the three answers a
// specimen can pin.
type outcome int

const (
	// unstated is a specimen that names no outcome. The zero value on purpose,
	// so one written without an outcome is refused rather than passing.
	unstated outcome = iota
	// refused rejects the program, and nothing is emitted.
	refused
	// warned emits the program and says something about it.
	warned
	// accepted emits the program and says nothing at all.
	accepted
)

func (o outcome) String() string {
	switch o {
	case unstated:
		return "no outcome at all"
	case refused:
		return "refused"
	case warned:
		return "accepted with a warning"
	case accepted:
		return "accepted with no diagnostic"
	default:
		return "outcome(" + strconv.Itoa(int(o)) + ")"
	}
}

// specimenPin is the device every specimen writes through, so that the value
// under study is read by something and the optimizer has no dead expression to
// delete. An outcome reached by deleting the construct is not the outcome of
// the construct.
const specimenPin = "const dev sink = d0;\n"

// specimenFile is the name a specimen's diagnostics carry. Nothing reads it
// from disk: the source is handed to [compile] as text.
const specimenFile = "specimen.c"

// specimen is one program with the outcome the compiler owes it.
type specimen struct {
	name string
	// value is an expression bound to a long long and written to the pin, which
	// is the shape most of these take, and src is the whole program after the
	// pin declaration, for one that needs another. Exactly one is set.
	value string
	src   string
	// emits is text the emitted program has to contain, for a row whose claim is
	// the value produced and not the outcome alone.
	emits string
	want  outcome
}

// program is the specimen as the compiler reads it.
func (s specimen) program() string {
	if s.src != "" {
		return specimenPin + s.src
	}
	return specimenPin + "void main(void) { long long v = " + s.value +
		"; __ic_store(sink, Setting, (double)v); }\n"
}

// pinnedOutcomes covers each refusal with its own examples, plus the acceptance
// just across the boundary it draws.
func pinnedOutcomes() []specimen {
	return []specimen{
		{name: "a constant past the exact-integer window", value: "9007199254740993", want: refused},
		{name: "a fold landing past the exact-integer window", value: "9007199254740992 + 8", want: refused},
		{name: "a constant at the top of the exact-integer window", value: "9007199254740992", want: accepted},

		{name: "an and operand at exactly 2^53", value: "9007199254740992 & -1", want: refused},
		{name: "a not operand at exactly 2^53", value: "~9007199254740992", want: refused},
		{name: "a shift operand at exactly 2^53", value: "9007199254740992 >> 1", want: refused},
		{name: "a not operand one below 2^53", value: "~9007199254740991", want: accepted},

		{name: "a left shift answering exactly 2^53", value: "(long long)1 << 53", want: refused},
		{name: "a left shift answering past 2^53", value: "(long long)1 << 60", want: refused},

		{name: "a shift count at the width of a bare literal", value: "1 << 31", want: refused},
		{name: "a shift count past the width of a bare literal", value: "1 << 32", want: refused},
		{
			name: "a variable shift count over a bare literal",
			src: "void main(void) { long long n = (long long)__ic_load(sink, Setting);" +
				" long long v = 1 << n; __ic_store(sink, Setting, (double)v); }\n",
			want: refused,
		},
		{
			name: "a variable shift count over a widened literal",
			src: "void main(void) { long long n = (long long)__ic_load(sink, Setting);" +
				" long long v = (long long)1 << n; __ic_store(sink, Setting, (double)v); }\n",
			want: accepted,
		},

		{name: "a fold overflowing the C type of its literals", value: "2147483647 + 1", want: refused},
		{name: "a hexadecimal fold wrapping the C type of its literals", value: "0xFFFFFFFF + 1", want: refused},
		{name: "a negation wrapping the C type of a hexadecimal literal", value: "-0x80000000", want: refused},
		{name: "the decimal spelling that negation was reaching for", value: "-2147483648", emits: "-2147483648", want: accepted},
		{name: "a comparison converting through the C type of its literals", value: "-1 < 0xFFFFFFFF", want: refused},
		{name: "a left shift of a negative value", value: "-1 << 1", want: refused},

		{
			name: "an array bound below one",
			src:  "long long a[0];\nvoid main(void) { a[0] = 1; __ic_store(sink, Setting, (double)a[0]); }\n",
			want: refused,
		},
		{
			name: "an array bound past the data region",
			src:  "long long a[513];\nvoid main(void) { a[0] = 1; __ic_store(sink, Setting, (double)a[0]); }\n",
			want: refused,
		},
		{
			name: "an array bound computed from a double",
			src:  "long long a[(long long)(3.5*2.0)];\nvoid main(void) { a[0] = 1; __ic_store(sink, Setting, (double)a[0]); }\n",
			want: refused,
		},
		{
			name: "an array bound a cast converts from a floating literal",
			src:  "long long a[(long long)3.5];\nvoid main(void) { a[0] = 1; __ic_store(sink, Setting, (double)a[0]); }\n",
			want: accepted,
		},

		{
			name: "a device pin the chip's housing does not carry",
			src:  "const dev far = d6;\nvoid main(void) { __ic_store(far, Setting, 1.0); }\n",
			want: refused,
		},
		{
			name: "a negative constant slot index",
			src:  "void main(void) { __ic_store(sink, Setting, __ic_load_slot(sink, -1, Occupied)); }\n",
			want: refused,
		},
		{
			name: "a constant slot index past any device's slots",
			src:  "void main(void) { __ic_store(sink, Setting, __ic_load_slot(sink, 600, Occupied)); }\n",
			want: accepted,
		},

		{
			name: "a batch prefab operand a device read supplies",
			src:  "void main(void) { __ic_store_batch((long long)__ic_load(sink, Setting), On, 1.0); }\n",
			want: refused,
		},
		{
			name: "a batch prefab operand a literal puts past the narrowing window",
			src:  "void main(void) { __ic_store_batch(3000000000, On, 1.0); }\n",
			want: refused,
		},
		{
			name: "a batch prefab operand a guard holds inside the narrowing window",
			src: "void main(void) { double v = __ic_load(sink, Setting);\n" +
				"long long hash = (v > -2147483648.0 && v < 2147483648.0)" +
				" ? (long long)v : __ic_hash(\"StructureWallLight\");\n" +
				"__ic_store_batch(hash, On, 1.0); }\n",
			want: accepted,
		},

		{
			name:  "a numeric escape above 127",
			value: `'\xe9'`,
			emits: "233",
			want:  accepted,
		},
		{
			name: "a sleep the emitted program leads with",
			src:  "void main(void) { __ic_sleep(1.0); __ic_store(sink, Setting, 1.0); }\n",
			want: refused,
		},

		{
			name: "a recursion whose depth is not decided at compile time",
			src: "long long fib(long long n) { if (n < 2) return n; return fib(n - 1) + fib(n - 2); }\n" +
				"void main(void) { __ic_store(sink, Setting, (double)fib((long long)__ic_load(sink, Setting))); }\n",
			want: warned,
		},
		{
			name: "a slot index past what a declared prefab holds",
			src: "[[ic11c::prefab(\"StructureFurnace\")]] const dev furnace = d1;\n" +
				"void main(void) { __ic_store(furnace, Setting, __ic_load_slot(furnace, 99, Occupied)); }\n",
			want: warned,
		},
		{
			name: "a property a declared prefab does not answer",
			src: "[[ic11c::prefab(\"StructureFurnace\")]] const dev furnace = d1;\n" +
				"void main(void) { __ic_store(furnace, Setting, __ic_load(furnace, Charge)); }\n",
			want: warned,
		},
	}
}

// TestPinnedOutcomes holds every specimen to the outcome the compiler owes it.
func TestPinnedOutcomes(t *testing.T) {
	for _, s := range pinnedOutcomes() {
		t.Run(s.name, func(t *testing.T) {
			if (s.value == "") == (s.src == "") {
				t.Fatalf("the specimen sets %q as an expression and %q as a whole program; it is one or the other", s.value, s.src)
			}
			if s.want == unstated {
				t.Fatalf("the specimen names no outcome, so its program is compiled against nothing")
			}
			program := s.program()
			got, emitted := compileOutcome(t, program)
			if got != s.want {
				t.Errorf("the compiler leaves this program %s where the specimen pins it %s:\n%s", got, s.want, program)
				return
			}
			if s.emits != "" && !strings.Contains(emitted, s.emits) {
				t.Errorf("the emitted program does not contain %q, which the specimen pins it to compute:\n%s", s.emits, emitted)
			}
		})
	}
}

// compileOutcome compiles one specimen through the shipped configuration and
// reports what the compiler made of it, with the assembly for a program it
// accepted.
func compileOutcome(t *testing.T, src string) (outcome, string) {
	t.Helper()
	output, diags, err := compile(t.Context(), specimenFile, src, options{})
	if err != nil {
		t.Fatalf("compiling the specimen: %v", err)
	}
	switch {
	case diags.HasErrors():
		return refused, ""
	case len(diags) > 0:
		return warned, output.Text
	default:
		return accepted, output.Text
	}
}

// TestBehaviouralAssertionsNameTheCorpus holds the table of fixtures with a
// behavioural test to naming fixtures the corpus has and tests this package
// declares. Nothing derives the table, so what holds it to reality is that
// the test takes its fixture from here, and both directions of drift fail.
func TestBehaviouralAssertionsNameTheCorpus(t *testing.T) {
	requireNamedInCorpus(t, "behaviourallyAsserted", behaviourallyAsserted, corpusFixtures(t))
	requireTestsDeclared(t, "behaviourallyAsserted", behaviourallyAsserted)
}

// TestPreludeNumbersEveryOperandName holds each operand name to the number
// the generated tables give it, in the spelling the prelude declares. C
// admits one enumerator per name in a scope, so a name shared across
// families is spelled with a prefix past the first family carrying it.
func TestPreludeNumbersEveryOperandName(t *testing.T) {
	declared := preludeEnumerators(t)
	var unplaced []string
	for _, family := range []struct {
		prefix        string
		slotPosition  bool
		batchPosition bool
		// values is the family in the order its generated table carries it.
		values []operandValue
	}{
		{prefix: ic10.LogicTypePrefix, values: operandValues(ic10.LogicTypes, func(i ic10.LogicTypeInfo) (string, int) { return i.Name, int(i.Value) })},
		{prefix: ic10.SlotTypePrefix, slotPosition: true, values: operandValues(ic10.LogicSlotTypes, func(i ic10.LogicSlotTypeInfo) (string, int) { return i.Name, int(i.Value) })},
		{prefix: ic10.BatchModePrefix, batchPosition: true, values: operandValues(ic10.BatchModes, func(i ic10.BatchModeInfo) (string, int) { return i.Name, int(i.Value) })},
		{prefix: ic10.ReagentModePrefix, values: operandValues(ic10.ReagentModes, func(i ic10.ReagentModeInfo) (string, int) { return i.Name, int(i.Value) })},
	} {
		for _, operand := range family.values {
			spelling := family.prefix + operand.name
			value, prefixed := declared[spelling]
			if !prefixed {
				if bare, known := declared[operand.name]; !known || bare != operand.value {
					t.Errorf("%s declares neither %s nor %s as %d, so a native build would not read a program naming it as this compiler does",
						ic10.PreludeFileName, operand.name, spelling, operand.value)
				}
				continue
			}
			if value != operand.value {
				t.Errorf("%s declares %s as %d and the tables number it %d, so a native build would not read a program naming it as this compiler does",
					ic10.PreludeFileName, spelling, value, operand.value)
				continue
			}
			if !family.slotPosition && !family.batchPosition {
				unplaced = append(unplaced, spelling)
			}
		}
	}
	if len(unplaced) > 0 {
		t.Errorf("%s spells %s with a prefix, and each stands in neither the slot position nor the batch-mode one, which are the only positions a prefixed spelling is compared through",
			ic10.PreludeFileName, strings.Join(unplaced, ", "))
	}
}

// operandValue is one operand name and the number MicroC gives it.
type operandValue struct {
	name  string
	value int
}

// operandValues is one operand family's names and the numbers MicroC gives
// them, in the order the generated table carries them.
func operandValues[I any](infos []I, read func(I) (string, int)) []operandValue {
	out := make([]operandValue, 0, len(infos))
	for _, info := range infos {
		name, value := read(info)
		out = append(out, operandValue{name: name, value: value})
	}
	return out
}
