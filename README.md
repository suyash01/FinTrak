# 🚀 FinTrak

**FinTrak** is a powerful, modern, and high-performance personal finance tracking application built with a **Go** backend and a **React + TypeScript** (Vite, bun) frontend. It simplifies transaction management, categorizes expenses using smart rules, and provides insightful dashboard visualizations to help you stay on top of your finances.

---

## ✨ Features

- **Dashboard & Analytics**: Get a clear overview of your financial health with income vs. expense summaries and category-wise breakdowns using **Recharts**.
- **Transaction Management**:
  - **CSV Import**: Seamlessly import your bank statements (powered by PapaParse).
  - **PDF Statement Import**: Upload a bank/credit-card statement PDF and preview the extracted transactions before importing (powered by a standalone parser service).
  - **Advanced Filtering**: Search and filter transactions by date, amount, account, or status.
  - **Compact Layout**: High-density view for managing large volumes of transactions.
- **Account Synchronization**: Track multiple bank accounts, credit cards, and wallets.
- **Category & Payee Management**: Organize your spending with hierarchical categories and tracked payees.
- **Smart Rules Engine**: Automate categorization by creating rules based on transaction descriptions or payees.
- **Transaction Linking**:
  - **Transfer Detection**: Link matching transactions between accounts to avoid double-counting.
  - **Cashback & Refunds**: Link refunds or cashback to their original purchases.
- **Bulk Operations**: Categorize, update payees, or delete multiple transactions at once.

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

The backend runs schema migrations on startup, and the frontend reverse-proxies `/api/v1` to the backend.

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

The backend exposes a RESTful API under `/api/v1`:

- `POST /auth/register`: Create an account (returns a JWT).
- `POST /auth/login`: Sign in (returns a JWT).
- `GET /accounts`: List all financial accounts. Accounts carry an `isDefault` flag and an optional `billingDay` (1-31, clamped to the month length; `null` when unset); the single default account (per user) is used to pre-fill account filters across the app (except the import screen).
- `GET /accounts/:id/billing-cycles`: List the billing cycles for an account with a `billingDay` set, auto-generating any missing cycles first (one per month, ending on the account's `billingDay`; changing the day regenerates the cycles). Each cycle carries `{ id, accountId, startDate, endDate, label, totalOutstanding, transactionCount }` where `totalOutstanding` is the sum of attached debit transactions. Accounts without a billing day return an empty list — cycles are never generated for them.
- `GET /transactions`: List transactions with support for search and filters. Category filtering accepts a category id (`categoryId`), the sentinel `categoryId=uncategorized` for transactions without a category, or a category group (`groupId=<group id>`, matching every category in the group; base group slugs like `expense` and custom group ids both work). When a single `accountId` is filtered, synthetic (non-persisted) summary rows are appended for any account with a `billingDay` set (regardless of account type): a `Total outstanding` row at the end of every billing cycle that has attached transactions (the sum of debit/purchase transactions in that cycle) plus a final row for the current in-progress cycle. Accounts without a billing day return only their raw transactions. Summary rows have `isSummary: true` and are interleaved by date. Each transaction also carries `billingCycleId` / `billingCycleLabel` when attached to a cycle.
- `POST /transactions`: Create a single transaction manually. Body: `{ accountId, date: "YYYY-MM-DD", description, amount, type: "debit"|"credit", categoryId?, payeeId?, tags?, notes?, billingCycleId? }`. The account must belong to the authenticated user; when `categoryId` is omitted the transaction is auto-categorized from rules. For accounts with a `billingDay` set the transaction is attached to the billing cycle matching its date by default (the suggested default); pass `billingCycleId` to attach it to a specific cycle instead. Returns `{ id }`.
- `POST /transactions/import`: Import transactions in bulk. Body: `{ accountId, transactions: [{date: "YYYY-MM-DD", description, amount, type: "debit"|"credit", payeeId?}], duplicateAction?: "skip"|"keep", billingCycleId? }`. With `duplicateAction: "skip"` rows that match an existing transaction (same date, amount, type, description) or repeat in the batch are dropped atomically; the response reports `{ imported, duplicates, total }`. For accounts with a `billingDay` set, pass `billingCycleId` to attach every imported transaction to that cycle (overriding the date-based default).
- `POST /transactions/validate`: Read-only duplicate check. Body: `{ accountId, transactions: [{date: "YYYY-MM-DD", description, amount, type: "debit"|"credit"}] }`. Returns `{ total, existingCount, missingCount, results: [{index, exists, date, description, amount, type}] }` where `exists` is true when a transaction with the same date, amount, type, and description is already stored in the account. Uses the same fingerprint matching as the import endpoint (so results agree with what `duplicateAction: "skip"` would drop) but writes nothing.
- `POST /statements/parse`: Upload a statement PDF (`file` multipart field, optional `password`) to extract transactions. The backend forwards the file to the standalone statement-parser service and returns normalized `{ transactions, summary, pageCount, transactionCount }` ready for preview and import.
- `GET /paperless/settings` / `PUT /paperless/settings`: Read/update the user's Paperless-ngx integration settings (`{ paperlessUrl, paperlessToken, paperlessTag }`), stored per-user against the `users` row. The Paperless import UI is hidden until both `paperlessUrl` and `paperlessToken` are set. `paperlessTag` is an optional label applied to successfully imported documents.
- `GET /paperless/documents`: Proxy the user's Paperless-ngx document list (`?page_size=100`) so statements can be picked manually. Correspondent, document type, and tag names are resolved from Paperless's lookup endpoints.
- `GET /paperless/documents/:id/file`: Stream a document's original file (e.g. a PDF) for in-browser preview/download.
- `POST /paperless/import`: Pull a document's original file from the user's Paperless-ngx (`POST { documentId, extractor?, password?, dateFormat?, tagOnImport? }`), feed it through the statement parser, and return the same normalized result as `/statements/parse` for preview and import. When `tagOnImport` is true and a `paperlessTag` label is configured, the document is tagged (created if needed) in Paperless-ngx after a successful parse.
- `POST /rules/apply`: Manually trigger categorization rules.
- `GET /dashboard/summary`: Retrieve aggregated data for charts. Supports optional `dateFrom`/`dateTo`/`accountId` filters. Pass `groupBy=billing_cycle` with an `accountId` that has a `billingDay` set to frame the entire dashboard around statement periods: the income/expense/transaction totals span the last `cycles` cycles (default 12, max 60), `billingCycleTrend` (one entry per cycle, keyed by label) replaces `monthlyTrend`, the category breakdowns cover the same window, `currentCycle` describes the in-progress period, and recent transactions are the most recent ones within that window. Date-range filters are ignored in this mode.
- `GET /groups`: List the category groups visible to the user — the four immutable base groups (`income`, `expense`, `transfer`, `cashback`) plus the user's own custom groups.
- `POST /groups`: Create a user-owned custom group. Body: `{ id, name, icon?, color? }`.
- `PUT /groups/:id` / `DELETE /groups/:id`: Rename/restyle or delete a user's own custom group. Base/global groups are immutable, and a group that still has categories cannot be deleted.
- `GET /categories`: List the user's categories plus global (admin-created) ones, in group order.
- `POST /categories`: Create a user-owned category in a group they may use (a base/global group or one of their own). Body: `{ name, icon?, color?, groupId }`.
- `PUT /categories/:id`: Edit a user's own category (name, icon, color, group).
- `DELETE /categories/:id`: Delete a user's category. In the same transaction its transactions are uncategorized (`category_id` cleared to NULL) and any rules pointing at it are removed. Returns `{ clearedTransactions, deletedRules }`.
- `POST /admin/groups` / `POST /admin/categories` / `PUT /admin/categories/:id` / `DELETE /admin/categories/:id`: Admin-only management of global groups and global categories shared by every user (requires the `admin` role).

All endpoints except `/auth/register` and `/auth/login` require an `Authorization: Bearer <token>` header. Set the signing secret via the `JWT_SECRET` environment variable (a dev default is used when unset).

---

## 📝 License

This project is licensed under the GNU Affero General Public License v3.0
