#!/usr/bin/env bash
# Validate every example under schemas/common/ in both directions
# (LESSONS E-01): each examples/*.json must validate against its schema,
# each examples/invalid/*.json must fail, and a schema with fewer than two
# valid or no invalid examples fails the script. Offline: every $ref
# resolves among the loaded schemas by $id, nothing is fetched.
#   scripts/validate-examples.sh [-v] [DIR]
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
cd "$here"
exec "${GO:-go}" run ./scripts/contracts examples "$@"
