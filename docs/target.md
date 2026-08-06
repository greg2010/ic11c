# IC10 target

The machine ic11c compiles for. Facts here come from decompiling `Assembly-CSharp.dll` out of Stationeers depot 600762 at manifest `2546537964923579038`, assembly version 0.2.6403.27689. Where published documentation and the implementation disagree, this file follows the implementation.

## Registers

18 doubles. `r0` through `r15` are general purpose. `sp` is r16 and `ra` is r17; both are ordinary registers with no hardware protection, freely readable and writable.

Indirect referencing prefixes extra `r`s: `rr0` means the register whose index is held in `r0`, and it chains. The bound is the register array length, 18, so an index of 16 or 17 reaches `sp` or `ra`. With `r0` holding 16, `move rr0 7` writes `sp`. An index outside 0 through 17 faults, but which fault depends on where the index came from. An index resolved out of a register is bounds-checked and raises `OutOfRegisterBounds`. An index written in the operand text is not checked at all: the parse accepts any integer after the `r`s and the register array is then indexed with it, so `r99` and `rr99` compile and raise `Unknown` when the operand is read. The `r0` through `r15` restriction that does exist applies only to a register holding a device reference, which a separate pattern matches.

What follows the `r` is read by the same integer parse numeric literals use, which admits a leading sign and leading zeros: `r05` and `r+5` both name r5. Nothing should emit those spellings, but a name-mangling pass has to treat them as taken.

## Memory

One 512-double array per chip. `push`, `pop`, `peek`, `poke`, `get`, and `put` all address the same array. The IC housing owns no memory of its own; an access through `db` forwards to the inserted chip.

| Access | Instruction | Instructions | Uses `sp` |
|---|---|---|---|
| Write at computed index | `poke` | 1 | no |
| Read at computed index | `get db` | 1 | no |
| Push, pop, peek | `push`, `pop`, `peek` | 1 | yes |
| Zero all 512 slots | `clr db` | 1 | no |

Because there is only one array, `push` and `pop` do not delimit a protected region. A `poke` to a low address corrupts call frames, and another chip writing to this housing with `put` reaches the same memory.

`pop` decrements `sp` before its bounds check, so a `pop` at zero leaves `sp` at -1 and then faults. The side effect is not rolled back.

State survives power loss, chip removal, and reflashing. Nothing can be assumed zero at program start.

## Values

Every register and slot holds an IEEE double. There is no integer type and no byte addressing.

- Integers are exact to 2^53.
- Bitwise and shift operations convert each operand to a signed int64, operate, then convert back. Neither conversion is the identity across the whole exact range, and they do not agree with each other. The operand conversion reduces modulo 2^53, so it is the identity strictly inside ±2^53 and sends both ends to 0. The write-back keeps 53 bits and takes the sign from bit 53, so it is the identity on [−2^53, 2^53) and answers −2^53 for 2^53. An operand outside ±2^63 faults instead: `ShiftUnderflow` below, `ShiftOverflow` above.
- `ext` and `ins` cap their payload at 53 bits. `rol` and `ror` rotate over 54. The widths differ.
- Arithmetic does not wrap. It follows IEEE double behavior, growing toward infinity.
- NaN propagates. `max`, `min`, and `clamp` return NaN if any operand is NaN.
- `mod` is a truncated remainder with the divisor added back once when that remainder is negative. It is not a floor modulus; see below.
- `round` is banker's rounding. Ties go to even, so `round 0.5` is 0 and `round 1.5` is 2. Folding a `round` with away-from-zero semantics diverges from the machine.

Nine constants exist: `nan`, `pinf`, `ninf`, `pi`, `tau`, `deg2rad`, `rad2deg`, `epsilon`, `rgas`. Eight are usable as literal operands. `nan` is not: the operand parser treats a NaN literal as unset, so `move r0 nan` raises `IncorrectVariable`. NaN reaches a register through `define x nan` or through another register.

`nan` is .NET's `double.NaN`, whose bit pattern is `0xfff8000000000000`. The payload is observable, because `mod` propagates an operand's own pattern instead of substituting one.

`deg2rad` and `rad2deg` are float-precision literals widened to double, `0.01745329238474369` and `57.295780181884766`, rather than pi/180 and 180/pi. Folding them at full double precision diverges from the game. `epsilon` is `double.Epsilon`, roughly 4.94e-324, the smallest subnormal. It is not a comparison tolerance.

### `mod`

`mod a b` takes the truncated remainder, which is C's `%` carrying the dividend's sign, and adds `b` back exactly once if that remainder came out negative.

| `a` | `b` | `mod` | C `%` | floor mod | `rem_euclid` |
|---|---|---|---|---|---|
| 7 | 3 | 1 | 1 | 1 | 1 |
| −7 | 3 | 2 | −1 | 2 | 2 |
| 7 | −3 | 1 | 1 | −2 | 1 |
| −7 | −3 | **−4** | −1 | −1 | 2 |
| −0.5 | −0.25 | −0 | −0 | n/a | n/a |

The add-back moves a negative remainder toward zero only when `b` is positive; with a negative divisor it moves the result further from zero. `mod` therefore coincides with a floor modulus for `b > 0` and with nothing standard otherwise, and its result carries no guaranteed sign.

Non-finite operands take the same two steps. A NaN operand propagates its own bit pattern, the dividend winning when both are NaN. An infinite dividend and a zero divisor both give the quiet NaN `0xfff8000000000000`. An infinite divisor leaves the remainder equal to the dividend, so the add-back sends a negative dividend to infinity: `mod -5 pinf` is `+inf`.

MicroC's `%` follows C, so instruction selection cannot emit `mod` for it and must synthesize the truncated remainder. A fix-up written against floor semantics, subtracting the divisor when the sign is wrong, is correct only for a positive divisor and silently wrong for `mod -7 -3`.

### Number syntax

A numeric literal admits a leading or a trailing sign, a decimal point, and thousands separators. It admits no exponent.

| Text | Result |
|---|---|
| `1,000` | 1000 |
| `42-` | −42 |
| `1e5` | `IncorrectVariable` |

The emitter must never produce exponential notation. Shortest-round-trip and `%g`-style formatters reach for an exponent at large and small magnitudes, and the chip rejects what they produce.

## Comparisons and `select`

`select d a b c` computes `d = a != 0 ? b : c`. The condition is the first source operand and the result operands follow it in true-then-false order. The test is against zero instead of against one, so any non-zero condition takes `b`. NaN is non-zero and takes `b` as well.

Every comparison is a plain IEEE double comparison, so a NaN operand answers by family rather than uniformly. The tolerance operand of the approximate forms counts as an operand for this purpose.

| Family | Instructions | Test | With a NaN operand |
|---|---|---|---|
| Ordered | `slt`, `sgt`, `sle`, `sge`, `blt`, `bgt`, `ble`, `bge` | `<`, `>`, `<=`, `>=` | false |
| Equality | `seq`, `sne`, `beq`, `bne` | `==`, `!=` | `==` false, `!=` true |
| Approximate | `sap`, `bap` | `abs(a - b) <= max(t * max(abs(a), abs(b)), 1.1210387714598537e-44)` | false |
| Approximate, negated | `sna`, `bna` | `abs(a - b) > max(t * max(abs(a), abs(b)), 1.1210387714598537e-44)` | false |
| NaN test | `snan`, `snanz`, `bnan` | `IsNaN(a)` | `snan` and `bnan` true, `snanz` false |

The `z`, `r`, and `al` forms substitute a literal `0` operand, a relative encoding, or a link, and change nothing about the predicate. The `z` suffix is two different things, and `snanz` is the other one: it substitutes nothing, taking exactly the operands `snan` takes, and answers the negation of `snan`'s test, which is why those two are each other's complement below. `bdse` and `bdns` test whether a device operand resolves and see no operand a NaN could reach.

Whether a predicate and its apparent negation are complements decides if a branch may be inverted to drop a trailing jump.

| Pair | Complements |
|---|---|
| `blt`/`bge`, `bgt`/`ble` | no. Both fall through when either operand is NaN |
| `beq`/`bne` | yes |
| `bap`/`bna` | no. `_BRNA_Operation` writes `>` instead of negating `<=`, so both fall through when an operand or the tolerance is NaN |
| `bdse`/`bdns` | yes |
| `snan`/`snanz` | yes, but `bnan` has no branch counterpart to invert into |

Inverting an ordered or approximate branch is therefore a miscompilation unless no operand can be NaN. Intrinsic results can be: a batch read matching no device returns NaN for `Average`, and `sqrt`, `log`, `asin`, and `acos` return NaN outside their domains. The approximate forms also take the tolerance as an operand, so a computed tolerance is a third way in.

## Limits

| Limit | Value |
|---|---|
| Lines | 128 |
| Characters per line | 90 |
| Program bytes | 4096 |
| Instructions per tick | 128 |
| Tick length | 500 ms |

The byte cap would be reached first only by a program averaging 32 bytes a line, which most programs stay well under, so lines bind first on all but the widest programs and the budget to plan against is 128 instructions. Which of the two a given program meets first is a property of its line width rather than of the target, so it is computed per program: the size report names the closer limit, and `task corpus:measure` prints what every fixture spends of each. A program spending an equal share of both has neither binding first, and is read as byte-bound.

The 90 character line width is not a third budget to rank against the other two. It bounds one line's formatting rather than the whole program, so a program spending most of it is one line from being too wide rather than close to being too big. A program can still exceed it: fixed notation is the only notation the chip reads, and a subnormal immediate other than `epsilon` spells as at least 310 characters.

All three limits belong to the editor, not to the chip's compiler, and it enforces them two different ways. The byte cap is a refusal: `UpdateFileSize` disables the submit button above 4096. The line count and the line width are silent truncation, so a program over either pastes cleanly and runs as a different program than the one compiled.

## Execution and errors

128 instructions run per tick, after which the chip pauses and resumes at the same point on the next tick with registers and stack intact. Overrun is not an error, and no exception type represents it. `yield` ends the tick early and returns unused budget; it differs from exhaustion only in timing.

`sleep` re-enters itself each tick until its duration elapses. On line 0 it returns `-0`, which fails the negative test that ends the tick, so it consumes the full instruction budget.

A program ends by running out of lines. Once the program counter reaches the line one past the last, the chip stops for good: no fault is raised, no error state is set, and every later tick retires nothing. Registers and memory keep whatever the run left in them, and only an edit or a reflash starts the program again. Falling off the end and jumping to the line one past the end are the same thing, which makes a jump past the end the only stop instruction the machine has.

A negative jump target is not that. The tick loop reads a negative program counter as the end-of-tick signal `yield` and `sleep` use, negates it, and resumes there on the next tick, so `j -5` ends the tick and continues at line 5. It stops the program only where the negated line is itself past the end.

| Failure | Effect |
|---|---|
| Runtime fault | The faulting line is retried every tick. If the cause clears, execution resumes and the error state clears. No player intervention required |
| Compile error | Execution is blocked entirely until the source is edited |

`hcf` is not a trap or an abort. It destroys the chip and starts a fire.

The chip validates almost nothing at compile time: every operand helper but one is constructed with exceptions disabled. Bad registers, out-of-range device pins, and invalid logic types compile cleanly and then fault at runtime, once per tick, indefinitely. Six conditions are caught at compile time: arity, unknown mnemonics, duplicate labels, duplicate `define`, the preprocessor forms, and malformed register text. The last is not a deliberate check. The index scanner carries no length guard, so `move r 1` runs off the end of the string while the operand is constructed and becomes a compile error of type `Unknown`. Operand validation is otherwise the compiler's responsibility.

Paired instructions do not report the same condition the same way. In each pair the first reports the less specific or the less accurate error.

| Condition | Instruction | Error |
|---|---|---|
| Memory address out of range | `get` | `Unknown` at either end |
| | `put` | Stack overflow above the array, stack underflow below zero |
| Unknown reference id | `ld` | `Unknown` |
| | `sd` | Device not found |
| Device not memory-writable | `clr` | Not readable |
| | `clrd` | Not writable |

`put` and `sd` wrap their calls and map failures onto specific error types; `get` and `ld` call through bare. `clr` and `clrd` are both writes, so `clr` naming a read is wrong.

## Instructions not to emit

Each of these miscompiles, cannot assemble, or destroys the machine. Selection must refuse them outright.

| Instruction | Reason |
|---|---|
| `brapz`, `brnaz`, `bapzal`, `bnazal` | Uncompilable in this build. Each checks for an argument count of 3 and then reads a fourth operand, so three operands raise an arity error and two raise an index-out-of-range surfaced as an unknown error |
| `sla` | Byte-identical to `sll`. Both construct the same operation performing a plain left shift with zero fill. The in-game help text claiming sign fill is wrong |
| `br*`, `jr` | Relative forms encoded as line offsets. Any pass that changes line counts silently corrupts them |
| `hcf` | Destroys the chip |
| `sleep` on line 0 | Returns `-0`, so the tick never ends and the chip re-runs it for the whole budget. Only the placement is refused; on any other line `sleep` is ordinary, and where a line falls is knowable only once the layout is final |

### Lines that cost budget

These assemble and behave correctly. They are a cost against the 128-line and 128-instruction budgets rather than a correctness hazard, so the backend weighs them and does not refuse them.

| Line | Cost |
|---|---|
| Comment on its own line | one line, one execution slot |
| Blank line | one line, one execution slot |
| `alias` | one line, one execution slot; the name resolves only once the line has run |
| `define` | one line, one execution slot; the value is fixed while compiling, so the line does nothing at runtime |

## Operand encoding

Logic types resolve by bare member name, case-sensitively, to an integer value. Emitting the integer is exactly equivalent, occupies the same one operand, and costs fewer bytes, which buys room only on a program the byte budget binds first.

Generated names must avoid the LogicType, LogicSlotType, batch-mode, and constant keyword space. A label named `Temperature` shadows the logic type, and every instruction referencing it then faults at runtime.

| Enum | Members | Backing | Notes |
|---|---|---|---|
| LogicType | 358 | 16-bit | 23 deprecated |
| LogicSlotType | 33 | 8-bit | none deprecated |
| LogicBatchMethod | 5 | int | `Average`, `Sum`, `Minimum`, `Maximum`, `Count` |
| LogicReagentMode | 4 | int | `Contents`, `Required`, `Recipe`, `TotalContents` |

Batch method and reagent mode take the full int range. A value of 256 is an undefined mode in the game rather than mode 0, and folding it onto mode 0 by truncation is wrong.

Batch reads with no matching device return `nan` for `Average`, `0` for `Sum`, `Minimum` and `Count`, and `ninf` for `Maximum`. Testing the result against zero is wrong; test for NaN.

`label`, `getd`, `putd`, `ld`, and `sd` are deprecated but functional. There is no name-filtered batch slot store: `lbns` exists for loads with no `sbns` counterpart.

Device operands accept `db`, `d0` through `d9`, indirect `dr` forms, a register holding a ReferenceId, and numeric literals. Network connection references take the form `d0:1`.

A housing has six pins. `d6` through `d9` compile cleanly and then index a six-element array, faulting as `Unknown` once per tick. An out-of-range pin is caught nowhere, so the compiler must reject it.

## Fidelity limits

These bound what can be established about the machine from what this repository has. They are not machine behavior.

| Area | Limit |
|---|---|
| Transcendental functions | The implementations are the .NET runtime's C library and are absent from the decompile. Any expectation written here is a second library's answer, and two libraries agree only to within an ULP |
| `rmap`'s narrowing cast | The reagent hash reaches an unchecked `conv.i4`, which ECMA-335 leaves unspecified for a NaN, an infinity and an out-of-range magnitude. The answer is the runtime's: measured on Mono 6.12 for amd64, where it lowers to a bare `cvttsd2si` and answers −2^31 for all of them. Unity ships a fork of that runtime and the fork has not been measured |
| String hashing | The preprocessor hash is CRC-32 reinterpreted as a signed 32-bit integer. It reproduces every published prefab hash, but the underlying function is native to the engine and absent from the decompile, so this is an inference rather than a reading |
| `rand` | Draws from an unseeded generator in the game. Runs here arm a seed instead, so what a program does with it is reproducible and is not what a game chip does |
