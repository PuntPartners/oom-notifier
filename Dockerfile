# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum* ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o oom-notifier ./cmd/oom-notifier

# Final stage
FROM alpine:3.19

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates

# Create non-root user (but we'll run as root for /dev/kmsg access)
RUN addgroup -g 1000 -S oom && \
    adduser -u 1000 -S oom -G oom

# Copy binary from builder
COPY --from=builder /app/oom-notifier /oom-notifier

# Note: Container must run as root to access /dev/kmsg
USER root

ENTRYPOINT ["/oom-notifier"]