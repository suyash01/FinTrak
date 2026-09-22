package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fintrak/client/api"
)

// authStub is a stub API that hands out a session on login the way the real
// handlers do (Set-Cookie), records the paths it served in order, and can hold
// a login open so concurrent callers overlap.
type authStub struct {
	mu         sync.Mutex
	paths      []string
	cookies    []string
	logins     int
	credential struct {
		Email    string
		Password string
	}
	reject   bool
	holdTime time.Duration
}

func (s *authStub) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths = append(s.paths, r.URL.Path)
	s.cookies = append(s.cookies, r.Header.Get("Cookie"))
}

func (s *authStub) served() ([]string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...), append([]string(nil), s.cookies...)
}

func (s *authStub) loginCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins
}

func (s *authStub) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/auth/login") {
			if s.holdTime > 0 {
				time.Sleep(s.holdTime)
			}
			var body struct{ Email, Password string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			s.mu.Lock()
			s.logins++
			s.credential.Email, s.credential.Password = body.Email, body.Password
			reject := s.reject
			s.mu.Unlock()
			if reject {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"errors":[{"message":"invalid email or password"}]}`))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: api.AccessCookieName, Value: "access-1", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: api.RefreshCookieName, Value: "refresh-1", Path: "/api/v1/auth"})
			_, _ = w.Write([]byte(`{"user":{"id":"u1","email":"a@b.c","role":"user"}}`))
			return
		}
		_, _ = w.Write([]byte(`null`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// sessionClient returns a client for the stub with the read-only guard
// installed, the same chain the binary builds.
func sessionClient(t *testing.T, stub *authStub) *api.Client {
	t.Helper()
	c, err := api.New(stub.start(t).URL + "/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	c.SetTransport(NewGuard(c, http.DefaultTransport))
	return c
}

// TestSessionSignsInOnTheFirstToolCall covers the whole reason the sign-in is
// lazy and lives above the transport: the first call must reach the API with
// the session the login just issued, not with an empty cookie.
func TestSessionSignsInOnTheFirstToolCall(t *testing.T) {
	stub := &authStub{}
	c := sessionClient(t, stub)
	session := connectWith(t, c, Credentials{Email: "a@b.c", Password: "hunter2hunter2"})

	if res := call(t, session, "list_accounts", nil); res.IsError {
		t.Fatalf("list_accounts reported an error: %s", errorText(res))
	}

	paths, cookies := stub.served()
	want := []string{"/api/v1/auth/login", "/api/v1/accounts"}
	if !equalStrings(paths, want) {
		t.Fatalf("served %v, want %v", paths, want)
	}
	if got := stub.loginCount(); got != 1 {
		t.Errorf("signed in %d times, want one", got)
	}
	if !strings.Contains(cookies[1], api.AccessCookieName+"=access-1") {
		t.Errorf("the first call carried %q, want the session the login issued", cookies[1])
	}
	stub.mu.Lock()
	got := stub.credential
	stub.mu.Unlock()
	if got.Email != "a@b.c" || got.Password != "hunter2hunter2" {
		t.Errorf("login body = %+v, want the configured credentials", got)
	}
}

// TestSessionSignsInOnlyOnceUnderConcurrency keeps a burst of tool calls from
// producing one login each.
func TestSessionSignsInOnlyOnceUnderConcurrency(t *testing.T) {
	stub := &authStub{holdTime: 50 * time.Millisecond}
	c := sessionClient(t, stub)
	session := connectWith(t, c, Credentials{Email: "a@b.c", Password: "hunter2hunter2"})

	const callers = 8
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if res := call(t, session, "list_accounts", nil); res.IsError {
				t.Errorf("list_accounts reported an error: %s", errorText(res))
			}
		}()
	}
	wg.Wait()

	if got := stub.loginCount(); got != 1 {
		t.Errorf("signed in %d times, want one", got)
	}
	paths, _ := stub.served()
	if len(paths) != callers+1 || paths[0] != "/api/v1/auth/login" {
		t.Errorf("served %v, want one login followed by %d calls", paths, callers)
	}
}

// TestFailedSignInIsAToolError checks the failure path: the tool call fails
// with the API's own message and nothing else reaches the API.
func TestFailedSignInIsAToolError(t *testing.T) {
	stub := &authStub{reject: true}
	c := sessionClient(t, stub)
	session := connectWith(t, c, Credentials{Email: "a@b.c", Password: "wrong"})

	res := call(t, session, "list_accounts", nil)
	if !res.IsError {
		t.Fatal("a failed sign-in must fail the tool call")
	}
	text := errorText(res)
	if !strings.Contains(text, "invalid email or password") {
		t.Errorf("error = %q, want the API's message", text)
	}
	if !strings.Contains(text, "a@b.c") {
		t.Errorf("error = %q, want it to name the account being signed in", text)
	}
	paths, _ := stub.served()
	if !equalStrings(paths, []string{"/api/v1/auth/login"}) {
		t.Errorf("served %v, want only the login attempt", paths)
	}
}

// TestServerWithoutCredentialsNeverSignsIn keeps a server configured with a
// session from calling the auth endpoints on its own.
func TestServerWithoutCredentialsNeverSignsIn(t *testing.T) {
	stub := &authStub{}
	c := sessionClient(t, stub)
	session := connectWith(t, c, Credentials{})

	call(t, session, "list_accounts", nil)

	paths, _ := stub.served()
	if !equalStrings(paths, []string{"/api/v1/accounts"}) {
		t.Errorf("served %v, want the call alone", paths)
	}
}

// TestSessionRecoversAfterItEnds covers the long-lived server: once the client
// drops its tokens (a rejected refresh does that), the next call signs in again
// rather than failing forever.
func TestSessionRecoversAfterItEnds(t *testing.T) {
	stub := &authStub{}
	c := sessionClient(t, stub)
	session := connectWith(t, c, Credentials{Email: "a@b.c", Password: "hunter2hunter2"})

	call(t, session, "list_accounts", nil)
	c.ClearSession()
	call(t, session, "list_accounts", nil)

	if got := stub.loginCount(); got != 2 {
		t.Errorf("signed in %d times, want a second sign-in after the session ended", got)
	}
}

// TestTheHandshakeDoesNotRequireASession keeps the middleware narrow: listing
// tools must not sign in, or a client with wrong credentials could not even
// discover what the server offers.
func TestTheHandshakeDoesNotRequireASession(t *testing.T) {
	stub := &authStub{}
	c := sessionClient(t, stub)
	session := connectWith(t, c, Credentials{Email: "a@b.c", Password: "wrong"})

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(listed.Tools) != len(Tools()) {
		t.Errorf("tools/list returned %d tools, want %d", len(listed.Tools), len(Tools()))
	}
	paths, _ := stub.served()
	if len(paths) != 0 {
		t.Errorf("the API saw %v, want nothing before a tool call", paths)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
