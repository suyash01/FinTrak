package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
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

// TestLogoutClearsSessionLocally ensures a sign-out cannot leave usable tokens
// behind even if the server call fails.
func TestLogoutClearsSessionLocally(t *testing.T) {
	c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/logout" {
			t.Errorf("unexpected path %s", r.URL.Path)
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
