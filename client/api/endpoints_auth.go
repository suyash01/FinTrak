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

// Logout clears the session server-side and locally.
func (c *Client) Logout(ctx context.Context) error {
	defer c.ClearSession()
	_, err := do[MessageResult](ctx, c, post("/auth/logout"))
	return err
}

// Health checks that the API is reachable, so the TUI can say "backend down"
// instead of showing an empty transaction list.
func (c *Client) Health(ctx context.Context) (HealthResult, error) {
	return do[HealthResult](ctx, c, get("/health"))
}
