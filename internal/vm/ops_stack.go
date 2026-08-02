package vm

import (
	"errors"
	"math"

	"github.com/greg2010/ic11c/internal/ic10"
)

// peekOperation is _PEEK_Operation: read the slot below sp without moving it.
type peekOperation struct {
	m     *Machine
	store indexVariable
	line  int
}

func (o *peekOperation) execute(index int) (step, error) {
	dest, err := o.store.resolveIndex(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	address := int32Saturating(math.RoundToEven(o.m.registers[ic10.RegSP])) - 1
	if address < 0 {
		return step{}, newFault(ExcStackUnderFlow, o.line)
	}
	if address >= ic10.NumMemorySlots {
		return step{}, newFault(ExcStackOverFlow, o.line)
	}
	o.m.registers[dest] = o.m.memory[address]
	return advance(index), nil
}

// popOperation is _POP_Operation.
//
// It decrements sp before resolving anything and before the bounds check, so a
// pop at zero leaves sp at -1 and then faults. The side effect is not rolled
// back, which means a retried pop keeps walking sp downward one per tick.
type popOperation struct {
	m     *Machine
	store indexVariable
	line  int
}

func (o *popOperation) execute(index int) (step, error) {
	o.m.registers[ic10.RegSP]--
	dest, err := o.store.resolveIndex(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	address := int32Saturating(math.RoundToEven(o.m.registers[ic10.RegSP]))
	if address < 0 {
		return step{}, newFault(ExcStackUnderFlow, o.line)
	}
	if address >= ic10.NumMemorySlots {
		return step{}, newFault(ExcStackOverFlow, o.line)
	}
	o.m.registers[dest] = o.m.memory[address]
	return advance(index), nil
}

// pushOperation is _PUSH_Operation. sp advances only after the write succeeds,
// which is the opposite of pop.
type pushOperation struct {
	m     *Machine
	value doubleValueVariable
	line  int
}

func (o *pushOperation) execute(index int) (step, error) {
	value, err := o.value.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	address := int32Saturating(math.RoundToEven(o.m.registers[ic10.RegSP]))
	if address < 0 {
		return step{}, newFault(ExcStackUnderFlow, o.line)
	}
	if address >= ic10.NumMemorySlots {
		return step{}, newFault(ExcStackOverFlow, o.line)
	}
	o.m.memory[address] = value
	o.m.registers[ic10.RegSP]++
	return advance(index), nil
}

// pokeOperation is _POKE_Operation: write a slot by computed address, leaving
// sp alone. It reads its value operand before its address operand.
type pokeOperation struct {
	m       *Machine
	address intValuedVariable
	value   doubleValueVariable
	line    int
}

func (o *pokeOperation) execute(index int) (step, error) {
	value, err := o.value.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	address, err := o.address.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	if address < 0 {
		return step{}, newFault(ExcStackUnderFlow, o.line)
	}
	if address >= ic10.NumMemorySlots {
		return step{}, newFault(ExcStackOverFlow, o.line)
	}
	o.m.memory[address] = value
	return advance(index), nil
}

// getOperation is _GET_Operation: read another device's memory, or the chip's
// own through db.
//
// It calls ReadMemory without a catch, so the address faults arrive at the tick
// loop as host exceptions and are reported as ExcUnknown. put, which does the
// same job in the other direction, maps them to specific types. The asymmetry
// is in the game.
type getOperation struct {
	m      *Machine
	store  indexVariable
	device deviceOperand
	// byID selects the getd form, which takes a reference id operand instead of
	// a device operand.
	deviceID intValuedVariable
	byID     bool
	address  intValuedVariable
	line     int
}

func (o *getOperation) execute(index int) (step, error) {
	dest, err := o.store.resolveIndex(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	if o.byID {
		id, err := o.deviceID.resolve(targetRegister, true)
		if err != nil {
			return step{}, err
		}
		address, err := o.address.resolve(targetRegister, true)
		if err != nil {
			return step{}, err
		}
		return o.read(index, dest, o.m.housing.logicableFromID(id, BaseNetworkIndex), address)
	}
	address, err := o.address.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	device, err := o.device.device(o.m)
	if err != nil {
		return step{}, err
	}
	return o.read(index, dest, device, address)
}

func (o *getOperation) read(index, dest int, device Device, address int) (step, error) {
	if device == nil {
		return step{}, newFault(ExcDeviceNotFound, o.line)
	}
	readable, ok := device.(MemoryReadable)
	if !ok {
		return step{}, newFault(ExcMemoryNotReadable, o.line)
	}
	value, err := readable.ReadMemory(address)
	if err != nil {
		return step{}, err
	}
	o.m.registers[dest] = value
	return advance(index), nil
}

// putOperation is _PUT_Operation and _PUTD_Operation.
//
// Unlike get it wraps the write, mapping a null device to ExcDeviceNotFound, an
// address below zero to ExcStackUnderFlow, an address past the array to
// ExcStackOverFlow and anything else to ExcUnknown.
type putOperation struct {
	m        *Machine
	value    doubleValueVariable
	device   deviceOperand
	deviceID intValuedVariable
	byID     bool
	address  intValuedVariable
	line     int
}

func (o *putOperation) execute(index int) (step, error) {
	value, err := o.value.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	var device Device
	var address int
	if o.byID {
		id, idErr := o.deviceID.resolve(targetRegister, true)
		if idErr != nil {
			return step{}, idErr
		}
		address, err = o.address.resolve(targetRegister, true)
		if err != nil {
			return step{}, err
		}
		device = o.m.housing.logicableFromID(id, BaseNetworkIndex)
	} else {
		address, err = o.address.resolve(targetRegister, true)
		if err != nil {
			return step{}, err
		}
		device, err = o.device.device(o.m)
		if err != nil {
			return step{}, err
		}
	}
	if device == nil {
		return step{}, newFault(ExcDeviceNotFound, o.line)
	}
	writable, ok := device.(MemoryWritable)
	if !ok {
		return step{}, newFault(ExcMemoryNotWriteable, o.line)
	}
	if err := writable.WriteMemory(address, value); err != nil {
		return step{}, o.mapWriteError(err)
	}
	return advance(index), nil
}

// mapWriteError is put's catch clause chain, in the game's order.
func (o *putOperation) mapWriteError(err error) error {
	switch {
	case errors.Is(err, errNullDevice):
		return newFault(ExcDeviceNotFound, o.line)
	case errors.Is(err, errStackUnderflow):
		return newFault(ExcStackUnderFlow, o.line)
	case errors.Is(err, errStackOverflow):
		return newFault(ExcStackOverFlow, o.line)
	default:
		return newFault(ExcUnknown, o.line)
	}
}

// clearOperation is _CLR_Operation and _CLRD_Operation: zero a device's whole
// memory. Through db that is all 512 slots of this chip, in one instruction.
//
// The two forms disagree about which fault an unwritable device produces: clr
// reports ExcMemoryNotReadable and clrd reports ExcMemoryNotWriteable, though
// both go on to write.
type clearOperation struct {
	m        *Machine
	device   deviceIndexVariable
	deviceID intValuedVariable
	byID     bool
	line     int
}

func (o *clearOperation) execute(index int) (step, error) {
	var device Device
	if o.byID {
		id, err := o.deviceID.resolve(targetRegister, true)
		if err != nil {
			return step{}, err
		}
		device = o.m.housing.logicableFromID(id, BaseNetworkIndex)
	} else {
		pin, err := o.device.resolveIndex(targetDevice, true)
		if err != nil {
			return step{}, err
		}
		resolved, err := o.m.housing.logicableFromIndex(pin, BaseNetworkIndex)
		if err != nil {
			return step{}, err
		}
		device = resolved
	}
	if device == nil {
		return step{}, newFault(ExcDeviceNotFound, o.line)
	}
	writable, ok := device.(MemoryWritable)
	if !ok {
		if o.byID {
			return step{}, newFault(ExcMemoryNotWriteable, o.line)
		}
		return step{}, newFault(ExcMemoryNotReadable, o.line)
	}
	writable.ClearMemory()
	return advance(index), nil
}
