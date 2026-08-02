package sema

import (
	"fmt"
	"slices"
	"sync"

	"github.com/greg2010/ic11c/internal/ic10"
)

// machineConstants names the chip's own numeric constants that MicroC exposes.
//
// Nine exist on the machine. nan has no literal spelling the operand parser
// reads, and pinf and ninf are left out with it rather than exposing a third of
// a family: an infinity arrives from arithmetic, which is where the program can
// still test for it. The six here are the ones a program has to be told,
// because it cannot write them: deg2rad and rad2deg in particular are float
// precision literals widened to double, so a program folding pi/180 at full
// double precision computes a different number from the chip.
var machineConstants = []string{"pi", "tau", "deg2rad", "rad2deg", "epsilon", "rgas"}

// isMachineConstant reports whether name is one of the predeclared constants,
// which is what makes it a spelling no declaration may take.
func isMachineConstant(name string) bool { return slices.Contains(machineConstants, name) }

// universe is the scope enclosing file scope, holding the machine's constants
// as constexpr double objects.
//
// They are ordinary names rather than keywords, and a program may not declare
// one: the spelling is reserved, so nothing shadows the machine's value.
//
// A constant the generated tables do not carry fails analysis rather than being
// skipped. The tables come from the game's own assembly, so a missing name is a
// table that moved; leaving it undeclared would report every program that used
// it and never the table.
var universe = sync.OnceValues(func() (*scope, error) {
	s := newScope(nil)
	values := make(map[string]float64, len(ic10.Constants))
	for _, constant := range ic10.Constants {
		values[constant.Name] = constant.Value
	}
	for _, name := range machineConstants {
		value, ok := values[name]
		if !ok {
			return nil, fmt.Errorf("the generated machine tables carry no constant %q", name)
		}
		folded := doubleValue(value)
		s.insert(&Symbol{
			Name:      name,
			Kind:      GlobalVar,
			Type:      constDoubleType,
			Constexpr: true,
			Value:     &folded,
		})
	}
	return s, nil
})
