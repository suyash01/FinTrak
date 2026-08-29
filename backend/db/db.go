// Package db owns the PostgreSQL connection pool, schema migrations, and
// idempotent seeders. Tests swap the package-level Pool for a pgxmock so
// handlers can be exercised without a real database.
package db

import (
	"context"
	"embed"
	"errors"
	"log/slog"
	"os"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// DBPool defines the interface for database operations, allowing for mocking in tests.
type DBPool interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
	Ping(ctx context.Context) error
	Close()
}

// Pool is the shared connection pool used by all handlers and seeders. Tests
// replace it with a mock before exercising handlers.
var Pool DBPool

// Connect opens a pgx connection pool against the given URL and verifies it
// with a ping. It exits the process on failure.
func Connect(databaseURL string) {
	var err error
	Pool, err = pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		slog.Error("unable to connect to database", "error", err)
		os.Exit(1)
	}

	if err := Pool.Ping(context.Background()); err != nil {
		slog.Error("unable to ping database", "error", err)
		os.Exit(1)
	}

	slog.Info("connected to PostgreSQL")
}

// Close releases the shared connection pool. Safe to call more than once.
func Close() {
	if Pool != nil {
		Pool.Close()
	}
}

// RunMigrations applies any pending SQL migrations embedded in the binary via
// golang-migrate. No-op when the schema is already up to date; exits on error.
func RunMigrations(databaseURL string) {
	d, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		slog.Error("failed to initialize migrations source", "error", err)
		os.Exit(1)
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, databaseURL)
	if err != nil {
		slog.Error("failed to initialize migrate instance", "error", err)
		os.Exit(1)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		slog.Error("migration up failed", "error", err)
		os.Exit(1)
	}

	slog.Info("database migrations complete")
}

// WithTx executes the given function within a database transaction.
// It automatically handles starting the transaction, rolling it back on error,
// and committing it on success.
func WithTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := fn(tx); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
