# ic11c

A compiler from MicroC to IC10, the assembly language of the programmable chips in Stationeers. One source file goes in; assembly comes out on stdout, ready to paste into an IC Editor.

## What IC10 gives you

A chip runs at most 128 lines and 4096 bytes, 128 instructions per 500 ms tick. It has 18 registers, all of them doubles — there is no integer type and no byte addressing. Memory is one 512-double array shared between your data and your call stack. Branches take line numbers, so inserting an instruction moves every target below it. `alias` and `define` each spend one of the 128 lines and one execution slot per tick, so naming a thing costs budget.

The chip validates almost nothing when it assembles. A bad register, an out-of-range device pin and an invalid logic type all compile cleanly and then fault at runtime, once per tick, indefinitely.

## What you write

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

## What the chip runs

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

`aim` was inlined at its call site. `__ic_hash("StructureSolarPanel")` became the CRC-32 the chip wants, `-2045627372`, at compile time — you write the prefab name, not the number. The constants folded. Comments cost nothing, because they never reach the chip.

The size report goes to stderr, so the file stdout leaves is pasteable as it stands:

```
program: 134 of 4096 bytes (3%), 8 of 128 lines (6%), longest line 28 of 90 characters (31%)
  closest limit: the line count, at 6% spent
```

Below those two lines it attributes bytes by construct, largest first, down to each inlined call site, and states what the 512-slot data region holds and how much room is left for call frames to grow into. It is what gets a program back under budget. A program over an editor limit fails with its assembly withheld, so nothing unpasteable reaches stdout.

## Your editor already works

```
ic11c prelude [directory]
```

Writes `compile_flags.txt` into a directory, defaulting to the working one, and `ic10_prelude.h` into `.ic11c/` beneath it. That is the whole of what a C editor needs. clangd then completes the 358 logic-type names, the 33 slot types and the intrinsic signatures, which is the part a programmer cannot hold in their head. Both files are generated from the machine tables the compiler itself uses, so they cannot describe a different machine.

MicroC is a strict subset of C23: `long long` (also spelled `long`), `bool` and `double` scalars, a `dev` type naming a device pin, arrays, and restricted pointers. Every MicroC program is a valid C23 translation unit, which is what lets an ordinary C editor read one. C admits more than MicroC does, so a clean parse there is not an analysis; the compiler settles that.

## What the compiler knows

It carries the game's device catalogue, so it holds what you say a pin is wired to against what that device actually answers for:

```
program.c:3:24: warning: a completed StructureGasSensor (Gas Sensor) accepts no write of 'Setting'; this program declares d0 to be a StructureGasSensor, and __ic_store writes it to whatever the world put there, so the chip faults on it wherever the declaration holds — check the device's properties for the one that takes this setting
```

It knows the arithmetic is a double rather than an integer, and refuses instead of emitting a program that means something other than what you wrote:

```
program.c:2:14: the integer literal 9007199254740993 is outside -2^53 to 2^53, the range a long holds on this machine, where every value lives in an IEEE double
```

It knows which of your variables ended up in a register, and that nothing zeroes those:

```
program.c:4:37: 'a' is read here without having been assigned on every path that reaches this point, and it lives in a register, which nothing zeroes; give the declaration at program.c:2:10 an initializer, or assign it in every branch before this one
```

## Machine facts come from the game

Every table is decompiled from the game's own assembly rather than taken from published documentation. Where the two disagree, the implementation wins. Extracted from game version 0.2.6403.27689, depot 600762, manifest 2546537964923579038: 154 IC10 instructions, 358 logic types, 33 logic slot types, 5 batch modes, 4 reagent modes, 9 machine constants, 1565 prefabs, 46 reagents.

## What checks the output

The fixture corpus is 27 programs, two of them written by players and adopted unchanged in shape.

| Check | What it establishes |
|---|---|
| The game's own chip | `tools/chipgen` cuts `ProgrammableChip` and the device surface around it out of the decompiled game source into a standalone C# compile unit, compiled under a digest-pinned Mono image and driven as a subprocess. Compiled programs execute on the game's own machine rather than on a model of one. The chip is the game's own code running outside the game, so the world around it is stood in for. A missing chip is a test failure, never a skip |
| Comparison against clang | The same C23 source is built natively by clang at `-O0`, with `-fsanitize=undefined -fno-sanitize-recover=all -ffp-contract=off`, and device access, machine functions and yields routed through the same chip harness. This is the check that answers whether the emitted program means what the source said |
| Optimized against unoptimized | Both builds of each fixture `--no-optimize` accepts run on the chip in one seeded world, and their device-write sequences are compared as bit patterns |
| Generated programs | A random MicroC program generator feeds the clang comparison. A failing program shrinks before it is reported |
| Generated assembly | A differential fuzzer generates IC10 assembly directly and runs it on the chip, reaching instruction families the backend never selects. It checks the harness against the game's machine and establishes nothing about a lowering |
| Three platforms | Every release requires Linux, macOS and Windows to emit byte-identical assembly for the whole corpus — same SHA-256, exit status, line and byte counts — or the release fails |
| C23 conformance | Every corpus program is compiled as C23 against the generated prelude, with `-pedantic-errors`, under whichever of `clang` and `gcc` is on PATH. The gate fails rather than skips when neither is present, which is what makes the subset claim enforceable rather than asserted |

## What it will not do

- The integer type is an IEEE double. `long long` (also spelled `long`) is exact to ±2^53, not 64 bits.
- Bitwise and shift operands are bounded more tightly still, to strictly inside ±2^53, and outside ±2^63 the conversion faults the chip.
- No structs, unions, enums, function pointers, preprocessor, includes, linker, or dynamic allocation.
- Recursion is permitted with no depth bound. Frames share the 512 slots with data, and exhausting them faults at runtime.

`docs/microc.md` is the language definition: a construct absent there is not in the language.

## Using it

```
ic11c program.c > program.ic10
```

| Flag | Effect |
|---|---|
| `--readable` | Name the block each line opens and the block each branch goes to, in a trailing comment. The chip cuts a line at its first `#`, so the program ships on the same lines; what the annotation costs is bytes and line width |
| `--numeric` | Emit the integer behind every logic type, slot type, batch mode and reagent mode instead of its name. The smallest form, for a program that has run out of bytes |
| `--no-optimize` | Emit what IR generation produced, for comparing against the optimized output when a lowering looks wrong. Not a second supported path: it refuses programs the shipped path accepts |
| `-v`, `--version` | Print the version |

| Exit | Meaning |
|---|---|
| 0 | Compiled and fits inside every limit |
| 1 | No program to keep: the source could not be read, the program was refused and the diagnostics name the lines, the program is over a limit the in-game editor enforces, or a stream write did not finish |
| 2 | The command line: an unknown flag, a value a flag does not take, the wrong number of arguments |
| 70 | A stage of the compiler could not run |

## Building

| Target | Needs |
|---|---|
| `task build` | Go 1.26, a C++ toolchain, libLLVM 22 with development headers |
| `task test` | the above, plus Docker, a `gamesrc/` tree from `task gamesrc`, and `clang` — the clang comparison takes no substitute, and the C23 gate also runs `gcc` where it is present |
| `task check` | the above, plus the .NET SDK 10.0.x and network access |
| `task release:linux`, `task release:windows` | Docker and nothing else |
| `task isa` | Docker, network access and Go — no game, no Steam client, no .NET toolchain |

The compiler links libLLVM through CGo, so the version is fixed at build time by a Go build tag. Commands live in `Taskfile.yml` ([go-task](https://taskfile.dev)).

| Command | Does |
|---|---|
| `task build` | Build the compiler |
| `task test` | Run every package against a chip built from the decompiled game source |
| `task lint` | golangci-lint plus gopls diagnostics |
| `task gamesrc` | Rebuild the whole decompiled game source tree from the pinned manifest |
| `task check` | Every gate: formatting, LLVM major, game pin, lint, shell, Taskfile, test, decompile gates, codegen, prefab reader |
| `task isa` | Regenerate the machine tables from the game assembly |
| `task release:linux VERSION=v1.2.3` | The Linux release artifact into `dist/` |
| `task release:windows VERSION=v1.2.3` | The Windows artifact, cross-built from Linux |

`task gamesrc` writes its tree under `gamesrc/`, and nothing in it is checked in. Only the extract half of `task isa` reaches the network; `task isa:generate` works from the checked-in ISA data, and ordinary builds touch neither.

What blocks a first build:

1. `task test` cannot run without Docker and a first `task gamesrc` run, which fetches and decompiles the game's own assembly. It fails rather than skips.
2. LLVM 22 is pinned by `.llvm-version`. The build tag is derived from it, and a gate refuses any file that disagrees.
3. gcc below 14 fails the C23 gate on every program. 14 is the first version accepting `-std=c23`.
4. The static Linux release link needs LLVM's static component archives, which only Debian and Ubuntu package. It cannot run on most other distributions.

Per-platform toolchains, including the MSYS2 setup Windows needs, are in `docs/building.md`.

A `v*` tag triggers a workflow that verifies the tag is on master and that CI passed on that commit, builds the three platforms, requires their corpus output to agree byte-for-byte, and publishes one executable per archive.

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
