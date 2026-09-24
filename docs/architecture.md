# FinTrak Architecture

## Runtime components and trust boundaries

```text
SPA/PWA ─┐
TUI ─────┼─> Go API (Gin) ──> PostgreSQL
MCP ─────┘          │
                    ├──> statement-parser (private Flask service)
                    └──> Paperless-ngx (per-user configured instance)
```

The web frontend and its nginx proxy expose the API under `/api/v1`. The TUI and
MCP server use the shared Go client. The statement parser is intentionally
unauthenticated and must remain on a private network. Paperless is an outbound,
user-configured integration with bounded URL and request handling.

## Authentication lifecycle

Login and registration create a short-lived JWT access token and a server-
recorded refresh-session family. The browser stores the access token in an
httpOnly cookie. The backend stores only a SHA-256 refresh-token hash, rotates
the token on every refresh, and groups rotations by `family_id`. Reuse of a
rotated token revokes the family; logout revokes the family as well. Refreshes
do not extend the original session deadline.

## Transaction lifecycle

1. A client creates, imports, or updates a transaction.
2. Ownership, amount, date-window, account-state, and optional field validation
   runs.
3. Rules may assign a category/payee and apply actions.
4. Billing cycles are attached for accounts with a billing day unless the user
   explicitly detached the transaction.
5. The write commits atomically.
6. Paperless tagging, when requested, happens after the import commit.

Link and recurring suggestions are read-only. They never create or link a
record; the user confirms the action separately.

## Derived data

Some GET routes write derived data: billing-cycle handlers generate missing
cycles and back-fill transaction assignments, and Paperless configuration reads
may re-seal a legacy encrypted token. The API documents those effects. Browser
cross-site GET guards protect routes that can write or silently download
authenticated data.

## Recurring and loan domains

Recurring series are templates made from non-overlapping date ranges. They
forecast and score candidates but never create or link automatically. Loan
accounts hold no transactions; EMI payments live on other accounts and attach
to a loan. Optional amortization schedules derive principal, interest, payoff,
transfers, and disbursement records.

## Offline and extension behavior

The PWA service worker caches the application shell. The app explicitly caches
an allowlist of successful reads per user and keeps a bounded per-user outbox
for manual creates made while the API is unreachable. Each queued create carries
a stable client key; the backend treats it as an idempotency key so replaying a
lost response cannot create a duplicate transaction. A reconnect flush preserves
order and reports payloads rejected by the server.

New statement issuers are added as registered parser extractors. New API
operations require a backend route, OpenAPI update, client parity case, and
appropriate frontend/TUI/MCP documentation or integration.
