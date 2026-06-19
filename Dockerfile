FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /dashboard ./cmd/server

# ---

FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /dashboard /app/dashboard
COPY config.yaml /app/config.yaml

RUN mkdir -p /data

ENV CONFIG_PATH=/app/config.yaml

EXPOSE 8080

ENTRYPOINT ["/app/dashboard"]
