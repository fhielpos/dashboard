FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

RUN apk add --no-cache git

COPY . .
RUN GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "dev") && \
    CGO_ENABLED=0 GOOS=linux go build \
      -ldflags "-X main.Commit=${GIT_COMMIT}" \
      -o /dashboard ./cmd/server

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
