# Shared helpers for pin.sh, check-mirrors.sh and check-layout.sh.
# Sourced, not executed.

# Git Bash on Windows rewrites arguments such as FETCH_HEAD:api/openapi.yaml
# into Windows path lists (LESSONS E-04); keep those as written. No effect
# elsewhere.
export MSYS2_ARG_CONV_EXCL='FETCH_HEAD'

# source_get FILE KEY: the value of `KEY = value` in a SOURCE file.
source_get() {
  sed -n "s/^$2 *= *//p" "$1" | tr -d '\r'
}

# with_token URL: the URL with LAB_READ_TOKEN injected for https GitHub
# remotes (CI on a private repository); unchanged without a token.
with_token() {
  if [[ -n "${LAB_READ_TOKEN:-}" && "$1" == https://github.com/* ]]; then
    echo "${1/https:\/\//https://x-access-token:${LAB_READ_TOKEN}@}"
  else
    echo "$1"
  fi
}

# fetch_at REPO REF DEST: a shallow fetch of REF (a full SHA, a tag or a
# branch) into a fresh repository at DEST, without checking anything out.
# Prints the resolved commit SHA. Fails with git's own message.
fetch_at() {
  local repo="$1" ref="$2" dest="$3"
  git -C "$dest" init -q
  # A mirror is the committed bytes: no line-ending conversion on checkout,
  # whatever the machine's core.autocrlf says.
  git -C "$dest" config core.autocrlf false
  git -C "$dest" remote add origin "$(with_token "$repo")"
  git -C "$dest" fetch -q --depth 1 origin "$ref"
  git -C "$dest" rev-parse 'FETCH_HEAD^{commit}'
}

# checkout_path DEST PATH: materialise PATH (a file or a directory) of
# FETCH_HEAD in DEST. Fails if PATH does not exist at that commit.
checkout_path() {
  local dest="$1" path="$2"
  if ! git -C "$dest" cat-file -e "FETCH_HEAD:$path" 2>/dev/null; then
    echo "error: $path does not exist at $(git -C "$dest" rev-parse --short 'FETCH_HEAD^{commit}')" >&2
    return 1
  fi
  git -C "$dest" checkout -q FETCH_HEAD -- "$path"
}
