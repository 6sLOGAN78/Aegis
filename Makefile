COMPOSE_FILE := deployments/compose/docker-compose.mvp.yml

.PHONY: certs test test-quick test-security test-workload test-bypass test-phase2 test-full compose-build compose-up compose-down test-e2e help

certs:
	go run ./scripts/certificates/main.go

test: test-quick

test-quick:
	go test -race ./internal/... ./services/... && opa test policies/rego policies/tests -v

test-security:
	go test -v -race ./tests/security/...

test-workload:
	go test -v -race ./tests/security/ -run "TestWorkload|TestAssertion"

test-bypass:
	go test -v -race ./tests/integration/ -run TestBackendBypassPrevention

test-phase2: test-workload test-bypass
	go test -v -race ./services/middleware/...

test-full:
	go test -race ./... && opa test policies/rego policies/tests -v

compose-build:
	docker compose -f $(COMPOSE_FILE) build

compose-up: certs
	docker compose -f $(COMPOSE_FILE) up -d

compose-down:
	docker compose -f $(COMPOSE_FILE) down

test-e2e: compose-up
	go test -v -race ./tests/integration/...
	docker compose -f $(COMPOSE_FILE) down

help:
	@echo "Aegis Zero-Trust Access Gateway Automation Targets:"
	@echo "  make certs         - Generate local development PKI certificates and assertion keys"
	@echo "  make test          - Run quick package and OPA policy unit tests"
	@echo "  make test-quick    - Run quick internal/services tests and OPA policy tests"
	@echo "  make test-security - Run automated negative security test suite"
	@echo "  make test-workload - Run workload identity and assertion security tests"
	@echo "  make test-bypass   - Run Docker Compose bypass prevention integration tests"
	@echo "  make test-phase2   - Run all Phase 2 workload identity and bypass prevention tests"
	@echo "  make test-full     - Run complete test suite and OPA policy tests"
	@echo "  make compose-build - Build Docker Compose MVP containers"
	@echo "  make compose-up    - Ensure certs and start Docker Compose MVP cluster"
	@echo "  make compose-down  - Stop and tear down Docker Compose MVP cluster"
	@echo "  make test-e2e      - Start cluster, run end-to-end integration tests, and tear down"
