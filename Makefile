.PHONY: help dev dev-down prod prod-no-db prod-down test vet build-backend build-frontend release

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
	@echo "  make vet                Run go vet on backend"
	@echo "  make build-backend      Verify backend compiles"
	@echo "  make build-frontend     Build frontend production bundle"
	@echo "  make release VERSION=v1.2.3  Test, tag, and push a release"

dev:
	docker compose up -d

dev-down:
	docker compose down

prod:
	docker compose -f docker-compose.prod.yml up -d

prod-no-db:
	docker compose -f docker-compose.prod-no-db.yml up -d

prod-down:
	docker compose -f docker-compose.prod.yml down

test:
	cd backend && go test ./...

vet:
	cd backend && go vet ./...

build-backend:
	cd backend && go build ./...

build-frontend:
	cd frontend && npm run build

release:
	@if [ -z "$(VERSION)" ]; then echo "Usage: make release VERSION=v1.2.3"; exit 1; fi
	$(RELEASE_CMD)
