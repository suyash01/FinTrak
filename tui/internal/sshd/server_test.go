package sshd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// The tests below drive the real server with a real SSH client, because the
// guarantees this package makes are protocol-level: credentials are verified by
// the API rather than by the door, and everything the protocol offers beyond an
// interactive terminal is refused. Unit-testing the callbacks individually would
// not prove either.

// stubAPI stands in for the FinTrak backend. It accepts one credential pair and
// answers the reference-data calls the TUI makes after signing in.
type stubAPI struct {
	server  *httptest.Server
	email   string
	pwd     string
	logins  int32
	mu      sync.Mutex
	seenGet []string
}

func newStubAPI(t *testing.T, email, password string) *stubAPI {
	t.Helper()
	stub := &stubAPI{email: email, pwd: password}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		switch {
		case path == "/auth/login" && r.Method == http.MethodPost:
			var body struct{ Email, Password string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Email != stub.email || body.Password != stub.pwd {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"errors":[{"message":"invalid email or password"}]}`))
				return
			}
			stub.mu.Lock()
			stub.logins++
			stub.mu.Unlock()
			http.SetCookie(w, &http.Cookie{Name: "fintrak_token", Value: "access", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "fintrak_refresh", Value: "refresh", Path: "/api/v1/auth"})
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"user":{"id":"u1","email":%q,"role":"admin"}}`, stub.email)

		case path == "/auth/me":
			_, _ = fmt.Fprintf(w, `{"id":"u1","email":%q,"role":"admin"}`, stub.email)

		case path == "/tags":
			_, _ = w.Write([]byte(`{"data":[]}`))

		case path == "/accounts", path == "/account-types", path == "/groups",
			path == "/categories", path == "/payees":
			stub.mu.Lock()
			stub.seenGet = append(stub.seenGet, path)
			stub.mu.Unlock()
			_, _ = w.Write([]byte(`[]`))

		default:
			// Anything the TUI asks for that this stub does not model answers an
			// empty object, which decodes into every response struct.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *stubAPI) url() string { return s.server.URL + "/api/v1" }

func (s *stubAPI) loginCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int(s.logins)
}

// startServer runs the door on a free port and returns its address.
func startServer(t *testing.T, cfg Config) string {
	t.Helper()

	// wish owns the listener, so pick a free port first and hand it over.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probing for a free port: %v", err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()
	cfg.Addr = addr

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return addr
		}
		select {
		case err := <-done:
			t.Fatalf("server exited during startup: %v", err)
		default:
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("server did not start listening")
	return ""
}

func testConfig(t *testing.T, apiURL string) Config {
	t.Helper()
	return Config{
		HostKeyPath:           filepath.Join(t.TempDir(), "ssh_host_ed25519_key"),
		APIURL:                apiURL,
		AuthMode:              AuthPassword,
		IdleTimeout:           30 * time.Second,
		MaxTimeout:            time.Minute,
		MaxSessions:           4,
		AuthAttemptsPerMinute: 10,
	}
}

// dial opens an authenticated client session.
func dial(t *testing.T, addr, user, password string) *gossh.Client {
	t.Helper()
	client, err := gossh.Dial("tcp", addr, &gossh.ClientConfig{
		User:            user,
		Auth:            []gossh.AuthMethod{gossh.Password(password)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("ssh dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestPasswordAuthIsDelegatedToTheAPI checks the door does not carry its own
// credential store: the API accepts the login, the door lets the session in, and
// the TUI starts signed in (it asks for reference data immediately instead of
// showing a login screen).
func TestPasswordAuthIsDelegatedToTheAPI(t *testing.T) {
	api := newStubAPI(t, "user@example.com", "correct horse battery")
	addr := startServer(t, testConfig(t, api.url()))

	client := dial(t, addr, "user@example.com", "correct horse battery")
	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer session.Close()

	if err := session.RequestPty("xterm-256color", 40, 120, gossh.TerminalModes{}); err != nil {
		t.Fatalf("request pty: %v", err)
	}
	out, err := session.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := session.Shell(); err != nil {
		t.Fatalf("shell: %v", err)
	}

	rendered := readFor(t, out, 5*time.Second, "Dashboard")
	if !strings.Contains(rendered, "Dashboard") {
		t.Fatalf("the session did not start signed in; got %q", rendered)
	}
	if api.loginCount() != 1 {
		t.Errorf("API logins = %d, want exactly 1", api.loginCount())
	}
	// The session is signed in, so the client must have fetched the shared
	// lookups instead of waiting on a login screen.
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.seenGet) == 0 {
		t.Error("the session never loaded reference data, so it did not start signed in")
	}
}

// TestWrongPasswordIsRejected proves the door does not authenticate anyone
// itself: only the API decides.
func TestWrongPasswordIsRejected(t *testing.T) {
	api := newStubAPI(t, "user@example.com", "correct horse battery")
	addr := startServer(t, testConfig(t, api.url()))

	_, err := gossh.Dial("tcp", addr, &gossh.ClientConfig{
		User:            "user@example.com",
		Auth:            []gossh.AuthMethod{gossh.Password("wrong")},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err == nil {
		t.Fatal("a wrong password was accepted")
	}
}

// TestForwardingAndSubsystemsAreRefused covers the protocol surface beyond an
// interactive terminal: a tunnel would let an authenticated user reach the API
// (or anything else reachable from the host) through this door.
func TestForwardingAndSubsystemsAreRefused(t *testing.T) {
	api := newStubAPI(t, "user@example.com", "pw")
	addr := startServer(t, testConfig(t, api.url()))
	client := dial(t, addr, "user@example.com", "pw")

	if conn, err := client.Dial("tcp", "example.com:80"); err == nil {
		_ = conn.Close()
		t.Error("local port forwarding was allowed")
	}
	if _, err := client.Listen("tcp", "127.0.0.1:0"); err == nil {
		t.Error("remote port forwarding was allowed")
	}

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer session.Close()
	if err := session.RequestSubsystem("sftp"); err == nil {
		t.Error("the sftp subsystem was allowed")
	}
}

// TestSessionCapRefusesExtraSessions checks the door degrades predictably under
// load instead of accepting sessions it cannot serve.
func TestSessionCapRefusesExtraSessions(t *testing.T) {
	api := newStubAPI(t, "user@example.com", "pw")
	cfg := testConfig(t, api.url())
	cfg.MaxSessions = 1
	addr := startServer(t, cfg)

	first := dial(t, addr, "user@example.com", "pw")
	session, err := first.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer session.Close()
	if err := session.RequestPty("xterm", 40, 120, gossh.TerminalModes{}); err != nil {
		t.Fatalf("request pty: %v", err)
	}
	out, err := session.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := session.Shell(); err != nil {
		t.Fatalf("shell: %v", err)
	}
	// Wait for the first session to actually occupy its slot.
	readFor(t, out, 5*time.Second, "Dashboard")

	second := dial(t, addr, "user@example.com", "pw")
	secondSession, err := second.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer secondSession.Close()

	secondOut, err := secondSession.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := secondSession.Shell(); err != nil {
		t.Fatalf("second shell: %v", err)
	}

	// The slot is checked in the middleware, so the second session is accepted by
	// the handshake and then refused with a message and a non-zero exit. Both
	// halves matter: a client must learn why it was turned away.
	refusal := readFor(t, secondOut, 5*time.Second, "capacity")
	if !strings.Contains(refusal, "capacity") {
		t.Errorf("second session was not told it was refused; output was %q", refusal)
	}
	if err := secondSession.Wait(); err == nil {
		t.Error("a refused session should exit with a non-zero status")
	}
}

// TestAuthThrottlePerAddressBoundsAttempts checks the local limiter: the API
// already throttles per-IP and per-account, but every SSH session reaches it
// from this door's address, so one client must not be able to spend the shared
// budget.
func TestAuthThrottlePerAddressBoundsAttempts(t *testing.T) {
	api := newStubAPI(t, "user@example.com", "pw")
	cfg := testConfig(t, api.url())
	cfg.AuthAttemptsPerMinute = 1
	addr := startServer(t, cfg)

	attempt := func() error {
		client, err := gossh.Dial("tcp", addr, &gossh.ClientConfig{
			User:            "user@example.com",
			Auth:            []gossh.AuthMethod{gossh.Password("pw")},
			HostKeyCallback: gossh.InsecureIgnoreHostKey(),
			Timeout:         5 * time.Second,
		})
		if err != nil {
			return err
		}
		_ = client.Close()
		return nil
	}

	if err := attempt(); err != nil {
		t.Fatalf("first attempt should succeed: %v", err)
	}
	if err := attempt(); err == nil {
		t.Error("the throttle allowed a second attempt within the same minute")
	}
}

// readFor reads the session output until want appears or the deadline passes,
// returning everything read.
func readFor(t *testing.T, r io.Reader, timeout time.Duration, want string) string {
	t.Helper()
	type result struct {
		text string
	}
	ch := make(chan result, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
				if want == "" || strings.Contains(b.String(), want) {
					ch <- result{text: b.String()}
					return
				}
			}
			if err != nil {
				ch <- result{text: b.String()}
				return
			}
		}
	}()

	select {
	case got := <-ch:
		return got.text
	case <-time.After(timeout):
		return ""
	}
}
