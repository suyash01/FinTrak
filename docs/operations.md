# FinTrak Operations

## Development

```bash
make dev
make dev-down
```

The development Compose stack publishes only loopback ports. PostgreSQL,
Adminer, pgAdmin, and the unauthenticated statement parser are developer
conveniences and must not be exposed to another host.

## Production

Use `make prod` for the bundled database or `make prod-no-db` for an external
database. The stack serves plain HTTP behind a TLS-terminating reverse proxy;
keep `COOKIE_SECURE=true` and configure HSTS at the terminator. The frontend is
the published ingress and proxies `/api/v1` to the backend.

`TRUSTED_PROXIES` must name only real proxy addresses. The production Compose
files default it to the frontend's static `FRONTEND_IP/32`, not the whole Docker
subnet, because the unauthenticated parser shares the network. For an external
PostgreSQL server, require TLS verification, for example `sslmode=verify-full`
with a mounted CA bundle. The private bundled-database network may use
`sslmode=disable`.

## Configuration and startup

`APP_ENV` accepts only `development` and `production`; unset means production.
Production requires strong `JWT_SECRET` and `TOKEN_ENCRYPTION_KEY` values.
`LOG_LEVEL=debug` enables debug records, but body capture also requires a
positive `LOG_BODY_LIMIT`. `ADMIN_EMAILS` controls new registration eligibility;
the persisted `users.role` is not changed by later allowlist edits.

At startup the backend creates the pgx pool, pings PostgreSQL, applies embedded
pending migrations, and runs the global account-type and category-group
seeders. `/health` is process liveness, not database or parser readiness.
`/openapi.yaml` is a public contract endpoint.

The HTTP server uses bounded header, read, write, and idle timeouts. On
SIGINT/SIGTERM it stops accepting requests and calls graceful shutdown with a
15-second context before closing the database pool. Statement parsing and
backup restore have longer coordinated client, proxy, and server limits.

## Parser, Paperless, and migrations

The parser accepts 20 MB uploads and enforces a page cap (`MAX_PAGES`, default
500). The backend's four-slot parse semaphore fails fast with `429` when
saturated. Keep the parser on its private network. Paperless settings are
stored per user; verify URL reachability, HTTPS/private-host policy, token, and
redirect behavior when troubleshooting.

Migrations are embedded and applied at startup. There is no automatic rollback.
Back up PostgreSQL before applying a new image; data-repair migrations may
change existing rows. See `backend/db/migrations/README.md` for pairing,
versioning, dirty-state recovery, and multi-replica locking.

## Backups and restore

`GET /export` creates a versioned JSON bundle. Password hashes and the
Paperless token are not included. `POST /import` is all-or-nothing, refuses to
merge into a non-empty user, and remaps IDs. Back up PostgreSQL before
migrations and before releases.

## Release and rollback

`make release VERSION=v1.2.3` requires a clean `master` at `origin/master` and
runs the same validation gate as CI before creating and pushing the tag.
Deploy a pinned released image tag rather than `latest`. For rollback, take a
database backup, deploy the previous image tag, and account for any schema
changes made by the newer image.

## Troubleshooting

- Parser saturation: retry a 429 rather than submitting a larger batch.
- Paperless failures: check reachability, TLS, token, and redirect policy.
- Session expiry: inspect access/refresh rotation without logging token values.
- PWA offline behavior: test with `bun run preview`, not only the Vite dev
  server, because the worker uses the production asset graph.
- Dirty migration state: stop replicas and follow the migration recovery steps
  before marking a migration clean.
