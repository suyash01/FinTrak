// Package readonly constrains an HTTP client to an allowlist of API
// operations.
//
// It exists because the MCP server hands a model a set of tools backed by the
// full FinTrak client: the tools only ever call reads, but "only ever" is a
// property of the tool list, which a later edit can break. A Guard sits in the
// transport instead, so a request outside the allowlist cannot leave the
// process even if a tool calls the wrong client method.
//
// A Route is a method plus a path template relative to the API base path, with
// {name} standing for exactly one path segment ("/accounts/{id}/loan-schedule"
// matches "/accounts/9f.../loan-schedule", not "/accounts" or
// "/accounts/a/b"). GET is read-only by definition; the allowlist admits a POST
// only when the API documents it as a preview that writes nothing
// (POST /transactions/validate, POST /rules/preview).
package readonly

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrNotAllowed is returned (wrapped) for a request outside the allowlist, so a
// caller can tell a policy refusal from a transport failure.
var ErrNotAllowed = errors.New("read-only: request not allowed")

// Route is one API operation: an HTTP method and a path template.
type Route struct {
	Method string
	Path   string
}

// String renders the route as "GET /accounts/{id}".
func (r Route) String() string { return r.Method + " " + r.Path }

// Guard is an http.RoundTripper that refuses any request that is not one of the
// allowed routes. Requests are matched after the API base path is stripped, so
// the allowlist is written the way the API documents its paths rather than the
// way a client's base URL happens to be configured.
type Guard struct {
	next  http.RoundTripper
	base  string
	allow []Route
}

// New builds a guard in front of next. basePath is the API base path the client
// was configured with (url.URL.Path of its base URL, e.g. "/api/v1"); a request
// whose path does not start with it is refused. A nil next falls back to
// http.DefaultTransport.
func New(next http.RoundTripper, basePath string, allow []Route) *Guard {
	if next == nil {
		next = http.DefaultTransport
	}
	base := "/" + strings.Trim(basePath, "/")
	if base == "/" {
		base = ""
	}
	return &Guard{next: next, base: base, allow: allow}
}

// RoundTrip refuses anything but an allowed route, so a call that was never
// meant to be made fails before it reaches the network.
func (g *Guard) RoundTrip(req *http.Request) (*http.Response, error) {
	path, ok := g.relative(req.URL.Path)
	if !ok || !g.Allows(req.Method, path) {
		return nil, fmt.Errorf("%w: %s %s", ErrNotAllowed, req.Method, req.URL.Path)
	}
	return g.next.RoundTrip(req)
}

// Allows reports whether method and path (already relative to the API base) are
// on the allowlist.
func (g *Guard) Allows(method, path string) bool {
	for _, r := range g.allow {
		if Match(r, method, path) {
			return true
		}
	}
	return false
}

// Match reports whether a concrete method and path (relative to the API base)
// satisfy one route. It is exported so a caller can check a request it actually
// observed against the route it declared, which is how the MCP server's tests
// prove each tool hits the operation it claims.
func Match(route Route, method, path string) bool {
	return route.Method == method && matchPath(route.Path, path)
}

// relative strips the API base path from a request path, reporting false for a
// path outside the base. Such a path is refused rather than matched against the
// allowlist, so a route template can never be satisfied by the wrong prefix.
func (g *Guard) relative(path string) (string, bool) {
	if g.base == "" {
		return path, true
	}
	rest, ok := strings.CutPrefix(path, g.base)
	if !ok || !strings.HasPrefix(rest, "/") {
		return path, false
	}
	return rest, true
}

// matchPath reports whether a concrete path satisfies a route template. Both
// sides are split into segments with any leading or trailing slash ignored, so
// "{name}" matches exactly one non-empty segment: "/accounts/{id}" matches
// "/accounts/9f" but neither "/accounts" nor "/accounts/a/b".
func matchPath(pattern, path string) bool {
	want := strings.Split(strings.Trim(pattern, "/"), "/")
	got := strings.Split(strings.Trim(path, "/"), "/")
	if len(want) != len(got) {
		return false
	}
	for i, w := range want {
		if isPlaceholder(w) {
			if got[i] == "" {
				return false
			}
			continue
		}
		if w != got[i] {
			return false
		}
	}
	return true
}

// isPlaceholder reports whether a template segment is a {name} capture.
func isPlaceholder(seg string) bool {
	return len(seg) > 2 && seg[0] == '{' && seg[len(seg)-1] == '}'
}
