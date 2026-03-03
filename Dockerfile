# Stage 1: Build
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install git (needed for go get with some modules)
RUN apk add --no-cache git

# Copy module files first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build the server binary (Fix 1: ./server instead of ./server/...)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/server ./server

# Stage 2: Runtime
FROM alpine:3.19

# Fix 7: Add wget for healthcheck + ca-certificates for HTTPS scraping
RUN apk add --no-cache ca-certificates tzdata wget

WORKDIR /app

# Copy compiled binary
COPY --from=builder /app/server .

EXPOSE 8080

ENTRYPOINT ["/app/server"]
