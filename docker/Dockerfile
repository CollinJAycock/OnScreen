# syntax=docker/dockerfile:1

# ── Stage 1: build frontend ───────────────────────────────────────────────────
FROM node:22-alpine AS frontend
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ── Stage 2: build Go binary ──────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder
WORKDIR /app

# Download deps first (cached unless go.mod/go.sum change)
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .
# Copy built frontend from stage 1
COPY --from=frontend /app/web/dist ./web/dist

# Build server and worker
ARG VERSION=dev
ARG BUILD_TIME
RUN CGO_ENABLED=0 GOOS=linux go build \
      -ldflags "-X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" \
      -o /bin/server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build \
      -ldflags "-X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" \
      -o /bin/worker ./cmd/worker

# ── Stage 3: minimal runtime image ────────────────────────────────────────────
FROM alpine:3.21
RUN apk add --no-cache ffmpeg ca-certificates tzdata

# Non-root user
RUN addgroup -S onscreen && adduser -S onscreen -G onscreen
USER onscreen

COPY --from=builder /bin/server  /usr/local/bin/server
COPY --from=builder /bin/worker  /usr/local/bin/worker

# Migrations are bundled; goose is run as a pipeline step, not on startup.
# To run migrations: docker run --rm onscreen server migrate
COPY --chown=onscreen:onscreen internal/db/migrations /migrations

EXPOSE 7070 7071

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s \
  CMD wget -qO- http://localhost:7070/health/live || exit 1

ENTRYPOINT ["/usr/local/bin/server"]
