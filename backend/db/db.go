package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log"

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

var Pool DBPool

func Connect(databaseURL string) {
	var err error
	Pool, err = pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v\n", err)
	}

	if err := Pool.Ping(context.Background()); err != nil {
		log.Fatalf("Unable to ping database: %v\n", err)
	}

	fmt.Println("✓ Connected to PostgreSQL")
}

func Close() {
	if Pool != nil {
		Pool.Close()
	}
}

func RunMigrations(databaseURL string) {
	d, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		log.Fatalf("Failed to initialize migrations source: %v", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, databaseURL)
	if err != nil {
		log.Fatalf("Failed to initialize migrate instance: %v", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		log.Fatalf("Migration up failed: %v", err)
	}

	fmt.Println("✓ Database migrations complete")
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
