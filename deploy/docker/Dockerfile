# syntax=docker/dockerfile:1.7

ARG VERSION=dev

FROM alpine:3 AS certs
RUN apk add --no-cache ca-certificates

# Stage 1: build the SPA. Output lands at /out/webdist and is copied
# into the Go builder's source tree so //go:embed picks up the real
# bundle instead of the committed placeholder.html.
FROM node:22-alpine AS spa-builder
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --silent
COPY web/ ./
# vite.config.ts sets build.outDir to ../internal/admin/webdist, which
# resolves to /internal/admin/webdist from this WORKDIR. We then move
# the result to /out/webdist for an unambiguous COPY in the next stage.
RUN npm run build && mkdir -p /out && mv /internal/admin/webdist /out/webdist

FROM golang:1.25-alpine AS builder
ARG VERSION
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Overlay the freshly-built SPA bundle on top of the committed
# placeholder.html so go:embed sees the real assets.
COPY --from=spa-builder /out/webdist/ /src/internal/admin/webdist/
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags "-s -w -X github.com/andyjmorgan/sluice-gateway/internal/version.Version=${VERSION}" \
        -o /out/gateway \
        ./cmd/gateway

FROM scratch

ARG VERSION

LABEL org.opencontainers.image.title="sluice-gateway"

LABEL org.opencontainers.image.description="Slim observable AI provider gateway."

LABEL org.opencontainers.image.source="https://github.com/andyjmorgan/sluice-gateway"

LABEL org.opencontainers.image.version="${VERSION}"

COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/gateway /gateway

EXPOSE 8585 8081
USER 65532:65532
ENTRYPOINT ["/gateway"]
CMD []
