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
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

// maxAuthBodyBytes caps the JSON body of the public credential endpoints. An
// email plus a password is a few hundred bytes; 4 KiB leaves room for any
// reasonable sign-up payload while keeping a chunked, slowly-growing body from
// allocating unbounded memory before the throttle runs. The password rule
// (maxbytes=72) is applied by the validator *after* decoding, so it cannot
// bound the allocation on its own.
const maxAuthBodyBytes = 4 << 10

// limitAuthBody wraps the request body in an http.MaxBytesReader, so decoding
// stops at the cap instead of buffering whatever the caller sends. It must run
// before any binding.
func limitAuthBody(c *gin.Context) {
	if c.Request.Body != nil {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAuthBodyBytes)
	}
}

// respondBindError answers a failed binding, mapping the body cap to a clean 413
// instead of the 400 a malformed body gets.
func respondBindError(c *gin.Context, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		validation.RespondError(c, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	validation.RespondBindError(c, err)
}

// Register creates a new user account. The role defaults to 'user'; a
// registration whose email is in the admin allowlist is granted 'admin' only
// when the request carries the operator-owned ADMIN_SETUP_TOKEN. The password is
// bcrypt-hashed and the stock default categories are seeded before starting a
// session (access + refresh token cookies, the latter recorded server-side so
// the session can be rotated and revoked).
func (srv *Server) Register(c *gin.Context) {
	limitAuthBody(c)
	var req models.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBindError(c, err)
		return
	}

	// Throttle before the expensive bcrypt hash so brute-force attempts cannot
	// pin CPU or exhaust the database.
	if !allowAuthAttempt(c, "register") {
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
		// setup token is refused (rather than falling back to a 'user' role) so
		// an unverified third party can neither take the address nor
		// self-promote.
		if !setupTokenMatches(req.SetupToken, adminSetupToken) {
			// The refusal is deliberately indistinguishable from the
			// duplicate-address branch below — same status, same envelope, no
			// mention of the setup token. Answering differently (or naming the
			// protection) confirmed to an unauthenticated caller that an address
			// is reserved as an admin identity, which is targeting information
			// for phishing, and disclosed that ADMIN_SETUP_TOKEN is configured.
			// The real reason stays in the server log.
			slog.Warn("registration refused for a reserved admin email", slog.String("email", email))
			validation.RespondError(c, "an account with this email already exists", http.StatusConflict)
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

	session, err := auth.NewSession(user.ID, user.Role, jwtSecret)
	if err != nil {
		slog.Error("generating tokens in Register", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Record the refresh token before handing out the cookie: a session the
	// server cannot look up could never be refreshed or revoked.
	if err := srv.startRefreshFamily(c, user.ID, session); err != nil {
		slog.Error("Register (storing session)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	auth.SetAuthCookies(c, session.AccessToken, session.RefreshToken, cookieSecure)

	c.JSON(http.StatusCreated, models.AuthResponse{User: user})
}

// Login verifies the email/password against the users table and starts a
// session on success. Failed lookups and mismatched passwords both return a
// generic 401 so the response doesn't reveal which accounts exist.
func (srv *Server) Login(c *gin.Context) {
	limitAuthBody(c)
	var req models.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBindError(c, err)
		return
	}

	// Throttle by client IP before touching the database or running bcrypt,
	// slowing online brute force without revealing whether the account exists.
	// Per-identity throttling happens after the credential check, on failure
	// only, so a correct password is never blocked (see recordAuthFailure).
	if !allowAuthAttempt(c, "login") {
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
		if !recordAuthFailure(c, "login", email) {
			return
		}
		validation.RespondError(c, "invalid email or password", http.StatusUnauthorized)
		return
	}
	if err != nil {
		slog.Error("Login (query)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if !auth.CheckPassword(passwordHash, req.Password) {
		if !recordAuthFailure(c, "login", email) {
			return
		}
		validation.RespondError(c, "invalid email or password", http.StatusUnauthorized)
		return
	}

	// The credentials are correct, so any failures charged against this
	// identity were somebody else's guesses: refund them.
	refundAuthFailures("login", email)

	session, err := auth.NewSession(user.ID, user.Role, jwtSecret)
	if err != nil {
		slog.Error("generating tokens in Login", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := srv.startRefreshFamily(c, user.ID, session); err != nil {
		slog.Error("Login (storing session)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	auth.SetAuthCookies(c, session.AccessToken, session.RefreshToken, cookieSecure)

	c.JSON(http.StatusOK, models.AuthResponse{User: user})
}

// errRefreshReused reports that a refresh token was rotated by a concurrent
// request between the read that found it live and the update that spends it.
var errRefreshReused = errors.New("refresh token already rotated")

// startRefreshFamily records a freshly issued refresh token as the first row of
// a new rotation family. Only the token's hash is stored, so the table can never
// be replayed against the API.
func (srv *Server) startRefreshFamily(c *gin.Context, userID uuid.UUID, session auth.Session) error {
	_, err := srv.db.Exec(c,
		`INSERT INTO refresh_tokens (user_id, token_hash, family_id, expires_at) VALUES ($1, $2, $3, $4)`,
		userID, auth.HashRefreshToken(session.RefreshToken), uuid.New(), session.ExpiresAt,
	)
	return err
}

// rotateRefreshToken spends the presented refresh token and stores its
// successor in the same family, in one transaction so a failure cannot leave
// the session with neither token. It reports errRefreshReused when the token was
// already spent by the time the update ran.
func (srv *Server) rotateRefreshToken(c *gin.Context, tokenID, familyID, userID uuid.UUID, refreshToken string, expiresAt time.Time) error {
	return db.WithTx(c, srv.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(c,
			`UPDATE refresh_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`,
			tokenID,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errRefreshReused
		}

		var newID uuid.UUID
		err = tx.QueryRow(c,
			`INSERT INTO refresh_tokens (user_id, token_hash, family_id, expires_at) VALUES ($1, $2, $3, $4) RETURNING id`,
			userID, auth.HashRefreshToken(refreshToken), familyID, expiresAt,
		).Scan(&newID)
		if err != nil {
			return err
		}

		_, err = tx.Exec(c, `UPDATE refresh_tokens SET replaced_by = $2 WHERE id = $1`, tokenID, newID)
		return err
	})
}

// revokeRefreshFamily revokes every live token of a session's rotation family.
// The caller decides what a failure means: Logout proceeds anyway (the cookies
// are cleared regardless) while reuse detection reports it and still refuses the
// request.
func (srv *Server) revokeRefreshFamily(c *gin.Context, familyID uuid.UUID) error {
	_, err := srv.db.Exec(c,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL`,
		familyID,
	)
	return err
}

// Refresh exchanges a valid refresh-token cookie for a fresh access token and a
// rotated refresh token. It is public (the access token is expected to have
// expired) but requires the long-lived refresh cookie, which the browser only
// sends to the auth endpoints. Rotation re-issues both cookies, so the browser
// always holds the current token of the family, and the replaced one is marked
// spent: a revoked token arriving again means a copy leaked, which revokes the
// whole family. The presented token's expiry is the session's absolute deadline,
// so rotating never extends a session; it only re-arms the access token.
// A missing, unknown or invalid refresh token clears both cookies so the browser
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

	// The token's session state is authoritative: a token this server never
	// issued (or one whose row has been pruned) is not a session, and a token
	// whose row is revoked has already been replaced. Liveness is selected as a
	// predicate rather than the timestamp (which is nullable and therefore
	// awkward to scan) because only "has this been spent" matters here.
	var (
		tokenID  uuid.UUID
		userID   uuid.UUID
		familyID uuid.UUID
		live     bool
	)
	err = srv.db.QueryRow(c,
		`SELECT id, user_id, family_id, revoked_at IS NULL FROM refresh_tokens WHERE token_hash = $1`,
		auth.HashRefreshToken(tokenString),
	).Scan(&tokenID, &userID, &familyID, &live)
	if errors.Is(err, pgx.ErrNoRows) {
		auth.ClearAuthCookies(c, cookieSecure)
		validation.RespondAuthError(c, "session expired; please log in again")
		return
	}
	if err != nil {
		slog.Error("Refresh (session lookup)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if !live {
		// Reuse detection. Rotation replaces the cookie in the browser, so the
		// only holder of this exact value is whoever had it before — the
		// legitimate client would present the successor. A replay therefore
		// means the token leaked, and the whole chain (including the token the
		// thief would try next) is revoked.
		slog.Warn("refresh token reuse detected; revoking session family",
			slog.String("user_id", userID.String()),
			slog.String("family_id", familyID.String()),
		)
		if err := srv.revokeRefreshFamily(c, familyID); err != nil {
			slog.Error("Refresh (revoking reused family)", slog.String("error", err.Error()))
		}
		auth.ClearAuthCookies(c, cookieSecure)
		validation.RespondAuthError(c, "session expired; please log in again")
		return
	}

	// Re-read the role from the database rather than trusting the refresh
	// token's copy: a demoted admin would otherwise keep re-minting admin
	// access until the session ended.
	var role string
	err = srv.db.QueryRow(c, "SELECT role FROM users WHERE id = $1", userID).Scan(&role)
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

	newRefreshToken, err := auth.RenewRefresh(claims, role, jwtSecret)
	if err != nil {
		// Same condition as above: the replacement token is capped at the
		// presented token's expiry, so both fail together at the deadline.
		auth.ClearAuthCookies(c, cookieSecure)
		validation.RespondAuthError(c, "session expired; please log in again")
		return
	}

	err = srv.rotateRefreshToken(c, tokenID, familyID, userID, newRefreshToken, claims.ExpiresAt.Time)
	if errors.Is(err, errRefreshReused) {
		// Two requests raced the same token: the loser is a replay as far as
		// the session is concerned. Revoking the family (which includes the
		// winner's brand-new token) is the safe side of that call.
		if err := srv.revokeRefreshFamily(c, familyID); err != nil {
			slog.Error("Refresh (revoking raced family)", slog.String("error", err.Error()))
		}
		auth.ClearAuthCookies(c, cookieSecure)
		validation.RespondAuthError(c, "session expired; please log in again")
		return
	}
	if err != nil {
		slog.Error("Refresh (rotation)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	auth.SetAuthCookies(c, accessToken, newRefreshToken, cookieSecure)
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

// Logout revokes the presented refresh token's whole rotation family and clears
// both session cookies, so the session cannot be resumed with a copy of the
// cookie. It is unauthenticated on purpose so an expired or invalid cookie can
// still be removed. Revocation is best-effort: a database failure must not stop
// the browser's cookies from being cleared, so it is logged rather than
// reported, and the caller's next refresh fails against the same fault anyway.
func (srv *Server) Logout(c *gin.Context) {
	if tokenString, err := c.Cookie(auth.RefreshCookieName); err == nil && tokenString != "" {
		var familyID uuid.UUID
		err := srv.db.QueryRow(c,
			`SELECT family_id FROM refresh_tokens WHERE token_hash = $1`,
			auth.HashRefreshToken(tokenString),
		).Scan(&familyID)
		switch {
		case err == nil:
			if err := srv.revokeRefreshFamily(c, familyID); err != nil {
				slog.Error("Logout (revoking session family)", slog.String("error", err.Error()))
			}
		case errors.Is(err, pgx.ErrNoRows):
			// Never issued, or already pruned: there is nothing to revoke.
		default:
			slog.Error("Logout (session lookup)", slog.String("error", err.Error()))
		}
	}

	auth.ClearAuthCookies(c, cookieSecure)
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}
