// Package api is a hand-written client for the FinTrak REST API.
//
// The TUI is not a browser, and that shapes the auth design:
//
//   - POST /auth/login (and /auth/register) deliver the session as two httpOnly
//     cookies. This client reads them off the Set-Cookie headers instead of
//     using net/http/cookiejar, because the jar enforces the Secure attribute
//     when sending ("if c.Secure && u.Scheme != \"https\" { continue }" in
//     net/http/cookiejar), so against a backend built with COOKIE_SECURE=true it
//     silently drops both cookies over plain HTTP and every request 401s.
//     Path, SameSite and Secure are browser-side policy that does not bind a
//     non-browser client, so the tokens are attached explicitly instead: the
//     access token on every request, the refresh token only to /auth/refresh,
//     matching the server-side scoping.
//   - A 401 replays the request once after a single-flight refresh, so the
//     15-minute access-token lifetime is invisible to the UI (a refresh never
//     extends the 30-day absolute deadline).
//
// Cookie names mirror backend/auth/auth.go (AccessCookieName,
// RefreshCookieName, RefreshCookiePath).
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultBaseURL matches the backend's default listen address; the frontend
	// and nginx both proxy /api/v1 to it.
	DefaultBaseURL = "http://localhost:8080/api/v1"

	// AccessCookieName and RefreshCookieName mirror auth.AccessCookieName and
	// auth.RefreshCookieName.
	AccessCookieName  = "fintrak_token"
	RefreshCookieName = "fintrak_refresh"

	requestTimeout   = 60 * time.Second
	streamTimeout    = 10 * time.Minute
	maxResponseBytes = 64 << 20

	// importTimeout is the budget for POST /import. A restore decodes a bundle
	// the route accepts up to 256 MB of and replays it row by row inside one
	// transaction, so the upload plus the replay routinely outlasts the 60s
	// JSON default; expiring mid-restore would report a failure for a
	// transaction the server is still committing (and the retry is refused with
	// 409 "user already has accounts"). It matches the frontend's import
	// timeout (320s) and the backend's raised write timeout.
	importTimeout = 320 * time.Second

	// parseTimeout is the budget for POST /statements/parse. The handler queues
	// the upload behind its four-way parse semaphore and then waits up to 60s
	// for the parser service, so a request can legitimately outlive the JSON
	// default without anything being wrong. It matches the frontend's statement
	// timeout (90s) and nginx's raised proxy read timeout for the route.
	parseTimeout = 90 * time.Second
)

// UserAgent identifies the client to the backend. main sets it to include the
// build version.
var UserAgent = "fintrak-tui"

// errNoSession means a 401 arrived with no refresh token to trade for a new
// access token.
var errNoSession = errors.New("no session; sign in first")

// Client is a concurrency-safe FinTrak API client. One Client holds one user's
// session, so the SSH TUI server gives each session its own.
type Client struct {
	base *url.URL
	http *http.Client

	// refreshMu serializes refresh attempts so a burst of concurrent 401s
	// produces one POST /auth/refresh rather than one per in-flight request.
	refreshMu sync.Mutex

	mu      sync.Mutex
	access  string
	refresh string
	gen     uint64 // bumped on every token change
}

// New builds a client for an API base URL such as
// http://localhost:8080/api/v1. An empty base URL falls back to
// DefaultBaseURL, so a locally-run TUI needs no configuration.
func New(baseURL string) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("invalid API URL %q: %w", baseURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("invalid API URL %q: scheme must be http or https", baseURL)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("invalid API URL %q: missing host", baseURL)
	}
	return &Client{base: u, http: &http.Client{}}, nil
}

// BaseURL returns the API base URL the client was built with.
func (c *Client) BaseURL() string { return c.base.String() }

// SetTransport routes every request through rt, which is how a caller wraps the
// client in a policy rather than a decorator: the MCP server installs a
// read-only guard here so a request the tools never intend to make cannot leave
// the process. A nil transport restores the default one. It must be called
// before the client is used concurrently.
func (c *Client) SetTransport(rt http.RoundTripper) {
	c.http.Transport = rt
}

// SetTokens installs a session outright.
func (c *Client) SetTokens(access, refresh string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setTokensLocked(access, refresh)
}

// AccessToken returns the current access token ("" when signed out).
func (c *Client) AccessToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.access
}

// HasSession reports whether an access token is held.
func (c *Client) HasSession() bool { return c.AccessToken() != "" }

// ClearSession drops both tokens locally without calling the server.
func (c *Client) ClearSession() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setTokensLocked("", "")
}

func (c *Client) setTokensLocked(access, refresh string) {
	if access == c.access && refresh == c.refresh {
		return
	}
	c.access, c.refresh = access, refresh
	c.gen++
}

func (c *Client) generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

func (c *Client) refreshToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refresh
}

// request is one API call, independent of the transport so a 401 can replay it
// after refreshing. The body is buffered rather than streamed precisely so it
// survives that replay.
type request struct {
	method      string
	path        string
	query       url.Values
	body        []byte
	contentType string

	// noAuth suppresses the access-token cookie and the 401-refresh replay:
	// the credential-carrying auth endpoints, where a 401 is a rejection of the
	// credentials the caller just sent rather than an expired session.
	noAuth bool
	// cookie, when set, is sent verbatim instead of the access token, so the
	// refresh call can present the refresh token without leaking it elsewhere.
	cookie string

	// timeout, when non-zero, replaces the 60s JSON default for this call.
	// Only the endpoints whose work legitimately outlives it (a whole-database
	// restore, a statement parse waiting on the parser) set it, through
	// withTimeout.
	timeout time.Duration

	marshalErr error
}

func get(path string) *request  { return &request{method: http.MethodGet, path: path} }
func post(path string) *request { return &request{method: http.MethodPost, path: path} }
func put(path string) *request  { return &request{method: http.MethodPut, path: path} }
func del(path string) *request  { return &request{method: http.MethodDelete, path: path} }

func patch(path string) *request {
	return &request{method: http.MethodPatch, path: path}
}

// withoutAuth marks the request as credential-carrying: no access token is
// attached and a 401 is reported as-is instead of triggering a refresh.
func (r *request) withoutAuth() *request {
	r.noAuth = true
	return r
}

// withTimeout gives the request its own deadline in place of the 60s JSON
// default, for the endpoints whose work legitimately outlives it.
func (r *request) withTimeout(d time.Duration) *request {
	r.timeout = d
	return r
}

// withJSON attaches a JSON body. A nil body sends none.
func (r *request) withJSON(body any) *request {
	if body == nil {
		return r
	}
	data, err := json.Marshal(body)
	if err != nil {
		// Encoding a request body is a programming error; do() surfaces it.
		r.marshalErr = err
		return r
	}
	r.body, r.contentType = data, "application/json"
	return r
}

// withRawJSON attaches an already-encoded JSON body (a backup bundle read from
// disk, for instance). Encoding failures cannot happen here, so the caller
// owns the validity of data.
func (r *request) withRawJSON(data []byte) *request {
	r.body, r.contentType = data, "application/json"
	return r
}

// addQuery appends a repeatable parameter, which the Paperless document list
// uses for its include/exclude filters.
func (r *request) addQuery(key, value string) *request {
	if strings.TrimSpace(value) == "" {
		return r
	}
	if r.query == nil {
		r.query = url.Values{}
	}
	r.query.Add(key, value)
	return r
}

// addQueryList appends one repeated key per value.
func (r *request) addQueryList(key string, values []string) *request {
	for _, v := range values {
		r.addQuery(key, v)
	}
	return r
}

// setQuery sets a parameter, skipping empty values so optional filters pass
// straight through from the UI. An explicit value (including "false") goes
// through setQueryValue.
func (r *request) setQuery(key, value string) *request {
	if strings.TrimSpace(value) == "" {
		return r
	}
	return r.setQueryValue(key, value)
}

// setQueryValue always sets the parameter.
func (r *request) setQueryValue(key, value string) *request {
	if r.query == nil {
		r.query = url.Values{}
	}
	r.query.Set(key, value)
	return r
}

// setQueryInt sets an integer parameter when it is non-zero.
func (r *request) setQueryInt(key string, value int) *request {
	if value == 0 {
		return r
	}
	return r.setQueryValue(key, strconv.Itoa(value))
}

// send performs the call, refreshing and replaying once on a 401.
func (c *Client) send(ctx context.Context, r *request) (*http.Response, error) {
	if r.marshalErr != nil {
		return nil, fmt.Errorf("encoding request body: %w", r.marshalErr)
	}
	gen := c.generation()
	resp, err := c.roundTrip(ctx, r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized || r.noAuth {
		return resp, nil
	}
	closeBody(resp.Body)

	if _, err := c.refreshAccess(ctx, gen); err != nil {
		if errors.Is(err, errNoSession) {
			return nil, &APIError{
				Status: http.StatusUnauthorized,
				Errors: []FieldError{{Message: "session expired — sign in again"}},
			}
		}
		return nil, err
	}
	return c.roundTrip(ctx, r)
}

// roundTrip builds and executes one HTTP request, recording any session cookies
// the response carries.
func (c *Client) roundTrip(ctx context.Context, r *request) (*http.Response, error) {
	req, err := c.newHTTPRequest(ctx, r)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	c.captureSession(resp)
	return resp, nil
}

func (c *Client) newHTTPRequest(ctx context.Context, r *request) (*http.Request, error) {
	u := *c.base
	u.Path = strings.TrimRight(c.base.Path, "/") + r.path
	if len(r.query) > 0 {
		u.RawQuery = r.query.Encode()
	}

	var body io.Reader
	if len(r.body) > 0 {
		body = bytes.NewReader(r.body)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if UserAgent != "" {
		req.Header.Set("User-Agent", UserAgent)
	}
	if r.contentType != "" {
		req.Header.Set("Content-Type", r.contentType)
	}
	if len(r.body) > 0 {
		req.ContentLength = int64(len(r.body))
	}
	switch {
	case r.cookie != "":
		req.Header.Set("Cookie", r.cookie)
	case !r.noAuth:
		if token := c.AccessToken(); token != "" {
			req.Header.Set("Cookie", AccessCookieName+"="+token)
		}
	}
	return req, nil
}

// captureSession records fintrak_token and fintrak_refresh from a response. The
// server clears a cookie (MaxAge < 0) on logout or when a refresh is rejected,
// which drops the local session too.
func (c *Client) captureSession(resp *http.Response) {
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	access, refresh := c.access, c.refresh
	for _, ck := range cookies {
		cleared := ck.MaxAge < 0 || ck.Value == ""
		switch ck.Name {
		case AccessCookieName:
			if cleared {
				access = ""
			} else {
				access = ck.Value
			}
		case RefreshCookieName:
			if cleared {
				refresh = ""
			} else {
				refresh = ck.Value
			}
		}
	}
	c.setTokensLocked(access, refresh)
}

// refreshAccess trades the refresh token for a new access token. Callers that
// raced on the same 401 collapse into one exchange: whoever takes the lock
// second sees the generation has already moved and returns immediately.
func (c *Client) refreshAccess(ctx context.Context, gen uint64) (uint64, error) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	if current := c.generation(); current != gen {
		return current, nil
	}
	token := c.refreshToken()
	if token == "" {
		return gen, errNoSession
	}

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	r := &request{
		method: http.MethodPost,
		path:   "/auth/refresh",
		noAuth: true,
		cookie: RefreshCookieName + "=" + token,
	}
	resp, err := c.roundTrip(ctx, r)
	if err != nil {
		return gen, err
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Only a rejection ends the session. The backend clears the cookies it
		// sent when it refuses the refresh (an expired or unknown token) and
		// answers a transient failure — a role-lookup DB error, a restarting
		// pooler, a proxy blip — with a plain 5xx that leaves the 30-day
		// refresh token intact. Dropping it locally there would force a fresh
		// sign-in for a session the server never rejected, so the tokens stay
		// and the next 401 retries the refresh.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			c.ClearSession()
		}
		return gen, newAPIError(resp, body)
	}
	if readErr != nil {
		return gen, readErr
	}
	if current := c.generation(); current == gen {
		// A 200 that set no cookie leaves the session unusable.
		c.ClearSession()
		return gen, errNoSession
	}
	return c.generation(), nil
}

// do executes a request and decodes its JSON response into T. An empty body
// leaves T zeroed, which covers the DELETE endpoints that return nothing. The
// deadline is the 60s JSON default unless the request carries its own (a
// restore, a statement parse), in which case the 401 replay shares it.
func do[T any](ctx context.Context, c *Client, r *request) (T, error) {
	var zero T
	timeout := requestTimeout
	if r.timeout > 0 {
		timeout = r.timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := c.send(ctx, r)
	if err != nil {
		return zero, err
	}
	defer closeBody(resp.Body)

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return zero, fmt.Errorf("reading %s %s response: %w", r.method, r.path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return zero, newAPIError(resp, data)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return zero, nil
	}
	if err := json.Unmarshal(data, &zero); err != nil {
		return zero, fmt.Errorf("decoding %s %s response: %w", r.method, r.path, err)
	}
	return zero, nil
}

// stream performs a request whose body is not JSON (CSV and JSON-bundle
// downloads). The caller closes the returned body, which releases the
// request's deadline.
func (c *Client) stream(ctx context.Context, r *request) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, streamTimeout)
	resp, err := c.send(ctx, r)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// cancelBody cancels a request's context when the body is closed.
type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// multipartFile is one uploaded file. The content is buffered so the request
// survives the 401 refresh replay.
type multipartFile struct {
	field    string
	filename string
	content  []byte
}

// uploadRequest builds a multipart/form-data request.
func uploadRequest(path string, fields map[string]string, file *multipartFile) (*request, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	for key, value := range fields {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if err := w.WriteField(key, value); err != nil {
			return nil, fmt.Errorf("building upload: %w", err)
		}
	}
	if file != nil {
		field := file.field
		if field == "" {
			field = "file"
		}
		part, err := w.CreateFormFile(field, file.filename)
		if err != nil {
			return nil, fmt.Errorf("building upload: %w", err)
		}
		if _, err := part.Write(file.content); err != nil {
			return nil, fmt.Errorf("building upload: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("building upload: %w", err)
	}
	return &request{
		method:      http.MethodPost,
		path:        path,
		body:        buf.Bytes(),
		contentType: w.FormDataContentType(),
	}, nil
}
