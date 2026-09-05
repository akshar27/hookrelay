.PHONY: help up down test build run image lint clean migrate-status

GO ?= go

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

up: ## Start Postgres (host port 5435)
	docker compose up -d db

down: ## Stop and remove Postgres + volume
	docker compose down -v

test: ## Run all tests (spins up throwaway Postgres via testcontainers)
	$(GO) test ./... -race -count=1

build: ## Build the binary into bin/
	$(GO) build -o bin/hookrelay ./cmd/hookrelay

run: up ## Run locally against the compose Postgres
	HOOKRELAY_DATABASE_URL=postgres://hookrelay:hookrelay@localhost:5435/hookrelay?sslmode=disable \
	$(GO) run ./cmd/hookrelay

image: ## Build the production Docker image
	docker compose --profile app build

lint: ## go vet + staticcheck
	$(GO) vet ./...
	$(GO) run honnef.co/go/tools/cmd/staticcheck@latest ./...

clean:
	rm -rf bin
