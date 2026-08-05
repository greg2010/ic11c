// Package chip drives the game's own IC10 chip as a subprocess, built by
// tools/chipgen from the decompiled Stationeers source and run under Mono. It
// starts the process, speaks its wire protocol, and reports what the chip did,
// with no opinion on whether the answer is right.
//
// Every double on the wire, in either direction, is its IEEE-754 bit pattern:
// "0x" followed by sixteen lowercase hex digits, read as unsigned. Decimal is
// unusable here — Mono's double.Parse is not correctly rounded and its "R"
// format drops the sign of zero. Integers (indices, counts, line numbers, tick
// budgets) stay decimal.
//
// [Start] runs a process with the game's own devices. [StartFixtures] runs one
// whose devices answer any seeded property, for tracing a compiled program's
// writes; such a device never raises the faults a real one does, so a run
// checking for those faults must use [Start]. The two are distinguished by
// type ([FixtureHarness] embeds [Harness], not the reverse) and by a
// "fixtures" key each permissive state block carries.
//
// [Harness.Step] retires up to a budget of instructions and stops on a fault,
// a yield, a suspending sleep, or the program counter leaving the program; a
// yield returns -index-1 and a suspending sleep returns -index, so a sleep on
// line 0 returns -0, does not suspend, and spins the rest of the budget.
// [Harness.Run] drives a whole program in one exchange using
// [InstructionsPerTick] per segment, as the game does; a caller that must act
// between segments should call Step instead.
package chip
