// Package sshd serves the FinTrak TUI over SSH.
//
// It is one of the two ways to run the client (the other is a local terminal)
// and the two share everything below the entrypoint: the same bubbletea model,
// the same API client, the same screens. Only the door differs.
//
// The door is deliberately narrow. In password mode the SSH password IS the
// FinTrak password: the credentials are exchanged for a session against the API
// during authentication, and the resulting tokens are parked on the connection
// context so the session starts signed in. In public-key mode a key only gets
// you through the door — a key cannot be traded for an API session — so the TUI
// shows its own sign-in screen.
//
// Everything the SSH protocol offers beyond an interactive terminal is refused:
// forwarding (local, reverse and agent), subsystems such as sftp, and any other
// channel request. Sessions are capped, authentication attempts are throttled
// per remote address, and idle and absolute timeouts are enforced, because this
// is the one component of FinTrak that is meant to face the open internet.
package sshd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	wishtea "github.com/charmbracelet/wish/bubbletea"

	"github.com/fintrak/tui/internal/api"
	"github.com/fintrak/tui/internal/ui"
)

// AuthMode selects which SSH authentication methods the door accepts.
type AuthMode string

// Supported authentication modes.
const (
	// AuthPassword authenticates against the FinTrak API: the SSH username is the
	// account email and the SSH password is the account password, so the session
	// begins signed in. This is the mode that makes `ssh -p 2222 host` a complete
	// FinTrak client.
	AuthPassword AuthMode = "password"
	// AuthPublicKey accepts an authorized SSH key. The key only opens the door;
	// the TUI still asks for the account credentials, since a public key cannot
	// be exchanged for an API session.
	AuthPublicKey AuthMode = "key"
	// AuthAny accepts a public key if authorized, or the account credentials.
	AuthAny AuthMode = "any"
)

// clientContextKey is where an authenticated session's API client is parked on
// the SSH connection context between authentication and the session handler.
type clientContextKey struct{}

// Config configures the SSH door.
type Config struct {
	// Addr is the listen address, e.g. ":2222".
	Addr string
	// HostKeyPath is the private host key. It is generated on first start if it
	// does not exist; keep it stable, because a new key makes every client see a
	// host-key-changed warning (and a strict client refuse to connect).
	HostKeyPath string
	// AuthorizedKeysPath is the authorized_keys file for public-key mode.
	AuthorizedKeysPath string
	// AuthMode selects the accepted authentication methods.
	AuthMode AuthMode
	// APIURL is the FinTrak API base URL the sessions talk to.
	APIURL string
	// Banner is sent before authentication. Keep it free of anything confidential,
	// since it is unauthenticated output.
	Banner string
	// IdleTimeout closes a session with no activity.
	IdleTimeout time.Duration
	// MaxTimeout caps the absolute session lifetime (0 disables).
	MaxTimeout time.Duration
	// MaxSessions caps concurrent sessions.
	MaxSessions int
	// AuthAttemptsPerMinute throttles authentication attempts per remote address.
	AuthAttemptsPerMinute int
	// Logger receives session and authentication events. Passwords are never
	// logged.
	Logger *slog.Logger
}

// server holds the listener plus the limits applied around it.
type server struct {
	cfg     Config
	logger  *slog.Logger
	srv     *ssh.Server
	slots   chan struct{}
	limiter *attemptLimiter
}

// Run starts the SSH door and blocks until ctx is cancelled or the listener
// fails.
func Run(ctx context.Context, cfg Config) error {
	s, err := newServer(cfg)
	if err != nil {
		return err
	}
	s.logger.Info("serving the TUI over SSH",
		slog.String("addr", s.srv.Addr),
		slog.String("auth", string(cfg.AuthMode)),
		slog.Int("max_sessions", cap(s.slots)),
		slog.String("api", cfg.APIURL),
	)

	errCh := make(chan error, 1)
	go func() { errCh <- s.srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, ssh.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	}
}

// newServer builds the listener with every hardening option applied.
func newServer(cfg Config) (*server, error) {
	if cfg.APIURL == "" {
		return nil, errors.New("sshd: APIURL is required")
	}
	if cfg.HostKeyPath == "" {
		return nil, errors.New("sshd: HostKeyPath is required (a stable host key keeps clients from seeing a host-key-changed warning)")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	// Fail fast on a bad URL rather than per session.
	if _, err := api.New(cfg.APIURL); err != nil {
		return nil, fmt.Errorf("sshd: %w", err)
	}

	s := &server{
		cfg:     cfg,
		logger:  logger,
		slots:   make(chan struct{}, max(1, cfg.MaxSessions)),
		limiter: newAttemptLimiter(max(1, cfg.AuthAttemptsPerMinute)),
	}

	opts := []ssh.Option{
		wish.WithAddress(cfg.Addr),
		wish.WithHostKeyPath(cfg.HostKeyPath),
		wish.WithVersion("FinTrak-TUI"),
		wish.WithIdleTimeout(cfg.IdleTimeout),
		wish.WithMaxTimeout(cfg.MaxTimeout),
		// wish composes these by wrapping each around the previous chain, so the
		// LAST entry is the outermost handler. The session cap must be outermost:
		// it has to hold its slot for the whole session, wrapping the bubbletea
		// middleware rather than sitting inside it.
		wish.WithMiddleware(
			wishtea.Middleware(s.model),
			s.logSession,
			s.limitSessions,
		),
	}
	if cfg.Banner != "" {
		opts = append(opts, wish.WithBanner(cfg.Banner+"\r\n"))
	}

	switch cfg.AuthMode {
	case AuthPassword:
		opts = append(opts, wish.WithPasswordAuth(s.authPassword))
	case AuthPublicKey:
		if cfg.AuthorizedKeysPath == "" {
			return nil, errors.New("sshd: AuthorizedKeysPath is required for public-key auth")
		}
		opts = append(opts, wish.WithAuthorizedKeys(cfg.AuthorizedKeysPath))
	case AuthAny:
		if cfg.AuthorizedKeysPath == "" {
			return nil, errors.New("sshd: AuthorizedKeysPath is required for combined auth")
		}
		opts = append(opts, wish.WithAuthorizedKeys(cfg.AuthorizedKeysPath), wish.WithPasswordAuth(s.authPassword))
	default:
		return nil, fmt.Errorf("sshd: unknown auth mode %q", cfg.AuthMode)
	}

	srv, err := wish.NewServer(opts...)
	if err != nil {
		return nil, err
	}

	// Port forwarding is denied when these callbacks are nil, but the denial is
	// made explicit: a tunnel would let an authenticated user reach the API (or
	// anything else) through this host, which is not what the door is for.
	srv.LocalPortForwardingCallback = func(ssh.Context, string, uint32) bool { return false }
	srv.ReversePortForwardingCallback = func(ssh.Context, string, uint32) bool { return false }
	// Only the requests an interactive terminal needs are allowed. This refuses
	// subsystems (sftp), direct-tcpip and agent forwarding, and it fails closed if
	// the library ever adds a request type.
	srv.SessionRequestCallback = allowTerminalRequest

	s.srv = srv
	return s, nil
}

// terminalRequests are the SSH request types an interactive TUI session needs.
var terminalRequests = map[string]bool{
	"shell":         true,
	"exec":          true,
	"pty-req":       true,
	"window-change": true,
	"env":           true,
	"signal":        true,
}

// allowTerminalRequest implements the session-request allowlist.
func allowTerminalRequest(_ ssh.Session, requestType string) bool {
	return terminalRequests[requestType]
}

// limitSessions caps concurrent sessions and releases the slot when the session
// ends. It is the outermost middleware, so the slot is held for the whole
// session rather than just its setup.
func (s *server) limitSessions(next ssh.Handler) ssh.Handler {
	return func(sess ssh.Session) {
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		default:
			s.logger.Warn("refused ssh session: at capacity",
				slog.String("remote", remoteHost(sess.RemoteAddr())),
				slog.Int("max", cap(s.slots)),
			)
			refuseSession(sess, fmt.Sprintf("FinTrak is at capacity (%d sessions). Try again shortly.", cap(s.slots)))
			return
		}
		next(sess)
	}
}

// refusalFlushDelay is how long a refusal notice is given to reach the client
// before the session is closed. Session.Exit sends the exit status and then
// closes the channel, which can discard a write that is still queued, so the
// notice needs a moment to drain. This only ever delays a client that is being
// turned away.
const refusalFlushDelay = 200 * time.Millisecond

// refuseSession tells a client why its session was refused and closes it with a
// non-zero status, so a scripted client can tell refusal from success.
func refuseSession(sess ssh.Session, message string) {
	_, _ = fmt.Fprintf(sess, "%s\r\n", message)
	time.Sleep(refusalFlushDelay)
	_ = sess.Exit(1)
}

// logSession records session boundaries and duration, without any credential.
func (s *server) logSession(next ssh.Handler) ssh.Handler {
	return func(sess ssh.Session) {
		started := time.Now()
		remote := remoteHost(sess.RemoteAddr())
		s.logger.Info("ssh session started", slog.String("remote", remote), slog.String("user", sess.User()))
		defer func() {
			s.logger.Info("ssh session ended",
				slog.String("remote", remote),
				slog.Duration("duration", time.Since(started)),
			)
		}()
		next(sess)
	}
}

// model hands a session its TUI. A session that authenticated with the FinTrak
// credentials reuses the client parked during authentication and starts signed
// in; a key-authenticated session gets a fresh client and the sign-in screen.
func (s *server) model(sess ssh.Session) (tea.Model, []tea.ProgramOption) {
	client, _ := sess.Context().Value(clientContextKey{}).(*api.Client)
	if client != nil {
		// The credentials were exchanged for a session during authentication; the
		// client keeps those tokens for the life of the connection, exactly as a
		// locally-run TUI would, so the session starts signed in.
		return ui.New(client), []tea.ProgramOption{tea.WithAltScreen()}
	}

	// Public-key authentication opened the door but cannot be traded for an API
	// session, so the TUI starts at its own sign-in screen.
	fresh, err := api.New(s.cfg.APIURL)
	if err != nil {
		s.logger.Error("ssh session: bad API URL", slog.String("error", err.Error()))
		return errorModel{text: "This TUI is misconfigured (bad API URL). Ask the operator to check the logs."},
			[]tea.ProgramOption{tea.WithAltScreen()}
	}
	return ui.New(fresh), []tea.ProgramOption{tea.WithAltScreen()}
}

// errorModel is the fallback for a session that cannot be set up at all. Startup
// validation is meant to make it unreachable; it exists so a misconfiguration is
// reported to the user instead of crashing the server.
type errorModel struct{ text string }

func (m errorModel) Init() tea.Cmd { return nil }

func (m errorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		return m, tea.Quit
	}
	return m, nil
}

func (m errorModel) View() string { return m.text }

// authPassword authenticates an SSH session against the FinTrak API. The SSH
// username is the account email and the password is the account password; the
// password is never stored, logged or reused — only the resulting session
// cookies outlive this call.
func (s *server) authPassword(ctx ssh.Context, password string) bool {
	remote := remoteHost(ctx.RemoteAddr())
	if !s.limiter.allow(remote) {
		s.logger.Warn("ssh auth throttled", slog.String("remote", remote))
		return false
	}

	client, err := api.New(s.cfg.APIURL)
	if err != nil {
		s.logger.Error("ssh auth: bad API URL", slog.String("error", err.Error()))
		return false
	}
	user, err := client.Login(ctx, ctx.User(), password)
	if err != nil {
		// The failure is logged without the password or the API's message, which
		// can distinguish "no such account" from "wrong password" on some paths.
		s.logger.Warn("ssh auth failed", slog.String("remote", remote), slog.String("user", ctx.User()))
		return false
	}
	ctx.SetValue(clientContextKey{}, client)
	s.logger.Info("ssh auth succeeded",
		slog.String("remote", remote),
		slog.String("user", user.Email),
		slog.String("role", user.Role),
	)
	return true
}

// remoteHost reduces a remote address to its host, so the throttle keys on the
// client rather than on an ephemeral port.
func remoteHost(addr net.Addr) string {
	if addr == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}

// attemptLimiter throttles authentication attempts per remote address with a
// token bucket. It is the local half of the defence: the API throttles per-IP
// and per-account too, but behind this door every session shares one client IP,
// so without a local limiter a single client could spend the backend's whole
// per-IP budget and slow sign-ins for everyone else.
type attemptLimiter struct {
	mu        sync.Mutex
	perMinute float64
	maxKeys   int
	buckets   map[string]*attemptBucket
}

type attemptBucket struct {
	tokens float64
	last   time.Time
}

// newAttemptLimiter builds a limiter allowing perMinute attempts per address,
// with a burst equal to the same number.
func newAttemptLimiter(perMinute int) *attemptLimiter {
	return &attemptLimiter{
		perMinute: float64(perMinute),
		maxKeys:   4096,
		buckets:   map[string]*attemptBucket{},
	}
}

// allow consumes one attempt for key, reporting whether it is permitted.
func (l *attemptLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if len(l.buckets) >= l.maxKeys {
		l.sweepLocked(now)
	}
	b, ok := l.buckets[key]
	if !ok {
		// Bound memory even when the keys are attacker-controlled.
		if len(l.buckets) >= l.maxKeys {
			return false
		}
		b = &attemptBucket{tokens: l.perMinute, last: now}
		l.buckets[key] = b
	}

	b.tokens = min(l.perMinute, b.tokens+now.Sub(b.last).Minutes()*l.perMinute)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweepLocked drops buckets that have been idle long enough to be full again.
func (l *attemptLimiter) sweepLocked(now time.Time) {
	for key, b := range l.buckets {
		if now.Sub(b.last) > 10*time.Minute {
			delete(l.buckets, key)
		}
	}
}
