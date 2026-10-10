package api

import "context"

// Auth endpoints. The tokens never appear in a response body: they arrive as
// Set-Cookie, which the client records on every response (Client.captureSession
// in client.go).

// Login signs in and returns the authenticated user.
func (c *Client) Login(ctx context.Context, email, password string) (User, error) {
	body := struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}{Email: email, Password: password}

	res, err := do[struct {
		User User `json:"user"`
	}](ctx, c, post("/auth/login").withoutAuth().withJSON(body))
	return res.User, err
}

// Register creates an account and signs it in. SetupToken is the operator-owned
// bootstrap secret, required only when the email is listed in ADMIN_EMAILS.
func (c *Client) Register(ctx context.Context, email, password, setupToken string) (User, error) {
	body := struct {
		Email      string `json:"email"`
		Password   string `json:"password"`
		SetupToken string `json:"setupToken,omitempty"`
	}{Email: email, Password: password, SetupToken: setupToken}

	res, err := do[struct {
		User User `json:"user"`
	}](ctx, c, post("/auth/register").withoutAuth().withJSON(body))
	return res.User, err
}

// Me returns the session's user, which the TUI calls on startup to rehydrate
// state from a still-valid cookie.
func (c *Client) Me(ctx context.Context) (User, error) {
	return do[User](ctx, c, get("/auth/me"))
}

// RefreshSession trades the refresh token for a new access token. It is
// normally implicit (a 401 triggers it and replays the request); the UI calls
// it explicitly only to warn before a long-running session expires.
func (c *Client) RefreshSession(ctx context.Context) error {
	_, err := c.refreshAccess(ctx, c.generation())
	return err
}

// Logout revokes the server-side refresh-session family and clears the local
// session. The refresh cookie must be sent explicitly: the backend identifies
// the family to revoke from that cookie, not from the access token.
func (c *Client) Logout(ctx context.Context) error {
	refresh := c.refreshToken()
	defer c.ClearSession()

	r := post("/auth/logout").withoutAuth()
	if refresh != "" {
		r = r.withCookie(RefreshCookieName + "=" + refresh)
	}
	_, err := do[MessageResult](ctx, c, r)
	return err
}

// ListSessions returns the account's own signed-in devices, most recently seen
// first, with Current set on the one this client's token names.
func (c *Client) ListSessions(ctx context.Context) ([]Session, error) {
	return do[[]Session](ctx, c, get("/auth/sessions"))
}

// RevokeSession ends one session by id. It is idempotent server-side: a session
// that is already revoked, or that does not exist, is reported the same way a
// fresh one is.
func (c *Client) RevokeSession(ctx context.Context, id string) error {
	_, err := do[MessageResult](ctx, c, del("/auth/sessions/"+pathEscape(id)))
	return err
}

// RevokeOtherSessions ends every session except the one making the request —
// "sign out everywhere else", which leaves the calling client signed in.
func (c *Client) RevokeOtherSessions(ctx context.Context) error {
	_, err := do[MessageResult](ctx, c, del("/auth/sessions"))
	return err
}

// RevokeAllSessions ends every session including the caller's own. The request
// succeeds, but the access token it was made with stops being accepted on the
// next call, which the client reports through its normal no-session path rather
// than through this error.
func (c *Client) RevokeAllSessions(ctx context.Context) error {
	_, err := do[MessageResult](ctx, c, del("/auth/sessions/current"))
	return err
}

// Health checks that the API is reachable, so the TUI can say "backend down"
// instead of showing an empty transaction list.
func (c *Client) Health(ctx context.Context) (HealthResult, error) {
	return do[HealthResult](ctx, c, get("/health"))
}
