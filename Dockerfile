# syntax=docker/dockerfile:1
# Production image: static binary (templates, assets and migrations are embedded), non-root.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/desakita ./cmd/desakita

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
 && addgroup -S -g 10001 app && adduser -S -D -H -u 10001 -G app app \
 && mkdir -p /data/uploads /data/mail /data/tiles && chown app:app /data/uploads /data/mail /data/tiles
COPY --from=build /out/desakita /usr/local/bin/desakita
USER 10001:10001
ENV APP_ENV=production \
    HTTP_ADDR=:8080 \
    UPLOAD_DIR=/data/uploads \
    MAIL_DIR=/data/mail \
    MAP_TILE_DIR=/data/tiles \
    LOG_LEVEL=info
VOLUME ["/data/uploads", "/data/mail", "/data/tiles"]
EXPOSE 8080
# Liveness only; orchestrators should use /readyz for traffic routing.
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
# Migrations take a PostgreSQL advisory lock, so concurrent starts are safe.
CMD ["/bin/sh", "-c", "desakita migrate up && exec desakita serve"]
