package db

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
)

// SeedCategory describes one row in the default category set created for each
// new user.
type SeedCategory struct {
	Name  string
	Icon  string
	Color string
	Group string
}

// SeedDefaultCategories inserts the stock income/expense/transfer/cashback
// categories for a user, but only when the user has none yet (so it is safe to
// call on every registration and boot). Errors are logged and swallowed.
func SeedDefaultCategories(ctx context.Context, userID uuid.UUID) {
	var count int
	err := Pool.QueryRow(ctx, "SELECT COUNT(*) FROM categories WHERE user_id = $1", userID).Scan(&count)
	if err != nil {
		log.Printf("Failed to count categories for user %s: %v", userID, err)
		return
	}
	if count > 0 {
		return
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

	_, err = Pool.Exec(ctx, query, values...)
	if err != nil {
		log.Printf("Failed to seed categories: %v", err)
		return
	}

	fmt.Println("✓ Seeded default categories")
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
			log.Printf("Failed to seed category group %s: %v", g.ID, err)
		}
	}

	fmt.Println("✓ Seeded default category groups")
}

// SeedAccountType describes one built-in account type row.
type SeedAccountType struct {
	ID              string
	Name            string
	PositiveTxnType string
}

// PromoteAdminUsers grants the 'admin' role to any existing user whose email is
// in the given allowlist. It is idempotent and safe to call on every boot.
func PromoteAdminUsers(emails []string) {
	if len(emails) == 0 {
		return
	}
	ctx := context.Background()
	for _, email := range emails {
		email = strings.TrimSpace(email)
		if email == "" {
			continue
		}
		_, err := Pool.Exec(ctx,
			"UPDATE users SET role = 'admin' WHERE LOWER(email) = LOWER($1)",
			email,
		)
		if err != nil {
			log.Printf("Failed to promote user %s to admin: %v", email, err)
		}
	}
	fmt.Println("✓ Ensured admin users")
}

// SeedAccountTypes inserts the built-in "bank" and "credit_card" account types.
// It is idempotent (ON CONFLICT DO NOTHING) and runs on every boot.
func SeedAccountTypes() {
	ctx := context.Background()

	accountTypes := []SeedAccountType{
		{"bank", "Bank Account", "credit"},
		{"credit_card", "Credit Card", "credit"},
	}

	for _, at := range accountTypes {
		_, err := Pool.Exec(ctx,
			`INSERT INTO account_types (id, name, positive_txn_type) VALUES ($1, $2, $3)
			 ON CONFLICT (id) DO NOTHING`,
			at.ID, at.Name, at.PositiveTxnType,
		)
		if err != nil {
			log.Printf("Failed to seed account type %s: %v", at.ID, err)
		}
	}

	fmt.Println("✓ Seeded default account types")
}
