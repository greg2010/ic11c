package vm

import (
	"github.com/greg2010/ic11c/internal/ic10"
)

var memoryCases = []instructionCase{
	{
		name: "push writes at sp and then advances it", op: ic10.OpPush,
		source:        "push 42",
		wantRegisters: map[ic10.Register]float64{ic10.RegSP: 1},
		wantMemory:    map[int]float64{0: 42},
	},
	{
		name: "peek reads below sp without moving it", op: ic10.OpPeek,
		source:        "push 42\npeek r0",
		wantRegisters: map[ic10.Register]float64{0: 42, ic10.RegSP: 1},
		wantMemory:    map[int]float64{0: 42},
	},
	{
		name: "pop reads below sp and moves it back", op: ic10.OpPop,
		source:        "push 7\npop r0",
		wantRegisters: map[ic10.Register]float64{0: 7, ic10.RegSP: 0},
		wantMemory:    map[int]float64{0: 7},
	},
	{
		// sp is decremented before the bounds check and the side effect is not
		// rolled back, so a retry keeps walking sp downward.
		name: "pop at zero leaves sp at minus one and then faults", op: ic10.OpPop,
		source:        "pop r0",
		wantRegisters: map[ic10.Register]float64{ic10.RegSP: -1},
		wantFault:     &Fault{Type: ExcStackUnderFlow, Line: 0},
		wantPC:        new(0),
	},
	{
		name: "peek at zero faults without touching sp", op: ic10.OpPeek,
		source:    "peek r0",
		wantFault: &Fault{Type: ExcStackUnderFlow, Line: 0},
	},
	{
		name: "push at the top of the array faults", op: ic10.OpPush,
		registers: map[ic10.Register]float64{ic10.RegSP: ic10.NumMemorySlots},
		source:    "push 1",
		wantFault: &Fault{Type: ExcStackOverFlow, Line: 0},
	},
	{
		name: "poke writes a computed address without touching sp", op: ic10.OpPoke,
		source:     "poke 5 42",
		wantMemory: map[int]float64{5: 42},
	},
	{
		name: "poke past the array faults", op: ic10.OpPoke,
		source:    "poke 512 1",
		wantFault: &Fault{Type: ExcStackOverFlow, Line: 0},
	},
	{
		name: "poke below zero faults", op: ic10.OpPoke,
		source:    "poke -1 1",
		wantFault: &Fault{Type: ExcStackUnderFlow, Line: 0},
	},
	{
		// push, poke, get and put all address the same array; db is the chip
		// itself.
		name: "get through db reads what push wrote", op: ic10.OpGet,
		source:        "push 42\nget r0 db 0",
		wantRegisters: map[ic10.Register]float64{0: 42, ic10.RegSP: 1},
		wantMemory:    map[int]float64{0: 42},
	},
	{
		name: "put through db writes what peek reads", op: ic10.OpPut,
		source:        "put db 0 9\nmove sp 1\npeek r0",
		wantRegisters: map[ic10.Register]float64{0: 9, ic10.RegSP: 1},
		wantMemory:    map[int]float64{0: 9},
	},
	{
		// put wraps the write and maps the address faults to specific types.
		name: "put past the array reports a stack overflow", op: ic10.OpPut,
		source:    "put db 512 1",
		wantFault: &Fault{Type: ExcStackOverFlow, Line: 0},
	},
	{
		name: "put below zero reports a stack underflow", op: ic10.OpPut,
		source:    "put db -1 1",
		wantFault: &Fault{Type: ExcStackUnderFlow, Line: 0},
	},
	{
		// get does not wrap, so the same address arrives as a host exception and
		// the chip reports it as Unknown. The asymmetry is in the game.
		name: "get past the array reports an unknown error", op: ic10.OpGet,
		source:    "get r0 db 512",
		wantFault: &Fault{Type: ExcUnknown, Line: 0},
	},
	{
		name: "get below zero also reports an unknown error", op: ic10.OpGet,
		source:    "get r0 db -1",
		wantFault: &Fault{Type: ExcUnknown, Line: 0},
	},
	{
		name: "clr db zeroes all 512 slots in one instruction", op: ic10.OpClr,
		memory:        map[int]float64{0: 1, 100: 2, 511: 3},
		source:        "clr db",
		wantMemory:    map[int]float64{0: 0, 100: 0, 511: 0},
		wantRegisters: nil,
	},
	{
		name: "a poke to a low address corrupts the region below sp", op: ic10.OpPoke,
		source:        "push 1\npush 2\npoke 0 99\npeek r0\npop r1\npop r2",
		wantRegisters: map[ic10.Register]float64{0: 2, 1: 2, 2: 99, ic10.RegSP: 0},
		wantMemory:    map[int]float64{0: 99, 1: 2},
	},
}
