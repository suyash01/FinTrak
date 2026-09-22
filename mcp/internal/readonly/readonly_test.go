package readonly

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubTransport records what reached the network and answers 200. The request
// URI is recorded rather than the path, so a test can prove the query survived
// the guard instead of being dropped with the match.
type stubTransport struct{ seen []string }

func (s *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.seen = append(s.seen, req.Method+" "+req.URL.RequestURI())
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
		Header:     http.Header{},
		Request:    req,
	}, nil
}

// testRoutes is the shape the MCP server's allowlist has: plain reads plus the
// two preview POSTs, and a session route the client calls itself.
func testRoutes() []Route {
	return []Route{
		{Method: http.MethodGet, Path: "/accounts"},
		{Method: http.MethodGet, Path: "/accounts/{id}/loan-schedule"},
		{Method: http.MethodGet, Path: "/transactions"},
		{Method: http.MethodPost, Path: "/rules/preview"},
		{Method: http.MethodPost, Path: "/auth/login"},
	}
}

// TestGuardAllowsListedRoutesAndRefusesEverythingElse is the whole point of the
// package: a request the allowlist does not name never reaches the transport.
func TestGuardAllowsListedRoutesAndRefusesEverythingElse(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		path    string
		allowed bool
	}{
		{name: "listed read", method: http.MethodGet, path: "/api/v1/accounts", allowed: true},
		{name: "listed read with a query", method: http.MethodGet, path: "/api/v1/transactions?accountId=x&limit=5", allowed: true},
		{name: "a query cannot name another route", method: http.MethodPost, path: "/api/v1/accounts?path=/transactions/validate", allowed: false},
		{name: "a query cannot widen the allowlist", method: http.MethodGet, path: "/api/v1/payees?next=/accounts", allowed: false},
		{name: "listed preview post", method: http.MethodPost, path: "/api/v1/rules/preview", allowed: true},
		{name: "listed session route", method: http.MethodPost, path: "/api/v1/auth/login", allowed: true},
		{name: "placeholder matches one segment", method: http.MethodGet, path: "/api/v1/accounts/9f1c/loan-schedule", allowed: true},
		{name: "write is refused", method: http.MethodPost, path: "/api/v1/transactions", allowed: false},
		{name: "delete is refused", method: http.MethodDelete, path: "/api/v1/accounts/9f1c", allowed: false},
		{name: "patch is refused", method: http.MethodPatch, path: "/api/v1/transactions/9f1c", allowed: false},
		{name: "unlisted read is refused", method: http.MethodGet, path: "/api/v1/payees", allowed: false},
		{name: "placeholder needs a segment", method: http.MethodGet, path: "/api/v1/accounts/loan-schedule", allowed: false},
		{name: "placeholder takes one segment only", method: http.MethodGet, path: "/api/v1/accounts/a/b/loan-schedule", allowed: false},
		{name: "path outside the base is refused", method: http.MethodGet, path: "/accounts", allowed: false},
		{name: "method must match too", method: http.MethodHead, path: "/api/v1/accounts", allowed: false},
	}

	next := &stubTransport{}
	guard := New(next, "/api/v1", testRoutes())
	client := &http.Client{Transport: guard}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := len(next.seen)
			req, err := http.NewRequestWithContext(context.Background(), tc.method, "http://api.test"+tc.path, nil)
			if err != nil {
				t.Fatalf("building the request: %v", err)
			}
			resp, err := client.Do(req)
			if resp != nil {
				_ = resp.Body.Close()
			}

			switch {
			case tc.allowed && err != nil:
				t.Fatalf("allowed request failed: %v", err)
			case !tc.allowed && err == nil:
				t.Fatalf("refused request reached the transport")
			case !tc.allowed && !errors.Is(err, ErrNotAllowed):
				t.Fatalf("error = %v, want ErrNotAllowed", err)
			}
			if want := len(next.seen); tc.allowed && want != before+1 {
				t.Errorf("transport saw %d requests, want %d", want, before+1)
			} else if !tc.allowed && want != before {
				t.Errorf("refused request reached the transport (%v)", next.seen[before:])
			}
		})
	}
}

// TestGuardIgnoresTheQueryButForwardsIt pins the two halves of the matcher's
// query contract. Matching looks at the path alone — the write-on-GET shapes
// this allowlist documents are triggered by query parameters, so a query must
// never decide which route matched, in either direction — while the query itself
// still reaches the transport untouched, or every filtered read would silently
// become an unfiltered one.
func TestGuardIgnoresTheQueryButForwardsIt(t *testing.T) {
	next := &stubTransport{}
	guard := New(next, "/api/v1", testRoutes())
	client := &http.Client{Transport: guard}

	const target = "http://api.test/api/v1/transactions?accountId=x&next=%2Ftransactions%2Fbulk-delete"
	resp, err := client.Get(target)
	if err != nil {
		t.Fatalf("a listed read with a query was refused: %v", err)
	}
	_ = resp.Body.Close()

	want := "GET /api/v1/transactions?accountId=x&next=%2Ftransactions%2Fbulk-delete"
	if len(next.seen) != 1 || next.seen[0] != want {
		t.Fatalf("the transport saw %v, want %q", next.seen, want)
	}
}

// TestGuardWithoutABasePathMatchesDocumentedPaths covers a client configured at
// the server root, where the allowlist needs no stripping.
func TestGuardWithoutABasePathMatchesDocumentedPaths(t *testing.T) {
	guard := New(&stubTransport{}, "", testRoutes())
	if !guard.Allows(http.MethodGet, "/accounts") {
		t.Error("a root-based client should match the documented path")
	}
	if guard.Allows(http.MethodGet, "/api/v1/accounts") {
		t.Error("a root-based client must not match a prefixed path")
	}
}

// TestRouteString covers the rendering the audit tests use to report a
// mismatch, which has to name the method and the template.
func TestRouteString(t *testing.T) {
	route := Route{Method: http.MethodGet, Path: "/accounts/{id}/loan-schedule"}
	if got := route.String(); got != "GET /accounts/{id}/loan-schedule" {
		t.Errorf("Route.String() = %q", got)
	}
}

// TestGuardFallsBackToTheDefaultTransport keeps a nil transport from turning
// every request into a nil-pointer panic.
func TestGuardFallsBackToTheDefaultTransport(t *testing.T) {
	guard := New(nil, "", testRoutes())
	if guard.next == nil {
		t.Fatal("a nil transport must fall back to the default one")
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://api.test/accounts", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	if _, err := guard.RoundTrip(req); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("error = %v, want ErrNotAllowed", err)
	}
}

// TestGuardWorksInFrontOfARealServer exercises the transport the way the client
// installs it, so the refusal is proven end to end rather than at the matcher.
func TestGuardWorksInFrontOfARealServer(t *testing.T) {
	var hits []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer upstream.Close()

	guard := New(http.DefaultTransport, "/api/v1", testRoutes())
	client := &http.Client{Transport: guard}

	resp, err := client.Get(upstream.URL + "/api/v1/accounts")
	if err != nil {
		t.Fatalf("allowed GET failed: %v", err)
	}
	_ = resp.Body.Close()

	if _, err := client.Post(upstream.URL+"/api/v1/accounts", "application/json", nil); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("write error = %v, want ErrNotAllowed", err)
	}
	if len(hits) != 1 || hits[0] != "GET /api/v1/accounts" {
		t.Fatalf("upstream saw %v, want only the read", hits)
	}
}
