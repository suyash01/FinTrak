# FinTrak Frontend

React 19 + TypeScript + Vite + Tailwind CSS 4 SPA for FinTrak, managed with
[bun](https://bun.sh/).

## Commands

Run from `frontend/`:

```bash
bun install            # install dependencies (bun.lock is the lockfile)
bun run dev            # Vite dev server on :5173 (host: true for Docker)
bun run typecheck      # tsc --noEmit
bun run test           # vitest run (single pass)
bun run test:coverage  # vitest with v8 coverage (enforced regression floor)
bun run build          # production build to dist/
bun run preview        # preview the production build
```

Verification before a PR is `bun run build` + `bun run test` + `bun run typecheck`.

## Provider hierarchy and state ownership

`App.tsx` mounts the outer providers in this order:

1. `ErrorBoundary`
2. `ThemeProvider` — light/dark mode, accent theme, and local-storage persistence
3. `BrowserRouter`
4. `SettingsProvider` — compact layout and page-size preferences
5. `AuthProvider` — non-sensitive user cache and session verification

The authenticated route tree mounts `DomainDataProvider` for shared accounts,
account types, categories, groups, payees, and settings, followed by
`OfflineProvider` for the offline status and outbox. `OfflineBanner` is the
only user-facing offline status component. Pages that display ledger data
reload when `syncedAt` changes.

`AuthContext` caches only the non-sensitive user object under `fintrak_user`;
the JWT remains in an httpOnly cookie. A transport failure while checking
`/auth/me` keeps the cached session so an installed app can boot offline.
Sign-out clears the departing user's offline cache.

## API and authentication

All API calls go through the single client at `src/api/client.ts`. The base URL
is `VITE_API_URL`, defaulting to `/api/v1` (same-origin). Vite proxies `/api`
to `http://localhost:8080` in development; the production nginx image proxies
`/api/v1` to the backend.

Keep the API same-origin (or build with `VITE_API_URL=/api/v1`). A cross-origin
`VITE_API_URL` also requires corresponding CSP and backend CORS changes.
Authentication is the backend's httpOnly, `SameSite=Lax` session cookie; no
component should call `fetch` directly.

## Offline cache, outbox, and PWA

`src/api/offlineCache.ts` keeps the last successful GET for an allowlist of
paths, namespaced by user. `src/api/outbox.ts` queues manual transaction
creates per user. `OfflineContext` flushes the queue on launch, reconnect, and
on demand. The queue is bounded, preserves order, reports server-rejected
payloads, and never silently evicts a user-recorded transaction.

The owner is captured when a request is issued, not when its response arrives.
This prevents a response after a session swap from being attributed to the new
user. `ApiError` and `NetworkError` distinguish rejected requests from requests
that never reached the server; only the latter are queued. Every create includes
a stable client-generated key so replay is idempotent.

`public/sw.js` and `public/manifest.webmanifest` are hand-written; there is no
PWA build plugin. Register the worker only in production builds and test it with
`bun run preview`, not the Vite dev server, because the worker uses the
production asset graph. `src/lib/prefetchRoutes.ts` warms dashboard and
transaction chunks for offline navigation.

## Dates, money, and partial updates

Use `todayLocalISO()` for a local "today" and `parseDateOnly()` for stored
`YYYY-MM-DD` values. Do not use `new Date("YYYY-MM-DD")` or
`toISOString().split("T")[0]` for user-facing dates; they can shift the day
because of UTC parsing/time zones. Money crosses the API boundary as decimal
major-unit text and is displayed through the existing formatters; do not use
floating-point arithmetic for financial calculations.

Inline edits must PATCH only the field the user changed. Sending a whole-row
body can silently overwrite a concurrent update. Use semantic theme tokens
(`bg-background`, `text-foreground`, `border-border`, `bg-primary`, and so on)
rather than raw palette classes. User-supplied account/category colors are the
intentional inline-style exception.

## UI conventions

Use the shadcn/ui primitives in `src/components/ui/`; add new primitives with
`bunx shadcn@latest add <name>` rather than hand-rolling buttons, inputs,
dialogs, selects, or tables. Destructive confirms use `AlertDialog`, errors use
`toast.error`, modals use `Dialog`, and right-side drawers use `Sheet`.
Radix `SelectItem` values use sentinels instead of empty strings. The dense
`EditableSelect` cells and the bulk-action selects in
`Transactions/BulkActionBar.tsx` are deliberate native `<select>` exceptions.
Respect the Compact Layout setting in new screens.

## Tests

Vitest runs under jsdom with shared setup polyfills. The jsdom version is
intentionally pinned because newer releases break Radix Select interaction
cleanup. See the root `AGENTS.md` for repository-wide conventions and test
policies.
