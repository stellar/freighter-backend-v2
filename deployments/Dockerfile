# syntax=docker/dockerfile:1

############### Build stage ###############
FROM golang:1.24-bullseye AS builder

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
COPY --from=builder /bin/freighter-backend /app/

# Copy any additional required files (like configs)
COPY --from=builder /app/freighter-backend/configs ./configs

# Create a non-root user and group
RUN groupadd -r freighter && useradd -r -g freighter freighter && \
    chown -R freighter:freighter /app

# Add metadata
LABEL org.opencontainers.image.title="Freighter Backend" \
      org.opencontainers.image.description="Freighter Backend Service"

# Switch to non-root user
USER freighter

# Run the application using tini for proper signal handling
EXPOSE 3002
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["./freighter-backend"]
