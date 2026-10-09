-- Drop the sessions table.
--
-- Like the baseline's down migration, this exists for a deliberate teardown of a
-- disposable database: the application never migrates down (see db.RunMigrations).
DROP TABLE IF EXISTS sessions;