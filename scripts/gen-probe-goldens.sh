#!/usr/bin/env bash
# Regenerate JAR-emitted goldens for every probe IDL under
# testdata/probe/src/probe/. Output lands in testdata/probe/expected/probe/.
#
# Add a new probe by dropping <Name>.idl into testdata/probe/src/probe/ and
# rerunning this script.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="${REPO_ROOT}/testdata/probe/src"
EXPECTED="${REPO_ROOT}/testdata/probe/expected/probe"

# Locate Core3 — defaults to ./submodules/Core3. Clone SWGEmu/Core3
# (recursively, for the engine3 submodule) there, or set CORE3_PATH to
# point anywhere else. The JAR ships inside Core3's engine3 submodule
# and is the same binary as the one we used to use at `ref/idlc.jar`
# (md5 match verified).
CORE3_PATH="${CORE3_PATH:-${REPO_ROOT}/submodules/Core3}"
JAR="${CORE3_PATH}/MMOCoreORB/utils/engine3/MMOEngine/lib/idlc.jar"
ENGINE3_SRC="${CORE3_PATH}/MMOCoreORB/utils/engine3/MMOEngine/src"

if [[ ! -f "${JAR}" ]]; then
	echo "missing ${JAR}" >&2
	echo "Clone SWGEmu/Core3 to ./submodules/Core3 or set CORE3_PATH=/abs/path." >&2
	exit 1
fi
if [[ ! -d "${ENGINE3_SRC}" ]]; then
	echo "missing engine3 source: ${ENGINE3_SRC}" >&2
	echo "(Re-clone Core3 with --recursive so engine3 is initialised.)" >&2
	exit 1
fi

# JAR quirk: -od is treated as path-relative-to-sd even when given an
# absolute path, so a literal /tmp/X ends up at <sd>/tmp/X. Pick a
# relative -od and let the JAR put output under the source tree, then
# clean it up.
# JAR writes its `-od` value into the file-header comment verbatim, so
# we mirror the production tree's `autogen/<pkg>/Foo.h` form to keep the
# probe goldens visually consistent with the corpus goldens.
OD_REL="autogen"
OUT="${SRC}/${OD_REL}"
trap 'rm -rf "${OUT}" 2>/dev/null || true' EXIT

mkdir -p "${EXPECTED}"
rm -rf "${OUT}"

shopt -s nullglob
for idl in "${SRC}"/probe/*.idl; do
	rel="probe/$(basename "${idl}")"
	echo "[probe] ${rel}"
	java -cp "${JAR}" org.sr.idlc.compiler.Compiler \
		-cp "${ENGINE3_SRC}" \
		-sd "${SRC}" -od "${OD_REL}" -rbcpp "${rel}" >/dev/null

	name="$(basename "${idl}" .idl)"
	gen_h="${OUT}/probe/${name}.h"
	gen_cpp="${OUT}/probe/${name}.cpp"
	if [[ ! -f "${gen_h}" ]]; then
		echo "  no output for ${rel}" >&2
		exit 2
	fi
	cp "${gen_h}" "${EXPECTED}/${name}.h"
	cp "${gen_cpp}" "${EXPECTED}/${name}.cpp"
done

echo "[probe] regenerated $(ls "${EXPECTED}"/*.h 2>/dev/null | wc -l) probe goldens"
