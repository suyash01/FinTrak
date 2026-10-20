# Server-side session management, slice 1 — sessions, revocation, and an access token that answers to its session

Implements slices A, B and C of [#83](https://github.com/suyash01/FinTrak/issues/83).

## Problem

Half the token scheme is server-recorded and half is not, and the missing half is
the one a user ever needs to revoke.

**What already exists.** The refresh token lives in `refresh_tokens` as a SHA-256
hash, rotated into a `family_id`, with reuse detection and family revocation
(`backend/handlers/auth.go:27-38`). That half is right.

**What does not.**

1. **The access token is unrevocable for its full 15 minutes.** `auth.RequireAuth`
   (`backend/auth/auth.go:280`) checks signature, issuer, audience, expiry and
   token type, then reads the user id and role out of the claims. There is no row
   behind it and no lookup. Revoking the family it was minted from leaves the
   token working until it expires on its own.

2. **A session can only be ended by the browser holding it.** `Logout` reads
   `family_id` from the refresh cookie the request itself presents
   (`handlers/auth.go:497-514`). Nothing addresses a family by anything else. A
   user who suspects a compromise cannot end the sessions they are *not* currently
   using — there is no `GET /auth/sessions`, no revoke-by-id, no "sign out of my
   other devices". Logging out of the one browser in front of you changes nothing
   about the others.

3. **Nothing identifies a session to the user.** Even with a list, three rows all
   reading "session — active" cannot answer the only question the list exists for,
   which is *which one is not mine*.

## Proposal

A session becomes a first-class server-side row. The user can list their own live
sessions, identify them, and end any of them — including the one they are using
right now, which logs them out through machinery that already exists.

The load-bearing change is small: **the access token names its session**, and
`RequireAuth` checks that the session is still live. Everything else in this slice
is the surface that makes that check reachable by a human.

## Scope of this slice

**In:** a `sessions` table; an `sid` claim on both token types; the
`RequireAuth` liveness check; `GET /auth/sessions` plus three revoke routes; a
Settings panel; the shared Go client methods and their parity cases; the
documented contract.

**Deliberately out, stated so it is a decision and not a gap:**

- **Personal access tokens for machines.** This is #44's scope — the *scope* half
  of one idea. It will reuse this `sessions` table, and the two should land
  together or #44 first, but it is a different issue and is not smuggled in here.
- **`POST /auth/change-password`.** Independent of everything in this slice; it
  depends on being able to revoke every family, which this slice delivers, but it
  carries no dependency *back*.
- **Re-reading `role` from the database in `RequireAdmin`.** A demoted admin
  currently keeps admin for the remainder of the access token's 15 minutes. A
  real hole, a separate fix.
- **Pruning `refresh_tokens`.** The table grows without bound today
  (`000001_initial_schema.up.sql:87-91`). Revocation widens the rows it must
  retain; that is a maintenance policy, not a feature.
- **Client IP in the session list.** Deliberate. AGENTS.md records that gin
  believes the left-most `X-Forwarded-For` entry whenever the peer is trusted,
  and that an unauthenticated in-network peer (the statement parser) shares the
  network. An IP displayed inside a security UI can therefore be attacker-chosen
  text. User agent and last-seen cannot be forged into something misleading by
  choosing *what* to send. This decision is revisited if the trusted-proxy story
  changes.

---

## Design

### 1. `sessions.id` **is** the `family_id`

One session *is* one rotation family. `family_id` is already minted at login,
stable across every rotation, and never reused. So the new table keys on it
rather than adding a `session_id` column to `refresh_tokens` and a foreign key to
maintain:

```sql
CREATE TABLE sessions (
    id          UUID PRIMARY KEY,      -- IS refresh_tokens.family_id
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_agent  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
```

`expires_at` is the family's absolute deadline — the value `refresh_tokens` already
carries on every row, and `NewSession` already returns as `Session.ExpiresAt`.
Rotation is capped at the presented token's expiry, so every row in a family shares
one deadline and `MAX(expires_at)` backfills it exactly.

The backfill is one statement over `refresh_tokens`:

```sql
INSERT INTO sessions (id, user_id, created_at, last_seen, expires_at)
SELECT family_id, user_id, MIN(issued_at), MAX(issued_at), MAX(expires_at)
FROM refresh_tokens GROUP BY family_id, user_id;
```

This is what keeps sessions that predate the migration revocable. Their
`user_agent` is empty and their timestamps are derived, which the list renders
honestly as "unknown device" and the family's own activity.

### 2. Both token types carry `sid`; the session row is written before either is signed

`Claims` gains `SessionID uuid.UUID \`json:"sid"\``. `NewSession` takes the
family id as a parameter and mints tokens against it, and `Login`/`Register`
insert the `sessions` row **before** calling it. The ordering is the invariant: a
code path that signs a token naming a session that does not exist would mint a
credential that can never be listed or revoked — the same "no code path may mint a
token without a live row" rule the refresh side already holds.

`Refresh` resolves `family_id` from the presented token's hash, exactly as it
does today, and looks the session up by it. A pre-migration refresh token
therefore acquires a real `sid` on its first rotation and is fully revocable from
that point.

### 3. `RequireAuth` checks liveness with one indexed read, uncached

```sql
SELECT 1 FROM sessions WHERE id = $1 AND revoked_at IS NULL
```

after signature validation, before the handler runs. A `sid` naming a missing or
revoked session is a 401.

The check deliberately does **not** test `expires_at`. It cannot need to: an
access token lives 15 minutes and `RenewAccess` already caps it at the session's
deadline, so no access token can outlive the session it names. Expiry governs the
*list* (§6), not the *check* — adding it here would test a condition that is
unreachable and would read as though it were load-bearing.

**No cache.** A cached revocation check is the precise bug this feature exists to
prevent; the read is one indexed primary-key lookup on a single-user app's traffic,
and the immediacy *is* the feature.

### 4. A token with no `sid` is accepted, deliberately

`auth.GenerateAccessToken` is exported and mints tokens for the handler test
suite (`backend/main_test.go`, `backend/handlers/auth_test.go`) with no database
row behind them. Requiring `sid` would rewrite every mock expectation in the
repository to buy a guarantee that heals itself:

- a pre-migration **access** token has no `sid` and dies within its 15-minute TTL;
- a pre-migration **refresh** token picks up a real `sid` on first rotation (§2).

So the window in which a `sid`-less token is accepted is bounded and closes
without intervention. The alternative — rejecting `sid`-less tokens outright —
converts that bounded window into a deploy-time logout for every user, in
exchange for closing a gap that closes itself in fifteen minutes. This is the
trade, and it is stated here rather than left to be discovered.

### 5. `last_seen` is written on the refresh path only

Every authenticated request now reads a session row; writing `last_seen` there
too would turn every authenticated GET into an UPDATE. Instead the refresh
handler updates it — at most one write per session per 15 minutes, riding a
request that already writes, and it lands on a path the client already calls
without being asked.

The staleness this accepts is bounded by the access-token TTL, which is the same
clock that already governs how long the UI can go without asking anything.

### 6. Four routes, and revoking the current session is never special-cased

| Route | Effect |
|---|---|
| `GET /auth/sessions` | the caller's sessions, current one flagged |
| `DELETE /auth/sessions/:id` | revoke one session |
| `DELETE /auth/sessions` | revoke every session **except** the caller's |
| `DELETE /auth/sessions/current` | revoke every session **including** the caller's |

The list returns every session of the caller's that is not revoked — including
the ones past `expires_at`, which it renders as expired rather than hiding. A
session the user cannot see is one they cannot understand the absence of, and a
row that disappeared on its own is exactly the kind of thing that makes a security
panel untrustworthy. Expired sessions are revokable but not worth revoking; the
bulk routes skip them.

The last two share one handler parameterised by a bool. They are two routes
because the two questions are different ones: *"that laptop is gone"* and *"I have
been compromised"* are not the same action, and collapsing them would force one of
them to be the wrong answer.

`DELETE /auth/sessions/:id` accepts the caller's own current session id and
revokes it. "Revoke this device" and "log me out here" are the same action, and
special-casing the current session out of its own revoke route would mean the
current tab silently continues while the UI says it was revoked.

All four live under the existing `api.Use(auth.RequireAuth(...))` group in
`main.go:212`.

### 7. The in-use failure path already exists and is not rebuilt

Revoking a session someone is actively using is the feature's real test, and it
costs no new code:

- `RequireAuth` answers 401.
- The SPA's `sendWithAuthRetry` (`frontend/src/api/client.ts:362`) sees the 401,
  calls `refreshSession()`, which fails because the session is revoked, returns
  false, and the existing 401 handler clears the stored user and redirects to
  `/login`.
- `client/api` reaches the same state through `errNoSession` and
  `Unauthorized(err)`, which is what the TUI and the MCP server already branch on.

No failure-path code is added. What *is* tested is that it happens — see §9.

### 8. The UI is one card on the existing Settings page

`Settings.tsx` is a single scrolling column of `bg-card` panels, so a "Signed-in
devices" panel drops into the existing shape with no navigation restructure.

Each row: a parsed user agent (*"Chrome on Windows"*), last seen relative, and
"This device" on the caller's own row. Parsing is a fixed table of substrings
(`Windows`, `Macintosh`, `Linux`, `Android`, `iPhone`…) with the raw string as
the fallback — a user agent is not worth a dependency or a grammar.

Destructive actions confirm through `AlertDialog`, per the repository convention.
Two buttons for the two bulk routes in §6.

### 9. The invariant is tested where a mock cannot lie about it

Handler tests cover the routes through `pgxmock`. The load-bearing claim —
*"an access token that was valid a moment ago is rejected the moment its session
is revoked"* — gets a case in `backend/integration_test.go` against real
Postgres: mint a session, capture its access token, revoke the session, assert
401 on a route that token would otherwise pass.

`pgxmock` would accept a wrong column or a wrong join and still return 200 for
everything, which is the same failure mode the existing integration suite was
written to catch elsewhere. The claim is about revocation actually happening, so
it is tested where it can actually fail.

---

## Alternatives considered

**Shorten the access-token TTL.** Cheapest mitigation, and worth doing anyway —
but it bounds the exposure rather than ending it, and it makes non-browser clients
re-authenticate constantly. Rejected as the primary answer: it reduces the blast
radius of the problem rather than solving the problem.

**Deny-list `jti` values after the fact.** Works, but only when the token leaks
while the log survives, and needs the same plumbing as a proper session store
without giving the user a list. The sessions table is the deny-list, indexed and
addressable.

**A per-user `session_version` counter bumped on any revocation.** Smaller — one
column on `users`, one read in `RequireAuth`. Rejected: it cannot revoke *one*
session, which is the feature. "Sign out of my other devices" is the common case
and it needs per-session granularity.

**Reject `sid`-less tokens outright.** Rejected in §4.

**Cache the liveness check.** Rejected in §3.

**Expose the session routes as MCP tools.** The audit in `tools_test.go` requires
every read-only route to be a tool or carry a stated reason in `unexposedRoutes`.
These go to `unexposedRoutes` with a reason: a read-only agent that could already
sign in holds the whole account, and listing the user's devices adds exposure
without adding capability to a surface whose tools are financial aggregates.
`readonly.Guard` then refuses the routes, which is the correct default for a
credential-adjacent surface an agent has no reason to read.

## Constraints

- `client/api/spec_parity_test.go` holds one `routeCase` per operation and fails
  until the shared client claims a new route. All four routes need
  `client/api` methods *and* parity cases, not just a backend handler.
- `backend/openapi.yaml` and `README.md` are held in lockstep by the route-parity
  test; both are updated in the same change.
- `logout` must mark the session revoked as well as the family, or every logged-out
  session lingers in the list as a row the user cannot explain.