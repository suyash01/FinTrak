# 📊 FinTrak System Flowchart

This document visualizes the core architecture and transaction lifecycle of the FinTrak application.

## 🏗️ System Architecture

```mermaid
graph TD
    User(["User"]) --> SPA["React SPA / PWA"]
    User --> TUI["Bubble Tea TUI<br/>(local, or over the SSH door)"]
    User --> MCP["MCP Server<br/>(read-only tools for model clients)"]

    SPA -->|"POST, GET, PATCH, PUT, DELETE"| API["Go API Gateway"]
    TUI -->|"shared Go client"| API
    MCP -->|"transport-enforced read-only guard"| API

    API --> PostgreSQL[("PostgreSQL DB")]

    subgraph "Frontend Components"
        Dashboard["Dashboard / Recharts"]
        Transactions["Transactions Table"]
        Import["Import / CSV + PDF Statement"]
    end

    subgraph "Backend Modules"
        Handlers["Gin Handlers"]
        RulesEngine["Rules Engine"]
        LinkingService["Linking Service"]
    end

    subgraph "Standalone Services"
        Parser["Python Statement Parser"]
        Paperless["Paperless-ngx<br/>(per-user configured instance)"]
    end

    SPA --> Dashboard
    SPA --> Transactions
    SPA --> Import

    API --> Handlers
    Handlers --> RulesEngine
    Handlers --> LinkingService
    Handlers -. "forwards PDFs over HTTP" .-> Parser
    Handlers -->|"outbound fetch"| Paperless
```

## 💸 Transaction Lifecycle

```mermaid
sequenceDiagram
    participant U as User
    participant F as Frontend
    participant B as Backend
    participant D as Database

    U->>F: Upload CSV/Manual Entry
    F->>B: POST /transactions/import
    B->>D: Save Raw Transactions
    B->>B: Identify Pending Rules
    B->>D: Apply Categories (Rules Engine)
    B-->>F: Success Response

    Note over F,B: Smart Linking Process

    B->>B: Detect Potential Transfers
    B->>D: Suggest Links
    F->>U: Show Linking Suggestions
    U->>F: Approve Link
    F->>B: POST /links
    B->>D: Link Transactions

    F->>B: GET /dashboard/summary
    B->>D: Aggregate Data
    B-->>F: JSON Stats
    F->>U: Render Charts
```

## 🛠️ Data Model Relationships

```mermaid
erDiagram
    USER ||--o{ REFRESH_TOKEN : "holds sessions in"
    USER ||--o{ ACCOUNT : owns
    USER ||--o{ CATEGORY : owns
    USER ||--o{ PAYEE : owns
    USER ||--o{ TRANSACTION : owns
    USER ||--o{ RULE : owns

    ACCOUNT_TYPE ||--o{ ACCOUNT : "classifies"
    ACCOUNT ||--o{ TRANSACTION : "holds"
    ACCOUNT ||--o{ BILLING_CYCLE : "closes into"
    ACCOUNT ||--o{ PAYEE : "owns, or NULL when manual"
    ACCOUNT ||--o{ LOAN_SCHEDULE : "amortizes into"
    ACCOUNT ||--o{ LOAN_TRANSFER : "is one end of"
    ACCOUNT ||--o{ LOAN_ATTACHMENT : "is paid off by"
    ACCOUNT ||--o{ LOAN_DISBURSEMENT : "is disbursed by"
    ACCOUNT ||--o{ RECURRING_SERIES_TERM : "forecasts into"

    CATEGORY_GROUP ||--o{ CATEGORY : groups
    CATEGORY ||--o{ TRANSACTION : "categorizes"
    CATEGORY ||--o{ RECURRING_SERIES : "categorizes"
    RULE }o--|| CATEGORY : "sets"

    PAYEE ||--o{ TRANSACTION : "names"
    PAYEE ||--o{ RECURRING_SERIES : "names"
    RULE }o--o| PAYEE : "sets"

    RULE }o--o| ACCOUNT : "scopes to"

    BILLING_CYCLE ||--o| TRANSACTION : "groups, or NULL when detached"

    TRANSACTION ||--o{ LINK : "is the source of"
    TRANSACTION ||--o{ LINK : "is the target of"
    TRANSACTION ||--o| LOAN_ATTACHMENT : "settles"
    TRANSACTION ||--o| LOAN_DISBURSEMENT : "records"
    TRANSACTION ||--o| RECURRING_ATTACHMENT : "matches"

    RECURRING_SERIES ||--o{ RECURRING_SERIES_TERM : "is dated by"
    RECURRING_SERIES ||--o{ RECURRING_ATTACHMENT : matches
```

Every table except `account_types` is tenant-scoped by `user_id`; the `USER`
edges above name the owning roots rather than repeat all eighteen. Two things
this diagram cannot draw: `transactions.tags` is a `TEXT[]` column, not a
table, and `transactions.amount` is integer minor units of the owning
account's `currency` — a transaction has no currency column of its own.
`recurring_series_terms.user_id` and `rules.filter_category_id` /
`filter_payee_id` are set but not constrained by a foreign key.
