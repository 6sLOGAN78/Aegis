# syntax=docker/dockerfile:1
FROM golang:alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/gateway ./cmd/gateway
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/demo-issuer ./cmd/demo-issuer
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/orders ./cmd/services/orders
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/payments ./cmd/services/payments
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/admin ./cmd/services/admin

# Target: gateway
FROM alpine:3.21 AS gateway
WORKDIR /app
COPY --from=builder /bin/gateway /usr/local/bin/gateway
COPY policies ./policies
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/gateway"]

# Target: demo-issuer
FROM alpine:3.21 AS demo-issuer
WORKDIR /app
COPY --from=builder /bin/demo-issuer /usr/local/bin/demo-issuer
EXPOSE 8085
ENTRYPOINT ["/usr/local/bin/demo-issuer"]

# Target: orders
FROM alpine:3.21 AS orders
WORKDIR /app
COPY --from=builder /bin/orders /usr/local/bin/orders
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/orders"]

# Target: payments
FROM alpine:3.21 AS payments
WORKDIR /app
COPY --from=builder /bin/payments /usr/local/bin/payments
EXPOSE 8082
ENTRYPOINT ["/usr/local/bin/payments"]

# Target: admin
FROM alpine:3.21 AS admin
WORKDIR /app
COPY --from=builder /bin/admin /usr/local/bin/admin
EXPOSE 8083
ENTRYPOINT ["/usr/local/bin/admin"]
