#!/usr/bin/env bash
# make conformance TARGET=<name>: the whole suite for one target
# (conformance/targets/<name>.yaml): the national and ED-318 tests, the
# uss_qualifier reports and the axe results given, the report signed when
# a key is given, gated on conformance/baseline/<name>.json when it
# exists. The exit status is cmd/conformance's (0 pass, or no regression
# and nothing unreviewed left unchecked; 1 a failure or a regression; 2 a
# configuration error; 3 incomplete: gate requirements not checked).
#
#   CONFORMANCE_ENV               KEY=VALUE file resolving the target's ${VAR}s
#   CONFORMANCE_OUT               default conformance/report/runs
#   CONFORMANCE_QUALIFIER_REPORTS "F3411-DP=path/report.json ..." (run-qualifier.sh output)
#   CONFORMANCE_AXE               conformance/axe results (axe-results.json)
#   CONFORMANCE_SIGN_KEY          the lab issuer's signing-key.pem
#   CONFORMANCE_ALLOW_INCOMPLETE  1 accepts an incomplete run (exit 0, not 3)
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
if [ "${CONFORMANCE_ALLOW_INCOMPLETE:-}" = 1 ]; then args+=(--allow-incomplete); fi
name="${CONFORMANCE_TARGET_NAME:-$target}"
if [ -f "conformance/baseline/$name.json" ]; then
  args+=(--baseline "conformance/baseline/$name.json")
else
  echo "conformance: no baseline conformance/baseline/$name.json: the run is judged by its verdict"
fi
# Built, not `go run`: go run turns every non-zero exit into 1, and the
# status is the verdict (1 failed, 2 misconfigured, 3 incomplete).
bin="$(mktemp -d)"
trap 'rm -rf "$bin"' EXIT
"$GO" build -o "$bin/conformance.exe" ./cmd/conformance
rc=0
"$bin/conformance.exe" "${args[@]}" || rc=$?
exit "$rc"
