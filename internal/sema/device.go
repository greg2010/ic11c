package sema

import (
	"math"
	"strconv"

	"github.com/greg2010/ic11c/internal/ast"
)

// Device is a resolved device operand: the housing, or one of its pins.
//
// A device is a compile-time value throughout. The chip resolves a device
// position when the line is assembled and checks nothing, so a literal is the
// only thing that may stand there, and a dev object, a dev parameter, and a
// written pin all have to fold to one before instruction selection runs.
type Device struct {
	// Base is db, the housing the chip is inserted into.
	Base bool
	// Pin is the pin index of a d0 through d5 operand.
	Pin uint8
}

// baseDevice is the encoding of db, which takes no pin.
const baseDevice = -1

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
// device produces.
//
// The pin it reads back is not checked against the housing. A pin the housing
// does not have is a diagnostic instruction selection owns, where the argument
// it came from can be named, and it needs the number to name it with.
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
// that reports one it does not.
const deviceSpellings = "db, d0 through d5, or a dev object"

// resolveDevice reads a device-valued expression.
//
// It answers with a fixed device, or with the dev parameter whose device the
// call site supplies — which is left for IR generation, since the pin is a
// property of the site the body is spliced into rather than of the body.
//
// A bare pin spelling never collides with a variable: the spellings are
// reserved, so no declaration takes one.
func (c *checker) resolveDevice(x ast.Expr, what string) (Device, *Symbol, bool) {
	id, named := x.(*ast.Ident)
	if !named {
		c.errorf(x.Pos(), "%s must name a device: %s", what, deviceSpellings)
		return Device{}, nil, false
	}
	if pin, isPin := devicePin(id.Name); isPin {
		device := BaseDevice()
		if pin >= 0 {
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
