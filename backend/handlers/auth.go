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

// Registration, login, refresh, logout and the "who am I" probe — the only
// routes in the API a caller may reach without a session.
//
// The access token is a short-lived JWT in an httpOnly cookie; the refresh token
// is server-recorded, so this is not a stateless scheme:
//
//   - POST /auth/refresh rotates both the cookie and the `refresh_tokens` row
//     (SHA-256 of the token only), grouped into a rotation `family_id`.
//   - Presenting an already-rotated token is *reuse*, and reuse revokes the whole
//     family. Logout revokes the family too.
//   - Refresh does not extend the original session deadline — a family dies when
//     it was created plus its window, however many times it is rotated.
//   - Login and Register write a `sessions` row first and name it in both tokens
//     (`sid`), so a session can be listed and revoked by id. The row's id IS the
//     rotation family's id.
//   - Refreshing stamps that row's `last_seen`, and rotation names it in the
//     tokens it hands out, so a session that predates the `sessions` table
//     acquires its id on its first rotation. Ending the session — logout, or a
//     detected replay — revokes the row, which is what stops the outstanding
//     access token and not merely the next refresh.
//
// So no code path here may mint an access token without a live row for the
// presented refresh token. That single rule is what makes the reuse detection
// meaningful, and the TUI and MCP clients both depend on it: they hold no
// cookie jar (see AGENTS.md on client/auth.go) and replay once after a
// single-flight refresh.
//
// The credential endpoints are throttled per client IP, and separately carry a
// per-identity budget that is charged *only* for failed credentials. That split
// is deliberate: an identity budget charged on success could lock a real user out
// of their own account, while the IP budget alone would let one attacker spread
// guesses across IPs. Neither replaces the body cap below.

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

	// One session per login, named by both tokens. The row is written before
	// either token is handed out: a credential naming a session the server has
	// no row for could never be listed or revoked, which is the same
	// "no token without a live row" rule the refresh side already holds. A
	// failure at any step below answers 500 with no cookie set.
	sessionID := uuid.New()
	session, err := auth.NewSession(user.ID, user.Role, jwtSecret, sessionID)
	if err != nil {
		slog.Error("generating tokens in Register", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := srv.recordSession(c, user.ID, session); err != nil {
		slog.Error("Register (recording session)", slog.String("error", err.Error()))
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

	// One session per login, named by both tokens and written before either is
	// handed out, for the same reason as in Register: a credential naming a
	// session the server has no row for could never be listed or revoked.
	sessionID := uuid.New()
	session, err := auth.NewSession(user.ID, user.Role, jwtSecret, sessionID)
	if err != nil {
		slog.Error("generating tokens in Login", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := srv.recordSession(c, user.ID, session); err != nil {
		slog.Error("Login (recording session)", slog.String("error", err.Error()))
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

// recordSession writes the revocable session row the freshly minted tokens
// name. Its id is the rotation family's id (sessions.id IS refresh_tokens'
// family_id), so it is written before startRefreshFamily keys the family on the
// same value. The request's user agent is what the session list shows the user
// to identify a device; expires_at is the session's absolute deadline, the same
// value stored on the refresh row.
func (srv *Server) recordSession(c *gin.Context, userID uuid.UUID, session auth.Session) error {
	_, err := srv.db.Exec(c,
		`INSERT INTO sessions (id, user_id, user_agent, expires_at) VALUES ($1, $2, $3, $4)`,
		session.ID, userID, c.Request.UserAgent(), session.ExpiresAt,
	)
	return err
}

// startRefreshFamily records a freshly issued refresh token as the first row of
// a new rotation family. Only the token's hash is stored, so the table can never
// be replayed against the API. The family is keyed on the session id rather than
// a fresh one, because sessions.id IS refresh_tokens.family_id.
func (srv *Server) startRefreshFamily(c *gin.Context, userID uuid.UUID, session auth.Session) error {
	_, err := srv.db.Exec(c,
		`INSERT INTO refresh_tokens (user_id, token_hash, family_id, expires_at) VALUES ($1, $2, $3, $4)`,
		userID, auth.HashRefreshToken(session.RefreshToken), session.ID, session.ExpiresAt,
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

// revokeRefreshFamily revokes every live token of a session's rotation family,
// so no refresh token in it can be spent again. It is one half of endSession; see
// that function for why the two halves are never separated.
func (srv *Server) revokeRefreshFamily(c *gin.Context, familyID uuid.UUID) error {
	_, err := srv.db.Exec(c,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL`,
		familyID,
	)
	return err
}

// revokeSession ends the session row itself. Revoking the refresh family alone
// stops only the *next* refresh: the access token already in the holder's hand
// stays good until it expires, up to a quarter of an hour after the chain was
// cut. This is the write RequireAuth reads, so it is what takes that token down
// with the family. Already-revoked rows are left alone, keeping the original
// revocation time rather than overwriting it with a later one.
func (srv *Server) revokeSession(c *gin.Context, sessionID uuid.UUID) error {
	_, err := srv.db.Exec(c,
		`UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`,
		sessionID,
	)
	return err
}

// endSession revokes both halves of one identified session — the family and the
// row — for callers that have already decided the session is over by resolving
// it: Logout (which also clears the cookies either way) and Refresh's reuse and
// raced-rotation detection (which refuse the request either way). Both
// statements run whatever the first does, because a failure of one is logged
// rather than propagated: returning early would leave the other half unrevoked.
//
// The session routes in session.go revoke the same two halves for the same
// reason but cannot route through here — RevokeSession's row write is scoped to
// the caller's own user id (endSession's is not, and by design cannot be: it is
// reached only with a family resolved from the caller's own cookie), and the
// bulk routes fan the family revoke out over every family they just revoked
// rather than taking one. What the pairing buys is asserted per route there.
func (srv *Server) endSession(c *gin.Context, familyID uuid.UUID, where string) {
	if err := srv.revokeRefreshFamily(c, familyID); err != nil {
		slog.Error(where+": revoking refresh family", slog.String("error", err.Error()))
	}
	if err := srv.revokeSession(c, familyID); err != nil {
		slog.Error(where+": revoking session", slog.String("error", err.Error()))
	}
}

// touchSession moves the session's last_seen stamp forward. It is the only
// write to sessions outside login and revocation, and it rides a request that
// already writes rather than being placed on the read path: RequireAuth runs on
// every authenticated request, so stamping there would turn every authenticated
// GET into an UPDATE.
func (srv *Server) touchSession(c *gin.Context, sessionID uuid.UUID) error {
	_, err := srv.db.Exec(c, `UPDATE sessions SET last_seen = now() WHERE id = $1`, sessionID)
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
//
// Refreshing also records that the session is alive: it stamps last_seen and
// names the session in the tokens it mints. Both are keyed on the family
// resolved from the presented token's hash rather than on the token's own `sid`,
// because that family is what sessions.id holds — and a token minted before the
// sessions table existed carries no sid at all, making the family the only
// identity available for it.
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
		srv.endSession(c, familyID, "Refresh (reuse detected)")
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

	// The session is the rotation family this token's hash resolved to —
	// sessions.id IS refresh_tokens.family_id — and that, not whatever the token
	// carries, is what the tokens minted below must name. A token predating the
	// sessions table has no sid of its own, so this is where it acquires one:
	// from this rotation the session can be revoked and its last_seen kept.
	claims.SessionID = familyID

	// last_seen is display data: the session list reads it and nothing enforces
	// on it, so a failure to move the stamp is logged and the refresh continues.
	// Ending a live session because a timestamp did not move would trade a stale
	// label in the UI for a user who is suddenly logged out.
	if err := srv.touchSession(c, familyID); err != nil {
		slog.Error("Refresh (recording session activity)", slog.String("error", err.Error()))
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
		// the session is concerned. Ending the session takes the winner's
		// brand-new refresh token with it, and its access token too, which is
		// the safe side of that call.
		srv.endSession(c, familyID, "Refresh (raced rotation)")
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

// Logout revokes the session — its rotation family and its session row — and
// clears both session cookies, so the session cannot be resumed with a copy of
// either cookie. It is unauthenticated on purpose so an expired or invalid
// cookie can still be removed. Revocation is best-effort: a database failure must
// not stop the browser's cookies from being cleared, so it is logged rather than
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
			srv.endSession(c, familyID, "Logout")
		case errors.Is(err, pgx.ErrNoRows):
			// Never issued, or already pruned: there is nothing to revoke.
		default:
			slog.Error("Logout (session lookup)", slog.String("error", err.Error()))
		}
	}

	auth.ClearAuthCookies(c, cookieSecure)
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}
