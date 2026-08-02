package vm

import (
	"fmt"

	"github.com/greg2010/ic11c/internal/ic10"
)

// loadLogicOperation is _L_Operation and _LD_Operation: read one logic property
// from one device.
type loadLogicOperation struct {
	m         *Machine
	store     indexVariable
	device    deviceOperand
	deviceID  intValuedVariable
	byID      bool
	logicType enumValuedVariable
	line      int
}

func (o *loadLogicOperation) execute(index int) (step, error) {
	dest, err := o.store.resolveIndex(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	var device Device
	if o.byID {
		id, idErr := o.deviceID.resolve(targetRegister, true)
		if idErr != nil {
			return step{}, idErr
		}
		device = o.m.housing.logicableFromID(id, BaseNetworkIndex)
		// ld does not null check before dereferencing, so a missing device is
		// a host exception the chip reports as ExcUnknown rather than
		// ExcDeviceNotFound.
		if device == nil {
			return step{}, hostErrorf("ld: no device with that reference id")
		}
	} else {
		device, err = o.device.device(o.m)
		if err != nil {
			return step{}, err
		}
		if device == nil {
			return step{}, newFault(ExcDeviceNotFound, o.line)
		}
	}
	logicType, err := o.logicType.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	if toLogicType(logicType) == logicTypeNone {
		return step{}, newFault(ExcLogicTypeIsNone, o.line)
	}
	if !device.CanLogicRead(toLogicType(logicType)) {
		return step{}, newFault(ExcIncorrectLogicType, o.line)
	}
	o.m.registers[dest] = device.LogicValue(toLogicType(logicType))
	return advance(index), nil
}

// storeLogicOperation is _S_Operation and _SD_Operation: write one logic
// property on one device.
//
// The current value is read before the write is checked, and the write is
// skipped when the value already matches, which is what makes a store to a
// read-only property whose value happens to match still fault.
type storeLogicOperation struct {
	m         *Machine
	device    deviceOperand
	deviceID  intValuedVariable
	byID      bool
	logicType enumValuedVariable
	value     doubleValueVariable
	line      int
	// checkNone selects the s form, which rejects LogicType.None before
	// resolving the device. sd does not check it at all.
	checkNone bool
}

func (o *storeLogicOperation) execute(index int) (step, error) {
	if o.byID {
		id, err := o.deviceID.resolve(targetRegister, true)
		if err != nil {
			return step{}, err
		}
		value, err := o.value.resolve(targetRegister, true)
		if err != nil {
			return step{}, err
		}
		logicType, err := o.logicType.resolve(targetRegister, true)
		if err != nil {
			return step{}, err
		}
		device := o.m.housing.logicableFromID(id, BaseNetworkIndex)
		if device == nil {
			return step{}, newFault(ExcDeviceNotFound, o.line)
		}
		return o.write(index, device, toLogicType(logicType), value)
	}
	value, err := o.value.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	logicType, err := o.logicType.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	if o.checkNone && toLogicType(logicType) == logicTypeNone {
		return step{}, newFault(ExcLogicTypeIsNone, o.line)
	}
	device, err := o.device.device(o.m)
	if err != nil {
		return step{}, err
	}
	if device == nil {
		return step{}, newFault(ExcDeviceNotFound, o.line)
	}
	return o.write(index, device, toLogicType(logicType), value)
}

func (o *storeLogicOperation) write(index int, device Device, logicType ic10.LogicType, value float64) (step, error) {
	current := device.LogicValue(logicType)
	if !device.CanLogicWrite(logicType) {
		return step{}, newFault(ExcIncorrectLogicType, o.line)
	}
	if current != value {
		device.SetLogicValue(logicType, value)
	}
	return advance(index), nil
}

// loadSlotOperation is _LS_Operation: read one slot property from one device.
type loadSlotOperation struct {
	m        *Machine
	store    indexVariable
	device   deviceOperand
	slot     intValuedVariable
	slotType enumValuedVariable
	line     int
}

func (o *loadSlotOperation) execute(index int) (step, error) {
	dest, err := o.store.resolveIndex(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	slot, err := o.slot.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	device, err := o.device.device(o.m)
	if err != nil {
		return step{}, err
	}
	if device == nil {
		return step{}, newFault(ExcDeviceNotFound, o.line)
	}
	slotType, err := o.slotType.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	if !device.CanSlotRead(toLogicSlotType(slotType), slot) {
		return step{}, newFault(ExcIncorrectLogicType, o.line)
	}
	o.m.registers[dest] = device.SlotValue(toLogicSlotType(slotType), slot)
	return advance(index), nil
}

// storeSlotOperation is _SS_Operation: write one slot property on one device.
type storeSlotOperation struct {
	m        *Machine
	device   deviceOperand
	slot     intValuedVariable
	slotType enumValuedVariable
	value    doubleValueVariable
	line     int
}

func (o *storeSlotOperation) execute(index int) (step, error) {
	value, err := o.value.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	slotType, err := o.slotType.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	slot, err := o.slot.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	device, err := o.device.device(o.m)
	if err != nil {
		return step{}, err
	}
	if device == nil {
		return step{}, newFault(ExcDeviceNotFound, o.line)
	}
	writer, ok := device.(SlotWriter)
	if !ok {
		return step{}, newFault(ExcDeviceNotSlotWriteable, o.line)
	}
	if !writer.CanSlotWrite(toLogicSlotType(slotType), slot) {
		return step{}, newFault(ExcIncorrectLogicSlotType, o.line)
	}
	if writer.SlotValue(toLogicSlotType(slotType), slot) != value {
		writer.SetSlotValue(toLogicSlotType(slotType), slot, value)
	}
	return advance(index), nil
}

// deviceSetOperation is _SDSE_Operation and _SDNS_Operation: store one when a
// device operand resolves to something, or when it does not.
type deviceSetOperation struct {
	m      *Machine
	store  indexVariable
	device deviceOperand
	onSet  bool
}

func (o *deviceSetOperation) execute(index int) (step, error) {
	dest, err := o.store.resolveIndex(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	device, err := o.device.device(o.m)
	if err != nil {
		return step{}, err
	}
	o.m.registers[dest] = boolValue((device != nil) == o.onSet)
	return advance(index), nil
}

// batchLoadOperation is _LB_Operation, _LBN_Operation, _LBS_Operation and
// _LBNS_Operation: aggregate one property across every device with a matching
// prefab hash, optionally narrowed by name hash.
//
// The readability sweep runs before the aggregation and over the whole list, so
// one unreadable match faults the instruction even though the aggregation would
// have skipped it.
type batchLoadOperation struct {
	m          *Machine
	store      indexVariable
	prefabHash intValuedVariable
	nameHash   intValuedVariable
	byName     bool
	slot       intValuedVariable
	slotType   enumValuedVariable
	logicType  enumValuedVariable
	bySlot     bool
	batchMode  enumValuedVariable
	line       int
}

func (o *batchLoadOperation) execute(index int) (step, error) {
	dest, err := o.store.resolveIndex(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	prefab, err := o.prefabHash.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	name := 0
	if o.byName {
		if name, err = o.nameHash.resolve(targetRegister, true); err != nil {
			return step{}, err
		}
	}
	slot := 0
	if o.bySlot {
		if slot, err = o.slot.resolve(targetRegister, true); err != nil {
			return step{}, err
		}
	}
	var logicType, slotType int
	if o.bySlot {
		if slotType, err = o.slotType.resolve(targetRegister, true); err != nil {
			return step{}, err
		}
	} else {
		if logicType, err = o.logicType.resolve(targetRegister, true); err != nil {
			return step{}, err
		}
		// Only the unnamed, unslotted form rejects LogicType.None. lbn does not.
		if !o.byName && toLogicType(logicType) == logicTypeNone {
			return step{}, newFault(ExcLogicTypeIsNone, o.line)
		}
	}
	mode, err := o.batchMode.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	devices := o.m.housing.batchOutput()
	if devices == nil {
		return step{}, newFault(ExcDeviceListNull, o.line)
	}
	matches := func(d Device) bool {
		return d.PrefabHash() == prefab && (!o.byName || d.NameHash() == name)
	}
	for i := len(devices) - 1; i >= 0; i-- {
		d := devices[i]
		if d == nil || !matches(d) {
			continue
		}
		if o.bySlot {
			if !d.CanSlotRead(toLogicSlotType(slotType), slot) {
				return step{}, newFault(ExcIncorrectLogicSlotType, o.line)
			}
			continue
		}
		if !d.CanLogicRead(toLogicType(logicType)) {
			return step{}, newFault(ExcIncorrectLogicType, o.line)
		}
	}
	read := func(d Device) float64 { return d.LogicValue(toLogicType(logicType)) }
	if o.bySlot {
		read = func(d Device) float64 { return d.SlotValue(toLogicSlotType(slotType), slot) }
	}
	o.m.registers[dest] = batchRead(mode, devices, matches, read)
	return advance(index), nil
}

// batchStoreOperation is _SB_Operation, _SBN_Operation and _SBS_Operation:
// write one property on every device with a matching prefab hash.
//
// There is no name filtered slot store; the instruction set has lbns for loads
// with no sbns counterpart.
type batchStoreOperation struct {
	m          *Machine
	prefabHash intValuedVariable
	nameHash   intValuedVariable
	byName     bool
	slot       intValuedVariable
	slotType   enumValuedVariable
	logicType  enumValuedVariable
	bySlot     bool
	value      doubleValueVariable
	line       int
}

func (o *batchStoreOperation) execute(index int) (step, error) {
	prefab, err := o.prefabHash.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	name := 0
	if o.byName {
		if name, err = o.nameHash.resolve(targetRegister, true); err != nil {
			return step{}, err
		}
	}
	value, err := o.value.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	var logicType, slotType, slot int
	if o.bySlot {
		if slotType, err = o.slotType.resolve(targetRegister, true); err != nil {
			return step{}, err
		}
		if slot, err = o.slot.resolve(targetRegister, true); err != nil {
			return step{}, err
		}
	} else {
		if logicType, err = o.logicType.resolve(targetRegister, true); err != nil {
			return step{}, err
		}
		if toLogicType(logicType) == logicTypeNone {
			return step{}, newFault(ExcLogicTypeIsNone, o.line)
		}
	}
	devices := o.m.housing.batchOutput()
	if devices == nil {
		return step{}, newFault(ExcDeviceListNull, o.line)
	}
	for i := len(devices) - 1; i >= 0; i-- {
		d := devices[i]
		if d == nil || d.PrefabHash() != prefab {
			continue
		}
		if o.byName && d.NameHash() != name {
			continue
		}
		if o.bySlot {
			writer, ok := d.(SlotWriter)
			if !ok {
				return step{}, newFault(ExcDeviceNotSlotWriteable, o.line)
			}
			if !writer.CanSlotWrite(toLogicSlotType(slotType), slot) {
				return step{}, newFault(ExcIncorrectLogicSlotType, o.line)
			}
			if writer.SlotValue(toLogicSlotType(slotType), slot) != value {
				writer.SetSlotValue(toLogicSlotType(slotType), slot, value)
			}
			continue
		}
		if !d.CanLogicWrite(toLogicType(logicType)) {
			return step{}, newFault(ExcIncorrectLogicType, o.line)
		}
		if d.LogicValue(toLogicType(logicType)) != value {
			d.SetLogicValue(toLogicType(logicType), value)
		}
	}
	return advance(index), nil
}

// reagentLoadOperation is _LR_Operation. Reagent mixtures are chemistry this
// package does not model, so every mode returns an explicit error rather than a
// plausible zero.
type reagentLoadOperation struct {
	m        *Machine
	store    indexVariable
	device   deviceOperand
	mode     enumValuedVariable
	reagent  intValuedVariable
	line     int
	mnemonic string
}

func (o *reagentLoadOperation) execute(_ int) (step, error) {
	if _, err := o.store.resolveIndex(targetRegister, true); err != nil {
		return step{}, err
	}
	mode, err := o.mode.resolve(targetRegister, true)
	if err != nil {
		return step{}, err
	}
	device, err := o.device.device(o.m)
	if err != nil {
		return step{}, err
	}
	if device == nil {
		return step{}, newFault(ExcDeviceNotFound, o.line)
	}
	switch mode {
	case reagentContents, reagentRequired, reagentRecipe:
		// The three per-reagent modes resolve the reagent id before reading,
		// so a malformed one faults here rather than reaching the mixture.
		if _, err := o.reagent.resolve(targetRegister, true); err != nil {
			return step{}, err
		}
		return step{}, fmt.Errorf("%s reagent mode %d: reagent mixtures are not modelled: %w", o.mnemonic, mode, ErrUnimplemented)
	case reagentTotalContents:
		return step{}, fmt.Errorf("%s: reagent mixtures are not modelled: %w", o.mnemonic, ErrUnimplemented)
	default:
		return step{}, newFault(ExcUnhandledReagentMode, o.line)
	}
}

// reagentMapOperation is _RMAP_Operation, which maps a reagent hash to the
// prefab hash that produces it. Recipes are chemistry this package does not
// model.
type reagentMapOperation struct {
	m      *Machine
	store  indexVariable
	device deviceIndexVariable
	value  doubleValueVariable
	line   int
}

func (o *reagentMapOperation) execute(_ int) (step, error) {
	if _, err := o.store.resolveIndex(targetRegister, true); err != nil {
		return step{}, err
	}
	pin, err := o.device.resolveIndex(targetDevice, true)
	if err != nil {
		return step{}, err
	}
	if _, err := o.value.resolve(targetRegister, true); err != nil {
		return step{}, err
	}
	device, err := o.m.housing.logicableFromIndex(pin, BaseNetworkIndex)
	if err != nil {
		return step{}, err
	}
	if device == nil {
		return step{}, newFault(ExcDeviceNotFound, o.line)
	}
	return step{}, fmt.Errorf("rmap: reagent recipes are not modelled: %w", ErrUnimplemented)
}
