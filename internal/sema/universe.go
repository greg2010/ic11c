package sema

import (
	"fmt"
	"slices"
	"sync"

	"github.com/greg2010/ic11c/internal/ic10"
)

// machineConstants names the chip's own numeric constants that MicroC
// exposes; nan, pinf, and ninf are omitted, since those arrive from
// arithmetic instead. deg2rad and rad2deg are float-precision literals
// widened to double, so folding pi/180 at double precision computes a
// different number than the chip does.
var machineConstants = []string{"pi", "tau", "deg2rad", "rad2deg", "epsilon", "rgas"}

// isMachineConstant reports whether name is one of the predeclared constants,
// which is what makes it a spelling no declaration may take.
func isMachineConstant(name string) bool { return slices.Contains(machineConstants, name) }

// machineConstant is one predeclared name and the number the generated tables
// give it.
type machineConstant struct {
	name  string
	value float64
}

// resolveMachineConstants reads each name in machineConstants out of the
// generated tables. It caches the plain number rather than a [Symbol]: a
// Symbol carries one analysis's findings, so sharing one across concurrent
// analyses would race.
var resolveMachineConstants = sync.OnceValues(func() ([]machineConstant, error) {
	values := make(map[string]float64, len(ic10.Constants))
	for _, constant := range ic10.Constants {
		values[constant.Name] = constant.Value
	}
	resolved := make([]machineConstant, 0, len(machineConstants))
	for _, name := range machineConstants {
		value, ok := values[name]
		if !ok {
			return nil, fmt.Errorf("the generated machine tables carry no constant %q", name)
		}
		resolved = append(resolved, machineConstant{name: name, value: value})
	}
	return resolved, nil
})

// universe builds what the scope enclosing file scope binds: the machine's
// constants, as constexpr double objects, private to the one analysis that
// asked for them. They are ordinary names rather than keywords; a program
// may not declare one, since the spelling is reserved.
func universe() ([]*Symbol, error) {
	constants, err := resolveMachineConstants()
	if err != nil {
		return nil, err
	}
	syms := make([]*Symbol, len(constants))
	for i, constant := range constants {
		folded := doubleValue(constant.value)
		syms[i] = &Symbol{
			Name:      constant.name,
			Kind:      GlobalVar,
			Type:      constDoubleType,
			Constexpr: true,
			Value:     &folded,
		}
	}
	return syms, nil
}
