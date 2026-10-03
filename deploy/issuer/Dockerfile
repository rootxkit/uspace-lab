# The lab issuer image (docs/WORKPACKAGES/WP-L2.md). Built from the
# repository root (deploy/compose.yaml sets the context). Both base
# images are pinned by digest; bump the tag and the digest together.
FROM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-mod=mod GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download
COPY internal/ internal/
COPY cmd/ cmd/
RUN go build -trimpath -ldflags="-s -w" -o /out/lab-issuer ./cmd/lab-issuer

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/lab-issuer /lab-issuer
COPY deploy/issuer/clients.yaml /etc/lab-issuer/clients.yaml
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/lab-issuer"]
CMD ["serve"]
