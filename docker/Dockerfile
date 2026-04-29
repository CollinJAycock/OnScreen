# syntax=docker/dockerfile:1

# ── Stage 1: build frontend ───────────────────────────────────────────────────
FROM node:24-alpine AS frontend
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm install --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ── Stage 2: build Go binaries ───────────────────────────────────────────────
FROM golang:1.26.2-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
COPY --from=frontend /app/web/dist ./internal/webui/dist

ARG VERSION=dev
ARG BUILD_TIME
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    set -e && \
    CGO_ENABLED=0 GOOS=linux go build \
      -ldflags "-X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" \
      -o /bin/server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build \
      -ldflags "-X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" \
      -o /bin/worker ./cmd/worker && \
    CGO_ENABLED=0 GOOS=linux go install github.com/pressly/goose/v3/cmd/goose@v3.24.1 && \
    cp $(go env GOPATH)/bin/goose /bin/goose && \
    test -x /bin/server && test -x /bin/worker && test -x /bin/goose

# ── Stage 3: minimal runtime image ────────────────────────────────────────────
FROM alpine:3.23
RUN apk add --no-cache ffmpeg ca-certificates tzdata chromaprint postgresql17-client \
      tesseract-ocr tesseract-ocr-data-eng tesseract-ocr-data-spa \
      tesseract-ocr-data-fra tesseract-ocr-data-deu tesseract-ocr-data-ita \
      tesseract-ocr-data-por tesseract-ocr-data-jpn tesseract-ocr-data-chi_sim \
      tesseract-ocr-data-chi_tra tesseract-ocr-data-kor tesseract-ocr-data-rus \
      tesseract-ocr-data-ara

# Non-root user
RUN addgroup -S onscreen && adduser -S onscreen -G onscreen
USER onscreen

COPY --from=builder /bin/server  /usr/local/bin/server
COPY --from=builder /bin/worker  /usr/local/bin/worker
COPY --from=builder /bin/goose   /usr/local/bin/goose

# Migrations are bundled; goose is run as a pipeline step, not on startup.
COPY --chown=onscreen:onscreen internal/db/migrations /migrations

EXPOSE 7070 7071

HEALTHCHECK --interval=10s --timeout=3s --start-period=15s \
  CMD wget -qO- http://localhost:7070/health/ready || exit 1

STOPSIGNAL SIGTERM
LABEL com.docker.compose.stop-grace-period="35s"

ENTRYPOINT ["/usr/local/bin/server"]
