# Testing

What the suite establishes, and, in the longer half, what it does not. A green run does not establish that the compiler is correct. The closing sections state where the gap is.

## How it runs

| Target | Establishes | Needs |
|---|---|---|
| `task test` | Every package in the module, against a chip cut and compiled from the decompiled game source on every run. The chip is the only thing that answers what the machine does, and the compiler driver is among the packages held to it, so compiled programs execute on the game's own machine rather than on a model of one | libLLVM matching the build tag; a decompiled game source, which `task gamesrc` writes; Docker, for the Mono image the chip is compiled and run under; `clang` or `gcc` for the C23 gate and `clang` alone for the clang comparison, both of which fail rather than skip without one |
| `task check:gamesrc-gates` | That the gates over the decompiled tree ran rather than stood down, and that the extractor still refuses a tree with no game in it | What `task test` needs |
| `task check:codegen` | The checked-in generated artifacts match what the generator produces now, and none of them shares a Go package with hand-written source | Nothing beyond Go |
| `task lint` | Static analysis and `gopls` diagnostics. Both halves always run and the verdict is taken over the pair afterwards | `task tools` |
| `task fmt:check` | No unformatted Go source | Nothing |
| `task check` | Every target above, and with it the LLVM-major gate, the game-pin gate, shellcheck over the scripts and over the Taskfile's own command blocks, and the prefab reader | What `task test` and `task lint` need, the .NET SDK, and network access for the pin |

`task test` runs `./...` and subtracts nothing. A package left out of the target that runs everywhere is a package nothing local covers, and the packages a Docker-free target would have to drop are the ones whose oracle is the game itself. The chip is cut and compiled first, and `internal/chiptest` fails rather than skips without one, so a green run is one that had it.

The prefab reader is the only part of `task check` that needs the .NET SDK. Its two targets shell out to it, so a machine without one fails them as a missing command rather than as a wall of test failures. CI gives them a job of their own, so master is gated either way.

CI splits `task check` by toolchain across four jobs, and one target sits outside all four. Three of the jobs are narrow: `check:codegen` on Go alone, the two prefab-reader targets on the .NET SDK, and `lint:shell` and `lint:taskfile` in the job that owns the build machinery. The `check` job runs every remaining target. It derives the decompiled game source and caches it on the pinned manifest, and the chip is cut again on every run, since the slicer refuses a game update that moved something. The one outside is `check:game-pin`, which asks the Steam depot which build it serves. A per-commit job holding it would depend on Steam being reachable, so a weekly workflow asks it instead. The two readings differ deliberately: the target passes over a depot that did not answer, since a Steam outage says nothing about the pin, and the weekly workflow fails on it, since asking is the whole of what that job does.

`task check:gamesrc-gates` reads the output of the two packages whose tests take a directory rather than the repository as their subject: the extractor that recovers the machine tables and the slicer that cuts the chip. `go test` exits 0 over a package that skipped everything in it, so status alone cannot tell a gate that held from one that stood down. Every test each package declares has to appear as a pass, with no skip. The script then runs the extractor over an empty tree and fails if that run exits 0 with no skip in its output, the state a package whose gates stopped reading the game would be in.

There is one coverage profile and one upload: `task test` writes it and the `check` job uploads it, so the reported figure is over every package in the module. It is skipped on a pull request from a fork, which gets no secrets.

| Variable | Effect |
|---|---|
| `IC11C_CHIP` | Runs `internal/chip`'s own tests instead of skipping them. Set by `task test`, which also passes the pinned Mono image and the binary directory. Every other package takes its chip through `internal/chiptest`, which fails rather than skips without one |
| `IC11C_GAMESRC` | Points the decompile-reading gates at a named tree |
| `IC11C_MICROC_PROGRAMS`, `IC11C_MICROC_SEED` | Widen the generated-MicroC campaign, or replay from one seed |
| `-short` | The generated-MicroC campaign draws 3 programs instead of 12, and stops building refusal witnesses natively |

## What each layer establishes

**Front end.** That the grammar has the precedence and associativity the language document states, checked through a fully parenthesized rendering that cannot hide a reassociation; that every node carries a usable source position; and that diagnostics are ordered, capped, one per root cause, and survive truncated input.

Analysis establishes definite assignment, the `const` and `constexpr` distinction, that constant folds agree with what C answers and refuse the ones C disagrees with, array bounds against the 512-slot region, and that reserved and prelude names cannot be redeclared. It also establishes the device-surface check in both directions: that an access the roster contradicts warns, through a batch hash and through a pin the program declares a prefab for, and that everything the check cannot decide stays silent. Two accesses that a completed wall cooler and a completed wall light refuse are run against the generated roster rather than a stub, in both the batch and the declared-pin spelling, and the same two accesses through undeclared pins are required to stay silent. What that establishes is that the shipped table answers them and that the declaration is what makes it answer.

**IR generation.** The shape each construct lowers to, the inline-site records the size report is built from, and debug locations, which every backend diagnostic depends on to name a source line.

**Optimizer.** That the linked libLLVM major is one of the supported set, since the build tag steers include paths but does not pin the library. Then, on modules shaped the way IR generation emits them: they verify either side of `-Oz`, allocas are promoted, debug locations survive, dead code and literal arithmetic fold, an attribute-free callee is not deleted, and two conditional stores to constant slots in different objects are left as two. The last is narrow on purpose. The pipeline does form pointer merges: InstCombine sinks two loads under a branch into one load of a merged pointer, and the driver's refusal fixtures carry a program it rejects for exactly that. The case therefore establishes which shapes still reach the backend with a resolvable pointer, and not that the optimizer declines to build one. It fails when that boundary moves, and it is not evidence that no pass needs excluding.

**Verification.** That every load and store resolves to one statically known object. It runs after optimization because the optimizer can create a pointer phi from source that had none, so a source-level check would pass and leave the backend with no base slot to add an index to.

**Instruction selection.** The largest unit surface: that refusals carry source-level diagnostics rather than IR text, that a subscript's element stride is divided out at compile time, that a bool's truth value and its signed reading are distinguished, that unsigned division reaches a signed synthesis, and that arithmetic wider than a double is refused rather than truncated.

**Register allocation and peephole.** Liveness across back edges, spill choice and slot reuse, saves around calls, device operands kept in range, and that allocation preserves meaning. The peephole is established to drop identity moves and to fold a negated set instruction into its complement without crossing a block boundary, a spill store or a reload.

The complement table itself is executed rather than inspected. Every pair is derived from the map, run on the chip, and asserted to answer exact opposites. The value pairs take the cross product of a grid holding signed zero, both infinities, the three doubles around 2^53, both subnormals, NaN, and operands that straddle the approximate comparisons' tolerance; the device pair takes a pin holding a device and an empty one. The pairs the table excludes, the ordered comparisons and the approximate ones, are executed on the same grid and asserted to answer either exact opposites or 0 for both, never 1 for both, with each half required to occur. The ordered pairs carry the stronger claim that both answer 0 for exactly the inputs holding a NaN. The approximate pairs cannot, because an infinite tolerance against equal operands makes the tolerance bound unordered with no operand being a NaN. The number of pairs is pinned so the table cannot shrink unnoticed, and the grid is held to still containing the values the conclusions rest on.

**Emission.** Golden output in both rendering modes, byte accounting that sums, budget violations and the report naming them, label resolution through an emptied block, and name mangling that resolves collisions.

**Machine model.** The tick model, `yield` and `sleep`, the game's own number syntax and its `HASH` function, and the table naming which operand position the chip reads through which conversion. That table is spelled out position by position because the extraction's own checks are satisfied by each conversion appearing somewhere, so a position that quietly lost one would clear every check upstream, which is the shape the defect took. Every position it names is one instruction selection bounds an operand against.

**Whole pipeline.** Source in, assembly out, executed on the chip, against hand-written expected values for hand-written programs: array and pointer addressing at every index, pointer difference, the boolean complement, a range check, a division under a sign check, recursion through the calling convention, the NaN behaviour of every ordered operator, and the constructs the optimizer is known to rewrite. The `push` and `pop` convention is executed rather than inspected, through tail recursion flattened to a loop, nested recursion twelve deep, and mutual recursion, each against an answer computed in the test, with the stack pointer asserted back to the base the prologue chose and the frame high-water mark asserted to grow with depth, so frames landing on top of each other fails.

## The fixture corpus

The MicroC programs `internal/corpus` holds, written as whole programs rather than snippets. Every consumer takes the whole set from that one package, so a program added to it joins all of them at once. Three copies of one list is how a fixture ends up compiled but never executed.

The package offers two views because the consumers split in two. A stage that only has to compile a program takes the corpus as the build captured it, out of an embed, and reads no file at all. A harness that hands a program to `clang` or to the `ic11c` command takes the directory the programs are checked in at instead, since another process needs a path, and globs it. Each such harness holds what its glob found to what the build captured, so the two views cannot come apart.

The embed is the point of the split. A pattern matching nothing fails the build, where a glob matching nothing yields an empty set and a table-driven test over an empty set passes. What still globs is what has to name a real file, and the release verification script with it, since shell cannot read a Go constant.

| Consumer | Asserts |
|---|---|
| Parsing | Parses with no diagnostic, and every node carries a valid position |
| Analysis | Analyzes with no error, against the full generated tables and the full device roster rather than a stub, and produces exactly the warnings a named list records for it |
| Conformance | Compiles as C23, see below |
| Compilation | Every emitted line is one the chip's assembler accepts: a known mnemonic not on the never-emit list, the right operand count, no exponent notation, a branch target inside the program |
| Execution | 200 ticks on the chip with every pin populated, asserting only that it does not fault and the program counter never leaves the program |
| Emission quality | No emitted `move` has the same register on both sides |
| Equivalence | Built twice, optimized and `--no-optimize`, driven through a seeded world, and the two device-write sequences compared |
| Clang | Built natively by clang from the same source, driven through the same seeded world, and the two device-write sequences compared |
| Measurement | Recompiled in every mode and configuration `task corpus:measure` prints, with the relations between them asserted |

Only `thermostat.c` has a behavioural test: driven across a setpoint and back, with the heater asserted to hold its last state inside the hysteresis band. The others are never checked against what they are supposed to do.

No fixture warns, and that is asserted per fixture by a registry of expected warnings rather than by a count: an expected warning that stops appearing fails, and so does a new one anywhere in the corpus. The registry is empty, which is the state the corpus is meant to be in. Two fixtures declare what their pins are wired to, so `thermostat.c`'s `Temperature` read and `On` write and `smelter.c`'s furnace slot read and `Lock` write are held to the roster rather than passing unexamined. `smelter.c` is also the fixture exercising the prefixed operand spellings, writing `SlotType_Quantity` and `BatchMode_Minimum` where the bare names belong to another family.

### C23 conformance gate

Every MicroC program is a valid C23 translation unit against the generated prelude header. The gate that establishes it sits in `internal/ic10`, beside the tables the header is generated from and the argument file it is reached through. It compiles each fixture under every driver it finds, `clang` first and `gcc` too where both are there, and never `cc`, whose target is unknown. Running both is the point rather than a fallback: the claim is about C and the two disagree about which extensions are C. It uses the checked-in argument file with the header path resolved against the corpus directory, plus `-fsyntax-only -pedantic-errors -Wno-deprecated-declarations`. `-pedantic-errors` is what gives it teeth: without it clang silently accepts GNU extensions that gcc rejects, so a program could pass on one machine and fail on another.

It keys on exit status rather than silence, since a program naming a retired logic type warns and is still valid MicroC. An absent C compiler fails rather than skips, because `go test` discards a passing package's output and a silent skip would be indistinguishable from a pass.

## The game's own chip as the oracle

`tools/chipgen` cuts `ProgrammableChip` and the device surface around it out of the decompiled game source into a standalone C# compile unit, which `task chip:build` compiles under a digest-pinned Mono image and `internal/chip` drives as a long-lived process. It is not a reading of the documentation and not a second reading of the source; it is the game's own text. Every question only the machine can answer, such as what Mono's parser reads a literal as or which fault the game's guard order raises, is asked of it rather than settled here.

What the slice leaves behind is world simulation, and each piece of it is narrowed to a value a test sets directly: what an atmosphere holds, which object carries a reference id, which devices a cable network carries, the animator and the notifications behind an interactable, the mode strings a prefab declares, and the ingot walk behind `rmap`'s return value. The permission predicates, the four batch reducers, the housing's batch lookup and the display-name sort it folds in, and both pairs of logic accessors are the game's own bodies. A run answers what the game answers for the state a test put the world into; it does not establish that the game would ever put the world into that state.

A reset leaves the housing on no data cable, the state an unwired one is in in game, and every batch form raises `DeviceListNull` there before it looks at a hash. Running a cable is a verb, and so is clearing the sync flag behind a state. Building a device on a pin is not: a faithful process carries the housing and nothing else, so what it can be asked about the batch forms is what they raise unwired and what the folds answer over an empty cable. A populated cable belongs to the permissive process, where laying a device on a pin also wires it onto the cable and files its reference id.

**The generated corpus** is assembly rather than compiler output, so it reaches the instruction families the backend never selects: the transcendentals, the bitfield and rotate group, the approximate comparisons, the relative branches and the device forms. Generation is a pure function of a seed, so a failure replays from the seed it reports; the default seed is fixed, so an unconfigured run is reproducible run to run and not merely from a printed seed. One generator emits terminating fault-free programs and is held to their running to the end, which stops it drifting into emitting faulting ones. The other provokes a deliberate fault, padded with lines that can neither fault nor branch, and every recipe is held to raising the fault it names on a line inside its own. A recipe that stopped faulting still assembles and still runs, so that assertion is the only thing that would report it.

The corpus is also held to reaching the shapes it exists for, each as a measured floor rather than a count: programs that return through `ra` rather than only writing it, programs where a poked address and a stack slot meet in the one 512-slot array, and programs that seed the pointer near either end of that array.

A run is bounded in ticks, and the tick loop runs inside the harness process so that a whole program costs one exchange. What the reply adds is how the last tick ended, the one distinction the chip itself destroys: a yield answers the negated line minus one and a suspending sleep answers the negated line, and the arm reflecting a negative address is the last place that sign exists.

**What is kept out of the corpus**, and why: `rand` draws from an unseeded generator in the game; `hcf` is the one parse arm the slice drops; `sleep` counts down against a clock a corpus draw arms with nothing; and `brapz`, `brnaz`, `bapzal` and `bnazal` assemble under no operand count in this build. A test holds the emitted set and the excluded set to partitioning the instruction table exactly, so nothing is both and nothing is neither, and a second holds every emitted mnemonic to being reached by a drawn corpus.

The seven batch forms are on no exclusion. The sweep runs no cable to the housing, so each resolves the housing's batch output, gets the null a housing on no network answers, and raises `DeviceListNull` before it has looked at a hash. They are a fault recipe for that reason rather than a value shape. What that leaves unasked is what the folds answer over an empty cable, which `internal/chip` settles directly against the same seven forms.

**What the protocol carries** is every bit of every double. A value crosses in either direction as `0x` and sixteen lower case hexadecimal digits, its IEEE-754 pattern read as unsigned, so the sign of a zero and a NaN's payload survive and a comparison has no third outcome between equal and different. A decimal rendering could carry neither: .NET's `"R"` drops the sign of a zero on Mono 6.12, and Mono's `double.Parse` is not correctly rounded, so about one shortest round-tripping decimal in three hundred reads back one unit in the last place away. Integers stay decimal. The driver proves the round trip at startup by seeding a negative zero, a NaN payload, an infinity and the largest finite double into a register and a memory slot and reading each back through the state block, a different path from the one that wrote it. It then has the chip compute a negative zero and divide into it, so the sign is read once off the zero and once off the infinity that has no zero in it.

**Guards against a vacuous pass.** A missing chip is a failure rather than a skip: every package that takes one through `internal/chiptest` fails naming the target that builds one, and `task test` passes `-count=1`, so a run that did not happen can never read as a run that found nothing. No package is left out of that target. `task test` re-cuts the slice before it compiles, because the slicer refuses a game update that moved something, and a run over a stale binary would answer questions about a game version nobody is running. The Mono image is pinned by digest as well as by tag, and the driver refuses one that is not, so a rebuilt tag cannot silently change what the answers are.

## Optimized against unoptimized

Every fixture is built twice, once as it ships and once with `--no-optimize`, run on the chip in one seeded world, and the two runs compared.

This establishes that optimizing did not change what the program does. It does not establish that either build is correct. Two builds can agree and both be wrong, and every check here compares the compiler against itself. What it buys is a defect located to one program with an unoptimized reference to diff against.

Only what a device could observe is compared: the ordered sequence of logic and slot writes, and how the run ended. Registers, memory addresses, instruction counts and the faulting line differ legitimately between two correct builds, so a comparison including them would report non-defects, and a comparison that reports non-defects gets silenced. Values are compared as bit patterns, so NaN and signed zero survive.

Runs are bounded in segments rather than ticks. A segment is the stretch between two yields, which is one turn of the loop the source wrote. Device intrinsics lower to opaque calls the optimizer may neither delete nor reorder, so at every segment boundary the two builds are at the same place in the source however much code each emitted. That lets the world change during a run, and a fixture whose branches only open on a reading be driven across them.

| Guard | Against |
|---|---|
| A per-fixture floor on device writes | A fixture that stopped producing output passing on an empty comparison against an empty one |
| Floors derived from a checked-in baseline, regenerated only on request | A run silently lowering its own bar |
| A production below a minimum failing, on the way into the baseline and on the way back out | An undriven fixture blessing its own vacuous measurement, and a recording made before that minimum existed going on being read |
| Named tables of what `--no-optimize` refuses and what exceeds the editor's limit | A program that stopped compiling joining an excluded set nobody looked at |
| A missing baseline entry failing | A new fixture being compared with nothing under it |

Three fixtures are not compared at all: `--no-optimize` refuses them, because a recursive function's local needs a data region slot, one address serves every activation, and only SROA makes the local disappear. A named table records each, and each is held to still being refused and refused for that reason. Four more exceed the in-game editor's 128-line limit unoptimized and are compared anyway, since the limit is the editor's and the chip loads the text regardless.

A divergence fails, naming the fixture and the first differing write. Nothing is quarantined: there is no allowlist and no skip for a program known to diverge, so a miscompile stays red until it is fixed.

## Compiled against clang

The one comparison whose two sides were not both produced by this compiler. MicroC is a strict C23 subset, so clang compiles and runs the same source natively, and what the two runs write to their devices is compared over the surface described above. `devtrace.Diff` is a pure function of two traces and does not know how either was produced, so both paths reuse one seam rather than forking.

This establishes that the emitted program means what its source said, to the extent clang and this compiler are independent readings of it. That is the question nothing else in the suite answers.

The corpus is the fixtures plus three purpose-built programs: every one-argument machine function, the machine functions of more than one argument, and integer division, remainder and bit operations. Each is driven by a sweep that reaches either side of the domains its operations differ over.

Everything outside the program is shared rather than reimplemented, leaving the program's own control flow and arithmetic as the only thing that can differ:

| Surface | How the native build gets it |
|---|---|
| Device loads, stores, batch and slot forms, reagents | A request to the harness, answered by a chip running that instruction over the same housing |
| The machine's functions, `sqrt` through `lerp`, and `rand` | The same, one dispatch line per intrinsic, which is also where what each intrinsic means is written down |
| `__ic_hash` | Computed natively, because the compiler folds it and a host that asked would leave the folding compared against nothing |
| `__ic_isnan` | Computed natively, as C's own unordered comparison |
| A yield | A segment boundary, where the harness steps the world and decides whether the run continues |

Two housing properties are the exception, and both are refused rather than answered. `LineNumber` and `Error` read out of the chip in the housing instead of out of the world, and a native build has no chip: the machine answering its requests is the dispatch program, so the same request would read a dispatch line on one side and the traced program's counter on the other. Every other property the housing answers is held by whichever machine holds the housing, and so reads back symmetrically.

The program writing a request and the line reading it are two hand-written tables, so a test holds every request to carrying exactly what its line reads. Arguments land in `r1` upward and nothing clears the registers above them, so a request one operand short would run against a previous one's operands and answer a plausible wrong number, which would arrive as a divergence against the compiler.

A second implementation of a batch aggregate or of a store's read-before-write would be somewhere for the two runs to disagree without either program being wrong. The cost is that the numeric kernel is not independently checked: a bit-pattern comparison over two numeric libraries would report which library it ran on, and NaN payloads differ between them for reasons no compiler decided. What the comparison still establishes for the machine functions is that the emitted program reaches the right one with the right operands in the right order, the part belonging to the compiler.

### Where the two languages disagree

The chip has no integers. Every register is a double and MicroC's `long long` is one, where clang has real 64-bit integers. The two agree exactly while values stay inside 2^53 and no operation relies on behaviour C leaves undefined.

Two mechanisms keep the comparison inside that domain, and neither is a filter that can quietly widen. The native build carries `-fsanitize=undefined -fno-sanitize-recover=all`, so a program relying on signed overflow, an out-of-range shift or a division by zero stops there and is reported with the construct and its line, which makes the domain check generated from the program rather than maintained as a list. Past that, a magnitude beyond 2^53 is not detected but is not hidden either: the two sides compute different numbers and the comparison fails naming the write. The seeded worlds are written to keep the fixtures' arithmetic well inside the bound.

The native build is unoptimized and compiled with `-ffp-contract=off`, since the machine has no fused multiply-add and a contracted product and sum would be a different number from the two instructions the chip runs.

What a program means is the same on both sides down to the operand names. The families share several, C admits one enumerator per name in a scope, and MicroC resolves in that same namespace rather than by the position a name stands in, so a member whose bare name an earlier family took is written with its own family's prefix in both languages and the compiler refuses the bare spelling there.

### Guards against a vacuous pass

| Guard | Against |
|---|---|
| An absent `clang` failing rather than skipping | A run that compiled nothing reading like one that compared everything |
| Per-program floors on device writes, derived from a checked-in baseline | A program that stopped producing output passing on an empty comparison |
| A production below a minimum failing, on the way into the baseline and on the way back out | An undriven program blessing its own vacuous measurement, and a recording made before that minimum existed going on being read |
| A missing baseline entry failing | A new program being compared with nothing under it |
| A build failure on either side failing | A program dropping out of the comparison by not compiling |
| A sanitizer report failing | A program leaving the shared domain and being compared anyway |

`task clang:baseline` regenerates the floors. It happens on request and lands as a diff somebody reads; a run that merely passes cannot move one.

Two fixtures calling `__ic_sleep` are excluded by name: it ends a tick without advancing the program counter and is therefore a segment boundary a native process has no counterpart for. Each is held to still calling it, so the exclusion cannot outlive its reason. Nothing else is left out, and no fixture is left diverging.

## Generated MicroC programs

A random program generator feeds the clang comparison, so that what is compared stops being only the shapes somebody wrote by hand. Every generated program goes through the path above unchanged: clang builds the source, this compiler builds it, and `devtrace.Diff` compares the write sequences. A failing seed reproduces the program exactly.

**Bounding.** The generator stays inside the comparable domain by construction rather than by filtering. It carries a bound for every `long long` expression, computed from its operands, and admits no production whose bound passes 2^40, well under the 2^53 the machine counts to. The ceiling is pushed down into a production's operands rather than tested on its result, so an intermediate is bounded as well as an answer. Every object holds its value under a declared modulus that each store reduces by, so a loop, a call, and any number of turns of the control loop leave the bound standing. Divisors are literals or made odd. Shift distances are literals inside the range and their operands are masked non-negative first. Subscripts are masked into a power-of-two length. The one cast from a `double` goes through a guard whose comparison is false for a NaN and for both infinities, which keeps `-fsanitize=undefined` reporting nothing.

Two properties follow from that. Moduli and masks stay wide, because bounding a value is also telling the optimizer its range, and a range narrow enough to fit an int is one the optimizer retypes the whole chain to. Every global the program declares is also written by it, because a global nothing writes folds to its zero, and two folded zeroes under a division are a constant NaN the chip has no literal spelling for.

**Bias.** The generator is weighted toward where the defects have been: pointer differences, computed subscripts and pointer arithmetic within one object; non-finite values arriving through the world, then propagated through comparisons, calls and stores; one-bit values, as `bool` globals, `bool` arrays and comparison results; and control flow SimplifyCFG rewrites, meaning conditional stores, arms that merge, and a `switch` whose first arm stacks its label onto the one below.

**Construct set.** The generator is held to its own declared set in both directions. A corpus that never reaches a declared shape fails, and a shape reached under a name the declaration does not carry fails too. That stops a domain restriction from quietly narrowing until the campaign generates only arithmetic the optimizer never rewrites.

**Shrinking.** A divergence is reduced before it reports, by removing whole statements, functions and declarations and keeping any candidate that still diverges. A node goes only when nothing left names what it declared, so every candidate is a program both compilers still accept and whose integers are still bounded. The reduction is held only to producing a shorter program than it started from; how much it removed is reported per seed and across the corpus, and no particular share is asserted. The difference reported is the reduced program's own, recomputed after the search.

**Operand names.** The pools the generator draws logic types, slot types, batch modes and reagent modes from are hand-written, so each is checked against the prelude's own enumerators: an entry has to be the spelling its position resolves, under the number the tables give the member behind it. That stops a name written bare where its position takes the prefixed form from entering a campaign and reporting the same difference once per program. The pools draw the prefixed spellings deliberately, since the names a position spells with a prefix reach the comparison nowhere else.

### What a campaign cannot compare

A minority of generated programs are valid MicroC that clang builds and this compiler refuses, always because the optimizer rewrote the source into something the machine has no representation for: two loads sunk into one selected address, device operations merged over a runtime operand, or a library intrinsic formed from written operators. Each is named in a registry, counted, and printed whether or not anything met it. A wider gate builds a larger corpus without running any of it and fails once more than one program in 4 goes uncompared, because past that what a campaign covers is not what it reports. The share is measured there rather than in the campaign, which stops early on a divergence and can have attempted too few programs for a share of them to mean anything.

The tally settles nothing about an individual entry. The rarest of these shapes are drawn a handful of times across corpora far wider than any run the suite affords, so a zero beside an entry says the corpus did not draw the shape rather than that the shape is gone. What holds an entry to still naming a restriction is a witness: one checked-in program per entry, reduced from a generated one and rebuilt on every run. An entry whose shape gets fixed makes its witness compile, and that fails. Each witness is also built and run natively under the sanitizers, so an entry cannot go on claiming a program clang accepts once it has stopped being one.

Reachability from the generator is not checked and cannot be at this cost. A generator narrowed until it stops producing one of these shapes would leave its witness refused and its tally at zero, and nothing would say so. `task refusal:witnesses` re-derives the witnesses by searching generated programs and shrinking what it finds; an entry it cannot meet at all is one to delete.

A campaign compares what the chip computes and says nothing about whether the program fits on one. All but a handful of generated programs are over the editor's 128-line limit, which is neither a defect nor tolerated by accident: a generated program is written to reach the language's constructs rather than to fit a chip. How many of a corpus went past it is printed rather than asserted, since which seeds land inside moves with the generator. The hand-written fixtures are the only programs measured at the size the target really runs.

The memory array is the other thing the corpus does not reach. Under the outlining configuration every generated program the compiler builds emits a call, a save, a restore and a moving stack pointer, so the calling convention is covered. A generated program declares a few variables and no large array, so its frames grow into slots nothing else has spoken for and the collision the layout report warns about is never demonstrated. Both figures are printed on every run, and the data region is gated: a generator change that brought a frame within reach of a global fails here rather than quietly widening what the campaign is described as covering.

## Generated artifacts

The instruction tables, the operand enums, the C prelude and both clangd argument files are generated from the game's decompiled assembly and checked in, and the device roster from the game's Unity assets beside them. `task check:codegen` regenerates every generated artifact from its checked-in inputs, with no game download, into a scratch tree, diffs that against the working tree, and fails on drift. What it reports is what the generator would write today, independent of what is committed and of whether an output is tracked yet. Which files that covers is derived from each generator's own flags rather than listed in the gate, so an output added there is diffed without a second edit. A path flag whose help says neither what it reads nor what it writes fails, and so does a file written to a path no flag names.

The same gate states where a derived file may sit: not in a directory Go compiles as a package alongside hand-written source. The package directory is the unit everything a mixture would mislead works in, since `go:embed`, the build, the linter's own exemption for generated files, and the coverage profile each take a directory as one thing with one owner. A directory Go compiles nothing in is not scanned, which leaves the corpus's argument file among the programs a C driver expects to find it beside. A file is read as derived two ways, neither of them a path written into the gate: a generator's own flags declare it, inputs as well as outputs, or it opens with the generated banner, which reaches an artifact vendored in by hand and one written by a generator the gate does not build.

That catches a generator whose output moved, and a hand edit to a generated file with it, since the comparison is against the tree rather than a regeneration over the top of it. Comparing two independently checked-in artifacts against each other catches more still: header enumerators against the four operand tables, header prototypes against the intrinsic definitions signature by signature, the corpus's argument file against the one beside the header, and the chip driver's reagent seeding verbs against the reagent modes the tables declare. Each holds in both directions.

What extraction itself asserts, what a decompile lets it be held to, and what the prefab roster rests on instead are in `building.md`. One artifact sits outside all of it: the digest fingerprinting the game declarations the hand-written Go derives from is in no gate, and nothing compares what it writes. It reports when the game changed, not whether the Go was right.

## Measuring the corpus

No document describing the compiler as it ships states a figure measured from the corpus. A figure that does moves with every fixture, every pass and every codegen change, and one written into prose is stale by the next commit whatever gate is put behind it. What those documents state is the direction of a trade and the reason for it; the figures live in a command. `future-work.md` is the exception and has to be: its figures measure passes that do not exist, so no command can print them and no gate can recompute them.

`task corpus:measure` compiles every fixture in every mode and every configuration and prints one report: the corpus totals, what each fixture spends of each of the three editor limits, which limit binds on it and how far it spilled, what `--readable` and `--numeric` cost, what the optimizer skipped comes out at and which fixtures that puts over a limit, and the corpus under each pass and inlining configuration the compiler does not ship. It is a pure function of the tree, with no wall clock, no path and no map order, so two runs diff to what moved between them.

The relations the compiler document argues from are asserted instead, because a relation is a property and survives a corpus that moved:

| Held | Why it is not a figure |
|---|---|
| The option-struct build equals the command-line build, mode for mode | The pass and inlining configurations carry no flag, so nothing else says a counterfactual describes the shipped pipeline rather than a second one the test assembled |
| The late pipeline is strictly smaller than the shipped one | It is what the pre-link spelling's foreclosure costs |
| Folding rotation's guard lands strictly between the shipped corpus and the rotated one | It recovers part of rotation's cost, not all and not none |
| A trailing instcombine with no rotation in front of it moves nothing | The recovery belongs to the rotation rather than the pass behind it |
| No call-site threshold moves the fixture with a `dev` parameter | A device position needs a literal at the call site, so no count can give that function a shared body |
| `--numeric` moves bytes and never lines | A name and its integer occupy the same one operand |
| `--readable` moves no line | The chip cuts a line at its first `#`, so the annotated program is the shipped one on the same lines |

Nothing holds the language document to the compiler. Its refusal and warning tables are prose that no test reads, so a row that goes stale goes stale in silence. The refusals are held on the Go side instead: every one the front end registers must be reachable, and a refusal must read the same wherever its construct stands.

One table the documents describe is held to the source rather than to prose: every fixture named as behaviourally asserted has to be a fixture the corpus carries and a test this package declares.

## Static analysis

`golangci-lint` at a version pinned in the Taskfile rather than the workflow, so CI and a local run cannot disagree and a tool release cannot break the default branch on its own. Issue caps are raised explicitly, since the default truncates at three issues per linter, which reads as clean when it is not. Enabled are `errcheck`, `exhaustive`, `forcetypeassert`, `govet`, `ineffassign`, `revive`, `staticcheck`, `unused`, `whitespace`, `exptostd` and `usestdlibvars`, plus `gofmt`. A `gopls` diagnostics pass runs afterward whatever the first half found, and the verdict is taken over both: as two commands the second would be a gate the first one failing switched off.

`exhaustive` is armed with `default-signifies-exhaustive: false`. A `switch` over an opcode or a logic type does not become exhaustive by having a `default`; every case must be named, or the switch carries an explicit ignore directive with a reason. Given how much of this compiler switches over generated enums, that setting makes a game update adding an enumerator a compile-time conversation rather than a silent fall-through.

Four more lint targets cover the build machinery. `task check` runs the two shellcheck ones; the actionlint and hadolint targets stay out, since actionlint comes from a tools target `task check` does not run and both read CI's own plumbing. The four are shellcheck over the scripts, actionlint over the workflows and the shell inside every `run` block, hadolint over the `Dockerfile` and the shell inside every `RUN`, and shellcheck again over the command blocks in the Taskfile, several of which are gates. Shellcheck and hadolint each run from an image pinned by digest, so all four need Docker and only actionlint needs a linter installed.

That last one takes its blocks from `task --dry --verbose` rather than from parsing the YAML, so what is linted is what Task would execute: templating expanded, and a variable holding a list rendered into the block that carries it. `--verbose` is what makes it read the whole file, since a `silent: true` target prints nothing under `--dry` alone and every one of them would have been left uncovered with nothing saying so. Dry-run output belonging to no command block fails rather than being linted as shell or passed over, and a run that rendered no block at all fails too.

One piece of shell falls outside all four: a composite action's own `run` block. The one in the tree is therefore a wrapper around a script and nothing else.

## What this does not test

### The generated assembly corpus establishes nothing about the compiler

Its programs are generated assembly rather than compiler output, which is how it reaches the instruction families the backend never selects. Nothing about a lowering follows from it. Compiler output does run on the chip, in the fixture corpus, the equivalence comparison and the clang comparison, so corpus coverage and compiler coverage are two different numbers and neither stands in for the other.

### What the clang comparison does not reach

Compiled output is compared against clang's reading of the same source. That comparison is what answers whether a program means what its source said. Four things stay outside it.

| Outside | Why |
|---|---|
| The chip itself | Tick boundaries, the 128-instruction budget, `sleep`, the line and byte limits, register pressure, and the data region sharing one array with call frames. C has no notion of any of them |
| The numeric kernel | Both sides ask a chip for `sqrt` and the transcendentals, so what is compared is which function the program reached and with what, never what the function computes |
| The world model | Both sides read one seeded world through one chip, so a batch aggregate answering the wrong thing answers it identically to each |
| Where the two languages genuinely disagree | The comparison stays out of those places rather than arbitrating them, which is why the domain analysis above is necessary rather than a caveat |

### Nothing validates against the game

The harness is the game's own code compiled outside the game, which is as close as this tree gets. It is not the game. Everything the slice left behind is world simulation, and a test sets each of those values by hand, so a run establishes what the machine does given a world rather than that the game ever builds that world. No path to reading state off a chip running in-game has been established, so nothing checks the harness against the thing it stands for.

The C# digest notices when the game changes a declaration the hand-written Go derives from, and a second digest checked in beside the slicer holds every construct the slice consumes, and every one a hand-written stand-in answers for, to the text it had. So a game update reaches the chip only through a human reading a diff. Neither can notice that the reading was wrong when it was first written: both builds agree, both digests stay quiet, and the slice goes on cutting what it always cut.

### A clean compile is not a checked device surface

The compiler holds a batch instruction's prefab operand to the device roster where that operand folds at compile time, and a pin-addressed access where the program declares what that pin is wired to. Nothing else about a program's device usage is checked. Most fixtures reach their devices through a pin they say nothing about, so a compile with no warning establishes nothing about whether they read a property the device they drive actually answers.

The remaining gap is not a missing pass, and the attribute does not close it. Which prefab sits on `d0` is a property of the world the chip is placed in; a declaration states what the programmer believes is there, and no analysis and no emitted instruction confirms it, so a wrong declaration relocates the mistake rather than catching it. The seeded worlds the comparisons run in do not close it either: their devices answer every property in both directions, without which no fixture could be driven at all, so a program reading something no real device answers reads it identically on both sides and the comparison passes.

### Named coverage holes

**Nothing computes what the math intrinsics compute.** Both sides of the clang comparison ask a chip for the answer, so a transcendental computing the wrong thing computes it identically for each, and the generated corpus runs them on the same chip. Every machine function the corpus does not exclude is emitted into it, which establishes that it assembles, runs and does not fault, and nothing about what it answers. Every exclusion is held to naming a reason, so one kept out with none is a failure rather than a quiet hole.

What is covered is which function a program reaches and with what. Every machine function is called from a C source at an input whose answer is written out by hand, such as a midpoint rounding to even rather than away from zero, `clamp` as a minimum of a maximum that does not order its bounds, and `lerp` clamping its interpolant instead of extrapolating. A function the harness dispatches that no such program calls fails. Programs written for the clang comparison then run all of them over a swept reading, crossing into the inverse trigonometry's undefined range and back.

**`bool` reaches no program in the generated assembly corpus.** That corpus is assembly, which has no types, so nothing there declares one and it says nothing about how a one-bit value is held or tested. Everywhere else it is covered. Every combination of two bools is run through four device writes and asserted, which catches a complement folded into a whole register. A bool read as a signed number is executed on both sides of the value. Mutual recursion returning `bool` is executed. Fixtures in the corpus declare one. Every generated program declares a `bool` array and writes it, and `bool` is one of the three kinds each of its scalar globals is drawn from.

### Further holes

**A fold the peephole table is missing costs an instruction and nothing reports it.** Every pair in the complement table is executed and asserted to answer exact opposites, so a wrong entry fails. Nothing enumerates the pairs that could be there and are not. One that is missing emits two instructions where one would do, against both the 128-line and the 128-instruction budgets, and the suite is silent about it. The same holds for the map of which re-tests are worth folding and for the choice of `select` over a branch, both of which still rest on assertions about instruction text.

**`rmap` reaches nothing but a fault.** No MicroC intrinsic selects it, so the only place it appears is the generated assembly corpus, where its device operand is an unset pin and what is reached is the fault that raises. Its return value would also need the ingot walk the slice left behind, and no verb seeds the table standing in for it.

**Almost no fixture has a behavioural assertion.** Nothing states in a test what any of them is supposed to compute; each carries a comment describing its control loop, which is an oracle written in prose and read by nobody. What the clang comparison establishes for them is narrower: the emitted program agrees with a second compiler's reading of the source. A fixture whose own source drives the wrong pin or inverts a condition still passes, because both readings of a wrong program are wrong together.
