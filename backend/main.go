package main

import (
	"fmt"
	"log"

	"github.com/fintrak/backend/config"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/handlers"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.Load()

	// Connect to database
	db.Connect(cfg.DatabaseURL)
	defer db.Close()

	// Run migrations and seed
	db.RunMigrations(cfg.DatabaseURL)
	db.SeedCategories()
	db.SeedAccountTypes()

	// Setup Gin
	r := gin.Default()

	// CORS
	r.Use(cors.New(cors.Config{
		AllowOriginFunc: func(origin string) bool {
			for _, o := range cfg.AllowedOrigins {
				if o == "*" || o == origin {
					return true
				}
			}
			return false
		},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With"},
		AllowCredentials: true,
	}))

	// Health check endpoint for Docker/orchestrators
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// API Routes
	api := r.Group("/api/v1")
	{
		// Accounts
		accounts := api.Group("/accounts")
		accounts.GET("", handlers.GetAccounts)
		accounts.POST("", handlers.CreateAccount)
		accounts.PUT("/:id", handlers.UpdateAccount)
		accounts.DELETE("/:id", handlers.DeleteAccount)
		accounts.GET("/:id/export", handlers.ExportAccount)

		// Account Types
		accountTypes := api.Group("/account-types")
		accountTypes.GET("", handlers.GetAccountTypes)
		accountTypes.POST("", handlers.CreateAccountType)
		accountTypes.PUT("/:id", handlers.UpdateAccountType)
		accountTypes.DELETE("/:id", handlers.DeleteAccountType)

		// Categories
		api.GET("/categories", handlers.GetCategories)
		api.POST("/categories", handlers.CreateCategory)

		// Transactions
		transactions := api.Group("/transactions")
		transactions.GET("", handlers.GetTransactions)
		transactions.PATCH("/:id", handlers.UpdateTransaction)
		transactions.DELETE("/:id", handlers.DeleteTransaction)
		transactions.POST("/import", handlers.ImportTransactions)
		transactions.POST("/bulk-categorize", handlers.BulkCategorize)
		transactions.POST("/bulk-payee", handlers.BulkUpdatePayee)
		transactions.POST("/bulk-delete", handlers.BulkDeleteTransactions)

		// Rules
		api.GET("/rules", handlers.GetRules)
		api.POST("/rules", handlers.CreateRule)
		api.PUT("/rules/:id", handlers.UpdateRule)
		api.DELETE("/rules/:id", handlers.DeleteRule)
		api.POST("/rules/apply", handlers.ApplyRules)

		// Payees
		api.GET("/payees", handlers.GetPayees)
		api.POST("/payees", handlers.CreatePayee)
		api.PUT("/payees/:id", handlers.UpdatePayee)
		api.DELETE("/payees/:id", handlers.DeletePayee)

		// Links
		api.GET("/links", handlers.GetLinks)
		api.POST("/links", handlers.CreateLink)
		api.POST("/links/bulk", handlers.BulkCreateLinks)
		api.DELETE("/links/:id", handlers.DeleteLink)
		api.POST("/links/bulk-delete", handlers.BulkDeleteLinks)
		api.GET("/links/transfer-suggestions", handlers.GetTransferSuggestions)
		api.GET("/links/cashback-suggestions", handlers.GetCashbackSuggestions)

		// Dashboard
		api.GET("/dashboard/summary", handlers.GetDashboardSummary)
	}

	const Version = "0.1.0-alpha"

	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("🚀 FinTrak API v%s running on %s\n", Version, addr)
	r.Run(addr)
}
