#!/usr/bin/env bash
# Fetch every system's api/openapi.yaml at the commit PINS names into
# DIR/<system>.yaml and check its SHA-256 against the pin, then classify
# it (cmd/conformance contract-check: every operation's credential must
# be known, fail closed). Online; a fetch failure is a failure, never a
# skip.
#
#   conformance/national/fetch-contracts.sh DIR
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
lab="$(cd "$here/../.." && pwd)"
dir="${1:?usage: fetch-contracts.sh DIR}"
mkdir -p "$dir"
fails=0
while read -r system commit sum; do
  case "$system" in ''|'#'*) continue ;; esac
  out="$dir/$system.yaml"
  url="https://raw.githubusercontent.com/rootxkit/uspace-$system/$commit/api/openapi.yaml"
  if ! curl -fsS --max-time 60 --retry 3 --retry-delay 2 -o "$out" "$url"; then
    echo "FAIL $system: cannot fetch $url"
    fails=$((fails + 1))
    continue
  fi
  got="$(sha256sum "$out" | cut -d' ' -f1)"
  if [ "$got" != "$sum" ]; then
    echo "FAIL $system: $url is sha256 $got, PINS says $sum"
    fails=$((fails + 1))
    continue
  fi
  if line="$(cd "$lab" && "${GO:-go}" run ./cmd/conformance contract-check --system "$system" --openapi "$out" 2>&1)"; then
    echo "ok   $line"
  else
    echo "FAIL $system: $line"
    fails=$((fails + 1))
  fi
done < "$here/contracts/PINS"
if [ "$fails" -ne 0 ]; then
  echo "contracts: $fails failed"
  exit 1
fi
echo "contracts: every pinned system contract fetched, matched and classified"
