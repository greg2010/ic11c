#!/usr/bin/env bash
# Writes the apt.llvm.org signing key, and only if it is the one this repository
# trusts. The packages are a rolling snapshot and cannot be pinned by version;
# what holds the Linux artifact together is this key, which signs the Release
# file every package digest is reached through.
set -euo pipefail

# The primary key, which is what signs the repository. Its subkeys are not
# asserted: the owner may rotate them without the key's identity changing.
# Exactly one primary is required, so a second appended to the armour is a
# mismatch rather than an extra a substring match would step over.
fingerprint=6084F3CF814B57C1CF12EFD515CF4D18AF4F7421

url=https://apt.llvm.org/llvm-snapshot.gpg.key

out="${1:?usage: fetch-llvm-key.sh <destination>}"

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

curl -fsSL -o "${work}/key.asc" "${url}"

# --show-keys reads the file without importing, so nothing reaches a keyring
# before the fingerprint is known. A pub record opens a primary key and the fpr
# after it is that key's fingerprint, so pairing them in order makes a second key
# an extra line here rather than something a bare grep would pass.
found="$(gpg --homedir "${work}" --batch --quiet --show-keys --with-colons "${work}/key.asc" | awk -F: '
	$1 == "pub" { primary = 1; next }
	$1 == "fpr" && primary { print $10; primary = 0 }
')"

if [ -z "${found}" ]; then
	echo "nothing in what ${url} served parses as an OpenPGP primary key" >&2
	exit 1
fi

if [ "${found}" != "${fingerprint}" ]; then
	echo "${url} served primary key(s):" >&2
	printf '%s\n' "${found}" | sed 's/^/  /' >&2
	echo "this build trusts ${fingerprint} and nothing else" >&2
	exit 1
fi

# Moved into place only once the fingerprint is known, so a rejected key leaves
# no file for a later step to install by mistake.
mv "${work}/key.asc" "${out}"
