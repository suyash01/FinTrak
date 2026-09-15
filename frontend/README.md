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

## API base URL and same-origin authentication

All API calls go through the single client at `src/api/client.ts`. The base URL
is `import.meta.env.VITE_API_URL`, defaulting to `http://localhost:8080/api/v1`
in dev and `/api/v1` (same-origin) in the production image.

In the Docker/nginx deployment the frontend reverse-proxies `/api/v1` to the
backend, and authentication uses an httpOnly, `SameSite=Lax` session cookie.
Keep the API same-origin (or build with `VITE_API_URL=/api/v1`): a cross-origin
`VITE_API_URL` is blocked by the frontend's `connect-src 'self'` CSP unless
`frontend/nginx.conf` and the backend `ALLOWED_ORIGINS` are updated too.

## Conventions

See the root `AGENTS.md` for component/state/theming conventions (shadcn/ui
primitives, semantic color tokens, Context providers, and the `@/*` alias).
