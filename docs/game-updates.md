# Reviewing a game update

Three things in this repository derive from the game's own build, each failing differently when the game moves.

| Derived | From | When the game moves |
|---|---|---|
| The machine tables, the C prelude and the device roster | `Assembly-CSharp.dll` and the Unity assets | `task isa` regenerates them, and extraction refuses to write a table it can no longer determine |
| The chip every test asks what the machine does | The decompiled `ProgrammableChip` and the device surface around it, cut by `tools/chipgen` | The slicer refuses a construct it can no longer locate, and refuses one whose text is not what `tools/chipgen/slice.digest` records, naming each. Nothing is written until both hold |
| A handful of facts written as Go | Declarations no extraction reaches: the machine's shape, the exception names, the never-emit list, the operand conversions the backend reasons about, and the editor's limits | The machine's shape and the editor's limits are held to the game's own declarations and fail. An exception name the chip prints and the Go cannot resolve stops the read that meets it. Nothing else notices, and this procedure covers the remainder |

This procedure reports which game declarations changed since the pinned build, and which Go each one backs. Whether a change matters is settled by a human reading a diff.

Two digests answer for different things. `gamesrc/csharp.digest` fingerprints the declarations the hand-written Go transliterates. It lives under the gitignored decompile, so it is regenerated with the tree it describes, and step 1 below copies the old one aside first. Its baseline is therefore held by hand: it is in no `task check` target and nothing compares what it writes. `tools/chipgen/slice.digest` fingerprints what the slicer consumes and is checked in, so it can gate. It is the only record in the repository of what the game looked like, so a slice can be held to it without anyone keeping a copy. Nine of the hand-written constants also have a gate of their own, listed below.

## The pin

Everything starts from the depot manifest gid, which fixes the exact bytes fetched from Steam.

| Where | What |
|---|---|
| `Dockerfile`, `ARG STEAM_MANIFEST` | The pin itself. Changing it moves every extraction and every digest |
| `internal/isa/isa.json`, `internal/isa/devices.json`, and `ic10.ManifestID` | The manifest the checked-in tables and the device roster were extracted from |
| `gamesrc/csharp.digest`, `manifest` line | The manifest the digest on the machine was taken from |

All three should name the same gid. `task isa:manifest` asks Steam what it serves now, fetching the depot's file index and no content. A gid different from the pin means the game shipped a build the repository has not looked at. `task check:game-pin` asks the same question as a gate, and a weekly workflow runs it.

## The procedure

**1. Fingerprint the new build.** This does not move the pin; it rewrites the digest from whichever manifest is named. Nothing under `gamesrc/` is in the repository, so the old digest has to be copied aside before it is overwritten.

```
cp gamesrc/csharp.digest /tmp/csharp.digest.old
task isa:csharp MANIFEST=<gid>
diff /tmp/csharp.digest.old gamesrc/csharp.digest
```

Every changed line names one C# declaration. The `type` and `backs` lines above it name the Go the declaration backs. No change means nothing in the mapped types moved.

**2. Read the C# behind each changed line.** The digest reports that a declaration changed, never what changed in it. Produce both builds and diff them. The export removes nothing, so a directory left from an earlier comparison would carry files neither build wrote:

```
rm -rf gamesrc/source-old gamesrc/source-new
task isa:csharp:source MANIFEST=<old-gid> GAMESRC_DEST=gamesrc/source-old
task isa:csharp:source MANIFEST=<new-gid> GAMESRC_DEST=gamesrc/source-new
diff -ru gamesrc/source-old gamesrc/source-new
```

That target writes the fingerprinted types alone. `task isa:csharp:source-full` writes the whole decompiled assembly, the tree the chip is cut from, and a change outside the mapped types has to be read there.

**3. Act on each change**, using the table below.

**4. Move the pin** once the Go agrees with the new build. Edit `ARG STEAM_MANIFEST` in the `Dockerfile`, then `task isa` to regenerate the tables. `task check:codegen` then holds the generated Go to the checked-in `internal/isa/isa.json` and `internal/isa/devices.json` and never reads the assembly. What holds that JSON to the assembly is `TestTheGameStillYieldsTheCheckedInISA` in `tools/isagen/gamesource_test.go`, which `task check:gamesrc-gates` runs.

`task gamesrc` rebuilds both decompiled trees and the digest from the new pin, and moves each in only once all three have succeeded, so a failed run leaves the machine with the decompile it had rather than with half of two. An interrupt during the move leaves the rest of the new set staged: `task gamesrc:finish` completes it, and `task gamesrc` runs that before it rebuilds anything. The digest is taken down before either tree moves and put back after both, so while a move is outstanding there is no digest at all rather than one naming a build the trees beside it did not come from.

Then the chip. `task chip:build` re-cuts before it compiles and holds the cut to `tools/chipgen/slice.digest`, refusing a decompile that digest does not describe. The refusal names changed constructs by file and signature, up to 40 per section, then reports how many more there are (`tools/chipgen/digest.go:254`). Read the C# behind every name, then take the new fingerprint and read that diff too:

```
go run ./tools/chipgen slice --update-digest
```

Only then does `task chip:build` run. Without the rebuild every chip-backed test goes on answering for the old game.

## Changed declarations

| Changed declaration | Backs | What to do |
|---|---|---|
| `ProgrammableChip._Registers`, `_Stack`, `_StackPointerIndex`, `_ReturnAddressIndex` | `ic10.NumRegisters`, `ic10.NumMemorySlots`, `ic10.RegSP`, `ic10.RegRA` | Array lengths and indices rather than named constants; read the `new double[N]`. Gated, below |
| `CircuitHousing.Devices`, `CircuitHousing.RUN_COUNT` | `ic10.NumDevicePins`, `chip.InstructionsPerTick` | The pin count, and the instruction budget one tick buys, which is also the reason `sleep` on line 0 is a hazard. Gated, below |
| `InputSourceCode.MAX_FILE_SIZE`, `MAX_LINES`, `LINE_LENGTH_LIMIT` | `emit.MaxBytes`, `emit.MaxLines`, `emit.MaxLineLength` | The whole backend cost model is built against these. Gated, below |
| `AsciiString.ParseLine` | The line charge in `internal/emit` and `internal/difftest` | The cut every line reaches the editor's grid through, so a wider line is charged and reported at the cut rather than at its emitted width. Not gated: the gate below reads `LINE_LENGTH_LIMIT`, and `InputSourceCode.Paste` and `InputSourceCode.Copy` each pass this a literal 90, so a change to the cut or to the length it is given is a digest record and nothing more |
| `ProgrammableChipException.ICExceptionType` | The `chip.ExceptionType` names | The name crosses the protocol and the ordinal does not, so renumbering reaches nothing. An added or renamed member is a name the Go cannot resolve, so the first read that meets it stops instead of taking it for a neighbour |
| `_LineOfCode`, and the constructor of any `_<MNEMONIC>_Operation` | The never-emit list in `internal/ic10` | Four branch forms are refused because their operand-count check and their operand read disagree, and `sla` because it constructs the same operation `sll` does. Both are properties of this build, and an arity change can make one of those five compilable. The rest of the list, the eighteen relative branch forms and `hcf`, is refused for reasons independent of the build (`internal/ic10/quirks.go:29`) |
| `ProgrammableChip.DoubleToLong`, `LongToDouble`, and the `GetVariableLong` and `GetVariableInt` an operand reaches them through | The operand modulus and the shift window in `internal/isel` | A bitwise value operand is reduced modulo 2^53 and a shift distance faults outside ±2^31; selection refuses a constant against both. The 53 and 54 bit widths are necessary and differ from each other |
| `ProgrammableChip.SetSourceCode`, `_LineOfCode` | The line and byte accounting in `internal/emit` | A line is cut at its first `#` in the `_LineOfCode` constructor, before the chip decides what the line is. An annotated program is therefore the shipped one on the same lines, and a width violation is raised against the instruction rather than the line |
| `DoubleValueVariable`'s constructor | `ic10.Unreadable` | The two values the operand parser cannot reproduce: a NaN, reported as no value at all, and a negative zero, read as `+0`. Neither is derived, so a change here surfaces in `internal/chip`'s literal tests rather than in a reading |

Nine of those constants have a gate rather than only a prompt. `tools/isagen` reads the register file size, the memory array size, the pin count, the two fixed register indices, `RUN_COUNT` and the three editor limits out of a decompiled tree and holds each to the Go constant that mirrors it (`tools/isagen/limits_test.go:39`). Without a tree to read it fails rather than skipping, naming `task gamesrc` as what writes one; `IC11C_GAMESRC` points it at a named tree, so the gate can run against the new build before the pin moves. A declaration it can no longer find fails too, since that is the case where the constant beside it stopped being checked. The byte charge the emitter computes is the shape of a loop rather than a number and is not covered.

The gate prefers the whole decompiled assembly and falls back to the fingerprinted subset. The subset answers for these nine constants and for nothing else `tools/isagen` reads. `task gamesrc` writes both; the subset alone is not enough for the rest of that package's gates.

A change with no obvious effect is still worth a note in the commit that moves the pin.

## Digest formats

The C# digest opens a section per type:

```
type <hash> <fully qualified C# type>
backs <the Go files whose behaviour derives from it>
note <what about the type the Go depends on>
	<hash> <declaration, or nested type/declaration>
```

A declaration's hash covers the declaration and everything inside it, with layout whitespace removed, so moving a method to a different nesting depth is not a change. Declarations are sorted by path, so moving one within its type is not a change either. Enums are the exception and are fingerprinted whole rather than split: their member order is their data, and sorted per-member records would hide a reordering. A member's path is its normalized signature, so a changed signature reads as one declaration replacing another rather than as the same declaration with a new hash.

The mapping lives in `gameTypes` in `tools/isagen` and is emitted into the digest. Add an entry there when a new Go file starts deriving from a game type, and only then, since the same list decides which types `task isa:csharp:source` writes out for step 2.

The slice digest carries one record per line, each the leading eight bytes of a hash over the text with comments removed and layout whitespace collapsed.

| Kind | Covers |
|---|---|
| `shape` | What a type declares: its keyword, base list and sorted member signatures, and no body |
| `cut` | The whole text of one declaration the slicer copies, rewrites, or reads and leaves behind |

Those are the only two record kinds. Two header lines carry the rest of what the file gates: `attributes` names the attributes dropped from the lifted declarations, and a set that no longer matches is its own refusal; `records` holds the record count, so a truncated file is refused rather than read as a shorter list of things to check (`tools/chipgen/digest.go:184`).

A record reports that something changed, never what, so each one is a construct to read the C# behind. A `shape` moving alone is the game growing members around the ones the slice takes; a `cut` moving is one of the constructs the compile unit is built out of.

Its reach is deliberately wider than the compile unit. `Thing.ColorState` has a branch the slicer reads and never emits, because what stands in its place hardcodes a claim about what that branch collapses to. Changing one line inside it moves a record and not one byte of the compiled oracle, so a claim the game falsified without moving a signature is still reported.

## Coverage limits

The chip's own bodies are not on the C# digest's list. The slice digest covers those, including constructs `gameTypes` never names: the four device permission predicates, the four batch reducers, both pairs of logic accessors, and the power and slot members beside them. A body that is still found and no longer says what it said stops the slice rather than reaching the chip.

The slice digest also covers the constructs the slice deliberately carries no text for. A hand-written stand-in is a claim about the game that nothing inside a slice can falsify: it says what the game does with something and puts a field, a constant or a narrowed interface where that something was. Each one names the construct it answers for, and the slicer reads it without emitting it. A game update that rewrites one of those stops the slice with its path in the refusal, and the stand-in has to be re-read before the slice can proceed.

| Not covered | Why |
|---|---|
| Whether the Go was right in the first place | A diff shows what changed between two game builds. A fact misread when it was first written down leaves both builds agreeing and nothing here noticing |
| Behaviour outside the mapped types | Anything `gameTypes` does not name is invisible. A game update moving logic out of `ProgrammableChip` into a type nobody listed leaves the digest quiet. Unlike the ordering exemption below, that is not deliberate |
| Anything not in `Assembly-CSharp.dll` | `ic10.HashName` is `UnityEngine.Animator.StringToHash`, native to the Unity runtime and inferred here as CRC-32 read as a signed 32-bit integer; the transcendentals come from the .NET runtime's C library; `rand` draws from `System.Random`, whose sequence belongs to whichever runtime the unit is compiled under |
| Whether the device roster still describes the game | The roster is extracted from the Unity assets, so it moves with `task isa` and needs no reading here. What the digest cannot see is a property whose behaviour changed while its declaration did not |
| Order within a type | Deliberate, as above, everywhere but enums |
| Whether a change matters | Every record is a prompt to read rather than a verdict |
