package handlers

// Handler-level runtime configuration. These values are installed once at
// startup by setupRouter and reset by tests. They live as package values rather
// than gin context keys on purpose: context entries are readable by every
// middleware and route handler (including public ones), so copying secrets into
// each request is needless exposure.

var (
	jwtSecret          string
	adminEmails        []string
	adminSetupToken    string
	appEnv             string
	cookieSecure       bool
	tokenEncryptionKey string
)

// SetJWTSecret installs the secret used to sign auth tokens.
func SetJWTSecret(secret string) { jwtSecret = secret }

// SetTokenEncryptionKey installs the key used to encrypt secrets at rest.
func SetTokenEncryptionKey(key string) { tokenEncryptionKey = key }

// SetAdminConfig installs the admin email allowlist and the operator-owned
// bootstrap token required to register an admin-listed address.
func SetAdminConfig(emails []string, setupToken string) {
	adminEmails = emails
	adminSetupToken = setupToken
}

// SetAppEnv installs the runtime environment name (e.g. "production"), which
// gates production-only behavior such as the Paperless SSRF guard.
func SetAppEnv(env string) { appEnv = env }

// SetCookieSecure controls the Secure flag on the auth cookie.
func SetCookieSecure(secure bool) { cookieSecure = secure }
