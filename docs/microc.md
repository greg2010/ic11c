# MicroC

## 1 Scope and conformance

### 1.1 Scope

MicroC is the language ic11c accepts: a C subset chosen so that every construct maps onto a machine with 18 registers, 512 doubles of memory shared between data and the call stack, and a 4096-byte program limit. Source files use the `.c` extension.

This document is the language definition. It is complete: a construct absent here is not in the language, and a disagreement between this document and the compiler is a defect in one of them.

### 1.2 Relation to C23

Every MicroC program is a valid C23 translation unit. Compiled `-std=c23 -ffreestanding` with the generated `ic10_prelude.h` forced in by `-include`, it is accepted by gcc and by clang, and denotes there what it denotes here for a program whose integers stay inside ±2^53, on a target whose `long` is at least 64 bits.

The reverse does not hold. MicroC diagnoses far more than C accepts. Annex A lists what it diagnoses.

### 1.3 The absence of a preprocessor

A source file contains no preprocessor directive. The prelude arrives through a compiler flag rather than through an `#include`, so no file needs a preprocessor and no file depends on a header being found. Any clangd-based editor therefore gives a MicroC file completion, go-to-definition, and diagnostics with no plugin; `ic11c prelude` writes the two files that configure one.

### 1.4 The target machine

| Property | Value |
|---|---|
| Registers | 18, all holding doubles. `r0` through `r15` are general; `sp` and `ra` are ordinary registers that `push`, `pop`, `peek` and `jal` happen to use, and no hardware protects them |
| General-register range | A device `ReferenceId` resolves within `r0` through `r15` only. An indirect `rr` form indexes the whole file, so index 16 or 17 reaches `sp` or `ra` and only an index outside 0 through 17 faults |
| Memory | 512 doubles in one array, shared with no boundary between the data region and the call stack |
| Device pins | Six sockets, `d0` through `d5`, plus `db` for the housing the chip is in |
| Program limit | 4096 bytes, 128 lines, 90 characters of instruction on a line. See 11.6 |

The register file is not a resource a program allocates. A function holding more values at once than the file and the remaining memory can serve is diagnosed rather than miscompiled; see 11.4.

## 2 Terms and definitions

The terms below are used throughout with the meanings given here.

### 2.1 Diagnostics

| Term | Meaning |
|---|---|
| diagnostic | A message the compiler attaches to a source position. It carries one of two severities and nothing else |
| error | A diagnostic that rejects the program. Nothing is emitted |
| warning | A diagnostic that does not reject the program. The program is emitted unchanged. There is exactly one warning severity and it never blocks emission |
| diagnosed | Of a construct: translation reports an error naming that construct. Refused and rejected mean the same thing and are used interchangeably |
| ill-formed | Of a program: at least one error names something in it. No assembly is written for an ill-formed program |

Diagnosing a construct is the language's defined answer for it. Annex A lists every diagnosed construct.

### 2.2 Behaviour

| Term | Meaning |
|---|---|
| undefined | The machine decides what happens and nothing reports it. No diagnostic precedes it and no runtime error follows it |
| unspecified | More than one behaviour is permitted and which one occurs is not documented. A program whose result depends on the choice is unspecified |
| fault | A runtime event, not a diagnostic. The chip raises a named error, stops partway through the tick, and loses every write it had left to make. `ShiftUnderflow`, `ShiftOverflow`, `StackOverFlow`, `StackUnderFlow` and `Unknown` are the faults this document names |

A fault is retried on the following tick. Nothing about a fault is visible at compile time, and nothing about it is reported to the program.

### 2.3 Value windows

Three ranges are referred to by name. Each is a property of the machine, and 4.2.3 and 4.7 give their rules.

| Window | Bound | What it governs |
|---|---|---|
| The exact-integer window | −2^53 to 2^53, inclusive | What a `long long` denotes, and what `+`, `-`, `*`, `/` and `%` may fold to |
| The bitwise operand window | −(2^53 − 1) to 2^53 − 1 | What `&`, `\|`, `^`, `~`, `<<` and `>>` may be handed |
| The narrowing window | −2147483648 to 2147483647 | What the chip converts a prefab hash, a name hash, a device, a slot index, a mode and a reagent hash to |

Two further bounds are unnamed: a bitwise operator answers within −2^53 to 2^53 − 1, and the conversion carrying a bitwise operand faults outside −2^63 to 2^63 inclusive.

## 3 Lexical elements

### 3.1 Source text

Source text is ASCII, except inside a comment, a string literal, and a character literal, where UTF-8 is admitted. A multi-byte rune in a character literal reaches semantic analysis and is diagnosed there rather than at the byte level; see 3.7. Positions count bytes, so a tab is one column and a multi-byte rune is as many columns as it has bytes.

Whitespace is space, tab, carriage return, vertical tab, form feed, and newline. It separates tokens and is otherwise insignificant. Tokens are formed by maximal munch: `a+++b` is `a`, `++`, `+`, `b`.

### 3.2 Comments

`//` runs to the end of the line. `/* */` does not nest and may span lines; an unterminated one is an error. Comment introducers inside a literal are text.

### 3.3 Keywords

Every keyword is reserved and can never be an identifier. Keywords the language diagnoses are still scanned as keywords so the compiler can name the construct rather than answer "unexpected token".

| | |
|---|---|
| Accepted | `bool` `break` `case` `const` `constexpr` `continue` `default` `dev` `do` `double` `else` `false` `for` `if` `long` `return` `switch` `true` `void` `while` |
| Diagnosed | `alignas` `alignof` `auto` `char` `enum` `extern` `float` `goto` `inline` `int` `nullptr` `register` `restrict` `short` `signed` `sizeof` `static` `struct` `thread_local` `typedef` `typeof` `typeof_unqual` `union` `unsigned` `volatile` `_Alignas` `_Alignof` `_Atomic` `_Generic` `_Noreturn` |
| Reserved | `static_assert` `_BitInt` `_Bool` `_Complex` `_Decimal32` `_Decimal64` `_Decimal128` `_Imaginary` `_Static_assert` `_Thread_local` |

A diagnosed word spells a construct, and the error names it: `alignas` and `_Alignas` draw the same sentence about alignment, `typeof` and `typeof_unqual` the one about a macro type specifier, `thread_local` the one about a storage class, and `_Atomic` and `_Noreturn` the one about a qualifier.

MicroC spells nothing with a reserved word, so the diagnostic quotes the word rather than naming a construct. They are reserved because C reserves them: a program declaring something called `static_assert` is not the valid C23 translation unit 1.2 requires.

A word C leaves alone is an ordinary identifier, whatever a compiler makes of it. `asm`, `offsetof`, `noreturn` and `NULL` are an extension, a library macro, an attribute and a macro; none is a C23 keyword, so each may name a variable or a function.

Clause 4.1 gives the type each accepted type keyword names. Clauses 4.2.1 and 4.3 give why `int` and `float` are diagnosed rather than accepted.

### 3.4 Identifiers

#### 3.4.1 Form

`[A-Za-z_][A-Za-z0-9_]*`, case-sensitive, ASCII only, unlimited length.

#### 3.4.2 Reserved spellings

Device pins and the machine names an intrinsic argument carries are written as ordinary identifiers such as `d0`, `db`, `Pressure` and `Average`, and denote something only where a device or a machine name is wanted. Nothing consults scope in those positions, so the spellings are reserved: no declaration, whether a global, a local, a parameter, or a function, may take one. The diagnostic names which set the identifier fell into.

| Reserved | Why |
|---|---|
| Anything beginning `__ic_` | Kept for intrinsics. It also sits in C's reserved identifier namespace, which is where an implementation-provided prelude belongs, so nothing diagnoses it there even under `-pedantic-errors` |
| `db`, and `d0` through `d5` | A device position resolves the pin from the spelling alone |
| The machine names: 358 logic types, 33 slot types, 5 batch modes, and 4 reagent modes, one spelling each and 400 in all | A named intrinsic operand resolves from the generated tables alone |
| `pi`, `tau`, `deg2rad`, `rad2deg`, `epsilon`, `rgas` | They are predeclared, so a declaration would be a redefinition |
| `ic10_logic`, `ic10_slot`, `ic10_batch`, `ic10_reagent` | The generated C prelude gives these names to the four operand types, so a declaration is a redefinition in the C a program is read as |

The `__ic_` reservation binds uses as well as declarations. Naming an `__ic_` identifier that is not an intrinsic is diagnosed as a misspelled intrinsic rather than as an undeclared name, since the prefix admits nothing else.

#### 3.4.3 Predeclared machine constants

Six machine constants are predeclared in a scope enclosing the file, each a `constexpr double`.

| Name | Note |
|---|---|
| `pi`, `tau`, `rgas` | The machine's own values |
| `deg2rad`, `rad2deg` | Float-precision literals widened to double rather than pi/180 and 180/pi, so a program folding the division itself computes a different number from the chip |
| `epsilon` | The smallest subnormal, not a comparison tolerance |

The machine carries three further constants, a NaN and the two infinities, and MicroC predeclares none of them. A program spells the value it means, or takes one out of the arithmetic that produces it; see 4.6.

### 3.5 Integer literals

#### 3.5.1 Forms and value bound

| Form | Syntax |
|---|---|
| Decimal | `0`, or `[1-9][0-9]*` |
| Hexadecimal | `0x` or `0X`, then one or more of `[0-9a-fA-F]` |

A literal must be a value the machine holds exactly: a magnitude of at most 2^53. One past that is diagnosed where it is written rather than folded to the nearest double, since the folded value is a different number from the one the program wrote.

Any identifier character butted against a literal is an invalid suffix. This covers C's `u`, `l`, and `ll` suffixes, C's `f` float suffix, and C23 binary literals alike: `0b1010` is the literal `0` with the suffix `b1010`.

#### 3.5.2 The C type of an integer literal

A literal denotes a `long long` wherever it stands alone, but the type C gives it is not always that, and the difference decides what arithmetic over it computes. C gives an integer constant the first type from a list that represents it, and which list depends on the spelling.

| Spelling | Types searched | Example |
|---|---|---|
| Decimal | signed only: `int`, then the 64-bit signed type | `2147483648` is 64-bit signed, so `-2147483648` is negative |
| Hexadecimal | unsigned interleaved: `int`, `unsigned int`, then the 64-bit signed type | `0x80000000` is `unsigned int`, so `-0x80000000` is not negative in C |

That model is target independent. C names `long` between `int` and the 64-bit type, and `long` is 64 bits on this target and 32 bits under LLP64, but a 32-bit `long` has the range of `int` and never takes a place on either list that `int` or `unsigned int` has not already taken. Every target whose `int` is 32 bits and whose `long long` is 64 gives a constant the same width and signedness, so a program means the same thing to a Windows editor as to a Linux one. That independence covers the type C gives a literal. It does not extend to the type a declaration names; see 4.2.1.

Clause 6.7.3 gives what follows from the type.

### 3.6 Floating-point literals

| Form | Syntax |
|---|---|
| Fraction | digits, a `.`, and digits, with either side omissible but not both |
| Exponent | either of the above, then `e` or `E`, an optional sign, and one or more digits |

`1.5`, `.5`, `2.`, and `1e5` are all valid and all have type `double`. A literal too large for a double is an error; one that underflows reads as zero, matching what the same expression gives at run time.

Exponent notation is admitted even though the chip's own number parser reads none: the emitter always writes a decimal expansion, so what the source may spell is independent of what a line may hold.

### 3.7 Character literals

`'c'` holds one ASCII character. Its value is that character's code point, and its type is `long long`, since there is no `char` type for it to have. A character written literally must be 0 to 127. A wider one is diagnosed, because C has no value to give it that agrees: the code point does not fit the type a character literal has there.

| Escape | Value |
|---|---|
| `\a` `\b` `\f` `\n` `\r` `\t` `\v` | 7, 8, 12, 10, 13, 9, 11 |
| `\\` `\'` `\"` `\?` | 92, 39, 34, 63 |
| `\x` then one or more hex digits | that value, which must be 0 to 255 |
| `\` then one to three octal digits | that value, which must be 0 to 255 |

An unknown escape, an empty literal, a literal holding more than one character, and a literal a newline or end of file interrupts are all errors.

A numeric escape above 127 is the one place a character literal diverges from C, and nothing diagnoses it: `'\xe9'` is 233 here and −23 there, since plain `char` is signed on this target and C narrows the escape into one. Capping escapes at 127 is the only rule that would make the two agree, and a numeric escape names a byte instead.

In a constant expression a character literal folds as C's `int`, not as the `long long` its MicroC type is; see 6.7.3.

### 3.8 Boolean literals

`true` and `false` have type `bool` and values 1 and 0.

### 3.9 String literals

`"..."` with the escapes of 3.7, each escape stored as one byte and each literal rune stored as its UTF-8 bytes. A newline before the closing quote is an error. Adjacent literals are not concatenated.

A string literal is valid only as the argument of `__ic_hash`, which takes the CRC-32 of exactly those bytes at compile time. No string object is created, no array is allocated, and no other position accepts one.

### 3.10 Punctuators

```
( ) [ ] { } , ; : :: ?
+ - * / % ~ ! & | ^ << >>
&& || ++ --
= += -= *= /= %= &= |= ^= <<= >>=
== != < <= > >=
. -> ...
```

`.`, `->`, and `...` are scanned so that member access and varargs can be named when diagnosed. `::` appears only inside an attribute; nothing else in the language puts two colons together.

### 3.11 Diagnosed lexical forms

| Form | Diagnostic |
|---|---|
| `#` anything | preprocessor directives are not supported in MicroC |
| `0` followed by more digits | octal literals are not supported in MicroC; write it in decimal or hexadecimal |
| `12u`, `0b1010`, `1.5f` | invalid suffix on the literal |
| `1e400` | the literal is larger than a double holds |

## 4 Types

### 4.1 The type set

`long long`, `bool`, and `double` are distinct for type checking, and `long` is a second spelling of `long long` rather than a fourth type. All three occupy one machine value, an IEEE double. The distinction decides which operations apply and where a value is rounded; a register holds the same thing under any of the three.

`dev` names a device pin and has no runtime representation; see 4.10. `void` is a function result type only; see 4.11.

There is no `char`: with no byte addressing it would be `long long` under another name. There is no 32-bit floating-point type; the machine has no 32-bit type of any kind.

### 4.2 The integer type

#### 4.2.1 Spellings

The integer type has two spellings, `long long` and `long`, and they name one type: the machine holds every integer exactly to 2^53 and neither spelling changes that.

| Rule | |
|---|---|
| A diagnostic quotes the spelling written where the type came from | The declaration, the cast target, or the `long long` the prelude's own prototype writes for an intrinsic argument |
| An operator answers in its integer operand's spelling | The left one, where it has two |
| The file's own spelling stands in where nothing wrote one | As for a literal or a pointer difference: the first spelling the file writes, and `long long` where it writes none |

`long` is 32 bits under LLP64, so a program using the full range means something narrower to a Windows toolchain.

`long long int x` and `long int x` are both diagnosed with the trailing word named as the one to delete. `long long long` is diagnosed at the third word. Every other combined spelling, `unsigned long long`, `signed long` and `int long` among them, draws one sentence naming the whole spelling and listing the types MicroC has, since with no `typedef` nothing else becomes one.

`int` is diagnosed outright rather than accepted as a narrower integer: C's `int` is 32 bits on every implementation this targets, so a program naming a value past 2^31 would mean something different to the C toolchain reading it.

#### 4.2.2 Properties

| Property | Behavior |
|---|---|
| Range | Exact to ±2^53 inclusive, not 64 bits. See 4.2.3 |
| Non-numbers | A register holds one double whatever the type is called, so a `long long` also holds ±∞ and NaN. A cast of one, an empty `Maximum` batch read, and `/` or `%` by zero all put one there, and a device property is a double too, so a read hands back whatever it holds. `<`, `<=`, `>`, `>=` answer false for a NaN operand as they do for a `double`. See 4.6 |
| Overflow | Undefined. Values do not wrap; they follow double arithmetic toward infinity |
| Division | Truncates toward zero, matching C99 |
| Remainder | Sign follows the dividend, matching C. Synthesized, because the machine's `mod` adds the divisor back to a negative remainder |
| Bitwise and shift | `long long` only. Each operand converts to a signed 64-bit integer and the answer converts back, and neither conversion is the identity across the whole range. See 4.2.3 |
| Zero | One zero, C's. `*`, `/`, unary `-` and a cast of a `double` are corrected where IEEE would answer −0. See 4.5 |

#### 4.2.3 The exact-integer window

`long long` is not 64 bits. Every value lives in an IEEE double, so the type is exact to ±2^53. What a value survives once it reaches a bitwise or shift instruction is narrower still, and the two windows differ by exactly one value.

| Window | Governs | Bound |
|---|---|---|
| Representable | What a `long long` denotes, and what `+`, `-`, `*`, `/`, `%` may fold to | −2^53 to 2^53, inclusive |
| Bitwise operand | What `&`, `\|`, `^`, `~`, `<<`, `>>` may be handed | −(2^53 − 1) to 2^53 − 1 |
| Bitwise result | What those operators answer with | −2^53 to 2^53 − 1 |
| Conversion range | Outside it a bitwise or shift operand faults the chip | −2^63 to 2^63, inclusive |

The machine reads a bitwise operand through a conversion that reduces modulo 2^53. It is the identity strictly inside ±2^53 and sends both ends to zero, so `x & -1` answers 0 for an `x` of 2^53 even though a register holds 2^53 unchanged. It reads the answer back through a different conversion, which keeps 53 bits and takes the sign from bit 53, so a left shift landing on 2^53 answers −2^53.

`9007199254740992` is a `long long` the language admits; `9007199254740992 & -1` is not.

Outside ±2^63 the operand conversion faults instead of converting: `ShiftUnderflow` below the range and `ShiftOverflow` above it. Both infinities are outside the range. A NaN is inside it, so it does not fault: the conversion computes `(long)(d mod 2^53)`, and the machine's double-to-long cast answers −2^63 for a NaN, so a NaN reaches the instruction as −2^63. Masked against any operand the language admits it still answers zero, since only bit 63 is set; `~` and `>>` do not. `~` over a NaN answers −1, and `>>` answers −2^53.

The count operand of a shift goes through a different conversion, bounded to the signed 32-bit range: a count below −2^31 raises `ShiftUnderflow` and one above 2^31 − 1 raises `ShiftOverflow`. Inside that range the chip shifts by the low six bits of the count; see B.1.

Those are the rules for an emitted operation. An operation whose result nothing reads is not emitted: the optimizer deletes it and the fault goes with it, so a shift assigned to a variable no later statement mentions neither runs nor faults.

These are diagnosed at compile time rather than emulated:

| Diagnosed | Example |
|---|---|
| A constant outside the representable window | `9007199254740993`, `(long long)1e18`, `9007199254740992 + 8` |
| A bitwise or shift operand at exactly ±2^53 | `9007199254740992 & -1`, `~9007199254740992`, `9007199254740992 >> 1` |
| A left shift whose answer reaches 2^53 | `(long long)1 << 53` |
| A shift count outside the width of the C type of its left operand | `1 << 32`, `1 << n` |
| An integer fold whose answer depends on the C type of its literals | `2147483647 + 1`, `-1 < 0xFFFFFFFF` |
| An optimizer-formed bitwise operation over a value that may be non-finite | none known: the rewrites this pipeline makes leave one-bit values the guard has nothing to diagnose |
| An operand the chip narrows, not shown to lie in the signed 32-bit range | `__ic_store_batch((long long)__ic_load(in, Setting), On, 1.0)`, `__ic_store_batch(3000000000, On, 1.0)` |
| An array bound below 1 or above 512 | `long long a[0]`, `long long a[513]` |
| A negative constant slot index | `__ic_load_slot(pump, -1, Occupied)` |

A slot index has no upper bound here. How many slots a device declares is a property of the device, and the pin carries one only where a declaration names a prefab, so an index above what the device holds is emitted and faults on the chip; where the prefab is declared it is warned about. See B.1.

No refusal covers a value the program computes at run time. A device read reaching 2^53, or an infinity reaching a bitwise operator through a variable, is undetected: the operator is emitted and the machine decides.

### 4.3 The floating type

| Property | Behavior |
|---|---|
| Range | The whole of an IEEE double, infinities and NaN included |
| Division | A plain division. `7.0 / 2.0` is 3.5 |
| Comparison | The four orderings are false for a NaN operand and `!=` is true for one, matching both C and the machine |
| Zero | IEEE's, kept throughout. Nothing corrects `-0.0` or `0.0 * -1.0` |

`double` exists because nothing a chip reads is an integer: a temperature is 293.15 K, and a pressure, a ratio and a dial reading are fractions. Writing such a value as a `long long` would round it at every cast, and the type distinction exists to prevent that. A register holds a double either way.

`float` is diagnosed rather than treated as a spelling of `double`. Every register and memory slot holds one whole double, so there is no 32-bit type for it to name, and accepting it would advertise a precision the machine does not have.

### 4.4 bool

A `bool` holds 0 or 1. Clauses 5.4 and 6.4 give which operations produce one, which conversions normalize one, and what an operator does with one.

### 4.5 Zero

C has one zero and IEEE has two. Four `long long` operations answer the one C cannot name, and each is corrected so the value is C's:

| Operation | Where the sign would come from |
|---|---|
| `a * b` | IEEE signs a zero product by the operands disagreeing, so `-5 * 0` is −0 |
| `a / b` | The quotient is signed the same way, so `0 / -3` is −0, as is every quotient whose magnitude rounds away |
| unary `-` | A sign flip answers −0 for a zero operand |
| `(long long)d` | The cast is signed by the operand alone, so every reading in (−1, 0) truncates to −0 |

`+`, `-`, `%` and the bitwise operators need no correction. Neither addition nor subtraction produces a −0 from operands that are not already one, the remainder's closing subtraction leaves a zero remainder positive, and a bitwise answer comes back out of an integer.

The correction is observable. The two zeroes are one value to every comparison and two different values to a device, which compares bit patterns, so a `long long` that kept the sign would agree with C everywhere except at a device.

### 4.6 NaN and infinities

A NaN compares false against everything under `<`, `<=`, `>`, and `>=`, so a condition and its negation are both false and `!(a >= b)` is true where `a >= b` is false. `==` and `!=` are ordinary: a NaN is equal to nothing, itself included. A `long long` holds that behavior as a `double` does, since a register holds one double whatever the type is called. The optimizer applies none of the identities that hold of an integer and not here, to any of the three scalar types: that `a - a` is zero, that `a * 0` is zero, that `a / a` is one, and that an ordered comparison and its negation are complements. Infinities behave the same way, and one reaches a program with no NaN anywhere in it.

A bitwise or shift operator treats the two differently, since neither value is an integer. A NaN is not outside the ±2^63 the operand conversion accepts, so it converts rather than faulting, and reaches the instruction as −2^63. An infinity is outside the range, so the instruction faults and the chip stops partway through the tick.

| Sources | |
|---|---|
| Outside a domain of finite values | `__ic_sqrt` of a negative, `__ic_log` of a negative or of zero, `__ic_asin` and `__ic_acos` past 1, `__ic_pow` of a negative base under a fractional exponent |
| At an infinity | `__ic_sin`, `__ic_cos` and `__ic_tan`, which have no value there; `__ic_lerp`, whose infinite endpoint less its other endpoint, or times a zero weight, is a NaN |
| Past what a `double` holds | `__ic_exp` and `__ic_pow` overflow to an infinity |
| A batch read matching no device | `Average` answers NaN and `Maximum` negative infinity |
| `double` arithmetic | `0.0 / 0.0` outright; `/` by zero answers an infinity; `+`, `-` and `*` reach a NaN out of the infinities |
| `long long` `/` and `%` | The machine has no integer division. It divides doubles, so `a / 0` is an infinity and `a % 0` is that infinity times zero, which is a NaN. Both are undefined in C, and the value they leave in the register is what the program then computes with |

Every other intrinsic answers a finite `double` for every finite `double` a register holds, or faults instead. `long long` `+`, `-` and `*` are not sources, since a finite sum, difference or product stays finite short of overflow, which is undefined, but they carry what reaches them: `m - m` where `m` came from an empty `Maximum` batch read is NaN rather than zero.

`__ic_isnan` is the direct test. C's `a != a` also finds a NaN, in a `long long` as in a `double`, but it is a spelling the optimizer is entitled to fold, while `__ic_isnan` is the machine's own instruction.

### 4.7 The narrowing window

A prefab hash, a name hash, a device, a slot index, a batch or reagent mode and a reagent hash are not read as the double the register holds. The chip converts each to a signed 32-bit integer, and outside the narrowing window that conversion neither stops the chip nor carries the value into range: the line runs against an integer the program never computed, and nothing on the chip reports the substitution. A NaN, both infinities, and every value below −2147483648 or above 2147483647 are outside the window; being finite is not enough, since `3e9` reaches the operand as something unrelated to it just as a NaN does.

Which integer it lands on is the runtime's choice rather than the language's, and a program cannot depend on it. The conversion's answer for a value it cannot represent decides whether the line reaches no device, every device the world left unnamed, or a slot below an array. A mask or a cast one instruction earlier moves it between those without the source saying anything.

An operand at one of those positions is diagnosed unless the compiler can read a range for it and that range is inside the window. A literal states itself, a `__ic_hash` of a name states the hash, a comparison and a slot index state their own small ranges, and a two-sided range test states what it admits. A range carries through `+`, `-`, `*`, `/`, `%`, unary `-`, `<<`, a cast, a `?:`, and `__ic_abs`, `__ic_round`, `__ic_trunc`, `__ic_ceil` and `__ic_floor`, and through no other form: `__ic_sgn`, `__ic_min` and `__ic_max` state nothing. The test is written in the conditional expression that produces the operand:

```c
long long hash = (v > -2147483648.0 && v < 2147483648.0) ? (long long)v : __ic_hash("StructureWallLight");
```

The guard must have all four properties below.

| Requirement | What fails without it |
|---|---|
| The test and the value in one expression the compiler still sees as a conditional, which an `if` over a single assignment also becomes | An arm the optimizer leaves as a branch reaches the operand through control the compiler does not read |
| Bounds inside the window | `(v > -1e300 && v < 1e300)` admits values the chip still narrows |
| Both arms inside the window, the arm the test fails on included | The failing arm reaches the operand as much as the arm the test admits |
| Ordered comparisons on the arm the value fills | `!(v <= -2147483648.0 \|\| v >= 2147483648.0)` is not the same test: a NaN satisfies neither comparison, so the negation admits it |

`>=` and `<=` state a bound the same way `>` and `<` do, and a conjunction of more than two comparisons states every bound in it.

Arithmetic after the test is read rather than diagnosed. A range that leaves the window is diagnosed. A guard and then a scale compiles whether the scale is written inside the arm or applied to the whole conditional, and so does a quotient whose divisor's own range excludes zero. A scale that carries a ±1000 range out to ±3e9 is diagnosed for leaving the window. Division states no range otherwise, since a divisor that could be zero answers with an infinity.

The same guard written as an `if` compiles wherever the optimizer speculates the arm, which it does for one assignment, so `if (v > -2147483648.0 && v < 2147483648.0) { h = (long long)v; }` reaches the operand as the conditional expression above and not as control. An arm the optimizer will not evaluate unconditionally stays a branch, and a branch states nothing. A call is the usual such arm: `__ic_abs`, `__ic_round`, `__ic_floor` and the rest inside an arm leave the test standing as control. A program bounds the value in a conditional expression first and shapes the bounded result afterward, which is read.

Neither `__ic_isnan` nor a mask silences the refusal, and a mask makes the outcome worse. It converts the value rather than testing it, and the mask answers zero over a NaN, which is the batch of every device whose prefab is unset, so a line that reached no device becomes one that writes to all of them; over an infinity the mask stops the chip outright. `__ic_isnan` says nothing about the magnitude, and nothing carries its answer back to the value that was tested.

A left shift is read only where its distance is read and lies in 0 through 63, the whole of the window the machine shifts by. Outside it the chip shifts by the low six bits of the distance rather than by the distance, so `-24` shifts left by 40 and `64` shifts by none, and a distance a guard holds below zero is diagnosed for that reason rather than read as a small scale:

```c
long long below = (w > -3.0 && w < -1.0) ? (long long)w : -2;
__ic_store_batch(m << below, On, 1.0);                   /* diagnosed: the chip shifts left by 62 */
long long above = (w > 1.0 && w < 3.0) ? (long long)w : 2;
__ic_store_batch(m << above, On, 1.0);                   /* compiles */
```

No refusal covers a value no range reading bounds, and a loop counter is the usual such value. A counter is held by the loop's own exit test, which is control rather than arithmetic, so a counter a loop runs past 2^31 reaches the operand unremarked and so does anything computed from one. A shift by a counter is the same gap one shift earlier, since `1 << i` leaves the window at `i` of 31, and it is carried for the same reason: diagnosing it would diagnose every loop that names a batch by a bit position. The bitwise operators are the same gap in another place: their result is whatever the chip's own ±2^63 conversion produced, a window 2^32 times wider than this one, so `x & m`, `x | m`, `x ^ m` and `x >> k` over a value the compiler could not bound are carried through rather than read.

### 4.8 Arrays

One-dimensional, with a bound that is a constant expression of type `long long`, at least 1, and at most 512. A zero-length array is diagnosed: a pointer always designates an object, so every array has a first element. Each element occupies one of the 512 slots; nothing packs. A second dimension is diagnosed. A flat array indexed as `a[y * width + x]` performs the address arithmetic a second dimension would have generated.

A bound above 512 is diagnosed at the declaration: 512 is the whole data region, so no program lays out more than that whatever else it declares. The bound a program can afford is smaller and is not known until layout: the same slots hold every global, every other array, every address-taken local, and the call stack, and instruction selection reports the one that does not fit; see 11.4.

| Rule | |
|---|---|
| An array name used as a value decays to a pointer to its first element | `&` applied to an array name is diagnosed, since a pointer to an array is not expressible |
| An array may not be assigned | Assignment moves one machine value, and an array is as many as it has elements |
| An element may not have type `void` or `dev` | Neither occupies a slot |
| A function may not return an array | A result reaches the caller in one register |
| A bound is a constant expression | A bound that names a non-`constexpr` object is diagnosed as such, and C's `[*]` variable-length spelling is diagnosed by name |

### 4.9 Pointers

A pointer is an index into the 512-slot space at runtime. There is no null pointer: a pointer always designates an object. C23's `nullptr` is diagnosed by name.

A pointer expression must trace at compile time to exactly one object: a named variable, an array, or a parameter. A pointer parameter is bound to its own object from the start, so assigning it the address of something else inside the body is a merge like any other.

| Permitted | Diagnosed |
|---|---|
| `&x`, `&a[i]` | any expression that may yield a pointer to either of two objects |
| an array name decaying | `cond ? &a : &b` |
| arithmetic within one object, a scalar being an array of one | a pointer variable or parameter assigned different objects on different paths |
| a pointer parameter, an array parameter that decayed, and the difference of two pointers into one object | comparing or subtracting pointers into different objects |
| forming the address one past the last slot, which `a + n` and `&a[n]` both spell | a constant step or subscript that leaves the object |

Reading or writing through the address one past the last slot is diagnosed where the expression itself settles the slot, which covers `*(a + n)`; `*&E` cancels the allowance the `&` gave, so `*&a[n]` is diagnosed where `&a[n]` stands. Through a pointer object the address survives into the optimized program, and the verifier below diagnoses the access wherever the read or write survives optimization. Where the object is dead the whole access is deleted and nothing checks it.

The check runs twice. Analysis diagnoses the merges the source writes, and a verifier repeats it over the optimized program, since optimization can introduce a pointer phi or a select the source never wrote. A program can therefore be diagnosed for a merge it does not visibly contain; the diagnostic names the source construct responsible.

### 4.10 dev

#### 4.10.1 Declaration

A `dev` names a device pin. It has no runtime representation and no value: the chip resolves a device position when it assembles the line and validates nothing, so the operand there has to be a literal. Every `dev` folds to a constant before any instruction is selected.

| Rule | Reason |
|---|---|
| A `dev` object is `const` or `constexpr` and carries an initializer naming a device | It stands for one pin for the whole program |
| Its initializer is `db`, `d0` through `d5`, or another `dev` object | Those are the spellings a device position takes |
| A `dev` may not be assigned, negated, compared, cast to, or cast from | It is a name, not a number |
| There is no array of `dev`, no pointer to one, and no function returning one | A device occupies no memory slot, and a result reaches a caller in a register the chip does not read as a device |

`d6` through `d9` are not device spellings in MicroC. The chip's assembler resolves a single digit, so those four assemble on a hand-written line and then fault every tick indexing past a six-socket array; MicroC has no spelling that reaches one.

#### 4.10.2 The prefab attribute

A declaration may say which prefab the pin is wired to, written as `[[ic11c::prefab("PrefabName")]]` ahead of the specifiers. The attribute makes an access through the pin checkable against the device roster.

C23 requires an implementation to ignore an attribute in a namespace it does not know, so the declaration means exactly what it means without one and a C toolchain reads the program unchanged. The compiler is therefore the only thing that reads it, and nothing about the emitted program changes: no instruction verifies the claim, and the chip reaches whatever the housing was actually wired to.

| Rule | Reason |
|---|---|
| The attribute names one prefab, written as a string literal | It is the same name a `__ic_hash` in a batch operand would spell |
| It goes on a `dev` declaration, ahead of `const` or `constexpr` | That is the position C appertains an attribute to the declaration in, and a declaration is where a pin gets a name. One written after a specifier is diagnosed for its position |
| It may not go on a `dev` parameter, or on a function | A parameter names whichever pin each call site passes, and a function names no pin |
| One attribute, and only this one | A second attribute and any other spelling are each diagnosed by name |
| Two declarations of one pin may not name two prefabs | One housing position reaches one device |
| A name this game build ships nothing under is warned about, and nothing through that pin is checked | The roster is one pinned build, and a later one may ship the name |
| A prefab on `db` that the roster says holds no programmable chip is warned about | `db` is the housing this chip is inserted into, so a thing with nowhere to put a chip cannot be it |

The claim covers the whole program rather than the scope it is written in: what is wired to `d0` is one fact about the world, so an access through the bare `d0` spelling elsewhere is judged by it too.

#### 4.10.3 dev parameters

A `dev` parameter is admitted, so one helper body can drive several similar devices. It works because the body is spliced into every call, which substitutes the device the site wrote.

A function that needs a real out-of-line call cannot take one, and recursion is the only thing that forces one, since a body cannot be spliced into itself. A recursive function with a `dev` parameter is diagnosed at the parameter, naming why. A `dev` parameter may be passed on to another `dev` parameter, and may not initialize a `dev` object, whose device is fixed for the program rather than per call.

### 4.11 void

`void` is a function result type only. A `void` variable, parameter, array element, or cast is diagnosed, and so is a pointer to `void`, wherever one is written.

## 5 Conversions

### 5.1 The conversion table

| From | To | Effect | How |
|---|---|---|---|
| `long long` | `bool` | 0 becomes false, anything else true | implicitly |
| `bool` | `long long` | 0 or 1 | implicitly |
| `long long` | `double` | widens, exactly to 2^53 | implicitly |
| `bool` | `double` | 0.0 or 1.0 | implicitly |
| `double` | `long long` | truncates toward zero | by cast only |
| `double` | `bool` | 0.0 becomes false, anything else true, a NaN included | by cast only |
| array | pointer to element | decay | implicitly |

### 5.2 Contexts

Implicit conversions happen on assignment, initialization, argument passing, return, and use as a condition.

A cast targets `long long`, `bool`, or `double` and takes any of the three. A cast to a pointer type, to `void`, or to `dev` is diagnosed, and so is a cast of a pointer or of a `dev`. A qualifier inside a cast is diagnosed with its own sentence: a qualifier says how a declaration is stored, and a cast declares nothing.

### 5.3 No implicit narrowing

Nothing narrows implicitly. A `double` reaches a `long long` only through a cast, which is stricter than C: the distinction between the two types exists to prevent silent truncation, so the line that wants a truncation writes it. The same refusal governs mixing `bool` and `long long` without a written conversion; see 6.4.

### 5.4 bool normalization

A `bool` value is 0 or 1. The comparison operators, `!`, `&&`, and `||` produce a `bool`. Conversion to `bool`, by cast, assignment, initialization, argument passing, return, or use as a condition, normalizes.

No other operation normalizes, so a `bool` object read before it is assigned need not be 0 or 1. A `bool` in the data region reads as zero, which is `false`. One in a register has no value at all, and reading it before it is assigned is diagnosed; see 7.5.

## 6 Expressions

### 6.1 Precedence and associativity

Tightest first. Every binary operator is left associative. This is C's precedence unchanged.

| Level | Operators | Associativity |
|---|---|---|
| 1 | `f()` `a[i]` `x++` `x--` | left |
| 2 | `++x` `--x` `+x` `-x` `!x` `~x` `&x` `*x` `(type)x` | right |
| 3 | `*` `/` `%` | left |
| 4 | `+` `-` | left |
| 5 | `<<` `>>` | left |
| 6 | `<` `<=` `>` `>=` | left |
| 7 | `==` `!=` | left |
| 8 | `&` | left |
| 9 | `^` | left |
| 10 | `\|` | left |
| 11 | `&&` | left |
| 12 | `\|\|` | left |
| 13 | `?:` | right |
| 14 | `=` and the compound assignments | right |

### 6.2 Operand types

An operator converts its operands in one case: a `long long` operand widens where the other is a `double`. Everything else meets only at the implicit conversion sites of 5.2.

| Operator | Operands |
|---|---|
| unary `~` | a `long long` |
| unary `+` `-` | a `long long` or a `double` |
| `%` `<<` `>>` `&` `^` `\|` with their compound assignments | `long long` |
| `*` `/` `*=` `/=` | two `long long`s or two `double`s |
| `+` `-` `+=` `-=` | two `long long`s, two `double`s, or a pointer and a `long long`; `-` also takes two pointers into one object, yielding the `long long` distance between them |
| `<` `<=` `>` `>=` | two `long long`s, two `double`s, or two pointers into one object |
| `==` `!=` | two `long long`s, two `bool`s, two `double`s, or two pointers into one object |
| `!` `&&` `\|\|` | conditions |
| `?:` | a condition, then two arms of one type, which is the type of the result |
| `++` `--` | a `long long`, a `double`, or a pointer object |
| `[` `]` | a pointer or an array, indexed by a `long long` |
| `=` | a value the target's type admits |

### 6.3 Compound assignment and the conditional operator

A compound assignment yields the target's own type, so `d *= n` widens the `long long` and `n *= d` is diagnosed: the result would have to narrow where nothing said to.

Two pointer arms of `?:` must have identical pointee types, `const` included, since the operator yields one arm's type and has no destination to justify the asymmetry assignment allows. A top-level qualifier is dropped, so a `const long long` arm pairs with a `long long` one.

### 6.4 bool under an operator

`bool` mixes with nothing under an operator, which is stricter than C. Only a conversion to `bool` normalizes (5.4), so `b++`, `b + 1` and `n == b` are each diagnosed. `long long` and `double` carry no such invariant and their widening is exact, so `t * 2` is admitted where `b + 1` is not. `==` and `!=` are the only comparisons a `bool` takes: ordering two of them is diagnosed, since `<` between two truth values asks nothing.

### 6.5 Order of evaluation

`&&` and `||` evaluate their right operand only when the left does not decide the result. `?:` evaluates only the branch it selects. Everything else is unspecified: the order in which other operands and function arguments are evaluated is not defined, and a program whose result depends on it is unspecified. A full sequence point falls at each `;` and at each of the three clauses of a `for`.

### 6.6 Conditions

A condition is the controlling expression of `if`, `while`, `do`, and `for`, the first operand of `?:`, and each operand of `!`, `&&`, and `||`. It is a `long long` or a `bool`. A `long long` converts, which normalizes it, so `if (total & kHighBit)` tests the bit and needs no comparison against zero. A pointer is not a condition: there is no null pointer for it to be tested against.

A `double` is not a condition either. Every value a chip reads is fractional, and a test against zero is almost never the comparison such a value wanted, so the comparison has to be written; `(bool)d` says the test against zero was meant.

A switch tag is not a condition; see 8.2.

### 6.7 Constant expressions

#### 6.7.1 Composition

A constant expression is built from integer, floating-point, character, and boolean literals, `constexpr` objects, the machine's own constants among them, and the operators. Function calls, intrinsics, assignment, `++`, `--`, and pointer, array, or device operands are excluded. It is evaluated at compile time, in IEEE arithmetic where a `double` is involved, so a folded value is the number the chip would have computed.

The exclusion of intrinsics covers `__ic_hash`, whose value the compiler does know. It is a call like any other and may stand only where an ordinary expression may: inside a function body, a local initializer included, and not in an array bound, a case label, or a global initializer.

#### 6.7.2 The two forms

Which form a position requires decides whether a `double` may appear in it.

| Form | Required by | A `double` operand |
|---|---|---|
| Integer | An array bound, a case label, the slot index of a slot intrinsic, the initializer of a `constexpr` object of type `long long` or `bool` | Only as a floating literal that a cast converts, as in `(long long)3.5` |
| Arithmetic | A global initializer, an element of a brace initializer, the initializer of a `constexpr double` | Any operand |

So `long long a[(long long)3.5]` is accepted and `long long a[(long long)(3.5 * 2.0)]` is not: the cast's operand there is an expression rather than a literal. Naming a `constexpr double` under the cast is diagnosed for the same reason: `constexpr double kHalf = 0.5; long long a[(long long)kHalf];` is diagnosed while `double scaled = kHalf * 8.0;` is not. The restriction is C's, and it keeps a MicroC program a C23 translation unit. The arithmetic form carries none of it, so a `constexpr double` stays declarable, foldable, and usable as an ordinary value.

#### 6.7.3 Integer folds and the C type

A fold over integers runs in the machine's own 64-bit signed arithmetic. C runs it in the type its usual arithmetic conversions give the operands, which for a pair of literals is often narrower than that, and a fold whose answer depends on which of the two is used is diagnosed. So the compiler admits exactly the constant expressions where C computes the same number, and nothing is folded to a number the editor reading the file would disagree with.

What C type an operand carries into the fold:

| Operand | C type |
|---|---|
| An integer literal | The first from the list of 3.5.2 that represents it |
| A character literal | `int`. There is no `char` type for it to have narrower than that, so `'a' + 2147483647` is diagnosed the way `1 + 2147483647` is |
| A `bool` | `int`, by C's integer promotions, so a `bool` operand does not widen an operation to the machine's own width |
| A shift | The promoted left operand's type alone. The count's type says nothing about it, which is why `(long long)1 << 40` folds where `1 << 40` does not |
| An arm of a folded `?:` | Both arms convert to one type, so the untaken arm's unsignedness can decide whether a negative value in the taken arm survives |

An operator is diagnosed when any of these applies:

| Diagnosed | Example | Why |
|---|---|---|
| An operand the common type does not hold | `-1 < 0xFFFFFFFF` | C converts `-1` to `unsigned int`, where it is 4294967295 |
| A result the common type does not hold | `2147483647 + 1`, `1 << 31`, `0xFFFFFFFF + 1`, `-0x80000000` | C overflows an `int` in the first two and wraps an `unsigned int` in the last two |
| A shift count outside the width of the left operand's type | `1 << 32` | C shifts an `int`, whose width is 32; the count says nothing about the type |
| A left shift of a negative value | `-1 << 1` | C leaves it undefined |
| Division or remainder by zero | `1 / 0`, `1 % 0` | The constant case is an error, whatever the runtime does with the same operation |
| A quotient the common type does not hold | `(-2147483647 - 1) / -1` | The one pair C leaves undefined for division. Cast an operand so C widens the operation too |
| A cast of a `double` constant that is not a value the target holds | `(long long)(1.0/0.0)` | A non-finite or out-of-range operand has no truncation |

Each operator is tested against its own C type rather than the expression being tested against its answer, so a composition of operations that all stay in range for the 64-bit fold is still diagnosed when an intermediate does not fit the type C computes it in. `8 - (-0x80000000)` is 2147483656 in both arithmetics and is diagnosed anyway, and the diagnostic names the negation under it rather than the subtraction: an expression C has no value for has no C answer to agree with, wherever it sits.

A cast on one operand widens the operation, in C as well as here: `(long long)2147483647 + 1` and `(long long)1 << 40` both fold. Writing the constant in decimal is the other way out, since a decimal constant never lands on an unsigned type. `-2147483648` is the spelling `-0x80000000` was reaching for.

The windows of 4.2.3 apply on top of this and are separate from it. `(long long)1 << 60` has a C type wide enough to hold the result and is still diagnosed: a double holds 2^60 exactly, but the conversion a shift result comes back through does not carry it, and the machine answers 0.

#### 6.7.4 Where the fold rules apply

The rules of 6.7.3 run on every expression whose operands are constant, not only where a constant expression is required, so a plain initializer's mistake is caught the same way an array bound's is.

They also reach the operands of an expression that never folds as a whole. A bitwise or shift operator whose other operand is an object is checked against the window of 4.2.3 for the operand that is constant, so `x &= 9007199254740992` is diagnosed where nothing about `x` is known. A compound assignment always lands there, since its left operand is an object.

## 7 Declarations

### 7.1 Declarators

One declaration declares one variable; `long long a, b;` is diagnosed. A declarator names one array or one parameter list and nothing outside it, so a second suffix is diagnosed and a parenthesized declarator, which spells a function pointer, is diagnosed by name.

An array bound may be omitted only on a parameter, where the array decays to a pointer. `()` and `(void)` both declare no parameters. A parameter may go unnamed only in a prototype, and may write `const` but not `constexpr`. Two parameters of one function may not share a name.

An old-style parameter list, which names parameters in the parentheses and types them behind, is diagnosed by name; each parameter's type is written inside the parentheses.

### 7.2 const and constexpr

Both precede the type. `long long const x`, `long long constexpr x`, and `long long *const p` are diagnosed; `const long long *p` qualifies the pointee, leaving `p` itself assignable. A declaration may write both, in either order, which says no more than `constexpr` says alone.

A `const` object requires an initializer and may not be assigned afterward. It is an ordinary object with storage, and its value is not a constant expression: C reads one at run time, so naming a `const` object in an array bound or a case label would denote something different there.

A `constexpr` object is a `const` object whose initializer is itself required to be a constant expression, and whose value is one. It is the only way MicroC names a constant, since there is neither `enum` nor a preprocessor, and a case label such as `case kIdle:` names one. A scalar one occupies no storage: every reference to it becomes the value, so `&` applied to one is diagnosed. That covers the six machine constants of 3.4.3, which are `constexpr double` objects.

Neither is valid on a function. `constexpr` is not valid on a parameter either, whose value is whatever the call site passed.

### 7.3 Initialization

A global's initializer must be a constant expression, and so must a `constexpr` object's wherever it is declared. Every other local's may be any expression.

A brace initializer is valid only as a variable's initializer, and an array requires one: initializing an array from an expression is diagnosed. It contains expressions, never another brace initializer, may carry a trailing comma, and may be empty. For an array it supplies elements from index zero; supplying more elements than the bound is an error, and elements past the last one supplied are not written. For a scalar it supplies exactly one value. A designated initializer is diagnosed by name.

### 7.4 Storage and value before the first write

An object with no initializer is not initialized by its declaration. What an object holds before it is first written, the elements a brace initializer did not supply included, depends on where it is stored, not on how it was declared.

| Storage | Value before the first assignment |
|---|---|
| A global, an array, or an address-taken local, in the data region | Zero. The entry prologue zeroes all 512 memory slots in one instruction before anything else runs |
| Any other local | Nothing. It lives in a register, nothing zeroes registers, and reading it before it is written is diagnosed by 7.5 |

Taking the address of a local moves it into the data region; a local the optimizer keeps in a register has no slot to have been zeroed.

The zeroing happens once, at program start. A data-region local is not re-zeroed when control re-enters the block declaring it, so it is zero on its first read and holds its last value from then on.

### 7.5 Definite assignment

A local that lives in a register must be assigned on every path reaching a read of it. One that is not is diagnosed at the read.

The rule covers exactly the locals 7.4 leaves with no value: a scalar or pointer local whose address is never taken. A global, an array, and an address-taken local are zero on their first read and are not subject to it. A parameter arrives assigned.

The rule is fixed rather than inferred, so whether a program compiles does not depend on how well an analysis reasons:

| Construct | Assigns |
|---|---|
| An initializer, or an assignment through `=` | Always |
| Both arms of an `if`, or every arm of a `switch` carrying a `default` | When every arm assigns, or leaves by `return`, `break`, or `continue` |
| Both arms of a `?:` | When both arms assign, since exactly one of them runs |
| The body of a `do` | Always, since it runs before the condition |
| The body of a `while` or a `for` | Never. The first iteration has not had one, and the loop may not run at all |
| The right operand of `&&` or `\|\|` | Never. It may not be evaluated |
| The post clause of a `for` | Never for the statement after the loop, since it runs after the body |

A loop that cannot end other than by `break` is left with what every `break` carried.

Reading an uninitialized register is undefined in C, and the consequence on this target is not confined to a wrong number: the read reaches the optimizer as an undefined value, which entitles it to fold whatever the read reaches, including deleting the device writes a comparison against it guarded. The program that remains does nothing and reports nothing.

## 8 Statements

### 8.1 General

The statement forms are those of Annex C: a block, the empty statement, a declaration, an expression statement, `if`, `while`, `do`, `for`, `switch`, `break`, `continue`, and `return`.

A statement is valid only inside a function body. One written at file scope is diagnosed as such rather than misread as a declaration.

A function may not be declared inside a block, whether it carries a body or not. C admits a block-scope prototype and gives it block scope; MicroC has one scope for functions and writes every declaration of one in it.

A label is diagnosed by name, there being no `goto` to reach one. A statement expression, C's `({ … })` extension, is diagnosed by name.

### 8.2 switch

A switch body opens with a label: a statement before the first `case` or `default` is diagnosed.

Control must not flow from the body of one arm into the next. An arm with an empty body stacks its label onto the arm below and is the only fallthrough the language permits:

```c
case kIdle:
case kStopped:
    shutdown();
    break;
```

An arm with a non-empty body must terminate, unless it is the last one, which has nothing below it to fall into. The diagnostic names the case whose body falls through.

A switch tag is a `long long` or a `bool` and does not convert, since its case labels carry its own type. Case labels are constant expressions of the tag's type, `long long` for a `long long` tag and `bool` for a `bool` one; nothing converts here either. They must be distinct, at most one `default` may appear, and arms may be written in any order. The switch body is one scope.

Semantic analysis checks these rules, since constant evaluation and reachability run there. The parser accepts any conditional expression as a case label and any arm body.

A switch lowers to a comparison chain, so it costs what the equivalent `if`/`else if` costs.

### 8.3 Jump statements

`break` in a switch leaves the switch. `break` in a loop leaves the loop. `continue` in a switch inside a loop continues the loop.

Misplacing one is diagnosed: a `break` outside a loop and a switch alike, a `continue` outside a loop, and a `case` or `default` label outside a switch.

Control may not reach the end of a function whose result type is not `void`. There is no value to return: the caller would read whatever the result register held.

### 8.4 Termination

A statement terminates when control cannot reach its end.

| Statement | Terminates when |
|---|---|
| `return`, `break`, `continue` | always |
| a block | any statement in it terminates |
| `if` | it has an `else` and both arms terminate |
| `while` | the condition is a constant expression that is never false, and no `break` bound to the loop appears in its body |
| `for` | the condition is absent, or is a constant expression that is never false, and no `break` bound to the loop appears in its body |
| `do` | no `break` bound to it appears, and either the condition is never false or the body terminates with no `continue` bound to it |
| `switch` | it has a `default`, no `break` bound to it appears, every arm with a body terminates, and the last arm has a body |

The test folds constants and reads nothing else, so a function that returns on every path an execution can take may still fail it.

## 9 Program structure

### 9.1 The translation unit

A translation unit is one source file. There is no preprocessor, no include mechanism, and no linker: every function and global lives in that file.

### 9.2 main and execution

A program defines `void main(void)`. Three things are diagnosed, each with its own message: a file that declares no `main`; a `main` with any other result type or with parameters; and a `main` declared as a prototype and never defined, since execution begins in its body. Execution begins there, and every other function is reachable only from it.

The program stops when `main` returns. Nothing wraps back to the start and nothing re-enters the program each tick. The chip resumes where the previous tick left off, so reaching the end of `main` ends the program for good. A control program that has to keep running owns its loop and calls `__ic_yield` inside it; see 10.6.

Globals keep their values across ticks, since chip memory survives ticks, power loss, and chip removal.

### 9.3 Prototypes and definitions

A declaration with no body is a prototype. It declares the function so that a call may precede the definition, as mutual recursion requires. A later definition must agree with it. Repeated prototypes are permitted if they agree; a second definition of one function is diagnosed. A prototype nothing calls needs no definition; one a call names does, since there is no linker and the definition lives in this file or nowhere.

A function name is not a value. Naming one without calling it is diagnosed, as is naming an intrinsic without calling it. Function pointers and varargs are excluded, so every call is direct and fixed-arity.

### 9.4 Scope

Scope is file scope for globals and functions, and block scope for everything else. A name is in scope from its declarator to the end of its block, or to the end of the file for a global or a function; an inner declaration shadows an outer one. Every use therefore follows a declaration: there is no implicit declaration of a function or of a variable, and a prototype declares a function ahead of its definition. Parameters share the function body's scope. Function definitions do not nest.

### 9.5 Recursion

Recursion is permitted. The compiler places no bound on depth: frames share the 512-slot array with data, and exhausting it faults at runtime. A recursion whose frames cannot fit even one activation in the slots left over is diagnosed; a shallower one draws a warning naming how many activations the remaining slots hold.

A recursive function may not take a `dev` parameter (4.10.3), and a local of a recursive function may not need a data-region slot, since one slot is one address for every activation and the inner call would overwrite the outer one's value.

### 9.6 No standard library

There is no standard library and no allocator. Nothing declares `malloc`, `printf` or any other library name, so a call to one is an undeclared name like any other rather than a diagnostic of its own.

## 10 Intrinsics

### 10.1 General

Intrinsics are ordinary identifiers to the grammar and are recognized by name in semantic analysis. A `dev` argument is a device the way 4.10 describes, fixed by the language because the pins are a property of the housing rather than of a table; `logic`, `slot-type`, `batch-mode`, and `reagent-mode` are names from the generated tables. Every one of them, and the `slot` index, resolves at compile time.

An intrinsic may only be called: naming one without a call is diagnosed. An intrinsic call is not a constant expression, `__ic_hash` included, so none of them may appear in a global initializer, an array bound, or a case label.

### 10.2 Device access

| Signature | Meaning |
|---|---|
| `double __ic_load(dev, logic)` | Read one logic value |
| `void __ic_store(dev, logic, double v)` | Write one logic value |
| `double __ic_load_slot(dev, long long slot, slot-type)` | Read a slot property |
| `void __ic_store_slot(dev, long long slot, slot-type, double v)` | Write a slot property |
| `double __ic_load_batch(long long hash, logic, batch-mode)` | Read across all devices of a prefab type |
| `void __ic_store_batch(long long hash, logic, double v)` | Write across all devices of a prefab type |
| `double __ic_load_batch_named(long long hash, long long name, logic, batch-mode)` | Batch read filtered by name |
| `void __ic_store_batch_named(long long hash, long long name, logic, double v)` | Batch write filtered by name |
| `double __ic_load_batch_slot(long long hash, long long slot, slot-type, batch-mode)` | Batch slot read |
| `void __ic_store_batch_slot(long long hash, long long slot, slot-type, double v)` | Batch slot write |
| `double __ic_load_batch_named_slot(long long hash, long long name, long long slot, slot-type, batch-mode)` | Batch slot read filtered by name |
| `double __ic_load_reagent(dev, reagent-mode, long long hash)` | Read a reagent quantity |
| `bool __ic_device_present(dev)` | Whether a pin is connected |
| `long long __ic_hash(string)` | CRC-32 of a string literal, read as a signed 32-bit integer |

Every read answers a `double`, because every value the machine holds is one. Assigning one to a `long long` needs a cast, which is where the truncation the program wanted is written.

There is no name-filtered batch slot write; the machine provides no such instruction.

Every hash, name, mode, device and slot operand passes through the narrowing window of 4.7.

Reading or writing an unconnected pin faults. The fault is retried each tick and clears if the device is later connected, so guarding with `__ic_device_present` chooses a behavior rather than avoiding a hazard.

### 10.3 Machine names

Logic type, slot type, batch mode, and reagent mode names come from the game's own tables. The game marks 23 of the 358 logic types deprecated; each of those still resolves on the chip and compiles unchanged, with a warning naming it.

A machine name means one thing wherever it is written, rather than being resolved against the position it stands in. C has one enumerator namespace per scope, so a language resolving by position could not be read as C.

Fifteen names are carried by two families: thirteen slot types plus the batch modes `Minimum` and `Maximum`. The first family to carry a name takes the bare spelling. The other family spells its own member with a prefix of its own: `LogicType_`, `SlotType_`, `BatchMode_` or `ReagentMode_`, in the order the families claim names. In this build only `SlotType_` and `BatchMode_` carry anything: logic types are the first family and give nothing up, and no reagent mode's name is taken.

`Lock` is the logic type the machine numbers 10 in every position; the slot type it numbers 23 is `SlotType_Lock`. The bare spelling in a position that gave it up is diagnosed, and the diagnostic names the prefixed one. A prefixed spelling exists only where a family gave a name up, so `SlotType_Occupied` names nothing and `Occupied` is the slot type. An over-prefixed spelling is diagnosed the same way, naming the bare spelling to write instead.

### 10.4 Prefab checking

Where a batch form's prefab operand is decided at compile time, the property, the slot index, the slot property and a constant `Mode` are checked against what the game says a completed device of that prefab answers for, and a contradiction warns. A pin-addressed access is checked the same way, but only through a pin whose declaration carries the prefab attribute of 4.10.2. Through an undeclared pin nothing is checked, since which device a pin reaches is a property of the world the chip is placed in rather than of the program.

### 10.5 Batch access

A batch form needs the housing on a data cable. On a housing wired to none, all seven fault before reading the prefab hash, whatever their operands name. On a cable, a batch read matching no device returns NaN for `Average`, zero for `Sum`, `Minimum` and `Count`, and negative infinity for `Maximum`. Testing the result against zero does not detect the empty case; `__ic_isnan` detects it.

### 10.6 Scheduling

| Signature | Meaning |
|---|---|
| `void __ic_yield(void)` | End the tick and return the unused instruction budget |
| `void __ic_sleep(double seconds)` | Suspend for a duration that survives save and load |

Neither is required for correctness: a program exceeding the per-tick budget pauses and resumes cleanly. Both have timing consequences: a loop containing a yield advances one iteration per 500 ms tick.

`__ic_sleep` must not become the program's first instruction, which is diagnosed rather than emitted. The machine's `sleep` returns `-0` on line 0, which fails the negative test that ends the tick, so the chip re-runs it for the whole instruction budget every tick instead of suspending. Any statement ahead of it clears the restriction. The zeroing prologue takes line 0 where a program allocates in the data region and would clear it too, but declaring a global, an array or an address-taken local does not put one there: the optimizer promotes an object whose every use it can see, and a program left with nothing to zero gets no prologue.

### 10.7 Math

Operations the machine performs in one instruction, exposed directly rather than through a library.

| Arity | Intrinsics |
|---|---|
| `double f(void)` | `__ic_rand` |
| `double f(double)` | `__ic_sqrt` `__ic_abs` `__ic_sgn` `__ic_round` `__ic_trunc` `__ic_ceil` `__ic_floor` `__ic_log` `__ic_exp` `__ic_sin` `__ic_cos` `__ic_tan` `__ic_asin` `__ic_acos` `__ic_atan` |
| `double f(double, double)` | `__ic_min` `__ic_max` `__ic_pow` `__ic_atan2` |
| `double f(double, double, double)` | `__ic_clamp(v, lo, hi)` `__ic_lerp(a, b, t)` |
| `bool f(double)` | `__ic_isnan` |

`__ic_round` is banker's rounding, not C's. Ties go to even, so it returns 0 for 0.5 and 2 for 1.5.

`__ic_min` and `__ic_max` propagate a NaN. An expression the optimizer folds into a NaN-blind minimum or maximum has no instruction to select, since answering with whichever operand is not a NaN would drop the NaN the program produced, and the fold is diagnosed with advice to write `__ic_min` or `__ic_max` where propagating it is wanted and `__ic_isnan` where it is not. An unsigned minimum or maximum is diagnosed the same way, the machine's arithmetic being signed throughout.

`__ic_rand` draws from an unseeded generator, so two runs of the same program disagree and no other implementation of the machine can be compared against it.

`__ic_isnan` is the machine's own NaN test, and the only way to detect the empty batch read the game documents.

The transcendental intrinsics, being the trigonometric family plus `__ic_log`, `__ic_exp` and `__ic_pow`, reach the host platform's math library. Results agree with the machine to within an ULP rather than exactly.

### 10.8 Purity

`__ic_trunc` is the only pure intrinsic: it computes a result and does nothing else, so a call whose result nothing reads may be deleted and repeated calls with the same argument may be merged.

Every other intrinsic is impure and a dead call to one is kept. A device access is observable, a yield is timing, and `__ic_rand` answers differently each time. This rule is separate from the deletion of a dead operation in 4.2.3; that deletion takes a dead shift's fault with it.

## 11 Translation limits

These bounds are properties of the translator rather than of the language. A program can reach one without containing any construct Annex A names.

### 11.1 Source size

A source file is read up to 1 MiB. A larger one is refused without being parsed. The bound is on the read, not on the language: an unbounded read blocks forever on a character device or a named pipe with no writer, and the largest program this compiler is built for is a small fraction of the limit.

### 11.2 Tree nesting

One top-level declaration may nest at most 400,000 constructs inside one another. Every pass that reads the tree descends it with a recursion of its own, and a deeper tree takes the stack with it. A file over the bound is refused whole and nothing else about it is reported.

### 11.3 Lowering

A program may not descend more than 50,000 levels while lowering, nor lower more than 200,000 constructs in all. Each is a diagnostic naming the bound it reached.

The bound of 11.2 is measured from each declaration on its own, and lowering composes them: a call is lowered by generating the callee's body inside the caller's own descent, so a chain spends every function's depth at once, and a chain whose functions each call two others doubles what is generated at every level. Two chained declarations analysis accepts can exhaust the descent bound; a kilobyte of source can exhaust the construct bound.

### 11.4 Storage layout and register pressure

| Refused | Message names |
|---|---|
| A function holding more values at once than the register file and the slots below it can serve | The function, how many slots it would spill into, how many the data region already holds, and that shortening an array, dropping a global, or splitting the expression are what reduce it |
| One line reading more distinct spilled operands than the configuration holds scratch registers back for | The line, since one reload would overwrite another before the instruction read it |
| A global or a local that does not lay out in whole memory slots | The object, and that `long long`, `double`, `bool`, a pointer, and an array of them are what the data region holds |
| A local of a function that can reach itself through a call, needing a data-region slot | The function, and that passing the value as a parameter or rewriting the recursion as a loop are the ways out |

Register pressure is a source diagnostic, not a compiler failure. The array bound of 4.8 caps a single declaration at 512; the limits above report the program that does not fit as a whole.

### 11.5 Diagnostic counts

| Limit | Value |
|---|---|
| Parse diagnostics | 64 |
| Analysis errors | 64 |
| Analysis warnings | 64, counted apart from errors so that warnings cannot crowd out the errors that decide whether the program compiles |
| Messages per source position | 1. An identical message at an identical position is issued once |

Past a cap, the remaining diagnostics of that severity are dropped and a note marks where the first dropped one sat. A clean-looking tail is therefore not proof that nothing follows it.

### 11.6 Editor limits

The IC Editor holds 4096 bytes, 128 lines, and 90 characters of instruction on a line. A program over any of the three is refused, with a size report naming each limit it exceeds and no assembly written.

The editor itself refuses only the byte budget. A paste over either of the other two is cut in silence and runs as a different program, so the compiler refuses all three. The chip cuts a line at its first `#`, so a comment carried past the 90-character width is cut on paste and does not fail the compile.

## Annex A Diagnosed constructs

Each produces a diagnostic naming the construct. The lexical forms of 3.11 and the bounds of clause 11 are not repeated here.

### A.1 Types

| Construct | Reason |
|---|---|
| `struct`, `union`, `enum`, `typedef` | No aggregate or alias types |
| `char`, `short`, `int`, `signed`, `unsigned` | `long long`, `bool`, and `double` are the only scalar types |
| `unsigned long long`, `signed long`, `int long` and every other combined spelling | The message names the whole spelling and lists the types MicroC has; with no `typedef`, nothing else becomes one |
| `long long long` | Diagnosed at the third word |
| `long long int`, `long int` | Diagnosed at the trailing word, named as the one to delete |
| `float` | Every register and memory slot holds one whole double, so there is no 32-bit type to name |
| A `void` variable, parameter, array element, or cast | `void` is a function result type only |
| A pointer to `void` | The same |
| An implicit narrowing of a `double` | A cast is what says the truncation was meant |
| A `double` as a condition | Compare it; a test against zero is rarely what a fractional reading wanted |
| A `bool` under an arithmetic, bitwise or ordering operator | Only a conversion to `bool` normalizes, so any other result would leave one that is neither 0 nor 1 |
| An assignment to a `dev`, an array of `dev`, a pointer to `dev`, a function returning `dev`, a cast to or from `dev` | A device is a compile-time name rather than a value |
| A `dev` parameter on a function that can reach itself through a call | It is compiled out of line, and the pin would have to travel in a register the chip does not read as a device |
| `nullptr` | A pointer names a slot and has no null |

### A.2 Declarations and declarators

| Construct | Reason |
|---|---|
| `static`, `extern`, `auto`, `register`, `thread_local` | One translation unit, one thread, no storage classes |
| `volatile`, `restrict`, `inline`, `_Atomic`, `_Noreturn` | None describes anything here; the compiler decides inlining |
| Several declarators in one declaration | One declaration declares one variable |
| A declarator with more than one suffix | A declarator names one array or one parameter list and nothing outside it |
| A parenthesized declarator | It spells a function pointer, which is excluded |
| Function pointers, varargs | Calls are direct and fixed-arity |
| An old-style parameter list | Write each parameter's type inside the parentheses |
| Two parameters of one function sharing a name | One name, one object |
| A second definition of one function | The first is the definition |
| Nested function definitions, and a function prototype inside a block | Functions are file scope, and MicroC writes every declaration of one there |
| Trailing `const` or `constexpr` | Both precede the type |
| `const` or `constexpr` on a function | Not meaningful |
| `constexpr` on a parameter | A parameter's value is whatever the call site passed |
| A qualifier inside a cast | A qualifier says how a declaration is stored, and a cast declares nothing |
| An attribute other than `[[ic11c::prefab("PrefabName")]]`, a second attribute, an attribute on a function, a parameter, a statement or a declarator, and an attribute written after a specifier | One attribute, one spelling, one position, one home |
| `alignas`, `alignof` | Every value occupies one slot |
| A linkage specification, and the macro type specifier `typeof` and `typeof_unqual` spell | Neither has anything to describe in one translation unit with no preprocessor |
| A declaration taking a device pin spelling, a machine name, or a machine constant | Those positions resolve the name without consulting scope, so the declaration could not be reached from one |
| An `__ic_` name that is not an intrinsic | The prefix is reserved for them, so the name is read as a misspelled intrinsic |

### A.3 Arrays, pointers and initializers

| Construct | Reason |
|---|---|
| Multi-dimensional arrays | Index a flat array |
| A variable-length array, spelled `[*]` | The data region is laid out at compile time |
| An array bound that is not a constant expression | The same |
| An array bound below 1 or above 512 | Every array has a first element, and the data region is 512 slots |
| An array element of type `void` or `dev` | Neither occupies a slot |
| An array assignment | Assignment moves one machine value |
| A function returning an array | A result reaches the caller in one register |
| `&` on an array name | A pointer to an array is not expressible |
| Nested brace initializers | Arrays are one-dimensional |
| An array initialized from an expression rather than a brace initializer | An array is as many machine values as it has elements |
| A designated initializer | A brace initializer supplies elements from index zero |
| Compound literals | No object is created outside a declaration |
| A brace initializer supplying more elements than the bound | The extra ones have no slot |
| A pointer expression that may designate either of two objects | A pointer must trace at compile time to exactly one |
| Comparing or subtracting pointers into different objects | The difference would not be a distance |
| A constant subscript that reaches outside the object it indexes | The slot is fixed at compile time and lands in another object or in the call stack, which share one 512-slot array with nothing between them |
| Pointer arithmetic whose object and step are both fixed by the expression, landing outside that object | The same slot, reached by arithmetic instead of by a subscript. Forming the address one past the last slot stands, as it does in C; reading or writing through it does not |
| Casts to a pointer type or to `void` | Would hide a pointer's object; an expression statement already discards |

### A.4 Expressions and statements

| Construct | Reason |
|---|---|
| Comma operator | No expression sequencing |
| `sizeof` | Meaningless without byte addressing |
| `.` and `->` | No aggregates to reach into |
| `_Generic` | One type per expression, chosen where it is written |
| A statement expression, `({ … })` | A block is not a value |
| A label | No `goto` to reach one |
| `goto` | No arbitrary control flow |
| A statement at file scope | A statement is only valid inside a function body |
| A statement in a switch body before the first `case` or `default` | Nothing would reach it |
| A `case` or `default` label outside a switch | It labels an arm of one |
| `break` outside a loop and a switch, `continue` outside a loop | Each leaves a construct that has to be there |
| An arm of a switch whose non-empty body falls into the next arm, the last arm excepted | An empty body is the one permitted fallthrough |
| Two case labels of one switch with the same value, or two `default`s | Each tag value selects one arm |
| Control reaching the end of a non-`void` function | The caller would read whatever the result register held |
| Reading a register-resident local not assigned on every path reaching the read | Nothing zeroes a register |
| Naming a function or an intrinsic without calling it | Neither name is a value |
| An expression the optimizer folds into an operation the machine has no instruction for | An unsigned minimum or maximum, and a NaN-blind minimum or maximum, each name the `__ic_` form to write instead |

### A.5 Constant expressions and value windows

| Construct | Reason |
|---|---|
| A `constexpr` object whose initializer is not a constant expression | The specifier is what promises it is one |
| The address of a `constexpr` object, the machine constants among them | A scalar one occupies no storage for a pointer to designate |
| A `double` computed with in a constant expression of integer type | C admits one there only as a floating literal a cast converts |
| An integer fold whose answer depends on the C type of its literals | The compiler folds in the machine's 64-bit arithmetic and C in a type that is often narrower; see 6.7.3 |
| Division or remainder by zero in a constant expression | The constant case has no value to fold to |
| A constant quotient the C type does not hold | The one pair C leaves undefined for division |
| A cast of a `double` constant that is not a value the target holds | A non-finite or out-of-range operand has no truncation |
| A constant outside the exact-integer window | The type is exact to ±2^53 and no further |
| A bitwise or shift operand outside the bitwise operand window | The machine's own conversion does not carry it |
| A left shift whose constant answer reaches 2^53 | The result conversion takes the sign from bit 53 |
| A shift count outside the width of the C type of its left operand, or one a read range puts outside 0 through 63 | The chip shifts by the low six bits of the count |
| An operand the chip narrows, not shown to lie in the narrowing window | See 4.7 |
| A negative constant slot index | A slot index counts from zero |
| An optimizer-formed bitwise operation over a value the compiler cannot show finite | An infinity there faults the chip |

### A.6 Program structure

| Construct | Reason |
|---|---|
| Preprocessor directives | No preprocessor |
| A file declaring no `main`, a `main` with any other result type or with parameters, and a `main` declared and never defined | Execution begins in its body |
| A function a call names and nothing defines | There is no linker |
| A recursion whose frames cannot fit even one activation in the remaining slots | Frames share the 512-slot array with data |
| A local of a recursive function that needs a data-region slot | One slot is one address for every activation |
| `__ic_sleep` as the program's first instruction | The machine's `sleep` returns `-0` on line 0, which fails the test that ends the tick |

## Annex B Undefined, unspecified and faulting behaviour

### B.1 The table

| Situation | Result |
|---|---|
| Signed overflow | Undefined. Values do not wrap; they grow toward infinity |
| A `long long` outside ±2^53 | Unrepresentable, from `+`, `-`, `*` and a cast alike. A bitwise or shift operand stops one value short of that and a left shift's answer with it; see 4.2.3. Diagnosed wherever the value is constant, and undetected otherwise |
| A bitwise or shift operand outside ±2^63 | Faults the chip, losing every write the tick had left, wherever the operation is emitted at all: one whose result nothing reads is deleted and takes its fault with it. Diagnosed only where the optimizer formed the bitwise operation itself and cannot show the operand finite; an operator the source wrote is emitted and undetected |
| A NaN reaching a bitwise or shift operand | Converts rather than faulting, and reaches the instruction as −2^63. Masked against any operand the language admits it answers zero; `~` answers −1 and `>>` answers −2^53 |
| Shift count negative or at least 64 | The chip shifts by the low six bits of the count, so `-24` shifts left by 40 and `64` shifts by none. A shift is held to the width of the C type of its left operand, which is 32 for a bare literal: `1 << 31` is diagnosed for the value it computes, and `1 << n` for a count no width bounds. Write `(long long)1 << n`. A count the compiler can read a range for is diagnosed where that range leaves 0 through 63; one it cannot, a loop counter being the shape that is, is undefined at run time |
| Shift count outside the signed 32-bit range | Faults the chip before the reduction above applies: `ShiftUnderflow` below −2^31 and `ShiftOverflow` above 2^31 − 1 |
| An operand outside the narrowing window | The line runs against an integer the program never computed and nothing reports the substitution. Diagnosed wherever a range reading shows the operand may leave the window; see 4.7 |
| A pointer stepped outside its object through a pointer variable, by a step the program computes, or by a subscript on a parameter, which carries no length | Undefined. The address is a slot in the 512-slot array: one the object does not hold but the array does is read or written quietly. Past either end of the array a write raises `StackOverFlow` above it and `StackUnderFlow` below it, and a read answers the `Unknown` error at either end. An address no signed 32-bit integer denotes narrows to one the program never computed, which on the shipped runtime is below the array |
| Division or remainder by zero | Undefined. The machine divides doubles, so the register is left holding an infinity or a NaN rather than faulting, and the program computes with it |
| Reading an object before it is assigned | Diagnosed for a register-resident local, by definite assignment (7.5). A data-region object reads as zero instead; see 7.4 |
| Recursion deeper than the 512-slot array allows | Faults at runtime. A warning names the activations the remaining slots hold, and a recursion that cannot fit even one is diagnosed, but the depth actually reached is the data's |
| A property a device does not answer, or a slot it does not declare | Faults at runtime on the first device the access reaches, and a batch read faults before it aggregates, so even `Count` does. Warned about wherever the prefab is decided at compile time, by a batch form whose prefab operand folds or a pin whose declaration carries the prefab attribute, and not otherwise |
| A batch form on a housing wired to no data cable | All seven fault before reading the prefab hash, whatever their operands name |
| Reading or writing an unconnected pin | Faults, retried each tick, and clears if the device is later connected |
| Evaluation order outside `&&`, `\|\|`, and `?:` | Unspecified |

### B.2 What a fault costs

A fault stops the chip partway through the tick. Every write the program had left to make in that tick is lost, and the chip restarts the program on the following tick. Nothing in the program observes the fault, and no diagnostic precedes one.

## Annex C Grammar summary

### C.1 Notation

`{ x }` is zero or more, `[ x ]` is optional, `|` alternates, quoted text is literal. `identifier`, `integer-literal`, `floating-literal`, `character-literal` and `string-literal` are the lexical terminals of clause 3.

### C.2 Grammar

```
translation-unit = { file-declaration } ;

file-declaration = [ attribute ] declaration-head
                   ( function-tail | variable-tail ) ;
local-declaration= declaration-head variable-tail ;
declaration-head = { specifier } type-specifier { "*" } identifier ;
attribute        = "[" "[" "ic11c" "::" "prefab" "(" string-literal ")" "]" "]" ;
specifier        = "const" | "constexpr" ;
function-tail    = parameter-list ( block | ";" ) ;
variable-tail    = [ "[" expression "]" ] [ "=" initializer ] ";" ;

type-specifier   = "long" [ "long" ] | "bool" | "double" | "dev" | "void" ;

parameter-list   = "(" [ "void" | parameter { "," parameter } ] ")" ;
parameter        = [ "const" ] type-specifier { "*" } [ identifier ]
                   [ "[" [ expression ] "]" ] ;

initializer      = expression
                 | "{" [ expression { "," expression } [ "," ] ] "}" ;

block            = "{" { statement } "}" ;

statement        = block
                 | ";"
                 | local-declaration
                 | expression ";"
                 | "if" "(" expression ")" statement [ "else" statement ]
                 | "while" "(" expression ")" statement
                 | "do" statement "while" "(" expression ")" ";"
                 | "for" "(" for-init [ expression ] ";" [ expression ] ")" statement
                 | "switch" "(" expression ")" "{" { case-clause } "}"
                 | "break" ";"
                 | "continue" ";"
                 | "return" [ expression ] ";" ;

for-init         = ";" | local-declaration | expression ";" ;
case-clause      = ( "case" conditional-expression | "default" ) ":" { statement } ;

expression       = conditional-expression [ assignment-operator expression ] ;
conditional-expression
                 = binary-expression [ "?" expression ":" conditional-expression ] ;
binary-expression= unary-expression { binary-operator unary-expression } ;
unary-expression = ( "++" | "--" ) unary-expression
                 | "(" type-specifier ")" unary-expression
                 | ( "+" | "-" | "!" | "~" | "&" | "*" ) unary-expression
                 | postfix-expression ;
postfix-expression
                 = primary-expression
                   { "[" expression "]"
                   | "(" [ expression { "," expression } ] ")"
                   | "++" | "--" } ;
primary-expression
                 = identifier | integer-literal | floating-literal
                 | character-literal
                 | "true" | "false" | string-literal
                 | "(" expression ")" ;

binary-operator  = "*" | "/" | "%" | "+" | "-" | "<<" | ">>"
                 | "<" | "<=" | ">" | ">=" | "==" | "!="
                 | "&" | "^" | "|" | "&&" | "||" ;

assignment-operator = "=" | "+=" | "-=" | "*=" | "/=" | "%="
                    | "&=" | "|=" | "^=" | "<<=" | ">>=" ;
```

`binary-expression` is written flat above; operands group by the precedence table of 6.1. A parenthesized type-specifier before a unary expression is always a cast, never a parenthesized expression, because there are no typedefs for a name to be ambiguous with.

`function-tail` is reachable only from `file-declaration`, which keeps a function declaration, with a body or without one, at file scope.

### C.3 What the grammar admits and the language does not

The grammar describes shape, not validity, and it is not the parser: the front end reads C. So every construct Annex A names parses and is then diagnosed by name, whether the grammar above derives it or not. `(void)` as a cast target and an attribute on a function declaration are shapes it derives; `struct`, `goto`, `sizeof` and the comma operator are shapes it does not. Both kinds draw an error naming the construct rather than an unexpected token.
