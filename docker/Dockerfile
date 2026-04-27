FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Version/build/commit stamping. Accepts --build-arg overrides; otherwise
# derives from .version + `date -u` + `git rev-parse --short HEAD` at build
# time. Matches the v3 reference pattern (one -ldflags -X per stamped field).
ARG VERSION
ARG BUILD
ARG GIT_COMMIT

RUN apk add --no-cache git && \
    VERSION="${VERSION:-$(awk '/^\[satellites\]/{f=1;next} /^\[/{f=0} f && /^version/{gsub(/version\s*=\s*/,""); gsub(/ /,""); print; exit}' .version 2>/dev/null || echo dev)}" && \
    BUILD="${BUILD:-$(date -u +"%Y-%m-%d-%H-%M-%S")}" && \
    GIT_COMMIT="${GIT_COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}" && \
    go build -ldflags="-s -w \
        -X 'github.com/bobmcallan/satellites/internal/config.Version=${VERSION}' \
        -X 'github.com/bobmcallan/satellites/internal/config.Build=${BUILD}' \
        -X 'github.com/bobmcallan/satellites/internal/config.GitCommit=${GIT_COMMIT}'" \
        -o /bin/satellites ./cmd/satellites && \
    go build -ldflags="-s -w \
        -X 'github.com/bobmcallan/satellites/internal/config.Version=${VERSION}' \
        -X 'github.com/bobmcallan/satellites/internal/config.Build=${BUILD}' \
        -X 'github.com/bobmcallan/satellites/internal/config.GitCommit=${GIT_COMMIT}'" \
        -o /bin/satellites-agent ./cmd/satellites-agent

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata curl bash git

WORKDIR /app

COPY --from=builder /bin/satellites /app/satellites
COPY --from=builder /bin/satellites-agent /app/satellites-agent
COPY docker/entrypoint-local.sh /app/docker/
RUN chmod +x /app/docker/entrypoint-local.sh

EXPOSE 8080

ENTRYPOINT ["/app/satellites"]
