# Compiler

How ic11c turns MicroC into IC10. Machine facts referenced here are documented in `target.md`.

## Pipeline

| Stage | Contract |
|---|---|
| Parsing | Source to AST, through a tree-sitter grammar. It is the only parse path |
| Semantic analysis | Types, subset enforcement, constant evaluation, device and logic-type resolution, call graph |
| IR generation | Typed AST to LLVM IR. Locals become allocas. Debug locations attach here and are threaded through every later stage |
| Optimization | The pass pipeline below, run over the in-memory module |
| Verification | Every load and store pointer traces to a unique alloca or global |
| Instruction selection | LLVM IR to machine IR over IC10 opcodes with virtual registers. Phi nodes become parallel copies |
| Literal materialisation | A value with no literal the chip's parser reproduces becomes the arithmetic that computes it |
| Register allocation | Linear scan over live intervals, spilling to the data region |
| Peephole | Rewrites what two instructions compute into one |
| Emission | Label resolution, byte accounting, size reporting |

The peephole sits between allocation and emission because both neighbours constrain it. Its register rewrites ask whether two operands are the same storage, which for a phi lowered to copies over virtual registers is the allocator's answer and is not visible before it. A branch target is an absolute line number, so removing a line has to happen before labels resolve.

LLVM supplies the mid-level optimizer only. Instruction selection and register allocation are ours; reaching LLVM's would require a backend target in C++ and TableGen.

IR construction and optimization go through CGo bindings to libLLVM. The module is built, optimized, and inspected as one in-memory object; no stage serializes IR to text or reparses it. The mid-level pipeline needs no registered target, and none is registered. The bindings fix the libLLVM major version at build time; toolchain requirements are in `building.md`.

## Optimizer pipeline

`thinlto-pre-link<Oz>` with loop unrolling disabled and a trailing function-level DCE. `internal/llvmopt` holds the exact spelling.

`-Oz` is the base because lines are the budget and it is the stock level that gives up the most speed for size. Vectorization is not what choosing it buys: no target machine is registered, so the cost model reports no vector register, and the loop and SLP vectorizers rewrite nothing at any level, including when run directly over a four-wide pattern with nothing else in front of them. Within `-Oz` the largest win is SROA and mem2reg: IR generation puts every local in an alloca, and the data region is reachable only by `poke` and `get db`, so an unpromoted local costs a memory instruction per access. GlobalOpt pays for the same reason, since a global has internal linkage and one accessing function, so it is demoted to an alloca and then promoted to a register, taking its data slot with it.

Three deviations from stock, each measured on the fixture corpus. No stock pipeline knows the cost model, which is lines of IC10 text against a 128-line budget with 4096 bytes behind it, and none knows that the register file holds nothing narrower than a whole double.

| Deviation | Why |
|---|---|
| Loop unrolling disabled | `-Oz` still fully unrolls a small constant trip count, which costs the corpus lines. The shape of the trade decides it rather than the margin: an unrolled loop grows with its trip count while the cost of refusing is a fixed couple of lines of loop overhead, which is the side to be on under a 128-line ceiling |
| `thinlto-pre-link<Oz>` rather than `default<Oz>` | Selects module simplification without the module optimization pipeline that runs after it. No link follows and nothing here is built for ThinLTO; the alias is used for where it stops. Size does not favour it: the late pipeline comes out marginally smaller, and that margin is what the foreclosure below costs |
| A trailing `function(dce)` | Dead argument elimination runs near the end of module simplification, and out-of-line functions have internal linkage, so it drops a parameter nothing reads. What follows it is module-level, and nothing behind it rewrites a function body once the late pipeline is omitted, so the argument the caller computed for that parameter is emitted and never read. That is two lines per call site for one dropped argument. The corpus is unmoved and is not claimed to move, since no fixture passes an argument nothing reads |

This section states the direction of each trade and no figure for it: a figure measured from the corpus moves with every fixture and every pass, and one written down here is stale by the next commit. `task corpus:measure` compiles the corpus in every configuration named here and prints what each costs, so two runs diff to what moved. The relations argued from are asserted instead, by `TestCorpusMeasurements` in `cmd/ic11c`, since a relation survives a corpus that moved. The configurations reach the pipeline through `llvmopt.Options` and `irgen.Options`, neither of which carries a command-line flag.

### What the pre-link spelling forecloses

The late pipeline it omits adds vectorization, late unrolling, and switch lookup tables. Vectorization is unreachable at any level without a target machine, so the lookup table is the only one of the three that the spelling actually withholds.

A lookup table costs more here than the branches it replaces. `get` takes its address operand in a register, so a global array read at a computed index emits a `poke` per element and a `get`.

The spelling is what withholds the transform: every `simplifycfg` the pre-link pipeline runs is spelled `no-switch-to-lookup`, and the single instance enabling it belongs to the module optimization pipeline the spelling stops before. Registering no target machine withholds nothing here and is not a second reason: the base cost model builds lookup tables, and the `n64` layout makes the table's `i64` element type legal, so a module carrying a dense `switch` gets a table out of `default<Oz>` with no triple and no target machine at all.

The margin runs against the spelling, so what it is taken for is a program shape rather than a figure. No program the compiler has compiled reaches the transform, and the type is what stops it: a `switch` tag is a `double` like every other value, so a `switch` lowers to a chain of comparisons and no LLVM `switch` instruction is built for the transform to convert. No fixture in the corpus produces one. The spelling holds against a later lowering that does build one.

Re-running loop rotation with header duplication is the deviation that is not taken. Stock disables duplication because duplicating a guard costs size on a conventional machine, and it costs size here too: rotation alone is larger than the shipped pipeline in both lines and bytes, and a trailing instcombine folding the duplicated guard into the preheader recovers only part of that. The same instcombine without rotation in front of it changes nothing at all. Even folded, the cost is spread across several fixtures rather than carried by one.

## Type representation in IR

The machine has one value type and the IR has one too. Every register and memory slot holds one whole IEEE double, and `long long`, `bool`, and `double` all lower to LLVM `double`. A `dev` lowers to nothing at all: a device position is resolved when the chip assembles the line, so every device folds to a constant argument of the intrinsic call that uses it.

The choice buys agreement between the optimizer's model and the machine's about the values a register can hold. A register holds NaN and both infinities, and no integer is any of the three, so over an integer type LLVM is entitled to `x - x → 0`, `x * 0 → 0`, `x / x → 1`, and the rewriting of `a >= b` into `!(a < b)` with the successors swapped. Every one of those is wrong on this machine, and lowering as `double` withholds all of them at once.

The source language does not change with it. `long long` still divides toward zero, still takes a remainder carrying the dividend's sign, and still has bitwise operators and shifts; each is written out of the operations the machine performs for it.

| MicroC operator | IR | Why |
|---|---|---|
| `+` `-` | `fadd`, `fsub` | The machine's `add` and `sub` are double arithmetic. Neither produces a negative zero from operands that are not one |
| `*` | `fmul`, then `fadd 0.0` | The machine's `mul` is double arithmetic. Operands of disagreeing sign give a negative zero product where C's integer multiply has one zero, and the added positive zero clears it |
| `/` | `fdiv` under `llvm.trunc`, then `fadd 0.0` | The machine's `div` does not truncate, and C's integer division does. `llvm.trunc` is the machine's `trunc` exactly, NaN and infinity included, so it needs nothing hidden from the optimizer. A quotient in (−1, 0) truncates to −0, which the added zero clears |
| `%` | `a - trunc(a/b)*b` | The machine's `mod` adds the divisor back to a negative remainder, which is C's answer for a positive divisor and not for a negative one. The closing subtraction already leaves a zero positive |
| `&` `\|` `^` `~` `<<` `>>` | a call to `__ic_and`, `__ic_or`, `__ic_xor`, `__ic_not`, `__ic_shl`, `__ic_shr`, operands and result `double` | The machine's instruction reads its operands through a conversion to a signed int64 which faults for a value outside that range, and a register holds NaN and both infinities. LLVM's `and`, `shl` and the rest cannot fail, so the optimizer would move one above the test bounding its operand; a call withholding `speculatable` is the only spelling that denies it. Nothing else is withheld: the calls are pure, and IR generation folds a pair of constant operands itself because the opacity blocks the optimizer's own fold |
| unary `-` | `fsub 0.0, x` | C's negation of an integer zero is a positive zero. `fneg` is a sign flip and would answer −0, which a device write then publishes |
| a comparison read as a value | `fcmp` under `uitofp` | The machine's set instructions already produce 0 or 1, so the widening is free. InstCombine turns it into `select %c, 1.0, 0.0`, which is free for the same reason |
| a cast of a `double` to a `long long` | `llvm.trunc`, then `fadd 0.0` | The rounding alone. Both types either side of the cast are the same `double`, so whether anything rounds is read off the operand's MicroC type rather than off the IR. A reading in (−1, 0) truncates to −0, cleared the same way |

The added zero is an ordinary `fadd` rather than an opaque call, so the optimizer folds it away wherever it can prove the value is never −0. It must sit directly on the result whose sign is in question, never ahead of the rounding or the multiply that produced it.

An explicit cast whose operand is already a `long long` emits nothing. The conversion analysis records on a cast's operand is the cast's own target, so it cannot answer that question here; the operand's own type is what does.

A `getelementptr` index and a `switch` tag are integers in LLVM whatever the value types are, so both convert through `fptosi`. The register already holds the whole number, and selection treats the conversion as the register it names. Array element type is uniform, so every `getelementptr` stride is a single constant that the backend divides out and folds away.

The residual gap: the optimizer folds integer constants exactly to 2^63 while the machine is exact to 2^53. Programs relying on values in that band compute different results at compile time than at run time.

IR generation also contracts to set a data layout declaring i64 native, `e-p:64:64-i64:64-n64`. The `n64` field is the necessary part. Without it LLVM believes no integer width is native, and InstCombine narrows a value whose range it has proved to the smallest integer type that holds it. The register file has nothing narrower than a whole double, so a narrowed type is one the backend cannot select and would have to widen straight back. The rest of the layout is inert, since nothing serializes bytes.

### What the lowering costs

Three source shapes cost more than they would over an integer type, because the fold that made them cheap needs a fact about integers that a `double` does not carry.

| Shape | Why it costs | What to write instead |
|---|---|---|
| `x % 8` and every other remainder by a power of two | Where the optimizer can prove `x` non-negative the remainder is a bit mask over an integer, which is one instruction against the division, rounding, multiplication, and subtraction it otherwise takes. Proving it needs integer range analysis | Mask explicitly: `x & 7`. It is the same answer for a non-negative `x`, and one instruction |
| `lo <= a && a <= hi` | Over an integer the pair folds into one unsigned comparison, which the backend rewrites into a subtraction and two fused compare-and-branches. There is no unsigned `double` to fold into, so both comparisons materialise and are combined before the branch | Nothing in the source is cheaper. The cost is one instruction per range check |
| a counting loop whose trip count and body are both compile-time constants | Scalar evolution closes a recurrence over an integer type and not over a float one, so the loop runs instead of folding to its answer. An eight-iteration bit count is one `move` over an integer and a ten-line loop here | Write the answer, or a `constexpr` that computes it |

Recovering the first two is a matter of establishing non-negativity in instruction selection rather than of the lowering; `future-work.md` carries what each would need.

## Ordered comparisons and NaN

The machine's ordered comparisons are all false for a NaN operand, so a predicate and its apparent negation are not complements. That is what LLVM already believes about a `double`, which is every MicroC value. An `fcmp oge` and an `fcmp olt` are not complements there either, and the canonicalisation InstCombine does perform, inverting `one`, `ole`, and `oge` in branch position into `ueq`, `ugt`, and `ult` with the successors swapped, is sound because it swaps the successors. Instruction selection undoes it: the machine has an instruction for each ordered predicate and none for an unordered one, and a branch's two successors are its to swap back. In value position the same negation costs the one instruction that complements a truth value, which a `select` reading the comparison absorbs by trading its arms instead.

Two float predicates have no single instruction. `one` is a less-than or a greater-than, and `uno` is the machine's NaN test over each operand; each becomes a two-step plan, and the negation of each is that plan complemented.

`llvm.minnum` and `llvm.maxnum` are refused instead of selected. They answer with whichever operand is not a NaN, where the machine's `min` and `max` propagate it, so the instruction that looks like each of them computes something else. `__ic_min` and `__ic_max` reach the machine's own behaviour directly.

Equality needs nothing. `beq` and `bne` are complements whatever the operands are, so inverting one is sound and the optimizer may. `a != a` is the classic NaN test and keeps its meaning, since LLVM turns `fcmp une a, a` into the unordered test it is; `__ic_isnan` is the instruction that answers it directly.

### The one-bit truth value

A one-bit operation is held apart from the whole-register operation of the same name, because LLVM reads the bit as a signed value of 0 or −1 where the machine holds it as 0 or 1. An `add` and a `sub` are exclusive or; a complement is the general `xor` against 1 rather than the machine's `not`, which would complement the whole double; a signed ordering has its predicate reversed, where an unsigned one already agrees and only has to be spelled signed; a signed reading costs a `mul` by −1; and the narrowing back to one bit costs an `and` by 1, which is the low bit rather than whether the value is zero. Nothing in the source produces one, since InstCombine folds every one-bit comparison into the bitwise operation it already is, but a pattern may not assume the machine's own width.

## Undefined values

A local the optimizer keeps in a register has no storage to have been zeroed. Reading one before it is written is undefined in C, and the consequence here is not a wrong number: the read becomes `undef`, and the optimizer is then entitled to fold anything reached through it, including deleting the device stores a comparison against it guarded. The emitted program does nothing and reports nothing.

Analysis therefore holds every register-class local to definite assignment, which `microc.md` states as a language rule. A global, an array, and a local whose address is taken are exempt: they live in the data region, which the entry prologue zeroes.

What still reaches instruction selection as `undef` or `poison` is the optimizer's own mark rather than a defect in the program. The incoming value a phi carries on the edge a folded constant branch never takes is the case measured. Refusing it would reject a program whose source is correct, so it becomes a literal zero: it is one of the values the operand is allowed to be, and nothing observes it.

## Verification ordering

Pointer restriction is verified after optimization rather than before. The optimizer can produce pointer phi nodes and selects from source that contained none, and a conditional over two arrays is enough. A source-level check would pass and the backend would then meet a load with no statically known base.

No pass is excluded to prevent this. SimplifyCFG's sinking and hoisting are the passes that would manufacture a pointer select, and both decline to introduce a phi or a select over the pointer operand of a load or a store, precisely so SROA is not blocked behind them. The verifier checks the result instead of trusting that reading, because the guarantee is a property of one LLVM version's passes and the failure it would let through is a load with no known base.

Most pointers do not survive optimization: SROA promotes scalar locals out of memory, and a global with one accessing function is demoted and promoted with them. What stays addressed through memory is arrays and address-taken objects, so a subscript at a runtime index reaches the backend as a `getelementptr` over one of them.

The same check runs when the optimizer is skipped, and has to reach the same verdict there or the flag would reject programs the shipped path accepts. Before SROA every pointer is one more step away from its object: a pointer variable is a slot holding a pointer, and a pointer parameter is that slot with the incoming argument written into it. So the walk also follows a load back to the values written into what it read, and a parameter back to the argument each call site passes. For a recursive function that is the call from outside, since the recursive one leads back through the same parameter.

## Memory layout

Slot 0 upward holds globals, arrays, address-taken locals, and spill slots, reached by `poke` and `get db`. From a link-time boundary upward, `sp` addresses call frames growing toward slot 511.

Chip state survives power loss, chip removal, and reflashing, so nothing starts at zero on its own. `clr db` zeroes all 512 slots in one instruction, and that one instruction is the whole zero-initialization sequence, so a `zeroinitializer` on an LLVM global means what it says. Selection prepends it to the entry block for a program that allocates anything in the data region. A program whose every value lives in a register has nothing to zero and gets none. When the calling convention is in use, allocation later prepends the `sp` initialization ahead of it, so the zeroing is line 0 or line 1 depending on which of the two the program needs.

The guarantee attaches to storage class rather than to the declaration.

| Storage | Value before the first assignment |
|---|---|
| Data region: a global, an array, an address-taken local | Zero |
| Register: any local the optimizer promoted out of memory | Undefined. Nothing zeroes registers, and a promoted local has no slot to zero |

The zeroing happens once, at program start. Nothing re-zeroes a slot on re-entry to the block that declared it, so a data-region local is zero on its first read and holds its last value from then on.

The two regions share one array and are not hardware-protected. Keeping `sp` above the data region is the allocator's responsibility; there is no trap if it fails. Frame depth is statically bounded for non-recursive code; for recursive code it is a property of the data, and only the slots one activation holds are known.

What the memory arithmetic settles is refused and the rest is reported. Frame analysis runs once the emitted sequence is final, since selection places the return address save and allocation the caller-saved registers, and counting before either would understate a frame. Five refusals come out of the arithmetic, across three stages.

| Refusal | Stage |
|---|---|
| A recursive function with a local needing a data region slot, since the slot is one address for every activation and the inner call would overwrite the outer one's value | Selection |
| A recursive function that spills, for the same reason: one spill slot serves every activation | Allocation |
| `sp` given a base with no slot above it at all, since `push` writes at `sp` and advances afterwards | Allocation |
| A chain of ordinary calls nesting deeper than the slots left, which faults on a push whatever the data says | Frame analysis |
| A recursion with too few slots left for even its first activation, which faults on the first call whatever the data says | Frame analysis |

Every other recursion gets a warning naming the activations the remaining slots hold, a count that errs low where a cycle's members hold different amounts and never high. Beyond it no figure is provably enough, so the size report states how many slots the data region took, how many of them were spill slots, and how many are left for frames to grow into.

## Calls

Functions are inlined by default. Recursion is the only thing that forces a real call, because a body cannot be inlined into itself.

That default is what a `dev` parameter rests on. A device position needs a literal when the chip assembles the line, and splicing the body in substitutes the device each call site wrote; a real call would have to pass the pin in a register, which is not a spelling the chip reads there. Analysis therefore refuses a `dev` parameter on a function that can reach itself.

A call-site count is the obvious second rule and does not pay for itself. Inlining is what puts a callee's arguments in front of the optimizer, and what folds away there is worth more than the `jal`, the return, and the argument move per parameter a shared body saves. Measured over the fixtures, inlining wins on the budget that binds and loses on bytes, so it is not a byte-directed choice: duplicated copies spend text a shared body would not.

The movement concentrates instead of spreading, and it runs both ways. It lands on a handful of fixtures and leaves the rest unchanged under either threshold. Most of those grow by a few lines and stay well inside the budget; the two that grow most cross the 128-line limit and stop fitting on a chip at all. The byte saving runs the other way and comes from a fixture whose shared bodies take more text off it than every other fixture's duplicated call sequences add. `task corpus:measure` prints which are which. A threshold cannot reach a function taking a `dev` parameter whatever its count, since the pin has no register to travel in.

Two bounds hold the expansion finite, and the tree-depth bound analysis applies is neither of them: that one is measured from each top-level declaration, and inlining composes declarations. A body is spliced in by generating it inside the caller's own recursion, so a chain spends every function's depth on one stack, and is refused past 50,000 levels of descent. That is well under the 400,000 levels analysis accepts inside one declaration, so a single accepted declaration can reach it. And a callee is generated once per site, so a chain whose functions each call two others is exponential in its source: sixteen of them turn under a kilobyte into more than half a million instructions, and each further pair multiplies that by four. That is refused past 200,000 constructs lowered in all. Nothing downstream would catch the second, since the optimizer refuses an oversized module and the module has to be finished before it is handed over. Inlining is capped at 32 calls deep besides, which is already past what the byte budget holds.

Real calls use `jal` to write `ra` and `j ra` to return. There is one `ra` and no hardware call stack, so a function that makes a call of its own saves `ra` in its prologue and restores it in its epilogue, around the whole body rather than around each call site. Calling a leaf costs two instructions, the `jal` and the callee's `j ra`. Calling a non-leaf costs four, adding that `push ra` and `pop ra`, and more once allocation pushes caller-saved registers around the site. That ratio is why the default is inlining.

## Register allocation

Eighteen registers are allocatable, or sixteen when the calling convention is in use: `sp` and `ra` are reserved then and available otherwise.

Three registers are held back as spill scratch, and only for a function that spills. Scratch exists to reload a spilled operand into, so a function whose values all fit in the file has no use for it; a function is placed in the whole file first and re-placed with the three withheld only if that was not enough. Reserving them unconditionally would start spilling three values early, and each value past the file costs a `poke` and a `get db` per touch.

The two forms differ only over a narrow band of live values, measured on a program with no calls: from the first value past what the withheld file holds to the last value the whole file holds. Below it nothing spills either way. Above it the second pass allocates against exactly the registers the unconditional form always had, which is the same allocation instruction for instruction. Inside it the unconditional form spills where the conditional one does not, so the change is a saving there and never a loss anywhere. Almost nothing in the corpus reaches the band at all, which is why the measurement is taken on a program written for it.

The set holds three because an instruction needs one scratch register per distinct spilled source it reads, and three register-capable sources is the most any selected instruction reads. `select`, `clamp` and `lerp` read three and write a result; `lbns` reads three and writes one; `sbn` and `sbs` read three and write nothing. A spilled destination costs no fourth register, since it takes back one a source already borrowed and the machine reads every operand before it writes, so the forms with no destination fit in the same three. The requirement is per instruction and does not grow with how much a function spills. A wider instruction would be reported as a shortfall instead of miscompiled.

The scratch set has to stay clear of every register instruction selection already names, which is the calling convention's argument and result registers plus whatever else arrives pinned. A pinned register carries no live range into allocation, so a reload into one would write over a value nothing here can see is live, and the write is silent. Allocation refuses such a configuration instead of emitting the reload; the two sets are disjoint today because arguments go in `r0` upward and the scratch set takes the top of the general file.

A spill is a `poke` and `get db` pair against the data region, neither of which disturbs `sp`.

Indirect register referencing is bounded by the register array, so it reaches `sp` and `ra` as well as the general file. A register-indirect scheme must therefore keep its own index within the general range; the machine will not stop it from writing `sp`.

## Peephole

Five rewrites. Three run over one block's instruction list. The other two read a block's trailing instructions against the block control falls into, and run per function once the first three have settled which blocks hold instructions.

| Pattern | Becomes | Why it is only visible here |
|---|---|---|
| `move d d` | nothing | Phi lowering emits copies over virtual registers, and whether a copy's two ends are one register is the allocator's decision |
| A set instruction writing `d`, then `seqz d d` | the set instruction's complement writing `d` | Selection materialises a negated comparison as the comparison plus the instruction that turns a truth value round, and whether the two share a register is again the allocator's decision |
| A set instruction writing `d`, then `snez d d` | the set instruction alone | The second test asks a value that is already 0 or 1 for its truth again. The optimizer cannot fold it, because an opaque declaration says nothing about the range of its result |
| A block's trailing jump to the block control falls into | nothing | Selection already dropped the ones it could see, but the three rewrites above empty blocks, so the pass is repeated over the shortened layout |
| A conditional branch to the block control falls into, then a jump elsewhere | the complementary branch to where the jump went | Needs the fallthrough the rewrite above has just settled |

The two set rewrites apply only to a definition whose result is exactly 0 or 1, and the complement only where the machine has an exact one. `target.md` records which pairs qualify. In value position they are equality, the device test, and the NaN test; not the ordered comparisons, where both members answer 0 for a NaN operand and neither is the other's negation; and not the approximate comparisons, where `sna` writes its tolerance test as `>` instead of negating `sap`'s `<=` and so answers 0 alongside it wherever that comparison is unordered. In branch position the set is the same minus the NaN test, since `bnan` has no complement at all and is never inverted. `__ic_isnan` is the case motivating both set rewrites: `!__ic_isnan(x)` read as a value is `snan; seqz`, which is `snanz`, and `if (__ic_isnan(x))` is `snan; snez`, which is `snan`.

The ordered pairs are withheld so that no branch is read the other way round. Inverting `blt` into `bge` would send a NaN operand to the arm the source sent it away from.

The pass has no liveness information. It sees one block's instruction list, and neither the allocator's interference graph nor a live-out set per block. The register rewrites are therefore restricted to a shape needing none: an instruction that reads and writes one register kills that register's earlier value whatever happens later, so folding the earlier definition into it is sound without knowing whether the register is live out. Adjacency within one block supplies the rest, since a branch target resolves to the start of a block and no control flow can arrive between two instructions of the same one. The general form, the complement written to a second register where the first may still be read, is what liveness would be for and is not taken. The two control-flow rewrites need no liveness because they write no register; what bounds them is the complement table and the requirement that the instructions end a block, so that dropping the jump moves no other block's fallthrough.

## Emission

Branches are absolute. Relative forms encode line offsets and would be corrupted by any pass that changes line counts. Branch targets emit as line numbers and no label definition lines are produced. Labels resolve at emission, which is the last stage, so nothing can shift a line count afterward. This costs nothing and sidesteps the question of whether a label-only line consumes an execution slot.

One hazard is a position rather than a form, so it is checked against the finished layout instead of at the selection tables. `sleep` on line 0 returns `-0`, which fails the negative test that ends the tick, and the chip re-runs it until the 128 instruction budget is gone, every tick until the duration elapses. Neither prologue is guaranteed to hold line 0, since `clr db` appears only for a program that allocates in the data region and the `sp` initialization only when a real call was selected, so the condition is stated over the emitted sequence rather than over the entry function's body. A program whose first instruction is a `sleep` is refused at the source line that produced it, after the peephole: allocation can prepend to the entry point and the peephole can delete from it, and both change what lands on line 0.

Two values a register can hold have no literal the chip's operand parser reproduces. A NaN reads as unset, so an instruction naming one raises `IncorrectVariable` every tick. A negative zero reads as `+0.0`, which the machine then treats as a different value everywhere it looks at sign. Both are replaced before register allocation with the one instruction that computes them, `div d 0 0` and `mul d 0 -1`, at a cost of one line each. Either one still spelled as a literal by the time emission runs is a defect in the compiler rather than in the program, and emission refuses it.

Logic types, slot types, batch modes, and reagent modes emit as names. A name and its integer resolve identically on the chip and occupy the same one operand, so the difference is bytes alone and no lines. A numeric mode emits the integers instead, and is the escape hatch for a program that has run out of bytes.

A readable mode names the block each line opens and the block each branch goes to, in a comment after the instruction. The chip cuts a line at its first `#`, so the annotated program is the shipped one on the same instructions, lines and branch targets, and what the annotation spends is bytes and line width. It is a debugging view rather than a second default because of that width: an annotated line is much longer than the instruction on it, so a program well inside the 90 character limit can be annotated past it. The editor's paste then cuts inside the comment, which the report counts and does not treat as a violation, since the program the chip runs is still the one that was compiled. The size report accounts for the annotations, so it describes the annotated text rather than the shipped form of the same program.

No comments, blank lines, `alias`, or `define` are produced. Each costs a line and an execution slot.

## Size reporting

The report states what the program spends against all three limits and names the size budget closest to binding, computed rather than assumed. Lines bind on all but the widest programs, since the byte cap would need an average of 32 charged bytes a line, but a program of long lines can still meet 4096 bytes first, and telling that program to cut lines would be wrong.

The 90 character line width is reported and not ranked. It bounds one line's formatting rather than the whole program, so ranking it would name it the closest limit on a program using a twentieth of both budgets. It is still a limit a program is refused for, but the two figures differ. The width reported is the whole line, comment included. The width refused is measured only up to the line's first `#`, so a `--readable` annotation carried past 90 characters is counted as a comment the paste cuts and is not a violation, while a subnormal immediate spells past the width in instruction text and is.

A program exceeding a limit is reported and then fails, with its assembly withheld. The report names each limit exceeded and goes to the error stream, so withholding the text loses nothing a programmer needs to get back under budget; emitting it would put a program the in-game editor will not take into the file the usual invocation redirects to, under a status saying the run failed. Every mode is held to the report it just printed rather than to some other form of the same program, since `--readable` spends bytes and line width on its annotations and `--no-optimize` is several times larger, and a status disagreeing with the report beside it would mean neither.

The memory array is reported alongside the three text limits and is not ranked against them. It is a separate resource, since cutting lines does not free a slot, and it is the one budget whose overrun is silent, because a call frame reaching a global overwrites it and nothing traps. The report names the slots the data region took, how many of those were spill slots, and how many are left above them; a program that makes no call is said to make none, because nothing then grows into what is left. What the report states is not itself a violation and does not fail the command: past the refusals the memory arithmetic makes, depth is data-dependent, so no remaining figure is provably enough and no threshold could separate a program that works from one that does not.

Attribution is by construct rather than by function. Every call is inlined, so a function called from three places pays for itself three times and a per-function total cannot say which of the three to delete. The unit is the inline site.

| Half | Where it comes from |
|---|---|
| Which callee expanded at which call | IR generation, which does the inlining, recorded against the call's line and column |
| Which emitted lines came through which chain of calls | The `inlinedAt` chain on each instruction's debug location, which survives the optimizer |

Neither half locates a byte alone: the metadata carries no name, and the front end does not know which instructions lived. Sites nest and roll up, so a row's byte count is what deleting that call would recover; the exclusive counts are what sum to the program. Two expansions the optimizer merged produce a location naming no line, and the report says so instead of picking one of the two arbitrarily.

## Operand validation

The chip validates six conditions at compile time and nothing else, so an operand outside what the machine accepts assembles cleanly and then faults once per tick, indefinitely, with no diagnostic beyond a line number. Everything below is refused at the source line that produced it.

| Operand | Bound | Caught at |
|---|---|---|
| Device pin | `db`, or `d0` through `d5`; the chip's own pattern admits `d0` through `d9` and a housing has six pins | Analysis, by name; selection, by value |
| Device slot index | Not negative. The upper bound is the device's and is not knowable here | Analysis |
| Logic type, slot type, batch mode, reagent mode | A member name in the generated tables, within the width its enum is backed by | Analysis, by name; selection, by value |
| Memory slot | 0 through 511. `get` answers an address outside the array with the unknown error, and `put` with a stack overflow above the array or a stack underflow below zero | Selection |
| Integer constant | Survives the round trip through a double. The optimizer folds to the whole of an i64 and the machine is exact to 2^53, so a constant in the band between them names a different number on the chip. The bound is not a magnitude: a power of two well past 2^53 round trips exactly | Selection |
| Register, device kind, connection index | A spelling the assembler resolves, rather than a debug form for a value outside the machine's range | Emission |
| `sleep` on line 0 | A placement rather than a form, so it is checked against the finished layout | After the peephole |
| Call frame | One activation of a recursion, and the deepest chain of ordinary calls, each have to fit in the slots above the data region; and a recursion may hold no data region slot, spilled or address-taken. Depth past the first activation is the data's and is warned about | Selection, allocation, and frame analysis |

A NaN or a negative zero immediate is not in the table because neither is refused: the operand parser reproduces no literal for either, so both are rewritten into the arithmetic that computes them before allocation.

A deprecated logic type, slot type, batch mode, or reagent mode is warned about instead of refused. The chip resolves a retired member exactly like a current one and the emitted operand is unchanged, so a refusal would reject a program that works; what the programmer cannot see unaided is that the game has moved the property and may stop maintaining this one. A warning does not stop the pipeline and does not change the exit status.

## Device surfaces

An access is checkable where the program states which device it reaches, and the game ships a roster of every prefab there is to check it against. A program states it in two ways. A batch instruction carries a prefab hash in its first operand, which names the devices the instruction aggregates over. A `dev` declaration may carry `[[ic11c::prefab("Name")]]`, which states what the housing position it names is wired to. Analysis holds the seven batch forms and, through a declared position, the four pin forms, to what that roster says a completed device of the named kind answers for.

The two differ in what the source settles. A batch hash is a fact about the instruction. A pin declaration is a promise about the world, since what is on `d0` is decided when the chip is wired in-game and nothing emitted verifies it, so a verdict against it reads "given what this program says is on this pin, this access is wrong". A pin no declaration covers is silent, which is how every program written before the attribute existed still compiles unchanged.

| Held to the roster | The check |
|---|---|
| The name or number the prefab operand carries | This game build ships something under it |
| The property named beside it | A completed device of that prefab answers it, in the direction the form uses it |
| A slot index | Below the slot count the prefab declares |
| A slot property | That slot answers it, in the direction the form uses it |
| A constant written to `Mode` | Inside the mode list extraction recovered, numbered from zero |
| The prefab a declaration of `db` names | The roster says such a thing can hold a programmable chip |

Only a prefab operand that folds during analysis is checked: a `__ic_hash` of a string literal written in the operand, a variable a declaration initialised with one and nothing wrote to afterwards, or an integer constant expression. A declaration's initializer is the one write that reaches every use of the object it declares; any later write, a compound assignment and a taken address included, leaves what the operand holds undecided.

A declaration's claim is keyed by the position rather than by the name, so an access through the bare `d0` spelling is held to a declaration that named `d0` under some other identifier, and a claim written inside a block covers the whole program. Two declarations naming one position as two prefabs is an error rather than a warning: it is a contradiction in the source, which no game build can make true. A prefab name the roster does not hold warns at the declaration and leaves every access through that position unchecked.

A clean compile therefore says little about a program's device usage. What stays outside the check:

| Outside the check | Why |
|---|---|
| A pin-addressed access through a position no declaration names | Which device `db` or `d0` through `d5` reaches is a property of the world the chip is placed in rather than of the program. Most programs, and most of the corpus, are written this way |
| A pin-addressed access through a `dev` parameter | Which position the body reaches is the call site's rather than the body's, and this pass does not follow a call |
| A prefab operand that does not fold: a parameter, an array element, a variable written twice, a computed hash | The chip takes a register there, so each is a legitimate program the pass has nothing to say about |
| A property whose direction the extraction could not decide | The game settles those from live state. Roughly a quarter of the roster's logic entries are undecided, and a logic mirror, which answers whatever it is pointed at, has every one of its own undecided |
| A device still under construction | It refuses every property. The roster describes the finished thing, so every verdict is already the optimistic reading |
| A `__ic_hash` outside a prefab operand | The same intrinsic derives device names, reagent hashes and mode strings, none of which the roster holds |

Every verdict about a surface is a warning. An access a matching device refuses does fault the chip, and for a read the refusal is checked over every matching device before the aggregate is computed, so even `Count` faults. But that happens only where the network holds such a device, so the world decides what the program does. A pin verdict is one step further from the source still, since the declaration it is judged against is a promise nothing verifies. That is the same line the call-frame diagnostic draws, which errors only where the arithmetic settles the outcome on its own. Two further reasons point the same way: the roster describes one pinned game build, and a build that moved a property would have this refusing a program that works in a player's game.

There is no run-time check to go with the compile-time one. A chip could read `PrefabHash` off the pin and compare it, but a comparison per declared position spends several lines of a tight budget on a claim the programmer already made.

An undecided surface is silent by construction rather than by a check remembering to skip it. What `internal/ic10` answers about a property is four refusal queries, each implemented by enumerating the cases that answer true, so anything the extraction leaves undecided, and any surface a later extraction introduces, refuses nothing. The pair of directions itself is exported from `internal/isa`, the generated leaf the roster lives in, because Go admits no unexported field a second package can read. A consumer holding one could read undecided as denied, which is the wrong-answer defect the roster exists to prevent. Nothing in the type system stops that; standing in its place is a test in `internal/ic10` that fails if any other package in the module names a roster declaration.

A `Mode` number is held to the mode list only where extraction recovered one. Thirty-six prefabs fill their mode names at run time, and one more writes `Mode` with no list recovered at all; the two-element default the game declares by inheritance is not what such a class ends up with, so counting it would understate how far the device's `Mode` reaches. All of them are skipped instead. The number written is judged as the device receives it: a device stores its mode as an integer and reaches one through a signed integer conversion, so a fractional constant truncates toward zero instead of being rejected, and `1.5` selects the same mode `1` does.

`db` is the housing the chip is inserted into, so a declaration naming a prefab there says the program is running inside one of those, which is the one thing a declaration promises that the roster can decide. A thing the roster says holds no programmable chip warns; a thing it leaves undecided is silent, as is a `db` no declaration names.

## Diagnostics

Every rejection names a source line and describes the construct in the language the source is written in. Nothing quotes the module text: an SSA register, a type suffix, or a metadata reference names something the programmer never wrote and cannot act on, and the position is already carried separately. Where an unsupported construct has an answer, the diagnostic gives it.

Positions raised after IR generation are rebuilt from a debug location, which carries a line and a column and no byte offset. Ordering is therefore by line and column, with the offset breaking ties, so a backend diagnostic interleaves with a lexical one in reading order; the offset itself is restored from a line index over the source.

A call the optimizer formed rather than the programmer wrote is named as such. InstCombine builds minimum and maximum intrinsics out of ordinary comparisons on the expectation that a target has an instruction for each, and this machine's are signed only, so the fold can land outside what selection covers.

A construct the optimizer built carries no debug location of its own, so a diagnostic about one falls back to the block it heads: the first instruction there that does carry a location is the statement that reads the value. Nothing is reported at no position at all.

A diagnostic carries a severity. An error rejects the program and nothing is emitted; a warning describes a construct the compiler accepts and emits anyway, and is printed alongside the size report. A warning does not change the exit status. The other thing that fails without rejecting is a program over one of the editor's three limits, which is reported and then fails with its assembly withheld.

## Exit status

The usual invocation is `ic11c program.c > program.ic10`, so the file is all a caller has once the run ends and the status is what says whether a program landed in it. Complete assembly is on the output stream under exactly 0; under no other status is anything on it worth keeping. The one program whose complete assembly is nothing is the one whose functions hold no instruction, which leaves 0 with the size report saying it is empty.

| Code | Meaning | Output stream |
|---|---|---|
| 0 | Compiled and fits inside every limit | Complete assembly |
| 1 | No program to keep: a source that could not be read, a program the diagnostics name lines in, a program over a limit the in-game editor enforces, or a stream that stopped taking bytes | Empty, unless the write of the assembly is what failed |
| 2 | The command line could not be read: an unknown flag, a value a flag does not take, the wrong number of arguments | Empty |
| 70 | A stage of the compiler could not run | Empty |

Those are the statuses the command chooses, which is not everything a shell can read from a run. A run a signal ended is reported as 128 plus that signal, and `ic11c program.c | head` is the everyday way to reach one: the reader stops early, the write raises `SIGPIPE`, and the run ends before anything above decides anything.

Every write to either stream is checked, whoever made it. Both streams are wrapped before the run starts, so the assembly, the diagnostics and the size report are held to landing whole alongside the help, usage, version and completion text the command-line framework prints on its own behalf, the byte count as well as the error, since a stream that takes fewer bytes than it was given and says otherwise is still a stream that stopped. A write that did not finish ends the run at 1, unless a defect in the compiler already ended it at 70. That is the one case where a redirection catches something under a failing status: the assembly is the last thing the command writes and every decision about the program is made before it, so nothing else can reach the stream, and a stream that stopped part way has already taken what it took. A prefix of an assembled program pastes into an IC Editor and runs, as a program other than the one that was compiled, so 1 has to mean there what it means everywhere else: the file is not to be used.

One write stands outside that. `__complete`, the hidden command a generated shell completion script calls, reports its own failures straight to the process error stream rather than through the command, so a run of it that could not answer leaves 0 with that message and nothing else.

1 is what `cc` and `clang` leave for a program they would not build, widened here to every way a run ends with no program to keep, since what a caller does about each of them is the same and the error stream is where they are told apart. 2 is the widespread convention for a command line that could not be read, and is kept to exactly that: it is the one status that sends a caller to look at what they typed rather than at what they wrote. 70 is `EX_SOFTWARE` from `sysexits.h`, taken for its distance from the small integers rather than for the rest of that table, since a defect in the compiler must not be readable as an outcome a source program can cause. Nothing else from that table is used: 65 for a syntax error would surprise anyone expecting 1 from a compiler.

A refusal the backend makes is a rejection like any other: it names a line, emits nothing, and leaves 1. Over budget leaves 1 as well, for all three limits, since each is measured on assembly that is already complete and none of them produces a program worth keeping. A warning never moves the status, so a program that both warns and exceeds a limit leaves 1.

`--version`, `--help`, the `help` and `completion` subcommands and the `prelude` subcommand compile nothing. They write to the output stream and leave 0, or 1 when that stream stops taking bytes. `prelude` writes two files that only configure an editor together, so it names both in one write to the output stream after every step that can fail; a failing run leaves the stream empty like any other, whatever it left on disk.

### Reading files

`prelude` reads and writes names in a directory the caller chose, so each of them gets the treatment a source gets: an open that does not wait, and a read bounded to what the decision needs. A file of the header's own name beside the sources is removed only when it is a plain file whose opening line is the one every generated header carries. Anything else of that name, a pipe, a device, a link the caller made, or a file somebody wrote, is reported on the error stream and left where it is.

A source is opened once and everything after that works on the handle, so what the command describes is what it reads. Checking a path and then reading the path are two resolutions of one name, and between them the name can come to mean a device, a pipe, or a file of any size.

A source has to be a regular file, since a device answers forever and a pipe answers whenever a writer does, and at most 1 MB. The size the file reports is a hint that buys a better message and not the bound; the read itself stops one byte past the limit, so a file that reports zero and reads out kilobytes, as everything under `/proc` does, is refused for what arrived. `ic11c /dev/stdin < program.c` therefore compiles when stdin is a regular file and is refused when it is a pipe, which is the file being described rather than the name.

1 MB is an upper bound on a working set and not on the language. Nothing downstream refuses a source that size first: it is well inside the optimizer's own bound on module size, and a file at the cap is parsed, optimized and emitted the whole way, coming out as a program hundreds of times over every limit the editor holds.

## Correctness

The machine is not modelled here. Every question about what an instruction means is asked of the game's own chip: `tools/chipgen` cuts `ProgrammableChip` and the device surface around it out of the decompiled source into a standalone C# compile unit, `internal/chip` drives that under a digest-pinned Mono image as a long-lived process, and `internal/chiptest` hands it to a test. A missing chip is a failure rather than a skip, so nothing needing one can pass by not running.

That carries across the behaviour no specification states, without anyone having to read it correctly first: the add-back remainder `mod` computes, which coincides with a floor modulus only for a positive divisor; the int64 round trip shared by every bitwise operation; the 53-bit payload cap on `ext` and `ins` against the 54-bit rotate width of `rol` and `ror`; NaN propagation through `max` and `min`; and the tick-ending behaviour of `yield` and `sleep`, where the chip packs the resume line and the end-of-tick signal into one signed integer, so a yield answers −line − 1 and a suspending sleep answers −line, which is why a `sleep` on line 0 answers −0, fails the negative test and spins the rest of the budget.

A generated assembly corpus runs on it program by program, to reach the instruction families the backend never selects. Its input is assembly rather than compiler output, so what it establishes is about the machine and never about a lowering. Two generators feed it: one emitting terminating fault-free programs, held to running to the end, and one provoking a deliberate fault, held to raising the fault it names on a line inside its own. `testing.md` carries what each reaches and what is kept out of them.

The compiler's generated tables are not the chip's. The chip carries the game's own, so a table this repository read wrong shows up as a program the chip will not assemble or a fault where none was expected, instead of as two copies of one mistake agreeing.

## Machine tables

The register file, opcode table, operand kinds and logic-type tables are generated from the game's own assembly and checked in, so an ordinary build never touches the game. A roster of every prefab the game ships is recovered from the game's Unity assets in the same run and stamped with the same manifest. Analysis consults it for the batch and declared-pin forms; nothing else in the pipeline reads it. Regeneration has two stages: extraction into canonical JSON stamped with the depot manifest and assembly version, then conversion of that JSON to Go. Only the first stage fetches anything, and it runs entirely in a container. `building.md` describes the pins and the procedure.

Extraction refuses to write a table that disagrees with what the compiler is built against. Every table size is asserted. A manifest of instructions pins the exact operand signature each must still have, covering every operand kind and each of the load, store, batch, stack, branch, and jump families. Every operand kind the extractor recognizes has to be reachable from some instruction, and the operand enum tables are held to their order as well as their sizes, because the assembler resolves a shared member name through whichever table comes first. A game update that moves any of these is reported in one pass rather than silently narrowing the tables.

An operand's direction is declared rather than inferred. It is read from the chip's own `_Operation` classes, being which constructor argument reaches which operand position and which operand's register the class assigns, while the operand list is read from the game's help text. The two are therefore independent readings of one build and each checks the other. Across 154 instructions and 405 operands every direction is determined: 75 instructions write operand 0, 79 write none, and none writes a non-first operand. The 75 counts `ins`, whose first operand is read-write; 74 instructions write operand 0 without also reading it. Extraction refuses an undetermined direction, and refuses any disagreement between a declared direction and the shape of the operand list, so neither reaches a checked-in table. `DirectionUnknown` is the zero value of `Direction`, so an operand built outside the generated table claims nothing and register allocation refuses it. The finished table is then executed rather than only cross-read: each instruction runs on the chip over a probed register whose starting value is the only thing that differs between two runs, so an operand the table calls a destination and the chip leaves alone fails, and so does the reverse.

The JSON is diffable, so a future game build produces a reviewable changeset rather than silent drift.
