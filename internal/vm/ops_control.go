package vm

import "math"

// aliasOperation is _ALIAS_Operation and, with its operands swapped,
// _LABEL_Operation.
//
// An alias takes effect when the line runs, not when it compiles, so a
// reference on an earlier line sees nothing on the first pass. A define is the
// opposite: it is registered at compile time. That asymmetry is why `alias`
// costs an execution slot and `define` does not change anything at run time.
type aliasOperation struct {
	m      *Machine
	name   string
	target aliasTarget
	// index is nil when the target text named neither a register nor a device.
	// The game leaves the field null and dereferences it at run time, so this
	// reproduces that as a host exception rather than a compile error.
	index indexResolver
	line  int
}

// indexResolver is the GetVariableIndex surface an alias target needs, which
// the register and device operand forms implement differently.
type indexResolver interface {
	resolveIndex(target aliasTarget, throwError bool) (int, error)
}

func newAliasOperation(m *Machine, line int, name, target string) (*aliasOperation, error) {
	op := &aliasOperation{m: m, name: name, line: line}
	if target == "" {
		return nil, hostErrorf("alias target is empty")
	}
	switch target[0] {
	case 'r':
		op.target = targetRegister
		index, err := newIndexVariable(m, line, target, maskStoreIndex|includeJumpTag)
		if err != nil {
			return nil, err
		}
		op.index = &index
	case 'd':
		op.target = targetDevice
		device, err := newDeviceIndexVariable(m, line, target, includeAlias|includeJumpTag|includeDeviceIndex)
		if err != nil {
			return nil, err
		}
		op.index = device
	}
	return op, nil
}

func (o *aliasOperation) execute(index int) (step, error) {
	if o.index == nil {
		return step{}, hostErrorf("alias target names neither a register nor a device")
	}
	resolved, err := o.index.resolveIndex(o.target, true)
	if err != nil {
		return step{}, err
	}
	if resolved < 0 ||
		(o.target == targetRegister && resolved >= len(o.m.registers)) ||
		(o.target == targetDevice && !o.m.housing.isValidIndex(resolved)) {
		return step{}, newFault(ExcIndexOutOfRange, o.line)
	}
	o.m.aliases[o.name] = aliasValue{target: o.target, index: resolved}
	return advance(index), nil
}

// defineOperation is _DEFINE_Operation. All of its work happens while
// compiling; at run time it is a no-op that still costs an instruction.
type defineOperation struct{}

func (defineOperation) execute(index int) (step, error) { return advance(index), nil }

// yieldOperation is _YIELD_Operation. The game returns -index-1, whose sign
// ends the tick and whose negation lands the program counter on the following
// line; [step] carries the two apart.
type yieldOperation struct{}

func (yieldOperation) execute(index int) (step, error) {
	return step{next: index + 1, endTick: true}, nil
}

// hcfOperation is _HCF_Operation. It is not a trap or an abort: the game
// destroys the chip and starts a fire, then raises ExcChipCatchingFire. The
// destruction is recorded so a test can tell it apart from any other route to
// that fault.
type hcfOperation struct {
	m    *Machine
	line int
}

func (o *hcfOperation) execute(_ int) (step, error) {
	o.m.destroyed = true
	return step{}, newFault(ExcChipCatchingFire, o.line)
}

// sleepOperation is _SLEEP_Operation.
//
// The game returns -index to end the tick and re-enter itself next tick, where
// yield returns -index-1 to move on. On line zero that is -0, which is not
// negative, so the game's tick loop never breaks and sleep consumes the whole
// instruction budget instead of yielding. [step] carries the next line and the
// end of tick apart, so that quirk is stated rather than emergent.
//
// The remaining duration lives on the operation, so it survives across ticks
// and is reset only by reloading the program.
type sleepOperation struct {
	m         *Machine
	duration  doubleValueVariable
	remaining float64
	lastSet   float32
	// started distinguishes a fresh sleep from one already counting down. The
	// game uses NaN in the remaining field for this, which cannot be told apart
	// from a sleep whose duration operand was NaN.
	started bool
}

func (o *sleepOperation) execute(index int) (step, error) {
	if !o.started {
		o.lastSet = o.m.clock()
		duration, err := o.duration.resolve(targetRegister, true)
		if err != nil {
			return step{}, err
		}
		o.remaining = duration
		o.started = !math.IsNaN(duration)
		return o.reenter(index), nil
	}
	now := o.m.clock()
	o.remaining -= float64(now - o.lastSet)
	if o.remaining < 0 {
		o.lastSet = 0
		o.remaining = math.NaN()
		o.started = false
		return advance(index), nil
	}
	o.lastSet = now
	return o.reenter(index), nil
}

// reenter is the step sleep hands back while it is still counting down. The
// tick ends on every line but zero, where the game's -0 is not negative.
func (o *sleepOperation) reenter(index int) step {
	return step{next: index, endTick: index != 0}
}
