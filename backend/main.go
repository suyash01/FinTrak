// Command backend is the FinTrak API server. It loads configuration, connects
// to PostgreSQL, runs migrations and seeders, then serves the /api/v1 REST
// endpoints consumed by the FinTrak frontend.
package main

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/config"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/handlers"
	"github.com/fintrak/backend/internal/logger"
	"github.com/fintrak/backend/internal/validation"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// Version is injected at build time via -ldflags "-X main.Version=...".
// Falls back to a dev version for local builds.
var Version = "0.1.0-alpha"

// main boots the FinTrak API: it initializes request validation, loads
// configuration, connects to the database (applying migrations and seeders),
// and finally starts the HTTP server on the configured port.
func main() {
	validation.Init()

	cfg := config.Load()

	// Structured logging: debug level (with request/response body capture) in
	// development, info + JSON in production.
	logger.New(cfg.Env, cfg.LogLevel)
	logger.SetMaxBodyLog(cfg.LogBodyLimit)

	// Connect to database
	db.Connect(cfg.DatabaseURL)
	defer db.Close()

	// Run migrations and seed
	db.RunMigrations(cfg.DatabaseURL)
	db.SeedAccountTypes()
	db.SeedCategoryGroups()
	db.PromoteAdminUsers(cfg.AdminEmails)

	// Setup Gin
	r := setupRouter(cfg)

	addr := fmt.Sprintf(":%s", cfg.Port)
	slog.Info("FinTrak API starting", "version", Version, "env", cfg.Env, "addr", addr)
	if err := r.Run(addr); err != nil {
		slog.Error("server exited", "error", err)
	}
}

// setupRouter builds the Gin router and registers all routes. It is extracted
// from main so it can be exercised by unit tests.
func setupRouter(cfg *config.Config) *gin.Engine {
	r := gin.New()

	// Recover panics into a structured 500 response instead of crashing the
	// process. Registered first so it also covers the logging middleware.
	r.Use(gin.CustomRecovery(func(c *gin.Context, recovered any) {
		slog.Error("panic recovered",
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Any("error", recovered),
		)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}))

	// Structured request logging. Emits an access line for every request and,
	// at debug level (development), captures and logs request/response bodies.
	// Skipped under test mode to keep unit test output quiet.
	if gin.Mode() != gin.TestMode {
		r.Use(logger.RequestLogger(slog.Default()))
	}

	// Point the statement handler at the standalone parser service.
	handlers.SetStatementParserURL(cfg.ParserURL)

	// Expose JWT secret, admin allowlist, environment, and the token
	// encryption key to handlers via the request context.
	r.Use(func(c *gin.Context) {
		c.Set("jwtSecret", cfg.JWTSecret)
		c.Set("adminEmails", cfg.AdminEmails)
		c.Set("appEnv", cfg.Env)
		c.Set("tokenEncryptionKey", cfg.TokenEncryptionKey)
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

		// Account Types (mutations are admin-only; the type list is shared
		// reference data that affects balance semantics for every user)
		accountTypes := api.Group("/account-types")
		accountTypes.GET("", handlers.GetAccountTypes)
		accountTypes.POST("", auth.RequireAdmin(), handlers.CreateAccountType)
		accountTypes.PUT("/:id", auth.RequireAdmin(), handlers.UpdateAccountType)
		accountTypes.DELETE("/:id", auth.RequireAdmin(), handlers.DeleteAccountType)

		// Category groups (base groups are read-only; custom groups are user-owned)
		api.GET("/groups", handlers.GetGroups)
		api.POST("/groups", handlers.CreateGroup)
		api.PUT("/groups/:id", handlers.UpdateGroup)
		api.DELETE("/groups/:id", handlers.DeleteGroup)

		// Categories (user-owned CRUD; global categories are admin-managed below)
		api.GET("/categories", handlers.GetCategories)
		api.POST("/categories", handlers.CreateCategory)
		api.PUT("/categories/:id", handlers.UpdateCategory)
		api.DELETE("/categories/:id", handlers.DeleteCategory)

		// Admin: global groups and global categories shared by every user
		admin := api.Group("/admin", auth.RequireAdmin())
		admin.POST("/groups", handlers.CreateGlobalGroup)
		admin.POST("/categories", handlers.CreateGlobalCategory)
		admin.PUT("/categories/:id", handlers.UpdateGlobalCategory)
		admin.DELETE("/categories/:id", handlers.DeleteGlobalCategory)

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
