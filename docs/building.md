# Building

ic11c links libLLVM through CGo. A libLLVM matching the build tag must be present to compile the compiler. Release artifacts link it statically, so running one requires nothing installed.

Commands are codified in `Taskfile.yml`; `task --list` enumerates them. The invocations below are what those tasks run, and are the form to reach for when a platform needs flags the default does not supply. Every task accepts a `GO_TAGS` override, for example `task test GO_TAGS="byollvm llvm21"`.

## Version selection

The libLLVM major version is fixed by a Go build tag, one of `llvm14` through `llvm22`. Omitting the tag selects `llvm20`. This project targets the major in `.llvm-version`, currently **22**.

That file is the only place the major is written down. The Taskfile builds the build tag out of it, the release scripts default to it, the CI action that installs the toolchain reads it, and the release container takes it as a build argument with no default of its own. `task check:llvm` holds every other spelling to it, because a bump that moved one and not another would compile the tests against one major's bindings while the release linked another's library, which is a wrong answer rather than a link error.

That gate scans the workflows, the `Dockerfile`, the Taskfile and the scripts for two shapes: a major written as a literal, whether assigned to the variable that carries it or spelled into a value such as an `llvm-config-N` or a Homebrew `llvm@N`; and the rule forming a build tag out of a major, which only `Taskfile.yml` and `scripts/release-common.sh` may hold, one per language, since Task templating cannot read a shell variable and a script cannot read a task variable. It separately holds `.golangci.yml`'s `run.build-tags` to the same major, since golangci-lint interpolates nothing and a bare `golangci-lint run` would otherwise lint a different major's build than the tests do. Prose is exempt: comments are stripped before each shape is retested. The four scan roots are themselves held to the top of the tree in both directions, so build machinery appearing in an unlisted directory fails rather than reading as a clean tree. Everything else at the top of the tree is named exempt rather than skipped by default, since none of it decides which library a build links.

Changing the tag is a migration rather than a version bump: the optimizer's output changes with the version, and the headers and shared library must agree with the tag.

A second tag, `byollvm`, disables the binding layer's hardcoded include and library paths so they can be supplied through `CGO_CPPFLAGS`, `CGO_CXXFLAGS`, and `CGO_LDFLAGS`. Those hardcoded paths assume a Debian layout. `byollvm` is the portable form, and is required on any other distribution.

## Requirements

A C++ toolchain rather than only a C one, since the binding layer compiles its own C++ sources and includes LLVM's C++ headers.

The mid-level pipeline registers no LLVM target backend and needs none, so an LLVM built without target support is sufficient.

A C compiler on `PATH` for `task test`, which the conformance gate and the clang comparison both need. The conformance gate compiles the MicroC corpus as C23 against the generated prelude, which makes the subset claim enforceable rather than asserted. Both `clang` and `gcc` are run wherever both are present, since the claim is about C rather than about a driver and the two disagree about which extensions are C; `clang` is named first because it is what the generated argument file targets. The gate fails rather than skips when it finds neither, so a run that checked nothing cannot read as one that checked everything. `cc` is not consulted: it is a symlink to one of the two and the name does not say which, which would leave the extension set under test unknown. The clang comparison takes `clang` alone and fails without it, since the argument file the corpus declares is clang's and a second driver would leave the language the comparison was made against unknown.

## Linux

Where LLVM installs to the default prefix, the plain tag works:

```
go build -tags llvm22 ./...
```

Where it installs to a versioned prefix, supply the flags:

```
CGO_CPPFLAGS="$(llvm-config-21 --cppflags)" \
CGO_CXXFLAGS="-std=c++17" \
CGO_LDFLAGS="$(llvm-config-21 --ldflags) -lLLVM-21" \
go build -tags "byollvm llvm21" ./...
```

Both link dynamically. The static release link is a separate recipe; see [Release artifacts](#release-artifacts).

## macOS

`brew install llvm`. The formula is `llvm` rather than `llvm@22`: Homebrew creates a versioned `llvm@N` only once N is superseded, so the versioned name does not exist while 22 is current.

The binding layer's built-in darwin paths name `/opt/homebrew/opt/llvm@22`, a directory never created for the same reason, so the plain tag does not work here. Use `byollvm` and derive the paths from `$(brew --prefix llvm)`, or run `scripts/build-release-macos.sh`, which does that and links statically. That script also names `$(brew --prefix zstd)/lib/libzstd.a` by path, so it needs Homebrew's `zstd` as well; the CI job installs both.

## Windows

This section is the native path, for developing on a Windows machine. The release artifact is cross-built from Linux instead; see [Release artifacts](#release-artifacts).

The toolchain must be mingw rather than MSVC. CGo does not support MSVC, and LLVM distributions built with MSVC use an incompatible C++ ABI: different mangling, vtable layout, and exception model. The official LLVM Windows releases are MSVC-built and cannot be used.

MSYS2's UCRT64 environment provides a mingw-ABI LLVM with headers, a shared library, and the full set of static component libraries:

```
pacman -S --needed \
  mingw-w64-ucrt-x86_64-gcc \
  mingw-w64-ucrt-x86_64-llvm \
  mingw-w64-ucrt-x86_64-llvm-tools \
  mingw-w64-ucrt-x86_64-llvm-libs \
  mingw-w64-ucrt-x86_64-go
```

```
CGO_CPPFLAGS="$(llvm-config --cppflags)" \
CGO_CXXFLAGS="-std=c++17" \
CGO_LDFLAGS="$(llvm-config --link-static --ldflags) \
  -Wl,--start-group $(llvm-config --link-static --libs) -Wl,--end-group \
  $(llvm-config --link-static --system-libs) -lversion \
  -static -static-libgcc -static-libstdc++ -lstdc++" \
go build -tags "byollvm llvm22" ./cmd/ic11c
```

Two defects in `llvm-config` output to correct by hand: it emits a literal `-lzstd.dll` token where `-lzstd` is wanted, and it omits `-lversion`, which LLVM's Support library requires on Windows.

**Stay within one environment.** UCRT64 and MINGW64 link different C runtimes, the UCRT against forwarders to `msvcrt.dll`. Mixing a compiler from one with an LLVM from the other puts two C runtimes in a single process, with separate heaps and separate `errno`, which links cleanly and then misbehaves. The GitHub Windows runner's preinstalled mingw is a UCRT build.

CLANG64 is a third incompatible choice: its LLVM is built against libc++ where UCRT64 and MINGW64 use libstdc++. The binding layer passes standard-library types across the boundary, so these must match.

## Release artifacts

Every published artifact runs with no libLLVM installed.

| Platform | Linkage | Recipe | Built on |
|---|---|---|---|
| Linux | Fully static, including libc | `scripts/build-release-linux.sh` | A Linux machine |
| macOS | LLVM static, OS dylibs dynamic | `scripts/build-release-macos.sh` | A Mac |
| Windows | Static but for the system DLLs | `scripts/build-release-windows.sh` | A Linux machine, cross-linked in the release container |

macOS stops short of fully static because Apple ships no static libSystem and does not support statically linked executables (QA1118). What remains are dylibs present on every Mac.

Each build has a matching `scripts/verify-release-*.sh` that fails if a dynamic dependency survived. Linux and macOS run theirs as a workflow step; the Windows check runs inside the container, because the `objdump` that reads a PE import table belongs to the cross toolchain rather than to the runner. A partial link succeeds silently and surfaces only as a missing-library error on a user's machine, so the artifact is never published unchecked.

Linkage is half the check. `scripts/verify-release-compiles.sh` compiles the whole fixture corpus in the shipped, `--numeric`, `--readable` and `--no-optimize` modes, and holds each build's assembly to the line count the size report beside it states. A link that starts and cannot compile passes every header inspection, and `--version` alone reaches no LLVM code, so without this nothing published has ever compiled a program. Linux and macOS run it from their own verify script. Windows runs it as a separate `windows-latest` job against the packaged zip, since nothing in the cross-build container can execute a PE.

The same script holds the reported version to the tag being built, and refuses `dev` as that tag. `dev` is what the version variable carries when the linker stamp does not take, so comparing it against itself would pass the binary the check exists to catch. Neither the container's build argument nor either release task defaults it.

| Recorded per build | Why |
|---|---|
| SHA-256 of the emitted assembly | Instruction selection, register allocation and float formatting all reach it |
| Exit status | Which fixtures are refused or over budget moves with the corpus; that all three platforms agree on it does not |
| Lines and bytes the report states | The two figures a size regression would move |

`CORPUS_MANIFEST` names where to write that record. Each manifest also opens with the libLLVM version the artifact links, which the comparison prints and drops before diffing, since no pin makes the three platforms link one build; a manifest carrying no such line is refused. Each platform job uploads its copy and a later job requires the three to record the same builds, which is the only cross-platform check either workflow makes: `go test` runs on Linux alone, so nothing else would notice a macOS or Windows artifact compiling the corpus differently.

The Linux static link needs the LLVM component archives, which only Debian and Ubuntu packages ship. `llvm-22-dev` from apt.llvm.org is what both CI and the container use. Arch, among others, omits them, so `scripts/build-release-linux.sh` cannot run there.

The Linux link emits warnings about `dlopen`, `getpwnam_r` and `getpwuid_r` needing a matching glibc at runtime. They come from LLVM's Support library, in its plugin loading and tilde expansion paths, neither of which this compiler reaches.

The Windows link emits a page of `duplicate section .rdata$_ZTS... has different size` warnings, one per libstdc++ member carrying a type-info string. MSYS2 builds LLVM with clang and builds its libstdc++ with GCC, and the two emit those sections at different sizes. This is a property of the two artifacts rather than of cross-linking, so a native MSYS2 link produces them as well. The binary runs and compiles correctly, but nothing has exercised a C++ exception crossing the LLVM boundary, which is where a real type-info mismatch would show.

### Through Docker

`task release:linux VERSION=v1.2.3` and `task release:windows VERSION=v1.2.3` produce `dist/ic11c_Linux_x86_64.tar.gz` and `dist/ic11c_Windows_x86_64.zip` with nothing installed on the host but Docker: no Go toolchain, no LLVM, no system packages. Each stage calls the same `scripts/build-release-*.sh` the release workflow calls, so the flags have one definition rather than two. `VERSION` is required rather than defaulted, for the reason the verification refuses `dev`.

Each artifact is owned by the invoking user: the build ends in a `scratch` stage that BuildKit writes straight to the host, which avoids the root-owned files a bind mount written from inside a container would leave.

The Windows stage draws its host-side driver and `binutils` from Debian, and everything on the target side, LLVM and libstdc++ and the CRT and winpthreads, from a single MSYS2 snapshot. Debian's mingw-w64 packages are two majors behind, and a link mixing the two sides fails on symbols the older side does not carry.

| Platform | In Docker | Why |
|---|---|---|
| Linux | Yes | apt.llvm.org ships the static component archives |
| Windows | Yes | MSYS2's UCRT64 packages are a complete mingw-ABI target toolchain in zstd tarballs, which unpack and cross-link on Linux |
| macOS | No | Requires the macOS SDK, which Apple's licensing does not permit in a container |

Reproducibility is complete on the Windows side and has a floor on the Linux one.

| Input | How it is pinned |
|---|---|
| Both base images | By digest, so a rebuilt `1.26` tag cannot move the bytes under a version string saying it did not |
| MSYS2 UCRT64 packages | By exact filename and `sha256` in `scripts/msys2-ucrt64.lock` |
| apt.llvm.org | By major only |
| Debian's own packages | Not pinned beyond the base image's snapshot of its sources |

`task msys2:lock` rewrites the MSYS2 pins from the live repository index and refuses an index that has moved to another LLVM major. MSYS2 is rolling and drops superseded packages, so a lock that has aged out fails the build on a `404` rather than quietly moving the toolchain: rerun the task and read the diff.

That index decides the filenames the container downloads and the digests it checks them against, and `mirror.msys2.org` redirects to third-party community mirrors, so it is held to the `ucrt64.db.sig` MSYS2 publishes beside it before a row is read. `scripts/msys2-lock.sh` carries the fingerprint of the MSYS2 development key that signature must be from, taken from MSYS2's own keyring rather than from a mirror, so MSYS2 rotating its signer stops the task rather than being followed. The container verifies no signature and re-derives nothing: it trusts the checked-in lock, whose every change is a diff a person reads.

apt.llvm.org serves a moving snapshot within a major and drops superseded ones, so pinning a package version there would break within weeks, and two Linux builds a month apart can differ in LLVM patch level with the optimizer's output differing too. `task release:linux` therefore re-runs the `linux-toolchain` stage unconditionally; without that refresh a local tree links current source against whatever snapshot the cache holds. `task release:windows` does not, and must not: its toolchain is pinned, so a cached layer and a cold one hold the same archives. Pass `--no-cache` for a build that must reuse no layer at all.

## Regenerating the machine tables

The instruction table, the operand enums, the C prelude and the two clangd argument files all derive from the game's own `Assembly-CSharp.dll`. The device roster derives from the game's Unity assets instead. All of it is checked in, so an ordinary build regenerates nothing.

`task isa` rebuilds them from Docker, network access and the Go toolchain the second stage runs under. No Steam client, no game installed, no .NET toolchain, no game licence. It runs two stages:

| Stage | Does |
|---|---|
| `task isa:extract` | Fetches the depot, decompiles the assembly, reads the Unity asset bundles, and rewrites the ISA and device JSON under `internal/isa` |
| `task isa:generate` | Renders the Go tables, the device tables, the C prelude, and the two clangd argument files from that JSON |

Only the first reaches the network. `task check:codegen` renders what the second writes into a scratch tree, diffs that against the working tree, and fails on any drift, which is why CI needs no Steam access. It reports what the generator would write today rather than what happens to be committed, and it leaves the tree alone. What it compares for each generator is derived from that generator's own path flags, so adding an output covers it automatically; a flag whose help says neither what it reads nor what it writes, and a file written to a path no flag declares, each fail the gate rather than going ungated. The same target also fails a derived file sharing a Go package directory with hand-written source, reading the same flag declarations plus the generated banner to decide which files those are.

The two JSON files are recovered in one run and the device tables are checked against the instruction tables written beside them, so the pair cannot describe two game builds. There is one prelude header and two argument files naming it: one beside the header, and one beside the MicroC corpus that reaches it by a relative path. The second is what lets an editor opening a fixture get clangd configuration; without it clang reports `unknown type name 'dev'` on every one of them. The path is computed rather than the header copied, so the two cannot describe different machines.

The assembly comes from the **dedicated server**, app 600760, rather than the client. Valve flags that app free to download, so an anonymous Steam login reaches it; the client app needs a licence and cannot be fetched this way. The two ship the same `ScriptCommand`, `LogicType` and `ProgrammableChip` definitions, and the tables extracted from the server assembly match the client-extracted ones name for name and ordinal for ordinal.

Five things are pinned, and each moves the extraction if changed:

| Pin | Value | Why it matters |
|---|---|---|
| Depot manifest gid | `2546537964923579038` on depot 600762 | Fixes the exact bytes fetched. Recorded in the JSON and in `ic10.ManifestID` |
| File list | `Assembly-CSharp.dll` for the decompile; the managed assemblies, the asset files, `globalgamemanagers` and the English localisation for the device roster | Keeps both fetches to a fraction of a 5 GB depot |
| DepotDownloader | Release tag plus a `sha256` of the asset | GitHub release assets are immutable, so this is a real pin |
| ilspycmd | 8.2.0.7535 | 9.x and 10.x fail on the .NET 9 SDK image, and 8.2 targets an older runtime, so it needs `DOTNET_ROLL_FORWARD=Major` to start at all |
| UABEA `classdata.tpk` | Commit plus a `sha256` | The type database that makes the asset bundles readable. It is served from a branch rather than a release, so the digest is what makes the commit a pin |

Every image the extraction runs on is pinned by digest as well. What is left unpinned is the Debian packages each image installs from its base's snapshot of the apt sources, the same floor the release containers have. What guards against a toolchain that moved is not the pinning: extraction asserts every table size, holds a manifest of instructions to their exact operand signatures, requires every operand kind to be reachable, and holds the operand enum tables to their order as well as their sizes. A run that produced a different table fails rather than writing one.

Those are extraction holding itself to the shapes it was written against. What holds it to the game is a separate reading, wherever a decompile is present: the whole ISA recovery runs over the decompiled source and its result is compared byte for byte against the checked-in instruction tables, so a build that renamed a type, moved a member, or wrote a body in a spelling the matchers no longer reach either stops the extraction or yields a table that is not the one the compiler is built from. The reagent table is held the same way.

The prefab half of the device roster is not re-derived by any of it. It comes from the game's serialized assets rather than its decompiled source, and those are not in the tree, so reproducing it takes a full extraction. That leaves 1,565 prefabs, the bulk of a 2.7 MB checked-in table and the largest thing here the game decides, resting on their own internal consistency, on the manifest they are stamped with, and on the one cross-check the source can make: every slot class they name has to be a member the game still declares.

The assembly is decompiled once, whole, into a file per type under its namespace. That layout is what lets the extractor resolve a type the game source names without a namespace, including the four enums nested inside another type.

The device roster is a separate reading of the same depot, because what it describes is configured in Unity assets rather than in code. A .NET reader walks the asset bundles with UABEA's type database and records every prefab the game ships: its name and hash, whether it can hold a chip, which logic properties it answers and in which direction, the slots it declares and the properties reachable on each, and the mode names its `Mode` property selects between. The English localisation supplies the titles a diagnostic would show a person. Analysis is the only consumer, and `compiler.md` describes what it does with it.

### What is not regenerable

A handful of machine facts are written as Go control flow rather than as a table, and no extraction can express any of them: the shape of the machine and the never-emit list in `internal/ic10`, the exception names in `internal/chip`, the operand conversions instruction selection refuses a constant against, and the editor's limits in `internal/emit`. Regeneration does not touch them and would not notice if the game changed them.

Nine of them have a gate of their own. The register file and memory array sizes, the device pin count, the two fixed register indices, the instruction budget a tick buys and the three editor limits are read out of a decompiled tree and held to the Go constants that mirror them. The gate reads a tree rather than a fixture, so it runs where there is one, and `IC11C_GAMESRC` points it at a named tree. It fails rather than skipping where there is none. A declaration whose spelling the game moved fails too: that is exactly the case where the constant beside it stopped being checked, and skipping would report the machine as unchanged.

The exception names, the never-emit list and the operand conversions have no gate. What covers them is a fingerprint of the game declarations each derives from, written to `gamesrc/csharp.digest` by `task isa:csharp`. Diffing it across two game builds names the declarations that moved and the Go files they back. It is a review aid rather than a gate: nothing compares what it writes, and the CI job producing one does so as a by-product of deriving the decompile its other steps read.

Of the three, the exception names are the only one a run catches on its own. The harness prints the game's own member name and the Go refuses one it cannot resolve, so a member the game added or renamed stops the first read that meets it. What that misses is a member the game removed, and the number a Go constant carries: no ordinal crosses the protocol and nothing compares one against anything, so renumbering the enum changes nothing here and is caught nowhere.

The chip every test asks what the machine does is regenerated by neither. `tools/chipgen` cuts it out of a decompile of the same build, and `task chip:build` re-cuts before it compiles, so refreshing it is a separate step from `task isa`. It has a prompt of its own: `tools/chipgen/slice.digest` is checked in and holds every construct the slicer consumes to the text it had, so a cut against a decompile it does not describe is refused with each changed construct named, and nothing is written. That makes a rewritten body a conversation rather than a chip that quietly answers differently. The procedure for all of it is [Reviewing a game update](game-updates.md).

The decompiled game source is not in the repository. It is third-party game code, and `task gamesrc` rebuilds the two decompiled trees and the digest beside them from the manifest the `Dockerfile` pins. It deliberately leaves the chip slice alone, which `task chip:slice` derives from the full tree with the Go toolchain and libLLVM.

## Continuous integration

Two workflows and a schedule. CI runs on push and pull request; releases are driven by a `v*` tag and refuse to build until the tagged commit is proven to be on `master` and to have a passing CI run; a weekly job runs `task check:game-pin` and reports a Steam build the repository has not looked at.

CI splits `task check` by toolchain across four jobs, so that a developer running `task check` locally runs in one place what CI runs in four. What the jobs add around those gates is toolchain setup, the lint tooling `task lint` needs, the decompiled game source the suite is held to, the coverage upload, and the Linux artifact built and verified for real. Four more jobs sit outside `task check` entirely: three covering the Windows and macOS artifacts, and one that requires the three platforms to compile the corpus alike.

| Job | Runs | Beyond Go and LLVM |
|---|---|---|
| `check` | `fmt:check`, `check:llvm`, `lint`, `test`, `check:gamesrc-gates`, and the coverage upload | The lint tooling at the versions `Taskfile.yml` pins; Docker; and minutes of depot fetch and decompilation for the tree `task test` cuts the chip from, cached on the pinned manifest since those bytes move only when the pin does |
| `generated tables are current` | `check:codegen` | None; regeneration reads the checked-in inputs |
| `prefab reader` | `prefabreader:build`, `prefabreader:test` | The .NET SDK |
| `release scripts` | `tools:actionlint`, `lint:shell`, `lint:taskfile`, `lint:workflows`, `lint:container`, then the Linux artifact built and verified for real | actionlint, and Docker for the pinned shellcheck and hadolint images. `task check` runs `lint:shell` and `lint:taskfile` as well; `lint:workflows` and `lint:container` stay out of it, since actionlint comes from a tools target it does not run and both read the build machinery, which is this job's own |

`check:game-pin` is the one gate `task check` runs that no job here does. It asks the Steam depot which build it serves, so a per-commit job holding it would depend on Steam being reachable; the weekly workflow asks it instead. The two readings differ on an unreachable depot, which says nothing about the pin: the local gate passes and the weekly job fails, since asking is the whole of what that job does.

`task test` runs every package and subtracts nothing, so the chip and the decompile it is cut from are what the `check` job sets up before it. A second job holding them would restore the same tree and compile the same chip to run the same targets, so there is no toolchain boundary to draw one on.

There is one coverage profile and one upload. `task test` writes it over every package in the module, and the `check` job uploads it. A pull request from a fork gets no secrets and can upload nothing, so the step is skipped there rather than failed.

`task check:gamesrc-gates` reads the output of the two packages that take the decompiled tree as their subject rather than the repository. It runs each with `-v` and requires every test the package declares to appear as a top-level pass with no skip anywhere in the output, since `go test` exits 0 over a package that skipped everything in it, which is the state a missing decompile leaves the slicer's own tests in. It then runs the extractor over an empty tree and fails if that run exits 0 with no skip in its output, so a package that stopped reading the game at all is reported rather than read as clean. The chip slice is re-cut on every run and never cached, since the slicer refuses a game update that moved something.

No standard runner ships a usable LLVM at this version, so every Linux job that compiles the compiler installs `llvm-22-dev` and `clang-22` from apt.llvm.org first. The macOS job installs Homebrew's `llvm` and `zstd`, and the Windows artifact is cross-built in the release container against the MSYS2 toolchain the lock pins.
