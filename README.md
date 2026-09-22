# 🚀 FinTrak

[![CI](https://github.com/suyash01/FinTrak/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/suyash01/FinTrak/actions/workflows/docker-publish.yml)
[![Codecov](https://img.shields.io/codecov/c/github/suyash01/FinTrak?branch=master&logo=codecov)](https://codecov.io/gh/suyash01/FinTrak)
[![Backend coverage](https://img.shields.io/codecov/c/github/suyash01/FinTrak?branch=master&flag=backend&label=backend&logo=codecov)](https://codecov.io/gh/suyash01/FinTrak)
[![Frontend coverage](https://img.shields.io/codecov/c/github/suyash01/FinTrak?branch=master&flag=frontend&label=frontend&logo=codecov)](https://codecov.io/gh/suyash01/FinTrak)
[![Parser coverage](https://img.shields.io/codecov/c/github/suyash01/FinTrak?branch=master&flag=parser&label=parser&logo=codecov)](https://codecov.io/gh/suyash01/FinTrak)
[![TUI coverage](https://img.shields.io/codecov/c/github/suyash01/FinTrak?branch=master&flag=tui&label=tui&logo=codecov)](https://codecov.io/gh/suyash01/FinTrak)

**FinTrak** is a powerful, modern, and high-performance personal finance tracking application built with a **Go** backend and a **React + TypeScript** (Vite, bun) frontend. It simplifies transaction management, categorizes expenses using smart rules, and provides insightful dashboard visualizations to help you stay on top of your finances.

---

## ✨ Features

- **Dashboard & Analytics**: Get a clear overview of your financial health with income vs. expense summaries and category-wise breakdowns using **Recharts**, framed by calendar month or by an account's statement cycles (`groupBy=billing_cycle` — offered for any account that has a billing day, not just credit cards).
- **Money Flow**: The transaction link graph drawn as a Sankey — money sources (income categories) → accounts → spending categories → payees — with a period timeline strip that scrubs the window, clickable nodes that open Transactions pre-filtered to them, and link tracing that walks a transaction's chain (purchase → refund, transfer → transfer) hop by hop.
- **Cash Flow Calendar**: A GitHub-style heatmap of each day's income, expense and net, with the selected account's billing-cycle boundaries and its synthetic balance rows overlaid.
- **Transaction Management**:
  - **CSV Import**: Import a bank/credit-card CSV export (powered by PapaParse) through a column-mapping step, then preview the parsed rows before importing — uncheck the rows you don't want, and rows that already exist in the account are flagged from a read-only duplicate check (the preview can be imported with duplicates skipped or kept).
  - **PDF Statement Import**: Upload a bank/credit-card statement PDF (password-protected files supported) and preview the extracted transactions before importing (powered by a standalone parser service). The parser's per-page reconciliation warnings are surfaced in the preview, so a statement whose rebuilt subtotals disagreed with the printed ones is flagged before anything is written.
  - **Paperless-ngx Import**: Pull a document straight from a Paperless instance — with correspondent/type/tag filters — and parse it through the same preview-then-import flow.
  - **Advanced Filtering**: Search and filter transactions by date, amount, account, category, category group, payee, tag, type, or linked state — the account, category, payee and tag filters are multi-select and match any of the picked values; free-text search spans the description, notes, payee name, and tags. The API's grammar is wider still: an exact `amount`, loan attachment (`loanAccountId` / `excludeAttached`) and recurring state (`recurringId` / `recurring=linked|unlinked`).
  - **Filter-aware export**: Export exactly what is currently filtered as a flat, spreadsheet-friendly CSV (the same filter grammar the list uses), or a single account's history from the Accounts page.
  - **Compact Layout**: High-density view for managing large volumes of transactions.
- **Installable & offline (PWA)**: The SPA ships a web app manifest and a service worker, so it installs to a home screen and boots without a network. The last-loaded dashboard, ledger page and reference data are kept read-only for offline viewing, and a transaction added while offline is queued in an outbox that syncs when the connection returns — each queued entry carries a client-generated idempotency key, so a retry can never post it twice.
- **Backup & Restore**: Export everything you own as a single JSON bundle, and restore it into a fresh account — portable across FinTrak instances.
- **Accounts**: Track multiple bank accounts, credit cards, wallets and loans, each with a bank, color, currency and an optional billing day; one account is the default, used to pre-fill the account filter on the Dashboard and the Transactions list.
- **Account Types**: Shared reference data (`bank`, `credit_card`, `loan`) whose positive-transaction convention defines how an account's running balance is computed. Administrators can add custom types (e.g. `wallet`) for every user; the built-in types are immutable.
- **Category & Payee Management**: Organize spending with grouped categories — the four base groups plus your own custom groups — and tracked payees. Categories and groups are flat; deleting a category uncategorizes its transactions and removes the rules that pointed at it.
- **Tags**: Free-text tags on a transaction, first-class in the UI: filter the list by tag, browse the vocabulary with usage counts, rename a tag across the whole history, and bulk add/remove one across a selection.
- **Smart Rules Engine**: Priority-ordered condition/action rules. A rule matches a description pattern (`contains`, `starts_with`, `exact`) plus optional ANDed conditions (account, category, payee, amount range, type, date window, linked/recurring state) and can set the category and payee and add tags or append a note. A live preview reports how many currently-uncategorized transactions a new or edited rule would change before it is saved, and one click applies the rules to everything uncategorized.
- **Transaction Linking**:
  - **Transfers**: Link matching transactions between your own accounts to avoid double-counting.
  - **Cashback & Refunds**: Link refunds or cashback to their original purchases.
  - **Bill Payments**: Link a payment to its counterpart transaction (e.g. credit-card bill paid from a bank account).
  - Link types are chosen manually for any pair of transactions, whether on the same account or different accounts.
  - **Circular Money**: The Money Flow page reports the account-to-account cycles the Sankey has to net
    away or break to stay acyclic, plus the one-directional flows with no counterpart link — the
    diagnostics for spotting money bouncing between accounts or a half-entered transfer.
- **Loan / EMI Accounts**: Track loans with no transactions of their own — EMI payments stay on your bank/credit-card accounts and are attached to the loan via a bulk "Link to Loan" action. One transaction can be attached to at most one loan account. Loan accounts show the total repaid, and each loan can carry an optional **amortization schedule** (principal, optional processing fee, annual rate, tenure, first installment date, optional disbursal date) that generates the EMI table — every installment split into principal and interest, with the paid ones matched to the attached EMI payments, plus interest paid and outstanding principal. A processing fee is recorded for reference without touching the table, so the EMI and every installment amortize the full principal — a lender that finances its charges instead has them entered in the principal; when the disbursal date makes the first period a broken month (disbursed on the 20th, first EMI on the 5th) that period is charged its actual days over a 30-day month and the EMI is re-solved so the loan still clears in the full tenure, keeping every installment level; and a **balance transfer** settles one loan at its **payoff** — the principal it still owes plus the interest accrued since its last EMI payment, which is what a lender's quote charges for settling mid-period — and applies that to another loan, either by recasting the installments that target still owes or — the refinance shape — by **taking it over**: the target keeps amortizing its own principal and the amount is paid out of its disbursement, so it releases that much less cash. Each loan also reconciles what it released (principal less the fee and less any takeover it funded) against the bank credit you link, flagging a mismatch instead of hiding it, and an EMI payment attached to a loan can be unlinked from the loan's own table (the payment is matched to the installment it covers, so the later matches shift up).
- **Recurring & Subscriptions**: Define repeating charges and income (rent, salary, subscriptions) with a frequency/interval. FinTrak forecasts the upcoming occurrences and suggests matching transactions, which you confirm with an explicit link. A series is defined by a list of account/amount entries, each with its own start and optional end date (a price rise, card switch, or a discontinuation-then-resume is a new entry rather than a rewrite); entries must not overlap, gaps are allowed, and the subscription's overall period is derived from them. Matching uses the entry whose date range covers each transaction. Linked transactions carry a recurring badge on the Transactions page, where a bulk "Link to subscription" action can attach or detach them. A dashboard card summarizes the monthly recurring totals and the next upcoming charges. Repeating series are **never** created or linked automatically.
- **Link Suggestions**: Confidence-scored, read-only link candidates — transfer pairs (the same amount within a few days, across different accounts) and cashback/refund credits matched to their likely originating purchase — offered for one-click confirmation in the terminal client; the web UI links manually, and nothing is ever linked automatically.
- **Close Accounts**: Mark an account closed and its transactions become immutable — no manual add/edit/remove, no bulk action (categorize, payee, billing cycle, tags, delete), no tag rename and no rule re-run can rewrite them; linking stays possible.
- **Bulk Operations**: Apply a category, payee, billing cycle or tag add/remove to a selection, delete many at once, or attach/detach the whole selection to a loan account or a recurring series.
- **Command Palette**: Ctrl/Cmd-K jumps to any page, opens the right record dialog, applies rules to uncategorized transactions, or toggles the theme.
- **Agent Access (MCP)**: `fintrak-mcp` serves the ledger to a model client (Claude Desktop, Claude Code, an IDE) over the Model Context Protocol, as **27 read-only tools**: the ledger, the aggregates, the rule and series definitions and the read-only preview endpoints the app itself uses. It cannot create, change or delete anything — the surface is checked against the OpenAPI document and enforced by a transport guard — so a model's suggestions are always confirmed in the app.
- **Admin Console**: An admin-only Settings card curates the shared global catalog of groups and categories, showing the category and transaction usage counts needed before editing or retiring one.

---

## 🛠️ Tech Stack

### Backend

- **Language**: Go 1.27
- **Framework**: [Gin Gonic](https://gin-gonic.com/)
- **Database**: PostgreSQL with [pgx](https://github.com/jackc/pgx)
- **Migrations**: [golang-migrate](https://github.com/golang-migrate/migrate)
- **Configuration**: godotenv
- **Auth**: stateless JWT (access + refresh) in httpOnly cookies
- **Money**: integer minor units (`internal/money.Amount`, an `int64` of cents) — no `float64` arithmetic anywhere

### Frontend

- **Library**: React 19 + TypeScript (Vite, bun)
- **Styling**: **Tailwind CSS 4** with semantic theme tokens (light/dark + 6 accent themes)
- **UI primitives**: [shadcn/ui](https://ui.shadcn.com/) on Radix (see `components.json`)
- **Icons**: Lucide React
- **Charts**: Recharts
- **Tables**: TanStack Table + TanStack Virtual (for smooth scrolling in long lists)
- **Routing**: React Router 7
- **Toasts**: sonner · **Command palette**: cmdk · **CSV**: PapaParse
- **Offline**: hand-written service worker + web app manifest (`public/sw.js`, `public/manifest.webmanifest`) — no build plugin
- **Tests**: Vitest + Testing Library under jsdom

### Statement parser (`statement_parser/`)

- **Language**: Python 3.14, managed with [uv](https://docs.astral.sh/uv/)
- **App**: Flask (gunicorn in the image) with `pdfplumber` for text extraction and `pypdf` for decrypting password-protected PDFs
- **Design**: a registry of per-bank extractors (`register_extractor`) selectable per upload, exposed by the service and surfaced to clients at `GET /api/v1/statements/extractors`

### Terminal client (`tui/`)

- **Language**: Go 1.27, a separate module (`github.com/fintrak/tui`)
- **Framework**: Bubble Tea + Bubbles/Lip Gloss, with [wish](https://github.com/charmbracelet/wish) for the optional SSH door
- **Tests**: stdlib `testing` plus a route-parity suite against `backend/openapi.yaml`

### Shared API client (`client/`)

- **Language**: Go 1.27, module `github.com/fintrak/client` (package `client/api`)
- **Role**: the single hand-written REST client for the Go side; the terminal client and the MCP server both depend on it, so auth, filters and the typed models exist once
- **Drift guard**: a route-parity suite pins every operation to `backend/openapi.yaml` in both directions

### MCP server (`mcp/`)

- **Protocol**: [Model Context Protocol](https://modelcontextprotocol.io/) over stdio (the [official Go SDK](https://github.com/modelcontextprotocol/go-sdk)), so an MCP client (Claude Desktop, Claude Code, an IDE) launches it as a subprocess
- **Surface**: 27 **read-only** tools over the same REST API and the same client the TUI uses — the ledger, its reference data, the aggregates, and the API's read-only preview endpoints
- **Guarantee**: every tool declares the API operation it performs, a test checks each against `backend/openapi.yaml`, and a transport guard refuses any other request before it can leave the process

---

## 🚀 Getting Started

### Prerequisites

- [Docker](https://www.docker.com/) & [Docker Compose](https://docs.docker.com/compose/)

### Quick Start with Docker

The easiest way to get FinTrak running is `make dev` (a thin wrapper over the
Compose file below), or Docker Compose directly:

1.  **Clone the repository**:
    ```bash
    git clone https://github.com/your-username/fintrak.git
    cd fintrak
    ```
2.  **Start the services**:
    ```bash
    make dev            # or: docker compose up -d
    ```
    This starts PostgreSQL, the statement parser, the backend, the frontend, Adminer and pgAdmin. Two further services are opt-in profiles: the dozzle log viewer (`docker compose --profile debug up -d`) and the terminal client's SSH door (`docker compose --profile tui up -d`, then `ssh -p 2222 localhost`).
3.  **Access the application**:
    - **Frontend**: [http://localhost:3000](http://localhost:3000)
    - **API Server**: [http://localhost:8080/api/v1](http://localhost:8080/api/v1)
    - **Statement parser** (internal to the backend in production): [http://localhost:5000](http://localhost:5000)
    - **Database Admin**: Adminer at [http://localhost:8081](http://localhost:8081), pgAdmin at [http://localhost:8082](http://localhost:8082)
    - **Log viewer** (debug profile only): [http://localhost:8083](http://localhost:8083)

Stop the stack with `make dev-down` (`docker compose down`).

### Development commands

`make help` lists everything. The targets below are the ones CI mirrors — its jobs run the same commands inline, and `make release` runs the full gate:

```bash
make test                   # backend unit tests (no database needed)
make test-cover-check       # backend tests + the 85% coverage floor
make test-integration       # backend integration tests (Docker + testcontainers)
make test-parser            # statement parser tests (uv/unittest)
make test-client            # shared API client tests
make test-client-cover-check  # client tests + its 85% coverage floor
make test-tui               # terminal client tests
make test-tui-cover-check   # TUI tests + its 18% coverage floor
make test-mcp               # MCP server tests
make test-mcp-cover-check   # MCP tests + its 80% coverage floor
make vet / make vet-client / make vet-tui / make vet-mcp   # go vet
make build-backend / make build-frontend / make build-client / make build-tui / make build-mcp
make openapi-check          # every registered route is in backend/openapi.yaml
make release VERSION=v1.2.3 # run CI's whole gate on master-at-origin, then tag and push
```

### Production Deployment

Copy `.env.example` to `.env`, set the values, then:

```bash
# With a bundled database (PostgreSQL)
docker compose -f docker-compose.prod.yml up -d

# Using an existing/external database
docker compose -f docker-compose.prod-no-db.yml up -d
```

`make prod` / `make prod-no-db` / `make prod-down` wrap those three commands (they preflight the Compose config first, so unset secrets or image variables fail before anything is created).

`APP_ENV=production` is set for you, so the backend refuses to start unless `JWT_SECRET` and `TOKEN_ENCRYPTION_KEY` are both set, distinct from every development value this repository publishes, and at least 32 characters with a non-trivial spread of characters (`openssl rand -hex 32`). `APP_ENV` accepts only `development` or `production`, and an unset value means **production** (fail closed): a deployment that forgets the variable gets the mandatory-secret/entropy floor, Secure cookies and no body logging instead of the development keys. Any other value — a typo like `prod`, or `staging` — stops startup. Only an explicit `APP_ENV=development` selects the built-in development secrets (which is what the dev Compose stack and `make dev` set). `ADMIN_EMAILS`, `ADMIN_SETUP_TOKEN`, `LOG_LEVEL`, `LOG_BODY_LIMIT`, `TRUSTED_PROXIES` and `FRONTEND_IP` are optional (log level defaults to `info` in production; body logging is off unless `LOG_BODY_LIMIT` is positive; `FRONTEND_IP` is the frontend nginx's static `app-net` address that `TRUSTED_PROXIES` trusts by default, and it must be overridden together with `APP_SUBNET`). `IMAGE_REPO` and `IMAGE_TAG` are required too: pin `IMAGE_TAG` to a released version (e.g. `v1.2.3`), never `latest`, so redeploys and rollbacks are deterministic. See `.env.example` for the full list.

The backend runs schema migrations on startup, and the frontend reverse-proxies `/api/v1` to the backend.

#### TLS termination (required)

Both Compose files serve **plain HTTP** and are designed to sit behind a TLS-terminating reverse proxy or ingress: production publishes only the frontend (`3000`), with the API reachable exclusively through the frontend's nginx over the internal `app-net` (the development stack additionally publishes the API on `8080`, plus its database and tool ports — all of them bound to `127.0.0.1`, so nothing in the dev stack is reachable from another machine). With `COOKIE_SECURE=true` (the production default) browsers only send the httpOnly session cookie over HTTPS, and `Strict-Transport-Security` is ignored over HTTP — so exposing the stack directly over HTTP is not a supported deployment. The supported topology is:

```
browser --HTTPS--> TLS terminator (nginx/Caddy/Traefik/cloud LB) --HTTP--> frontend:3000 --HTTP(internal)--> backend:8080
```

Requirements for the terminator:

- Terminate TLS and forward to the frontend on port 3000 (the frontend proxies `/api/v1` to the backend itself).
- Preserve `Host` and set `X-Forwarded-Proto: https`.
- Emit `Strict-Transport-Security` at the terminator. The frontend nginx also sends an HSTS header, but it is only seen by the TLS terminator (over the internal HTTP hop), so the browser-facing HSTS policy must be set where TLS terminates.
- Keep `COOKIE_SECURE=true`. Only set it to `false` for isolated local development over HTTP; never in production.
- `TRUSTED_PROXIES` (comma-separated proxy CIDRs) decides whether `X-Forwarded-For` / `X-Real-IP` are believed, and therefore whether per-IP rate limiting sees real client addresses. Both production Compose files set it for you, defaulting to the **frontend's single static address** (`172.28.0.2/32`, the `FRONTEND_IP` variable) rather than the whole `app-net` subnet: gin believes the left-most forwarded entry whenever the peer is trusted, so a subnet-wide allowlist would let any other container on that network — including the unauthenticated statement parser that handles hostile PDFs — forge a client IP and pick its own rate-limit bucket. Override `APP_SUBNET` and `FRONTEND_IP` **together** (the address must fall inside the subnet) if `172.28.0.0/24` collides with an existing Docker network on the host. The default applies only when the variable is **unset**; setting it **explicitly empty** means "trust no forwarded headers" (forwarded headers are then ignored and cannot be spoofed), which is the right setting only when nothing proxies to the API — not in the bundled stacks, where the frontend always does.

If TLS is terminated by another container in the same Compose project, add its network to `TRUSTED_PROXIES` (for example `TRUSTED_PROXIES=172.18.0.0/16`). Do not set `TRUSTED_PROXIES=0.0.0.0/0` — that trusts arbitrary client-supplied forwarding headers.

#### External PostgreSQL over TLS

When `DATABASE_URL` points at a database outside the Compose network, require TLS:

```
DATABASE_URL=postgres://user:pass@db.example.com:5432/fintrak?sslmode=verify-full&sslrootcert=/etc/fintrak/db-ca.pem
```

Use `sslmode=verify-full` (not `require`) so the server hostname and certificate chain are both verified; mount the CA bundle into the backend container. The bundled-database Compose file uses `sslmode=disable` only because that connection never leaves the private Docker network — do not copy that URI for an external database.

#### Frontend build, CSP, and authentication

- Build-time `VITE_API_URL` overrides the API base URL. The image default is `/api/v1` (same-origin) and the nginx config reverse-proxies that path to the backend. A **cross-origin** `VITE_API_URL` will be blocked by the frontend's `connect-src 'self'` CSP unless you also update `frontend/nginx.conf` and the backend `ALLOWED_ORIGINS`.
- Authentication uses an httpOnly, `SameSite=Lax` session cookie, so the API must be same-origin (or a same-site subdomain) for the browser to attach it.
- Useful commands (run in `frontend/`): `bun install`, `bun run dev`, `bun run typecheck`, `bun run test`, `bun run build`.

#### Rotating `TOKEN_ENCRYPTION_KEY`

Paperless-ngx API tokens are encrypted at rest with `TOKEN_ENCRYPTION_KEY`. Ciphertext is versioned: new writes use an HKDF-SHA256-derived key (`enc:v2:`) and legacy `enc:v1:` values (bare SHA-256) stay readable and are transparently re-sealed to v2 the next time a user's settings are read. To rotate the key:

1. Generate a new key (`openssl rand -hex 32`) and set it as `TOKEN_ENCRYPTION_KEY`.
2. Re-enter the Paperless API token in Settings for each affected user, which re-stores it under the new key.
3. Keep a backup of the previous key until every token has been re-entered (there is intentionally no automatic re-encryption once the old key is discarded — it can no longer decrypt existing ciphertext).

If a token cannot be decrypted after rotation, the Paperless integration surfaces an error and the token must be re-entered; no other data is affected.

### Installable app (PWA)

The web frontend is a Progressive Web App: `frontend/public/manifest.webmanifest` declares the installable shell (name, icons, standalone display) and `frontend/public/sw.js` is a hand-written service worker — no build plugin, because `vite-plugin-pwa` does not yet accept Vite 8 in its peer range.

- **Shell**: the worker precaches the shell files and the bundles named by `index.html`, then the app reports the assets its page loaded (`src/lib/prefetchRoutes.ts`) and warms the two routes that must work offline — dashboard and transactions. Other pages need one online visit before they open offline. A new build keeps the previous build's cache generation, so a tab still running the old bundle can finish loading its route chunks (a `vite:preloadError` reload moves it forward once it is online).
- **Reads**: successful GETs for an allowlist of paths (`src/api/offlineCache.ts`) are kept in `localStorage`, namespaced per user, and answered from there when a request never reaches the server. Nothing else is cached: the service worker deliberately does not touch `/api/`.
- **Writes**: a transaction added while offline is queued in an outbox (`src/api/outbox.ts`) and flushed on reconnect, on the next launch, or from the offline bar. Each entry carries a client-generated `clientKey` that `POST /transactions` treats as an idempotency key, so a replay after a lost response writes exactly one row. The queue survives sign-out; the cached reads do not. Because the queue *is* the record, a create is only reported as queued once the entry is really stored — a browser that refuses the write (full or blocked storage) surfaces an error instead of a false "saved offline", and a queue at its cap refuses the new entry rather than evicting the oldest.
- **Banner**: `components/Layout/OfflineBanner.tsx` is the only place the offline layer talks to the user — it reports cached data, the pending count, and lets a rejected entry be retried or discarded.
- The production nginx config serves `/sw.js` and `/manifest.webmanifest` with `no-cache` (the hashed-asset rule would otherwise pin a worker for a year) and states its security headers once in `frontend/security-headers.conf`, `include`d by every location that sets a `Cache-Control` of its own — nginx stops inheriting `add_header` at that point, which is what used to require seven verbatim copies kept in sync by a comment. Proxied API responses carry `Cache-Control: no-store` and `Vary: Cookie` (an authenticated ledger read is never reusable by anyone else), the restore and statement-upload locations get proxy timeouts that outlast the backend's own (310s / 75s), and text/JSON is gzipped. The CSP itself needs no change: `manifest-src`/`worker-src` fall back to `default-src 'self'` and `script-src 'self'`.

### Terminal client (`tui/`)

A keyboard-driven terminal client lives in `tui/` (Go + [Bubble Tea](https://github.com/charmbracelet/bubbletea)). It is a pure client of the same REST API — no extra endpoint, no schema change — so it covers what the web frontend covers: filtered transaction search, inline create/edit/delete, multi-select bulk actions, statement and Paperless imports, CSV/JSON exports and restore, the dashboard, the money-flow graph with its timeline, the cash-flow calendar, and the recurring/loan tooling.

```bash
cd tui
go run .                                        # default API http://localhost:8080/api/v1
go run . -api https://fintrak.example.com/api/v1
```

It can also *be* the server: `-ssh` serves the TUI over SSH, one session per connection.

```bash
go run . -ssh -ssh-addr :2222 -ssh-auth password
ssh -p 2222 localhost          # username = FinTrak email, password = FinTrak password
```

With `-ssh-auth password` (the default) the SSH credentials are exchanged for an API session during authentication, so the session opens already signed in. `-ssh-auth key` instead accepts a public key listed in `-ssh-authorized-keys`; a key only opens the door, so the TUI then shows its own sign-in screen, because a public key cannot be traded for an API session. `any` accepts both. The host key (default `<config dir>/fintrak-tui/ssh_host_ed25519_key`) is generated on first start and must be kept stable — a regenerated key makes every client refuse the host.

The door is deliberately narrow: port, agent and reverse forwarding, subsystems such as `sftp`, and any other channel request are refused; sessions are capped (`-ssh-max-sessions`), authentication is throttled per remote address (`-ssh-auth-per-minute`), and idle/absolute timeouts apply. Because every session reaches the API from the door's address, it shares that address's rate-limit bucket (the backend keys auth throttling per-IP *and* per-account); the local per-address limiter stops one client from spending that shared budget. Run `go run . -h` for every flag and its `FINTRAK_*` environment equivalent — the container image is configured entirely through them.

The terminal signs in with the account credentials for the same reason it works at all: a non-browser client cannot read the browser's httpOnly session cookie, so it captures the two tokens from the `Set-Cookie` headers instead and refreshes them on a 401.

In the dev stack the door is opt-in (it publishes a port and accepts logins):

```bash
docker compose --profile tui up -d
ssh -p 2222 localhost
```

### Agent access (MCP)

The `mcp/` module ships a second binary, `fintrak-mcp`, which serves FinTrak to a model client over the Model Context Protocol. It is a pure client of the same API and holds the same session the other clients do, so nothing has to be enabled server-side.

```bash
cd mcp
go build -o fintrak-mcp .    # or: go install .
```

Point an MCP client at it, passing the credentials through the environment (`-email`/`-password` work too, but a password on a command line is visible to other processes):

```json
{
  "mcpServers": {
    "fintrak": {
      "command": "fintrak-mcp",
      "env": {
        "FINTRAK_API_URL": "http://localhost:8080/api/v1",
        "FINTRAK_EMAIL": "you@example.com",
        "FINTRAK_PASSWORD": "…"
      }
    }
  }
}
```

**Every tool is read-only.** The server can read the ledger (`list_accounts`, `list_categories`, `list_groups`, `list_payees`, `list_tags`, `list_transactions` with the app's full filter grammar, `list_billing_cycles`, `get_loan_schedule`, `list_links`, `list_recurring`, `list_recurring_terms`, `list_recurring_transactions`) and the derived aggregates (`get_dashboard_summary`, `get_money_flow`, `get_money_flow_timeline`, `get_cash_flow_calendar`), the rules (`list_rules`) and the Paperless document list (`list_paperless_documents`) — and it can run the API's own read-only preview twins (`validate_transactions`, `preview_rule`, `get_transfer_suggestions`, `get_cashback_suggestions`, `get_recurring_suggestions`, `forecast_recurring`, `get_loan_payoff`, `get_link_cycles`), which is how a model proposes something the app then validates. It exposes no write operation and can neither edit a ledger record nor delete one, and every request is checked against the tool list by a transport guard, so a call outside it fails locally instead of reaching the ledger. The one qualification is the handful of reads that materialize derived billing cycles, described below.

The read-only claim is not a convention but a checked property. Each tool declares the API operation it performs; `go test ./...` in `mcp/` verifies every declared operation exists in `backend/openapi.yaml` and is either a `GET` or one of the two documented preview `POST`s, that every read-only operation is either exposed as a tool or explicitly exempted with a reason, that calling every tool really hits the route it declares, and that a tool whose route is not a pure read declares what it writes. `mcp/internal/readonly` refuses anything else at the transport, including the write endpoints of the shared client.

Six `GET`s in this API are not pure reads: the billing-cycle handlers materialize a credit-card account's statement periods on read (and back-fill transactions' cycle assignment), and `GET /paperless/documents` re-seals a legacy-format Paperless token on the user's row. So `list_billing_cycles`, `list_transactions` (with an `accountId`), `get_dashboard_summary`, `get_money_flow_timeline`, `get_cash_flow_calendar` and `list_paperless_documents` can write derived rows. All six are listed in `readonly.SideEffectingGETs` with that reason, each of those tools carries a `SideEffect` that is appended to the description the model reads, and the audit fails if either side of that pair changes without the other. Each one is also advertised with `readOnlyHint: false` — the hint is derived from the declaration rather than hardcoded, so a client that auto-approves read-only calls cannot be misled by it. The two list endpoints that cannot be paged server-side (`list_links`, `list_recurring_transactions`) cap their response instead of streaming the whole history into the model's context, and say so (with the cap and a `truncated` flag) in their result. A model's suggestions therefore have to be confirmed in the app — which is exactly the app's own ethos: it never auto-categorizes, auto-links or auto-creates either.

The server signs in lazily with its first tool call and refreshes the session from then on, so a backend restart or a long-lived session needs no attention; a failed sign-in is reported as a tool error rather than killing the process. Since stdout carries the protocol, logs go to stderr (`-log-file` to redirect them). There is no scoped read-only token yet (backlog idea #56), which is why the server takes the account credentials; the propose-and-apply half of idea #59 is deliberately not implemented.

---

## 📂 Project Structure

```text
.
├── backend              # Go API server
│   ├── auth             # JWT minting/validation, session cookies, middleware
│   ├── config           # Configuration loader (godotenv)
│   ├── db               # Connection, migrations, every-boot seeders
│   │   └── migrations   # NNNNNN_*.up.sql / .down.sql
│   ├── handlers         # HTTP handlers, one file per resource (+ tests)
│   ├── internal         # money, validation, logger, ratelimit, crypto
│   ├── models           # Every model/type (models.go)
│   ├── cmd/covercheck   # Coverage-floor enforcement used by CI
│   ├── openapi.yaml     # Machine-readable API spec (route-parity tested)
│   └── main.go          # Entry point + setupRouter (all route registration)
├── frontend             # React + TypeScript + Vite SPA (bun)
│   ├── src
│   │   ├── api          # The single API client (client.ts)
│   │   ├── components   # PascalCase feature dirs (<Feature>/<Feature>.tsx)
│   │   │   └── ui       # shadcn/ui primitives
│   │   ├── context      # Auth, Theme, Settings, DomainData providers
│   │   ├── lib          # Shared helpers (cn, tables, categories, intents)
│   │   ├── utils        # Formatters and small display helpers
│   │   └── types.ts     # API models
│   └── Dockerfile       # Build + nginx runtime (reverse-proxies /api/v1)
├── client               # Shared Go REST client (own module, used by tui and mcp)
│   └── api              # The one hand-written API client
├── mcp                  # MCP server (own module): read-only tools for model clients
│   └── internal
│       ├── mcpserver    # Tool registry, handlers and the lazy sign-in
│       └── readonly     # Transport guard behind the read-only guarantee
├── statement_parser     # Standalone Python PDF statement parser (own module)
├── tui                  # Go terminal client (Bubble Tea), local or over SSH
├── scripts              # release.sh / release.ps1 guardrails
├── .github/workflows    # CI: tests, coverage upload, validate gate, GHCR publish
├── Makefile             # dev / test / vet / build / openapi / release targets
├── codecov.yml          # Per-flag coverage targets (backend 85, frontend 78, parser 90, client 85, tui 18, mcp 80)
├── AGENTS.md            # Repo conventions for contributors and coding agents
├── FLOWCHART.md         # Architecture, transaction lifecycle, ER diagram
├── IDEAS.md             # Feature backlog
├── LICENSE              # AGPL-3.0
├── .env.example         # Template for environment variables
├── docker-compose.yml            # Local development orchestration
├── docker-compose.prod.yml       # Production (with bundled database)
└── docker-compose.prod-no-db.yml # Production (external database)
```

---

## 📡 API Overview

The backend exposes a RESTful API under `/api/v1`. A machine-readable OpenAPI spec lives in [`backend/openapi.yaml`](backend/openapi.yaml) and is also served at `GET /api/v1/openapi.yaml`. A backend test fails whenever a registered route is missing from the spec (or the spec advertises a route that no longer exists), so the contract cannot silently drift; run `make openapi-check` to verify locally. Every operation the spec defines is described below.

- `POST /auth/register`: Create an account (sets the session cookie). Body: `{ email, password, setupToken? }`. Emails are stored lowercase. Regular registrations get the `user` role. An email listed in `ADMIN_EMAILS` is a reserved identity: registering it grants `admin` only when `setupToken` matches the `ADMIN_SETUP_TOKEN` environment variable, otherwise the request is refused — an unverified registrant can neither self-promote nor squat the address. The refusal is deliberately indistinguishable from a taken address (the same 409 `an account with this email already exists` body the duplicate branch returns, with the real reason only in the server log), so an unauthenticated caller cannot probe which addresses the deployment has reserved or whether `ADMIN_SETUP_TOKEN` is configured. This is the **only** path to the `admin` role: existing accounts are never promoted automatically, so an operator must grant it deliberately (self-register the admin email with the setup token, or update `users.role` directly).
- `POST /auth/login`: Sign in (sets the session cookie). Body: `{ email, password }`. Throttled per client IP; failed credential checks also draw on a per-identity budget, but a correct password is never refused by it.
- `POST /auth/logout`: Revoke the session (the refresh token's whole rotation family) and clear both cookies.
- `GET /auth/me`: Return the authenticated user for the current session cookie. The frontend calls it on mount to rehydrate auth state.
- `GET /accounts`: List all financial accounts, newest first, each with the running balance implied by its account type (loan accounts show the total repaid on the loan). Accounts carry an `isDefault` flag and an optional `billingDay` (1-31, clamped to the month length; `null` when unset); the single default account (per user) pre-fills the account filter on the Dashboard and the Transactions list.
- `GET /accounts/:id/billing-cycles`: List the billing cycles for an account with a `billingDay` set, auto-generating any missing cycles first (one per month, ending on the account's `billingDay`; changing the day regenerates the cycles). Each cycle carries `{ id, accountId, startDate, endDate, label, totalOutstanding, transactionCount }` where `totalOutstanding` is the account's running balance at the cycle's end date — all debits minus all credits (purchases, payments, refunds, cashbacks) posted up to that date. Accounts without a billing day return an empty list — cycles are never generated for them.
- `GET /accounts/:id/export`: Stream one account's transactions as a CSV attachment (`Date, Description, Amount, Type, Tags, Notes`), scoped to the authenticated user.
- `POST /accounts`: Create an account. Body: `{ name, accountTypeId, bank?, currency? (default INR), color?, billingDay? (1-31), isDefault? }`. Marking it the default clears the flag on the user's other accounts, and the account-linked payee (the counterpart used when linking transfers) is created or renamed to match the account name. Returns `201` with the account.
- `PUT /accounts/:id`: Edit an account. Omitted fields keep their current value, `isDefault` and `closed` are pointers so they change only when sent, `billingDay: null` clears it, and the account-linked payee is renamed to match. `closed: true` freezes the account, `false` reopens it.
- `DELETE /accounts/:id`: Delete an account along with its transactions (and their links) and its account-linked payee. Returns `{ message, transactionsDeleted }` so the UI can report what was removed. A recurring series whose *every* range pointed at the account is deleted with it (its ranges cascade with the account, and a series with no range would be unusable); a series that still has a range on another account survives.
- `GET /account-types`: List the shared account types (`bank`, `credit_card`, `loan`, plus admin-created ones). Each carries `positiveTxnType`, the convention that defines how its accounts' running balance is computed.
- `POST /account-types` / `PUT /account-types/:id` / `DELETE /account-types/:id`: Admin-only. Create a custom type (`{ id, name, positiveTxnType: "credit"|"debit" }`; the id must be a lowercase slug and may not reuse a built-in id — a duplicate id is answered `409`, like a duplicate group id), edit one (empty fields keep their current value, built-in types are immutable), or delete one that no account still uses.
- `GET /export`: Download a complete, versioned JSON backup of everything the authenticated user owns — accounts, category groups and categories (including every global category the user's data references: from a transaction, a recurring series, a rule's action category *and* a rule's `filterCategoryId`), payees, billing cycles, transactions, links, loan attachments, loan schedules, loan balance transfers, loan disbursement credits, recurring series/terms/attachments, rules and non-secret settings. The Paperless API token and password hash are never included. Each record keeps its original id as an in-bundle reference.
- `POST /import`: Restore a bundle produced by `GET /export`. The whole restore runs in a single transaction and is all-or-nothing; it is refused with 409 when the authenticated user already has accounts (merging a foreign bundle into existing data is ambiguous), and a bundle carrying values the schema would reject — an unknown transaction/series `type`, recurring `frequency`, rule `matchType`/`txnType` or link `type`, a rule date that is not `YYYY-MM-DD`, or an amount beyond ±1e15 minor units — is refused with 400 naming the field, having inserted nothing. Every row is inserted with a freshly minted id and every reference (account, category, payee, billing cycle, transaction) is remapped, so a bundle is portable across users and FinTrak instances. Global category references are matched to the target instance's global categories by name; a rule whose filter category the bundle does not carry restores as "no filter" and says so in `warnings`. Returns per-resource row counts plus any `warnings` for rows skipped due to a missing reference (categories moved into the `Imported` fallback group are reported once with their count, and the list ends with `further warnings omitted after the first 100` when the cap is reached, so it is never silently truncated). A large restore needs a long request budget: the backend allows 300s, the bundled nginx 310s and the SPA/CLI clients 320s.
- `GET /transactions`: List transactions with support for search and filters. `search` is a case-insensitive substring match across the description, notes, payee name, and tags. Category filtering accepts a category id (`categoryId`), the sentinel `categoryId=uncategorized` for transactions without a category, or a category group (`groupId=<group id>`, matching every category in the group; base group slugs like `expense` and custom group ids both work). The account, category, payee and tag filters each take a comma-separated list and match a transaction satisfying any entry (`accountId=a,b`, `categoryId=c1,uncategorized`, `payeeId=p1,none`, `tags=trip,work`); a group and a category picked together are OR-ed into one category filter, and so are an account's transactions and a loan account's attached EMI payments (`loanAccountId`). When a single `accountId` is filtered, synthetic (non-persisted) summary rows are appended for any account with a `billingDay` set (regardless of account type): a `Total outstanding` row at the end of every billing cycle that has attached transactions (the account's running balance at that date — all debits minus all credits) plus a final row for the current in-progress cycle (the running balance up to the requested range end). Accounts without a billing day return only their raw transactions. Summary rows have `isSummary: true` and are interleaved by date. Each transaction also carries `billingCycleId` / `billingCycleLabel` when attached to a cycle, and `recurringSeriesId` / `recurringSeriesName` when linked to a recurring subscription. Pass `recurringId=<series id>` to list only the transactions linked to one series, or `recurring=linked|unlinked` to filter by linkage. `page` defaults to 1 and is rejected with 400 above 1,000,000 (so the offset cannot overflow); `limit` defaults to 50, and a value below 1 falls back to that default while anything above 1000 is clamped to 1000.
- `POST /transactions`: Create a single transaction manually. Body: `{ accountId, date: "YYYY-MM-DD", description, amount, type: "debit"|"credit", categoryId?, payeeId?, tags?, notes?, billingCycleId?, clientKey? }`. `clientKey` is an optional idempotency key (max 64 chars, unique per user): repeating a key the user has already used returns that transaction with `200` instead of inserting a second row, which is what lets an offline client replay a create whose response was lost. The account must belong to the authenticated user; when `categoryId` is omitted the transaction is auto-categorized from rules. Dates are accepted in the ledger's window `[1900-01-01, today+1y]` (a wider value is a `400`, not a stored row that every later read would have to cope with). For accounts with a `billingDay` set the transaction is attached to the billing cycle matching its date by default (the suggested default); pass `billingCycleId` to attach it to a specific cycle instead. Returns `{ id }`.
- `PATCH /transactions/:id` / `DELETE /transactions/:id`: Partially update or delete one transaction. Only fields present in the request change, and an explicit `null` on `categoryId` / `payeeId` / `billingCycleId` clears that column; every referenced account, category, payee or cycle must belong to the user, and a date must fall in the ledger's window `[1900-01-01, today+1y]`. Clearing the billing cycle is durable (the date-based default does not re-attach it on the next read), and moving a transaction's date or account clears its cycle the same way — a cycle belongs to one account and one period, so keeping the old one would report the transaction under a statement it is no longer part of; naming a cycle explicitly always wins. Transactions on a closed account are immutable, so a request that touches one matches nothing and is answered 404.
- `POST /transactions/import`: Import transactions in bulk. Body: `{ accountId, transactions: [{date: "YYYY-MM-DD", description, amount, type: "debit"|"credit", payeeId?}], duplicateAction?: "skip"|"keep", billingCycleId?, paperlessDocumentIds?: number[] }`. With `duplicateAction: "skip"` rows that match an existing transaction (same date, amount, type, description) or repeat in the batch are dropped atomically; the response reports `{ imported, duplicates, total }`. For accounts with a `billingDay` set, pass `billingCycleId` to attach every imported transaction to that cycle (overriding the date-based default). `paperlessDocumentIds` is the Paperless-ngx "tag on import" mechanism: when present and a `paperlessTag` label is configured, the documents are tagged asynchronously (best-effort, after the import commits) — it is supplied by the Paperless import flow, not the manual CSV import. Every row's date must fall in the ledger's window `[1900-01-01, today+1y]` (400 naming the row otherwise), and every row is stored with an empty tag array rather than SQL NULL, so a later bulk tag add always applies.
- `POST /transactions/validate`: Read-only duplicate check. Body: `{ accountId, transactions: [{date: "YYYY-MM-DD", description, amount, type: "debit"|"credit"}] }`. Returns `{ total, existingCount, missingCount, results: [{index, exists, date, description, amount, type}] }` where `exists` is true when a transaction with the same date, amount, type, and description is already stored in the account. Uses the same fingerprint matching as the import endpoint (so results agree with what `duplicateAction: "skip"` would drop) but writes nothing.
- `POST /transactions/bulk-loan`: Attach/detach transactions as EMI payments. Body: `{ transactionIds, loanAccountId? }`. With `loanAccountId` set, the transactions are attached to that Loan / EMI account (400 if the target is not a loan account, 404 if it is not found, 409 if any transaction is already linked to a loan account — detach first — or is a loan's disbursement credit, which can never also be an EMI payment); each attached transaction's payee is set to the loan account's linked payee, so EMI payments read as paid to the loan. With `loanAccountId` omitted/null the transactions are detached from whatever loan account they were attached to (payees are left unchanged). One transaction can be attached to at most one loan account (a UNIQUE constraint on the attachment). Closing an account does not affect linking.
- `GET /accounts/:id/loan-schedule` / `PUT /accounts/:id/loan-schedule` / `DELETE /accounts/:id/loan-schedule`: Read, create/replace, or remove a Loan / EMI account's optional amortization schedule. `PUT` takes `{ principal, processingFee?, annualRateBps, tenureMonths, startDate, disbursalDate? }` (the rate in basis points, `950` = 9.50% p.a., `0` allowed) and answers with the generated table; `GET` answers with `schedule: null` when the loan has none. The fee is recorded for reference only — it never changes the EMI or the table, which amortize `principal` in full — and `disbursalDate` (before `startDate`) makes a first period that is not a whole month a stub: that period is charged its actual days over a 30-day month and the EMI is solved so the loan still clears in the full tenure, so the installments stay level. The EMI is quoted rounded up to the whole rupee, the way a lender quotes it, and the final installment clears the surplus. Every installment carries its principal/interest split, remaining balance, the linked EMI transaction that covers it (payments are matched in date order, the way a lender numbers installments) and whether a balance transfer recast or cancelled it; the totals include `interestPaid`, `outstandingPrincipal`, `transfers` and `settledOn`.
- `GET /accounts/:id/loan-payoff?date=YYYY-MM-DD`: Quote what settling the loan on that date costs — `{ asOf, fromDate, days, outstandingPrincipal, accruedInterest, payoff }`, where the interest runs from the last EMI *payment* date (paying an installment is what clears its interest), falling back to the disbursal date, then to the month before the first installment. The date is required so the same request always answers the same thing; 400 for a loan with no schedule or one already settled by a transfer.
- `POST /accounts/:id/loan-transfer` / `DELETE /accounts/:id/loan-transfer/:transferId`: Settle a loan at its payoff using another loan, or undo that. `POST` takes `{ toLoanAccountId, transferDate, mode?, targetAnnualRateBps?, targetTenureMonths?, targetStartDate? }` — the amount is never supplied by the caller, it is the payoff `GET /accounts/:id/loan-payoff` quotes for the same date, reported back split into `principal` and `accruedInterest`. `mode` decides what the target does with it: `recast` (default when the target has a schedule) adds it to the target's installments still due, so the target's debt grows and those installments come back `recast`; `takeover` leaves the target's own table alone and records the amount as paid out of the target's disbursement, which must cover it; `opens` (default when the target has no schedule) starts the target's schedule from the amount, with the three `target*` fields then required. The source always comes back settled, its unpaid installments `cancelled`. 400 for a source with no schedule, nothing outstanding, a closed/non-loan target, a mode the target's state does not allow, or a source already settled by a transfer — 409 on a concurrent double-transfer. Both loans' figures are derived from the recorded transfer, so `DELETE` reverts both — and removes the target's schedule too when the transfer is what opened it. It answers 409 (changing nothing) when the target's terms have since been replaced, so the undo can never delete a schedule the user rebuilt; a target schedule that is already gone does not block the undo.
- `PUT /accounts/:id/loan-disbursement` / `DELETE /accounts/:id/loan-disbursement`: Link (or unlink) the bank credit that released a loan, so its disbursement can be reconciled. Body `{ transactionId }`; the credit must be a credit transaction on one of the user's own accounts and cannot already be an EMI payment or another loan's disbursement (400 for a loan with no schedule, a debit, or a transaction on a loan account; 409 when the transaction is already another loan's disbursement credit or already attached as an EMI payment). The loan detail reports `disbursement` as `{ sanctioned, processingFee, paidOut, net, creditTransactionId?, creditAmount?, verified, difference }`, where `net = sanctioned - processingFee - paidOut` is what should have arrived and `difference = creditAmount - net` is reported rather than rejected — a mismatch is the thing worth seeing.
- `POST /transactions/bulk-categorize` / `POST /transactions/bulk-payee` / `POST /transactions/bulk-billing-cycle` / `POST /transactions/bulk-tags` / `POST /transactions/bulk-delete`: Apply one category, payee, billing cycle, tag add/remove, or deletion to many of the user's transactions at once. Transactions on closed accounts are skipped (immutable except linking). The same freeze applies to the global `POST /tags/rename` and to `POST /rules/apply`, so a closed account's history cannot be rewritten through a tag rename or a rule re-run.
- `GET /transactions/export`: Stream the current filter as a CSV attachment (`Date, Description, Amount, Type, Tags, Notes`). It honors the identical query grammar as `GET /transactions`, so it is a flat report over whatever the caller has filtered to — unlike the JSON backup. A filter that matches more than 100,000 rows is refused with 400 (narrow the filters) instead of being truncated, so a downloaded file is never silently partial.
- `POST /statements/parse`: Upload a statement PDF (`file` multipart field, optional `password`, optional `extractor` / `date_format`) to extract transactions. The backend forwards the file to the standalone statement-parser service and returns normalized `{ transactions, summary, pageCount, transactionCount, validationErrors }` ready for preview and import. `validationErrors` carries the parser's per-page reconciliation warnings (a page's rebuilt subtotal did not match the printed one); it is empty when the statement reconciled, and a non-empty list means the extracted rows are suspect — the import preview surfaces it before anything is written. Uploads are capped at 20 MB and rejected with 413 before the multipart body is read. Concurrent forwards are capped server-side (429 when saturated).
- `GET /statements/extractors`: Proxy the parser service's extractor registry so the client can offer a dropdown of the statement parsers it can run.
- `GET /paperless/settings` / `PUT /paperless/settings`: Read/update the user's Paperless-ngx integration settings (`{ paperlessUrl, paperlessToken, paperlessTag }`), stored per-user against the `users` row. The Paperless import UI is hidden until both `paperlessUrl` and `paperlessToken` are set. `paperlessTag` is an optional label applied to successfully imported documents.
- `GET /paperless/documents`: Proxy the user's Paperless-ngx document list (`?pageSize`, default 25, max 100) so statements can be picked manually. Correspondent, document type, and tag names are resolved from Paperless's lookup endpoints (following their own pagination, so instances with many entries are not truncated) — and a lookup that cannot be read fails the whole request (`502 Paperless lookup unavailable`, `504 Paperless lookup timed out`) rather than answering 200 with the name filters silently dropped.
- `GET /paperless/documents/:id/file`: Stream a document's original file (e.g. a PDF) for in-browser preview/download.
- `POST /paperless/import`: Pull a document's original file from the user's Paperless-ngx (`POST { documentId, extractor?, password?, dateFormat? }`), feed it through the statement parser, and return the same normalized result as `/statements/parse` for preview and import. This endpoint only parses — the caller previews the result and then imports via `POST /transactions/import`; pass that document's id in `paperlessDocumentIds` to apply the `paperlessTag` label after the import commits.
- `GET /rules` / `POST /rules` / `PUT /rules/:id` / `DELETE /rules/:id`: List (highest priority first, joined with the category/payee/account/filter names), create, edit, or delete a rule. Body: `{ pattern, matchType?: "contains"|"starts_with"|"exact" (default contains), categoryId, payeeId?, priority?, addTags?, notes?, accountId?, filterCategoryId?, filterPayeeId?, minAmount?, maxAmount?, txnType?, dateFrom?, dateTo?, isLinked?, isRecurring? }` — every referenced record must be the user's own, `pattern` is capped at 500 characters, and `addTags` goes through the same tag validation as every other write edge (blank tags dropped, duplicates collapsed, 50-character cap), with the response echoing the stored list (`[]` when there are none). An update must carry `categoryId` (400 when omitted — previously that reported the rule as not found, which read as "it was deleted").
- `POST /rules/preview`: Report how many currently-uncategorized transactions a hypothetical rule (or rule edit) would categorize, writing nothing. It builds the same predicate `POST /rules/apply` uses, so the preview and a real apply agree by construction.
- `POST /rules/apply`: Manually trigger categorization rules against the user's uncategorized transactions, applying the highest-priority matching rule's category, payee, tags and note. Transactions on closed accounts are skipped, so re-running rules never rewrites a frozen account's history.
- `GET /recurring`: List the user's recurring series with joined account/category/payee names and derived `nextDueDate`, `monthlyAmount` (normalized to an estimated monthly cost), and `attachedCount`.
- `POST /recurring` / `PUT /recurring/:id` / `DELETE /recurring/:id`: Create, partially update, or delete a recurring series. Body: `{ name, type: "debit"|"credit", frequency: "daily"|"weekly"|"monthly"|"yearly", interval?, categoryId?, payeeId?, active?, notes?, ranges? }`, where `ranges` is a list of `{ startDate, endDate?, amount, accountId }` entries (or supply a single `startDate` + `accountId` + `amount`). Each entry covers `[startDate, endDate)` (an omitted end is open-ended); entries must not overlap but may leave gaps, so a subscription can be discontinued and resumed. The series' own `startDate`/`endDate` are derived from the entries. A series is a template only — no transaction is ever created from it.
- `GET /recurring/:id/terms`: List the series' date-ranged amount/account entries, oldest first. An entry is `{ id, startDate, endDate?, amount, accountId, accountName? }`; a transaction matches it only when its date falls within `[startDate, endDate)` — the **end date is exclusive**, so adjacent entries can share a boundary (`x..y`, `y..z`) and an omitted `endDate` is open-ended.
- `PUT /recurring/:id/terms`: Add a range (`{ startDate, endDate?, amount, accountId }`). Ranges of the same series must not overlap (400 otherwise); gaps are allowed.
- `PUT /recurring/:id/terms/:termId`: Edit a range's dates, amount, or account; the edited range must not overlap another entry.
- `DELETE /recurring/:id/terms/:termId`: Remove a range. Any range may be removed; deleting one leaves a gap that matches no transaction until it is replaced.
- `GET /recurring/:id/forecast`: Project the next occurrences (`?count`, default 12, max 60), each carrying the amount of the range covering its date. Each item is `{ date, amount, type, matched }` where `matched` means an attached transaction already falls on (or near) that occurrence.
- `GET /recurring/:id/suggestions`: Score existing transactions that likely satisfy the series' occurrences (near an expected date, and matching the account **and** amount of the range covering the transaction's own date), highest score first. Transactions that fall outside every range match nothing. Read-only — nothing is linked.
- `GET /recurring/:id/transactions`: List the transactions attached to a series.
- `POST /recurring/attach` / `POST /recurring/detach`: Link transactions to a series, or unlink them. Body: `{ seriesId, transactionIds }` for attach (400 if the series/txns are not found, 409 if any transaction is already linked to a series — one transaction belongs to at most one series, mirroring loan attachments) and `{ transactionIds }` for detach. Both are explicit user actions.
- `GET /dashboard/summary`: Retrieve aggregated data for charts. Supports optional `dateFrom`/`dateTo`/`accountId` filters. Pass `groupBy=billing_cycle` with an `accountId` that has a `billingDay` set to frame the entire dashboard around statement periods: the income/expense/transaction totals span the last `cycles` cycles (default 12, max 60), `billingCycleTrend` (one entry per cycle, keyed by label) replaces `monthlyTrend`, the category breakdowns cover the same window, `currentCycle` describes the in-progress period, and recent transactions are the most recent ones within that window. Date-range filters are ignored in this mode.
- `GET /dashboard/money-flow`: Build the money-flow graph for the Money Flow page — money sources (income categories) → accounts → spending categories → payees — over optional `dateFrom`/`dateTo`/`accountId` filters. Returns `{ nodes, links, totalIncome, totalExpense, linkSummary }`, where each node carries `{ id, name, kind, color, group?, total }` (`kind` is `income`/`account`/`category`/`payee`) and each link is `{ source, target, value }` keyed by node id. The income, category, and payee stages are capped at `limit` nodes (default 12, max 30) with the remainder collapsed into an `Other` node; category nodes are colored by their base group. The graph is acyclic, so `linkSummary` (`[{ type, count, total }]`, per transfer/cashback/refund/bill_payment) reports the user's linked pairs separately instead of drawing account-to-account edges.
- `GET /dashboard/money-flow/timeline`: Per-period income/expense/net for the Money Flow timeline strip — calendar months by default, or the account's statement periods with `groupBy=billing_cycle` (requires a single account with a billing day) — over the same optional `dateFrom`/`dateTo`/`accountId` filters.
- `GET /dashboard/cash-flow-calendar`: Per-day income, expense and net for the GitHub-style heatmap over an optional `dateFrom`/`dateTo`/`accountId`. When a single account is supplied the response also carries that account's billing-cycle boundaries and the synthetic summary rows the transaction list shows, so they can be overlaid on the calendar.
- `GET /links`: List the user's links, optionally filtered by `type` and/or `txnId`, newest first, with both sides joined in (date, description, amount, type, account name).
- `POST /links` / `DELETE /links/:id`: Link two of the user's transactions (`{ type: "transfer"|"cashback"|"refund"|"bill_payment", fromTxnId, toTxnId, notes? }`) or remove one. A `transfer` re-categorizes both transactions as `Transfer` and swaps their payees to the counterpart account's linked payee; deleting a transfer clears that derived category/payee again, but only on transactions no remaining link still references. Non-transfer links never touched category/payee, so deleting them leaves the user's own categorization intact.
- `POST /links/bulk` / `POST /links/bulk-delete`: The same create/delete semantics for many links at once (bulk create skips exact duplicates and applies the transfer re-categorization) and returns how many links were written.
- `GET /links/transfer-suggestions` / `GET /links/cashback-suggestions`: Read-only link candidates, each with a confidence score, `page`/`limit`-paginated (default 50, max 100) newest first. Transfers are debit/credit pairs of the same amount within ±3 days across different accounts that are not already linked; cashbacks pair a reward/refund credit with up to three prior debits on the same account (within 90 days) as the likely originating purchase.
- `GET /links/cycles`: Circular-money report for an optional `dateFrom`/`dateTo`/`accountId` window. Returns the account-to-account cycles the money-flow Sankey has to net away (kind `reciprocal`) or break (kind `cycle`) to stay acyclic — with the participants in flow order, each leg's gross flow and link-type breakdown, and `net`, the amount that actually circulates the loop — plus `oneSidedFlows`: directed account flows whose counterpart link is missing (a one-way bill payment looks exactly like a half-entered transfer).
- `GET /groups`: List the category groups visible to the user — the four immutable base groups (`income`, `expense`, `transfer`, `cashback`) plus the user's own custom groups.
- `POST /groups`: Create a user-owned custom group. Body: `{ id, name, icon?, color? }`.
- `PUT /groups/:id` / `DELETE /groups/:id`: Rename/restyle or delete a user's own custom group. Base/global groups are immutable, and a group that still has categories cannot be deleted.
- `GET /categories`: List the user's categories plus global (admin-created) ones, in group order.
- `POST /categories`: Create a user-owned category in a group they may use (a base/global group or one of their own). Body: `{ name, icon?, color?, groupId }`.
- `PUT /categories/:id`: Edit a user's own category (name, icon, color, group).
- `DELETE /categories/:id`: Delete a user's category. In the same transaction its transactions are uncategorized (`category_id` cleared to NULL) and any rules pointing at it are removed. Returns `{ clearedTransactions, deletedRules }`.
- `GET /payees` / `POST /payees` / `PUT /payees/:id` / `DELETE /payees/:id`: List (alphabetically), create, rename/re-link, or delete tracked payees. Body: `{ name, accountId? }`; a referenced account must be the user's (400), a duplicate name is rejected (`409 a payee with this name already exists`) and so is re-linking an account that already has a linked payee (`409 this account already has a linked payee` — every account gets one automatically, so the two clashes are reported separately).
- `GET /tags`: The user's tag vocabulary as `{ data: [{ name, count }] }` — every distinct tag with the number of transactions carrying it, most-used first. Tags are derived from `transactions.tags`; there is no tag table.
- `POST /tags/rename`: Rewrite one tag to another across every transaction (`{ from, to }`), collapsing any duplicates the rename creates. Transactions on closed accounts are skipped. A blank (or whitespace-only) `from`/`to` is answered `400` rather than silently doing nothing, and a tag longer than the 50-character cap is rejected like every other tag write edge.
- `GET /admin/catalog`: The shared global catalog for the admin console — the global groups and global categories, each with the usage counts an admin needs before editing or retiring it (categories per group, transactions per category). Admin-only.
- `POST /admin/groups` / `POST /admin/categories` / `PUT /admin/categories/:id` / `DELETE /admin/categories/:id`: Admin-only management of global groups and global categories shared by every user (requires the `admin` role).

All endpoints require authentication except the public ones: `/health`, `/openapi.yaml`, and the auth endpoints (`/auth/register`, `/auth/login`, `/auth/refresh`, `/auth/logout`). The login/register endpoints deliver an httpOnly, `SameSite=Lax` access-token cookie (`fintrak_token`, 15 minutes) plus a long-lived refresh-token cookie (`fintrak_refresh`, 30 days, scoped to `/api/v1/auth`). The browser sends both automatically, so neither token is exposed to JavaScript. A bearer `Authorization: Bearer <access-token>` header is still accepted (used by internal tooling/tests). Set the signing secret via the `JWT_SECRET` environment variable. Because a `SameSite=Lax` cookie is attached to a cross-site top-level navigation, the handful of authenticated `GET` routes that write (the billing-cycle generation and Paperless re-seal paths) additionally refuse a request that declares `Sec-Fetch-Site: cross-site`; same-origin requests and non-browser clients (which send no `Sec-Fetch-*` header) are unaffected.

When the short-lived access token expires, the SPA calls `POST /auth/refresh`, which exchanges the refresh cookie for a new access token and retries the original request transparently, so an active user is not kicked out every 15 minutes. Sessions are recorded server-side (migration `000012_refresh_tokens`): only the SHA-256 of each refresh token is stored, the tokens of one session form a rotation family, and every successful refresh rotates the cookie (revoking the presented token and storing its successor). Refreshing never extends the session's absolute deadline: the refresh token expires 30 days after login, after which the user must sign in again. Presenting a token that was already rotated is treated as **reuse** — the only holder of a replaced value is someone who copied it — and revokes the whole family, so a leaked cookie is not silently re-armed. `POST /auth/logout` revokes the family as well as clearing the browser's cookies, so signing out really ends the session.

---

## 📝 License

This project is licensed under the GNU Affero General Public License v3.0
