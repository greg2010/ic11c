package sema_test

import (
	goast "go/ast"
	goparser "go/parser"
	"go/token"
	"slices"
	"testing"

	"github.com/greg2010/ic11c/internal/sema"
)

// notDeviceSurfaces names each intrinsic that reaches a device and that the
// prefab roster has nothing to say about, with what the roster is missing. An
// intrinsic here is a decision rather than an omission, which is the whole
// difference between the two: an omission is what a device intrinsic added
// without a roster entry would otherwise be, and it would go unchecked in
// silence.
var notDeviceSurfaces = map[string]string{
	"__ic_load_reagent": "reads a reagent quantity by mode, and the roster describes logic types and slots; it holds nothing about what a device contains",
	"__ic_device_present": "asks whether a pin is connected at all, which is a fact about the world the chip is wired into rather than about the prefab; " +
		"every completed device answers it and none refuses it",
}

// TestEveryDeviceIntrinsicIsRosterChecked holds the hand-written
// deviceDirections list to the intrinsic table, which is the only place a device
// intrinsic is declared.
//
// An intrinsic reaching a device names a pin, a logic type or a slot property,
// and each of those is something the prefab roster describes. One added without
// an entry in deviceDirections is simply not checked against the roster, and
// nothing says so: the program compiles, the access is never held to what the
// device answers, and the absence looks exactly like a device the roster covers.
//
// Only membership is checked here. Where each form's operands stand is derived
// from the intrinsic's own parameter list rather than written down, so there is
// no second copy of it to disagree with this one.
func TestEveryDeviceIntrinsicIsRosterChecked(t *testing.T) {
	checked := judgedIntrinsics(t)
	for _, in := range sema.Intrinsics {
		if !reachesADevice(in) {
			continue
		}
		_, held := slices.BinarySearch(checked, in.Name)
		why, decided := notDeviceSurfaces[in.Name]
		switch {
		case held && decided:
			t.Errorf("%s is checked against the roster and also recorded as unreachable by it (%s)", in.Name, why)
		case !held && !decided:
			t.Errorf("%s reaches a device and deviceDirections does not hold it, so no access through it is checked against the roster; give it an entry or record why the roster cannot judge it", in.Name)
		}
	}

	for _, name := range checked {
		if !isDeviceIntrinsic(name) {
			t.Errorf("deviceDirections holds %s, which is no longer an intrinsic reaching a device", name)
		}
	}
	for name := range notDeviceSurfaces {
		if !isDeviceIntrinsic(name) {
			t.Errorf("%s is recorded as reaching a device the roster cannot judge, and no longer reaches one", name)
		}
	}
}

// TestEveryJudgedIntrinsicNamesAProperty holds deviceDirections to the one
// operand every check the roster runs reads.
//
// Each verdict the roster reaches is about a property: what a completed device
// answers for, and what one of its slots answers for. An intrinsic listed here
// that names no property has nothing for those checks to read, so deviceFormOf
// builds no form for it and every access through it goes unjudged in silence —
// which is exactly what the list exists to rule out. Such an intrinsic belongs
// in notDeviceSurfaces, where the roster's silence is written down with why.
func TestEveryJudgedIntrinsicNamesAProperty(t *testing.T) {
	for _, name := range judgedIntrinsics(t) {
		for _, in := range sema.Intrinsics {
			if in.Name != name {
				continue
			}
			if !namesAProperty(in) {
				t.Errorf("deviceDirections holds %s, which names no logic type and no slot property, so the roster reaches no verdict about it and no access through it is checked; record it in notDeviceSurfaces with what the roster is missing", name)
			}
		}
	}
}

// reachesADevice reports whether an intrinsic addresses something the prefab
// roster describes: a pin, a property of a device, or a property of one of its
// slots. A batch form names its devices by a prefab hash rather than by a pin,
// which is an ordinary value operand and is why the property decides this rather
// than the device operand alone.
func reachesADevice(in *sema.Intrinsic) bool {
	return namesAProperty(in) || slices.Contains(in.Params, sema.OperandDevice)
}

// namesAProperty reports whether an intrinsic names something a completed device
// answers for, which is the whole of what the roster holds a verdict about.
func namesAProperty(in *sema.Intrinsic) bool {
	return slices.ContainsFunc(in.Params, func(k sema.OperandKind) bool {
		return k == sema.OperandLogicType || k == sema.OperandSlotType
	})
}

func isDeviceIntrinsic(name string) bool {
	return slices.ContainsFunc(sema.Intrinsics, func(in *sema.Intrinsic) bool {
		return in.Name == name && reachesADevice(in)
	})
}

// judgedIntrinsics reads the intrinsics deviceDirections holds out of the
// analyser's own source, sorted. The table is unexported and this test is not,
// which is the same reason the parser's recovery guard reads its lists out of
// source: a list written down twice is a list that can disagree with itself.
func judgedIntrinsics(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := goparser.ParseFile(fset, "prefab.go", nil, 0)
	if err != nil {
		t.Fatalf("reading the analyser's source: %v", err)
	}
	var names []string
	goast.Inspect(file, func(n goast.Node) bool {
		spec, ok := n.(*goast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "deviceDirections" || len(spec.Values) != 1 {
			return true
		}
		lit, ok := spec.Values[0].(*goast.CompositeLit)
		if !ok {
			return true
		}
		for _, elt := range lit.Elts {
			pair, ok := elt.(*goast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := pair.Key.(*goast.BasicLit); ok {
				names = append(names, mustUnquote(t, key.Value))
			}
		}
		return false
	})
	if len(names) == 0 {
		t.Fatal("found no intrinsic in deviceDirections, so this test proves nothing")
	}
	slices.Sort(names)
	return names
}

func mustUnquote(t *testing.T, quoted string) string {
	t.Helper()
	if len(quoted) < 2 || quoted[0] != '"' || quoted[len(quoted)-1] != '"' {
		t.Fatalf("deviceDirections is keyed by %s, which is not a plain string", quoted)
	}
	return quoted[1 : len(quoted)-1]
}
