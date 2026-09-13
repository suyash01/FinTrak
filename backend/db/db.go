// Package db owns the PostgreSQL connection pool, schema migrations, and
// idempotent seeders. The pool is constructed once at startup and handed to
// handlers explicitly via handlers.Server; the db package's own tests swap the
// package-level Pool for a pgxmock.
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
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
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
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
		slog.Error("unable to connect to database", slog.String("error", err.Error()))
		os.Exit(1)
	}

	if err := Pool.Ping(context.Background()); err != nil {
		slog.Error("unable to ping database", slog.String("error", err.Error()))
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
	if err := Migrate(databaseURL); err != nil {
		slog.Error("migration up failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	slog.Info("database migrations complete")
}

// Migrate applies the embedded migrations and returns an error instead of
// exiting, so tests and tooling can handle failures. A fully up-to-date schema
// is not an error.
func Migrate(databaseURL string) error {
	d, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("initialize migrations source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, databaseURL)
	if err != nil {
		return fmt.Errorf("initialize migrate instance: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

// WithTx executes the given function within a database transaction on the
// provided pool. It automatically handles starting the transaction, rolling it
// back on error, and committing it on success.
func WithTx(ctx context.Context, pool DBPool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := fn(tx); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
