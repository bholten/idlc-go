#!/usr/bin/env bash
# Run the JAR over every IDL in Core3 + engine3, saving the autogen to
# _baseline/jar/. Output is the canonical reference for diffing
# against idlc-go's output (see scripts/baseline-idlc-go.sh and
# scripts/baseline-diff.sh).
#
# Output layout: _baseline/jar/<tree>/<pkg>/<Class>.{h,cpp}
# where <tree> is "core3" or "engine3" (matches the source tree the
# IDL came from, since they have overlapping package paths).
#
# Override: CORE3_PATH=/abs/path bash <script>; PARALLEL=8 bash <script>.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CORE3_PATH="${CORE3_PATH:-${REPO_ROOT}/submodules/Core3}"
JAR="${CORE3_PATH}/MMOCoreORB/utils/engine3/MMOEngine/lib/idlc.jar"
CORE3_SRC="${CORE3_PATH}/MMOCoreORB/src"
ENGINE3_SRC="${CORE3_PATH}/MMOCoreORB/utils/engine3/MMOEngine/src"
BASELINE="${REPO_ROOT}/_baseline/jar"
PARALLEL="${PARALLEL:-8}"

if [[ ! -f "${JAR}" ]]; then
	echo "missing JAR: ${JAR}" >&2
	exit 1
fi

# Worker run for each IDL — exported so xargs subshells can call it.
# Args: $1 = absolute IDL path, $2 = src tree root, $3 = label.
compile_one() {
	local idl="$1"
	local src_root="$2"
	local label="$3"
	local rel="${idl#${src_root}/}"

	# `-outdir autogen` matches Core3's CMake `IDL_DIRECTIVES` so the
	# file-header comment (`autogen/<pkg>/Foo.h …`) is the same as the
	# idlc-go pass writes — keeps byte-equality the right comparison.
	if java -cp "${IDLC_JAR}" org.sr.idlc.compiler.Compiler \
		-outdir autogen \
		-cp "${IDLC_ENGINE3}" \
		-silence -rbcpp \
		-sd "${src_root}" "${rel}" 2>/dev/null; then
		return 0
	fi
	echo "    fail [${label}]: ${rel}" >&2
}
export -f compile_one
export IDLC_JAR="${JAR}"
export IDLC_ENGINE3="${ENGINE3_SRC}"

run_tree() {
	local src_root="$1"
	local label="$2"

	echo "[baseline:${label}] scanning ${src_root}"
	rm -rf "${src_root}/autogen"

	# JAR resolves -outdir relative to -sd, so output ends up at
	# <src_root>/autogen/<pkg>/. Parallel invocations write disjoint
	# files; mkdir is idempotent.
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
