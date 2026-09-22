// Package handlers implements the HTTP handlers backing every /api/v1 route.
// Handlers read the authenticated user from the context (see auth.RequireAuth),
// validate request bodies, run SQL against the Server's database pool, and render JSON via the
// validation helpers. This package deliberately keeps its dependencies on a
// single db.DBPool global so the whole surface can be tested with pgxmock.
package handlers

import (
	"crypto/subtle"
	"errors"
	"log/slog"
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
// default categories are seeded before starting a session (access + refresh
// token cookies).
func (srv *Server) Register(c *gin.Context) {
	var req models.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	// Throttle before the expensive bcrypt hash so brute-force attempts cannot
	// pin CPU or exhaust the database.
	if !allowAuthAttempt(c, "register", req.Email) {
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		slog.Error("hashing password in Register", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Emails are always stored lowercase so identity is case-insensitive: the
	// case-sensitive UNIQUE constraint on users.email then rejects any
	// case-variant duplicate at insert time (23505 -> 409), and logins can
	// compare email = $1 directly against the plain index.
	email := strings.ToLower(strings.TrimSpace(req.Email))

	role := "user"
	if isAdminEmail(email, adminEmails) {
		// Admin-listed identities are reserved: registering one without the
		// setup token is refused outright (rather than falling back to a
		// 'user' role) so an unverified third party can neither take the
		// address nor self-promote.
		if !setupTokenMatches(req.SetupToken, adminSetupToken) {
			validation.RespondError(c, "registering an admin email requires a valid admin setup token", http.StatusForbidden)
			return
		}
		role = "admin"
	}

	// Create the user and seed its default categories in one transaction, so a
	// transient seeding failure rolls the user back instead of leaving an
	// account that cannot repair its missing defaults by registering again.
	tx, err := srv.db.Begin(c)
	if err != nil {
		slog.Error("Register (begin)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(c)

	var user models.User
	err = tx.QueryRow(c,
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
		slog.Error("Register", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := db.SeedDefaultCategories(c, tx, user.ID); err != nil {
		slog.Error("Register (seeding categories)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(c); err != nil {
		slog.Error("Register (commit)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	accessToken, refreshToken, err := auth.NewSession(user.ID, user.Role, jwtSecret)
	if err != nil {
		slog.Error("generating tokens in Register", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	auth.SetAuthCookies(c, accessToken, refreshToken, cookieSecure)

	c.JSON(http.StatusCreated, models.AuthResponse{User: user})
}

// Login verifies the email/password against the users table and starts a
// session on success. Failed lookups and mismatched passwords both return a
// generic 401 so the response doesn't reveal which accounts exist.
func (srv *Server) Login(c *gin.Context) {
	var req models.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	// Throttle by IP and by account before touching the database or running
	// bcrypt, slowing online brute force without revealing whether the account
	// exists.
	if !allowAuthAttempt(c, "login", req.Email) {
		return
	}

	var user models.User
	var passwordHash string
	// Registered emails are always stored lowercase, so a direct equality
	// lookup is exact, uses the unique index on users.email, and can never
	// match more than one row per identity.
	email := strings.ToLower(strings.TrimSpace(req.Email))
	err := srv.db.QueryRow(c,
		"SELECT id, email, password_hash, role FROM users WHERE email = $1",
		email,
	).Scan(&user.ID, &user.Email, &passwordHash, &user.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		// Spend the bcrypt work a wrong-password attempt would: answering
		// immediately made the response time tell an unknown email from a known
		// one, enumerating accounts despite the identical 401 body.
		auth.EqualizePasswordTiming(req.Password)
		validation.RespondError(c, "invalid email or password", http.StatusUnauthorized)
		return
	}
	if err != nil {
		slog.Error("Login (query)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if !auth.CheckPassword(passwordHash, req.Password) {
		validation.RespondError(c, "invalid email or password", http.StatusUnauthorized)
		return
	}

	accessToken, refreshToken, err := auth.NewSession(user.ID, user.Role, jwtSecret)
	if err != nil {
		slog.Error("generating tokens in Login", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	auth.SetAuthCookies(c, accessToken, refreshToken, cookieSecure)

	c.JSON(http.StatusOK, models.AuthResponse{User: user})
}

// Refresh exchanges a valid refresh-token cookie for a fresh access token. It is
// public (the access token is expected to have expired) but requires the
// long-lived refresh cookie, which the browser only sends to the auth
// endpoints. The refresh token's expiry is the session's absolute deadline, so
// refreshing never extends a session; it only re-arms the short-lived access
// token. A missing or invalid refresh token clears both cookies so the browser
// stops sending a dead session.
func (srv *Server) Refresh(c *gin.Context) {
	tokenString, err := c.Cookie(auth.RefreshCookieName)
	if err != nil || tokenString == "" {
		validation.RespondAuthError(c, "missing refresh token")
		return
	}

	claims, err := auth.ParseToken(tokenString, jwtSecret, auth.TokenTypeRefresh)
	if err != nil {
		auth.ClearAuthCookies(c, cookieSecure)
		validation.RespondAuthError(c, "session expired; please log in again")
		return
	}

	// Re-read the role from the database rather than trusting the refresh
	// token's copy: there is no other revocation path, so a demoted admin would
	// otherwise keep re-minting admin access until the refresh token expired.
	var role string
	err = srv.db.QueryRow(c, "SELECT role FROM users WHERE id = $1", claims.UserID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		// The account is gone; end the session instead of minting for it.
		auth.ClearAuthCookies(c, cookieSecure)
		validation.RespondAuthError(c, "session expired; please log in again")
		return
	}
	if err != nil {
		slog.Error("Refresh (role lookup)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	accessToken, err := auth.RenewAccess(claims, role, jwtSecret)
	if err != nil {
		// The refresh token is structurally valid but the session has reached
		// its absolute deadline (RenewAccess refuses to mint a token whose
		// expiry is already in the past).
		auth.ClearAuthCookies(c, cookieSecure)
		validation.RespondAuthError(c, "session expired; please log in again")
		return
	}

	auth.SetAccessCookie(c, accessToken, cookieSecure)
	c.JSON(http.StatusOK, gin.H{"message": "token refreshed"})
}

// Me returns the authenticated user for the session cookie. The frontend calls
// it on mount to rehydrate auth state, since the JWT cookie is httpOnly and
// therefore unreadable from JavaScript.
func (srv *Server) Me(c *gin.Context) {
	userID := auth.GetUserID(c)
	var user models.User
	err := srv.db.QueryRow(c,
		"SELECT id, email, role FROM users WHERE id = $1",
		userID,
	).Scan(&user.ID, &user.Email, &user.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondAuthError(c, "user not found")
		return
	}
	if err != nil {
		slog.Error("Me", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusOK, user)
}

// Logout clears both session cookies. It is unauthenticated on purpose so an
// expired or invalid cookie can still be removed.
func (srv *Server) Logout(c *gin.Context) {
	auth.ClearAuthCookies(c, cookieSecure)
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}
