# syntax=docker/dockerfile:1
# check=skip=FromPlatformFlagConstDisallowed

# Declared globally because two stages far below fetch from it, and a default per
# stage is one that can be moved in one place and not in the other.
ARG STEAM_MANIFEST=2546537964923579038

# Builds the Linux and Windows release artifacts with nothing installed on the
# host but Docker. Pinned by digest because a rebuilt tag is different bytes
# under a version string saying it is not, and this one goes into a published
# binary. Its own stage so the release targets can refresh the apt layer below.
FROM --platform=linux/amd64 golang:1.26-bookworm@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651 AS linux-toolchain

ARG LLVM_VERSION
ENV LLVM_VERSION=${LLVM_VERSION}

# Ahead of the source tree, so this layer's cache key is the pinned fingerprint
# that script holds rather than every file in the repository.
COPY scripts/fetch-llvm-key.sh /fetch-llvm-key.sh

# apt.llvm.org is a rolling snapshot within a major, so only the major is pinned
# and the signing key stands in for the rest; scripts/fetch-llvm-key.sh says why.
# scripts/setup-llvm.sh composes the same source line and gates the same two
# interpolations for the same reasons, which it states.
RUN set -eux; \
	case "${LLVM_VERSION}" in \
		"" | *[!0-9]*) echo "LLVM_VERSION is '${LLVM_VERSION}', which is not a bare major version number" >&2; exit 1 ;; \
	esac; \
	apt-get update; \
	apt-get install -y --no-install-recommends \
		ca-certificates curl file gnupg lsb-release \
		libffi-dev libzstd-dev zlib1g-dev; \
	codename="$(lsb_release -cs)"; \
	case "${codename}" in \
		"" | *[!a-z]*) echo "lsb_release calls this image's release '${codename}', which is not a Debian codename; the LLVM source line is built out of it twice and would name a suite no mirror serves" >&2; exit 1 ;; \
	esac; \
	/fetch-llvm-key.sh /usr/share/keyrings/apt.llvm.org.asc; \
	echo "deb [signed-by=/usr/share/keyrings/apt.llvm.org.asc] https://apt.llvm.org/${codename}/ llvm-toolchain-${codename}-${LLVM_VERSION} main" \
		> /etc/apt/sources.list.d/llvm.list; \
	apt-get update; \
	apt-get install -y --no-install-recommends "llvm-${LLVM_VERSION}-dev"; \
	rm -rf /var/lib/apt/lists/*

FROM linux-toolchain AS build-linux

ARG VERSION
ENV VERSION=${VERSION}

WORKDIR /src
COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
	--mount=type=cache,target=/go/pkg/mod \
	set -eux; \
	: "${VERSION:?build with --build-arg VERSION=<tag>; the verification below refuses the unstamped default}"; \
	mkdir -p /out; \
	scripts/build-release-linux.sh /out/ic11c; \
	scripts/verify-release-linux.sh /out/ic11c; \
	cp README.md /out/; \
	tar czf /out/ic11c_Linux_x86_64.tar.gz -C /out ic11c README.md

# BuildKit writes a scratch stage straight onto the host with `--output`, landing
# the files owned by whoever ran the build. A bind mount written from inside the
# container would leave them owned by root.
FROM scratch AS artifact-linux
COPY --from=build-linux /out/ic11c_Linux_x86_64.tar.gz /

# Cross-builds the Windows artifact from Linux. cgo cannot consume the official
# MSVC-ABI LLVM releases, so the LLVM here is MSYS2's UCRT64 build. Trixie, not
# bookworm: the g++-mingw-w64-ucrt64 cross compiler that targets the UCRT rather
# than msvcrt is not in bookworm.
FROM --platform=linux/amd64 golang:1.26-trixie@sha256:4ee9ffa999b4583ce281939cdff828763083610292f252279a0cee77473bd9a7 AS windows-toolchain

ARG LLVM_VERSION
ENV LLVM_VERSION=${LLVM_VERSION}

# Debian supplies only the host-side driver here. Every target-side artifact --
# libstdc++, the CRT, winpthreads -- comes from MSYS2 below, because Debian's
# GCC 14 and mingw-w64-crt 12 are two majors behind what MSYS2 compiled its
# LLVM against and the link fails on symbols theirs do not carry.
RUN set -eux; \
	case "${LLVM_VERSION}" in \
		"" | *[!0-9]*) echo "LLVM_VERSION is '${LLVM_VERSION}', which is not a bare major version number" >&2; exit 1 ;; \
	esac; \
	apt-get update; \
	apt-get install -y --no-install-recommends \
		ca-certificates curl zstd zip \
		g++-mingw-w64-ucrt64 gcc-mingw-w64-ucrt64 \
		binutils-mingw-w64-ucrt64 mingw-w64-ucrt64-dev; \
	rm -rf /var/lib/apt/lists/*

# On its own, ahead of the source tree, so this layer's cache key is the pins and
# not every file in the repository.
COPY scripts/msys2-ucrt64.lock /msys2.lock

# Pinned archives rather than a build-time resolve, so two builds of one tag
# unpack the same toolchain. This stage verifies no signature and re-derives
# nothing: what roots the digests is scripts/msys2-lock.sh, which says so.
# An aged-out lock fails here on a 404, because MSYS2 drops superseded packages.
RUN --mount=type=cache,target=/dl set -eux; \
	base=https://mirror.msys2.org/mingw/ucrt64; \
	mkdir -p /msys; \
	pinned() { awk -v want="mingw-w64-ucrt-x86_64-$1" \
		'$1 == want { found++; file = $2; sha = $3 } \
		END { if (found == 1) print file" "sha; else exit 1 }' /msys2.lock \
		|| { echo "scripts/msys2-ucrt64.lock does not pin ${1} exactly once; run 'task msys2:lock'" >&2; return 1; }; }; \
	names="$(sed -n 's/^# packages: //p' /msys2.lock)"; \
	[ -n "${names}" ] || { echo "scripts/msys2-ucrt64.lock carries no package list; run 'task msys2:lock'" >&2; exit 1; }; \
	awk -v names="${names}" ' \
		/^#/ || NF == 0 { next } \
		NF != 3 { print "  "$0" is not a name, filename and digest"; bad = 1; next } \
		{ row = $1; sub(/^mingw-w64-ucrt-x86_64-/, "", row); rows[row] = 1 } \
		END { \
			n = split(names, want, " "); \
			for (i = 1; i <= n; i++) known[want[i]] = 1; \
			for (r in rows) if (!(r in known)) { print "  "r" is pinned but not named"; bad = 1 } \
			exit bad \
		}' /msys2.lock \
		|| { echo "scripts/msys2-ucrt64.lock holds rows its '# packages:' header does not account for, so they are pinned, checked by nothing and never unpacked; run 'task msys2:lock'" >&2; exit 1; }; \
	llvm_entry="$(pinned llvm)"; \
	case "${llvm_entry%% *}" in \
		mingw-w64-ucrt-x86_64-llvm-${LLVM_VERSION}.*) ;; \
		*) echo "the lock pins '${llvm_entry%% *}', not LLVM ${LLVM_VERSION}" >&2; exit 1 ;; \
	esac; \
	for name in ${names}; do \
		entry="$(pinned "${name}")"; \
		pkg="${entry%% *}"; \
		sha="${entry##* }"; \
		[ -n "${pkg}" ] && [ -n "${sha}" ] && [ "${pkg}" != "${sha}" ] \
			|| { echo "scripts/msys2-ucrt64.lock pins no ${name}; run 'task msys2:lock'" >&2; exit 1; }; \
		echo "${sha}  /dl/${pkg}" | sha256sum -c --status - 2>/dev/null \
			|| { curl -sSLf -o "/dl/${pkg}" "${base}/${pkg}" \
				|| { echo "${base}/${pkg} is gone; MSYS2 drops superseded packages, so run 'task msys2:lock'" >&2; exit 1; }; \
				echo "${sha}  /dl/${pkg}" | sha256sum -c -; }; \
		tar --use-compress-program=unzstd -xf "/dl/${pkg}" -C /msys; \
	done; \
	rm -f /msys/.BUILDINFO /msys/.MTREE /msys/.PKGINFO

ENV MSYS_PREFIX=/msys/ucrt64

FROM windows-toolchain AS build-windows

ARG VERSION
ENV VERSION=${VERSION}

WORKDIR /src
COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
	--mount=type=cache,target=/go/pkg/mod \
	set -eux; \
	: "${VERSION:?build with --build-arg VERSION=<tag>; the verification below refuses the unstamped default}"; \
	mkdir -p /out; \
	scripts/build-release-windows.sh /out/ic11c.exe; \
	scripts/verify-release-windows.sh /out/ic11c.exe; \
	cp README.md /out/; \
	(cd /out && zip -q ic11c_Windows_x86_64.zip ic11c.exe README.md)

FROM scratch AS artifact-windows
COPY --from=build-windows /out/ic11c_Windows_x86_64.zip /

# Recovers the machine tables from the game's own assembly. It comes from the
# dedicated server rather than the client: app 600760 carries Valve's
# free-to-download flag, so an anonymous login reaches it and no account or
# licence is involved, and it ships the same definitions the client does.
FROM --platform=linux/amd64 mcr.microsoft.com/dotnet/sdk:10.0@sha256:72dd743782f2ae7e5476fd64f6a460045e3998dc862218b80e6944cba79a01b0 AS isa-tools

ARG DEPOTDOWNLOADER_VERSION=3.4.0
ARG DEPOTDOWNLOADER_SHA256=7419f65efb7eb16b6e56987ed1b76475f29c02475c0016166c84798388e796bc
# 9.x and 10.x fail on this SDK image, and 8.2 targets an older runtime, so it
# needs the roll-forward below to start at all. A nuget.org version is immutable,
# so unlike the two downloads it needs no digest beside it.
ARG ILSPYCMD_VERSION=8.2.0.7535

# Environment rather than build arguments so a container started from this stage
# carries them too: `task isa:manifest` runs one, and a single definition of the
# ids keeps that question and the pinned fetch below about the same depot.
ENV STEAM_APP=600760 STEAM_DEPOT=600762 \
	DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_NOLOGO=1 \
	DOTNET_ROLL_FORWARD=Major \
	PATH="/root/.dotnet/tools:${PATH}"

RUN set -eux; \
	apt-get update; \
	apt-get install -y --no-install-recommends ca-certificates curl unzip; \
	rm -rf /var/lib/apt/lists/*

# The framework build, not one of the self-contained ones: this image already
# carries the runtime, and the difference is 2 MB against 33 MB.
RUN set -eux; \
	curl -sSLf -o /tmp/depotdownloader.zip \
		"https://github.com/SteamRE/DepotDownloader/releases/download/DepotDownloader_${DEPOTDOWNLOADER_VERSION}/DepotDownloader-framework.zip"; \
	echo "${DEPOTDOWNLOADER_SHA256}  /tmp/depotdownloader.zip" | sha256sum -c -; \
	unzip -q /tmp/depotdownloader.zip -d /opt/depotdownloader; \
	rm /tmp/depotdownloader.zip

RUN dotnet tool install --global ilspycmd --version "${ILSPYCMD_VERSION}"

FROM isa-tools AS isa-decompile

ARG STEAM_MANIFEST

RUN set -eux; \
	printf 'regex:.*/Assembly-CSharp\\.dll$\n' > /tmp/filelist.txt; \
	dotnet /opt/depotdownloader/DepotDownloader.dll \
		-app "${STEAM_APP}" -depot "${STEAM_DEPOT}" -manifest "${STEAM_MANIFEST}" \
		-filelist /tmp/filelist.txt -dir /depot; \
	assembly="$(find /depot -name Assembly-CSharp.dll)"; \
	test -f "${assembly}"; \
	cp "${assembly}" /assembly.dll; \
	printf '%s' "${STEAM_MANIFEST}" > /manifest

# A file per type under its namespace, which is what lets the extractor find a
# type the game source names without a namespace -- as the enums registered on
# the chip are.
RUN ilspycmd --disable-updatecheck --nested-directories --project --outputdir /decompiled /assembly.dll

# Recovers the prefab roster, which the assembly does not carry: what the game
# ships, what each hashes to, which class drives it, and the serialized state its
# logic surface is a function of. That lives in the Unity serialized files, so
# this stage fetches game data rather than game code.
FROM isa-tools AS isa-prefabs

ARG STEAM_MANIFEST
# The engine class layouts, which the build ships no type trees for. A GitHub
# artifact rather than Steam content, so it is pinned by commit and checked
# against its digest the way DepotDownloader is. The script types are covered
# instead by the Managed directory the file list below keeps.
ARG CLASSDATA_COMMIT=5adb448deeefa1b88881f1fa44243009b352db3a
ARG CLASSDATA_SHA256=129e1f80f930415db6779fe6089afa75280cb51462bcee812beab6cd81a764c6

RUN set -eux; \
	curl -sSLf -o /opt/classdata.tpk \
		"https://raw.githubusercontent.com/nesrak1/UABEA/${CLASSDATA_COMMIT}/ReleaseFiles/classdata.tpk"; \
	echo "${CLASSDATA_SHA256}  /opt/classdata.tpk" | sha256sum -c -

# What keeps this off most of a 5 GB depot. resources.assets holds the roster and
# every prefab it points at, and that copy points nowhere else, so the scene
# files and the other shared asset files are not needed.
RUN set -eux; \
	{ \
		printf 'regex:^rocketstation_DedicatedServer_Data/Managed/.*\\.dll$\n'; \
		printf 'regex:^rocketstation_DedicatedServer_Data/resources\\.assets(\\.resS)?$\n'; \
		printf 'regex:^rocketstation_DedicatedServer_Data/globalgamemanagers(\\.assets)?$\n'; \
		printf 'regex:^rocketstation_DedicatedServer_Data/sharedassets0\\.assets$\n'; \
		printf 'regex:^rocketstation_DedicatedServer_Data/StreamingAssets/Language/english\\.xml$\n'; \
	} > /tmp/filelist.txt; \
	dotnet /opt/depotdownloader/DepotDownloader.dll \
		-app "${STEAM_APP}" -depot "${STEAM_DEPOT}" -manifest "${STEAM_MANIFEST}" \
		-filelist /tmp/filelist.txt -dir /depot

WORKDIR /reader
COPY tools/prefabreader/ .

RUN --mount=type=cache,target=/root/.nuget/packages \
	set -eux; \
	dotnet publish -c Release -o /reader/out; \
	dotnet /reader/out/prefabreader.dll \
		/depot/rocketstation_DedicatedServer_Data \
		/opt/classdata.tpk \
		/prefabs.json; \
	cp /depot/rocketstation_DedicatedServer_Data/StreamingAssets/Language/english.xml /english.xml

# Every image the extraction runs on is pinned by digest, this one included: it
# runs the extractor, the extractor writes the checked-in JSON, and a rebuilt tag
# would move that JSON under a manifest saying the game did not.
FROM --platform=linux/amd64 golang:1.26-bookworm@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651 AS isa-extract

COPY --from=isa-decompile /decompiled /decompiled
COPY --from=isa-decompile /assembly.dll /manifest /
COPY --from=isa-prefabs /prefabs.json /english.xml /

WORKDIR /src
COPY . .

# Both tables are recovered in one run and --isa names the fresh file rather than
# the checked-in one, so the pair this stage emits cannot describe two game
# builds. The roster arrives from a separate depot fetch the manifest says
# nothing about, so `devices` refuses one whose version is not what was decompiled.
RUN --mount=type=cache,target=/root/.cache/go-build \
	--mount=type=cache,target=/go/pkg/mod \
	set -eux; \
	mkdir -p /out/internal/isa; \
	go run ./tools/isagen extract \
		--source /decompiled \
		--assembly /assembly.dll \
		--manifest "$(cat /manifest)" \
		--out /out/internal/isa/isa.json; \
	go run ./tools/isagen devices \
		--source /decompiled \
		--assembly /assembly.dll \
		--manifest "$(cat /manifest)" \
		--prefabs /prefabs.json \
		--names /english.xml \
		--isa /out/internal/isa/isa.json \
		--out /out/internal/isa/devices.json

FROM scratch AS isa-artifact
COPY --from=isa-extract /out/ /

# Fingerprints the game types the interpreter, the chip limits and the quirk list
# are transliterated from. A sibling of isa-extract rather than a stage on top of
# it: extraction stops on a game build that changed a table it asserts, which is
# exactly the build whose C# a reviewer needs to see, so the two fail separately.
FROM --platform=linux/amd64 golang:1.26-bookworm@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651 AS isa-digest

COPY --from=isa-decompile /decompiled /decompiled
COPY --from=isa-decompile /assembly.dll /manifest /

WORKDIR /src
COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
	--mount=type=cache,target=/go/pkg/mod \
	set -eux; \
	mkdir -p /out/gamesrc; \
	go run ./tools/isagen digest \
		--source /decompiled \
		--assembly /assembly.dll \
		--manifest "$(cat /manifest)" \
		--out /out/gamesrc/csharp.digest; \
	go run ./tools/isagen dump --source /decompiled --out /source

FROM scratch AS isa-digest-artifact
COPY --from=isa-digest /out/ /

# The decompiled source itself, which is deliberately not checked in: it is
# game code, and the digest above is what the repository carries instead.
FROM scratch AS isa-source-artifact
COPY --from=isa-digest /source/ /

# The whole decompiled assembly, which tools/chipgen cuts the conformance compile
# unit out of. Exported from isa-decompile rather than the Go stage above: a game
# build that moved something the matchers assert must still yield the source that
# says what moved.
FROM scratch AS isa-source-full-artifact
COPY --from=isa-decompile /decompiled/ /
