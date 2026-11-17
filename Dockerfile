# Release image. Configuration comes from environment variables at runtime; no .env is baked in.
FROM golang:1.25-alpine AS builder
ENV CGO_ENABLED=0
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags "-s -w" -o main .

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
WORKDIR /www
COPY --from=builder /build/main /www/
COPY --from=builder /build/public/ /www/public/
# The app writes logs to storage/logs; it must be writable by the non-root user.
RUN mkdir -p /www/storage/logs && chown -R app:app /www/storage
# Structured logs go to stdout only (`docker logs`): the log files have no rotation yet
# (RF-023), and request/response bodies are not logged outside local development.
ENV LOG_PRINT=true \
    LOG_FILES=false \
    LOG_HTTP_BODIES=false
USER app
EXPOSE 3000
ENTRYPOINT ["/www/main"]
