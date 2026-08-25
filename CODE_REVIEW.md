# FinTrak Code Review

Scope: current repository at `9945c69` (`feat: update Docker configurations...`). The findings below are ordered by impact. Line numbers refer to the reviewed working tree and should be rechecked after edits.

## 🔴 Critical Issues

- [x] **Enforce ownership for every user-owned foreign key.**
  - **References:** `backend/handlers/transaction.go:280-306, 334-409, 426-477`; `backend/handlers/category.go:37-48`; `backend/handlers/payee.go:41-52`; `backend/handlers/rule.go:48-64`; `backend/db/migrations/000001_initial_schema.up.sql:43-88`.
  - **Problem:** Mutations check the transaction/user row, but not whether the submitted `account_id`, `category_id`, `payee_id`, `billing_cycle_id`, or parent/category relationship belongs to the same user. PostgreSQL foreign keys prove existence, not authorization. A user can associate their transaction with another user's records or move it into another user's account.
  - **Suggested fix:** Validate related rows in the same transaction, or constrain the mutation with ownership predicates. Prefer composite `(id, user_id)` foreign keys and `NOT NULL` user IDs for user-owned tables:
    ```sql
    UPDATE transactions t
    SET category_id = $1
    WHERE t.id = $2 AND t.user_id = $3
      AND EXISTS (
        SELECT 1 FROM categories c
        WHERE c.id = $1 AND c.user_id = $3
      );
    ```
  - **Rationale:** This is a cross-tenant data-integrity and confidentiality issue. Add negative authorization tests for every relationship and link endpoint before merge.

- [x] **Restrict global account-type mutations.**
  - **References:** `backend/main.go:94-100`; `backend/handlers/account_type.go:38-63, 66-133`; `backend/db/migrations/000001_initial_schema.up.sql:18-26`.
  - **Problem:** Any authenticated user can create, edit, or delete the globally shared account types, including changing balance semantics for all users or removing built-in types when unused.
  - **Suggested fix:** Remove mutation routes, manage reference data through migrations, or require an explicit admin role. At minimum, reject modification/deletion of seeded IDs and validate IDs/names.
    ```go
    if id == "bank" || id == "credit_card" {
        validation.RespondError(c, "built-in account type cannot be changed", http.StatusForbidden)
        return
    }
    ```
  - **Rationale:** A normal user must not be able to alter another user's application behavior.

- [x] **Eliminate SSRF through the Paperless URL and pagination links.**
  - **References:** `backend/handlers/paperless.go:69-82, 209-272, 317-328, 379-390, 450-476`.
  - **Problem:** Users control `paperless_url`, and the server requests it with a bearer token. The server also follows Paperless-provided `next` URLs without verifying their host. This can reach localhost, private networks, cloud metadata, or another host and may disclose the token.
  - **Suggested fix:** Parse and validate the URL at save/use time; require HTTPS in production; reject loopback, link-local, private, multicast, and unspecified resolved IPs; cap redirects and require the configured origin for every request. Do not use arbitrary `next` URLs:
    ```go
    client.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
        if req.URL.Scheme != configured.Scheme || req.URL.Host != configured.Host {
            return http.ErrUseLastResponse
        }
        return nil
    }
    ```
  - **Rationale:** This is a server-side network boundary bypass and credential-exfiltration risk.

- [x] **Do not return or store Paperless API tokens as plaintext.**
  - **References:** `backend/handlers/paperless.go:41-52, 103-117`; `backend/models/models.go:127-134`; `backend/db/migrations/000001_initial_schema.up.sql:7-16`.
  - **Problem:** `GetPaperlessSettings` returns the token directly, making it available to browser code and logs/proxies. The database stores it unencrypted.
  - **Suggested fix:** Return `hasToken` plus a masked value, accept replacement only when explicitly provided, and encrypt the stored secret with an application key/KMS. Never include the secret in normal responses:
    ```go
    type PaperlessSettingsResponse struct {
        PaperlessURL string `json:"paperlessUrl"`
        HasToken bool `json:"hasToken"`
    }
    ```
  - **Rationale:** Financial integrations are high-value credentials; one XSS or database disclosure otherwise grants Paperless access.

- [x] **Bound all uploaded and proxied bodies.**
  - **References:** `backend/handlers/statement.go:78-92, 157-160, 286-287`; `backend/handlers/paperless.go:166, 233-234, 411-412, 487-508, 536-537`.
  - **Problem:** `io.ReadAll` is used on upstream responses and downloaded PDFs without a hard limit. Request-level limits do not protect the Paperless proxy/import paths.
  - **Suggested fix:** Apply `http.MaxBytesReader` to uploads and use `io.LimitReader` with a checked maximum for every upstream body. Reject oversized content before forwarding or buffering it:
    ```go
    const maxPDF = 20 << 20
    body, err := io.ReadAll(io.LimitReader(resp.Body, maxPDF+1))
    if len(body) > maxPDF { return errors.New("response too large") }
    ```
  - **Rationale:** An attacker or compromised upstream can exhaust backend memory and worker capacity.

- [x] **Make transaction creation atomic with billing-cycle assignment.**
  - **References:** `backend/handlers/transaction.go:279-307`.
  - **Problem:** The transaction insert commits before billing-cycle generation/assignment. A later failure returns an error while leaving the transaction persisted, encouraging duplicate retries.
  - **Suggested fix:** Insert, validate the account/cycle ownership, ensure cycles, and assign the cycle inside one database transaction. Return only after commit.
  - **Rationale:** Financial writes must be all-or-nothing.

## 🟡 Suggestions

- [ ] **Require the exact JWT algorithm and harden authentication.**
  - **References:** `backend/auth/auth.go:34-41, 59-76`; `backend/config/config.go:19-43`; `backend/handlers/auth.go:17-40, 60-85`.
  - **Problem:** Verification accepts any HMAC algorithm, while signing uses HS256. The default secret is predictable outside production, passwords require only six characters, and login has no rate limiting. Registration also stores case-sensitive emails while login queries case-insensitively.
  - **Suggested fix:** Require `jwt.SigningMethodHS256`, reject weak secrets in every deployment, add rate limits, normalize email before insert, and add `UNIQUE INDEX ... ON users (LOWER(email))`. Consider issuer/audience and token revocation/shorter access-token lifetimes.
  - **Rationale:** Prevent algorithm confusion, credential stuffing, ambiguous identities, and accidental insecure deployments.

- [ ] **Validate all transaction updates and bulk targets.**
  - **References:** `backend/handlers/transaction.go:320-421, 426-477`.
  - **Problem:** `UpdateTransaction` does not validate amount, date, description, account ownership, relationship ownership, or account/cycle compatibility. Bulk operations accept unbounded ID arrays and have the same foreign-key ownership flaw.
  - **Suggested fix:** Load and validate the current transaction and all target relationships in one transaction; cap bulk requests, for example at 500 or 1,000 IDs; validate dates and finite monetary values.
  - **Rationale:** Consistent validation prevents invalid financial records and resource abuse.

- [ ] **Make duplicate import detection atomic and decimal-safe.**
  - **References:** `backend/handlers/transaction.go:597-719`; `backend/db/migrations/000001_initial_schema.up.sql:64-77`.
  - **Problem:** Read-then-insert duplicate detection races under concurrent imports. Database `DECIMAL(15,2)` values are represented and fingerprinted using `float64`.
  - **Suggested fix:** Persist a normalized fingerprint, add a scoped unique index, and insert with `ON CONFLICT DO NOTHING`. Use integer minor units or a decimal type for API and business calculations.
  - **Rationale:** Avoid duplicate transactions and currency rounding errors.

- [ ] **Cap pagination and handle database errors.**
  - **References:** `backend/handlers/transaction.go:38-50, 162-175`; `backend/handlers/account.go:55-66`; `backend/handlers/account_type.go:24-35`; `backend/handlers/category.go:23-34`; `backend/handlers/payee.go:27-38`; `backend/handlers/link.go:53-70`.
  - **Problem:** `limit=0` returns an unbounded result despite the cap comment, count-query errors are ignored, and several handlers omit `rows.Err()` checks.
  - **Suggested fix:** Define a bounded maximum and a separate export path for full exports; return an error when count or iteration fails; add deterministic tie-breakers such as `ORDER BY date DESC, created_at DESC, id DESC`.
  - **Rationale:** Prevent expensive queries and misleading successful responses.

- [ ] **Improve query and server resource behavior.**
  - **References:** `backend/handlers/transaction.go:66-82, 904-927`; `backend/handlers/dashboard.go:47-187`; `backend/main.go:20-39`; `backend/db/db.go:34-69`.
  - **Problem:** Transaction listing repeats correlated link subqueries, account summaries load all transactions into memory, dashboard queries run sequentially, and the HTTP server lacks explicit timeouts/graceful shutdown. Startup database operations use fatal exits and background contexts.
  - **Suggested fix:** Pre-aggregate links, use SQL aggregates/window functions, combine or transactionally snapshot dashboard reads, configure `http.Server` read/write/idle timeouts, handle SIGTERM, and return startup errors using deadline-bound contexts.
  - **Rationale:** Improves latency, memory safety, operability, and testability as data grows.

- [ ] **Preserve user metadata when deleting links and report actual deletion counts.**
  - **References:** `backend/handlers/link.go:339-350, 419-445`.
  - **Problem:** Removing the last link clears category and payee values that may have been manually assigned. Bulk deletion reports requested IDs rather than rows actually deleted.
  - **Suggested fix:** Track link-derived fields separately or only clear values created by the link; return `RowsAffected()` as `deletedCount`.
  - **Rationale:** Destructive operations should not erase unrelated user data or misreport their result.

- [ ] **Fix frontend authentication and Paperless workflow issues.**
  - **References:** `frontend/src/api/client.ts:50-80`; `frontend/src/components/PaperlessImport/PaperlessImport.tsx:400-405, 471-475`; `frontend/src/App.tsx:1`; `frontend/src/components/Import/Import.tsx:1507-1514`.
  - **Problem:** Bearer tokens are stored in `localStorage`, exposing them to any XSS. Paperless confirmation always uses `duplicateAction: "keep"`, and the Settings button writes a hash even though the app uses `BrowserRouter`; other navigation forces a full reload.
  - **Suggested fix:** Prefer `HttpOnly; Secure; SameSite` cookies or keep short-lived access tokens in memory, offer duplicate skip/preview for Paperless, and use `useNavigate()` for internal routes.
  - **Rationale:** Protects sessions and avoids silent duplicate imports and broken navigation.

- [ ] **Bound and validate frontend imports.**
  - **References:** `frontend/src/components/Import/Import.tsx:102-144, 485-509, 512-529`; `frontend/src/components/Import/Import.tsx:434-445`; `frontend/src/components/Transactions/Transactions.tsx:264, 452-456`.
  - **Problem:** CSV/PDF validation is mostly by extension, parsing runs on the main thread without size/row limits, impossible dates are accepted, explicit date-format failures fall back to auto parsing, and duplicate checking downloads every account transaction with `limit: 0`.
  - **Suggested fix:** Enforce size/signature/row limits client and server side, use a worker for large CSVs, strictly validate calendar dates, reject explicit-format mismatches, and provide a server-side fingerprint validation endpoint. Avoid unbounded “show all” or virtualize large tables.
  - **Rationale:** Prevents browser denial of service and silent financial data corruption.

- [ ] **Improve frontend lifecycle, error, and accessibility handling.**
  - **References:** `frontend/src/components/PaperlessImport/PaperlessImport.tsx:223-247, 334-353, 365-388`; `frontend/src/context/SettingsContext.tsx:19-27`; `frontend/src/components/Transactions/EditTransactionModal.tsx:246-286`; `frontend/src/components/Import/Import.tsx:876-985`; `frontend/src/components/Accounts/Accounts.tsx:41-44`.
  - **Problem:** Object URLs are not revoked on replacement/unmount, async effects lack cancellation, malformed settings storage can crash initialization, many failures only reach `console.error`, and modals/drop zones/forms lack consistent focus, dialog, keyboard, label, and duplicate-submit handling.
  - **Suggested fix:** Standardize abortable requests and cleanup, safely parse local storage, show retryable user-facing errors, add saving guards, use a tested dialog primitive, associate labels with controls, and make drop zones keyboard-operable.
  - **Rationale:** Prevents stale state/resource leaks and makes core finance workflows usable with keyboard and assistive technology.

- [ ] **Add browser security headers and runtime response validation.**
  - **References:** `frontend/nginx.conf:1-36`; `frontend/src/api/client.ts:171-181`; `frontend/src/types.ts:1-4, 42, 118-125`; `frontend/src/components/PaperlessImport/PaperlessImport.tsx:973-977`.
  - **Problem:** Nginx does not set CSP, `nosniff`, referrer, permissions, or clickjacking protections. API JSON is cast to TypeScript types without runtime validation, and the PDF iframe is not sandboxed.
  - **Suggested fix:** Add deployment-appropriate security headers, validate API responses with schemas such as Zod, use literal unions for domain enums, and add `sandbox=""` plus safe content headers to the iframe response.
  - **Rationale:** Reduces XSS and framing exposure and turns malformed backend data into controlled errors instead of runtime crashes.

## ✅ Good Practices

- [x] SQL values are generally parameterized, and dynamic sort/update fragments are selected from internal allowlists or generated column names.
- [x] Passwords use bcrypt rather than plaintext, and protected handlers generally scope primary queries by authenticated user ID.
- [x] Statement parser calls use request contexts and a client timeout; parser/Paperless errors are mostly normalized instead of exposing upstream bodies.
- [x] Migrations are versioned and embedded, and the codebase has useful `pgxmock` coverage across handlers plus focused frontend helper/context/API tests.
- [x] API access is centralized in `frontend/src/api/client.ts`, and React text rendering avoids an obvious `dangerouslySetInnerHTML` sink.
- [x] The transaction list already uses abort handling, memoized rows, and precomputed select options, providing a good foundation for broader request cancellation and virtualization.

## Test Coverage Needed

- [ ] Cross-user ownership tests for account/category/payee/billing-cycle assignments, rules, links, and joined response data.
- [ ] Tests for exact JWT algorithm enforcement, weak production secrets, malformed claims, email normalization, and rate limiting.
- [ ] SSRF tests covering private IPs, redirects, malicious `next` links, and token non-forwarding.
- [ ] Resource-limit tests for oversized request bodies, PDFs, Paperless responses, import batches, bulk IDs, and pagination.
- [ ] Integration tests against PostgreSQL for composite ownership constraints, decimal behavior, concurrent default-account updates, and concurrent imports.
- [ ] React tests for Paperless duplicate handling, routing, upload validation, modal keyboard behavior, accessibility semantics, cancellation, and mutation failures.
