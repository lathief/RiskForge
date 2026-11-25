# Release image. Configuration comes from environment variables at runtime; no .env is baked in.
# Base images are pinned by digest; refresh them deliberately (docker pull + inspect RepoDigests).
FROM golang:1.26-alpine@sha256:ecb67f987aa1a57794c91d19ade7ae0209f2f0f5496aaef9e6ae85ddf92d02ab AS builder
ENV CGO_ENABLED=0
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags "-s -w" -o main .

FROM alpine:3.22@sha256:49340b346798875b5592e5d420241f2f9cfba5f47e62ebb1446a1b34734e898f
RUN addgroup -S app && adduser -S -G app app
WORKDIR /www
COPY --from=builder /build/main /www/
COPY --from=builder /build/public/ /www/public/
# Goravel's own framework log (goravel-YYYY-MM-DD.log, daily) is written to storage/logs, so
# the directory must be writable by the non-root user.
RUN mkdir -p /www/storage/logs && chown -R app:app /www/storage
# RiskForge's structured logs go to stdout only (`docker logs`): the activity/error/lifetime/
# query files have no rotation yet (RF-023), and request/response bodies are not logged.
ENV LOG_PRINT=true \
    LOG_FILES=false \
    LOG_HTTP_BODIES=false
USER app
EXPOSE 3000
ENTRYPOINT ["/www/main"]
