// Command fintrak-tui is the FinTrak terminal client.
//
// It runs in two modes that share everything except the entrypoint:
//
//	fintrak-tui                 run in the local terminal
//	fintrak-tui -ssh            serve the TUI over SSH
//
// Both talk to the same REST API as the web frontend, configured with -api (or
// FINTRAK_API_URL), so the TUI needs no special deployment on the server side.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fintrak/client/api"
	"github.com/fintrak/tui/internal/sshd"
	"github.com/fintrak/tui/internal/ui"
)

// version is injected at build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

// options are the resolved command-line/environment settings.
type options struct {
	apiURL string

	ssh            bool
	sshAddr        string
	sshHostKey     string
	sshAuth        string
	sshKeys        string
	sshIdleTimeout time.Duration
	sshMaxTimeout  time.Duration
	sshMaxSessions int
	sshAuthPerMin  int

	logFile  string
	logLevel string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fintrak-tui:", err)
		os.Exit(1)
	}
}

func run() error {
	opts, err := parseFlags()
	if err != nil {
		return err
	}
	// Identify the client to the API's logs; the version is useful when a stale
	// binary misbehaves against a newer server.
	api.UserAgent = "fintrak-tui/" + version

	logger, closeLog, err := newLogger(opts)
	if err != nil {
		return err
	}
	defer closeLog()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if opts.ssh {
		return startSSH(ctx, opts, logger)
	}
	return startLocal(opts)
}

// startLocal runs the TUI on the current terminal.
func startLocal(opts options) error {
	client, err := api.New(opts.apiURL)
	if err != nil {
		return err
	}
	program := tea.NewProgram(ui.New(client), tea.WithAltScreen())
	_, err = program.Run()
	return err
}

// startSSH serves the TUI over SSH until the context is cancelled.
func startSSH(ctx context.Context, opts options, logger *slog.Logger) error {
	// The host key must be stable and its directory must exist before wish tries
	// to create the key.
	if dir := filepath.Dir(opts.sshHostKey); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("creating the host key directory: %w", err)
		}
	}

	banner := ""
	if opts.sshAuth == string(sshd.AuthPassword) || opts.sshAuth == string(sshd.AuthAny) {
		banner = "FinTrak — sign in with your FinTrak email and password."
	}

	cfg := sshd.Config{
		Addr:                  opts.sshAddr,
		HostKeyPath:           opts.sshHostKey,
		AuthorizedKeysPath:    opts.sshKeys,
		AuthMode:              sshd.AuthMode(opts.sshAuth),
		APIURL:                opts.apiURL,
		Banner:                banner,
		IdleTimeout:           opts.sshIdleTimeout,
		MaxTimeout:            opts.sshMaxTimeout,
		MaxSessions:           opts.sshMaxSessions,
		AuthAttemptsPerMinute: opts.sshAuthPerMin,
		Logger:                logger,
	}
	return sshd.Run(ctx, cfg)
}

// parseFlags reads the command line, falling back to environment variables so
// the same binary can be configured in a container without arguments.
func parseFlags() (options, error) {
	var (
		opts     options
		showVer  = flag.Bool("version", false, "print the version and exit")
		apiURL   = flag.String("api", envOr("FINTRAK_API_URL", api.DefaultBaseURL), "FinTrak API base URL (env FINTRAK_API_URL)")
		serveSSH = flag.Bool("ssh", envBool("FINTRAK_SSH"), "serve the TUI over SSH instead of running locally")
		sshAddr  = flag.String("ssh-addr", envOr("FINTRAK_SSH_ADDR", ":2222"), "SSH listen address")
		sshKey   = flag.String("ssh-host-key", envOr("FINTRAK_SSH_HOST_KEY", defaultHostKeyPath()), "SSH host key path (created if missing; keep it stable)")
		sshAuth  = flag.String("ssh-auth", envOr("FINTRAK_SSH_AUTH", string(sshd.AuthPassword)), "SSH authentication: password, key or any")
		sshKeys  = flag.String("ssh-authorized-keys", os.Getenv("FINTRAK_SSH_AUTHORIZED_KEYS"), "authorized_keys file, required for key or any auth")
		sshIdle  = flag.Duration("ssh-idle-timeout", envDuration("FINTRAK_SSH_IDLE_TIMEOUT", 15*time.Minute), "close an idle SSH session after this long")
		sshMax   = flag.Duration("ssh-max-timeout", envDuration("FINTRAK_SSH_MAX_TIMEOUT", 12*time.Hour), "absolute SSH session lifetime (0 disables)")
		sshSlots = flag.Int("ssh-max-sessions", envInt("FINTRAK_SSH_MAX_SESSIONS", 32), "maximum concurrent SSH sessions")
		sshAuthN = flag.Int("ssh-auth-per-minute", envInt("FINTRAK_SSH_AUTH_PER_MINUTE", 10), "SSH authentication attempts allowed per remote address per minute")
		logFile  = flag.String("log-file", os.Getenv("FINTRAK_LOG_FILE"), "write logs here (default: stderr when serving SSH, discarded otherwise)")
		logLevel = flag.String("log-level", envOr("FINTRAK_LOG_LEVEL", "info"), "log level: debug, info, warn or error")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("fintrak-tui", version)
		os.Exit(0)
	}

	opts = options{
		apiURL:         *apiURL,
		ssh:            *serveSSH,
		sshAddr:        *sshAddr,
		sshHostKey:     *sshKey,
		sshAuth:        strings.ToLower(strings.TrimSpace(*sshAuth)),
		sshKeys:        *sshKeys,
		sshIdleTimeout: *sshIdle,
		sshMaxTimeout:  *sshMax,
		sshMaxSessions: *sshSlots,
		sshAuthPerMin:  *sshAuthN,
		logFile:        *logFile,
		logLevel:       strings.ToLower(strings.TrimSpace(*logLevel)),
	}

	switch sshd.AuthMode(opts.sshAuth) {
	case sshd.AuthPassword, sshd.AuthPublicKey, sshd.AuthAny:
	default:
		return opts, fmt.Errorf("invalid -ssh-auth %q: want password, key or any", opts.sshAuth)
	}
	if opts.ssh && (opts.sshAuth == string(sshd.AuthPublicKey) || opts.sshAuth == string(sshd.AuthAny)) && opts.sshKeys == "" {
		return opts, errors.New("-ssh-authorized-keys is required for key or any authentication")
	}
	return opts, nil
}

// newLogger builds the process logger. A locally-run TUI must not write to
// stderr, because that is the terminal the program is drawing on, so logs are
// discarded unless a file is configured or the process is serving SSH.
func newLogger(opts options) (*slog.Logger, func(), error) {
	level := slog.LevelInfo
	switch opts.logLevel {
	case "debug":
		level = slog.LevelDebug
	case "info", "":
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, func() {}, fmt.Errorf("invalid log level %q", opts.logLevel)
	}

	var out io.Writer = io.Discard
	closeFn := func() {}
	switch {
	case opts.logFile != "":
		file, err := os.OpenFile(opts.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, closeFn, fmt.Errorf("opening the log file: %w", err)
		}
		out = file
		closeFn = func() { _ = file.Close() }
	case opts.ssh:
		out = os.Stderr
	}

	handler := slog.NewTextHandler(out, &slog.HandlerOptions{Level: level})
	return slog.New(handler), closeFn, nil
}

// defaultHostKeyPath places the generated host key with the user's other
// configuration, so it survives restarts: a regenerated host key makes every
// client refuse the connection until its known_hosts entry is cleared.
func defaultHostKeyPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "fintrak-tui-ssh-host-key"
	}
	return filepath.Join(dir, "fintrak-tui", "ssh_host_ed25519_key")
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envInt(key string, fallback int) int {
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(os.Getenv(key)), "%d", &n); err != nil {
		return fallback
	}
	return n
}

func envDuration(key string, fallback time.Duration) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return fallback
	}
	return d
}
