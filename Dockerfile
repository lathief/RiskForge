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
COPY --from=builder /build/resources/ /www/resources/
USER app
EXPOSE 3000
ENTRYPOINT ["/www/main"]
