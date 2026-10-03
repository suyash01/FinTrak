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

`000001_initial_schema` is the whole schema: it is applied to an empty database
in one step, and it is the only migration that exists today. It was squashed
from the incremental history that preceded the first release, so the repairs
that history performed — clearing cross-account billing-cycle references,
backfilling balance-transfer principal, merging duplicate payees, and nulling
legacy NULL transaction tags — are no-ops on an empty database and are not
carried over. Each one's surviving invariant is in the baseline as the
constraint that enforces it (`transactions_billing_cycle_account_fkey`,
`loan_transfers_principal_check`, `payees_user_name_uq`, and `tags NOT NULL`).

Squashing reset the numbering, so the next migration is `000002_`. A database
whose `schema_migrations` still records a version above 1 was built by the
squashed-away history and must be recreated: `golang-migrate` finds no version
higher than the one it holds, reports no change, and leaves that old schema in
place silently.

## Recovery from a dirty version

If golang-migrate reports a dirty schema, stop application instances before
inspecting the `schema_migrations` state. Compare the database with the failed
migration, repair the data only through a reviewed migration or backup
restore, and then follow the golang-migrate recovery procedure to clear the
dirty version. Never mark a migration clean solely because the process exited.

## Refresh-token retention

Refresh-token rows are retained after expiry or revocation so reuse detection
can distinguish a spent token from an unknown one. The application does not
currently run an automatic cleanup job. Any future pruning must preserve the
reuse-detection window and be operated as a reviewed maintenance task.