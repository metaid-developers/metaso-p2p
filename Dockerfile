# Stage 1: Build
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o metaso-p2p ./cmd/metaso-p2p/

# Stage 2: Runtime
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /app/metaso-p2p /usr/local/bin/metaso-p2p
EXPOSE 8080
VOLUME /data
ENV METASO_P2P_PEBBLE_DATA_DIR=/data/pebble
ENTRYPOINT ["metaso-p2p"]
