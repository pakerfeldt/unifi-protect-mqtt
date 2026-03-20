# Stage 1: Build the application
FROM golang:1.25.2-alpine AS builder
WORKDIR /app
# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download
# Copy the rest of the source code
COPY . .
# Build the binary statically
RUN CGO_ENABLED=0 GOOS=linux go build -o /unifi-protect-mqtt .
# Stage 2: Minimal runtime environment
FROM alpine:latest
WORKDIR /app
# Install ca-certificates (needed for external HTTPS/MQTT endpoints) and timezone data
RUN apk add --no-cache ca-certificates tzdata
# Copy the built binary from the builder stage
COPY --from=builder /unifi-protect-mqtt /app/unifi-protect-mqtt
# (Optional but recommended) Run as an unprivileged user
RUN adduser -D -g '' appuser
USER appuser
# Expose the default proxy port
EXPOSE 8080
ENTRYPOINT ["/app/unifi-protect-mqtt"]
