#!/usr/bin/env bash
# Rewrites the MSYS2 UCRT64 package pins the Windows release toolchain unpacks.
# Every filename and digest below ends up inside the published Windows binary, so
# MSYS2's database is held to MSYS2's signature first: mirror.msys2.org redirects
# to community mirrors, and https settles who answered, not what they may say.
set -euo pipefail

# shellcheck source=release-common.sh
. "$(dirname "${BASH_SOURCE[0]}")/release-common.sh"

base=https://mirror.msys2.org/mingw/ucrt64
lock="${release_root}/scripts/msys2-ucrt64.lock"

# The MSYS2 development key, which signs the ucrt64 database. The primary is
# pinned rather than the masters that certified it, so a rotated signer stops
# this and is re-established by a person reading upstream. Only the fingerprint
# is trusted; every other key in the bundle below is discarded.
msys2_fingerprint=5F944B027F7FE2091985AA2EFA11531AA0AA7F57
msys2_keyring_url=https://raw.githubusercontent.com/msys2/MSYS2-keyring/main/msys2.gpg

# Before anything is fetched, so a machine missing one says so instead of
# downloading a database it has no way to check.
for tool in curl gpg gpgv; do
	if ! command -v "${tool}" >/dev/null 2>&1; then
		echo "${tool} is not on PATH; the MSYS2 package database cannot be fetched and checked against its signature without it" >&2
		exit 1
	fi
done

# What the cross link needs, and the only place the list is written down: it is
# carried into the lock's header below and the container reads it from there, so
# a package added here reaches the container by rerunning this rather than by a
# second edit that can be forgotten.
packages=(llvm gcc crt headers winpthreads zlib zstd libffi libxml2 libiconv)

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

curl -sSLf -o "${work}/keyring.gpg" "${msys2_keyring_url}"

# Imported into a throwaway homedir and exported back out by fingerprint, so the
# keyring handed to gpgv below holds the pinned key alone: a bundle carrying an
# extra key contributes none of it.
gpg --homedir "${work}" --batch --quiet --import "${work}/keyring.gpg"
gpg --homedir "${work}" --batch --export "${msys2_fingerprint}" >"${work}/signer.gpg"

# Exporting a fingerprint the bundle does not carry writes an empty file and
# exits 0, so emptiness is what says the pinned key is not there. Left to gpgv it
# would surface as a keyring refused for a reason of its own.
if [ ! -s "${work}/signer.gpg" ]; then
	echo "MSYS2's keyring at ${msys2_keyring_url} carries no key ${msys2_fingerprint}, so nothing here can establish who signed the package database" >&2
	exit 1
fi

# Read back out of what was exported rather than taken to be what was asked for.
# The pub/fpr pairing is the one scripts/fetch-llvm-key.sh explains.
found="$(gpg --homedir "${work}" --batch --quiet --show-keys --with-colons "${work}/signer.gpg" | awk -F: '
	$1 == "pub" { primary = 1; next }
	$1 == "fpr" && primary { print $10; primary = 0 }
')"

if [ "${found}" != "${msys2_fingerprint}" ]; then
	echo "the keyring this lock would be written from holds primary key(s):" >&2
	printf '%s\n' "${found}" | sed 's/^/  /' >&2
	echo "and this trusts ${msys2_fingerprint} and nothing else" >&2
	exit 1
fi

curl -sSLf -o "${work}/ucrt64.db" "${base}/ucrt64.db"
curl -sSLf -o "${work}/ucrt64.db.sig" "${base}/ucrt64.db.sig"

# Ahead of the extraction and not merely of the parse: an unverified database is
# a set of paths tar would write. This establishes that the pairs below are the
# ones MSYS2 published, which is what makes the container's later check of an
# archive against its digest mean anything.
gpgv --keyring "${work}/signer.gpg" "${work}/ucrt64.db.sig" "${work}/ucrt64.db"

mkdir -p "${work}/db"
tar -xf "${work}/ucrt64.db" -C "${work}/db"

# One desc file per package, each a sequence of %KEY% headers with their value on
# the following line. Filename and digest are taken together, so an entry
# carrying only one is rejected rather than yielding a pin with an empty half.
resolve() {
	awk -v want="$1" '
		FNR == 1 { file = ""; name = ""; sha = "" }
		/^%FILENAME%$/ { getline; file = $0 }
		/^%NAME%$/ { getline; name = $0 }
		/^%SHA256SUM%$/ { getline; sha = $0 }
		file != "" && name == want && sha != "" { print file, sha; exit }
	' "${work}"/db/*/desc
}

{
	echo "# MSYS2 UCRT64 packages the Windows release toolchain unpacks: name,"
	echo "# filename, sha256. Rewritten by scripts/msys2-lock.sh; read the diff."
	# The container requires a pin for each of these, so a row deleted by hand
	# fails the build by name rather than by an absent archive at link time.
	echo "# packages: ${packages[*]}"
	for name in "${packages[@]}"; do
		want="mingw-w64-ucrt-x86_64-${name}"
		entry="$(resolve "${want}")"
		file="${entry%% *}"
		sha="${entry##* }"
		if [ -z "${file}" ] || [ -z "${sha}" ] || [ "${file}" = "${sha}" ]; then
			echo "the ucrt64 database resolves no filename and digest for ${want}" >&2
			exit 1
		fi

		# The archives decide what the bindings link against, so MSYS2 moving on
		# has to stop this rather than pin the next major.
		if [ "${name}" = llvm ]; then
			case "${file}" in
				"mingw-w64-ucrt-x86_64-llvm-${LLVM_VERSION}."*) ;;
				*)
					echo "MSYS2 serves ${file}, not LLVM ${LLVM_VERSION}" >&2
					exit 1
					;;
			esac
		fi

		printf '%s %s %s\n' "${want}" "${file}" "${sha}"
	done
} >"${lock}.new"

mv "${lock}.new" "${lock}"
echo "wrote ${lock}"
