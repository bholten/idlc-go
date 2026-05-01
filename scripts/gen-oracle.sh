#!/usr/bin/env bash
# Regenerate the JAR-derived hash oracle.
#
# Runs idlc.jar over scripts/oracle/src/probe/Probe.idl and prints the
# field-name → 32-bit-hash pairs found in the generated C++. The output
# is formatted to paste straight into internal/hash/crc_bzip2_test.go.
#
# Locates the JAR at ${CORE3_PATH}/MMOCoreORB/utils/engine3/MMOEngine/lib/
# (set CORE3_PATH or run `make pull-core3` to populate it).
#
# To extend the oracle, edit scripts/oracle/src/probe/Probe.idl, add the
# fields you want hashed, then re-run this script.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CORE3_PATH="${CORE3_PATH:-${REPO_ROOT}/submodules/Core3}"
JAR="${CORE3_PATH}/MMOCoreORB/utils/engine3/MMOEngine/lib/idlc.jar"
SRC="${REPO_ROOT}/scripts/oracle/src"
OUT="$(mktemp -d)"
trap 'rm -rf "${OUT}"' EXIT

if [[ ! -f "${JAR}" ]]; then
	echo "missing ${JAR}" >&2
	echo "set CORE3_PATH or run \`make pull-core3\` to populate submodules/Core3" >&2
	exit 1
fi

java -cp "${JAR}" org.sr.idlc.compiler.Compiler \
	-sd "${SRC}" -od "${OUT}" -rbcpp probe/Probe.idl >/dev/null

# idlc resolves -od relative to -sd, not cwd, so the real output is here:
GEN="${SRC}/$(basename "${OUT}")/probe/Probe.cpp"
if [[ ! -f "${GEN}" ]]; then
	# Fall back to whatever the JAR actually produced.
	GEN="$(find "${SRC}" -name Probe.cpp -newer "${JAR}" | head -1)"
fi

grep -hE '0x[0-9a-fA-F]{8}.*//' "${GEN}" \
	| sed -E 's/.*(0x[0-9a-fA-F]{8}).*\/\/(.*)/"\2": \1,/' \
	| sort -u

# Clean up the JAR's side-effect output dir under src/.
rm -rf "${SRC}"/out "${SRC}"/$(basename "${OUT}") 2>/dev/null || true
