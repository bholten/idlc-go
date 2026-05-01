#!/usr/bin/env bash
# Extract every first-method RPC seed from the JAR baseline at
# _baseline/jar and print them as Go map entries ready to paste into
# `legacyRPCSeeds` in internal/sema/rpc.go.
#
# Each .cpp file in the baseline starts with:
#   enum {RPC_FIRSTMETHOD__SIGSUFFIX_ = NNNN, ...};
# We read each .cpp, find the first method's signature suffix and seed,
# look up the package + class + signature from the corresponding .idl
# (in Core3 src), and emit:
#   "<pkg>.<Class>.<methodCamelCase>(<idlTypes>)": NNNN,
#
# This has to consult the IDL because the Go map's key uses
# original-case method name + IDL type names, not the upper-cased
# RPC enum form.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CORE3_PATH="${CORE3_PATH:-${REPO_ROOT}/submodules/Core3}"
BASELINE="${REPO_ROOT}/_baseline/jar"
CORE3_SRC="${CORE3_PATH}/MMOCoreORB/src"
ENGINE3_SRC="${CORE3_PATH}/MMOCoreORB/utils/engine3/MMOEngine/src"

if [[ ! -d "${BASELINE}" ]]; then
	echo "missing ${BASELINE} — run \`make baseline-jar\` first" >&2
	exit 1
fi

# For each .cpp, extract: { package qname (from IDL), class name, first
# RPC method seed }. Then find the IDL's first non-@local public method
# and build the Go-map key.
find "${BASELINE}" -name '*.cpp' | sort | while read -r cpp; do
	# Extract first enum entry with seed: RPC_<UCNAME>__<UCSUFFIX> = N
	enum_line=$(grep -m1 '^enum {RPC_' "${cpp}" || true)
	if [[ -z "${enum_line}" ]]; then continue; fi
	# Parse: "RPC_FOO__BAR_ = 12345"
	first_entry=$(echo "${enum_line}" | sed -E 's/^enum \{//; s/,.*//; s/\};.*//')
	if ! echo "${first_entry}" | grep -q '='; then
		continue   # no seed = enum auto-numbers from 0; nothing to extract
	fi
	seed=$(echo "${first_entry}" | sed -E 's/.* = ([0-9]+).*/\1/')

	# Locate the corresponding IDL. Path: _baseline/jar/<tree>/<pkg>/<Class>.cpp
	rel="${cpp#${BASELINE}/}"
	tree="${rel%%/*}"
	rel_after="${rel#${tree}/}"
	pkg_path="$(dirname "${rel_after}")"
	cls="$(basename "${rel_after}" .cpp)"

	case "${tree}" in
		core3)   src_root="${CORE3_SRC}" ;;
		engine3) src_root="${ENGINE3_SRC}" ;;
		*) continue ;;
	esac

	idl="${src_root}/${pkg_path}/${cls}.idl"
	if [[ ! -f "${idl}" ]]; then continue; fi

	# Pull `package …;` from the IDL to use as the Go-map qname.
	pkg_qname=$(grep -m1 '^package ' "${idl}" | sed -E 's/^package +([^;]+);.*/\1/')

	# Find the first non-@local public method's signature. We look for
	# the first RPC enum entry's method name in the IDL and grab the
	# matching method signature. The RPC enum's name is
	# `RPC_<METHODUC>__<SUFFIX>` — extract <METHODUC>.
	rpc_name=$(echo "${first_entry}" | awk '{print $1}')
	method_uc=$(echo "${rpc_name}" | sed -E 's/^RPC_([^_]*[A-Z0-9]+)__.*/\1/')
	# Grep for the first non-comment, non-@local method whose name (case-
	# insensitive) matches method_uc. Print params.
	# This is heuristic — IDL parsing in bash is rough.
	method_line=$(awk -v ucname="${method_uc}" '
		BEGIN { lower = tolower(ucname) }
		/^[[:space:]]*\/\// { next }
		/@local/ { skip = 1; next }
		skip && /^[[:space:]]*$/ { skip = 0; next }
		skip { next }
		# Look for "name(...)" pattern with case-insensitive match.
		{
			if (match($0, /[A-Za-z_][A-Za-z0-9_]*\(/)) {
				name = substr($0, RSTART, RLENGTH-1)
				if (tolower(name) == lower) {
					print
					exit
				}
			}
		}
	' "${idl}")

	if [[ -z "${method_line}" ]]; then continue; fi

	# Extract method name (preserving original case) and IDL param types.
	method_name=$(echo "${method_line}" | grep -oE '[A-Za-z_][A-Za-z0-9_]*\(' | head -1 | tr -d '(')
	# Params are between ( and ). Strip annotations / `final` modifiers,
	# split on comma, take the first token of each (the type).
	params_raw=$(echo "${method_line}" | sed -E 's/.*\(([^)]*)\).*/\1/')
	if [[ -z "${params_raw}" ]]; then
		params_csv=""
	else
		params_csv=$(echo "${params_raw}" \
			| sed -E 's/@[A-Za-z]+(\([^)]*\))?[[:space:]]*//g' \
			| sed -E 's/(^|[, ]+)final[[:space:]]+/\1/g' \
			| awk -F, '{
				for (i=1; i<=NF; i++) {
					gsub(/^[[:space:]]+|[[:space:]]+$/, "", $i)
					n = split($i, parts, "[[:space:]]+")
					if (n >= 2) {
						# Reassemble all but the last token (which is the param name).
						type = parts[1]
						for (j=2; j<n; j++) type = type " " parts[j]
						printf "%s%s", (i>1 ? "," : ""), type
					}
				}
			}')
	fi

	printf '\t"%s.%s.%s(%s)": %s,\n' \
		"${pkg_qname}" "${cls}" "${method_name}" "${params_csv}" "${seed}"
done
