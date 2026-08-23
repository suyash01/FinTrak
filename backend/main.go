package main

import (
	"fmt"
	"log"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/config"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/handlers"
	"github.com/fintrak/backend/internal/validation"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// Version is injected at build time via -ldflags "-X main.Version=...".
// Falls back to a dev version for local builds.
var Version = "0.1.0-alpha"

func main() {
	validation.Init()

	cfg := config.Load()

	// Connect to database
	db.Connect(cfg.DatabaseURL)
	defer db.Close()

	// Run migrations and seed
	db.RunMigrations(cfg.DatabaseURL)
	db.SeedAccountTypes()

	// Setup Gin
	r := setupRouter(cfg)

	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("🚀 FinTrak API v%s running on %s\n", Version, addr)
	r.Run(addr)
}

// setupRouter builds the Gin router and registers all routes. It is extracted
// from main so it can be exercised by unit tests.
func setupRouter(cfg *config.Config) *gin.Engine {
	r := gin.Default()

	// Point the statement handler at the standalone parser service.
	handlers.SetStatementParserURL(cfg.ParserURL)

	// Expose JWT secret to auth handlers via context
	r.Use(func(c *gin.Context) {
		c.Set("jwtSecret", cfg.JWTSecret)
		c.Next()
	})

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

	// API Routes
	api := r.Group("/api/v1")

	// Health check endpoint for Docker/orchestrators
	api.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Public: authentication
	api.POST("/auth/register", handlers.Register)
	api.POST("/auth/login", handlers.Login)

	// Protected routes
	api.Use(auth.RequireAuth(cfg.JWTSecret))
	{
		// Accounts
		accounts := api.Group("/accounts")
		accounts.GET("", handlers.GetAccounts)
		accounts.POST("", handlers.CreateAccount)
		accounts.PUT("/:id", handlers.UpdateAccount)
		accounts.DELETE("/:id", handlers.DeleteAccount)
		accounts.GET("/:id/export", handlers.ExportAccount)
		accounts.GET("/:id/billing-cycles", handlers.GetBillingCycles)

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
		transactions.POST("", handlers.CreateTransaction)
		transactions.PATCH("/:id", handlers.UpdateTransaction)
		transactions.DELETE("/:id", handlers.DeleteTransaction)
		transactions.POST("/import", handlers.ImportTransactions)
		transactions.POST("/validate", handlers.ValidateTransactions)
		transactions.POST("/bulk-categorize", handlers.BulkCategorize)
		transactions.POST("/bulk-payee", handlers.BulkUpdatePayee)
		transactions.POST("/bulk-billing-cycle", handlers.BulkUpdateBillingCycle)
		transactions.POST("/bulk-delete", handlers.BulkDeleteTransactions)

		// Statement parsing (forwards to the standalone parser service)
		api.POST("/statements/parse", handlers.ParseStatement)
		api.GET("/statements/extractors", handlers.ListStatementExtractors)

		// Paperless-ngx integration (per-user settings + manual pull)
		api.GET("/paperless/settings", handlers.GetPaperlessSettings)
		api.PUT("/paperless/settings", handlers.UpdatePaperlessSettings)
		api.GET("/paperless/documents", handlers.ListPaperlessDocuments)
		api.GET("/paperless/documents/:id/file", handlers.GetPaperlessDocumentFile)
		api.POST("/paperless/import", handlers.ImportPaperlessDocument)

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

	return r
}
