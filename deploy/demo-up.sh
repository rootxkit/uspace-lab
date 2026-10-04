#!/usr/bin/env bash
# make demo, first half (docs/RUNBOOKS/demo.md): the DSS, the lab issuer
# and the four systems as one compose project, then the proofs that they
# are up and talk to each other. The seed (deploy/systems/seed.sh) and
# the scenarios run after it.
#
#   deploy/demo-up.sh
#
# Steps, each checked before the next (LESSONS E-02):
#   1. deploy/demo.env from demo.env.example when there is none;
#   2. keys, passwords and the lab CA (systems/gen-secrets.sh);
#   3. the ANSP image, built from ANSP_SOURCE_COMMIT when it is not
#      present (the ANSP publishes none yet; every other image is pulled
#      by digest);
#   4. the DSS and the issuer, healthy, and the issuer's client secrets
#      written one per file for the systems (clients/<id>.secret);
#   5. the systems profile, every container with a health check healthy
#      and every one-shot migration exited 0;
#   6. the proofs: each system's JWKS through the lab Caddy over TLS from
#      the lab CA, and a lab issuer token accepted by the DSS;
#   7. one docker stats sample of the whole project (L-Q1).
#
# Environment: COMPOSE_PROJECT_NAME (default uspace-demo), ANSP_SOURCE_DIR
# (a checkout of uspace-ansp to build from; default: the GitHub
# repository at ANSP_SOURCE_COMMIT).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
cd "$here"
export MSYS_NO_PATHCONV=1

say() { echo "demo-up: $*"; }
die() { echo "demo-up: $*" >&2; exit 1; }
command -v docker >/dev/null 2>&1 || die "docker is required"

if [ ! -f demo.env ]; then
  cp demo.env.example demo.env
  say "created deploy/demo.env from deploy/demo.env.example"
fi
val() { sed -n "s/^$1=//p" demo.env | tail -n 1; }

MSYS_NO_PATHCONV=0 systems/gen-secrets.sh demo.env
state="$(val DEMO_STATE_DIR)"; state="${state:-./local-demo}"
ground="$(val DEMO_GROUND_DIR)"; ground="${ground:-./local-demo/ground}"
[ -f "$ground/egm2008-2_5.pgm" ] || die "no $ground/egm2008-2_5.pgm: put GeographicLib's egm2008-2_5 grid there (docs/RUNBOOKS/demo.md, 'Ground'), or set DEMO_GROUND_DIR"
issuer_state="$(val LAB_STATE_DIR)"; issuer_state="${issuer_state:-./local-demo/issuer}"
mkdir -p "$issuer_state/public"

LAB_UID="$(id -u)"
LAB_GID="$(id -g)"
export LAB_UID LAB_GID

project="${COMPOSE_PROJECT_NAME:-uspace-demo}"
dc() {
  docker compose -p "$project" -f compose.yaml -f systems/compose.yaml \
    --env-file demo.env --env-file "$state/passwords.env" \
    --profile dss --profile issuer --profile systems "$@"
}

# ---- 3. the ANSP image -------------------------------------------------------
ansp_image="$(val ANSP_GO_IMAGE)"
case "$ansp_image" in
  *@sha256:*)
    # Published (the ANSP's CI pushes images since its 85436de): pulled
    # by digest like every other system.
    docker image inspect "$ansp_image" >/dev/null 2>&1 || docker pull -q "$ansp_image" >/dev/null
    say "ANSP image $ansp_image (pulled by digest)" ;;
  *)
    if ! docker image inspect "$ansp_image" >/dev/null 2>&1; then
      commit="$(val ANSP_SOURCE_COMMIT)"
      src="${ANSP_SOURCE_DIR:-https://github.com/rootxkit/uspace-ansp.git#$commit}"
      say "building $ansp_image from $src"
      docker build -q -f deploy/Dockerfile --build-arg VERSION="${commit:0:7}" -t "$ansp_image" "$src" >/dev/null
    fi
    say "ANSP image $ansp_image ($(docker image inspect -f '{{.Id}}' "$ansp_image"), built locally)" ;;
esac

# ---- 4. the DSS and the issuer ------------------------------------------------
start="$(date +%s)"
dc up -d --build --wait --wait-timeout 600 dss-crdb dss lab-issuer
say "DSS and issuer healthy after $(( $(date +%s) - start ))s"
secrets="$issuer_state/client-secrets.json"
[ -s "$secrets" ] || die "the issuer wrote no $secrets"
# One file per client, as the systems' *_SECRET_FILE variables read them.
py="$(command -v python3 || command -v python || true)"
[ -n "$py" ] || die "python3 is required"
"$py" - "$secrets" "$state/clients" <<'PY' || die "could not split $secrets"
import json, os, sys
src, out = sys.argv[1], sys.argv[2]
with open(src) as f:
    secrets = json.load(f)
os.makedirs(out, exist_ok=True)
for client, secret in secrets.items():
    path = os.path.join(out, client + ".secret")
    with open(path, "w", newline="\n") as f:
        f.write(secret + "\n")
print("demo-up: client secrets for", ", ".join(sorted(secrets)))
PY

# ---- 5. the systems -----------------------------------------------------------
# The authority first: its images have no probe subcommand (no compose
# health check), and the CISP's api refuses to start until the
# authority's JWKS answers, so the wait is on that JWKS through Caddy.
start="$(date +%s)"
authority_svcs="$(dc config --services | grep '^authority-' | tr '
' ' ')"
# shellcheck disable=SC2086 # one argument per service
dc up -d --wait --wait-timeout 600 caddy $authority_svcs
ah="$(val AUTHORITY_HOST)"; port="$(val DEMO_HTTPS_PORT)"; port="${port:-443}"
for _ in $(seq 1 60); do
  code="$(MSYS_NO_PATHCONV=0 curl -s -o /dev/null -w '%{http_code}' --max-time 5 --ssl-no-revoke     --cacert "$state/ca/ca.pem" --resolve "$ah:$port:127.0.0.1" "https://$ah:$port/.well-known/jwks.json" || true)"
  [ "$code" = "200" ] && break
  sleep 2
done
[ "$code" = "200" ] || die "the authority's JWKS did not answer 200 through Caddy (last: $code): dc logs authority-api"
say "the authority's JWKS answers after $(( $(date +%s) - start ))s"
dc up -d --wait --wait-timeout 900
say "every service healthy after $(( $(date +%s) - start ))s"
for m in authority-migrate cisp-migrate ussp-migrate ussp-migrate-timeseries ansp-migrate; do
  code="$(docker inspect -f '{{.State.ExitCode}}' "$(dc ps -a -q "$m")")"
  [ "$code" = "0" ] || die "$m exited $code: dc logs $m"
done
say "every migration exited 0"

# ---- 6. the proofs --------------------------------------------------------------
systems/prove.sh demo.env

# ---- 7. footprint ------------------------------------------------------------------
say "memory and CPU per container (docker stats, one sample):"
# shellcheck disable=SC2046 # one argument per container id
docker stats --no-stream --format 'table {{.Name}}\t{{.MemUsage}}\t{{.CPUPerc}}' $(dc ps -q)
