#!/usr/bin/env bash
# make chaos, second step (docs/RUNBOOKS/chaos.md): seed the stack that
# deploy/demo-up.sh started for the background scenario, through the
# systems' public APIs (cmd/demo-seed, docs/RUNBOOKS/demo.md §2):
#
#   1. the receivers, then rid-ingest restarted (it binds 127.0.0.1 when
#      it starts with no receiver keys, demo.md "rid-ingest");
#   2. the registry, the U-space airspace moved off the area (the
#      background needs an authorised intent, demo.md "Where the U-space
#      airspace goes"), the USSP's operators, the ANSP's supervisor and
#      fresh console sessions (targets/local-demo.yaml);
#   3. the background's zone.
#
#   scripts/chaos/prepare.sh [--fresh]
#
# --fresh is for a stack whose volumes were just created (make chaos runs
# demo-down first): it removes the seed's state file, whose rows (the
# receivers, the airspace, the accounts) the new databases do not hold.
# Without it the seed trusts that file and answers, for example, "U-space
# airspace LABUSP1 answered 404" (seen by WP-L9).
#
# Environment: GO (default go), DEMO_GEOID (the geoid grid the targets
# file names, default deploy/local-demo/ground/egm2008-2_5.pgm),
# CHAOS_USPACE_NORTH_M (default 60000), and lib.sh's.
chaos_domain=prepare
# shellcheck source=scripts/chaos/lib.sh
. "$(dirname "$0")/lib.sh"
cd "$chaos_repo"

GO="${GO:-go}"
geoid="${DEMO_GEOID:-deploy/local-demo/ground/egm2008-2_5.pgm}"
north="${CHAOS_USPACE_NORTH_M:-60000}"
bg="scripts/chaos/background.yaml"
bin="deploy/local-demo/bin"
fresh=0
case "${1:-}" in
  --fresh) fresh=1 ;;
  "") ;;
  *) usage "prepare.sh [--fresh]" ;;
esac
[ -f "$geoid" ] || die "no geoid grid at $geoid (DEMO_GEOID)"
[ -f sim/sitl.env ] || { cp sim/sitl.env.example sim/sitl.env; say "created sim/sitl.env from sim/sitl.env.example"; }
mkdir -p "$bin"
"$GO" build -o "$bin/demo-seed" ./cmd/demo-seed
seed() { "$bin/demo-seed" --geoid "$geoid" "$@" "$bg"; }

if [ "$fresh" = 1 ]; then
  state="deploy/local-demo/seed-state.json"
  if [ -e "$state" ]; then rm -f "$state"; say "removed $state (a fresh stack holds none of its rows)"; fi
fi

seed --steps receivers
id="$(cid authority-rid-ingest)"
docker restart "$id" >/dev/null
[ "$(state_of "$id")" = "running" ] || die "authority-rid-ingest is $(state_of "$id") after its restart"
say "restarted authority-rid-ingest after registering the receivers"
seed --steps registry,uspace,ussp,ansp,sessions --uspace-north-m "$north"
seed --steps zones --zones chaos-background
say "seeded for $bg: targets/local-demo.yaml written"
