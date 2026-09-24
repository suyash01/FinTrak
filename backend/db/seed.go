package db

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// categoryStore is the minimal query surface needed to seed default
// categories. Both *pgxpool.Pool (via DBPool) and pgx.Tx satisfy it, so the
// seeder can run inside the same transaction that creates the user.
type categoryStore interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// SeedCategory describes one row in the default category set created for each
// new user.
type SeedCategory struct {
	Name  string
	Icon  string
	Color string
	Group string
}

// SeedDefaultCategories inserts the stock income/expense/transfer/cashback
// categories for a user, but only when the user has none yet. It runs during
// registration inside the user-creation transaction, not on every boot. It
// returns any database error so a failed seed rolls back the new user rather
// than leaving an account without its default categories.
func SeedDefaultCategories(ctx context.Context, store categoryStore, userID uuid.UUID) error {
	var count int
	err := store.QueryRow(ctx, "SELECT COUNT(*) FROM categories WHERE user_id = $1", userID).Scan(&count)
	if err != nil {
		return fmt.Errorf("count categories for user %s: %w", userID, err)
	}
	if count > 0 {
		return nil
	}

	categories := []SeedCategory{
		{"Food & Dining", "utensils-crossed", "#f97316", "expense"},
		{"Groceries", "shopping-cart", "#84cc16", "expense"},
		{"Shopping", "shopping-bag", "#ec4899", "expense"},
		{"Transport", "car", "#8b5cf6", "expense"},
		{"Fuel", "fuel", "#f59e0b", "expense"},
		{"Bills & Utilities", "receipt", "#06b6d4", "expense"},
		{"Rent", "home", "#6366f1", "expense"},
		{"Entertainment", "film", "#d946ef", "expense"},
		{"Health & Medical", "heart-pulse", "#ef4444", "expense"},
		{"Education", "graduation-cap", "#14b8a6", "expense"},
		{"Personal Care", "sparkles", "#f472b6", "expense"},
		{"Travel", "plane", "#0ea5e9", "expense"},
		{"Insurance", "shield", "#64748b", "expense"},
		{"Subscriptions", "repeat", "#a855f7", "expense"},
		{"EMI & Loans", "landmark", "#e11d48", "expense"},
		{"Investments", "trending-up", "#10b981", "expense"},
		{"Salary", "wallet", "#22c55e", "income"},
		{"Interest", "percent", "#16a34a", "income"},
		{"Refund", "undo", "#38bdf8", "income"},
		{"Other Income", "plus-circle", "#4ade80", "income"},
		{"Dividends", "trending-up", "#10b981", "income"},
		{"Transfer", "arrow-left-right", "#94a3b8", "transfer"},
		{"ATM Withdrawal", "banknote", "#78716c", "transfer"},
		{"Cashback", "badge-indian-rupee", "#eab308", "cashback"},
	}

	query := `INSERT INTO categories (id, name, icon, color, group_id, user_id) VALUES `
	values := []interface{}{}
	placeholders := []string{}
	for i, c := range categories {
		base := i * 6
		placeholders = append(placeholders,
			fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d)", base+1, base+2, base+3, base+4, base+5, base+6))
		values = append(values, uuid.New(), c.Name, c.Icon, c.Color, c.Group, userID)
	}
	query += strings.Join(placeholders, ", ")

	if _, err = store.Exec(ctx, query, values...); err != nil {
		return fmt.Errorf("seed default categories for user %s: %w", userID, err)
	}

	slog.Info("seeded default categories", slog.String("user_id", userID.String()))
	return nil
}

// SeedCategoryGroup describes one immutable base category group row.
type SeedCategoryGroup struct {
	ID        string
	Name      string
	Icon      string
	Color     string
	SortOrder int
}

// SeedCategoryGroups inserts the four immutable base category groups (income,
// expense, transfer, cashback). They are global (user_id NULL), marked is_base
// so they can never be deleted, and shared by every user. It is idempotent
// (ON CONFLICT DO NOTHING) and runs on every boot.
func SeedCategoryGroups() {
	ctx := context.Background()

	groups := []SeedCategoryGroup{
		{"income", "Income", "wallet", "#22c55e", 1},
		{"expense", "Expense", "shopping-bag", "#f97316", 2},
		{"transfer", "Transfer", "arrow-left-right", "#94a3b8", 3},
		{"cashback", "Cashback", "badge-indian-rupee", "#eab308", 4},
	}

	for _, g := range groups {
		_, err := Pool.Exec(ctx,
			`INSERT INTO category_groups (id, name, icon, color, is_base, user_id, sort_order)
			 VALUES ($1, $2, $3, $4, TRUE, NULL, $5)
			 ON CONFLICT (id) WHERE user_id IS NULL DO NOTHING`,
			g.ID, g.Name, g.Icon, g.Color, g.SortOrder,
		)
		if err != nil {
			slog.Error("failed to seed category group", slog.String("id", g.ID), slog.String("error", err.Error()))
		}
	}

	slog.Info("seeded default category groups")
}

// SeedAccountType describes one built-in account type row.
type SeedAccountType struct {
	ID              string
	Name            string
	PositiveTxnType string
}

// SeedAccountTypes inserts the built-in account types. It is idempotent
// (ON CONFLICT DO NOTHING) and runs on every boot.
func SeedAccountTypes() {
	ctx := context.Background()

	accountTypes := []SeedAccountType{
		{"bank", "Bank Account", "credit"},
		{"credit_card", "Credit Card", "credit"},
		// Loan/EMI accounts hold no transactions of their own; EMI payments
		// live on other accounts and are attached via loan_attachments. The
		// balance is the total attached (repaid) amount, hence 'debit'.
		{"loan", "Loan / EMI", "debit"},
	}

	for _, at := range accountTypes {
		_, err := Pool.Exec(ctx,
			`INSERT INTO account_types (id, name, positive_txn_type) VALUES ($1, $2, $3)
			 ON CONFLICT (id) DO NOTHING`,
			at.ID, at.Name, at.PositiveTxnType,
		)
		if err != nil {
			slog.Error("failed to seed account type", slog.String("id", at.ID), slog.String("error", err.Error()))
		}
	}

	slog.Info("seeded default account types")
}
