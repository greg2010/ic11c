package vm

import (
	"math"

	"github.com/greg2010/ic11c/internal/ic10"
)

// A branch case runs a three or four line program where the target line adds a
// distinctive amount to r0, so the final value says which path ran.
var branchCases = []instructionCase{
	{
		name: "j is absolute", op: ic10.OpJ,
		source:        "j 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
		wantPC:        new(3),
	},
	{
		name: "jr is relative to the current line", op: ic10.OpJr,
		source:        "add r0 r0 1\njr 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		name: "jal writes the following line to ra whether or not anything is conditional", op: ic10.OpJal,
		source:        "jal 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5, ic10.RegRA: 1},
	},
	{
		name: "jal and j ra make a call and return", op: ic10.OpJal,
		source:        "jal 3\nadd r0 r0 100\nj 5\nadd r0 r0 7\nj ra\nmove r1 1",
		wantRegisters: map[ic10.Register]float64{0: 107, 1: 1, ic10.RegRA: 1},
	},
	{
		name: "beq taken", op: ic10.OpBeq,
		source:        "beq 1 1 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "beq not taken falls through", op: ic10.OpBeq,
		source:        "beq 1 2 3\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 6},
	},
	{
		name: "bne", op: ic10.OpBne,
		source:        "bne 1 2 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "blt", op: ic10.OpBlt,
		source:        "blt 1 2 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "bgt", op: ic10.OpBgt,
		source:        "bgt 2 1 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "ble", op: ic10.OpBle,
		source:        "ble 2 2 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "bge", op: ic10.OpBge,
		source:        "bge 2 2 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "bltz", op: ic10.OpBltz,
		source:        "bltz -1 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "bgez", op: ic10.OpBgez,
		source:        "bgez 0 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "blez", op: ic10.OpBlez,
		source:        "blez 0 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "bgtz", op: ic10.OpBgtz,
		source:        "bgtz 1 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "beqz", op: ic10.OpBeqz,
		source:        "beqz 0 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "bnez", op: ic10.OpBnez,
		source:        "bnez 1 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "bap", op: ic10.OpBap,
		source:        "bap 1000 1000.5 0.001 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "bna", op: ic10.OpBna,
		source:        "bna 1000 1002 0.001 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "bapz", op: ic10.OpBapz,
		source:        "bapz 0 0.001 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "bnaz", op: ic10.OpBnaz,
		source:        "bnaz 5 0.001 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "bnan", op: ic10.OpBnan,
		registers:     map[ic10.Register]float64{5: math.NaN()},
		source:        "bnan r5 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		name: "brnan is relative", op: ic10.OpBrnan,
		registers:     map[ic10.Register]float64{5: math.NaN()},
		source:        "add r0 r0 1\nbrnan r5 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		name: "breq is relative", op: ic10.OpBreq,
		source:        "add r0 r0 1\nbreq 1 1 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		name: "brne is relative", op: ic10.OpBrne,
		source:        "add r0 r0 1\nbrne 1 2 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		name: "brlt is relative", op: ic10.OpBrlt,
		source:        "add r0 r0 1\nbrlt 1 2 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		name: "brgt is relative", op: ic10.OpBrgt,
		source:        "add r0 r0 1\nbrgt 2 1 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		name: "brle is relative", op: ic10.OpBrle,
		source:        "add r0 r0 1\nbrle 1 1 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		name: "brge is relative", op: ic10.OpBrge,
		source:        "add r0 r0 1\nbrge 1 1 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		name: "brltz is relative", op: ic10.OpBrltz,
		source:        "add r0 r0 1\nbrltz -1 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		name: "brgtz is relative", op: ic10.OpBrgtz,
		source:        "add r0 r0 1\nbrgtz 1 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		name: "brlez is relative", op: ic10.OpBrlez,
		source:        "add r0 r0 1\nbrlez 0 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		name: "brgez is relative", op: ic10.OpBrgez,
		source:        "add r0 r0 1\nbrgez 0 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		name: "breqz is relative", op: ic10.OpBreqz,
		source:        "add r0 r0 1\nbreqz 0 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		name: "brnez is relative", op: ic10.OpBrnez,
		source:        "add r0 r0 1\nbrnez 1 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		name: "brap is relative", op: ic10.OpBrap,
		source:        "add r0 r0 1\nbrap 1000 1000.5 0.001 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		name: "brna is relative", op: ic10.OpBrna,
		source:        "add r0 r0 1\nbrna 1000 1002 0.001 2\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 101},
	},
	{
		// A link form writes ra only when the branch is taken, unlike jal.
		name: "beqal links on the taken path", op: ic10.OpBeqal,
		source:        "beqal 1 1 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5, ic10.RegRA: 1},
	},
	{
		name: "beqal leaves ra alone when not taken", op: ic10.OpBeqal,
		source:        "beqal 1 2 3\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 6},
	},
	{
		name: "bneal", op: ic10.OpBneal,
		source:        "bneal 1 2 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5, ic10.RegRA: 1},
	},
	{
		name: "bltal", op: ic10.OpBltal,
		source:        "bltal 1 2 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5, ic10.RegRA: 1},
	},
	{
		name: "bgtal", op: ic10.OpBgtal,
		source:        "bgtal 2 1 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5, ic10.RegRA: 1},
	},
	{
		name: "bleal", op: ic10.OpBleal,
		source:        "bleal 1 1 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5, ic10.RegRA: 1},
	},
	{
		name: "bgeal", op: ic10.OpBgeal,
		source:        "bgeal 1 1 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5, ic10.RegRA: 1},
	},
	{
		name: "bapal", op: ic10.OpBapal,
		source:        "bapal 1000 1000.5 0.001 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5, ic10.RegRA: 1},
	},
	{
		name: "bnaal", op: ic10.OpBnaal,
		source:        "bnaal 1000 1002 0.001 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5, ic10.RegRA: 1},
	},
	{
		name: "bltzal", op: ic10.OpBltzal,
		source:        "bltzal -1 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5, ic10.RegRA: 1},
	},
	{
		name: "bgtzal", op: ic10.OpBgtzal,
		source:        "bgtzal 1 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5, ic10.RegRA: 1},
	},
	{
		name: "blezal", op: ic10.OpBlezal,
		source:        "blezal 0 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5, ic10.RegRA: 1},
	},
	{
		name: "bgezal", op: ic10.OpBgezal,
		source:        "bgezal 0 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5, ic10.RegRA: 1},
	},
	{
		name: "beqzal", op: ic10.OpBeqzal,
		source:        "beqzal 0 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5, ic10.RegRA: 1},
	},
	{
		name: "bnezal", op: ic10.OpBnezal,
		source:        "bnezal 1 2\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5, ic10.RegRA: 1},
	},
	{
		name: "a label resolves to its own line", op: ic10.OpJ,
		source:        "j done\nadd r0 r0 1\ndone:\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
	{
		// A program counter below zero stops the chip for good, exactly as one
		// past the last line does. Nothing after the jump ever runs, however
		// many ticks pass.
		name: "a negative jump target stops the chip", op: ic10.OpJ,
		source:        "j -3\nadd r0 r0 1\nadd r0 r0 5\nadd r0 r0 10\nadd r0 r0 100\nadd r0 r0 1000",
		ticks:         3,
		wantRegisters: map[ic10.Register]float64{},
		wantPC:        new(-3),
		wantExecuted:  new(0),
	},
	{
		name: "a negative branch target stops the chip", op: ic10.OpBltz,
		source:        "bltz -1 -3\nadd r0 r0 1\nadd r0 r0 5\nadd r0 r0 10\nadd r0 r0 100\nadd r0 r0 1000",
		ticks:         3,
		wantRegisters: map[ic10.Register]float64{},
		wantPC:        new(-3),
		wantExecuted:  new(0),
	},
	{
		// Jumping one past the last line is the machine's only stop instruction,
		// and it stops the same way a negative target does.
		name: "a jump past the last line stops the chip", op: ic10.OpJ,
		source:        "j 3\nadd r0 r0 1\nadd r0 r0 5",
		ticks:         3,
		wantRegisters: map[ic10.Register]float64{},
		wantPC:        new(3),
		wantExecuted:  new(0),
	},
	{
		// A relative jump reaches a negative line the same way, and the operand
		// is an offset rather than a line.
		name: "a relative jump below zero stops the chip", op: ic10.OpJr,
		source:        "add r0 r0 1\njr -4\nadd r0 r0 5",
		ticks:         3,
		wantRegisters: map[ic10.Register]float64{0: 1},
		wantPC:        new(-3),
		wantExecuted:  new(0),
	},
	{
		// The register arm of a jump target rounds to even, where every other
		// arm of the same chain truncates.
		name: "a register jump target rounds up to even", op: ic10.OpJ,
		registers:     map[ic10.Register]float64{5: 3.5},
		source:        "j r5\nadd r0 r0 1\nadd r0 r0 5\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 100},
	},
	{
		name: "a register jump target rounds a tie down to even", op: ic10.OpJ,
		registers:     map[ic10.Register]float64{5: 2.5},
		source:        "j r5\nadd r0 r0 1\nadd r0 r0 5\nadd r0 r0 10\nadd r0 r0 100",
		wantRegisters: map[ic10.Register]float64{0: 115},
	},
	{
		name: "a defined jump target truncates", op: ic10.OpJ,
		source:        "define target 2.9\nj target\nadd r0 r0 1\nj 5\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 1},
	},
	{
		name: "an aliased register jump target truncates", op: ic10.OpJ,
		registers:     map[ic10.Register]float64{5: 3.9},
		source:        "alias t r5\nj t\nadd r0 r0 1\nadd r0 r0 5",
		wantRegisters: map[ic10.Register]float64{0: 5},
	},
}
