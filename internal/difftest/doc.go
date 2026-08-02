// Package difftest cross-checks the ic11c IC10 interpreter against an
// independent emulator.
//
// It supplies three things: an adapter that runs a program on internal/vm and
// reports the run in the shape internal/oracle compares, and two generators
// that produce the programs. The first generator emits terminating, fault-free
// programs and is compared on final machine state; the second provokes faults
// and is compared on error type and faulting line. The error model is where
// independent implementations diverge most, which is what makes the second the
// more valuable of the two.
//
// Generation is a pure function of a seed, so a failure is replayed by
// regenerating from the seed the failure reports.
//
// Both corpora run against ic10emu. The npm harness is not used: beyond the
// error model the divergence registry already records as incomparable, it also
// disagrees on final machine state in ways no entry covers. Those disagreements
// are recorded rather than worked around; TestReportsWhatIsNotCompared prints
// the record on every run.
//
// # What is not generated
//
// The generators emit a deliberately narrow slice of the instruction set.
// Excluded lists every mnemonic that is kept out and why, and Coverage reports
// what a corpus actually reached, so a generator that never emits half the
// instruction set says so rather than looking thorough.
//
// A second record covers the constructs kept out because the two
// implementations disagree about them and the oracle divergence registry does
// not cover it. That registry is an allowlist and stays closed; nothing here
// excuses a mismatch.
//
// # Source text
//
// A program must not end in a newline. The two harnesses disagree about a
// trailing one: ic10emu parses with Rust's str::lines and drops it, the npm
// package and internal/vm both split on "\n" and keep it as a final empty
// line that retires one more instruction. Neither behaviour is wrong, and
// neither is worth a divergence entry, so the generators simply never emit
// one.
package difftest
