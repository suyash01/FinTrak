.PHONY: help dev dev-down prod prod-no-db prod-down test test-cover test-cover-check test-integration test-parser test-parser-cover-check test-client test-client-cover-check vet build-client test-tui test-tui-cover test-tui-cover-check vet-client vet-tui build-tui test-mcp test-mcp-cover-check vet-mcp build-mcp build-backend build-frontend openapi-check docs-check release

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
	@echo "  make test-parser-cover-check  Run parser tests and enforce the coverage floor"
	@echo "  make test-client        Run shared API client tests"
	@echo "  make test-client-cover-check  Run client tests and enforce the coverage floor"
	@echo "  make test-tui           Run TUI tests"
	@echo "  make test-tui-cover     Run TUI tests with a coverage profile"
	@echo "  make test-tui-cover-check  Run TUI tests and enforce the coverage floor"
	@echo "  make test-mcp           Run MCP server tests"
	@echo "  make test-mcp-cover-check  Run MCP tests and enforce the coverage floor"
	@echo "  make vet                Run go vet on backend"
	@echo "  make vet-client         Run go vet on the shared API client"
	@echo "  make vet-tui            Run go vet on the TUI"
	@echo "  make vet-mcp            Run go vet on the MCP server"
	@echo "  make build-backend      Verify backend compiles"
	@echo "  make build-frontend     Build frontend production bundle"
	@echo "  make build-client       Verify the shared API client compiles"
	@echo "  make build-tui          Verify the TUI compiles"
	@echo "  make build-mcp          Verify the MCP server compiles"
	@echo "  make openapi-check      Verify openapi.yaml covers every registered route"
	@echo "  make docs-check         Validate Markdown links, Mermaid, Makefile and OpenAPI docs"
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

# The parser's own floor, at the same 90% the Codecov flag reports: without it
# the documented target was aspirational, because nothing failed on a regression.
test-parser-cover-check:
	cd statement_parser && uv run --frozen coverage run --source=statement_parser -m unittest discover -s tests
	cd statement_parser && uv run --frozen coverage report --fail-under=90

vet:
	cd backend && go vet ./...

build-backend:
	cd backend && go build ./...

# The shared API client is its own module so the TUI and the MCP server use one
# client rather than a copy each: `cd client && go test ./...` runs it, and the
# spec-parity suite inside it still reads backend/openapi.yaml.

test-client:
	cd client && go test ./...

# Its own floor, enforced with the same stdlib-only covercheck as the other
# modules; the target matches the backend's library-level gate.
test-client-cover-check:
	cd client && go test -covermode=atomic -coverprofile=coverage.out ./...
	cd backend && go run ./cmd/covercheck -profile ../client/coverage.out -min 85 -root ../client

vet-client:
	cd client && go vet ./...

build-client:
	cd client && go build ./...

test-tui:
	cd tui && go test ./...

test-tui-cover:
	cd tui && go test -covermode=atomic -coverprofile=coverage.out ./...

# The floor is enforced by the backend's covercheck (stdlib-only), the same tool
# and the same baseline the CI job uses. It sits at 18% because the shared API
# client moved out to client/ (25.25% -> 19.77% when it left, with the client
# now carrying its own 85% floor): the render-heavy screens in internal/ui are
# what this number really measures. Ratchet it up as screen tests land.
test-tui-cover-check:
	cd tui && go test -covermode=atomic -coverprofile=coverage.out ./...
	cd backend && go run ./cmd/covercheck -profile ../tui/coverage.out -min 18 -root ../tui

vet-tui:
	cd tui && go vet ./...

build-tui:
	cd tui && go build ./...

test-mcp:
	cd mcp && go test ./...

# The MCP server's own floor. Ratchet it up as the surface grows: the tool audit
# against openapi.yaml and the binary's end-to-end test keep it high.
test-mcp-cover-check:
	cd mcp && go test -covermode=atomic -coverprofile=coverage.out ./...
	cd backend && go run ./cmd/covercheck -profile ../mcp/coverage.out -min 80 -root ../mcp

vet-mcp:
	cd mcp && go vet ./...

build-mcp:
	cd mcp && go build ./...

build-frontend:
	cd frontend && bun run build

openapi-check:
	cd backend && go test . -run TestOpenAPI -count=1
	cd backend && go test . -run TestServeOpenAPISpec -count=1

docs-check:
	python scripts/check-docs.py

release:
	@if [ -z "$(VERSION)" ]; then echo "Usage: make release VERSION=v1.2.3"; exit 1; fi
	$(RELEASE_CMD)
