# syntax=docker/dockerfile:1

############### Build stage ###############
FROM golang:1.24-bullseye AS builder

# Install build dependencies
RUN apt-get update && apt-get install -y git && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app/freighter-backend
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN go build -o /bin/freighter-backend

############### Final stage ###############
FROM debian:bullseye-slim

# Install tini and other runtime dependencies
RUN apt-get update && apt-get install -y tini wget && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy binary from builder with explicit permissions
COPY --from=builder --chmod=755 /bin/freighter-backend /app/

# Copy any additional required files (like configs)
COPY --from=builder /app/freighter-backend/configs ./configs

# Create a non-root user and group
RUN groupadd -r freighter && useradd -r -g freighter freighter && \
    chown -R freighter:freighter /app

# Set default environment variables
ENV FREIGHTER_BACKEND_HOST=0.0.0.0 \
    FREIGHTER_BACKEND_PORT=3002 \
    REDIS_HOST=redis \
    REDIS_PORT=6379 \
    MODE=development

# Add metadata
LABEL org.opencontainers.image.title="Freighter Backend" \
      org.opencontainers.image.description="Freighter Backend Service" \
      org.opencontainers.image.version="1.0.0"

# Switch to non-root user
USER freighter

# Run the application using tini for proper signal handling
EXPOSE 3002
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["./freighter-backend", "serve"]
