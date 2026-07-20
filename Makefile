include .env
export

DB_URL=postgres://$(DB_USER):$(DB_PASSWORD)@localhost:$(DB_PORT)/$(DB_NAME)?sslmode=disable

# ============================================
# HELP
# ============================================
.PHONY: help
help:
	@echo "Available commands:"
	@echo ""
	@echo "  🐳 Docker Commands:"
	@echo "  make up              - Start all containers (server-dev & worker-dev, hot reload)"
	@echo "  make up-fg           - Same as 'make up' but attached, to watch Air rebuild logs"
	@echo "  make down            - Stop all containers"
	@echo "  make restart         - Restart all containers"
	@echo "  make logs            - View logs from all containers"
	@echo "  make logs-server     - View logs from server-dev"
	@echo "  make logs-worker     - View logs from worker-dev"
	@echo "  make reset           - Reset database & start fresh"
	@echo ""
	@echo "  🔨 Development Commands (all run inside the server-dev container):"
	@echo "  make bootstrap       - Create initial roles & admin user"
	@echo "  make sh-server       - Shell into server-dev container"
	@echo "  make sh-worker       - Shell into worker-dev container"
	@echo ""
	@echo "  🗄️  Database Commands:"
	@echo "  make migrate-up      - Run database migrations up"
	@echo "  make migrate-down    - Run database migration down by 1"
	@echo "  make migrate-force   - Force migration version (VERSION=xxx)"
	@echo "  make migrate-version - Show current migration version"
	@echo "  make migrate-create  - Create new migration (NAME=xxx)"
	@echo ""
	@echo "  🏗️  Build Commands:"
	@echo "  make sqlc             - Generate code from SQL queries"
	@echo "  make build-server     - Build server-dev image (with Air)"
	@echo "  make build-worker     - Build worker-dev image (with Air)"
	@echo "  make build-all        - Build both server-dev & worker-dev images"

# ============================================
# DOCKER
# ============================================
.PHONY: up up-fg down restart logs logs-server logs-worker logs-scheduler reset

up:
	docker compose up -d

up-fg:
	docker compose up

down:
	docker compose down

restart: down up

logs:
	docker compose logs -f

logs-server:
	docker compose logs -f server-dev

logs-worker:
	docker compose logs -f worker-dev

logs-scheduler:
	docker compose logs -f scheduler-dev

reset:
	docker compose down -v
	docker compose up -d
	sleep 5
	$(MAKE) migrate-up

# ============================================
# DEVELOPMENT
# ============================================
.PHONY: bootstrap sh-server sh-worker build-server build-worker build-scheduler build-all

bootstrap:
	docker compose exec server-dev go run ./cmd/bootstrap/main.go

sh-server:
	docker compose exec server-dev sh

sh-worker:
	docker compose exec worker-dev sh

build-server:
	docker build -f Dockerfile.dev --build-arg AIR_CONFIG=.air.server.toml -t notification-server-dev:latest .

build-worker:
	docker build -f Dockerfile.dev --build-arg AIR_CONFIG=.air.worker.toml -t notification-worker-dev:latest .

build-scheduler:
	docker build -f Dockerfile.dev --build-arg AIR_CONFIG=.air.scheduler.toml -t notification-scheduler-dev:latest .

build-all: build-server build-worker build-scheduler

# ============================================
# DATABASE MIGRATION
# ============================================
.PHONY: migrate-up migrate-down migrate-force migrate-version migrate-create

migrate-up:
	migrate -path db/migrations -database "$(DB_URL)" up

migrate-down:
	migrate -path db/migrations -database "$(DB_URL)" down 1

migrate-force:
	migrate -path db/migrations -database "$(DB_URL)" force $(VERSION)

migrate-version:
	migrate -path db/migrations -database "$(DB_URL)" version

migrate-create:
	migrate create -ext sql -dir db/migrations -seq $(NAME)

# ============================================
# BUILD
# ============================================
.PHONY: sqlc

sqlc:
	sqlc generate -f db/sqlc.yaml
