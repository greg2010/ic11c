// Package difftest generates IC10 programs and runs them on the game's own
// chip.
//
// [ValueProgram] emits terminating, fault-free programs; [FaultProgram]
// provokes a named fault on a known line. Generation is a pure function of a
// seed, so a failure replays by regenerating from the seed it reports. A
// generated program is held to the size limits internal/emit states, since
// nothing about running one enforces them, and never ends in a newline, since
// the chip would count a trailing one as an extra empty instruction.
package difftest
