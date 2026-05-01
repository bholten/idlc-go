#!/usr/bin/env bash
# Run idlc-go over every IDL in Core3 + engine3, saving the autogen to
# _baseline/idlc-go/. Mirror of scripts/baseline-jar.sh — same input
# set, same output layout, different binary. Diff the two trees with
# scripts/baseline-diff.sh.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CORE3_PATH="${CORE3_PATH:-${REPO_ROOT}/submodules/Core3}"
CORE3_SRC="${CORE3_PATH}/MMOCoreORB/src"
ENGINE3_SRC="${CORE3_PATH}/MMOCoreORB/utils/engine3/MMOEngine/src"
BASELINE="${REPO_ROOT}/_baseline/idlc-go"
PARALLEL="${PARALLEL:-8}"

# Build the binary fresh.
echo "[baseline:idlc-go] building binary"
( cd "${REPO_ROOT}" && go build -o "${REPO_ROOT}/_baseline/idlc-go-bin" ./cmd/idlc )
BIN="${REPO_ROOT}/_baseline/idlc-go-bin"

compile_one() {
	local idl="$1"
	local src_root="$2"
	local label="$3"
	local rel="${idl#${src_root}/}"

	if "${IDLC_BIN}" \
		-outdir autogen \
		-cp "${IDLC_ENGINE3}" \
		-silence -rbcpp \
		-sd "${src_root}" "${rel}" 2>/dev/null; then
		return 0
	fi
	echo "    fail [${label}]: ${rel}" >&2
}
export -f compile_one
export IDLC_BIN="${BIN}"
export IDLC_ENGINE3="${ENGINE3_SRC}"

run_tree() {
	local src_root="$1"
	local label="$2"

	echo "[baseline:${label}] scanning ${src_root}"
	rm -rf "${src_root}/autogen"

	find "${src_root}" -name '*.idl' -type f -print0 \
		| xargs -0 -n1 -P "${PARALLEL}" -I{} \
			bash -c 'compile_one "$1" "$2" "$3"' _ {} "${src_root}" "${label}"

	if [[ -d "${src_root}/autogen" ]]; then
		mkdir -p "${BASELINE}/${label}"
		cp -a "${src_root}/autogen/." "${BASELINE}/${label}/"
		rm -rf "${src_root}/autogen"
	fi
}

rm -rf "${BASELINE}"
mkdir -p "${BASELINE}"

run_tree "${ENGINE3_SRC}" "engine3"
run_tree "${CORE3_SRC}" "core3"

count_h=$(find "${BASELINE}" -name '*.h' | wc -l)
count_cpp=$(find "${BASELINE}" -name '*.cpp' | wc -l)
echo "[baseline:done] ${count_h} .h + ${count_cpp} .cpp files in ${BASELINE}"
