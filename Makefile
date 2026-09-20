.PHONY: help dev dev-down prod prod-no-db prod-down test test-cover test-cover-check test-integration test-parser test-tui vet vet-tui build-backend build-frontend build-tui openapi-check release

ifeq ($(OS),Windows_NT)
RELEASE_CMD = powershell -ExecutionPolicy Bypass -File scripts/release.ps1 $(VERSION)
else
RELEASE_CMD = ./scripts/release.sh $(VERSION)
endif

help:
	@echo "FinTrak targets:"
	@echo "  make dev                Start local dev stack (docker compose up -d)"
	@echo "  make dev-down           Stop local dev stack"
	@echo "  make prod               Deploy production stack (with bundled DB)"
	@echo "  make prod-no-db         Deploy production stack (external DB)"
	@echo "  make prod-down          Stop production stack"
	@echo "  make test               Run backend tests"
	@echo "  make test-cover         Run backend tests with a coverage profile"
	@echo "  make test-cover-check   Run backend tests and enforce the coverage floor"
	@echo "  make test-integration   Run backend integration tests (Docker + testcontainers)"
	@echo "  make test-parser        Run statement parser tests"
	@echo "  make test-tui           Run TUI tests"
	@echo "  make vet                Run go vet on backend"
	@echo "  make vet-tui            Run go vet on the TUI"
	@echo "  make build-backend      Verify backend compiles"
	@echo "  make build-frontend     Build frontend production bundle"
	@echo "  make build-tui          Verify the TUI compiles"
	@echo "  make openapi-check      Verify openapi.yaml covers every registered route"
	@echo "  make release VERSION=v1.2.3  Test, tag, and push a release"

dev:
	docker compose up -d

dev-down:
	docker compose down

prod:
	@# Preflight: fail fast (and portably) if required secrets/image vars are unset.
	docker compose -f docker-compose.prod.yml config --quiet
	docker compose -f docker-compose.prod.yml up -d

prod-no-db:
	@# Preflight: fail fast (and portably) if required secrets/image vars are unset.
	docker compose -f docker-compose.prod-no-db.yml config --quiet
	docker compose -f docker-compose.prod-no-db.yml up -d

prod-down:
	docker compose -f docker-compose.prod.yml down

test:
	cd backend && go test ./...

test-cover:
	cd backend && go test -covermode=atomic -coverprofile=coverage.out ./...

test-cover-check:
	cd backend && go test -covermode=atomic -coverprofile=coverage.out ./...
	cd backend && go run ./cmd/covercheck -profile coverage.out -min 85

test-integration:
	cd backend && go test -tags=integration -count=1 ./...

test-parser:
	cd statement_parser && uv run python -m unittest discover -s tests -v

vet:
	cd backend && go vet ./...

build-backend:
	cd backend && go build ./...

build-tui:
	cd tui && go build ./...

vet-tui:
	cd tui && go vet ./...

test-tui:
	cd tui && go test ./...

build-frontend:
	cd frontend && bun run build

openapi-check:
	cd backend && go test . -run 'TestOpenAPI|TestServeOpenAPISpec' -count=1

release:
	@if [ -z "$(VERSION)" ]; then echo "Usage: make release VERSION=v1.2.3"; exit 1; fi
	$(RELEASE_CMD)
