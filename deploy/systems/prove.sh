#!/usr/bin/env bash
# The proofs of make demo (deploy/demo-up.sh step 6), from outside the
# stack through the published lab Caddy, with the lab CA and the hosts
# resolved to 127.0.0.1 by curl itself (nothing on the machine changes):
#
#   deploy/systems/prove.sh <demo.env>
#
#   - without the lab CA, the TLS handshake is refused (so the next
#     answers are known to come over the lab's TLS, not around it);
#   - each system's and the lab issuer's JWKS answers 200 with at least
#     one key: every verifier in the stack fetches these;
#   - a lab-01 token for the lab host is refused by the DSS without it
#     (401) and accepted with it (200).
set -euo pipefail
# demo-up.sh sets it for docker; curl needs its file paths converted.
unset MSYS_NO_PATHCONV

env_file="${1:?usage: prove.sh <demo.env>}"
here="$(cd "$(dirname "$0")/.." && pwd)"
val() { sed -n "s/^$1=//p" "$env_file" | tail -n 1; }
state="$(val DEMO_STATE_DIR)"; state="${state:-./local-demo}"
case "$state" in /*) ;; *) state="$here/$state" ;; esac
port="$(val DEMO_HTTPS_PORT)"; port="${port:-443}"
ca="$state/ca/ca.pem"
lab="$(val LAB_HOST)"
fail=0

lc() { # lab curl: <host> <curl args...>
  local h="$1"; shift
  # --ssl-no-revoke: Windows curl (Schannel) asks for a revocation list the
  # lab CA does not publish; other TLS backends ignore the flag.
  curl -sS --max-time 15 --ssl-no-revoke --cacert "$ca" --resolve "$h:$port:127.0.0.1" "$@"
}
ok() { echo "ok     $*"; }
bad() { echo "FAIL   $*"; fail=1; }

h="$(val AUTHORITY_HOST)"
if curl -sS --max-time 10 --resolve "$h:$port:127.0.0.1" -o /dev/null "https://$h:$port/.well-known/jwks.json" 2>/dev/null; then
  bad "https://$h answered without the lab CA: the proofs below would not prove the lab's TLS"
else
  ok "https://$h refused without the lab CA (want refused)"
fi

for hv in AUTHORITY_HOST CISP_HOST USSP_HOST ANSP_HOST LAB_HOST; do
  h="$(val "$hv")"
  out="$(lc "$h" -w '\n%{http_code}' "https://$h:$port/.well-known/jwks.json" || true)"
  code="$(printf '%s' "$out" | tail -n 1)"
  body="$(printf '%s' "$out" | sed '$d')"
  if [ "$code" = "200" ] && printf '%s' "$body" | grep -q '"kid"'; then
    ok "GET https://$h/.well-known/jwks.json -> $code with $(printf '%s' "$body" | grep -o '"kid"' | wc -l | tr -d ' ') key(s)"
  else
    bad "GET https://$h/.well-known/jwks.json -> ${code:-no answer}: $(printf '%s' "$body" | head -c 200)"
  fi
done

secret="$(tr -d '\r\n' < "$state/clients/lab-01.secret")"
tok="$(lc "$lab" -X POST "https://$lab:$port/oauth/token" \
  --data-urlencode grant_type=client_credentials --data-urlencode client_id=lab-01 \
  --data-urlencode "client_secret=$secret" --data-urlencode scope=utm.strategic_coordination \
  --data-urlencode "audience=$lab" | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')"
[ -n "$tok" ] || bad "no lab-01 token from https://$lab/oauth/token"
q='{"area_of_interest":{"volume":{"outline_circle":{"center":{"lat":'"$(val DSS_PROBE_LAT_DEG)"',"lng":'"$(val DSS_PROBE_LNG_DEG)"'},"radius":{"value":500,"units":"M"}},"altitude_lower":{"value":0,"reference":"W84","units":"M"},"altitude_upper":{"value":1500,"reference":"W84","units":"M"}}}}'
c0="$(lc "$lab" -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d "$q" "https://$lab:$port/dss/v1/operational_intent_references/query" || true)"
[ "$c0" = "401" ] && ok "DSS query through https://$lab without a token -> $c0 (want 401)" || bad "DSS query without a token -> $c0 (want 401)"
c1="$(lc "$lab" -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -H "Authorization: Bearer $tok" -d "$q" "https://$lab:$port/dss/v1/operational_intent_references/query" || true)"
[ "$c1" = "200" ] && ok "DSS query through https://$lab as lab-01 -> $c1 (want 200)" || bad "DSS query as lab-01 -> $c1 (want 200)"
unset tok secret

[ "$fail" = 0 ] || { echo "prove: a proof failed" >&2; exit 1; }
echo "prove: all proofs passed"
