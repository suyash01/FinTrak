# Server-Side Session Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a signed-in session a server-side row the user can see and revoke, and make an access token stop working the instant its session is revoked.

**Architecture:** A new `sessions` table keyed on the existing `refresh_tokens.family_id` (one session *is* one rotation family, so no new column or foreign key is needed). Both token types carry a `sid` claim; `RequireAuth` does one uncached indexed read against that row before the handler runs. Four routes expose the caller's own sessions for listing and revocation.

**Tech Stack:** Go 1.27, Gin, pgx/v5 + pgxmock, React 19, TypeScript, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-10-server-side-session-management-design.md` — read it; this plan argues from it and does not restate it.

## Global Constraints

- **Never edit the schema directly.** Add `backend/db/migrations/000002_sessions.up.sql` and `.down.sql`. The next migration number is `000002`.
- **Log with typed `slog` attrs** — `slog.Error("...", slog.String("error", err.Error()))`. Never the bare `slog.Error("...", "error", err)` key/value form.
- **All models live in `backend/models/models.go`.** Nothing else declares a wire type.
- **Handler filenames are named after the resource**: `backend/handlers/session.go`.
- **Every route goes in `main.go` `setupRouter`.** The session routes live under the existing `api.Use(auth.RequireAuth(...))` group.
- **Handler tests need no database**: build a `handlers.Server` over `pgxmock` via the existing `newTestServer`/`newMockServer` helpers, and call `gin.SetMode(gin.TestMode)`.
- **`backend/openapi.yaml` and `README.md` stay in lockstep with the router.** They and `client/api`'s route table must all change in the same commit, or three parity tests go red.
- **Semantic tokens only** in the frontend — `bg-card`, `border-border`, `text-muted-foreground`, `bg-destructive`. No raw palette classes. Use `cn` from `src/lib/utils.ts`.
- **Destructive actions confirm through `AlertDialog`.**
- **Coverage floors** are enforced: backend 85%, client 85%. Do not add untested exported code.

## Review Focus

Inputs and conditions the spec implies, that no task's happy path exercises, most likely to bite first.

1. **Two live sessions for one user, from the same browser.** Revoking one must leave the other usable — a `WHERE` clause that forgets `id` and matches on `user_id` alone would revoke both, and the list would still look correct.
2. **Another user's session id passed to `DELETE /auth/sessions/:id`.** Must 404 without touching a row. This is the authorization hole; the scoping predicate is the only thing standing between two users.
3. **A session with an empty `user_agent`.** Backfilled rows have one, and a client may send none. It must render as "Unknown device", not as a blank row or a crash.
4. **A session past `expires_at`** (spec §6). It must render as expired, never as active — a security panel that calls a dead session "active" is worse than one that hides it.
5. **Revoking an already-revoked session** (double-click, or a retry after a dropped response). Must be idempotent, not a 500.

Each line is pinned by a named test in the task that owns the code: 1–2 in Task 4, 3–4 in Task 7, 5 in Task 4.

---

## File Structure

| File | Responsibility |
|---|---|
| `backend/db/migrations/000002_sessions.{up,down}.sql` | The table and its backfill from `refresh_tokens`. |
| `backend/models/models.go` | `Session` — the only new wire type. |
| `backend/auth/auth.go` | `Claims.SessionID`, `Session.ID`, `NewSession`'s new parameter, `RequireAuth`'s liveness read. |
| `backend/handlers/auth.go` | Login/Register write the session row; Refresh refreshes `last_seen` and preserves `sid`; Logout revokes the session. |
| `backend/handlers/session.go` | **New.** The four routes and their SQL. |
| `backend/handlers/session_test.go` | **New.** pgxmock coverage for the four routes. |
| `backend/main.go` | Route registration; `RequireAuth` gains its pool argument. |
| `backend/integration_test.go` | The real-Postgres proof that revocation is immediate. |
| `backend/openapi.yaml`, `README.md` | The contract. |
| `client/api/endpoints_auth.go`, `client/api/types.go` | Four client methods + `Session`. |
| `client/api/spec_parity_test.go` | Four route cases. |
| `mcp/internal/mcpserver/tools_test.go` | `unexposedRoutes` entries. |
| `frontend/src/types.ts` | `Session`. |
| `frontend/src/api/client.ts` | Four API methods. |
| `frontend/src/lib/userAgent.ts` | **New.** `describeUserAgent`. |
| `frontend/src/components/Settings/SessionsManager.tsx` | **New.** The card. |
| `frontend/src/components/Settings/Settings.tsx` | One panel added. |

---

### Task 1: A session row exists and both tokens name it

**Files:**
- Create: `backend/db/migrations/000002_sessions.up.sql`, `backend/db/migrations/000002_sessions.down.sql`
- Modify: `backend/auth/auth.go`, `backend/handlers/auth.go`, `backend/integration_test.go`
- Test: `backend/auth/auth_test.go`, `backend/handlers/auth_test.go`, `backend/integration_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces:
  - `auth.Claims.SessionID uuid.UUID` — JSON claim `sid`.
  - `auth.Session.ID uuid.UUID` — the session this pair names.
  - `func NewSession(userID uuid.UUID, role, secret string, sessionID uuid.UUID) (Session, error)`
  - `func GenerateAccessToken(userID uuid.UUID, role, secret string) (Session, error)` — unchanged signature; keeps minting `sid`-less tokens (spec §4).
  - `func (*Server) startRefreshFamily(c *gin.Context, userID uuid.UUID, session auth.Session) error` — same signature; takes the family id from `session.ID` instead of calling `uuid.New()`.

- [ ] **Step 1: Write the failing tests in `backend/auth/auth_test.go`**

`TestNewSessionNamesItsSessionInBothTokens` — call `NewSession(userID, "user", secret, sid)`; `ParseToken` the access token as `TokenTypeAccess` and the refresh token as `TokenTypeRefresh`; assert both claims' `SessionID` equal `sid`.

`TestRenewalPreservesTheSessionID` — take the refresh token's claims, call `RenewRefresh`, then `RenewAccess` on the successor's claims; assert `SessionID` is unchanged on both. This is the spec §2 guarantee that a pre-migration token acquires a `sid` on first rotation.

- [ ] **Step 2: Run them and watch them fail**

Run: `cd backend && go test ./auth/ -run 'TestNewSessionNamesItsSession|TestRenewalPreserves' -v`
Expected: FAIL — `NewSession` takes 3 arguments, does not accept 4.

- [ ] **Step 3: Write the migration**

`000002_sessions.up.sql` creates the table and `sessions_user_id_idx` exactly as spec §1 gives it, then runs the backfill statement immediately after. Lead the file with a comment saying why `id` is `family_id` and why `expires_at` backfills as `MAX`. `000002_sessions.down.sql` is a single `DROP TABLE IF EXISTS sessions;` with a comment noting the application never migrates down (same convention as `000001`).

- [ ] **Step 4: Implement the claim and the minting change in `backend/auth/auth.go`**

Add `SessionID uuid.UUID \`json:"sid"\`` to `Claims` and `ID uuid.UUID` to `Session`. `NewSession` gains the `sessionID` parameter and passes it to `generateTokenAt` for both tokens. `generateTokenAt` sets the claim. `RenewAccess` and `RenewRefresh` pass `claims.SessionID` through — no signature change, since both already receive the claims.

**Leave `GenerateAccessToken` and `GenerateRefreshToken` alone.** They keep minting a zero `sid`, which is what spec §4 depends on.

- [ ] **Step 5: Run the auth package tests**

Run: `cd backend && go test ./auth/`
Expected: PASS, including the pre-existing token tests, which still pass because a zero `sid` serialises and parses unchanged.

- [ ] **Step 6: Write the failing handler tests in `backend/handlers/auth_test.go`**

`TestLoginWritesTheSessionRowBeforeTheCookies` and `TestRegisterWritesTheSessionRowBeforeTheCookies` — pgxmock, ordered: `ExpectExec("INSERT INTO sessions")` must precede `ExpectExec("INSERT INTO refresh_tokens")`. pgxmock's ordered expectations are what prove the sequence; a test that only asserts both happen cannot tell which order the handler used.

- [ ] **Step 7: Run them and watch them fail**

Run: `cd backend && go test ./handlers/ -run 'TestLoginWritesTheSessionRow' -v`
Expected: FAIL — no `INSERT INTO sessions` is issued.

- [ ] **Step 8: Implement the write in `backend/handlers/auth.go`**

In both `Login` and `Register`: mint `sessionID := uuid.New()` **before** `auth.NewSession`, pass it in, then insert the session row, then call `startRefreshFamily`. Order is the spec §2 invariant — the token is never handed to a client unless the row naming it exists, and a failure at any step returns 500 with no cookie set.

Insert with the request's user agent: `INSERT INTO sessions (id, user_id, user_agent, expires_at) VALUES ($1, $2, $3, $4)` bound to `sessionID, userID, c.Request.UserAgent(), session.ExpiresAt`.

Change `startRefreshFamily` to use `session.ID` as `family_id` instead of `uuid.New()`.

- [ ] **Step 9: Run the handler tests**

Run: `cd backend && go test ./handlers/ -run 'TestLogin|TestRegister'`
Expected: PASS.

- [ ] **Step 10: Add the integration assertion that every family has a row**

In `backend/integration_test.go` add `TestIntegrationEveryRefreshFamilyHasASessionRow`: register, log in a second time, then
`SELECT count(*) FROM refresh_tokens r LEFT JOIN sessions s ON s.id = r.family_id WHERE s.id IS NULL` — assert `0`.

Note in a comment that this does **not** cover the backfill against a pre-populated table, because the suite migrates an empty database. What it does pin is the invariant going forward: no code path may write a `refresh_tokens` row without a `sessions` row.

- [ ] **Step 11: Run it**

Run: `make test-integration`
Expected: PASS.

- [ ] **Step 12: Commit**

```bash
git add backend/db/migrations backend/auth backend/handlers/auth.go backend/handlers/auth_test.go backend/integration_test.go
git commit -m "feat(auth): record a session per login and name it in both tokens"
```

---

### Task 2: `RequireAuth` refuses a token whose session is not live

**Files:**
- Modify: `backend/auth/auth.go`, `backend/main.go`, `backend/main_test.go`
- Test: `backend/main_test.go`

**Interfaces:**
- Consumes: `auth.Claims.SessionID` (Task 1).
- Produces: `func RequireAuth(pool *pgxpool.Pool, secret string) gin.HandlerFunc` — the pool argument is new; the only production caller is `main.go:212`.

- [ ] **Step 1: Write the failing tests in `backend/main_test.go`**

Three cases, all using the existing `setupRouter` helper and a `pgxmock` pool:

`TestRequireAuthRejectsATokenWhoseSessionIsRevoked` — mint an access token that carries a `sid`, have the mock answer `ExpectQuery("SELECT 1 FROM sessions").WillReturnError(pgx.ErrNoRows)`, request a protected route, expect `401`.

`TestRequireAuthAcceptsALiveSession` — same token, mock answers one row, expect the route's success status.

`TestRequireAuthAcceptsATokenWithNoSessionClaim` — a token from `auth.GenerateAccessToken` (zero `sid`), and **no** `ExpectQuery` registered for `sessions`. If the handler queried anyway, pgxmock fails the test on the unexpected call. This is the test that pins spec §4.

- [ ] **Step 2: Run them and watch the first one fail**

Run: `cd backend && go test . -run 'TestRequireAuth' -v`
Expected: FAIL — no session query is made and the revoked token is accepted.

- [ ] **Step 3: Implement the check**

In `RequireAuth`, after `ParseToken` succeeds and before the context is populated:

```go
if claims.SessionID != uuid.Nil {
    var live int
    err := pool.QueryRow(c, "SELECT 1 FROM sessions WHERE id = $1 AND revoked_at IS NULL", claims.SessionID).Scan(&live)
    if err != nil {
        // No rows means revoked or pruned; any other error is a server fault and
        // must not read to the client as a valid session.
        validation.RespondAuthError(c, "invalid or expired token")
        return
    }
}
```

Fail closed on a database error rather than admitting the request. Comment the `uuid.Nil` guard with the spec §4 reasoning in one sentence, not a paragraph.

- [ ] **Step 4: Update the caller**

`main.go:212` becomes `api.Use(auth.RequireAuth(pool, cfg.JWTSecret))`. Use whatever name `setupRouter` already holds the pool under.

- [ ] **Step 5: Run the tests**

Run: `cd backend && go test . ./auth/`
Expected: PASS. Confirm `TestRequireAuthAcceptsATokenWithNoSessionClaim` passes without a sessions query — that is the assertion, not an absence of failure.

- [ ] **Step 6: Commit**

```bash
git add backend/auth backend/main.go backend/main_test.go
git commit -m "feat(auth): RequireAuth rejects a token whose session is revoked"
```

---

### Task 3: `last_seen` moves on refresh, and logout ends the session

**Files:**
- Modify: `backend/handlers/auth.go`
- Test: `backend/handlers/auth_test.go`

**Interfaces:**
- Consumes: `auth.Claims.SessionID` (Task 1).
- Produces: no new exported names. Later tasks rely only on the routes from Task 4.

- [ ] **Step 1: Write the failing tests**

`TestRefreshUpdatesTheSessionLastSeen` — pgxmock `ExpectExec("UPDATE sessions SET last_seen")` on the happy refresh path, with args `(sid, ...)`.

`TestRefreshRotatesTokensCarryingTheSameSessionID` — decode the `Set-Cookie` access token from the refresh response with `auth.ParseToken` and assert `SessionID` matches the presented token's.

`TestLogoutRevokesTheSessionAsWellAsTheFamily` — pgxmock expects both `UPDATE refresh_tokens SET revoked_at` and `UPDATE sessions SET revoked_at` on logout.

- [ ] **Step 2: Run and watch them fail**

Run: `cd backend && go test ./handlers/ -run 'TestRefreshUpdatesTheSessionLastSeen|TestLogoutRevokesTheSession' -v`
Expected: FAIL — neither statement is issued.

- [ ] **Step 3: Implement**

In `Refresh`, after the row is found live and the role is re-read: `UPDATE sessions SET last_seen = now() WHERE id = $1` bound to `familyID`. Use `familyID`, not `claims.SessionID` — the family resolved from the token's hash is the authority, and for a pre-migration token it is the only value available. Log the error and continue on failure; `last_seen` is display data and must never fail a refresh.

Add `revokeSession` next to the existing `revokeRefreshFamily`, and call it from both `revokeRefreshFamily` call sites (logout, and reuse detection). Reuse detection is the more important of the two: a leaked chain must stop working at the access token too, not just at the next refresh.

- [ ] **Step 4: Run**

Run: `cd backend && go test ./handlers/ -run 'TestRefresh|TestLogout'`
Expected: PASS. Existing refresh and reuse-detection tests must still pass — adding a statement means their mock expectations need the new call added in the right position.

- [ ] **Step 5: Commit**

```bash
git add backend/handlers/auth.go backend/handlers/auth_test.go
git commit -m "feat(auth): refresh records last_seen; logout revokes the session row"
```

---

### Task 4: The four routes, their contract, and the client's claim on them

One commit, because three parity tests compare the router, `openapi.yaml` and `client/api`'s route table in both directions — they cannot be satisfied by separate commits.

**Files:**
- Create: `backend/handlers/session.go`, `backend/handlers/session_test.go`
- Modify: `backend/main.go`, `backend/models/models.go`, `backend/openapi.yaml`, `README.md`, `client/api/endpoints_auth.go`, `client/api/types.go`, `client/api/spec_parity_test.go`, `mcp/internal/mcpserver/tools_test.go`
- Test: `backend/handlers/session_test.go`, `client/api/spec_parity_test.go`

**Interfaces:**
- Consumes: `auth.Claims.SessionID`, `auth.GetClaims` (Tasks 1–2).
- Produces:
  - `models.Session` — `ID uuid.UUID \`json:"id"\``, `UserAgent string \`json:"userAgent"\``, `CreatedAt time.Time \`json:"createdAt"\``, `LastSeen time.Time \`json:"lastSeen"\``, `ExpiresAt time.Time \`json:"expiresAt"\``, `Current bool \`json:"current"\``.
  - `func (*Server) ListSessions(c *gin.Context)`
  - `func (*Server) RevokeSession(c *gin.Context)`
  - `func (*Server) RevokeOtherSessions(c *gin.Context)`
  - `func (*Server) RevokeAllSessions(c *gin.Context)`
  - `api.Session` — the same five fields plus `Current`, so the TUI and MCP can render a list identically.
  - `func (c *Client) ListSessions(ctx context.Context) ([]Session, error)`
  - `func (c *Client) RevokeSession(ctx context.Context, id string) error`
  - `func (c *Client) RevokeOtherSessions(ctx context.Context) error`
  - `func (c *Client) RevokeAllSessions(ctx context.Context) error`

- [ ] **Step 1: Write the failing handler tests in `backend/handlers/session_test.go`**

Use the existing `new*TestRouter()` + `testAuthMiddleware()` helpers.

`TestListSessionsReturnsTheCallersOwnSessionsFlaggingTheCurrent` — mock the SELECT returning two rows for the caller; assert both are returned and `Current` is true only on the row whose `id` equals the token's `sid`.

`TestListSessionsDoesNotReturnAnotherUsersSessions` — assert the SELECT's SQL and args bind the user id.

`TestRevokeSessionIsScopedToTheCaller` — call with a different user's session id; expect `404`, and register **no** `ExpectExec` for the UPDATE, so an unbounded `UPDATE` fails the test. This is Review Focus #2.

`TestRevokeSessionLeavesOtherSessionsAlone` — assert the UPDATE's SQL carries `AND id = $1` and its args are `(sessionID, userID)`. This is Review Focus #1: the assertion is on the exact statement, because a `WHERE user_id = $1` alone would revoke everything and still return 200.

`TestRevokeSessionIsIdempotent` — the UPDATE reports 0 rows affected; expect `200`, not 500. This is Review Focus #5.

`TestRevokeSessionAcceptsTheCallersOwnSession` — expect `200`; the caller revoking itself is allowed (spec §6).

`TestRevokeOthersLeavesTheCallersSessionLive` — assert the statement excludes the caller's own `sid`.

`TestRevokeAllIncludesTheCallersSession` — assert no exclusion.

- [ ] **Step 2: Run them and watch them fail**

Run: `cd backend && go test ./handlers/ -run 'Session' -v`
Expected: FAIL — no routes registered, handlers undefined.

- [ ] **Step 3: Implement `models.Session` and the four handlers**

`ListSessions` — `SELECT id, user_agent, created_at, last_seen, expires_at FROM sessions WHERE user_id = $1 AND revoked_at IS NULL ORDER BY last_seen DESC`. Set `Current` by comparing against `auth.GetClaims(c).SessionID` in Go, not in SQL — the claim is the only place the caller's own session is known.

`RevokeSession` — parse the uuid, 400 on a malformed one, then `UPDATE sessions SET revoked_at = now() WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`. Always `200 {"message": ...}` — the idempotence case above forbids treating 0 rows as a failure. Also revoke the family, reusing `revokeRefreshFamily`.

`RevokeOtherSessions` / `RevokeAllSessions` — one shared helper taking `exceptCurrent bool`, issuing `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()`, plus `AND id <> $2` when the flag is set. The `expires_at > now()` term is required by spec §6: the bulk routes skip expired sessions, which are already dead and whose revocation would be noise. A single-session revoke by id does **not** carry it — an expired session is still listed, and the user may still want it gone. Fan the family revoke out over the affected families only.

Respond with `gin.H{"message": ...}`, matching `Logout`.

- [ ] **Step 4: Register the routes in `backend/main.go`**

Inside the authenticated group, next to `api.GET("/auth/me", srv.Me)`:

```go
api.GET("/auth/sessions", srv.ListSessions)
api.DELETE("/auth/sessions", srv.RevokeOtherSessions)
api.DELETE("/auth/sessions/current", srv.RevokeAllSessions)
api.DELETE("/auth/sessions/:id", srv.RevokeSession)
```

`current` is registered before `:id` deliberately; gin would otherwise bind `/auth/sessions/current` to `:id` and fail on the uuid parse.

- [ ] **Step 5: Run the handler tests**

Run: `cd backend && go test ./handlers/ -run 'Session' -v`
Expected: PASS.

- [ ] **Step 6: Add the four `routeCase` entries to `client/api/spec_parity_test.go`**

Place them in the existing `// Auth.` block, after `"me"`. Each must call the real client method, since the table's calls are compiled and a renamed method breaks the build:

- `"list sessions"`, `GET`, `/auth/sessions`, body `[{"id":"sess-1","userAgent":"UA","createdAt":"2026-01-01T00:00:00Z","lastSeen":"2026-01-02T00:00:00Z","expiresAt":"2026-01-31T00:00:00Z","current":true}]`
- `"revoke session"`, `DELETE`, `/auth/sessions/:id`, body `{"message":"ok"}`, `params: map[string]string{"id": "sess-1"}`
- `"revoke other sessions"`, `DELETE`, `/auth/sessions`, body `{"message":"ok"}`
- `"revoke all sessions"`, `DELETE`, `/auth/sessions/current`, body `{"message":"ok"}`

Add `idSess = "sess-1"` to the path-parameter constants at the top, and set `params` on the one case that needs it. The other three fall through to `defaultParams`, which is harmless because their paths carry no placeholder.

- [ ] **Step 7: Implement the four client methods in `client/api/endpoints_auth.go`**

Use the existing builders `get(path)` and `del(path)` from `client.go`. `ListSessions` decodes `[]Session`; the three revoke methods return `MessageResult`, the same type `Logout` already uses. Add `Session` to `client/api/types.go`.

- [ ] **Step 8: Run the client suite**

Run: `make test-client`
Expected: PASS — `TestRouteTableMatchesTheSpec` in both directions and `TestEveryRouteHitsItsDocumentedPath` both green.

- [ ] **Step 9: Update the contract**

Add the four paths to `backend/openapi.yaml`, matching the style of the surrounding `/auth/*` entries. Add a row to the `README.md` API overview.

- [ ] **Step 10: Verify the contract**

Run: `make openapi-check docs-check`
Expected: PASS.

- [ ] **Step 11: Add the `unexposedRoutes` entries in `mcp/internal/mcpserver/tools_test.go`**

`"GET /auth/sessions": "the user's own device list; credential-adjacent, not ledger data"`

No tool is added and `SideEffectingGETs` is not touched — `GET /auth/sessions` is a pure read (spec §5 keeps `last_seen` off the read path), so `readonly.Guard` refuses it by default and the completeness audit is satisfied by the reason.

- [ ] **Step 12: Run the MCP suite**

Run: `make test-mcp`
Expected: PASS.

- [ ] **Step 13: Commit**

```bash
git add backend/main.go backend/models/models.go backend/handlers/session.go backend/handlers/session_test.go backend/openapi.yaml README.md client/api mcp/internal/mcpserver/tools_test.go
git commit -m "feat(auth): list and revoke your own sessions"
```

---

### Task 5: Revocation is immediate, proven against real Postgres

**Files:**
- Modify: `backend/integration_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–4.
- Produces: no new names.

- [ ] **Step 1: Write the test**

`TestIntegrationRevokingASessionStopsItsAccessTokenImmediately` — two `newAPIClient(t)` values (two independent cookie jars, same `db.Pool`); register on `a`; log in again on `b` so the user has two families. `GET /api/v1/auth/sessions` on `a`, find the entry that is **not** `current`, take its id. `DELETE /api/v1/auth/sessions/{id}` on `a`, expect 200. Then `GET /api/v1/auth/me` on `b` must be **401**, and on `a` must still be **200**.

The 401 on `b` is the load-bearing assertion: it is a token that was valid a moment ago, rejected the moment its session was revoked. `pgxmock` could not prove this — it would accept a wrong column and still return 200 for everything.

- [ ] **Step 2: Write the second test**

`TestIntegrationRevokingAnotherUsersSessionChangesNothing` — a second registered user; `a` attempts `DELETE` with the other user's session id; expect 404; both users' `/auth/me` still 200.

- [ ] **Step 3: Run and watch the first one fail**

Run: `make test-integration`
Expected: the test compiles and runs; if `b` is still 200, the `RequireAuth` check from Task 2 is not reaching this path and Task 2 is incomplete.

- [ ] **Step 4: Commit**

```bash
git add backend/integration_test.go
git commit -m "test(auth): prove a revoked session stops its access token immediately"
```

---

### Task 6: The SPA's API surface

**Files:**
- Modify: `frontend/src/types.ts`, `frontend/src/api/client.ts`
- Test: `frontend/src/api/client.test.ts`

**Interfaces:**
- Consumes: the four routes (Task 4).
- Produces: `api.getSessions(): Promise<Session[]>`, `api.revokeSession(id: string): Promise<void>`, `api.revokeOtherSessions(): Promise<void>`, `api.revokeAllSessions(): Promise<void>`; `Session` in `src/types.ts` mirroring `models.Session`.

- [ ] **Step 1: Write the failing tests in `client.test.ts`**

Four cases following the file's existing fetch-mock style: each asserts the method and path sent — `GET /auth/sessions`, `DELETE /auth/sessions/sess-1`, `DELETE /auth/sessions`, `DELETE /auth/sessions/current`.

- [ ] **Step 2: Run and watch them fail**

Run: `cd frontend && bun run test src/api/client.test.ts`
Expected: FAIL — the methods do not exist.

- [ ] **Step 3: Implement**

Add `Session` to `src/types.ts` next to `User`. Add the four methods to the `api` object in `client.ts`, next to `logout`/`me` (lines 665–667), using the same `request(...)` helper. None of them take options, so none of them consult the offline cache — these are the user's own credential surface, and serving a stale device list from `localStorage` would be showing security state that may no longer be true.

- [ ] **Step 4: Run**

Run: `cd frontend && bun run test src/api/client.test.ts && bun run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/types.ts frontend/src/api/client.ts frontend/src/api/client.test.ts
git commit -m "feat(web): session list and revoke API methods"
```

---

### Task 7: The Signed-in devices card

**Files:**
- Create: `frontend/src/lib/userAgent.ts`, `frontend/src/components/Settings/SessionsManager.tsx`
- Modify: `frontend/src/components/Settings/Settings.tsx`
- Test: `frontend/src/lib/userAgent.test.ts`, `frontend/src/components/Settings/SessionsManager.test.tsx`

**Interfaces:**
- Consumes: the four client methods (Task 6), `Session` (Task 6).
- Produces: `export function describeUserAgent(ua: string): string`.

- [ ] **Step 1: Write the failing tests**

`userAgent.test.ts`: `describeUserAgent("")` → `"Unknown device"` (Review Focus #3); a Chrome-on-Windows string → `"Chrome on Windows"`; a Safari-on-iPhone string → `"Safari on iPhone"`; an unrecognised string → the raw string, unchanged.

`SessionsManager.test.tsx`:
- renders each session's parsed user agent and marks `current` as "This device"
- renders "Unknown device" for an empty `userAgent`
- renders a session past `expiresAt` as expired rather than active (Review Focus #4) — compare against a fixed `Date.now` in the test, not a live clock
- requires an `AlertDialog` confirmation before `revokeSession` is called
- `revokeOtherSessions` and `revokeAllSessions` are each behind their own confirmation

- [ ] **Step 2: Run and watch them fail**

Run: `cd frontend && bun run test src/lib/userAgent.test.ts src/components/Settings/SessionsManager.test.tsx`
Expected: FAIL — neither module exists.

- [ ] **Step 3: Implement `userAgent.ts`**

A substring table over the raw string — `iPhone`, `iPad`, `Android`, `Macintosh`, `Windows`, `Linux`, plus the browser names — returning `"<browser> on <platform>"`. Empty input returns `"Unknown device"`. Unrecognised input returns the raw string. No dependency; a user agent is not worth a parser library.

- [ ] **Step 4: Implement `SessionsManager.tsx`**

Match the `bg-card border border-border rounded-xl p-6 max-w-125` shell already used by `PaperlessSettingsManager` in `Settings.tsx`. Load on mount via `api.getSessions()`. Render one row per session: `describeUserAgent`, a relative `lastSeen`, and a "This device" marker on `current`. One `AlertDialog` per destructive action, per the repository convention. Errors go to `toast.error`.

After `revokeAllSessions` — which revokes the caller's own session — do not re-fetch: the next request will 401 and the existing handler signs the user out. Calling `getSessions` again would race the logout.

- [ ] **Step 5: Wire it into Settings**

Add one panel into `Settings.tsx` after the Data panel, with an `<h3>` and a one-line description matching the surrounding cards' voice.

- [ ] **Step 6: Run**

Run: `cd frontend && bun run test && bun run typecheck && bun run build`
Expected: PASS on all three.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/lib/userAgent.ts frontend/src/lib/userAgent.test.ts frontend/src/components/Settings/SessionsManager.tsx frontend/src/components/Settings/SessionsManager.test.tsx frontend/src/components/Settings/Settings.tsx
git commit -m "feat(web): Signed-in devices card with per-session revoke"
```

---

### Task 8: Documentation

**Files:**
- Modify: `AGENTS.md`, `FLOWCHART.md` if it carries the auth flow.

**Interfaces:**
- Consumes: all of the above.
- Produces: nothing executable.

- [ ] **Step 1: Update `AGENTS.md`**

The "Sessions are recorded server-side" bullet now understates the scheme. Rewrite it to say: sessions are rows in `sessions`, keyed on the rotation `family_id`; both token types carry `sid`; `RequireAuth` reads the session row on every request, so revoking a session takes effect immediately rather than at the next refresh; a token with no `sid` is accepted (pre-migration, bounded by the access TTL); `last_seen` is written on refresh only, which is what keeps `GET /auth/sessions` a pure read and therefore off `readonly.SideEffectingGETs`.

- [ ] **Step 2: Update `FLOWCHART.md`** if and only if it diagrams the token lifecycle.

- [ ] **Step 3: Verify**

Run: `make docs-check && make test && make test-client && make test-mcp && make openapi-check`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add AGENTS.md FLOWCHART.md
git commit -m "docs: record the session model and its revocation guarantee"
```