include .env
export

DB_URL=postgres://$(DB_USER):$(DB_PASSWORD)@localhost:$(DB_PORT)/$(DB_NAME)?sslmode=disable

.PHONY: up down logs

up:
	docker compose up -d

down:
	docker compose down

run:
	go run cmd/main.go

restart:
	docker compose down
	docker compose up -d

logs:
	docker compose logs -f

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

reset:
	docker compose down -v
	docker compose up -d
	sleep 5
	make migrate-up
