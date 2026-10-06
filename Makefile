COMPOSE_FILE := deployments/compose/docker-compose.mvp.yml

.PHONY: test test-quick test-security test-full compose-build compose-up compose-down test-e2e help

test: test-quick

test-quick:
	go test -race ./internal/... && opa test policies/rego policies/tests -v

test-security:
	go test -v -race ./tests/security/...

test-full:
	go test -race ./... && opa test policies/rego policies/tests -v

compose-build:
	docker compose -f $(COMPOSE_FILE) build

compose-up:
	docker compose -f $(COMPOSE_FILE) up -d

compose-down:
	docker compose -f $(COMPOSE_FILE) down

test-e2e: compose-up
	go test -v -race ./tests/integration/...
	docker compose -f $(COMPOSE_FILE) down

help:
	@echo "Aegis Zero-Trust Access Gateway Automation Targets:"
	@echo "  make test          - Run quick internal package and OPA policy unit tests"
	@echo "  make test-quick    - Run quick internal package and OPA policy unit tests"
	@echo "  make test-security - Run automated negative security test suite"
	@echo "  make test-full     - Run complete test suite and OPA policy tests"
	@echo "  make compose-build - Build Docker Compose MVP containers"
	@echo "  make compose-up    - Start Docker Compose MVP cluster in background"
	@echo "  make compose-down  - Stop and tear down Docker Compose MVP cluster"
	@echo "  make test-e2e      - Start cluster, run end-to-end integration tests, and tear down"
