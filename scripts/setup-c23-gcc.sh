#!/usr/bin/env bash
# Puts a gcc that understands -std=c23 on PATH, which the C conformance gate
# compiles the corpus with. gcc first accepted the flag in 14, and Ubuntu 24.04
# defaults to 13.
set -euo pipefail

probe() {
	gcc -std=c23 -E -x c /dev/null >/dev/null 2>&1
}

if probe; then
	exit 0
fi

sudo apt-get update
sudo apt-get install -y gcc-14
sudo update-alternatives --install /usr/bin/gcc gcc /usr/bin/gcc-14 100

# Asserted rather than assumed: an install that left the old driver first would
# otherwise surface as the gate failing on every program in the corpus.
if ! probe; then
	echo "gcc still does not accept -std=c23 after installing gcc-14" >&2
	exit 1
fi
