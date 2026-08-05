# ic11c

A compiler from MicroC to IC10, the assembly language of the programmable chips in Stationeers. It reads one source file and writes IC10 assembly to stdout, ready to paste into an IC Editor.

## The target

A chip runs at most 128 lines and 4096 bytes, and executes 128 instructions per 500 ms tick. It has 18 registers, all of them doubles; there is no integer type and no byte addressing. Memory is one 512-double array shared between data and the call stack. Branch operands are line numbers, so inserting an instruction moves every target below it. `alias` and `define` each occupy a line and an execution slot.

The chip validates almost nothing at assembly time. A bad register, an out-of-range device pin and an invalid logic type all assemble, then fault at runtime once per tick until the program is changed.

Those limits make size the binding constraint, so the compiler optimizes for it. The front end parses with tree-sitter and lowers to LLVM IR, which is optimized at `-Oz` with a trailing dead-code pass. A custom backend then selects IC10 instructions, allocates the 18 registers and spills to the data region, lays out frames, and runs a peephole pass over the result. The cost model is emitted lines rather than cycles, which no stock LLVM pipeline targets; `docs/compiler.md` covers the pass selection and what it forecloses.

## Input

```c
// Aims every motorised solar panel on the data network at the sun, from one
// daylight sensor. Panels are reached by prefab hash, so they need no pins.
//
// The sensor must be placed facing up: it reports the sun as an angle away from
// its own forward axis, which the arithmetic below reads as an angle away from
// straight up.
[[ic11c::prefab("StructureDaylightSensor")]] const dev sensor = d0;

// Degrees to add to the sensor's bearing, where an array was built on a
// different rotation from the sensor. Usually 0, 90, 180 or 270.
constexpr double kAzimuthTrim = 90.0;

void aim(long long panels, double azimuth, double elevation) {
  __ic_store_batch(panels, Horizontal, azimuth);
  __ic_store_batch(panels, Vertical, elevation);
}

void main(void) {
  while (true) {
    double azimuth = __ic_load(sensor, Horizontal) + kAzimuthTrim;

    // A panel's Vertical is a pitch the game clamps into [15, 165], where 90
    // lies flat facing up, so it wants the sun's height above the horizon. At
    // night this goes negative and clamps to 15, parking the array on the
    // bearing the sun will rise from.
    double elevation = 90.0 - __ic_load(sensor, Vertical);

    aim(__ic_hash("StructureSolarPanel"), azimuth, elevation);

    __ic_yield();
  }
}
```

## Output

```
l r0 d0 Horizontal
add r0 r0 90
l r1 d0 Vertical
sub r1 90 r1
sb -2045627372 Horizontal r0
sb -2045627372 Vertical r1
yield
j 0
```

`aim` was inlined at its call site, `__ic_hash("StructureSolarPanel")` folded to `-2045627372`, and the constants folded. Comments do not reach the chip.

The size report is written to stderr, leaving stdout pasteable as it stands:

```
program: 134 of 4096 bytes (3%), 8 of 128 lines (6%), longest line 28 of 90 characters (31%)
  closest limit: the line count, at 6% spent
```

Below those two lines the report attributes bytes by construct, largest first, down to each inlined call site, and states what the 512-slot data region holds and how much room remains for call frames. A program over an editor limit exits non-zero and withholds its assembly, so stdout stays empty.

## Editor support

```
ic11c prelude [directory]
```

Writes `compile_flags.txt` into a directory, defaulting to the working one, and `ic10_prelude.h` into `.ic11c/` beneath it. clangd then completes every logic-type name, slot type and intrinsic signature. Both files are generated from the machine tables the compiler uses, so they cannot describe a different machine.

MicroC is a strict subset of C23: `long long` (also spelled `long`), `bool` and `double` scalars, a `dev` type naming a device pin, arrays, and restricted pointers. A MicroC program is valid C23, which is what a C editor parses.

## Diagnostics

The compiler carries the game's device catalogue, and checks a pin's declared prefab against what that device answers for:

```
program.c:3:24: warning: a completed StructureGasSensor (Gas Sensor) accepts no write of 'Setting'; this program declares d0 to be a StructureGasSensor, and __ic_store writes it to whatever the world put there, so the chip faults on it wherever the declaration holds — check the device's properties for the one that takes this setting
```

The integer type is backed by a double, and a value it cannot hold exactly is refused at the literal:

```
program.c:2:14: the integer literal 9007199254740993 is outside -2^53 to 2^53, the range a long holds on this machine, where every value lives in an IEEE double
```

Register-resident locals are not zeroed, so reading one before it is assigned is refused:

```
program.c:4:37: 'a' is read here without having been assigned on every path that reaches this point, and it lives in a register, which nothing zeroes; give the declaration at program.c:2:10 an initializer, or assign it in every branch before this one
```

## Machine tables

The instruction set, the logic and slot types, the batch and reagent modes, the machine constants and the prefab roster are decompiled from the game's own assembly rather than transcribed from published documentation. `task isa` regenerates them from a pinned game manifest, so a game update is applied by rerunning it.

## Verification

The fixture corpus is 27 programs, two of them written by players and adopted unchanged in shape.

| Check | What it establishes |
|---|---|
| The game's own chip | `tools/chipgen` cuts `ProgrammableChip` and the devices around it out of the decompiled source into a standalone C# unit, run as a subprocess under a pinned Mono image. Compiled programs execute on the game's own implementation. It runs outside the game, so the surrounding world is simulated by the harness. A missing chip fails the test rather than skipping it |
| Comparison against clang | The same C23 source is built natively by clang at `-O0` with UBSan trapping, device access and yields routed through the same chip harness, and the two runs compared. This establishes that the emitted program computes what the source specifies |
| Optimized against unoptimized | Both builds of each fixture `--no-optimize` accepts run on the chip in one seeded world, and their device-write sequences are compared as bit patterns |
| Generated programs | A random MicroC program generator feeds the clang comparison. A failing program is shrunk before it is reported |
| Generated assembly | A differential fuzzer generates IC10 assembly directly and runs it on the chip, reaching instruction families the backend never selects. It exercises the harness, and establishes nothing about a lowering |
| Three platforms | Every release requires Linux, macOS and Windows to emit byte-identical assembly for the whole corpus, matching on SHA-256, exit status, line count and byte count |
| C23 conformance | Every corpus program is compiled as C23 against the generated prelude, with `-pedantic-errors`, under whichever of `clang` and `gcc` is on PATH. The gate fails rather than skips when neither is present |

## Limitations

- The integer type is an IEEE double. `long long` (also spelled `long`) is exact to ±2^53, not 64 bits.
- Bitwise and shift operands are bounded further, to strictly inside ±2^53. Outside ±2^63 the operand conversion faults the chip.
- No structs, unions, enums, function pointers, preprocessor, includes, linker, or dynamic allocation.
- Recursion is permitted with no depth bound. Frames share the 512 slots with data, and exhausting them faults at runtime.

`docs/microc.md` is the language definition: a construct absent there is not in the language.

## Usage

```
ic11c program.c > program.ic10
```

| Flag | Effect |
|---|---|
| `--readable` | Name the block each line opens and the block each branch goes to, in a trailing comment. The chip cuts a line at its first `#`, so the program ships on the same lines; the annotation costs bytes and line width only |
| `--numeric` | Emit the integer behind every logic type, slot type, batch mode and reagent mode instead of its name. The smallest form, for a program that has run out of bytes |
| `--no-optimize` | Emit what IR generation produced, for comparison against the optimized output when a lowering looks wrong. Not a second supported path: it refuses programs the shipped path accepts |
| `-v`, `--version` | Print the version |

| Exit | Meaning |
|---|---|
| 0 | Compiled and fits inside every limit |
| 1 | No program to keep: the source could not be read, the program was refused and the diagnostics name the lines, the program is over a limit the in-game editor enforces, or a stream write did not finish |
| 2 | The command line: an unknown flag, a value a flag does not take, the wrong number of arguments |
| 70 | A stage of the compiler could not run |

## Building

Building requires Go, a C++ toolchain, and libLLVM with development headers at the major `.llvm-version` names. The compiler links libLLVM through CGo, so that major is fixed at build time by a Go build tag, and a gate rejects any file naming a different one.

Commands live in `Taskfile.yml` ([go-task](https://taskfile.dev)).

| Command | Does | Also requires |
|---|---|---|
| `task build` | Build the compiler | |
| `task test` | Every package, against a chip cut from the decompiled game | Docker, a `gamesrc/` tree, `clang` |
| `task lint` | golangci-lint and gopls diagnostics | |
| `task check` | Every gate, including `task test` | the .NET SDK, network access |
| `task gamesrc` | Rebuild the decompiled game source from the pinned manifest | Docker, network access |
| `task isa` | Regenerate the machine tables from that source | Docker, network access |
| `task release:linux VERSION=v1.2.3` | The Linux artifact into `dist/` | Docker only |
| `task release:windows VERSION=v1.2.3` | The Windows artifact, cross-built from Linux | Docker only |

`task test` requires Docker and a prior `task gamesrc`, which fetches and decompiles the game's assembly into an untracked `gamesrc/`; it fails rather than skips without them. `clang` is required outright, since the comparison against a native build has no substitute. gcc is optional, and must be 14 or later if present, that being the first version accepting `-std=c23`.

Per-platform toolchains, including the MSYS2 setup Windows requires, are in `docs/building.md`.

Pushing a `v*` tag starts a release. The workflow checks that the tag is on master and that CI passed on that commit, builds the three platforms, and requires their corpus output to agree byte-for-byte before publishing.

## Documentation

| File | Contents |
|---|---|
| `docs/microc.md` | The language definition: lexical structure, grammar, types, conversions, expressions, statements, intrinsics |
| `docs/target.md` | The IC10 machine: registers, memory, limits, error model, instructions not to emit |
| `docs/compiler.md` | Pipeline stages, memory layout, calling convention, verification ordering |
| `docs/testing.md` | What the suite establishes layer by layer, and what it leaves untested |
| `docs/building.md` | Toolchain requirements, per-platform builds, release artifacts, CI |
| `docs/game-updates.md` | Reviewing a game update against what derives from it |
| `docs/future-work.md` | Open questions and deferred decisions |
