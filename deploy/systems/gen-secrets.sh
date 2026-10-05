#!/usr/bin/env bash
# Generates the lab demo's keys, passwords and TLS material into
# DEMO_STATE_DIR (deploy/local-demo/, git-ignored), as uspace-deploy's
# scripts/gen-secrets.sh does for the droplet, less what the lab does not
# run (NATS users, mTLS CA, web secrets):
#
#   deploy/systems/gen-secrets.sh <demo.env>
#
# It never overwrites a file that exists, so a second run changes nothing,
# and it never prints a secret, only file names. A failure is never
# silent: it names the step and repeats openssl's own diagnostics on
# stderr, and the exit status is non-zero.
#
#   ca/ca.pem, ca/ca.key      the lab CA (RSA 3072, 30 days), trusted by
#                             every system (SSL_CERT_FILE) and the runner
#   tls/cert.pem, key.pem     the Caddy certificate for the five hosts
#   authority/                token-1.pem, publication-1.pem, pii.key,
#                             registry-hash.key, admin.pw
#   cisp/                     signing.pem, session.pem, secrets.key
#   ussp/                     issuer.pem, mfa.key
#   ansp/                     session.pem, secrets.key, delivery.pem,
#                             admin.pw
#   passwords.env             database passwords and the CISP kid, the
#                             second --env-file of the compose project
set -euo pipefail

die() { echo "gen-secrets: $*" >&2; exit 1; }
trap 'echo "gen-secrets: failed (exit $?) at line $LINENO: $BASH_COMMAND" >&2' ERR

# Git for Windows turns its path conversion off when MSYS_NO_PATHCONV is
# set to any value, "0" included, and demo-up.sh exports it. openssl is
# a native Windows program under Git Bash: handed /c/... paths
# unconverted it can open none of them. This script relies on the
# conversion (MSYS2_ARG_CONV_EXCL below narrows it), so it drops the
# variable. Elsewhere it means nothing.
unset MSYS_NO_PATHCONV

env_file="${1:?usage: gen-secrets.sh <demo.env>}"
[ -r "$env_file" ] || die "cannot read $env_file"
here="$(cd "$(dirname "$0")/.." && pwd)" # deploy/
val() { sed -n "s/^$1=//p" "$env_file" | tail -n 1; }

state="$(val DEMO_STATE_DIR)"
state="${state:-./local-demo}"
# Absolute: /..., or C:/... as a Windows program writes it.
case "$state" in /* | [A-Za-z]:/*) ;; *) state="$here/$state" ;; esac
command -v openssl >/dev/null || die "openssl not found"

# openssl with its diagnostics kept: on a failure they are printed with
# the subcommand instead of being thrown away with the exit status.
# Standard output passes through (rand writes to it); inside a pipeline
# the exit below ends the pipeline, and pipefail the script.
ossl() {
  local log rc=0
  log="$(mktemp)"
  openssl "$@" 2>"$log" || rc=$?
  if [ "$rc" -ne 0 ]; then
    {
      echo "gen-secrets: openssl $1 failed (exit $rc):"
      sed 's/^/  /' "$log"
    } >&2
    rm -f "$log"
    exit 1
  fi
  rm -f "$log"
}

umask 077
created=0
mkdir -p "$state"/{ca,tls,authority,cisp,ussp,ansp,clients}

note() { echo "gen-secrets: wrote ${1#"$state"/}"; created=$((created + 1)); }
# Each secret is written to <file>.tmp and moved into place only when it
# is not empty, so a failed run leaves no file a later run would keep.
place() { [ -s "$1.tmp" ] || die "nothing was written to ${1#"$state"/}"; mv "$1.tmp" "$1"; note "$1"; }
rsa_key() { [ -e "$1" ] && return 0; ossl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 -out "$1.tmp"; place "$1"; }
b64_key() { [ -e "$1" ] && return 0; ossl rand -base64 32 | tr -d '\r' > "$1.tmp"; place "$1"; }
hex_file() { [ -e "$1" ] && return 0; ossl rand -hex 24 | tr -d '\r' > "$1.tmp"; place "$1"; }

# ---- the lab CA and the Caddy certificate ---------------------------------
# Git Bash rewrites an argument that starts with /CN= into a Windows path;
# exclude that prefix only (file paths must still be converted).
export MSYS2_ARG_CONV_EXCL="/CN="
if [ ! -e "$state/ca/ca.pem" ]; then
  ossl req -x509 -newkey rsa:3072 -nodes -days 30 -sha256 \
    -keyout "$state/ca/ca.key" -out "$state/ca/ca.pem" \
    -subj "/CN=uspace-lab demo CA/O=uspace-lab" \
    -addext "basicConstraints=critical,CA:TRUE" \
    -addext "keyUsage=critical,keyCertSign,cRLSign"
  note "$state/ca/ca.pem"
fi
if [ ! -e "$state/tls/cert.pem" ]; then
  sans=""
  for h in AUTHORITY_HOST CISP_HOST USSP_HOST ANSP_HOST LAB_HOST; do
    v="$(val "$h")"
    [ -n "$v" ] || die "$h is not set in $env_file"
    sans="${sans:+$sans,}DNS:$v"
  done
  ossl req -newkey rsa:3072 -nodes -keyout "$state/tls/key.pem" -out "$state/tls/req.csr" \
    -subj "/CN=$(val LAB_HOST)/O=uspace-lab"
  printf 'subjectAltName=%s\nbasicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n' "$sans" > "$state/tls/ext.cnf"
  ossl x509 -req -in "$state/tls/req.csr" -CA "$state/ca/ca.pem" -CAkey "$state/ca/ca.key" \
    -CAcreateserial -days 30 -sha256 -extfile "$state/tls/ext.cnf" -out "$state/tls/cert.pem"
  rm -f "$state/tls/req.csr" "$state/tls/ext.cnf"
  note "$state/tls/cert.pem"
fi
# The containers run as other users and read these through bind mounts.
chmod 0644 "$state/ca/ca.pem" "$state/tls/cert.pem"

# ---- the systems' keys ------------------------------------------------------
rsa_key "$state/authority/token-1.pem"
rsa_key "$state/authority/publication-1.pem"
b64_key "$state/authority/pii.key"
b64_key "$state/authority/registry-hash.key"
hex_file "$state/authority/admin.pw"
rsa_key "$state/cisp/signing.pem"
rsa_key "$state/cisp/session.pem"
b64_key "$state/cisp/secrets.key"
rsa_key "$state/ussp/issuer.pem"
b64_key "$state/ussp/mfa.key"
rsa_key "$state/ansp/session.pem"
b64_key "$state/ansp/secrets.key"
rsa_key "$state/ansp/delivery.pem"
hex_file "$state/ansp/admin.pw"

# ---- passwords.env ------------------------------------------------------------
if [ ! -e "$state/passwords.env" ]; then
  {
    echo "# Generated by deploy/systems/gen-secrets.sh. Secret: never copy it into git."
    for n in AUTHORITY_PG_PASSWORD CISP_PG_PASSWORD CISP_PG_API_PASSWORD CISP_PG_DELIVER_PASSWORD \
      USSP_PG_PASSWORD USSP_PG_API_PASSWORD USSP_PG_TSDB_PASSWORD ANSP_PG_PASSWORD; do
      # An assignment, not "echo $(...)": echo would hide the failure.
      pw="$(ossl rand -hex 24 | tr -d '\r')"
      [ -n "$pw" ] || die "no password was generated for $n"
      echo "$n=$pw"
    done
    echo "CISP_SIGNING_KID=cisp-lab-$(date -u +%Y%m%d)-1"
  } > "$state/passwords.env.tmp"
  mv "$state/passwords.env.tmp" "$state/passwords.env"
  note "$state/passwords.env"
fi

echo "gen-secrets: done, $created file(s) written, everything else kept"
