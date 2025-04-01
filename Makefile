check:
	./scripts/check.sh

# Docker commands
.PHONY: docker-build docker-up docker-down docker-logs docker-clean

# Build the Docker images
docker-build:
	docker-compose -f deployments/docker-compose.yml build

# Start the containers
docker-up:
	docker-compose -f deployments/docker-compose.yml up

# Start the containers in detached mode
docker-up-d:
	docker-compose -f deployments/docker-compose.yml up -d

# Stop the containers
docker-down:
	docker-compose -f deployments/docker-compose.yml down

# View container logs
docker-logs:
	docker-compose -f deployments/docker-compose.yml logs -f

# Clean up Docker resources
docker-clean:
	docker-compose -f deployments/docker-compose.yml down -v
	docker system prune -f

# Build and start containers in one command
docker-build-up: docker-build docker-up

# Build and start containers in detached mode
docker-build-up-d: docker-build docker-up-d
