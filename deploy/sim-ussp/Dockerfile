# The peer USSP image (cmd/sim-ussp, docs/WORKPACKAGES/WP-L5.md), run in
# the lab stack's `sim` profile against the DSS. Built from the
# repository root (deploy/compose.yaml sets the context). Both base
# images are pinned by digest, the same as deploy/issuer/Dockerfile's;
# bump them together.
FROM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-mod=mod GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download
COPY internal/ internal/
COPY cmd/ cmd/
RUN go build -trimpath -ldflags="-s -w" -o /out/sim-ussp ./cmd/sim-ussp

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/sim-ussp /sim-ussp
USER 65532:65532
EXPOSE 8093
ENTRYPOINT ["/sim-ussp"]
