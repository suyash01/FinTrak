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
		slog.Info("FinTrak API starting", slog.String("version", Version), slog.String("env", cfg.Env), slog.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server exited", slog.String("error", err.Error()))
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down FinTrak API")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown", slog.String("error", err.Error()))
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

	// Only believe forwarded client-IP headers from explicitly configured
	// proxies. With none configured (the default) gin ignores X-Forwarded-For
	// and X-Real-IP, so a client cannot spoof its IP to evade the per-IP auth
	// rate limit or pad the limiter map.
	if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		slog.Error("invalid TRUSTED_PROXIES configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}

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
		r.Use(logger.RequestLogger(slog.Default(), cfg.LogBodyLimit))
	}

	// Construct the handler server with its explicit dependencies: the shared
	// database pool and the statement-parser base URL.
	srv := handlers.NewServer(db.Pool, cfg.ParserURL, cfg.LogBodyLimit)

	// Install handler configuration as package-level values rather than
	// per-request context entries, so secrets are never copied into every
	// request (including public routes).
	handlers.SetJWTSecret(cfg.JWTSecret)
	handlers.SetTokenEncryptionKey(cfg.TokenEncryptionKey)
	handlers.SetAdminConfig(cfg.AdminEmails, cfg.AdminSetupToken)
	handlers.SetAppEnv(cfg.Env)
	handlers.SetCookieSecure(cfg.CookieSecure)

	// CORS. A wildcard origin is incompatible with credentialed requests, so
	// buildCORSConfig disables credentials whenever "*" is configured.
	r.Use(cors.New(buildCORSConfig(cfg.AllowedOrigins)))

	// API Routes
	api := r.Group("/api/v1")

	// Health check endpoint for Docker/orchestrators
	api.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Public API contract. openapi_test.go keeps this in lockstep with the
	// registered routes.
	api.GET("/openapi.yaml", serveOpenAPISpec)

	// Public: authentication
	api.POST("/auth/register", srv.Register)
	api.POST("/auth/login", srv.Login)
	api.POST("/auth/logout", srv.Logout)

	// Protected routes. RequireAuth validates the token; RenewSession then keeps
	// active sessions alive by sliding the access token forward (up to the
	// session's absolute deadline).
	api.Use(auth.RequireAuth(cfg.JWTSecret))
	api.Use(auth.RenewSession(cfg.JWTSecret, cfg.CookieSecure))
	{
		// Current session user (used to rehydrate the SPA from the httpOnly cookie).
		api.GET("/auth/me", srv.Me)

		// Accounts
		accounts := api.Group("/accounts")
		accounts.GET("", srv.GetAccounts)
		accounts.POST("", srv.CreateAccount)
		accounts.PUT("/:id", srv.UpdateAccount)
		accounts.DELETE("/:id", srv.DeleteAccount)
		accounts.GET("/:id/export", srv.ExportAccount)
		accounts.GET("/:id/billing-cycles", srv.GetBillingCycles)

		// Account Types (mutations are admin-only; the type list is shared
		// reference data that affects balance semantics for every user)
		accountTypes := api.Group("/account-types")
		accountTypes.GET("", srv.GetAccountTypes)
		accountTypes.POST("", auth.RequireAdmin(), srv.CreateAccountType)
		accountTypes.PUT("/:id", auth.RequireAdmin(), srv.UpdateAccountType)
		accountTypes.DELETE("/:id", auth.RequireAdmin(), srv.DeleteAccountType)

		// Category groups (base groups are read-only; custom groups are user-owned)
		api.GET("/groups", srv.GetGroups)
		api.POST("/groups", srv.CreateGroup)
		api.PUT("/groups/:id", srv.UpdateGroup)
		api.DELETE("/groups/:id", srv.DeleteGroup)

		// Categories (user-owned CRUD; global categories are admin-managed below)
		api.GET("/categories", srv.GetCategories)
		api.POST("/categories", srv.CreateCategory)
		api.PUT("/categories/:id", srv.UpdateCategory)
		api.DELETE("/categories/:id", srv.DeleteCategory)

		// Admin: global groups and global categories shared by every user
		admin := api.Group("/admin", auth.RequireAdmin())
		admin.POST("/groups", srv.CreateGlobalGroup)
		admin.POST("/categories", srv.CreateGlobalCategory)
		admin.PUT("/categories/:id", srv.UpdateGlobalCategory)
		admin.DELETE("/categories/:id", srv.DeleteGlobalCategory)

		// Transactions
		transactions := api.Group("/transactions")
		transactions.GET("", srv.GetTransactions)
		transactions.POST("", srv.CreateTransaction)
		transactions.PATCH("/:id", srv.UpdateTransaction)
		transactions.DELETE("/:id", srv.DeleteTransaction)
		transactions.POST("/import", srv.ImportTransactions)
		transactions.POST("/validate", srv.ValidateTransactions)
		transactions.POST("/bulk-categorize", srv.BulkCategorize)
		transactions.POST("/bulk-payee", srv.BulkUpdatePayee)
		transactions.POST("/bulk-billing-cycle", srv.BulkUpdateBillingCycle)
		transactions.POST("/bulk-loan", srv.BulkLinkLoan)
		transactions.POST("/bulk-delete", srv.BulkDeleteTransactions)

		// Statement parsing (forwards to the standalone parser service)
		api.POST("/statements/parse", srv.ParseStatement)
		api.GET("/statements/extractors", srv.ListStatementExtractors)

		// Paperless-ngx integration (per-user settings + manual pull)
		api.GET("/paperless/settings", srv.GetPaperlessSettings)
		api.PUT("/paperless/settings", srv.UpdatePaperlessSettings)
		api.GET("/paperless/documents", srv.ListPaperlessDocuments)
		api.GET("/paperless/documents/:id/file", srv.GetPaperlessDocumentFile)
		api.POST("/paperless/import", srv.ImportPaperlessDocument)

		// Rules
		api.GET("/rules", srv.GetRules)
		api.POST("/rules", srv.CreateRule)
		api.PUT("/rules/:id", srv.UpdateRule)
		api.DELETE("/rules/:id", srv.DeleteRule)
		api.POST("/rules/apply", srv.ApplyRules)

		// Payees
		api.GET("/payees", srv.GetPayees)
		api.POST("/payees", srv.CreatePayee)
		api.PUT("/payees/:id", srv.UpdatePayee)
		api.DELETE("/payees/:id", srv.DeletePayee)

		// Links
		api.GET("/links", srv.GetLinks)
		api.POST("/links", srv.CreateLink)
		api.POST("/links/bulk", srv.BulkCreateLinks)
		api.DELETE("/links/:id", srv.DeleteLink)
		api.POST("/links/bulk-delete", srv.BulkDeleteLinks)
		api.GET("/links/transfer-suggestions", srv.GetTransferSuggestions)
		api.GET("/links/cashback-suggestions", srv.GetCashbackSuggestions)

		// Dashboard
		api.GET("/dashboard/summary", srv.GetDashboardSummary)
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
