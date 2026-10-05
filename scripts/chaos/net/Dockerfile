# The packet filter scripts/chaos/partition.sh runs inside a container's
# network namespace (docker run --net container:<id> --cap-add NET_ADMIN):
# iptables on Alpine, pinned by digest. Built locally, never pushed;
# lib.sh builds it on first use.
FROM alpine:3@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
RUN apk add --no-cache iptables
ENTRYPOINT ["/bin/sh", "-c"]
