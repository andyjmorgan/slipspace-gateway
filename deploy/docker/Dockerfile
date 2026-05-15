# syntax=docker/dockerfile:1.7

ARG VERSION=dev

FROM alpine:3 AS certs
RUN apk add --no-cache ca-certificates

FROM golang:1.25-alpine AS builder
ARG VERSION
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
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

EXPOSE 8585
USER 65532:65532
ENTRYPOINT ["/gateway"]
CMD []
