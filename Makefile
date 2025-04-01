check:
	./scripts/check.sh

# Docker commands
.PHONY: docker-build docker-up docker-down docker-logs docker-clean

# Build the Docker images
docker-build:
	docker-compose -f deployments/docker-compose.yml -p freighter-backend build

# Start the containers
docker-up:
	docker-compose -f deployments/docker-compose.yml -p freighter-backend up

# Stop the containers
docker-down:
	docker-compose -f deployments/docker-compose.yml -p freighter-backend down

# View container logs
docker-logs:
	docker-compose -f deployments/docker-compose.yml -p freighter-backend logs -f

# Clean up Docker resources
docker-clean:
	docker-compose -f deployments/docker-compose.yml -p freighter-backend down -v
	docker system prune -f

# Build and start containers in one command
docker-build-up: docker-build docker-up
