package ic10_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/greg2010/ic11c/internal/ic10"
	"github.com/greg2010/ic11c/internal/sema"
)

// prototypePattern matches one function declaration in the generated
// header. Nothing else in the header declares a function, so this finds
// the intrinsic list and only it. The result type is matched lazily since
// it can be more than one word — the name is the last identifier before
// the parameter list.
var prototypePattern = regexp.MustCompile(`(?m)^(.+?) +([A-Za-z_][A-Za-z0-9_]*)\(([^)]*)\);$`)

// parameterName matches the identifier a declaration gives its parameter, which
// is what has to come off to leave the type.
var parameterName = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*$`)

// TestPreludeDeclaresEveryIntrinsic checks the checked-in header against
// the table sema resolves an intrinsic call through — nothing else ties
// the two together, since tools/isagen writes the prototypes as literal
// text and cannot import sema. Compares whole signatures, not just names;
// it cannot see a slot index passed as an ordinary int, which C spells the
// same way.
func TestPreludeDeclaresEveryIntrinsic(t *testing.T) {
	declared := declaredPrototypes(t)

	defined := make(map[string]bool, len(sema.Intrinsics))
	for _, in := range sema.Intrinsics {
		defined[in.Name] = true
		t.Run(in.Name, func(t *testing.T) {
			want := cSignature(t, in)
			got, ok := declared[in.Name]
			if !ok {
				t.Fatalf("%s is an intrinsic and %s declares no such function; it should read %q",
					in.Name, ic10.PreludeFileName, want)
			}
			if got != want {
				t.Errorf("%s declares %q, and the intrinsic is %q", ic10.PreludeFileName, got, want)
			}
		})
	}

	for name := range declared {
		if !defined[name] {
			t.Errorf("%s declares %s, which is not an intrinsic; a program calling it would compile here and be rejected by the compiler",
				ic10.PreludeFileName, name)
		}
	}
}

// declaredPrototypes reads the header's function declarations into the normal
// form cSignature renders sema's table in.
func declaredPrototypes(t *testing.T) map[string]string {
	t.Helper()
	declared := make(map[string]string)
	for _, match := range prototypePattern.FindAllStringSubmatch(ic10.Prelude, -1) {
		result, name, params := match[1], match[2], match[3]
		if _, seen := declared[name]; seen {
			t.Errorf("%s declares %s more than once", ic10.PreludeFileName, name)
		}
		declared[name] = cDeclaration(result, name, parameterTypes(params))
	}
	if len(declared) == 0 {
		t.Fatalf("%s declares no function at all, so the pattern that finds them is what is wrong", ic10.PreludeFileName)
	}
	return declared
}

// parameterTypes strips the names off a declaration's parameter list. They are
// the header's own and correspond to nothing sema holds.
func parameterTypes(params string) []string {
	if params == "" || params == "void" {
		return nil
	}
	fields := strings.Split(params, ",")
	types := make([]string, len(fields))
	for i, field := range fields {
		types[i] = strings.TrimSpace(parameterName.ReplaceAllString(strings.TrimSpace(field), ""))
	}
	return types
}

// cSignature renders one intrinsic the way the header declares it.
func cSignature(t *testing.T, in *sema.Intrinsic) string {
	t.Helper()
	result := cResultType(in.Result.Kind())
	if result == "" {
		t.Fatalf("%s answers with %s, which no C result type spells", in.Name, in.Result.Kind())
	}
	params := make([]string, len(in.Params))
	for i, kind := range in.Params {
		params[i] = cOperandType(kind)
		if params[i] == "" {
			t.Fatalf("parameter %d of %s is a %s, which no C parameter type spells", i, in.Name, kind)
		}
	}
	return cDeclaration(result, in.Name, params)
}

// cDeclaration renders one C declaration without its parameter names. An empty
// params is the (void) form, which is how C spells a function of no arguments.
func cDeclaration(result, name string, params []string) string {
	if len(params) == 0 {
		params = []string{"void"}
	}
	return result + " " + name + "(" + strings.Join(params, ", ") + ")"
}

// cOperandType is the type the header declares a parameter of the given
// kind with, empty for a kind C cannot spell. A slot index shares "long
// long" with an ordinary value — what separates them, that it must be a
// constant expression, is not a C type.
func cOperandType(kind sema.OperandKind) string {
	switch kind {
	case sema.OperandValue, sema.OperandSlot:
		return "long long"
	case sema.OperandDouble:
		return "double"
	case sema.OperandDevice:
		return "dev"
	case sema.OperandLogicType:
		return "ic10_logic"
	case sema.OperandSlotType:
		return "ic10_slot"
	case sema.OperandBatchMode:
		return "ic10_batch"
	case sema.OperandReagentMode:
		return "ic10_reagent"
	case sema.OperandString:
		return "const char *"
	}
	return ""
}

// cResultType is the type the header declares a result of the given kind with,
// and empty for a kind no intrinsic answers with.
func cResultType(kind sema.Kind) string {
	switch kind {
	case sema.Void:
		return "void"
	case sema.Int:
		return "long long"
	case sema.Bool:
		return "bool"
	case sema.Double:
		return "double"
	case sema.Invalid, sema.Dev, sema.Pointer, sema.Array:
		// A device is named rather than computed and a pointer is an index
		// into the chip's own storage; no instruction answers with either.
	}
	return ""
}
