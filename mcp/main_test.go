package main

import (
	"bytes"
	"context"

	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fintrak/client/api"
	"github.com/fintrak/mcp/internal/mcpserver"
)

// testMainEnv turns this test binary into the real server: the smoke test
// re-executes itself with it set, which is how the binary's own wiring — flags,
// environment, transport chain, stdio — is exercised without shelling out to
// `go build`.
const testMainEnv = "FINTRAK_MCP_TEST_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(testMainEnv) == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

// apiStub is a minimal FinTrak API: it hands out a session on login and serves
// one account, recording the requests in order.
type apiStub struct {
	mu       sync.Mutex
	requests []string
	cookies  []string
}

func (s *apiStub) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		s.cookies = append(s.cookies, r.Header.Get("Cookie"))
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/login":
			http.SetCookie(w, &http.Cookie{Name: api.AccessCookieName, Value: "access-1", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: api.RefreshCookieName, Value: "refresh-1", Path: "/api/v1/auth"})
			_, _ = w.Write([]byte(`{"user":{"id":"u1","email":"a@b.c","role":"user"}}`))
		case "/api/v1/accounts":
			_, _ = w.Write([]byte(`[{"id":"acct-1","name":"Checking","typeId":"bank","currency":"INR","balance":"1234.56"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[{"message":"no such route"}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *apiStub) seen() ([]string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...), append([]string(nil), s.cookies...)
}

// TestBinaryServesReadOnlyToolsOverStdio is the end-to-end proof for the
// command: it starts the real binary, speaks MCP to it over stdin/stdout,
// checks the advertised tool surface is read-only, and calls one tool through
// the whole chain (login, guard, API, JSON response).
func TestBinaryServesReadOnlyToolsOverStdio(t *testing.T) {
	stub := &apiStub{}
	srv := stub.start(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(childEnv(), testMainEnv+"=1",
		"FINTRAK_API_URL="+srv.URL+"/api/v1",
		"FINTRAK_EMAIL=a@b.c",
		"FINTRAK_PASSWORD=hunter2hunter2",
	)
	var logs bytes.Buffer
	cmd.Stderr = &logs

	session, err := mcp.NewClient(&mcp.Implementation{Name: "smoke", Version: "test"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connecting to the binary: %v (stderr: %s)", err, logs.String())
	}
	defer func() { _ = session.Close() }()

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(listed.Tools) != len(mcpserver.Tools()) {
		t.Errorf("the binary advertises %d tools, want %d", len(listed.Tools), len(mcpserver.Tools()))
	}
	for _, tool := range listed.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %q is not annotated read-only", tool.Name)
		}
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_accounts"})
	if err != nil {
		t.Fatalf("tools/call list_accounts: %v (stderr: %s)", err, logs.String())
	}
	if res.IsError {
		t.Fatalf("list_accounts failed: %v", res.Content)
	}
	text := ""
	for _, content := range res.Content {
		if block, ok := content.(*mcp.TextContent); ok {
			text += block.Text
		}
	}
	if !strings.Contains(text, "Checking") {
		t.Errorf("list_accounts returned %q, want the stub's account", text)
	}

	// A write the API has but this server does not expose must not exist.
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "create_transaction"}); err == nil {
		t.Error("the server answered a write tool it should not have")
	}

	if err := session.Close(); err != nil {
		t.Fatalf("closing the session: %v", err)
	}
	_ = cmd.Wait()

	requests, cookies := stub.seen()
	want := []string{"POST /api/v1/auth/login", "GET /api/v1/accounts"}
	if len(requests) != len(want) || requests[0] != want[0] || requests[1] != want[1] {
		t.Errorf("the API saw %v, want %v", requests, want)
	}
	if !strings.Contains(cookies[1], api.AccessCookieName+"=access-1") {
		t.Errorf("the accounts request carried %q, want the session cookie from the login", cookies[1])
	}
	if !strings.Contains(logs.String(), "read_only=true") {
		t.Errorf("stderr = %q, want the startup log line", logs.String())
	}
}

// childEnv is the parent environment without any FINTRAK_* the developer may
// have set, so the test's own configuration is the only one that applies.
func childEnv() []string {
	env := os.Environ()
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, "FINTRAK_") {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// TestParseFlagsRequiresCredentials keeps the startup check honest: without
// either credential shape every tool call would answer 401, so the binary must
// refuse to start instead.
func TestParseFlagsRequiresCredentials(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(childEnv(), testMainEnv+"=1", "FINTRAK_EMAIL=a@b.c")
	var stderr, stdout bytes.Buffer
	cmd.Stderr, cmd.Stdout = &stderr, &stdout

	if err := cmd.Run(); err == nil {
		t.Fatal("the binary started without credentials")
	}
	if !strings.Contains(stderr.String(), "FINTRAK_EMAIL") {
		t.Errorf("stderr = %q, want it to name the missing configuration", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want it left alone: it carries the MCP protocol", stdout.String())
	}
}

// TestParseFlagsRequiresBothTokenHalves keeps the token shape honest too. The
// server holds no other credentials, so a lone access token works until it
// expires and then fails every call with "sign in again" — the permanent 401
// the startup check exists to prevent — so the pair is required at startup.
func TestParseFlagsRequiresBothTokenHalves(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(childEnv(), testMainEnv+"=1", "FINTRAK_ACCESS_TOKEN=access-token")
	var stderr, stdout bytes.Buffer
	cmd.Stderr, cmd.Stdout = &stderr, &stdout

	if err := cmd.Run(); err == nil {
		t.Fatal("the binary started with an access token but no refresh token")
	}
	if !strings.Contains(stderr.String(), "FINTRAK_REFRESH_TOKEN") {
		t.Errorf("stderr = %q, want it to name the missing variable", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want it left alone: it carries the MCP protocol", stdout.String())
	}
}
