# 📊 FinTrak System Flowchart

This document visualizes the core architecture and transaction lifecycle of the FinTrak application.

## 🏗️ System Architecture

```mermaid
graph TD
    User([User]) <--> Frontend[React Frontend]
    Frontend <--> API[Go API Gateway]
    API <--> PostgreSQL[(PostgreSQL DB)]
    API <--> Parser[Statement Parser Service]

    subgraph "Frontend Components"
        Dashboard[Dashboard / Recharts]
        Transactions[Transactions Table]
        Import[Import / CSV + PDF Statement]
    end

    subgraph "Backend Modules"
        Handlers[Gin Handlers]
        RulesEngine[Rules Engine]
        LinkingService[Linking Service]
    end

    subgraph "Standalone Services"
        Parser[Python Statement Parser]
    end

    Frontend --- Dashboard
    Frontend --- Transactions
    Frontend --- Import

    API --- Handlers
    Handlers --- RulesEngine
    Handlers --- LinkingService
    Handlers -. forwards PDFs via HTTP .-> Parser
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
    ACCOUNT ||--o{ TRANSACTION : "belongs to"
    ACCOUNT ||--o{ BILLING_CYCLE : "has"
    BILLING_CYCLE ||--o{ TRANSACTION : "groups"
    CATEGORY ||--o{ TRANSACTION : "categorizes"
    PAYEE ||--o{ TRANSACTION : "associated with"
    TRANSACTION ||--o{ LINK : "linked as source"
    TRANSACTION ||--o{ LINK : "linked as target"
    RULE ||--o{ CATEGORY : "assigns"
```
