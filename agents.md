# AGENTS.md

FinTrak: personal finance tracker. Monorepo with `backend/` (Go 1.26 + Gin + PostgreSQL/pgx) and `frontend/` (React 18 + Vite + Tailwind CSS 4). All routes live under `/api/v1`; API docs are in `README.md`.

## Commands (run from repo root)

- `make dev` — `docker compose up -d` (db + backend + frontend + adminer). Frontend at :3000, API at :8080, adminer at :8081.
- `make test` / `make vet` / `make build-backend` / `make build-frontend`
- `make release VERSION=v1.2.3` — must be on `master` with a clean tree; runs backend tests + frontend build, then tags & pushes. CI then publishes GHCR images. Releases are only cut from `master`.
- Frontend has **no lint/typecheck/test scripts** — only `dev`, `build`, `preview`. Verification is `npm run build`.
- CI (`.github/workflows/docker-publish.yml`) also enforces `go mod tidy` leaves `go.mod`/`go.sum` unchanged — keep them tidy.

## Backend

- All routes are registered in `backend/main.go` `setupRouter` (per-handler groups). Adding an endpoint means: handler in `handlers/`, route in `main.go`, migration if schema changes.
- All models/types live in one file: `backend/models/models.go`. Handler filenames are singular: `rule.go`, `link.go`, `payee.go`, `account_type.go`.
- **Migrations**: never edit the schema directly. Add a new `NNNNNN_*.up.sql` / `.down.sql` in `backend/db/migrations` (current schema is a single squashed migration, `000001_initial_schema`). Migrations run automatically at startup via `db.RunMigrations`, followed by `db.SeedAccountTypes()` (idempotent, every boot). `SeedDefaultCategories` runs per-user at registration.
- **Tests need no database**: unit tests use `pgxmock` and swap the package-level `db.Pool` global with a mock (see `backend/db/db_test.go` `setupMock`, `handlers/*_test.go`). Tests that hit SQL expect exact queries/args against the mock — change a query string or arg order and tests fail. Follow the existing `new*TestRouter()` + `testAuthMiddleware()` helpers; always call `gin.SetMode(gin.TestMode)`.
- Config via godotenv (`.env`, template in `.env.example`). `JWT_SECRET` has a dev default; startup fails if unset when `APP_ENV=production`. `ALLOWED_ORIGINS` is a comma-separated CORS allowlist. `main.Version` is injected via `-ldflags` in Docker builds.
- Business logic stays in handlers; e.g. transfer scoring is `calculateTransferScore` in `backend/handlers/link.go`.
- Statement parsing (`backend/handlers/statement.go`) is decoupled: the backend only forwards uploaded PDFs to a standalone Python service (`statement_parser/`) over HTTP. The parser base URL comes from `STATEMENT_PARSER_URL` (config `ParserURL`, wired in `setupRouter` via `SetStatementParserURL`). Handler tests use `httptest` to simulate the upstream parser.

## Frontend

- All API calls go through the single `src/api/client.js` (exports one `api` object + `downloadCSV`). Base URL is `VITE_API_URL` or defaults to `http://localhost:8080/api/v1`. Do not call `fetch` elsewhere.
- Components are PascalCase directories under `src/components/<Feature>/<Feature>.jsx` (e.g. `Dashboard/Dashboard.jsx`, `Transactions/Transactions.jsx`). Routing is in `src/App.jsx`.
- Global state via React Context in `src/context/` — `AuthContext` (auth + JWT in localStorage) and `SettingsContext` (compact layout, page size). New UI should respect the Compact Layout toggle.
- CSV import is client-side (PapaParse) in `components/Import/Import.jsx`; bank-statement parsing quirks live there.
- Vite dev server runs on :5173 (`host: true` for Docker). In the Docker/nginx setup the frontend reverse-proxies `/api/v1` to the backend.

## References

- `FLOWCHART.md` — system architecture, transaction lifecycle, ER diagram.
- `scripts/release.sh` / `scripts/release.ps1` — release guardrails (branch, clean tree, tag format).
