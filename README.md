# 🚀 FinTrak

[![CI](https://github.com/suyash01/FinTrak/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/suyash01/FinTrak/actions/workflows/docker-publish.yml)
[![Codecov](https://img.shields.io/codecov/c/github/suyash01/FinTrak?branch=master&logo=codecov)](https://codecov.io/gh/suyash01/FinTrak)
[![Backend coverage](https://img.shields.io/codecov/c/github/suyash01/FinTrak?branch=master&flag=backend&label=backend&logo=codecov)](https://codecov.io/gh/suyash01/FinTrak)
[![Frontend coverage](https://img.shields.io/codecov/c/github/suyash01/FinTrak?branch=master&flag=frontend&label=frontend&logo=codecov)](https://codecov.io/gh/suyash01/FinTrak)
[![Parser coverage](https://img.shields.io/codecov/c/github/suyash01/FinTrak?branch=master&flag=parser&label=parser&logo=codecov)](https://codecov.io/gh/suyash01/FinTrak)

**FinTrak** is a powerful, modern, and high-performance personal finance tracking application built with a **Go** backend and a **React + TypeScript** (Vite, bun) frontend. It simplifies transaction management, categorizes expenses using smart rules, and provides insightful dashboard visualizations to help you stay on top of your finances.

---

## ✨ Features

- **Dashboard & Analytics**: Get a clear overview of your financial health with income vs. expense summaries and category-wise breakdowns using **Recharts**.
- **Transaction Management**:
  - **CSV Import**: Seamlessly import your bank statements (powered by PapaParse).
  - **PDF Statement Import**: Upload a bank/credit-card statement PDF and preview the extracted transactions before importing (powered by a standalone parser service).
  - **Advanced Filtering**: Search and filter transactions by date, amount, account, category, payee, type, or linked state. Free-text search spans the description, notes, payee name, and tags.
  - **Compact Layout**: High-density view for managing large volumes of transactions.
- **Backup & Restore**: Export everything you own as a single JSON bundle, and restore it into a fresh account — portable across FinTrak instances.
- **Account Synchronization**: Track multiple bank accounts, credit cards, and wallets.
- **Category & Payee Management**: Organize your spending with grouped categories and tracked payees.
- **Smart Rules Engine**: Automate categorization by creating rules based on transaction descriptions or payees.
- **Transaction Linking**:
  - **Transfers**: Link matching transactions between your own accounts to avoid double-counting.
  - **Cashback & Refunds**: Link refunds or cashback to their original purchases.
  - **Bill Payments**: Link a payment to its counterpart transaction (e.g. credit-card bill paid from a bank account).
  - Link types are chosen manually for any pair of transactions, whether on the same account or different accounts.
  - **Circular Money**: The Money Flow page reports the account-to-account cycles the Sankey has to net
    away or break to stay acyclic, plus the one-directional flows with no counterpart link — the
    diagnostics for spotting money bouncing between accounts or a half-entered transfer.
- **Loan / EMI Accounts**: Track loans with no transactions of their own — EMI payments stay on your bank/credit-card accounts and are attached to the loan via a bulk "Link to Loan" action. One transaction can be attached to at most one loan account. Loan accounts show the total repaid, and each loan can carry an optional **amortization schedule** (principal, annual rate, tenure, first installment date) that generates the EMI table — every installment split into principal and interest, with the paid ones matched to the attached EMI payments, plus interest paid and outstanding principal.
- **Recurring & Subscriptions**: Define repeating charges and income (rent, salary, subscriptions) with a frequency/interval. FinTrak forecasts the upcoming occurrences and suggests matching transactions, which you confirm with an explicit link. A series is defined by a list of account/amount entries, each with its own start and optional end date (a price rise, card switch, or a discontinuation-then-resume is a new entry rather than a rewrite); entries must not overlap, gaps are allowed, and the subscription's overall period is derived from them. Matching uses the entry whose date range covers each transaction. Linked transactions carry a recurring badge on the Transactions page, where a bulk "Link to subscription" action can attach or detach them. A dashboard card summarizes the monthly recurring totals and the next upcoming charges. Repeating series are **never** created or linked automatically.
- **Close Accounts**: Mark an account closed and its transactions become immutable — no manual add/edit/remove, no bulk action (categorize, payee, billing cycle, tags, delete), no tag rename and no rule re-run can rewrite them; linking stays possible.
- **Bulk Operations**: Categorize, update payees, delete, or link multiple transactions to a loan or a recurring subscription at once.

---

## 🛠️ Tech Stack

### Backend

- **Language**: Go 1.27
- **Framework**: [Gin Gonic](https://gin-gonic.com/)
- **Database**: PostgreSQL with [pgx](https://github.com/jackc/pgx)
- **Migrations**: [golang-migrate](https://github.com/golang-migrate/migrate)
- **Configuration**: godotenv

### Frontend

- **Library**: React 19 + TypeScript (Vite, bun)
- **Styling**: **Tailwind CSS 4**
- **Icons**: Lucide React
- **Charts**: Recharts
- **Routing**: React Router 7
- **Virtualization**: TanStack Virtual (for smooth scrolling in long lists)

---

## 🚀 Getting Started

### Prerequisites

- [Docker](https://www.docker.com/) & [Docker Compose](https://docs.docker.com/compose/)

### Quick Start with Docker

The easiest way to get FinTrak running is using Docker Compose:

1.  **Clone the repository**:
    ```bash
    git clone https://github.com/your-username/fintrak.git
    cd fintrak
    ```
2.  **Start the services**:
    ```bash
    docker compose up -d
    ```
3.  **Access the application**:
    - **Frontend**: [http://localhost:3000](http://localhost:3000)
    - **API Server**: [http://localhost:8080/api/v1](http://localhost:8080/api/v1)
    - **Database Admin (Adminer)**: [http://localhost:8081](http://localhost:8081)

### Production Deployment

Copy `.env.example` to `.env`, set the values, then:

```bash
# With a bundled database (PostgreSQL)
docker compose -f docker-compose.prod.yml up -d

# Using an existing/external database
docker compose -f docker-compose.prod-no-db.yml up -d
```

`APP_ENV=production` is set for you, so the backend refuses to start unless `JWT_SECRET` and `TOKEN_ENCRYPTION_KEY` are both set. `ADMIN_EMAILS`, `ADMIN_SETUP_TOKEN`, `LOG_LEVEL`, `LOG_BODY_LIMIT`, and `TRUSTED_PROXIES` are optional (log level defaults to `info` in production; body logging is off unless `LOG_BODY_LIMIT` is positive). `IMAGE_REPO` and `IMAGE_TAG` are required too: pin `IMAGE_TAG` to a released version (e.g. `v1.2.3`), never `latest`, so redeploys and rollbacks are deterministic. See `.env.example` for the full list.

The backend runs schema migrations on startup, and the frontend reverse-proxies `/api/v1` to the backend.

#### TLS termination (required)

Both Compose files serve **plain HTTP** on their published ports (`frontend` 3000, `backend` 8080) and are designed to sit behind a TLS-terminating reverse proxy or ingress. With `COOKIE_SECURE=true` (the production default) browsers only send the httpOnly session cookie over HTTPS, and `Strict-Transport-Security` is ignored over HTTP — so exposing the stack directly over HTTP is not a supported deployment. The supported topology is:

```
browser --HTTPS--> TLS terminator (nginx/Caddy/Traefik/cloud LB) --HTTP--> frontend:3000 --HTTP--> backend:8080
```

Requirements for the terminator:

- Terminate TLS and forward to the frontend on port 3000 (the frontend proxies `/api/v1` to the backend itself).
- Preserve `Host` and set `X-Forwarded-Proto: https`.
- Emit `Strict-Transport-Security` at the terminator. The frontend nginx also sends an HSTS header, but it is only seen by the TLS terminator (over the internal HTTP hop), so the browser-facing HSTS policy must be set where TLS terminates.
- Keep `COOKIE_SECURE=true`. Only set it to `false` for isolated local development over HTTP; never in production.
- Set `TRUSTED_PROXIES` on the backend to the proxy network/CIDR (comma-separated) so `X-Forwarded-For` / `X-Real-IP` are believed and per-IP rate limiting sees real client addresses. Leave it empty when the backend is exposed directly: with no trusted proxies configured, forwarded headers are ignored and cannot be spoofed.

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

---

## 📂 Project Structure

```text
.
├── backend          # Go API Server
│   ├── config       # Configuration Loader
│   ├── db           # Database Connection & Migrations
│   ├── handlers     # API Request Handlers
│   ├── models       # Database Schemas & Types
│   └── main.go      # Application Entry point
├── frontend         # React + TypeScript + Vite Application (bun)
│   ├── src
│   │   ├── api      # API Client Calls
│   │   ├── components # UI Components
│   │   ├── context  # State Management (Settings, etc.)
│   │   └── utils    # Helper Functions
│   └── index.html
├── .env.example       # Template for environment variables
├── docker-compose.yml # Local development orchestration
├── docker-compose.prod.yml     # Production (with bundled database)
└── docker-compose.prod-no-db.yml # Production (external database)
```

---

## 📡 API Overview

The backend exposes a RESTful API under `/api/v1`. A machine-readable OpenAPI spec lives in [`backend/openapi.yaml`](backend/openapi.yaml) and is also served at `GET /api/v1/openapi.yaml`. A backend test fails whenever a registered route is missing from the spec (or the spec advertises a route that no longer exists), so the contract cannot silently drift; run `make openapi-check` to verify locally.

- `POST /auth/register`: Create an account (sets the session cookie). Body: `{ email, password, setupToken? }`. Emails are stored lowercase. Regular registrations get the `user` role. An email listed in `ADMIN_EMAILS` is a reserved identity: registering it grants `admin` only when `setupToken` matches the `ADMIN_SETUP_TOKEN` environment variable, otherwise the request is refused (403) — an unverified registrant can neither self-promote nor squat the address. This is the **only** path to the `admin` role: existing accounts are never promoted automatically, so an operator must grant it deliberately (self-register the admin email with the setup token, or update `users.role` directly).
- `POST /auth/login`: Sign in (sets the session cookie). Body: `{ email, password }`.
- `POST /auth/logout`: Clear the session cookie.
- `GET /auth/me`: Return the authenticated user for the current session cookie. The frontend calls it on mount to rehydrate auth state.
- `GET /accounts`: List all financial accounts. Accounts carry an `isDefault` flag and an optional `billingDay` (1-31, clamped to the month length; `null` when unset); the single default account (per user) is used to pre-fill account filters across the app (except the import screen).
- `GET /accounts/:id/billing-cycles`: List the billing cycles for an account with a `billingDay` set, auto-generating any missing cycles first (one per month, ending on the account's `billingDay`; changing the day regenerates the cycles). Each cycle carries `{ id, accountId, startDate, endDate, label, totalOutstanding, transactionCount }` where `totalOutstanding` is the account's running balance at the cycle's end date — all debits minus all credits (purchases, payments, refunds, cashbacks) posted up to that date. Accounts without a billing day return an empty list — cycles are never generated for them.
- `GET /accounts/:id/export`: Stream one account's transactions as a CSV attachment (`Date, Description, Amount, Type, Tags, Notes`), scoped to the authenticated user.
- `GET /export`: Download a complete, versioned JSON backup of everything the authenticated user owns — accounts, category groups and categories (including referenced global categories), payees, billing cycles, transactions, links, loan attachments, recurring series/terms/attachments, rules and non-secret settings. The Paperless API token and password hash are never included. Each record keeps its original id as an in-bundle reference.
- `POST /import`: Restore a bundle produced by `GET /export`. The whole restore runs in a single transaction and is all-or-nothing; it is refused with 409 when the authenticated user already has accounts (merging a foreign bundle into existing data is ambiguous). Every row is inserted with a freshly minted id and every reference (account, category, payee, billing cycle, transaction) is remapped, so a bundle is portable across users and FinTrak instances. Global category references are matched to the target instance's global categories by name. Returns per-resource row counts plus any `warnings` for rows skipped due to a missing reference.
- `GET /transactions`: List transactions with support for search and filters. `search` is a case-insensitive substring match across the description, notes, payee name, and tags. Category filtering accepts a category id (`categoryId`), the sentinel `categoryId=uncategorized` for transactions without a category, or a category group (`groupId=<group id>`, matching every category in the group; base group slugs like `expense` and custom group ids both work). When a single `accountId` is filtered, synthetic (non-persisted) summary rows are appended for any account with a `billingDay` set (regardless of account type): a `Total outstanding` row at the end of every billing cycle that has attached transactions (the account's running balance at that date — all debits minus all credits) plus a final row for the current in-progress cycle (the running balance up to the requested range end). Accounts without a billing day return only their raw transactions. Summary rows have `isSummary: true` and are interleaved by date. Each transaction also carries `billingCycleId` / `billingCycleLabel` when attached to a cycle, and `recurringSeriesId` / `recurringSeriesName` when linked to a recurring subscription. Pass `recurringId=<series id>` to list only the transactions linked to one series, or `recurring=linked|unlinked` to filter by linkage.
- `POST /transactions`: Create a single transaction manually. Body: `{ accountId, date: "YYYY-MM-DD", description, amount, type: "debit"|"credit", categoryId?, payeeId?, tags?, notes?, billingCycleId? }`. The account must belong to the authenticated user; when `categoryId` is omitted the transaction is auto-categorized from rules. For accounts with a `billingDay` set the transaction is attached to the billing cycle matching its date by default (the suggested default); pass `billingCycleId` to attach it to a specific cycle instead. Returns `{ id }`.
- `POST /transactions/import`: Import transactions in bulk. Body: `{ accountId, transactions: [{date: "YYYY-MM-DD", description, amount, type: "debit"|"credit", payeeId?}], duplicateAction?: "skip"|"keep", billingCycleId?, paperlessDocumentIds?: number[] }`. With `duplicateAction: "skip"` rows that match an existing transaction (same date, amount, type, description) or repeat in the batch are dropped atomically; the response reports `{ imported, duplicates, total }`. For accounts with a `billingDay` set, pass `billingCycleId` to attach every imported transaction to that cycle (overriding the date-based default). `paperlessDocumentIds` is the Paperless-ngx "tag on import" mechanism: when present and a `paperlessTag` label is configured, the documents are tagged asynchronously (best-effort, after the import commits) — it is supplied by the Paperless import flow, not the manual CSV import.
- `POST /transactions/validate`: Read-only duplicate check. Body: `{ accountId, transactions: [{date: "YYYY-MM-DD", description, amount, type: "debit"|"credit"}] }`. Returns `{ total, existingCount, missingCount, results: [{index, exists, date, description, amount, type}] }` where `exists` is true when a transaction with the same date, amount, type, and description is already stored in the account. Uses the same fingerprint matching as the import endpoint (so results agree with what `duplicateAction: "skip"` would drop) but writes nothing.
- `POST /transactions/bulk-loan`: Attach/detach transactions as EMI payments. Body: `{ transactionIds, loanAccountId? }`. With `loanAccountId` set, the transactions are attached to that Loan / EMI account (400 if the target is not a loan account, 404 if it is not found, 409 if any transaction is already linked to a loan account — detach first); each attached transaction's payee is set to the loan account's linked payee, so EMI payments read as paid to the loan. With `loanAccountId` omitted/null the transactions are detached from whatever loan account they were attached to (payees are left unchanged). One transaction can be attached to at most one loan account (a UNIQUE constraint on the attachment). Closing an account does not affect linking.
- `GET /accounts/{id}/loan-schedule` / `PUT /accounts/{id}/loan-schedule` / `DELETE /accounts/{id}/loan-schedule`: Read, create/replace, or remove a Loan / EMI account's optional amortization schedule. `PUT` takes `{ principal, annualRateBps, tenureMonths, startDate }` (the rate in basis points, `950` = 9.50% p.a., `0` allowed) and answers with the generated table; `GET` answers with `schedule: null` when the loan has none. Every installment carries its principal/interest split, remaining balance, and the linked EMI transaction that covers it (payments are matched in date order, the way a lender numbers installments); the totals include `interestPaid` and `outstandingPrincipal`.
- `POST /transactions/bulk-categorize` / `POST /transactions/bulk-payee` / `POST /transactions/bulk-billing-cycle` / `POST /transactions/bulk-tags` / `POST /transactions/bulk-delete`: Apply one category, payee, billing cycle, tag add/remove, or deletion to many of the user's transactions at once. Transactions on closed accounts are skipped (immutable except linking). The same freeze applies to the global `POST /tags/rename` and to `POST /rules/apply`, so a closed account's history cannot be rewritten through a tag rename or a rule re-run.
- `POST /statements/parse`: Upload a statement PDF (`file` multipart field, optional `password`, optional `extractor` / `date_format`) to extract transactions. The backend forwards the file to the standalone statement-parser service and returns normalized `{ transactions, summary, pageCount, transactionCount, validationErrors }` ready for preview and import. `validationErrors` carries the parser's per-page reconciliation warnings (a page's rebuilt subtotal did not match the printed one); it is empty when the statement reconciled, and a non-empty list means the extracted rows are suspect — the import preview surfaces it before anything is written. Uploads are capped at 20 MB and rejected with 413 before the multipart body is read. Concurrent forwards are capped server-side (429 when saturated).
- `GET /paperless/settings` / `PUT /paperless/settings`: Read/update the user's Paperless-ngx integration settings (`{ paperlessUrl, paperlessToken, paperlessTag }`), stored per-user against the `users` row. The Paperless import UI is hidden until both `paperlessUrl` and `paperlessToken` are set. `paperlessTag` is an optional label applied to successfully imported documents.
- `GET /paperless/documents`: Proxy the user's Paperless-ngx document list (`?pageSize`, default 25, max 100) so statements can be picked manually. Correspondent, document type, and tag names are resolved from Paperless's lookup endpoints.
- `GET /paperless/documents/:id/file`: Stream a document's original file (e.g. a PDF) for in-browser preview/download.
- `POST /paperless/import`: Pull a document's original file from the user's Paperless-ngx (`POST { documentId, extractor?, password?, dateFormat? }`), feed it through the statement parser, and return the same normalized result as `/statements/parse` for preview and import. This endpoint only parses — the caller previews the result and then imports via `POST /transactions/import`; pass that document's id in `paperlessDocumentIds` to apply the `paperlessTag` label after the import commits.
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
- `GET /links/cycles`: Circular-money report for an optional `dateFrom`/`dateTo`/`accountId` window. Returns the account-to-account cycles the money-flow Sankey has to net away (kind `reciprocal`) or break (kind `cycle`) to stay acyclic — with the participants in flow order, each leg's gross flow and link-type breakdown, and `net`, the amount that actually circulates the loop — plus `oneSidedFlows`: directed account flows whose counterpart link is missing (a one-way bill payment looks exactly like a half-entered transfer).
- `GET /groups`: List the category groups visible to the user — the four immutable base groups (`income`, `expense`, `transfer`, `cashback`) plus the user's own custom groups.
- `POST /groups`: Create a user-owned custom group. Body: `{ id, name, icon?, color? }`.
- `PUT /groups/:id` / `DELETE /groups/:id`: Rename/restyle or delete a user's own custom group. Base/global groups are immutable, and a group that still has categories cannot be deleted.
- `GET /categories`: List the user's categories plus global (admin-created) ones, in group order.
- `POST /categories`: Create a user-owned category in a group they may use (a base/global group or one of their own). Body: `{ name, icon?, color?, groupId }`.
- `PUT /categories/:id`: Edit a user's own category (name, icon, color, group).
- `DELETE /categories/:id`: Delete a user's category. In the same transaction its transactions are uncategorized (`category_id` cleared to NULL) and any rules pointing at it are removed. Returns `{ clearedTransactions, deletedRules }`.
- `POST /admin/groups` / `POST /admin/categories` / `PUT /admin/categories/:id` / `DELETE /admin/categories/:id`: Admin-only management of global groups and global categories shared by every user (requires the `admin` role).

All endpoints require authentication except the public ones: `/health`, `/openapi.yaml`, and the auth endpoints (`/auth/register`, `/auth/login`, `/auth/refresh`, `/auth/logout`). The login/register endpoints deliver an httpOnly, `SameSite=Lax` access-token cookie (`fintrak_token`, 15 minutes) plus a long-lived refresh-token cookie (`fintrak_refresh`, 30 days, scoped to `/api/v1/auth`). The browser sends both automatically, so neither token is exposed to JavaScript. A bearer `Authorization: Bearer <access-token>` header is still accepted (used by internal tooling/tests). Set the signing secret via the `JWT_SECRET` environment variable (a dev default is used when unset).

Sessions use a **stateless JWT refresh token**. When the short-lived access token expires, the SPA calls `POST /auth/refresh`, which exchanges the refresh cookie for a new access token and retries the original request transparently, so an active user is not kicked out every 15 minutes. Refreshing never extends the session's absolute deadline: the refresh token expires 30 days after login, after which the user must sign in again. There is no server-side session store, so revocation is bounded by the access-token lifetime (a stolen refresh token stays valid until the 30-day deadline) — the trade-off accepted in exchange for not storing sessions.

---

## 📝 License

This project is licensed under the GNU Affero General Public License v3.0
