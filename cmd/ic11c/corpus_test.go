package main

import (
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
)

// microcDoc is the language document. It is the one document a program's
// behaviour has to be predictable from, and the outcomes it states are compiled
// for real rather than read.
const microcDoc = "../../docs/microc.md"

// outcome is what the compiler does with a program: the three answers the
// language document states.
type outcome int

const (
	// unstated is a specimen that names no outcome. It is the zero value on
	// purpose: a row written without one would otherwise claim the document
	// refuses its program, and would pass or fail on a sentence nobody wrote
	// it against.
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
// delete. An outcome reached by deleting the construct is not the outcome the
// document states about it.
const specimenPin = "const dev sink = d0;\n"

// specimenFile is the name a specimen's diagnostics carry. Nothing reads it
// from disk: the source is handed to [compile] as text.
const specimenFile = "specimen.c"

// specimen is one program the language document states an outcome for. A
// refusal stated in prose and checked by nothing goes stale silently: a row
// claiming a slot index outside 0 through 511 is refused could outlive a
// compiler that only ever refused a negative one.
type specimen struct {
	name string
	// states is every sentence documenting the outcome, each matched literally
	// and required to appear exactly once. A row reworded or deleted reports here
	// rather than leaving the outcome stated nowhere and checked by nothing.
	states []string
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

// documentedOutcomes is every program whose outcome the language document
// states, against the sentence stating it. Each refusal row is covered with
// its own examples, plus the acceptance just across the boundary it draws:
// a row refusing an operand at exactly 2^53 is a claim about 2^53 − 1 too.
func documentedOutcomes() []specimen {
	const (
		windowRow      = "| A constant outside the representable window | `9007199254740993`, `(long long)1e18`, `9007199254740992 + 8` |"
		operandRow     = "| A bitwise or shift operand at exactly ±2^53 | `9007199254740992 & -1`, `~9007199254740992`, `9007199254740992 >> 1` |"
		leftShiftRow   = "| A left shift whose answer reaches 2^53 | `(long long)1 << 53` |"
		shiftWidthRow  = "| A shift count outside the width of the C type of its left operand | `1 << 32`, `1 << n` |"
		foldRow        = "| An integer fold whose answer depends on the C type of its literals | `2147483647 + 1`, `-1 < 0xFFFFFFFF` |"
		arrayBoundRow  = "| An array bound below 1 or above 512 | `long long a[0]`, `long long a[513]` |"
		slotIndexRow   = "| A negative constant slot index | `__ic_load_slot(pump, -1, Occupied)` |"
		slotUpperPara  = "A slot index has no upper bound here."
		representable  = "| Representable | What a `long long` denotes, and what `+`, `-`, `*`, `/`, `%` may fold to | −2^53 to 2^53, inclusive |"
		operandWindow  = "| Bitwise operand | What `&`, `\\|`, `^`, `~`, `<<`, `>>` may be handed | −(2^53 − 1) to 2^53 − 1 |"
		shiftCountRow  = "`1 << 31` is refused for the value it computes, and `1 << n` for a count no width bounds. Write `(long long)1 << n`"
		wideShiftPara  = "`(long long)1 << 60` has a C type wide enough to hold the result and is still rejected"
		castLiteral    = "Only as a floating literal that a cast converts, as in `(long long)3.5` |"
		devicePinRow   = "| Its initializer is `db`, `d0` through `d5`, or another `dev` object | Those are the spellings a device position takes |"
		escapeSentence = "`'\\xe9'` is 233 here and −23 there"
		sleepSentence  = "Any statement ahead of it clears the restriction."
		recursionRow   = "| Recursion deeper than the 512-slot array allows |"
		deviceSurface  = "| A property a device does not answer, or a slot it does not declare |"
		narrowRow      = "| An operand the chip narrows, not shown to lie in the signed 32-bit range | `__ic_store_batch((long long)__ic_load(in, Setting), On, 1.0)`, `__ic_store_batch(3000000000, On, 1.0)` |"
		guardSpelling  = "long long hash = (v > -2147483648.0 && v < 2147483648.0) ? (long long)v : __ic_hash(\"StructureWallLight\");"
		commonOperand  = "| An operand the common type does not hold | `-1 < 0xFFFFFFFF` | C converts `-1` to `unsigned int`, where it is 4294967295 |"
		commonResult   = "| A result the common type does not hold | `2147483647 + 1`, `1 << 31`, `0xFFFFFFFF + 1`, `-0x80000000` | C overflows an `int` in the first two and wraps an `unsigned int` in the last two |"
		commonShift    = "| A shift count outside the width of the left operand's type | `1 << 32` | C shifts an `int`, whose width is 32; the count says nothing about the type |"
		negativeShift  = "| A left shift of a negative value | `-1 << 1` | C leaves it undefined |"
		decimalWayOut  = "Writing the constant in decimal is the other way out, since a decimal constant never lands on an unsigned type. `2147483648` is the number `-0x80000000` was reaching for."
	)
	return []specimen{
		{name: "a constant past the exact-integer window", states: []string{windowRow}, value: "9007199254740993", want: refused},
		{name: "a fold landing past the exact-integer window", states: []string{windowRow}, value: "9007199254740992 + 8", want: refused},
		{name: "a constant at the top of the exact-integer window", states: []string{representable}, value: "9007199254740992", want: accepted},

		{name: "an and operand at exactly 2^53", states: []string{operandRow}, value: "9007199254740992 & -1", want: refused},
		{name: "a not operand at exactly 2^53", states: []string{operandRow}, value: "~9007199254740992", want: refused},
		{name: "a shift operand at exactly 2^53", states: []string{operandRow}, value: "9007199254740992 >> 1", want: refused},
		{name: "a not operand one below 2^53", states: []string{operandWindow}, value: "~9007199254740991", want: accepted},

		{name: "a left shift answering exactly 2^53", states: []string{leftShiftRow}, value: "(long long)1 << 53", want: refused},
		{name: "a left shift answering past 2^53", states: []string{wideShiftPara}, value: "(long long)1 << 60", want: refused},

		{name: "a shift count at the width of a bare literal", states: []string{shiftCountRow, commonResult}, value: "1 << 31", want: refused},
		{name: "a shift count past the width of a bare literal", states: []string{shiftWidthRow, commonShift}, value: "1 << 32", want: refused},
		{
			name:   "a variable shift count over a bare literal",
			states: []string{shiftWidthRow},
			src: "void main(void) { long long n = (long long)__ic_load(sink, Setting);" +
				" long long v = 1 << n; __ic_store(sink, Setting, (double)v); }\n",
			want: refused,
		},
		{
			name:   "a variable shift count over a widened literal",
			states: []string{shiftCountRow},
			src: "void main(void) { long long n = (long long)__ic_load(sink, Setting);" +
				" long long v = (long long)1 << n; __ic_store(sink, Setting, (double)v); }\n",
			want: accepted,
		},

		{name: "a fold overflowing the C type of its literals", states: []string{foldRow, commonResult}, value: "2147483647 + 1", want: refused},
		{name: "a hexadecimal fold wrapping the C type of its literals", states: []string{commonResult}, value: "0xFFFFFFFF + 1", want: refused},
		{name: "a negation wrapping the C type of a hexadecimal literal", states: []string{commonResult}, value: "-0x80000000", want: refused},
		{name: "the decimal spelling that negation was reaching for", states: []string{decimalWayOut}, value: "-2147483648", emits: "-2147483648", want: accepted},
		{name: "a comparison converting through the C type of its literals", states: []string{foldRow, commonOperand}, value: "-1 < 0xFFFFFFFF", want: refused},
		{name: "a left shift of a negative value", states: []string{negativeShift}, value: "-1 << 1", want: refused},

		{
			name:   "an array bound below one",
			states: []string{arrayBoundRow},
			src:    "long long a[0];\nvoid main(void) { a[0] = 1; __ic_store(sink, Setting, (double)a[0]); }\n",
			want:   refused,
		},
		{
			name:   "an array bound past the data region",
			states: []string{arrayBoundRow},
			src:    "long long a[513];\nvoid main(void) { a[0] = 1; __ic_store(sink, Setting, (double)a[0]); }\n",
			want:   refused,
		},
		{
			name:   "an array bound computed from a double",
			states: []string{castLiteral},
			src:    "long long a[(long long)(3.5*2.0)];\nvoid main(void) { a[0] = 1; __ic_store(sink, Setting, (double)a[0]); }\n",
			want:   refused,
		},
		{
			name:   "an array bound a cast converts from a floating literal",
			states: []string{castLiteral},
			src:    "long long a[(long long)3.5];\nvoid main(void) { a[0] = 1; __ic_store(sink, Setting, (double)a[0]); }\n",
			want:   accepted,
		},

		{
			name:   "a device pin the chip's housing does not carry",
			states: []string{devicePinRow},
			src:    "const dev far = d6;\nvoid main(void) { __ic_store(far, Setting, 1.0); }\n",
			want:   refused,
		},
		{
			name:   "a negative constant slot index",
			states: []string{slotIndexRow},
			src:    "void main(void) { __ic_store(sink, Setting, __ic_load_slot(sink, -1, Occupied)); }\n",
			want:   refused,
		},
		{
			name:   "a constant slot index past any device's slots",
			states: []string{slotUpperPara},
			src:    "void main(void) { __ic_store(sink, Setting, __ic_load_slot(sink, 600, Occupied)); }\n",
			want:   accepted,
		},

		{
			name:   "a batch prefab operand a device read supplies",
			states: []string{narrowRow},
			src:    "void main(void) { __ic_store_batch((long long)__ic_load(sink, Setting), On, 1.0); }\n",
			want:   refused,
		},
		{
			name:   "a batch prefab operand a literal puts past the narrowing window",
			states: []string{narrowRow},
			src:    "void main(void) { __ic_store_batch(3000000000, On, 1.0); }\n",
			want:   refused,
		},
		{
			name:   "a batch prefab operand a guard holds inside the narrowing window",
			states: []string{guardSpelling},
			src: "void main(void) { double v = __ic_load(sink, Setting);\n" +
				guardSpelling + "\n__ic_store_batch(hash, On, 1.0); }\n",
			want: accepted,
		},

		{
			name:   "a numeric escape above 127",
			states: []string{escapeSentence},
			value:  `'\xe9'`,
			emits:  "233",
			want:   accepted,
		},
		{
			name:   "a sleep the emitted program leads with",
			states: []string{sleepSentence},
			src:    "void main(void) { __ic_sleep(1.0); __ic_store(sink, Setting, 1.0); }\n",
			want:   refused,
		},

		{
			name:   "a recursion whose depth is not decided at compile time",
			states: []string{recursionRow},
			src: "long long fib(long long n) { if (n < 2) return n; return fib(n - 1) + fib(n - 2); }\n" +
				"void main(void) { __ic_store(sink, Setting, (double)fib((long long)__ic_load(sink, Setting))); }\n",
			want: warned,
		},
		{
			name:   "a slot index past what a declared prefab holds",
			states: []string{deviceSurface},
			src: "[[ic11c::prefab(\"StructureFurnace\")]] const dev furnace = d1;\n" +
				"void main(void) { __ic_store(furnace, Setting, __ic_load_slot(furnace, 99, Occupied)); }\n",
			want: warned,
		},
		{
			name:   "a property a declared prefab does not answer",
			states: []string{deviceSurface},
			src: "[[ic11c::prefab(\"StructureFurnace\")]] const dev furnace = d1;\n" +
				"void main(void) { __ic_store(furnace, Setting, __ic_load(furnace, Charge)); }\n",
			want: warned,
		},
	}
}

// TestDocumentedOutcomes holds every outcome the language document states to
// the outcome the compiler produces, and the sentence stating it to still
// being there: a compiler that started accepting what a row refuses is
// caught by the compile, and a reworded row is caught by the missing sentence.
func TestDocumentedOutcomes(t *testing.T) {
	document := languageDocument(t)

	for _, s := range documentedOutcomes() {
		t.Run(s.name, func(t *testing.T) {
			if (s.value == "") == (s.src == "") {
				t.Fatalf("the specimen sets %q as an expression and %q as a whole program; it is one or the other", s.value, s.src)
			}
			if s.want == unstated {
				t.Fatalf("the specimen names no outcome, so the sentence it quotes is checked against nothing")
			}
			if len(s.states) == 0 {
				t.Fatalf("the specimen quotes no sentence, so what %s says about this program is checked against nothing", microcDoc)
			}
			for _, states := range s.states {
				if n := strings.Count(document, states); n != 1 {
					t.Fatalf("%s states %q %d times, and it is the sentence saying this program is %s; a sentence reworded or removed leaves the outcome checked by nothing, which is how a refusal the compiler does not make survived",
						microcDoc, states, n, s.want)
				}
			}
			program := s.program()
			got, emitted := compileOutcome(t, program)
			if got != s.want {
				t.Errorf("the compiler leaves this program %s where %s states it is %s:\n%s",
					got, microcDoc, s.want, program)
				return
			}
			if s.emits != "" && !strings.Contains(emitted, s.emits) {
				t.Errorf("the emitted program does not contain %q, which %s states it computes:\n%s",
					s.emits, microcDoc, emitted)
			}
		})
	}
}

// refusalTableHeaders are the header rows of the language document's two tables
// of what the compiler refuses. Every row under one names a construct and an
// example of it, which is a program a specimen can compile.
var refusalTableHeaders = []string{
	"| Refused | Example |",
	"| Rejected | Example | Why |",
}

// tableDivider is the row markdown puts between a table's header and its body.
var tableDivider = regexp.MustCompile(`^\|[-:| ]+\|$`)

// unreachableRefusals are the refusal rows no MicroC program reaches, against
// why each is out of reach. Anything not here needs a specimen.
var unreachableRefusals = map[string]string{
	"| An optimizer-formed bitwise operation over a value that may be non-finite | none known: the rewrites this pipeline makes leave one-bit values the guard has nothing to refuse |": "the row names no example, and the guard stands over an operation this pipeline's rewrites do not form",
}

// TestEveryRefusalRowHasASpecimen holds every row of the language document's
// refusal tables to a specimen compiling it — the direction
// [TestDocumentedOutcomes] does not run: a row nothing quotes states a
// refusal the compiler is never asked to make.
func TestEveryRefusalRowHasASpecimen(t *testing.T) {
	var quoted []string
	for _, s := range documentedOutcomes() {
		quoted = append(quoted, s.states...)
	}
	rows := refusalRows(t, languageDocument(t))
	for _, row := range rows {
		reason, excused := unreachableRefusals[row]
		compiled := slices.ContainsFunc(quoted, func(sentence string) bool {
			return strings.Contains(row, sentence)
		})
		switch {
		case compiled && excused:
			t.Errorf("a specimen quotes %s, so excusing it as %s is stale; drop it from unreachableRefusals", row, reason)
		case !compiled && !excused:
			t.Errorf("no specimen quotes %s, so %s states this refusal and nothing makes it; write one into documentedOutcomes, drop the row, or record in unreachableRefusals why no program reaches it",
				row, microcDoc)
		}
	}
	for row := range unreachableRefusals {
		if !slices.Contains(rows, row) {
			t.Errorf("unreachableRefusals excuses %s, which %s no longer carries under a refusal table", row, microcDoc)
		}
	}
}

// refusalRows returns the body rows of every table in document headed by
// one of [refusalTableHeaders]. A missing header, divider, or empty table is
// reported rather than silently returning fewer rows, since a row
// enumeration that quietly comes back short is a gate that proves nothing.
func refusalRows(t *testing.T, document string) []string {
	t.Helper()
	lines := strings.Split(document, "\n")
	var rows []string
	for _, header := range refusalTableHeaders {
		found := 0
		for i, line := range lines {
			if line != header {
				continue
			}
			found++
			if i+1 >= len(lines) || !tableDivider.MatchString(lines[i+1]) {
				t.Errorf("%s does not follow the header %s with a table divider, so the rows under it cannot be read", microcDoc, header)
				continue
			}
			body := 0
			for j := i + 2; j < len(lines) && strings.HasPrefix(lines[j], "|"); j++ {
				rows = append(rows, lines[j])
				body++
			}
			if body == 0 {
				t.Errorf("%s carries the header %s with no rows under it", microcDoc, header)
			}
		}
		if found != 1 {
			t.Errorf("%s carries the header %s %d times, and the rows under it are the refusals this test enumerates", microcDoc, header, found)
		}
	}
	return rows
}

// languageDocument reads the language document, which several tests compile
// against rather than read.
func languageDocument(t *testing.T) string {
	t.Helper()
	text, err := os.ReadFile(microcDoc)
	if err != nil {
		t.Fatalf("reading %s: %v", microcDoc, err)
	}
	return string(text)
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
