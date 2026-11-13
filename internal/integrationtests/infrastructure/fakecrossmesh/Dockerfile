# ABOUTME: Image for the fake Cross Mesh Ingress API used by the integration tests, built from the repo root:
# ABOUTME: docker build -f internal/integrationtests/infrastructure/fakecrossmesh/Dockerfile .

FROM golang:1.25-bookworm AS builder

WORKDIR /app/freighter-backend
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN go build -o /bin/fakecrossmesh ./internal/integrationtests/infrastructure/fakecrossmesh

FROM debian:bookworm-slim

COPY --from=builder /bin/fakecrossmesh /app/fakecrossmesh
USER nobody
EXPOSE 8080
ENTRYPOINT ["/app/fakecrossmesh"]
