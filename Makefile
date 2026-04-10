.PHONY: build run test lint migrate docker-build docker-up docker-down clean

build:
	go build -o bin/nkorebank ./cmd/nkorebank

run:
	go run ./cmd/nkorebank

test:
	go test ./... -v -race -coverprofile=coverage.out

lint:
	golangci-lint run ./...

migrate:
	@echo "Run migrations with: psql -f migrations/*.sql"

docker-build:
	docker build -t nkore-bank:latest .

docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

clean:
	rm -rf bin/ coverage.out
