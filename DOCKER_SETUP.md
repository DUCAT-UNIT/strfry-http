# Strfry Docker Setup

Easy Docker Compose setup for running and testing Strfry relay.

## Quick Start

### Build and Start Relay

```bash
# Using Makefile (recommended)
make docker-build    # Build the Docker image
make docker-up       # Start relay

# Or using docker-compose directly
docker-compose up -d

# View logs
make docker-logs     # or: docker-compose logs -f strfry-relay

# Check status
docker-compose ps
```

### Stop and Remove

```bash
# Using Makefile
make docker-down     # Stop and remove

# Or using docker-compose directly
docker-compose stop
docker-compose down

# Remove everything including volumes
docker-compose down -v
```

## Configuration

The `docker-compose.yml` file is configured with:
- **Container name**: `strfry-relay` (fixed name for easy management)
- **Ports**:
  - `7777`: WebSocket relay port
  - `8080`: HTTP API port
- **Volumes**:
  - `./strfry.conf`: Configuration file (read-only)
  - `./strfry-db`: Database directory (persistent)
- **Health check**: Monitors `/health` endpoint
- **Restart policy**: `unless-stopped`

## Testing

Run tests from the root directory:

```bash
# First time setup
make test-setup

# Run all tests (auto-restarts relay)
make test

# See all test commands
make help-test
```

The Makefile automatically:
1. Stops the relay container
2. Removes it (resets rate limit buckets)
3. Starts it fresh
4. Runs tests

Available test commands:
- `make test` - All tests
- `make test-short` - Quick tests only
- `make test-security` - Security tests
- `make test-query` - Query filter tests
- `make test-ratelimit` - Rate limit tests
- `make test-load` - Load tests

## Troubleshooting

### Check if relay is running
```bash
docker-compose ps
curl http://localhost:8080/health
```

### View logs
```bash
# All logs
docker-compose logs

# Follow logs
docker-compose logs -f

# Last 100 lines
docker-compose logs --tail=100
```

### Restart relay
```bash
docker-compose restart
```

### Rebuild after code changes
```bash
docker-compose build
docker-compose up -d
```

### Clean start (removes database)
```bash
docker-compose down -v
rm -rf strfry-db/*
docker-compose up -d
```

## Development Workflow

### 1. Make changes to strfry source code

```bash
vim src/apps/relay/MyFile.cpp
```

### 2. Rebuild and restart

```bash
make docker-build
make docker-up

# Or combined:
make test-setup
```

### 3. Run tests

```bash
make test

# Or specific test categories:
make test-query
make test-security
```

## Configuration Changes

### Edit strfry.conf

```bash
vim strfry.conf
```

### Apply changes (restart relay)

```bash
docker-compose restart
```

The relay will automatically reload the configuration on restart.

## Ports

- **7777**: Nostr WebSocket relay (NIP-01)
- **8080**: HTTP REST API
  - `GET /health` - Health check
  - `POST /api/quotes` - Submit events
  - `GET /api/query` - Query events
  - `GET /api/quotes/:id` - Get event by ID

## Docker Compose Commands

```bash
# Start in foreground (see logs directly)
docker-compose up

# Start in background
docker-compose up -d

# Stop services
docker-compose stop

# Stop and remove
docker-compose down

# View logs
docker-compose logs -f

# Execute commands in container
docker-compose exec strfry-relay /bin/bash

# Restart specific service
docker-compose restart strfry-relay

# Check resource usage
docker stats strfry-relay
```

## Environment-Specific Configs

You can override settings using environment-specific compose files:

```bash
# Development
docker-compose -f docker-compose.yml -f docker-compose.dev.yml up -d

# Production
docker-compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

## Database Backup

```bash
# Backup database
docker-compose exec strfry-relay strfry export > backup.jsonl

# Restore database
cat backup.jsonl | docker-compose exec -T strfry-relay strfry import
```

## Monitoring

The health check endpoint is monitored automatically by Docker:

```bash
# Check health status
docker inspect strfry-relay --format='{{.State.Health.Status}}'

# View health check logs
docker inspect strfry-relay --format='{{range .State.Health.Log}}{{.Output}}{{end}}'
```

## Production Deployment

For production, consider:

1. **Reverse Proxy**: Use nginx/traefik for HTTPS
2. **Monitoring**: Add Prometheus metrics endpoint
3. **Logging**: Configure log rotation
4. **Backups**: Automated database backups
5. **Resource Limits**: Set memory/CPU limits in docker-compose.yml

Example with resource limits:

```yaml
services:
  strfry-relay:
    # ... existing config ...
    deploy:
      resources:
        limits:
          cpus: '2.0'
          memory: 2G
        reservations:
          cpus: '1.0'
          memory: 1G
```
