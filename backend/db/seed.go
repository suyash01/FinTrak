package db

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"
)

type SeedCategory struct {
	Name  string
	Icon  string
	Color string
	Type  string
}

func SeedCategories() {
	ctx := context.Background()

	var count int
	Pool.QueryRow(ctx, "SELECT COUNT(*) FROM categories").Scan(&count)
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
		{"Cashback", "badge-indian-rupee", "#eab308", "income"},
		{"Refund", "undo", "#38bdf8", "income"},
		{"Other Income", "plus-circle", "#4ade80", "income"},
		{"Transfer", "arrow-left-right", "#94a3b8", "transfer"},
		{"ATM Withdrawal", "banknote", "#78716c", "transfer"},
		{"Uncategorized", "help-circle", "#6b7280", "expense"},
		{"Dividends", "trending-up", "#10b981", "income"},
	}

	for _, c := range categories {
		id := uuid.New()
		_, err := Pool.Exec(ctx,
			"INSERT INTO categories (id, name, icon, color, type) VALUES ($1, $2, $3, $4, $5)",
			id, c.Name, c.Icon, c.Color, c.Type,
		)
		if err != nil {
			log.Printf("Failed to seed category %s: %v", c.Name, err)
		}
	}

	fmt.Println("✓ Seeded default categories")
}
