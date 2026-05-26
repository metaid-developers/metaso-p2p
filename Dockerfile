# Stage 1: Build
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o meta-socket ./cmd/meta-socket/

# Stage 2: Runtime
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /app/meta-socket /usr/local/bin/meta-socket
EXPOSE 8080
VOLUME /data
ENV META_SOCKET_PEBBLE_DATA_DIR=/data/pebble
ENTRYPOINT ["meta-socket"]
