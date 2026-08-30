// Package handlers implements the HTTP handlers backing every /api/v1 route.
// Handlers read the authenticated user from the context (see auth.RequireAuth),
// validate request bodies, run SQL against db.Pool, and render JSON via the
// validation helpers. This package deliberately keeps its dependencies on a
// single db.DBPool global so the whole surface can be tested with pgxmock.
package handlers

import (
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// adminEmailsFromContext returns the configured admin email allowlist, or nil
// when the router did not set it (e.g. unit tests).
func adminEmailsFromContext(c *gin.Context) []string {
	if v, ok := c.Get("adminEmails"); ok {
		if emails, ok := v.([]string); ok {
			return emails
		}
	}
	return nil
}

// isAdminEmail reports whether email (already trimmed/lowercased by callers)
// appears in the configured admin allowlist.
func isAdminEmail(email string, adminEmails []string) bool {
	for _, e := range adminEmails {
		if strings.EqualFold(strings.TrimSpace(e), email) {
			return true
		}
	}
	return false
}

// setupTokenMatches compares the supplied setup token against the configured
// one in constant time so a wrong guess doesn't leak timing information about
// its length or prefix. Returns false when either side is empty.
func setupTokenMatches(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// Register creates a new user account. The role defaults to 'user'; a
// registration whose email is in the admin allowlist is granted 'admin' only
// when the request carries the operator-owned ADMIN_SETUP_TOKEN (admin-listed
// addresses are otherwise refused so they can't be squatted by a registrant
// who merely knows the address). The password is bcrypt-hashed and the stock
// default categories are seeded before returning a fresh JWT.
func Register(c *gin.Context) {
	var req models.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		log.Printf("Error hashing password in Register: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Emails are always stored lowercase so identity is case-insensitive: the
	// case-sensitive UNIQUE constraint on users.email then rejects any
	// case-variant duplicate at insert time (23505 -> 409), and logins can
	// compare email = $1 directly against the plain index.
	email := strings.ToLower(strings.TrimSpace(req.Email))

	role := "user"
	if isAdminEmail(email, adminEmailsFromContext(c)) {
		// Admin-listed identities are reserved: registering one without the
		// setup token is refused outright (rather than falling back to a
		// 'user' role) so an unverified third party can neither take the
		// address nor self-promote.
		if !setupTokenMatches(req.SetupToken, c.GetString("adminSetupToken")) {
			validation.RespondError(c, "registering an admin email requires a valid admin setup token", http.StatusForbidden)
			return
		}
		role = "admin"
	}

	var user models.User
	err = db.Pool.QueryRow(c,
		`INSERT INTO users (email, password_hash, role) VALUES ($1, $2, $3)
			 RETURNING id, email, role`,
		email, hash, role,
	).Scan(&user.ID, &user.Email, &user.Role)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			validation.RespondError(c, "an account with this email already exists", http.StatusConflict)
			return
		}
		log.Printf("Error in Register: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	db.SeedDefaultCategories(c, user.ID)

	token, err := auth.GenerateToken(user.ID, user.Role, c.MustGet("jwtSecret").(string))
	if err != nil {
		log.Printf("Error generating token in Register: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusCreated, models.AuthResponse{Token: token, User: user})
}

// Login verifies the email/password against the users table and returns a fresh
// JWT on success. Failed lookups and mismatched passwords both return a generic
// 401 so the response doesn't reveal which accounts exist.
func Login(c *gin.Context) {
	var req models.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	var user models.User
	var passwordHash string
	// Registered emails are always stored lowercase, so a direct equality
	// lookup is exact, uses the unique index on users.email, and can never
	// match more than one row per identity.
	email := strings.ToLower(strings.TrimSpace(req.Email))
	err := db.Pool.QueryRow(c,
		"SELECT id, email, password_hash, role FROM users WHERE email = $1",
		email,
	).Scan(&user.ID, &user.Email, &passwordHash, &user.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "invalid email or password", http.StatusUnauthorized)
		return
	}
	if err != nil {
		log.Printf("Error in Login (query): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if !auth.CheckPassword(passwordHash, req.Password) {
		validation.RespondError(c, "invalid email or password", http.StatusUnauthorized)
		return
	}

	token, err := auth.GenerateToken(user.ID, user.Role, c.MustGet("jwtSecret").(string))
	if err != nil {
		log.Printf("Error generating token in Login: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, models.AuthResponse{Token: token, User: user})
}
