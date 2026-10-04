#!/usr/bin/env bash
# make conformance TARGET=<name>: the whole suite for one target
# (conformance/targets/<name>.yaml): the national and ED-318 tests, the
# uss_qualifier reports and the axe results given, the report signed when
# a key is given, gated on conformance/baseline/<name>.json when it
# exists. The exit status is cmd/conformance's (0 pass or incomplete, or
# no regression; 1 a failure or a regression; 2 a configuration error).
#
#   CONFORMANCE_ENV               KEY=VALUE file resolving the target's ${VAR}s
#   CONFORMANCE_OUT               default conformance/report/runs
#   CONFORMANCE_QUALIFIER_REPORTS "F3411-DP=path/report.json ..." (run-qualifier.sh output)
#   CONFORMANCE_AXE               conformance/axe results (axe-results.json)
#   CONFORMANCE_SIGN_KEY          the lab issuer's signing-key.pem
#   GO                            the go command
set -euo pipefail
cd "$(dirname "$0")/.."

target="${1:?usage: conformance/run-target.sh <target name>}"
file="conformance/targets/$target.yaml"
if [ ! -f "$file" ]; then
  echo "conformance: no target $file (one of: $(cd conformance/targets && ls ./*.yaml | sed 's|^\./||; s|\.yaml$||' | tr '\n' ' '))" >&2
  exit 2
fi
GO="${GO:-go}"
args=(run --target "$file" --out "${CONFORMANCE_OUT:-conformance/report/runs}")
if [ -n "${CONFORMANCE_ENV:-}" ]; then args+=(--env-file "$CONFORMANCE_ENV"); fi
if [ -n "${CONFORMANCE_AXE:-}" ]; then args+=(--axe "$CONFORMANCE_AXE"); fi
for q in ${CONFORMANCE_QUALIFIER_REPORTS:-}; do args+=(--qualifier-report "$q"); done
name="${CONFORMANCE_TARGET_NAME:-$target}"
if [ -f "conformance/baseline/$name.json" ]; then
  args+=(--baseline "conformance/baseline/$name.json")
else
  echo "conformance: no baseline conformance/baseline/$name.json: the run is judged by its verdict"
fi
rc=0
"$GO" run ./cmd/conformance "${args[@]}" || rc=$?
exit "$rc"
