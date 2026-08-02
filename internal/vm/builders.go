package vm

import "math"

// buildFunc turns a validated token line into an operation.
type buildFunc func(m *Machine, line int, args []string) (operation, error)

// argSpec names where an operand's text comes from: a token position, or a
// literal the game substitutes. The zero-suffixed forms are all
// substitutions - bltz is blt against the literal "0" - so the literal has to
// travel through the same operand construction as a real token.
type argSpec struct {
	index   int
	literal string
}

// at names token position i, counting the mnemonic as zero.
func at(i int) argSpec { return argSpec{index: i} }

// zero is the "0" the game passes for the implicit operand of every
// zero-suffixed comparison and branch.
var zero = argSpec{index: -1, literal: "0"}

func operandText(args []string, spec argSpec) (string, error) {
	if spec.index < 0 {
		return spec.literal, nil
	}
	return token(args, spec.index)
}

// storeValue builds a destination register plus up to three double arguments,
// which is _Operation_1_1 through _Operation_1_3.
func storeValue(apply func(a, b, c float64) float64, specs ...argSpec) buildFunc {
	return func(m *Machine, line int, args []string) (operation, error) {
		store, err := storeOperand(m, line, args[1])
		if err != nil {
			return nil, err
		}
		op := &storeValueOp{m: m, store: store, argc: len(specs), apply: apply}
		for i, spec := range specs {
			code, err := operandText(args, spec)
			if err != nil {
				return nil, err
			}
			if op.args[i], err = valueOperand(m, line, code); err != nil {
				return nil, err
			}
		}
		return op, nil
	}
}

// storeTest builds a comparison that stores one or zero.
func storeTest(taken func(a, b, c float64) bool, specs ...argSpec) buildFunc {
	return storeValue(func(a, b, c float64) float64 { return boolValue(taken(a, b, c)) }, specs...)
}

// storeLong builds the bitwise instructions, whose arguments and result travel
// through the double to int64 round trip.
func storeLong(apply func(a, b int64) int64, specs ...argSpec) buildFunc {
	return func(m *Machine, line int, args []string) (operation, error) {
		store, err := storeOperand(m, line, args[1])
		if err != nil {
			return nil, err
		}
		op := &storeLongOp{m: m, store: store, argc: len(specs), signed: true, apply: apply}
		for i, spec := range specs {
			code, err := operandText(args, spec)
			if err != nil {
				return nil, err
			}
			if op.args[i], err = valueOperand(m, line, code); err != nil {
				return nil, err
			}
		}
		return op, nil
	}
}

// shift builds the shift and rotate instructions. signed picks which
// conversion the value argument takes, which is the only thing separating srl
// from sra.
func shift(signed bool, apply func(value int64, distance int) int64) buildFunc {
	return func(m *Machine, line int, args []string) (operation, error) {
		store, err := storeOperand(m, line, args[1])
		if err != nil {
			return nil, err
		}
		value, err := valueOperand(m, line, args[2])
		if err != nil {
			return nil, err
		}
		distance, err := valueOperand(m, line, args[3])
		if err != nil {
			return nil, err
		}
		return &shiftOp{m: m, store: store, value: value, distance: distance, signed: signed, apply: apply}, nil
	}
}

// compare builds a conditional branch: the arguments, then the target, which is
// only resolved when the branch is taken.
func compare(form branchForm, taken func(a, b, c float64) bool, target argSpec, specs ...argSpec) buildFunc {
	return func(m *Machine, line int, args []string) (operation, error) {
		op := &compareBranch{m: m, argc: len(specs), form: form, taken: taken}
		for i, spec := range specs {
			code, err := operandText(args, spec)
			if err != nil {
				return nil, err
			}
			if op.args[i], err = valueOperand(m, line, code); err != nil {
				return nil, err
			}
		}
		targetCode, err := operandText(args, target)
		if err != nil {
			return nil, err
		}
		if op.target, err = jumpOperand(m, line, targetCode); err != nil {
			return nil, err
		}
		return op, nil
	}
}

// jump builds an unconditional jump. Its link form writes ra whether or not
// anything is conditional, unlike the conditional link forms.
func jump(form branchForm) buildFunc {
	return func(m *Machine, line int, args []string) (operation, error) {
		target, err := jumpOperand(m, line, args[1])
		if err != nil {
			return nil, err
		}
		return &jumpOperation{m: m, target: target, form: form}, nil
	}
}

// deviceBranchBuilder builds bdse, bdns and their relative and link forms.
func deviceBranchBuilder(form branchForm, onSet bool) buildFunc {
	return func(m *Machine, line int, args []string) (operation, error) {
		device, err := newDeviceOperand(m, line, args[1])
		if err != nil {
			return nil, err
		}
		target, err := jumpOperand(m, line, args[2])
		if err != nil {
			return nil, err
		}
		return &deviceBranch{m: m, device: device, target: target, form: form, onSet: onSet}, nil
	}
}

// logicBranchBuilder builds bdnvl and bdnvs.
func logicBranchBuilder(onWrite bool) buildFunc {
	return func(m *Machine, line int, args []string) (operation, error) {
		device, err := newDeviceOperand(m, line, args[1])
		if err != nil {
			return nil, err
		}
		logicType, err := logicTypeOperand(m, line, args[2])
		if err != nil {
			return nil, err
		}
		target, err := jumpOperand(m, line, args[3])
		if err != nil {
			return nil, err
		}
		return &logicBranch{m: m, device: device, logicType: logicType, target: target, line: line, onWrite: onWrite}, nil
	}
}

// deviceSet builds sdse and sdns.
func deviceSet(onSet bool) buildFunc {
	return func(m *Machine, line int, args []string) (operation, error) {
		store, err := storeOperand(m, line, args[1])
		if err != nil {
			return nil, err
		}
		device, err := newDeviceOperand(m, line, args[2])
		if err != nil {
			return nil, err
		}
		return &deviceSetOperation{m: m, store: store, device: device, onSet: onSet}, nil
	}
}

// Predicates shared by the comparison and branch forms. Each takes three
// arguments so that one signature covers the two argument and three argument
// shapes; unused arguments are ignored.

func lessThan(a, b, _ float64) bool       { return a < b }
func greaterThan(a, b, _ float64) bool    { return a > b }
func lessOrEqual(a, b, _ float64) bool    { return a <= b }
func greaterOrEqual(a, b, _ float64) bool { return a >= b }
func equal(a, b, _ float64) bool          { return a == b }
func notEqual(a, b, _ float64) bool       { return a != b }
func approximate(a, b, c float64) bool    { return approximatelyEqual(a, b, c) }
func notApproximate(a, b, c float64) bool { return !approximatelyEqual(a, b, c) }

// Unary and binary bodies, lifted so that the instruction table reads as one
// line per mnemonic.

func unary(f func(a float64) float64) func(a, b, c float64) float64 {
	return func(a, _, _ float64) float64 { return f(a) }
}

func binary(f func(a, b float64) float64) func(a, b, c float64) float64 {
	return func(a, b, _ float64) float64 { return f(a, b) }
}

func isNaNValue(a float64) float64    { return boolValue(math.IsNaN(a)) }
func isNotNaNValue(a float64) float64 { return boolValue(!math.IsNaN(a)) }
