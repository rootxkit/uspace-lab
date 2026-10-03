#!/usr/bin/env bash
# Run every scenario marked `reference: true` (except the self-test, which
# internal/runner's tests run and which fails by design) against the
# lab's reference target with synthetic vehicles, in parallel, and fail
# if any fails. Each run starts its own reference target on its own
# loopback port. The exit status of every run is collected; none is
# masked.
#
# Usage: scripts/run-reference-scenarios.sh [results-dir]
set -euo pipefail

out="${1:-results/ci-reference}"
GO="${GO:-go}"
mkdir -p "${out}"
"${GO}" build -o "${out}/scenario" ./cmd/scenario

mapfile -t files < <(grep -l '^reference: true' scenarios/*.yaml | grep -v selftest)
(( ${#files[@]} > 0 )) || { echo "no reference scenarios found" >&2; exit 1; }

pids=()
for f in "${files[@]}"; do
  name="$(basename "${f}" .yaml)"
  "${out}/scenario" run --targets targets/reference.yaml --run ci-reference --out "${out}" "${f}" \
    > "${out}/${name}.txt" 2> "${out}/${name}.log" &
  pids+=("$!")
done

status=0
for i in "${!pids[@]}"; do
  name="$(basename "${files[i]}" .yaml)"
  if wait "${pids[i]}"; then
    echo "PASS ${name}"
  else
    rc=$?
    echo "FAIL ${name} (exit ${rc})"
    status=1
  fi
  cat "${out}/${name}.txt"
done
exit "${status}"
