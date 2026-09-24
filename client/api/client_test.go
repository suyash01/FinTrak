package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newStub starts a test server and returns a client pointed at it. The handler
// receives the request and decides the response, so each test can assert on
// what the client actually sent.
func newStub(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c, err := New(srv.URL + "/api/v1")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// sessionCookies writes the two session cookies the way the auth handlers do.
func sessionCookies(w http.ResponseWriter, access, refresh string) {
	http.SetCookie(w, &http.Cookie{Name: AccessCookieName, Value: access, Path: "/"})
	http.SetCookie(w, &http.Cookie{Name: RefreshCookieName, Value: refresh, Path: "/api/v1/auth"})
}

// TestLoginStoresSessionFromCookies covers the whole reason this client does not
// use net/http/cookiejar: the tokens only arrive as Set-Cookie headers, and they
// must be replayed explicitly on later requests. Driving it over httptest means
// the transport is plain HTTP with no Secure attribute in play, which is exactly
// the production-cookie-over-http case that a jar would silently break.
func TestLoginStoresSessionFromCookies(t *testing.T) {
	var sawLoginBody map[string]string
	c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/login" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&sawLoginBody); err != nil {
			t.Errorf("decoding login body: %v", err)
		}
		sessionCookies(w, "access-1", "refresh-1")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user":{"id":"u1","email":"a@b.c","role":"user"}}`))
	})

	if c.HasSession() {
		t.Fatal("client should start with no session")
	}
	user, err := c.Login(context.Background(), "a@b.c", "hunter2hunter2")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if user.Email != "a@b.c" || user.Role != "user" {
		t.Errorf("unexpected user %+v", user)
	}
	if got := sawLoginBody["email"]; got != "a@b.c" {
		t.Errorf("login body email = %q", got)
	}
	if !c.HasSession() || c.AccessToken() != "access-1" {
		t.Fatalf("session not captured: hasSession=%v token=%q", c.HasSession(), c.AccessToken())
	}
}

// TestAuthenticatedRequestCarriesAccessCookieButNotRefreshToken checks the
// server-side scoping is mirrored: the refresh token is only ever presented to
// the refresh endpoint.
func TestAuthenticatedRequestCarriesAccessCookieButNotRefreshToken(t *testing.T) {
	var gotCookie string
	c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			sessionCookies(w, "access-1", "refresh-1")
			_, _ = w.Write([]byte(`{"user":{"id":"u1","email":"a@b.c","role":"user"}}`))
		case "/api/v1/accounts":
			gotCookie = r.Header.Get("Cookie")
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	if _, err := c.Login(context.Background(), "a@b.c", "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := c.ListAccounts(context.Background()); err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if gotCookie != AccessCookieName+"=access-1" {
		t.Errorf("Cookie header = %q, want only the access token", gotCookie)
	}
}

// TestExpiredAccessTokenRefreshesAndReplays is the behaviour that makes the
// 15-minute access token invisible to the UI: a 401 triggers one refresh and a
// replay of the original request.
func TestExpiredAccessTokenRefreshesAndReplays(t *testing.T) {
	var accountsCalls, refreshCalls int32
	var refreshCookie string

	c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/accounts":
			atomic.AddInt32(&accountsCalls, 1)
			if r.Header.Get("Cookie") != AccessCookieName+"=access-2" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"errors":[{"message":"invalid or expired token"}]}`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":"a1","name":"Everyday","accountTypeId":"bank"}]`))
		case "/api/v1/auth/refresh":
			atomic.AddInt32(&refreshCalls, 1)
			refreshCookie = r.Header.Get("Cookie")
			sessionCookies(w, "access-2", "refresh-1")
			_, _ = w.Write([]byte(`{"message":"token refreshed"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	c.SetTokens("access-1", "refresh-1")
	accounts, err := c.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accounts) != 1 || accounts[0].Name != "Everyday" {
		t.Errorf("replayed response not decoded: %+v", accounts)
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Errorf("refresh calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&accountsCalls); got != 2 {
		t.Errorf("accounts calls = %d, want 2 (original + replay)", got)
	}
	if refreshCookie != RefreshCookieName+"=refresh-1" {
		t.Errorf("refresh presented %q, want the refresh token", refreshCookie)
	}
}

// TestConcurrentUnauthorizedCallsRefreshOnce proves the refresh is
// single-flight: a screen that loads five resources at once with an expired
// access token must not open five sessions.
func TestConcurrentUnauthorizedCallsRefreshOnce(t *testing.T) {
	var refreshCalls int32

	c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/accounts":
			if r.Header.Get("Cookie") != AccessCookieName+"=access-fresh" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"errors":[{"message":"invalid or expired token"}]}`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case "/api/v1/auth/refresh":
			atomic.AddInt32(&refreshCalls, 1)
			sessionCookies(w, "access-fresh", "refresh-1")
			_, _ = w.Write([]byte(`{"message":"token refreshed"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	c.SetTokens("access-stale", "refresh-1")

	const callers = 6
	var wg sync.WaitGroup
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = c.ListAccounts(context.Background())
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d failed: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Errorf("refresh calls = %d, want exactly 1", got)
	}
}

// TestRefreshRejectionEndsTheSession covers the 30-day absolute deadline: once
// the server refuses the refresh, the client must stop retrying and report a
// sign-in prompt rather than looping.
func TestRefreshRejectionEndsTheSession(t *testing.T) {
	c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/accounts":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errors":[{"message":"invalid or expired token"}]}`))
		case "/api/v1/auth/refresh":
			http.SetCookie(w, &http.Cookie{Name: AccessCookieName, Value: "", Path: "/", MaxAge: -1})
			http.SetCookie(w, &http.Cookie{Name: RefreshCookieName, Value: "", Path: "/api/v1/auth", MaxAge: -1})
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errors":[{"message":"refresh token expired"}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	c.SetTokens("access-1", "refresh-1")
	_, err := c.ListAccounts(context.Background())
	if err == nil {
		t.Fatal("expected an error once the refresh is refused")
	}
	if !Unauthorized(err) {
		t.Errorf("error should read as unauthorized, got %v", err)
	}
	if c.HasSession() {
		t.Error("a refused refresh must clear the local session")
	}
}

// TestTransientRefreshFailureKeepsTheSession splits the two ways POST
// /auth/refresh can fail. A rejection clears the cookies server-side, so the
// session is over and the client drops it too. A transient 5xx (a role-lookup
// DB error, a restarting pooler, a proxy blip) leaves the 30-day refresh token
// intact on the server, so the client must keep its copy and let the next 401
// retry the exchange: clearing it locally would force a fresh sign-in for a
// session the server never refused.
func TestTransientRefreshFailureKeepsTheSession(t *testing.T) {
	var refreshCalls int32
	var health atomic.Bool

	c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/accounts":
			// The access token is only replaced by a successful refresh, so it
			// stays stale until the exchange works.
			if r.Header.Get("Cookie") == AccessCookieName+"=access-1" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"errors":[{"message":"invalid or expired token"}]}`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":"a1","name":"Everyday","accountTypeId":"bank"}]`))
		case "/api/v1/auth/refresh":
			atomic.AddInt32(&refreshCalls, 1)
			if !health.Load() {
				// Transient: no Set-Cookie at all, so nothing is cleared.
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"errors":[{"message":"internal server error"}]}`))
				return
			}
			sessionCookies(w, "access-2", "refresh-1")
			_, _ = w.Write([]byte(`{"message":"token refreshed"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	c.SetTokens("access-1", "refresh-1")
	_, err := c.ListAccounts(context.Background())
	if err == nil {
		t.Fatal("a failed refresh must fail the call")
	}
	if Unauthorized(err) {
		t.Errorf("error = %v, want the transient 500 rather than a sign-in prompt", err)
	}
	if !StatusIs(err, http.StatusInternalServerError) {
		t.Errorf("error = %v, want the server's 500", err)
	}
	if !c.HasSession() {
		t.Error("a transient refresh failure must keep the session")
	}
	if got := c.refreshToken(); got != "refresh-1" {
		t.Errorf("refresh token = %q, want it kept for the next attempt", got)
	}

	// The retry path: once the server is healthy the same client recovers
	// without signing in again.
	health.Store(true)
	accounts, err := c.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAccounts after recovery: %v", err)
	}
	if len(accounts) != 1 || accounts[0].Name != "Everyday" {
		t.Errorf("accounts = %+v, want the replayed response", accounts)
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 2 {
		t.Errorf("refresh calls = %d, want 2 (one per 401)", got)
	}
}

// TestUnauthorizedPostReplaysTheSameBody pins the design decision the request
// struct is built on: the body is buffered, so the replay that follows a 401
// sends it again instead of an empty one (a json.Encoder streamed into the
// first attempt could not be replayed at all).
func TestUnauthorizedPostReplaysTheSameBody(t *testing.T) {
	var bodies []string
	var refreshed atomic.Bool

	c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/transactions":
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading body: %v", err)
			}
			bodies = append(bodies, string(raw))
			if !refreshed.Load() {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"errors":[{"message":"invalid or expired token"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"txn-1"}`))
		case "/api/v1/auth/refresh":
			refreshed.Store(true)
			sessionCookies(w, "access-2", "refresh-1")
			_, _ = w.Write([]byte(`{"message":"token refreshed"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	c.SetTokens("access-1", "refresh-1")
	id, err := c.CreateTransaction(context.Background(), CreateTransactionRequest{
		AccountID: "acct-1", Date: "2026-01-01", Description: "coffee", Amount: "10.00", Type: "debit",
	})
	if err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}
	if id != "txn-1" {
		t.Errorf("id = %q, want the replayed response", id)
	}
	if len(bodies) != 2 {
		t.Fatalf("saw %d POSTs, want 2 (original + replay)", len(bodies))
	}
	if bodies[0] == "" || bodies[0] != bodies[1] {
		t.Errorf("replay body = %q, want the original %q", bodies[1], bodies[0])
	}
	if !strings.Contains(bodies[1], `"description":"coffee"`) {
		t.Errorf("replay body = %q, want the encoded transaction", bodies[1])
	}
}

// TestLongCallsGetTheirOwnDeadline covers the two endpoints whose work does not
// fit the 60s JSON default: a whole-database restore (the upload plus a row-by-
// row replay inside one transaction) and a statement parse (queued behind the
// server's four-way parse semaphore, then forwarded to the parser). Expiring
// mid-restore reports a failure for a transaction the server still commits, and
// the retry is refused with 409. The deadline the client actually sends is
// observed on the transport, and the ordinary call is there as the control.
func TestLongCallsGetTheirOwnDeadline(t *testing.T) {
	tests := []struct {
		name string
		body string
		want time.Duration
		call func(ctx context.Context, c *Client) error
	}{
		{
			name: "restore",
			body: `{"accounts":1}`,
			want: importTimeout,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.ImportBackup(ctx, []byte(`{"format":"fintrak.backup","version":1}`))
				return err
			},
		},
		{
			name: "statement parse",
			body: `{"transactions":[],"pageCount":1}`,
			want: parseTimeout,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.ParseStatement(ctx, "statement.pdf", []byte("%PDF-1.4"), "", "", "")
				return err
			},
		},
		{
			name: "control: an ordinary call keeps the JSON default",
			body: `[]`,
			want: requestTimeout,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.ListAccounts(ctx)
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New("http://example.invalid/api/v1")
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			var got time.Duration
			c.SetTransport(transportFunc(func(req *http.Request) (*http.Response, error) {
				deadline, ok := req.Context().Deadline()
				if !ok {
					return nil, errors.New("request carries no deadline")
				}
				got = time.Until(deadline)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(tc.body)),
					Request:    req,
				}, nil
			}))

			if err := tc.call(context.Background(), c); err != nil {
				t.Fatalf("call: %v", err)
			}
			if got < tc.want-10*time.Second || got > tc.want {
				t.Errorf("deadline = %v, want the %v budget", got, tc.want)
			}
		})
	}
}

// TestLogoutClearsSessionLocally ensures a sign-out cannot leave usable tokens
// behind even if the server call fails.
func TestLogoutClearsSessionLocally(t *testing.T) {
	c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/logout" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Cookie"); got != RefreshCookieName+"=refresh-1" {
			t.Errorf("logout Cookie header = %q, want the refresh token", got)
		}
		_, _ = w.Write([]byte(`{"message":"logged out"}`))
	})

	c.SetTokens("access-1", "refresh-1")
	if err := c.Logout(context.Background()); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if c.HasSession() {
		t.Error("logout must clear the session")
	}
}

// TestLogoutRevokesRefreshTokenForAnotherClient proves that the shared client
// presents the refresh cookie the backend needs to revoke the rotation family.
// A second client with a copy of the same tokens must not be able to refresh
// after the first client logs out.
func TestLogoutRevokesRefreshTokenForAnotherClient(t *testing.T) {
	var revoked atomic.Bool
	c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/logout":
			if got := r.Header.Get("Cookie"); got != RefreshCookieName+"=refresh-1" {
				t.Errorf("logout Cookie header = %q, want the refresh token", got)
			}
			revoked.Store(true)
			_, _ = w.Write([]byte(`{"message":"logged out"}`))
		case "/api/v1/auth/refresh":
			if revoked.Load() {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"errors":[{"message":"refresh token revoked"}]}`))
				return
			}
			t.Errorf("refresh unexpectedly reached the server after logout")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected refresh"}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	other, err := New(c.BaseURL())
	if err != nil {
		t.Fatalf("New second client: %v", err)
	}
	c.SetTokens("access-1", "refresh-1")
	other.SetTokens("access-1", "refresh-1")

	if err := c.Logout(context.Background()); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if err := other.RefreshSession(context.Background()); !Unauthorized(err) {
		t.Fatalf("copied refresh token error = %v, want unauthorized", err)
	}
}

func TestAPIErrorMessages(t *testing.T) {
	tests := []struct {
		name   string
		status int
		header http.Header
		body   string
		want   string
	}{
		{
			name:   "validation envelope joins field errors",
			status: http.StatusBadRequest,
			body:   `{"errors":[{"field":"amount","message":"must be positive"},{"field":"date","message":"required"}]}`,
			want:   "amount: must be positive; date: required",
		},
		{
			name:   "envelope without a field",
			status: http.StatusConflict,
			body:   `{"errors":[{"message":"no accounts may exist"}]}`,
			want:   "no accounts may exist",
		},
		{
			name:   "recovered panic uses a different key",
			status: http.StatusInternalServerError,
			body:   `{"error":"internal server error"}`,
			want:   "internal server error",
		},
		{
			name:   "rate limit reports the retry hint",
			status: http.StatusTooManyRequests,
			header: http.Header{"Retry-After": []string{"30"}},
			body:   `{"errors":[{"message":"too many attempts"}]}`,
			want:   "too many attempts",
		},
		{
			name:   "non-JSON body falls back to its text",
			status: http.StatusBadGateway,
			body:   "upstream is unavailable\n",
			want:   "upstream is unavailable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tc.status, Header: tc.header, Status: http.StatusText(tc.status)}
			err := newAPIError(resp, []byte(tc.body))
			if err := err.Error(); !strings.Contains(err, tc.want) {
				t.Errorf("message = %q, want it to contain %q", err, tc.want)
			}
			if !StatusIs(err, tc.status) {
				t.Errorf("StatusIs(%d) = false", tc.status)
			}
		})
	}
}

func TestRateLimitWithoutEnvelopeNamesTheRetryDelay(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"30"}},
	}
	err := newAPIError(resp, nil)
	if !strings.Contains(err.Message(), "30") {
		t.Errorf("message = %q, want the retry delay", err.Message())
	}
}

// wrappedErr is a stand-in for any caller that wraps an API error on its way up
// the stack.
type wrappedErr struct{ err error }

func (w wrappedErr) Error() string { return "wrapped: " + w.err.Error() }
func (w wrappedErr) Unwrap() error { return w.err }

// TestUnauthorizedUnwrapsWrappedErrors keeps the helper honest for callers that
// wrap the API error on the way up.
func TestUnauthorizedUnwrapsWrappedErrors(t *testing.T) {
	base := &APIError{Status: http.StatusUnauthorized}
	if Unauthorized(errors.New("context: " + base.Error())) {
		t.Error("a non-APIError must not read as unauthorized")
	}
	if !Unauthorized(wrappedErr{err: base}) {
		t.Error("a wrapped APIError must still read as unauthorized")
	}
}

// transportFunc adapts a function to an http.RoundTripper.
type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// TestSetTransportWrapsEveryRequest covers the hook the MCP server installs its
// read-only guard through: the transport sees the request before the network,
// and a transport that refuses makes the call fail.
func TestSetTransportWrapsEveryRequest(t *testing.T) {
	c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("request reached the server: %s %s", r.Method, r.URL.Path)
	})

	var seen []string
	c.SetTransport(transportFunc(func(req *http.Request) (*http.Response, error) {
		seen = append(seen, req.Method+" "+req.URL.Path)
		return nil, errors.New("refused by policy")
	}))

	if _, err := c.ListAccounts(context.Background()); err == nil {
		t.Fatal("a refusing transport must fail the call")
	}
	if len(seen) != 1 || seen[0] != "GET /api/v1/accounts" {
		t.Fatalf("transport saw %v, want the accounts request", seen)
	}

	// A nil transport restores the default one, so the client works again.
	c.SetTransport(nil)
	if c.http.Transport != nil {
		t.Error("SetTransport(nil) should restore the default transport")
	}
}

// TestLoginFailureReportsTheAPIsOwnError pins the credential-carrying auth
// requests: a 401 from /auth/login rejects the credentials that were just sent,
// so it must surface the server's message rather than a refresh attempt's
// "session expired".
func TestLoginFailureReportsTheAPIsOwnError(t *testing.T) {
	var refreshed bool
	c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			if cookie := r.Header.Get("Cookie"); cookie != "" {
				t.Errorf("login sent a session cookie: %q", cookie)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"errors":[{"message":"invalid email or password"}]}`))
		case "/api/v1/auth/refresh":
			refreshed = true
			w.WriteHeader(http.StatusUnauthorized)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})

	_, err := c.Login(context.Background(), "a@b.c", "wrong")
	if err == nil {
		t.Fatal("a rejected login must fail")
	}
	if !StatusIs(err, http.StatusUnauthorized) {
		t.Fatalf("error = %v, want a 401 APIError", err)
	}
	if !strings.Contains(err.Error(), "invalid email or password") {
		t.Errorf("error = %q, want the server's message", err)
	}
	if refreshed {
		t.Error("a rejected login must not try to refresh a session it never had")
	}
}
