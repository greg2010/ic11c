package vm

import (
	"math"
	"slices"

	"github.com/greg2010/ic11c/internal/ic10"
)

// Housing pin and network sentinels, from the ProgrammableChip constants of the
// same meaning. They are ordinary indices the chip compares against, not flags,
// so they leak into arithmetic: an out of range device index is whatever the
// operand resolved to, not a normalised error value.
const (
	// BaseUnitIndex is the device index `db` resolves to. The housing answers
	// it with the chip itself, which is why a memory access through db reaches
	// the same 512 doubles as push and poke.
	BaseUnitIndex = math.MaxInt32
	// BaseNetworkIndex means no `:n` network suffix was given.
	BaseNetworkIndex = math.MinInt32
)

// Device is a logic-addressable thing the chip can reach, the subset of the
// game's ILogicable that IC10 instructions actually use.
//
// Reads and writes are gated by CanLogicRead and CanLogicWrite: the chip asks
// first and faults with ExcIncorrectLogicType when the answer is no, so a
// device that returns true for everything is not a faithful stand-in for a real
// one. Slot reads are part of this interface because the game puts them on
// ILogicable; slot writes are not, because the game puts those on a separate
// interface (see SlotWriter).
type Device interface {
	// ReferenceID is the id `ld`, `sd`, `getd`, `putd`, `clrd` and the register
	// held device forms look devices up by. Zero means no device.
	ReferenceID() int
	// PrefabHash selects a batch. Batch instructions compare it for equality.
	PrefabHash() int
	// NameHash narrows a batch. Zero when the device is unnamed.
	NameHash() int

	CanLogicRead(t ic10.LogicType) bool
	CanLogicWrite(t ic10.LogicType) bool
	// LogicValue is read without consulting CanLogicRead in some paths, most
	// notably the store instructions, which read the current value before
	// checking that writing is allowed.
	LogicValue(t ic10.LogicType) float64
	SetLogicValue(t ic10.LogicType, value float64)

	CanSlotRead(t ic10.LogicSlotType, slot int) bool
	SlotValue(t ic10.LogicSlotType, slot int) float64
}

// SlotWriter is the game's ISlotWriteable. A device that does not implement it
// makes `ss` and `sbs` fault with ExcDeviceNotSlotWriteable.
type SlotWriter interface {
	Device
	CanSlotWrite(t ic10.LogicSlotType, slot int) bool
	SetSlotValue(t ic10.LogicSlotType, slot int, value float64)
}

// MemoryReadable is the game's IMemoryReadable, what `get` and `getd` require.
// ReadMemory reports ExcStackUnderFlow below zero and ExcStackOverFlow at or
// above the array length; `get` does not wrap those, so they surface unchanged,
// while `put` maps them through its own catch clauses.
type MemoryReadable interface {
	ReadMemory(address int) (float64, error)
}

// MemoryWritable is the game's IMemoryWritable, what `put`, `putd`, `clr` and
// `clrd` require.
//
// WriteMemory's error is mapped by put and putd rather than passed through: an
// address fault becomes ExcStackUnderFlow or ExcStackOverFlow, a nil dependency
// becomes ExcDeviceNotFound, and anything else becomes ExcUnknown. Return the
// same errors ReadMemory documents so the mapping lands where the game's does.
type MemoryWritable interface {
	WriteMemory(address int, value float64) error
	ClearMemory()
}

// NetworkDevice is a device that answers a `d0:1` style network reference. A
// device that does not implement it resolves any network suffix to no device,
// which is what an unconnected pin does in game.
type NetworkDevice interface {
	Network(index int) Device
}

// Housing is the circuit housing the chip sits in: six device pins, the data
// network those pins are reached through, and the batch list the batch
// instructions aggregate over.
//
// The zero value is a housing with no pins and no network, which makes every
// device reference fault the way an unpowered housing does. Use NewHousing.
type Housing struct {
	pins    [ic10.NumDevicePins]Device
	network []Device
	byID    map[int]Device
	// noNetwork reproduces InputNetwork1 being null: the housing then skips its
	// membership check and hands back whatever is on the pin.
	noNetwork bool
	chip      *Machine
}

// NewHousing builds a housing with the given devices on pins d0 upward. Every
// non-nil device joins the data network and becomes reachable by reference id,
// which is the arrangement a working in-game housing has.
//
// Passing more devices than there are pins is an error the caller cannot
// recover from at run time, so the extra devices are dropped rather than
// silently renumbering the pins; check len before calling if that matters.
func NewHousing(devices ...Device) *Housing {
	h := &Housing{byID: make(map[int]Device, len(devices))}
	for i, d := range devices {
		if i >= ic10.NumDevicePins {
			break
		}
		h.pins[i] = d
		if d == nil {
			continue
		}
		h.network = append(h.network, d)
		h.byID[d.ReferenceID()] = d
	}
	return h
}

// Attach places a device on a pin, adding it to the data network and the
// reference id index and removing whatever was on that pin. It reports false
// for a pin outside d0 through d5.
func (h *Housing) Attach(pin int, d Device) bool {
	if pin < 0 || pin >= ic10.NumDevicePins {
		return false
	}
	if h.byID == nil {
		h.byID = make(map[int]Device)
	}
	if previous := h.pins[pin]; previous != nil {
		h.network = slices.DeleteFunc(h.network, func(other Device) bool { return other == previous })
		delete(h.byID, previous.ReferenceID())
	}
	h.pins[pin] = d
	if d == nil {
		return true
	}
	h.network = append(h.network, d)
	h.byID[d.ReferenceID()] = d
	return true
}

// SetBatchList replaces the data network, which is both the membership set a
// pin reference is checked against and the list the batch instructions
// aggregate over. Passing nil detaches the network entirely, which makes every
// batch instruction fault with ExcDeviceListNull.
func (h *Housing) SetBatchList(devices []Device) {
	h.network = devices
	h.noNetwork = devices == nil
}

// isValidIndex is CircuitHousing.IsValidIndex. BaseUnitIndex is always valid;
// everything else must be a real pin.
func (h *Housing) isValidIndex(index int) bool {
	if index == BaseUnitIndex {
		return true
	}
	return index >= 0 && index < ic10.NumDevicePins
}

// batchOutput is CircuitHousing.GetBatchOutput. A nil result is the null the
// batch instructions turn into ExcDeviceListNull, and is distinct from an empty
// list, which is a network with nothing matching on it.
func (h *Housing) batchOutput() []Device {
	if h.noNetwork {
		return nil
	}
	if h.network == nil {
		return []Device{}
	}
	return h.network
}

// onNetwork is the InputNetwork1.DataDeviceList.Contains check the housing
// applies before handing back a device.
func (h *Housing) onNetwork(d Device) bool {
	if h.noNetwork {
		return true
	}
	return slices.Contains(h.network, d)
}

// logicableFromIndex is CircuitHousing.GetLogicableFromIndex.
//
// It returns errRuntime for a pin index outside the array, because the game
// indexes the array before validating and the chip reports the resulting host
// exception as ExcUnknown. That is reachable: the device operand forms accept
// d0 through d9.
func (h *Housing) logicableFromIndex(index, network int) (Device, error) {
	if index == BaseUnitIndex {
		if network != BaseNetworkIndex {
			return nil, nil
		}
		if h.chip == nil {
			return nil, nil
		}
		return h.chip, nil
	}
	if index < 0 || index >= ic10.NumDevicePins {
		return nil, hostErrorf("device index %d outside d0 through d%d", index, ic10.NumDevicePins-1)
	}
	device := h.pins[index]
	if !h.onNetwork(device) {
		return nil, nil
	}
	if network != BaseNetworkIndex {
		connected, ok := device.(NetworkDevice)
		if !ok {
			return nil, nil
		}
		return connected.Network(network), nil
	}
	return device, nil
}

// logicableFromID is CircuitHousing.GetLogicableFromId. Reference id zero is
// no device, matching the game's own guard.
func (h *Housing) logicableFromID(id, network int) Device {
	if id == 0 {
		return nil
	}
	device := h.byID[id]
	if !h.onNetwork(device) {
		return nil
	}
	if network != BaseNetworkIndex {
		connected, ok := device.(NetworkDevice)
		if !ok {
			return nil
		}
		return connected.Network(network)
	}
	return device
}

// Enum ordinals the interpreter branches on directly. They restate values that
// live in the generated tables so that a switch can be written over them;
// TestEnumOrdinalsMatchTables keeps the two in step.
//
// Batch modes and reagent modes are plain ints rather than the generated
// narrow types because the game's enums are int backed: an operand resolving to
// 256 is an undefined mode there and must not fold onto Contents here.
const (
	logicTypeNone ic10.LogicType = 0

	batchAverage = 0
	batchSum     = 1
	batchMinimum = 2
	batchMaximum = 3
	batchCount   = 4

	reagentContents      = 0
	reagentRequired      = 1
	reagentRecipe        = 2
	reagentTotalContents = 3
)

// toLogicType narrows an operand to the ushort the game's LogicType enum is
// backed by, matching Enum.ToObject's unchecked conversion.
func toLogicType(value int) ic10.LogicType { return ic10.LogicType(uint16(value)) }

// toLogicSlotType narrows an operand to the byte LogicSlotType is backed by.
func toLogicSlotType(value int) ic10.LogicSlotType { return ic10.LogicSlotType(uint8(value)) }

// batchRead is Device.BatchRead, the aggregation behind lb, lbn, lbs and lbns.
//
// A read matching no device does not fault. It returns NaN for Average, because
// the sum is divided by a zero count, zero for Sum and Minimum, and negative
// infinity for Maximum. Testing the result against zero is therefore wrong;
// test for NaN.
//
// matches selects the devices in scope, and value reads one of them. Both are
// called back to front over the list, matching the game's descending loop, so
// that ties resolve the same way.
func batchRead(mode int, devices []Device, matches func(Device) bool, value func(Device) float64) float64 {
	switch mode {
	case batchCount:
		count := 0
		for i := len(devices) - 1; i >= 0; i-- {
			if devices[i] != nil && matches(devices[i]) {
				count++
			}
		}
		return float64(count)
	case batchAverage, batchSum:
		total, count := 0.0, 0
		for i := len(devices) - 1; i >= 0; i-- {
			d := devices[i]
			if d == nil || !matches(d) {
				continue
			}
			total += value(d)
			count++
		}
		if mode == batchAverage {
			total /= float64(count)
		}
		return total
	case batchMinimum:
		best := math.Inf(1)
		for i := len(devices) - 1; i >= 0; i-- {
			d := devices[i]
			if d == nil || !matches(d) {
				continue
			}
			// The comparison is negated so a NaN reading replaces the running
			// value rather than being skipped.
			if v := value(d); !(v >= best) {
				best = v
			}
		}
		if best >= math.Inf(1) {
			best = 0
		}
		return best
	case batchMaximum:
		best := math.Inf(-1)
		for i := len(devices) - 1; i >= 0; i-- {
			d := devices[i]
			if d == nil || !matches(d) {
				continue
			}
			if v := value(d); !(v <= best) {
				best = v
			}
		}
		return best
	default:
		// An undefined batch mode falls off the game's switch, leaving the
		// accumulator at the zero it was initialised with. Not an error.
		return 0
	}
}
