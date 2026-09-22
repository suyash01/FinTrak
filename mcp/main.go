// Command fintrak-mcp serves FinTrak to model clients over MCP.
//
// It speaks the Model Context Protocol on stdin/stdout, the transport an MCP
// client launches a subprocess with, and is configured the same way the other
// clients are: an API base URL plus a FinTrak login. An MCP client config looks
// like:
//
//	{
//	  "mcpServers": {
//	    "fintrak": {
//	      "command": "fintrak-mcp",
//	      "env": {
//	        "FINTRAK_API_URL": "http://localhost:8080/api/v1",
//	        "FINTRAK_EMAIL": "you@example.com",
//	        "FINTRAK_PASSWORD": "..."
//	      }
//	    }
//	  }
//	}
//
// A session can also be supplied as a token pair instead of a login, in
// FINTRAK_ACCESS_TOKEN and FINTRAK_REFRESH_TOKEN. Both halves are required: the
// pair is the only thing that can renew the session, so a lone access token
// would simply stop working at its expiry.
//
// Every tool it exposes is read-only (see internal/mcpserver): the model can
// read the ledger, the aggregates and the API's preview endpoints, and cannot
// change anything. Because stdout carries the protocol, logs go to stderr or
// -log-file and never to stdout.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fintrak/client/api"
	"github.com/fintrak/mcp/internal/mcpserver"
)

// version is injected at build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

// options are the resolved command-line/environment settings.
type options struct {
	apiURL   string
	email    string
	password string
	access   string
	refresh  string

	logFile  string
	logLevel string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fintrak-mcp:", err)
		os.Exit(1)
	}
}

func run() error {
	opts, err := parseFlags()
	if err != nil {
		return err
	}
	api.UserAgent = "fintrak-mcp/" + version
	mcpserver.Version = version

	logger, closeLog, err := newLogger(opts)
	if err != nil {
		return err
	}
	defer closeLog()

	client, err := newClient(opts)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("serving FinTrak over stdio",
		slog.String("api", client.BaseURL()),
		slog.Int("tools", len(mcpserver.Tools())),
		slog.Bool("read_only", true),
	)
	server := mcpserver.New(client, mcpserver.Credentials{Email: opts.email, Password: opts.password})
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// newClient builds the API client with the read-only guard installed as its
// transport, so no request outside the tool surface can leave the process. The
// sign-in is the server's job (see mcpserver.New), not the transport's.
func newClient(opts options) (*api.Client, error) {
	client, err := api.New(opts.apiURL)
	if err != nil {
		return nil, err
	}
	if opts.access != "" {
		client.SetTokens(opts.access, opts.refresh)
	}
	client.SetTransport(mcpserver.NewGuard(client, http.DefaultTransport))
	return client, nil
}

// parseFlags reads the command line, falling back to environment variables so
// an MCP client can configure the server entirely through its environment.
func parseFlags() (options, error) {
	var (
		opts     options
		showVer  = flag.Bool("version", false, "print the version and exit")
		apiURL   = flag.String("api", envOr("FINTRAK_API_URL", api.DefaultBaseURL), "FinTrak API base URL (env FINTRAK_API_URL)")
		email    = flag.String("email", os.Getenv("FINTRAK_EMAIL"), "FinTrak account email (env FINTRAK_EMAIL; prefer the environment over the flag)")
		password = flag.String("password", os.Getenv("FINTRAK_PASSWORD"), "FinTrak account password (env FINTRAK_PASSWORD; prefer the environment over the flag)")
		logFile  = flag.String("log-file", os.Getenv("FINTRAK_LOG_FILE"), "write logs here (default: stderr, never stdout — that is the protocol channel)")
		logLevel = flag.String("log-level", envOr("FINTRAK_LOG_LEVEL", "info"), "log level: debug, info, warn or error")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("fintrak-mcp", version)
		os.Exit(0)
	}

	opts = options{
		apiURL:   *apiURL,
		email:    strings.TrimSpace(*email),
		password: *password,
		access:   strings.TrimSpace(os.Getenv("FINTRAK_ACCESS_TOKEN")),
		refresh:  strings.TrimSpace(os.Getenv("FINTRAK_REFRESH_TOKEN")),
		logFile:  *logFile,
		logLevel: strings.ToLower(strings.TrimSpace(*logLevel)),
	}

	// Without one of the two credential shapes every tool call would answer
	// 401, so say so at startup rather than at the first question. An access
	// token alone is not one of those shapes: nothing here holds a refresh
	// token, so the session would work until the token expires and then fail
	// every call with "sign in again" while holding no credentials to do it.
	if opts.access == "" && (opts.email == "" || opts.password == "") {
		return opts, errors.New("set FINTRAK_EMAIL and FINTRAK_PASSWORD (or FINTRAK_ACCESS_TOKEN with FINTRAK_REFRESH_TOKEN) to sign in")
	}
	if opts.access != "" && opts.refresh == "" {
		return opts, errors.New("FINTRAK_ACCESS_TOKEN needs FINTRAK_REFRESH_TOKEN (or set FINTRAK_EMAIL and FINTRAK_PASSWORD): a lone access token expires and cannot be refreshed")
	}
	return opts, nil
}

// newLogger builds the process logger. It must never write to stdout, which
// carries the MCP protocol; stderr is where an MCP client collects a server's
// diagnostics, so that is the default.
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

	var out io.Writer = os.Stderr
	closeFn := func() {}
	if opts.logFile != "" {
		file, err := os.OpenFile(opts.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, closeFn, fmt.Errorf("opening the log file: %w", err)
		}
		out, closeFn = file, func() { _ = file.Close() }
	}

	return slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: level})), closeFn, nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
