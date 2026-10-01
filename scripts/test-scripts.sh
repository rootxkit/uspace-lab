#!/usr/bin/env bash
# Fixture tests of pin.sh, check-mirrors.sh and check-layout.sh against a
# local repository, offline. Each check is run where it must pass and
# where it must fail (LESSONS E-01), and the passing output is read, not
# only its exit code (E-02).
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
native() { if command -v cygpath >/dev/null; then cygpath -m "$1"; else echo "$1"; fi; }

pass=0
# expect <exit code> <text the output must contain> <description> -- cmd...
expect() {
  local want_rc="$1" want_text="$2" what="$3"; shift 4
  local rc=0
  "$@" >"$tmp/log" 2>&1 || rc=$?
  if [[ "$rc" != "$want_rc" ]] || ! grep -qF -- "$want_text" "$tmp/log"; then
    echo "FAIL $what: exit $rc (want $want_rc), output:"; sed 's/^/    /' "$tmp/log"
    exit 1
  fi
  echo "ok   $what"
  pass=$((pass + 1))
}

# A fixture system repository with the KT-3 layout.
fx="$tmp/uspace-fix"
git init -q "$fx"
git -C "$fx" config user.email fixture@example.invalid
git -C "$fx" config user.name fixture
git -C "$fx" config uploadpack.allowAnySHA1InWant true
git -C "$fx" config core.autocrlf false
mkdir -p "$fx/api" "$fx/schemas/thing/v1" "$fx/migrations/relational" "$fx/migrations/timeseries" "$fx/deploy" "$fx/web"
for d in migrations/relational migrations/timeseries deploy web; do touch "$fx/$d/.gitkeep"; done
printf 'openapi: 3.1.0\ninfo:\n  title: fix\n  version: 0.1.0\npaths:\n  /v1/things: {}\n' > "$fx/api/openapi.yaml"
printf '{"$id": "https://schemas.uspace.ge/thing/v1.json"}\n' > "$fx/schemas/thing/v1/schema.json"
git -C "$fx" add -A && git -C "$fx" commit -q -m layout
c1="$(git -C "$fx" rev-parse HEAD)"
repo="file://$(native "$fx")"

out="$tmp/out"
expect 0 "pinned fix at ${c1:0:12}" "pin.sh fetches and writes the copies" -- \
  "$here/scripts/pin.sh" --out "$out" --repo "$repo" fix "$c1"
grep -qx "commit = $c1" "$out/api/fix/SOURCE" || { echo "FAIL SOURCE does not record $c1"; exit 1; }
cmp -s "$fx/api/openapi.yaml" "$out/api/fix/openapi.yaml" || { echo "FAIL api copy differs"; exit 1; }
cmp -s "$fx/schemas/thing/v1/schema.json" "$out/schemas/fix/thing/v1/schema.json" || { echo "FAIL schemas copy differs"; exit 1; }
echo "ok   SOURCE records the full commit and the copies are byte-identical"; pass=$((pass + 1))

expect 0 "2 mirrors checked and identical, 0 differ" "check-mirrors passes on an intact pin" -- \
  "$here/scripts/check-mirrors.sh" --root "$out"
printf ' ' >> "$out/api/fix/openapi.yaml"
expect 1 "MISMATCH" "check-mirrors fails on one appended byte" -- \
  "$here/scripts/check-mirrors.sh" --root "$out"
expect 0 "no change against the previous copy" "re-pinning the same commit restores the copy and reports no schemas change" -- \
  "$here/scripts/pin.sh" --out "$out" --repo "$repo" fix "$c1"
touch "$out/schemas/fix/extra.json"
expect 1 "Only in" "check-mirrors fails on an extra file in a mirror" -- \
  "$here/scripts/check-mirrors.sh" --root "$out"
rm "$out/schemas/fix/extra.json"

expect 0 "1 systems' layout checked, 0 failed" "check-layout passes on the KT-3 layout" -- \
  "$here/scripts/check-layout.sh" --root "$out"
git -C "$fx" rm -q -r web && git -C "$fx" commit -q -m "drop web"
c2="$(git -C "$fx" rev-parse HEAD)"
"$here/scripts/pin.sh" --out "$out" --repo "$repo" fix "$c2" >/dev/null
expect 1 "web is missing" "check-layout fails when web/ is gone" -- \
  "$here/scripts/check-layout.sh" --root "$out"

expect 1 "does not exist at" "pin.sh refuses a path that is not at the commit" -- \
  "$here/scripts/pin.sh" --out "$tmp/out2" --repo "$repo" --api-path api/missing.yaml fix "$c2"

# Unpinned and unreachable mirrors.
mkdir -p "$tmp/empty/api/fix"
printf 'repo = %s\ncommit =\npath = api/openapi.yaml\nfetched_at =\n' "$repo" > "$tmp/empty/api/fix/SOURCE"
expect 0 "0 mirrors checked and identical, 0 differ, 0 unverified, 1 unpinned" "an unpinned mirror is counted, not checked" -- \
  "$here/scripts/check-mirrors.sh" --root "$tmp/empty"
printf 'repo = file://%s\ncommit = %s\npath = api/openapi.yaml\nfetched_at = x\n' "$(native "$tmp/nowhere")" "$c1" > "$tmp/empty/api/fix/SOURCE"
expect 0 "1 unverified" "an unreachable mirror is unverified, not ok" -- \
  "$here/scripts/check-mirrors.sh" --root "$tmp/empty"
expect 1 "1 unverified" "an unreachable mirror fails with REQUIRE_MIRRORS=1" -- \
  env REQUIRE_MIRRORS=1 "$here/scripts/check-mirrors.sh" --root "$tmp/empty"

echo "$pass script checks passed"
