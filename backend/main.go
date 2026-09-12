// Command backend is the FinTrak API server. It loads configuration, connects
// to PostgreSQL, runs migrations and seeders, then serves the /api/v1 REST
// endpoints consumed by the FinTrak frontend.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/config"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/handlers"
	"github.com/fintrak/backend/internal/logger"
	"github.com/fintrak/backend/internal/ratelimit"
	"github.com/fintrak/backend/internal/validation"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// Version is injected at build time via -ldflags "-X main.Version=...".
// Falls back to a dev version for local builds.
var Version = "0.1.0-alpha"

// HTTP server timeouts. ReadHeaderTimeout is the primary slowloris defense;
// the others bound body reads, response writes, and idle keep-alive conns.
// WriteTimeout is generous because statement parsing proxies to an upstream
// service that can take up to ~60s.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 60 * time.Second
	writeTimeout      = 120 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 15 * time.Second
)

// main boots the FinTrak API: it initializes request validation, loads
// configuration, connects to the database (applying migrations and seeders),
// and finally starts the HTTP server on the configured port. It shuts down
// gracefully on SIGINT/SIGTERM so in-flight requests drain and the database
// connection is closed.
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

	// Setup Gin
	r := setupRouter(cfg)

	// Throttle unauthenticated auth endpoints (per-IP and per-account).
	authLimiter := ratelimit.New(ratelimit.DefaultConfig())
	handlers.SetAuthRateLimiter(authLimiter)

	addr := fmt.Sprintf(":%s", cfg.Port)
	srv := newServer(addr, r)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go authLimiter.StartJanitor(ctx.Done(), 0)

	go func() {
		slog.Info("FinTrak API starting", "version", Version, "env", cfg.Env, "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server exited", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down FinTrak API")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown", "error", err)
	}
}

// newServer returns an http.Server with production timeouts applied. It is
// extracted from main so the timeout policy is explicit and testable.
func newServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
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

	// Conservative security headers. nginx owns the SPA's CSP; these cover API
	// responses (including proxied Paperless content) so a compromised upstream
	// can never have the browser sniff or frame it.
	r.Use(securityHeaders())

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
		c.Set("adminSetupToken", cfg.AdminSetupToken)
		c.Set("appEnv", cfg.Env)
		c.Set("cookieSecure", cfg.CookieSecure)
		c.Set("tokenEncryptionKey", cfg.TokenEncryptionKey)
		c.Next()
	})

	// CORS. A wildcard origin is incompatible with credentialed requests, so
	// buildCORSConfig disables credentials whenever "*" is configured.
	r.Use(cors.New(buildCORSConfig(cfg.AllowedOrigins)))

	// API Routes
	api := r.Group("/api/v1")

	// Health check endpoint for Docker/orchestrators
	api.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Public: authentication
	api.POST("/auth/register", handlers.Register)
	api.POST("/auth/login", handlers.Login)
	api.POST("/auth/logout", handlers.Logout)

	// Protected routes
	api.Use(auth.RequireAuth(cfg.JWTSecret))
	{
		// Current session user (used to rehydrate the SPA from the httpOnly cookie).
		api.GET("/auth/me", handlers.Me)

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
		transactions.POST("/bulk-loan", handlers.BulkLinkLoan)
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

// securityHeaders sets conservative security headers on every response.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		c.Next()
	}
}

// buildCORSConfig returns the CORS middleware configuration for the allowed
// origins. Access-Control-Allow-Credentials must never be combined with a
// wildcard origin (browsers reject the pair, and reflecting arbitrary origins
// with credentials would let any site make authenticated requests), so
// credentials are disabled whenever "*" appears in origins.
func buildCORSConfig(origins []string) cors.Config {
	allowCredentials := true
	for _, o := range origins {
		if o == "*" {
			allowCredentials = false
			break
		}
	}

	return cors.Config{
		AllowOriginFunc: func(origin string) bool {
			for _, o := range origins {
				if o == "*" || o == origin {
					return true
				}
			}
			return false
		},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With"},
		AllowCredentials: allowCredentials,
	}
}
