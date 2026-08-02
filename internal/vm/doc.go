// Package vm interprets IC10 assembly the way the Stationeers chip does, so
// that compiler output can be checked against a reference rather than against a
// reading of the documentation.
//
// Every operation is transliterated from the decompiled game implementation in
// Assets.Scripts.Objects.Electrical.ProgrammableChip, from the assembly named
// by ic10.ManifestID, version 0.2.6403.27689. Each operation records the C#
// class it derives from in the instruction table, so a future game build can be
// diffed class by class.
// Where published documentation and the implementation disagree, this package
// follows the implementation, including behaviour that looks like a defect.
//
// The chip validates almost nothing at compile time. Only arity, unknown
// mnemonics, duplicate labels, duplicate defines, and the preprocessor forms
// are rejected by Load; everything else faults at run time, once per tick,
// until the cause clears. Reproducing that split matters as much as
// reproducing the arithmetic: it is what makes operand validation the
// compiler's responsibility rather than the machine's.
//
// Nothing here fails silently. An unimplemented path returns an error rather
// than doing nothing, because a silent no-op in an oracle is worse than a
// crash.
//
// # Fidelity limits
//
// Transcendental results (sin, cos, tan, asin, acos, atan, atan2, log, exp,
// pow) come from Go's math package, while the game gets them from the .NET
// runtime's C library. Both are correctly rounded to within an ULP but neither
// guarantees bit equality with the other, so differential comparison of those
// instructions can differ in the last place. Every other instruction is
// bit-exact by construction.
//
// A Machine is not safe for concurrent use.
package vm
