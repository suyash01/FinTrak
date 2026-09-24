# Database Migrations

Migrations are embedded in the backend binary with `//go:embed migrations/*.sql`
and applied by `db.RunMigrations` during startup. `db.Migrate` returns errors
for integration tests and tooling. Startup applies pending `up` migrations; it
never automatically rolls them back.

## Rules

- Every `NNNNNN_*.up.sql` has a matching `NNNNNN_*.down.sql`.
- Versions are contiguous and are never edited after release.
- Schema or data changes always use a new migration, never a direct edit to an
  applied migration.
- Back up PostgreSQL before an upgrade. Some migrations repair existing data,
  including orphan cleanup, duplicate resolution, and backfills.
- Do not bypass migration locking or let a second replica apply migrations
  independently. Let one migration-aware startup path take the lock, or stop
  the other replicas during maintenance.

`000001_initial_schema` is a squashed baseline for the initial schema. It is
followed by the incremental migrations in this directory; it is not the final
schema by itself. Later migrations add features such as server-recorded
refresh sessions, loan schedules, recurring term history, and client-key
idempotency.

## Recovery from a dirty version

If golang-migrate reports a dirty schema, stop application instances before
inspecting the `schema_migrations` state. Compare the database with the failed
migration, repair the data only through a reviewed migration or backup
restore, and then follow the golang-migrate recovery procedure to clear the
dirty version. Never mark a migration clean solely because the process exited.

## Important data migrations

- `000004` removes invalid cross-account billing-cycle references.
- `000008` backfills balance-transfer principal.
- `000010` resolves duplicate payees and account-linked payee conflicts.
- `000012` creates server-recorded refresh sessions and adds the billing-cycle
  detachment intent flag.
- `000013` backfills NULL transaction tags and enforces the non-null tags
  invariant.

Refresh-token rows are retained after expiry or revocation so reuse detection
can distinguish a spent token from an unknown one. The application does not
currently run an automatic cleanup job. Any future pruning must preserve the
reuse-detection window and be operated as a reviewed maintenance task.
