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
	@echo "  ./dev up             - Start the complete isolated local environment"
	@echo "  ./dev logs           - Follow local environment logs"
	@echo "  ./dev down           - Stop local processes; retain local data"
	@echo ""
	@echo "  🗄️  Database Commands:"
	@echo "  make migrate-up      - Run database migrations up"
	@echo "  make migrate-down    - Run database migration down by 1"
	@echo "  make migrate-force   - Force migration version (VERSION=xxx)"
	@echo "  make migrate-version - Show current migration version"
	@echo "  make migrate-create  - Create new migration (NAME=xxx)"
	@echo ""
	@echo "  🏗️  Code generation:"
	@echo "  make sqlc            - Generate code from SQL queries"

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
