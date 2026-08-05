package ic10_test

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/sema"
	"github.com/greg2010/ic11c/internal/tsparse"
)

// devEnumPattern matches the enumeration the header spells the device pins as,
// and pinPattern the pins inside it. db is deliberately not matched: it is the
// housing rather than a pin, and it is the pin count the two sides can disagree
// about.
var (
	devEnumPattern = regexp.MustCompile(`(?m)^typedef enum ic10_dev \{(.*)\} dev;$`)
	pinPattern     = regexp.MustCompile(`\bd(\d+)\b`)
)

// constantPattern matches one machine constant the header declares. Nothing
// else in the header is a constexpr double, so what this finds is that list and
// only it.
var constantPattern = regexp.MustCompile(`(?m)^constexpr double ([A-Za-z_][A-Za-z0-9_]*) = `)

// TestPreludeDeclaresThePinsMicroCResolves holds the header's device enum
// to the pins analysis actually resolves — nothing else ties the two,
// since tools/isagen writes the enum from its own copy of the limit and
// cannot depend on sema. A housing that grew a pin would otherwise leave
// an editor erroring on a program the compiler accepts, and one that lost
// a pin would leave it silent about a program the compiler rejects.
func TestPreludeDeclaresThePinsMicroCResolves(t *testing.T) {
	declared := declaredPins(t)
	// One past the highest pin declared rather than one past how many are, so
	// that a header skipping a pin still has every pin it names checked, and a
	// limit that moved either way is a failure rather than a check that happens
	// to still pass.
	highest := slices.Max(slices.Collect(maps.Keys(declared)))
	for pin := 0; pin <= highest+1; pin++ {
		t.Run(fmt.Sprintf("d%d", pin), func(t *testing.T) {
			accepted := analyzes(t, fmt.Sprintf("const dev probe = d%d;\n\nvoid main(void) {}\n", pin))
			if want := declared[pin]; accepted != want {
				t.Errorf("analysis resolves d%d = %t, and %s declares it = %t",
					pin, accepted, ic10.PreludeFileName, want)
			}
		})
	}
}

// TestPreludeDeclaresTheConstantsMicroCPredeclares holds the header's
// constant list to the names analysis predeclares, in both directions: the
// machine carries constants MicroC does not expose, so the ones the header
// leaves out are checked to still be names a program cannot use. Missing
// the other way would be an editor error on a program that compiles.
func TestPreludeDeclaresTheConstantsMicroCPredeclares(t *testing.T) {
	declared := declaredConstants(t)
	for _, constant := range ic10.Constants {
		t.Run(constant.Name, func(t *testing.T) {
			accepted := analyzes(t, fmt.Sprintf("double reading(void) {\n    return %s;\n}\n\nvoid main(void) {}\n", constant.Name))
			if want := declared[constant.Name]; accepted != want {
				t.Errorf("analysis predeclares %s = %t, and %s declares it = %t",
					constant.Name, accepted, ic10.PreludeFileName, want)
			}
		})
	}
	for name := range declared {
		if _, ok := ic10.LookupConstant(name); !ok {
			t.Errorf("%s declares %s, which the machine tables carry no constant for",
				ic10.PreludeFileName, name)
		}
	}
}

// declaredPins reads the pin numbers the header's device enum names.
func declaredPins(t *testing.T) map[int]bool {
	t.Helper()
	enum := devEnumPattern.FindStringSubmatch(ic10.Prelude)
	if enum == nil {
		t.Fatalf("%s declares no ic10_dev enum, so the pattern that finds it is what is wrong", ic10.PreludeFileName)
	}
	pins := make(map[int]bool)
	for _, match := range pinPattern.FindAllStringSubmatch(enum[1], -1) {
		var pin int
		if _, err := fmt.Sscanf(match[1], "%d", &pin); err != nil {
			t.Fatalf("pin %q in the ic10_dev enum: %v", match[1], err)
		}
		pins[pin] = true
	}
	if len(pins) == 0 {
		t.Fatalf("the ic10_dev enum of %s names no pin at all", ic10.PreludeFileName)
	}
	return pins
}

// declaredConstants reads the machine constants the header declares.
func declaredConstants(t *testing.T) map[string]bool {
	t.Helper()
	names := make(map[string]bool)
	for _, match := range constantPattern.FindAllStringSubmatch(ic10.Prelude, -1) {
		names[match[1]] = true
	}
	if len(names) == 0 {
		t.Fatalf("%s declares no machine constant, so the pattern that finds them is what is wrong", ic10.PreludeFileName)
	}
	return names
}

// analyzes reports whether src is a program the compiler's front end accepts.
// A parse problem fails the test rather than being reported as a rejection: the
// programs here are written to isolate one name, so anything else wrong with
// them is wrong with this file.
func analyzes(t *testing.T, src string) bool {
	t.Helper()
	file, diags, err := tsparse.Parse("prelude_test.c", src)
	if err != nil {
		t.Fatalf("parsing\n%s\n: %v", src, err)
	}
	if len(diags) != 0 {
		t.Fatalf("parsing\n%s\nreported %v", src, diags)
	}
	_, diags, err = sema.Analyze(context.Background(), file, sema.Shipped{})
	if err != nil {
		t.Fatalf("analyzing\n%s\n: %v", src, err)
	}
	return len(diags) == 0
}
