# 🚀 FinTrak

**FinTrak** is a powerful, modern, and high-performance personal finance tracking application built with a **Go** backend and a **React** (Vite) frontend. It simplifies transaction management, categorizes expenses using smart rules, and provides insightful dashboard visualizations to help you stay on top of your finances.

---

## ✨ Features

-   **Dashboard & Analytics**: Get a clear overview of your financial health with income vs. expense summaries and category-wise breakdowns using **Recharts**.
-   **Transaction Management**:
    -   **CSV Import**: Seamlessly import your bank statements (powered by PapaParse).
    -   **Advanced Filtering**: Search and filter transactions by date, amount, account, or status.
    -   **Compact Layout**: High-density view for managing large volumes of transactions.
-   **Account Synchronization**: Track multiple bank accounts, credit cards, and wallets.
-   **Category & Payee Management**: Organize your spending with hierarchical categories and tracked payees.
-   **Smart Rules Engine**: Automate categorization by creating rules based on transaction descriptions or payees.
-   **Transaction Linking**:
    -   **Transfer Detection**: Link matching transactions between accounts to avoid double-counting.
    -   **Cashback & Refunds**: Link refunds or cashback to their original purchases.
-   **Bulk Operations**: Categorize, update payees, or delete multiple transactions at once.

---

## 🛠️ Tech Stack

### Backend
-   **Language**: Go 1.26
-   **Framework**: [Gin Gonic](https://gin-gonic.com/)
-   **Database**: PostgreSQL with [pgx](https://github.com/jackc/pgx)
-   **Migrations**: [golang-migrate](https://github.com/golang-migrate/migrate)
-   **Configuration**: godotenv

### Frontend
-   **Library**: React 18+ (Vite)
-   **Styling**: **Tailwind CSS 4**
-   **Icons**: Lucide React
-   **Charts**: Recharts
-   **Routing**: React Router 7
-   **Virtualization**: TanStack Virtual (for smooth scrolling in long lists)

---

## 🚀 Getting Started

### Prerequisites
-   [Docker](https://www.docker.com/) & [Docker Compose](https://docs.docker.com/compose/)

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
    -   **Frontend**: [http://localhost:3000](http://localhost:3000)
    -   **API Server**: [http://localhost:8080/api/v1](http://localhost:8080/api/v1)
    -   **Database Admin (Adminer)**: [http://localhost:8081](http://localhost:8081)

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
├── frontend         # React + Vite Application
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

-   `POST /auth/register`: Create an account (returns a JWT).
-   `POST /auth/login`: Sign in (returns a JWT).
-   `GET /accounts`: List all financial accounts.
-   `GET /transactions`: List transactions with support for search and filters.
-   `POST /transactions/import`: Import transactions in bulk. Body: `{ accountId, transactions: [{date: "YYYY-MM-DD", description, amount, type: "debit"|"credit", payeeId?}], duplicateAction?: "skip"|"keep" }`. With `duplicateAction: "skip"` rows that match an existing transaction (same date, amount, type, description) or repeat in the batch are dropped atomically; the response reports `{ imported, duplicates, total }`.
-   `POST /rules/apply`: Manually trigger categorization rules.
-   `GET /dashboard/summary`: Retrieve aggregated data for charts.

All endpoints except `/auth/register` and `/auth/login` require an `Authorization: Bearer <token>` header. Set the signing secret via the `JWT_SECRET` environment variable (a dev default is used when unset).

---

## 📝 License

This project is licensed under the MIT License - see the LICENSE file for details.
