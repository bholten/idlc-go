#!/usr/bin/env bash
# Diff _baseline/jar/ against _baseline/idlc-go/ and report.
#
# Output sections:
#   1. files only in JAR baseline (idlc-go failed to produce)
#   2. files only in idlc-go baseline (we generated something the JAR didn't — unlikely)
#   3. byte-equal vs differing pair counts
#   4. first N differing files with a short snippet of the diff
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
JAR_DIR="${REPO_ROOT}/_baseline/jar"
GO_DIR="${REPO_ROOT}/_baseline/idlc-go"
SHOW_N="${SHOW_N:-10}"

if [[ ! -d "${JAR_DIR}" ]]; then
	echo "missing ${JAR_DIR} — run \`make baseline-jar\` first" >&2
	exit 1
fi
if [[ ! -d "${GO_DIR}" ]]; then
	echo "missing ${GO_DIR} — run \`make baseline-idlc-go\` first" >&2
	exit 1
fi

# Build sorted file lists relative to each baseline root.
jar_files="$(cd "${JAR_DIR}" && find . -type f \( -name '*.h' -o -name '*.cpp' \) | sort)"
go_files="$(cd "${GO_DIR}" && find . -type f \( -name '*.h' -o -name '*.cpp' \) | sort)"

only_jar="$(comm -23 <(echo "${jar_files}") <(echo "${go_files}") || true)"
only_go="$(comm -13 <(echo "${jar_files}") <(echo "${go_files}") || true)"
both="$(comm -12 <(echo "${jar_files}") <(echo "${go_files}") || true)"

count_only_jar=$(echo "${only_jar}" | grep -c . || true)
count_only_go=$(echo "${only_go}" | grep -c . || true)
count_both=$(echo "${both}" | grep -c . || true)

echo "=== file counts ==="
echo "  only in JAR baseline:  ${count_only_jar}"
echo "  only in idlc-go:       ${count_only_go}"
echo "  in both:               ${count_both}"
echo

if [[ "${count_only_jar}" -gt 0 ]]; then
	echo "=== files only in JAR (idlc-go failed to produce, top ${SHOW_N}) ==="
	echo "${only_jar}" | head -n "${SHOW_N}"
	echo
fi

if [[ "${count_only_go}" -gt 0 ]]; then
	echo "=== files only in idlc-go (top ${SHOW_N}) ==="
	echo "${only_go}" | head -n "${SHOW_N}"
	echo
fi

# For files in both, count byte-equal vs differing.
equal=0
differ=0
diff_paths=()

while IFS= read -r f; do
	[[ -z "${f}" ]] && continue
	if cmp -s "${JAR_DIR}/${f}" "${GO_DIR}/${f}"; then
		equal=$((equal + 1))
	else
		differ=$((differ + 1))
		diff_paths+=("${f}")
	fi
done <<< "${both}"

echo "=== diff summary (files in both) ==="
echo "  byte-equal: ${equal}"
echo "  differing:  ${differ}"
echo

if [[ "${differ}" -gt 0 ]]; then
	echo "=== first ${SHOW_N} differing files (short diff per file) ==="
	for f in "${diff_paths[@]:0:${SHOW_N}}"; do
		echo
		echo "--- ${f} ---"
		diff -u "${JAR_DIR}/${f}" "${GO_DIR}/${f}" | head -n 30
	done
fi
