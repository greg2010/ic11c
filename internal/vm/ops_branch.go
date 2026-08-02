package vm

import (
	"math"

	"github.com/greg2010/ic11c/internal/ic10"
)

// branchForm distinguishes the three encodings every conditional shares.
//
// The absolute forms subtract the current line before adding the target, so the
// operand is a line number. The relative forms do not, so the operand is a line
// offset, which any pass that changes the line count silently corrupts. The
// link forms write the return address only when the branch is taken.
type branchForm struct {
	// relative leaves the current line in the sum, making the operand an offset.
	relative bool
	// link writes ra with the following line when the branch is taken.
	link bool
}

// offsetFor is the `-index` the absolute forms pass and the zero the relative
// forms pass.
func (f branchForm) offsetFor(index int) int {
	if f.relative {
		return 0
	}
	return -index
}

// jumpOperation is _JR_Operation and, through the absolute form, _J_Operation
// and _JAL_Operation.
//
// jal is unconditional, so it writes ra whether or not anything else happens,
// unlike the conditional link forms.
type jumpOperation struct {
	m      *Machine
	target lineNumberVariable
	form   branchForm
}

func (o *jumpOperation) execute(index int) (step, error) {
	if o.form.link {
		o.m.registers[ic10.RegRA] = float64(index + 1)
	}
	target, err := o.target.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	return step{next: index + o.form.offsetFor(index) + target}, nil
}

// compareBranch is _BRLT_Operation and its siblings: two double arguments, a
// predicate, and a jump target resolved only when the branch is taken.
//
// The target resolving lazily matters. A branch that is not taken never touches
// its target operand, so a malformed target only faults on the tick the branch
// succeeds.
type compareBranch struct {
	m      *Machine
	args   [3]doubleValueVariable
	argc   int
	target lineNumberVariable
	form   branchForm
	taken  func(a, b, c float64) bool
}

func (o *compareBranch) execute(index int) (step, error) {
	var values [3]float64
	for i := range o.argc {
		value, err := o.args[i].resolve(targetRegister, true)
		if err != nil {
			return step{}, err
		}
		values[i] = value
	}
	if !o.taken(values[0], values[1], values[2]) {
		return advance(index), nil
	}
	target, err := o.target.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	if o.form.link {
		o.m.registers[ic10.RegRA] = float64(index + 1)
	}
	return step{next: index + o.form.offsetFor(index) + target}, nil
}

// deviceBranch is _BRDSE_Operation and _BRDNS_Operation: branch on whether a
// device operand resolves to anything.
type deviceBranch struct {
	m      *Machine
	device deviceOperand
	target lineNumberVariable
	form   branchForm
	// onSet branches when the device is present, rather than when it is absent.
	onSet bool
}

func (o *deviceBranch) execute(index int) (step, error) {
	device, err := o.device.device(o.m)
	if err != nil {
		return step{}, err
	}
	if (device != nil) != o.onSet {
		return advance(index), nil
	}
	target, err := o.target.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	if o.form.link {
		o.m.registers[ic10.RegRA] = float64(index + 1)
	}
	return step{next: index + o.form.offsetFor(index) + target}, nil
}

// logicBranch is _BDNVS_Operation and _BDNVL_Operation: branch when a device
// cannot accept a logic write, or cannot answer a logic read.
//
// Both are absolute and neither has a relative or a link form, so the operand
// is the target line. Both check the logic type before resolving the device.
type logicBranch struct {
	m         *Machine
	device    deviceOperand
	logicType enumValuedVariable
	target    lineNumberVariable
	line      int
	// onWrite tests CanLogicWrite rather than CanLogicRead.
	onWrite bool
}

func (o *logicBranch) execute(index int) (step, error) {
	logicType, err := o.logicType.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	if toLogicType(logicType) == logicTypeNone {
		return step{}, newFault(ExcLogicTypeIsNone, o.line)
	}
	device, err := o.device.device(o.m)
	if err != nil {
		return step{}, err
	}
	if device == nil {
		return step{}, newFault(ExcDeviceNotFound, o.line)
	}
	allowed := device.CanLogicRead(toLogicType(logicType))
	if o.onWrite {
		allowed = device.CanLogicWrite(toLogicType(logicType))
	}
	if allowed {
		return advance(index), nil
	}
	target, err := o.target.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	return step{next: target}, nil
}

// isNaN is the predicate behind bnan and brnan, which have one argument rather
// than two.
func isNaN(a, _, _ float64) bool { return math.IsNaN(a) }
