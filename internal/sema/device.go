package sema

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/greg2010/ic11c/internal/ast"
	"github.com/greg2010/ic11c/internal/ic10"
)

// Device is a resolved device operand: the housing, or one of its pins. It
// is a compile-time value throughout — the chip resolves a device position
// when the line is assembled and checks nothing, so a literal is the only
// thing that may stand there.
type Device struct {
	// Base is db, the housing the chip is inserted into.
	Base bool
	// Pin is the index of one of the housing's numbered pins.
	Pin uint8
}

// baseDevice is the encoding of db, which takes no pin. It is what
// [devicePin] answers for the spelling as well as what [Device.Code] emits, so
// the two cannot come to disagree about which integer means the housing.
const baseDevice = -1

// maxDevicePin is the highest pin a housing has. A pin past it assembles on the
// chip and then faults once per tick with no error naming it, so the compiler is
// the only thing that can reject one.
const maxDevicePin = ic10.NumDevicePins - 1

// BaseDevice is db, the housing the chip is inserted into.
func BaseDevice() Device { return Device{Base: true} }

// PinDevice is one housing pin.
func PinDevice(pin uint8) Device { return Device{Pin: pin} }

// String renders the device the way the source would write it.
func (d Device) String() string {
	if d.Base {
		return "db"
	}
	return "d" + strconv.FormatUint(uint64(d.Pin), 10)
}

// Code encodes the device as the integer constant an intrinsic call carries in
// its device argument position, which is how instruction selection receives it.
func (d Device) Code() int64 {
	if d.Base {
		return baseDevice
	}
	return int64(d.Pin)
}

// DecodeDevice reverses [Device.Code], reporting false for an encoding no
// device produces. The pin it reads back is not checked against the
// housing: a pin the housing lacks is a diagnostic instruction selection
// owns, where the argument it came from can still be named.
func DecodeDevice(code int64) (Device, bool) {
	if code == baseDevice {
		return BaseDevice(), true
	}
	if code < 0 || code > math.MaxUint8 {
		return Device{}, false
	}
	return Device{Pin: uint8(code)}, true
}

// deviceSpellings describes what a device position accepts, for the diagnostic
// that reports one it does not. The range is spelled out of the pin count rather
// than written, so the sentence cannot outlive a game build that moves it.
var deviceSpellings = fmt.Sprintf("db, d0 through d%d, or a dev object", maxDevicePin)

// devicePin resolves a device operand spelling to its pin number, reporting
// [baseDevice] for db, which addresses the housing rather than a pin.
func devicePin(name string) (int64, bool) {
	if name == "db" {
		return baseDevice, true
	}
	digits, ok := strings.CutPrefix(name, "d")
	if !ok || len(digits) != 1 || digits[0] < '0' || digits[0] > '9' {
		return 0, false
	}
	n := int64(digits[0] - '0')
	if n > maxDevicePin {
		return 0, false
	}
	return n, true
}

// isDevicePinSpelling reports whether name is one a device position resolves,
// which is what makes it a spelling no declaration may take.
func isDevicePinSpelling(name string) bool {
	_, ok := devicePin(name)
	return ok
}

// resolveDevice reads a device-valued expression, answering with a fixed
// device or with the dev parameter whose device the call site supplies —
// left unresolved here, since the pin is a property of the site rather than
// of the body. A bare pin spelling never collides with a variable.
func (c *checker) resolveDevice(x ast.Expr, what string) (Device, *Symbol, bool) {
	id, named := x.(*ast.Ident)
	if !named {
		c.errorf(x.Pos(), "%s must name a device: %s", what, deviceSpellings)
		return Device{}, nil, false
	}
	if pin, isPin := devicePin(id.Name); isPin {
		device := BaseDevice()
		if pin != baseDevice {
			device = PinDevice(uint8(pin))
		}
		c.prog.Types[id] = DevType
		c.prog.Devices[id] = device
		return device, nil, true
	}
	sym := c.scope.lookup(id.Name)
	if sym == nil || unqual(sym.Type).Kind() != Dev {
		c.errorf(id.NamePos, "'%s' is not a device; %s must be %s", id.Name, what, deviceSpellings)
		return Device{}, nil, false
	}
	c.prog.Uses[id] = sym
	c.prog.Types[id] = sym.Type
	switch {
	case sym.Device != nil:
		c.prog.Devices[id] = *sym.Device
		return *sym.Device, nil, true
	case sym.Kind == ParamVar:
		return Device{}, sym, true
	default:
		// A dev object whose initializer did not resolve, which is reported at
		// the declaration.
		return Device{}, nil, false
	}
}

// deviceExpr reads a device-valued expression that has to be fixed here, which
// a declaration's initializer is: a dev object names one pin for the whole
// program, where a parameter names whichever the call site passed.
func (c *checker) deviceExpr(x ast.Expr, what string) (Device, bool) {
	device, param, ok := c.resolveDevice(x, what)
	if !ok {
		return Device{}, false
	}
	if param != nil {
		c.errorf(x.Pos(), "'%s' is a dev parameter, and which device it names is decided by each call; %s has to name one device for the whole program", param.Name, what)
		return Device{}, false
	}
	return device, true
}

// checkDeviceParams reports a dev parameter on a function that cannot be
// inlined: only inlining substitutes the call site's literal for the
// parameter, and a recursive function is never inlined.
func (c *checker) checkDeviceParams() {
	for _, fn := range c.prog.Funcs {
		if !fn.Recursive {
			continue
		}
		for _, param := range fn.Params {
			if unqual(param.Type).Kind() != Dev {
				continue
			}
			c.errorf(param.Pos, "'%s' takes the dev parameter '%s' and can reach itself through a call, so it is compiled out of line rather than inlined; the chip resolves a device position when the line is assembled and a real call would have to pass the pin in a register, which is not a spelling it reads — name the device at each use, or rewrite the recursion as a loop", fn.Name, param.Name)
		}
	}
}
