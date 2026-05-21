# Production Dockerfile for satellites-server.
#
# Built and pushed by .github/workflows/image-push.yml on every push to
# main, tagged registry.fly.io/satellites-pprod:latest. The actual
# Fly.io deploy is owned by the satellites-infra repo
# (fly/deploy.sh), which pulls this image.
#
# Local development uses scripts/Dockerfile.dev (Postgres via
# scripts/docker-compose.dev.yml + satellites-server in --dev mode).

FROM golang:1.25-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Version stamping. The image-push workflow extracts these from .version
# (satellites-server.version + satellites-server.build) and supplies
# COMMIT as ${{ github.sha }}. Defaults keep ad-hoc local builds working.
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD=unknown

RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w \
      -X github.com/bobmcallan/satellites/internal/verb.Version=${VERSION} \
      -X github.com/bobmcallan/satellites/internal/verb.Commit=${COMMIT} \
      -X github.com/bobmcallan/satellites/internal/verb.BuildTime=${BUILD}" \
    -o /out/satellites-server ./cmd/satellites-server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/satellites-server /usr/local/bin/satellites-server
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/satellites-server"]
