package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/internal/crypto"
	"github.com/fintrak/backend/internal/logger"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// paperlessClientTimeout bounds calls to the user's Paperless-ngx instance.
const paperlessClientTimeout = 60 * time.Second

// tagPaperlessTimeout bounds the whole post-import tagging batch. Each document
// can take several round-trips, so the batch runs under one deadline instead of
// unboundedly (the per-call client timeout alone would allow N docs × 60s).
const tagPaperlessTimeout = 3 * time.Minute

// tagPaperlessConcurrency caps how many documents are tagged in parallel, so a
// large import doesn't hammer the user's Paperless instance.
const tagPaperlessConcurrency = 4

// maxPaperlessResponse bounds upstream JSON/body reads from Paperless so a
// compromised or malicious instance can't exhaust backend memory.
const maxPaperlessResponse = 20 << 20 // 20 MB

// maxPaperlessDocument bounds a statement PDF downloaded from Paperless.
const maxPaperlessDocument = 20 << 20 // 20 MB

// defaultPaperlessPageSize is the page size requested from Paperless when the
// caller does not specify one.
const defaultPaperlessPageSize = 25

// maxPaperlessPageSize mirrors Paperless-ngx's server-side cap on page_size.
const maxPaperlessPageSize = 100

// paperlessConfig loads a user's Paperless-ngx settings from the users row. The
// stored API token may be encrypted at rest; it is decrypted on demand by
// paperlessToken so read-only paths never need it. A legacy v1-format token is
// transparently re-sealed under the current v2 derivation, which makes the read
// paths that call this helper (GET /paperless/settings, GET
// /paperless/documents among them) potential writers — see upgradeLegacyToken
// for how narrow that write is kept.
func (srv *Server) paperlessConfig(ctx context.Context, userID uuid.UUID) (models.UserSettings, error) {
	var s models.UserSettings
	err := srv.db.QueryRow(ctx,
		"SELECT paperless_url, paperless_token, paperless_tag, page_size FROM users WHERE id = $1",
		userID,
	).Scan(&s.PaperlessURL, &s.PaperlessToken, &s.PaperlessTag, &s.PageSize)
	if err != nil {
		return s, err
	}
	s.PaperlessToken = srv.upgradeLegacyToken(ctx, userID, s.PaperlessToken)
	return s, nil
}

// upgradeLegacyToken re-encrypts a v1-format token under the current HKDF (v2)
// derivation and persists it, so legacy ciphertext converges on the stronger
// key derivation over time. Best-effort: any failure leaves the original value
// in place so reads still work.
//
// The write is a compare-and-swap on the exact value that was read, so a
// concurrent settings save that stored a new token is never clobbered with the
// re-sealed old one (the read and the write are not in one transaction). It is
// also naturally one-shot: once a row holds a v2 token IsLegacy is false and no
// further writes happen.
func (srv *Server) upgradeLegacyToken(ctx context.Context, userID uuid.UUID, token string) string {
	if !crypto.IsLegacy(token) || tokenEncryptionKey == "" {
		return token
	}
	plaintext, err := crypto.Decrypt(token, tokenEncryptionKey)
	if err != nil {
		slog.Debug("paperless token upgrade skipped (decrypt failed)", slog.String("error", err.Error()))
		return token
	}
	resealed, err := crypto.Encrypt(plaintext, tokenEncryptionKey)
	if err != nil {
		slog.Debug("paperless token upgrade skipped (encrypt failed)", slog.String("error", err.Error()))
		return token
	}
	tag, err := srv.db.Exec(ctx,
		"UPDATE users SET paperless_token = $1 WHERE id = $2 AND paperless_token = $3",
		resealed, userID, token)
	if err != nil {
		slog.Debug("paperless token upgrade skipped (persist failed)", slog.String("error", err.Error()))
		return token
	}
	if tag.RowsAffected() == 0 {
		// The row changed under us (another read re-sealed it first, or the
		// user saved a new token). The value this caller read is still usable,
		// so answer with it and leave the newer one alone.
		slog.Debug("paperless token upgrade skipped (token changed concurrently)",
			slog.String("user_id", userID.String()))
		return token
	}
	return resealed
}

// paperlessToken decrypts the stored API token for outbound calls. Legacy
// plaintext values pass through unchanged.
func paperlessToken(ctx context.Context, s models.UserSettings, tokenEncryptionKey string) (string, error) {
	if s.PaperlessToken == "" {
		return "", nil
	}
	return crypto.Decrypt(s.PaperlessToken, tokenEncryptionKey)
}

// paperlessConfigured reports whether the user has set both required fields.
func paperlessConfigured(s models.UserSettings) bool {
	return strings.TrimSpace(s.PaperlessURL) != "" && strings.TrimSpace(s.PaperlessToken) != ""
}

// paperlessBase returns the configured URL with any trailing slash trimmed so
// the app's own API paths can be appended.
func paperlessBase(s models.UserSettings) string {
	return strings.TrimRight(strings.TrimSpace(s.PaperlessURL), "/")
}

// paperlessOrigin returns the scheme://host origin for the configured URL.
func paperlessOrigin(s models.UserSettings) (string, error) {
	u, err := url.Parse(paperlessBase(s))
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("unsupported paperless URL scheme")
	}
	if u.Host == "" {
		return "", errors.New("paperless URL is missing a host")
	}
	return u.Scheme + "://" + u.Host, nil
}

// paperlessQueryInt parses an integer query parameter, clamping it into
// [min, max] and returning def when absent or malformed.
func paperlessQueryInt(c *gin.Context, key string, def, min, max int) int {
	v := strings.TrimSpace(c.Query(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// resolvePaperlessIDs maps the name-based filter values the UI sends back into
// the numeric IDs Paperless requires for its `*__id__*` query filters. Unknown
// names are ignored rather than failing the whole request.
func resolvePaperlessIDs(names map[int]string, values []string) []int {
	byName := make(map[string]int, len(names))
	for id, name := range names {
		byName[name] = id
	}
	ids := make([]int, 0, len(values))
	seen := map[int]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if id, ok := byName[v]; ok && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids
}

// joinPaperlessIDs formats a list of IDs for a Paperless `*__id__*` filter.
func joinPaperlessIDs(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}

// sortedNameList returns the names of a lookup map in sorted order so the UI
// can render stable filter dropdown options.
func sortedNameList(m map[int]string) []string {
	names := make([]string, 0, len(m))
	for _, name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// validatePaperlessURL is used at save time: the URL must be an absolute
// http(s) URL without embedded credentials.
func validatePaperlessURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("paperless URL is required")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return errors.New("invalid paperless URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("paperless URL must use http or https")
	}
	if u.Host == "" {
		return errors.New("paperless URL is missing a host")
	}
	if u.User != nil {
		return errors.New("paperless URL must not contain credentials")
	}
	return nil
}

// paperlessTransports caches one transport per (environment, log body limit)
// for the process. A transport owns the keep-alive connection pool, so building
// one per request — as this used to — forced a fresh TCP+TLS handshake for
// every lookup page and for each of the four round-trips a tagged document
// costs, and left a pool of up to 100 idle connections behind every call. The
// dial-time SSRF veto only depends on the environment and the token travels per
// request in the Authorization header, so a single pool serves every user's
// origin and nothing secret is stored here.
//
// MaxIdleConnsPerHost is raised above net/http's default of 2 so the concurrent
// tag writes (tagPaperlessConcurrency at a time, four round-trips each) keep
// their connections instead of re-dialing for the next batch.
var paperlessTransports sync.Map // key string -> http.RoundTripper

// paperlessTransport returns the shared transport for the given environment.
func paperlessTransport(appEnv string, logBodyLimit int) http.RoundTripper {
	key := appEnv + "|" + strconv.Itoa(logBodyLimit)
	if cached, ok := paperlessTransports.Load(key); ok {
		return cached.(http.RoundTripper)
	}
	wrapped := logger.LoggingRoundTripper(&http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           paperlessDialContext(appEnv),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   tagPaperlessConcurrency * 4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}, slog.Default(), logBodyLimit)
	actual, _ := paperlessTransports.LoadOrStore(key, wrapped)
	return actual.(http.RoundTripper)
}

// paperlessClient builds an HTTP client for the user's Paperless-ngx instance
// that refuses to follow redirects leaving the configured origin and dials only
// addresses vetted by isDisallowedPaperlessIP. Resolving and dialing inside the
// custom DialContext closes the DNS-rebinding (TOCTOU) gap that would exist if
// validation and connection happened as separate lookups.
//
// Only the redirect policy is per-client; the transport (and therefore the
// connection pool) is shared process-wide — see paperlessTransports.
func paperlessClient(s models.UserSettings, appEnv string, logBodyLimit int) (*http.Client, error) {
	origin, err := paperlessOrigin(s)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout:   paperlessClientTimeout,
		Transport: paperlessTransport(appEnv, logBodyLimit),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme+"://"+req.URL.Host != origin {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}, nil
}

// paperlessAllowedPort reports whether a Paperless URL may use the given port.
// Restricting production traffic to the standard web ports stops a user-supplied
// URL from reaching arbitrary internal services (databases, caches, admin
// endpoints) on non-web ports.
func paperlessAllowedPort(port string) bool {
	return port == "80" || port == "443"
}

// isDisallowedPaperlessIP reports whether ip is outside the public Internet.
// allowPrivate (true outside production, so a locally-hosted Paperless remains
// reachable) permits loopback and RFC1918/ULA addresses. Unspecified,
// multicast, link-local (including cloud metadata endpoints), and CGNAT
// addresses are never permitted.
func isDisallowedPaperlessIP(ip net.IP, allowPrivate bool) bool {
	if ip == nil {
		return true
	}
	// Normalize IPv4-mapped IPv6 (e.g. ::ffff:127.0.0.1) so the IPv4 range
	// checks below apply to it.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || isCGNAT(ip) {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() {
		return !allowPrivate
	}
	return false
}

// isCGNAT reports whether ip is in 100.64.0.0/10 (carrier-grade NAT), which is
// not covered by net.IP.IsPrivate.
func isCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

// paperlessDialContext returns a net.Dialer-style function that resolves the
// target hostname, rejects disallowed addresses, and connects to the validated
// IP directly.
func paperlessDialContext(appEnv string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	allowPrivate := appEnv != "production"
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if !allowPrivate && !paperlessAllowedPort(port) {
			return nil, fmt.Errorf("paperless port %q is not allowed", port)
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("could not resolve paperless host: %w", err)
		}
		var lastErr error
		for _, ipAddr := range ips {
			if isDisallowedPaperlessIP(ipAddr.IP, allowPrivate) {
				lastErr = fmt.Errorf("paperless host resolves to a disallowed address %s", ipAddr.IP)
				continue
			}
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ipAddr.IP.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		if lastErr == nil {
			lastErr = errors.New("paperless host has no usable address")
		}
		return nil, lastErr
	}
}

// validatePaperlessHost rejects obviously unsafe Paperless URLs before any
// request is made: production requires HTTPS and a standard web port, and the
// configured host must not resolve to a disallowed address. The custom
// DialContext re-checks the concrete IP at connect time, so this pre-check is a
// fast-fail courtesy rather than the security boundary.
func validatePaperlessHost(ctx context.Context, s models.UserSettings, appEnv string) error {
	u, err := url.Parse(paperlessBase(s))
	if err != nil {
		return err
	}
	allowPrivate := appEnv != "production"
	if !allowPrivate {
		if u.Scheme != "https" {
			return errors.New("paperless URL must use https in production")
		}
		port := u.Port()
		if port == "" {
			port = "443"
		}
		if !paperlessAllowedPort(port) {
			return errors.New("paperless URL must use port 80 or 443 in production")
		}
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil {
		return fmt.Errorf("could not resolve paperless host: %w", err)
	}
	for _, ip := range ips {
		if isDisallowedPaperlessIP(ip.IP, allowPrivate) {
			return errors.New("paperless host resolves to a non-public address")
		}
	}
	return nil
}

// readAllLimited reads at most max+1 bytes and rejects larger bodies.
func readAllLimited(r io.Reader, max int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, errors.New("response too large")
	}
	return body, nil
}

// rejectPaperlessRedirect blocks the 3xx responses the client refused to follow.
func rejectPaperlessRedirect(c *gin.Context, status int) bool {
	if status >= 300 && status < 400 {
		validation.RespondError(c, "Paperless returned a redirect to an untrusted origin", http.StatusBadGateway)
		return true
	}
	return false
}

// GetPaperlessSettings returns the current user's Paperless-ngx integration
// settings. The API token is never returned; HasToken reports whether one is
// configured so the Settings page can render a masked state.
func (srv *Server) GetPaperlessSettings(c *gin.Context) {
	userID := auth.GetUserID(c)
	settings, err := srv.paperlessConfig(c, userID)
	if err != nil {
		slog.Error("GetPaperlessSettings", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusOK, models.PaperlessSettingsResponse{
		PaperlessURL: settings.PaperlessURL,
		HasToken:     strings.TrimSpace(settings.PaperlessToken) != "",
		PaperlessTag: settings.PaperlessTag,
		PageSize:     settings.PageSize,
	})
}

// UpdatePaperlessSettings persists the user's settings against their user row.
// Only the fields present in the request are updated, so saving one setting
// (e.g. the transactions page size) never clobbers the others. The API token is
// stored encrypted at rest.
func (srv *Server) UpdatePaperlessSettings(c *gin.Context) {
	userID := auth.GetUserID(c)
	var req models.UpdateUserSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	if req.PaperlessURL != nil {
		if err := validatePaperlessURL(*req.PaperlessURL); err != nil {
			validation.RespondError(c, err.Error(), http.StatusBadRequest)
			return
		}
	}

	updates := []string{}
	args := []any{}
	argIdx := 1

	if req.PaperlessURL != nil {
		updates = append(updates, fmt.Sprintf("paperless_url = $%d", argIdx))
		args = append(args, strings.TrimSpace(*req.PaperlessURL))
		argIdx++
	}
	if req.PaperlessToken != nil {
		token := strings.TrimSpace(*req.PaperlessToken)
		updates = append(updates, fmt.Sprintf("paperless_token = $%d", argIdx))
		if token == "" {
			args = append(args, "")
		} else {
			enc, err := crypto.Encrypt(token, tokenEncryptionKey)
			if err != nil {
				slog.Error("UpdatePaperlessSettings (encrypt token)", slog.String("error", err.Error()))
				validation.RespondError(c, "internal server error", http.StatusInternalServerError)
				return
			}
			args = append(args, enc)
		}
		argIdx++
	}
	if req.PaperlessTag != nil {
		updates = append(updates, fmt.Sprintf("paperless_tag = $%d", argIdx))
		args = append(args, strings.TrimSpace(*req.PaperlessTag))
		argIdx++
	}
	if req.PageSize.Set() {
		updates = append(updates, fmt.Sprintf("page_size = $%d", argIdx))
		args = append(args, req.PageSize.Value())
		argIdx++
	}

	if len(updates) == 0 {
		validation.RespondError(c, "no settings provided", http.StatusBadRequest)
		return
	}

	args = append(args, userID)
	query := fmt.Sprintf("UPDATE users SET %s WHERE id = $%d", strings.Join(updates, ", "), argIdx)
	if _, err := srv.db.Exec(c, query, args...); err != nil {
		slog.Error("UpdatePaperlessSettings", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Echo back only the fields that were just updated, never the token itself.
	updated := models.PaperlessSettingsResponse{}
	if req.PaperlessURL != nil {
		updated.PaperlessURL = strings.TrimSpace(*req.PaperlessURL)
	}
	if req.PaperlessToken != nil {
		updated.HasToken = strings.TrimSpace(*req.PaperlessToken) != ""
	}
	if req.PaperlessTag != nil {
		updated.PaperlessTag = strings.TrimSpace(*req.PaperlessTag)
	}
	if req.PageSize.Set() {
		updated.PageSize = req.PageSize.Value()
	}
	c.JSON(http.StatusOK, updated)
}

// rawPaperlessDocument mirrors the Paperless-ngx document list item fields we
// care about. Paperless returns correspondent/document_type/tags as integer IDs,
// so we fetch the ID->name maps separately and resolve them (see
// paperlessNameMaps).
type rawPaperlessDocument struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	Correspondent int    `json:"correspondent"`
	DocumentType  int    `json:"document_type"`
	Created       string `json:"created"`
	Tags          []int  `json:"tags"`
}

// paperlessNameMaps holds ID->name lookups fetched from Paperless so document
// list items can be humanized.
type paperlessNameMaps struct {
	correspondents map[int]string
	documentTypes  map[int]string
	tags           map[int]string
}

// paperlessNameEntry is one row of a Paperless lookup table.
type paperlessNameEntry struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// nameMapPageSize is the page size requested for each Paperless lookup table.
// Paperless (DRF) clamps it server-side to maxPaperlessPageSize, which is why
// the pagination loop follows the response instead of this number.
const nameMapPageSize = 1000

// paperlessLookupTimeout bounds the whole lookup-table fetch (three paginated
// resources, up to maxNameMapPages pages each) against a user-supplied host.
// paperlessClientTimeout bounds each individual call, so without this a slow
// instance could hold the request and its connections for hours.
const paperlessLookupTimeout = 30 * time.Second

// maxNameMapPages bounds the pagination loop so a misbehaving instance that
// keeps advertising a next page can't make the lookup hang forever.
const maxNameMapPages = 100

// fetchNameMaps pulls the correspondent/document_type/tag lookup tables from
// Paperless and returns them as ID->name maps. The maps resolve the name-based
// filters and humanize the document list, so a failure is returned to the caller
// rather than degraded: a missing map would forward the user's filters upstream
// unresolved and look exactly like "no documents matched".
//
// Each table is paginated, and the loop is driven by the response's own `next`
// link instead of by the size it asked for: DRF clamps page_size server-side, so
// a page can come back far shorter than requested while more pages remain, and
// treating a short page as the last one silently truncated every lookup table
// (blank names, half-empty filter dropdowns). maxNameMapPages still bounds an
// instance that never clears `next`.
func fetchNameMaps(ctx context.Context, client *http.Client, base, token string) (paperlessNameMaps, error) {
	maps := paperlessNameMaps{
		correspondents: map[int]string{},
		documentTypes:  map[int]string{},
		tags:           map[int]string{},
	}
	fetch := func(resource string) ([]paperlessNameEntry, error) {
		var out []paperlessNameEntry
		path := fmt.Sprintf("/api/%s/?page_size=%d", resource, nameMapPageSize)
		for page := 1; page <= maxNameMapPages; page++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet,
				fmt.Sprintf("%s%s&page=%d", base, path, page), nil)
			if err != nil {
				return nil, fmt.Errorf("lookup %s page %d: %w", resource, page, err)
			}
			req.Header.Set("Authorization", "Token "+token)
			resp, err := client.Do(req)
			if err != nil {
				return nil, fmt.Errorf("lookup %s page %d: %w", resource, page, err)
			}
			body, readErr := readAllLimited(resp.Body, maxPaperlessResponse)
			resp.Body.Close()
			if readErr != nil {
				return nil, fmt.Errorf("lookup %s page %d: %w", resource, page, readErr)
			}
			if resp.StatusCode >= 400 {
				return nil, fmt.Errorf("lookup %s page %d: status %d", resource, page, resp.StatusCode)
			}
			var list struct {
				// Next is decoded raw: only its presence matters, its URL is
				// never followed (the next request is built from the configured
				// base), so an upstream cannot point this loop elsewhere.
				Next    json.RawMessage      `json:"next"`
				Results []paperlessNameEntry `json:"results"`
			}
			if err := json.Unmarshal(body, &list); err != nil {
				return nil, fmt.Errorf("lookup %s page %d: %w", resource, page, err)
			}
			out = append(out, list.Results...)
			// A page with no results makes no progress, and a missing/null
			// `next` (DRF's last page, or an upstream that does not paginate)
			// ends the table.
			if !hasNextPage(list.Next) || len(list.Results) == 0 {
				break
			}
		}
		return out, nil
	}

	correspondents, err := fetch("correspondents")
	if err != nil {
		return maps, err
	}
	documentTypes, err := fetch("document_types")
	if err != nil {
		return maps, err
	}
	tags, err := fetch("tags")
	if err != nil {
		return maps, err
	}
	for _, r := range correspondents {
		maps.correspondents[r.ID] = r.Name
	}
	for _, r := range documentTypes {
		maps.documentTypes[r.ID] = r.Name
	}
	for _, r := range tags {
		maps.tags[r.ID] = r.Name
	}
	return maps, nil
}

// hasNextPage reports whether a page advertises a following one. DRF sets `next`
// to the next page's URL and null on the last page; an upstream that returns
// everything in one response omits the key entirely, and both end the loop.
func hasNextPage(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

// ListPaperlessDocuments proxies the user's Paperless-ngx document list so the
// Paperless import UI can show available statements to pull. Filters (search
// text, correspondents, document types, tags) and pagination are forwarded to
// Paperless so it does the filtering and returns a single page — the backend
// never pulls the full document set and filters in memory. The name-based
// filters are resolved against Paperless's lookup tables, so a lookup that
// cannot be fetched fails the request (502/504) instead of being dropped.
// Requires the user to have configured both a URL and an API token.
func (srv *Server) ListPaperlessDocuments(c *gin.Context) {
	settings, err := srv.paperlessConfig(c, auth.GetUserID(c))
	if err != nil {
		slog.Error("ListPaperlessDocuments (config)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if !paperlessConfigured(settings) {
		validation.RespondError(c, "Paperless is not configured", http.StatusBadRequest)
		return
	}
	if err := validatePaperlessHost(c.Request.Context(), settings, appEnv); err != nil {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}
	client, err := paperlessClient(settings, appEnv, srv.logBodyLimit)
	if err != nil {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}
	token, err := paperlessToken(c, settings, tokenEncryptionKey)
	if err != nil {
		slog.Error("ListPaperlessDocuments (decrypt token)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	base := paperlessBase(settings)

	// The lookup tables are fetched once and reused to (a) resolve the
	// name-based filters the UI sends back into Paperless IDs, (b) humanize the
	// document list, and (c) populate the filter dropdown options in the
	// response. A failure is fatal rather than tolerated: continuing would
	// forward a filtered request upstream with the filters dropped (and answer
	// with empty dropdowns), so the client cannot tell "nothing matched" from
	// "your filters were ignored".
	lookupCtx, cancelLookup := context.WithTimeout(c.Request.Context(), paperlessLookupTimeout)
	defer cancelLookup()
	maps, lookupErr := fetchNameMaps(lookupCtx, client, base, token)
	if lookupErr != nil {
		slog.Error("ListPaperlessDocuments (name maps)", slog.String("error", lookupErr.Error()))
		if errors.Is(lookupErr, context.DeadlineExceeded) {
			validation.RespondError(c, "Paperless lookup timed out", http.StatusGatewayTimeout)
			return
		}
		validation.RespondError(c, "Paperless lookup unavailable", http.StatusBadGateway)
		return
	}

	page := paperlessQueryInt(c, "page", 1, 1, math.MaxInt)
	pageSize := paperlessQueryInt(c, "pageSize", defaultPaperlessPageSize, 1, maxPaperlessPageSize)

	// Translate the name-based UI filters into Paperless query filters so the
	// filtering and pagination happen server-side.
	qs := url.Values{}
	qs.Set("page", strconv.Itoa(page))
	qs.Set("page_size", strconv.Itoa(pageSize))
	qs.Set("ordering", "-created")
	// Only request the fields the list actually renders; Paperless returns the
	// full document serializer (including the OCR'd `content`) otherwise, which
	// is large and unused.
	qs.Set("fields", "id,title,correspondent,document_type,created,tags")
	// `title_search` is Paperless's Tantivy title-only simple search (regex
	// infix, so partial words match too); `text`/`query` touching the OCR
	// content is deliberately never sent — this box searches titles only. On
	// paperless-ngx 3.0.x multi-word `title_search` is broken ("credit card"
	// returns nothing even when titles contain both words), so multi-word
	// queries instead become an explicit fielded Tantivy query AND-ing each
	// word on the title field, where order and separators are irrelevant:
	//     query=title:"credit" AND title:"card"
	if q := strings.TrimSpace(c.Query("search")); q != "" {
		if words := strings.Fields(q); len(words) == 1 {
			qs.Set("title_search", q)
		} else {
			terms := make([]string, 0, len(words))
			for _, w := range words {
				terms = append(terms, `title:"`+strings.ReplaceAll(w, `"`, "")+`"`)
			}
			qs.Set("query", strings.Join(terms, " AND "))
		}
	}
	if ids := resolvePaperlessIDs(maps.correspondents, c.QueryArray("correspondentInc")); len(ids) > 0 {
		qs.Set("correspondent__id__in", joinPaperlessIDs(ids))
	}
	if ids := resolvePaperlessIDs(maps.correspondents, c.QueryArray("correspondentExc")); len(ids) > 0 {
		qs.Set("correspondent__id__none", joinPaperlessIDs(ids))
	}
	if ids := resolvePaperlessIDs(maps.documentTypes, c.QueryArray("documentTypeInc")); len(ids) > 0 {
		qs.Set("document_type__id__in", joinPaperlessIDs(ids))
	}
	if ids := resolvePaperlessIDs(maps.documentTypes, c.QueryArray("documentTypeExc")); len(ids) > 0 {
		qs.Set("document_type__id__none", joinPaperlessIDs(ids))
	}
	if ids := resolvePaperlessIDs(maps.tags, c.QueryArray("tagInc")); len(ids) > 0 {
		qs.Set("tags__id__any", joinPaperlessIDs(ids))
	}
	if ids := resolvePaperlessIDs(maps.tags, c.QueryArray("tagExc")); len(ids) > 0 {
		qs.Set("tags__id__none", joinPaperlessIDs(ids))
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, base+"/api/documents/?"+qs.Encode(), nil)
	if err != nil {
		slog.Error("ListPaperlessDocuments (build request)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Authorization", "Token "+token)

	resp, err := client.Do(req)
	if err != nil {
		slog.Error("ListPaperlessDocuments (calling paperless)", slog.String("error", err.Error()))
		validation.RespondError(c, "Paperless is unavailable", http.StatusBadGateway)
		return
	}
	body, readErr := readAllLimited(resp.Body, maxPaperlessResponse)
	resp.Body.Close()
	if readErr != nil {
		slog.Error("ListPaperlessDocuments (read response)", slog.String("error", readErr.Error()))
		validation.RespondError(c, "Paperless returned an unreadable response", http.StatusBadGateway)
		return
	}

	if rejectPaperlessRedirect(c, resp.StatusCode) {
		return
	}
	if resp.StatusCode == http.StatusUnauthorized {
		validation.RespondError(c, "Paperless rejected the API token", http.StatusBadGateway)
		return
	}
	if resp.StatusCode >= 500 {
		slog.Error("listing paperless documents", slog.Int("status", resp.StatusCode), slog.String("response", string(body)))
		validation.RespondError(c, "Paperless failed to list documents", http.StatusBadGateway)
		return
	}

	var raw struct {
		Results []rawPaperlessDocument `json:"results"`
		Count   int                    `json:"count"`
		Error   string                 `json:"error"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		slog.Error("ListPaperlessDocuments (unmarshal)", slog.String("error", err.Error()))
		validation.RespondError(c, "Paperless returned an invalid response", http.StatusBadGateway)
		return
	}
	if resp.StatusCode >= 400 || raw.Error != "" {
		msg := raw.Error
		if msg == "" {
			msg = "failed to list Paperless documents"
		}
		validation.RespondError(c, msg, resp.StatusCode)
		return
	}

	totalCount := raw.Count
	if totalCount == 0 && len(raw.Results) > 0 {
		totalCount = len(raw.Results)
	}
	totalPages := (totalCount + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}

	docs := make([]models.PaperlessDocument, 0, len(raw.Results))
	for _, d := range raw.Results {
		doc := models.PaperlessDocument{
			ID:      d.ID,
			Title:   d.Title,
			Created: d.Created,
		}
		doc.Correspondent = maps.correspondents[d.Correspondent]
		doc.DocumentType = maps.documentTypes[d.DocumentType]
		for _, t := range d.Tags {
			if name, ok := maps.tags[t]; ok {
				doc.Tags = append(doc.Tags, name)
			}
		}
		docs = append(docs, doc)
	}

	c.JSON(http.StatusOK, models.PaperlessDocumentsResponse{
		Documents:      docs,
		Page:           page,
		PageSize:       pageSize,
		TotalCount:     totalCount,
		TotalPages:     totalPages,
		Correspondents: sortedNameList(maps.correspondents),
		DocumentTypes:  sortedNameList(maps.documentTypes),
		Tags:           sortedNameList(maps.tags),
	})
}

// GetPaperlessDocumentFile proxies a document's original file from the user's
// Paperless-ngx instance so it can be viewed in the browser. The bytes are
// returned with the upstream content type; oversized files are rejected.
func (srv *Server) GetPaperlessDocumentFile(c *gin.Context) {
	settings, err := srv.paperlessConfig(c, auth.GetUserID(c))
	if err != nil {
		slog.Error("GetPaperlessDocumentFile (config)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if !paperlessConfigured(settings) {
		validation.RespondError(c, "Paperless is not configured", http.StatusBadRequest)
		return
	}
	if err := validatePaperlessHost(c.Request.Context(), settings, appEnv); err != nil {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}
	client, err := paperlessClient(settings, appEnv, srv.logBodyLimit)
	if err != nil {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}
	token, err := paperlessToken(c, settings, tokenEncryptionKey)
	if err != nil {
		slog.Error("GetPaperlessDocumentFile (decrypt token)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	docID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid document id", http.StatusBadRequest)
		return
	}

	dlURL := paperlessBase(settings) + "/api/documents/" + strconv.Itoa(docID) + "/download/"
	getReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, dlURL, nil)
	if err != nil {
		slog.Error("GetPaperlessDocumentFile (build request)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	getReq.Header.Set("Authorization", "Token "+token)

	resp, err := client.Do(getReq)
	if err != nil {
		slog.Error("GetPaperlessDocumentFile (calling paperless)", slog.String("error", err.Error()))
		validation.RespondError(c, "Paperless is unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if rejectPaperlessRedirect(c, resp.StatusCode) {
		return
	}
	if resp.StatusCode == http.StatusNotFound {
		validation.RespondError(c, "document not found", http.StatusNotFound)
		return
	}
	if resp.StatusCode == http.StatusUnauthorized {
		validation.RespondError(c, "Paperless rejected the API token", http.StatusBadGateway)
		return
	}
	if resp.StatusCode >= 400 {
		validation.RespondError(c, "Paperless failed to download the document", http.StatusBadGateway)
		return
	}
	if resp.ContentLength > maxPaperlessDocument {
		validation.RespondError(c, "Paperless document is too large", http.StatusRequestEntityTooLarge)
		return
	}

	data, err := readAllLimited(resp.Body, maxPaperlessDocument)
	if err != nil {
		slog.Error("GetPaperlessDocumentFile (read download)", slog.String("error", err.Error()))
		validation.RespondError(c, "Paperless document is too large", http.StatusRequestEntityTooLarge)
		return
	}

	// Never forward the upstream Content-Type: the Paperless URL is
	// user-supplied, so a compromised instance could serve text/html with
	// attacker script from the app origin. Pin the type, force a download-style
	// disposition, and disable MIME sniffing.
	c.Header("Content-Type", "application/pdf")
	c.Header("Content-Disposition", "inline; filename=\""+strconv.Itoa(docID)+".pdf\"")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "application/pdf", data)
}

// ImportPaperlessDocument downloads the original file for a document from the
// user's Paperless-ngx instance and feeds it through the existing statement
// parser, returning the same normalized result as the manual upload path so the
// frontend can preview and import it.
func (srv *Server) ImportPaperlessDocument(c *gin.Context) {
	settings, err := srv.paperlessConfig(c, auth.GetUserID(c))
	if err != nil {
		slog.Error("ImportPaperlessDocument (config)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if !paperlessConfigured(settings) {
		validation.RespondError(c, "Paperless is not configured", http.StatusBadRequest)
		return
	}
	if err := validatePaperlessHost(c.Request.Context(), settings, appEnv); err != nil {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}
	client, err := paperlessClient(settings, appEnv, srv.logBodyLimit)
	if err != nil {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}
	token, err := paperlessToken(c, settings, tokenEncryptionKey)
	if err != nil {
		slog.Error("ImportPaperlessDocument (decrypt token)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	var req models.PaperlessImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	dlURL := paperlessBase(settings) + "/api/documents/" + strconv.Itoa(req.DocumentID) + "/download/"
	getReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, dlURL, nil)
	if err != nil {
		slog.Error("ImportPaperlessDocument (build request)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	getReq.Header.Set("Authorization", "Token "+token)

	resp, err := client.Do(getReq)
	if err != nil {
		slog.Error("ImportPaperlessDocument (calling paperless)", slog.String("error", err.Error()))
		validation.RespondError(c, "Paperless is unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if rejectPaperlessRedirect(c, resp.StatusCode) {
		return
	}
	if resp.StatusCode == http.StatusNotFound {
		validation.RespondError(c, "document not found", http.StatusNotFound)
		return
	}
	if resp.StatusCode == http.StatusUnauthorized {
		validation.RespondError(c, "Paperless rejected the API token", http.StatusBadGateway)
		return
	}
	if resp.StatusCode >= 400 {
		validation.RespondError(c, "Paperless failed to download the document", http.StatusBadGateway)
		return
	}

	pdf, err := readAllLimited(resp.Body, maxPaperlessDocument)
	if err != nil {
		slog.Error("ImportPaperlessDocument (read download)", slog.String("error", err.Error()))
		validation.RespondError(c, "Paperless document is too large", http.StatusRequestEntityTooLarge)
		return
	}

	filename := "paperless-" + strconv.Itoa(req.DocumentID) + ".pdf"
	if req.Extractor == "" {
		req.Extractor = "sbi_cc"
	}

	result, status, errMsg := srv.forwardStatementToParser(c.Request.Context(), pdf, filename, req.Extractor, req.Password, req.DateFormat)
	if errMsg != "" {
		validation.RespondError(c, errMsg, status)
		return
	}

	c.JSON(http.StatusOK, result)
}

// tagPaperlessDocuments applies the user's configured FinTrak tag to the given
// Paperless documents. It is called from the import flow only after the
// transactions have been committed, so the tag marks documents that were
// actually imported. Tagging is best-effort (failures are logged, never
// surfaced) and runs with bounded concurrency: a single document can cost up
// to four upstream round-trips, so tagging serially inside the request would
// stall the import response behind the caller's Paperless instance.
func (srv *Server) tagPaperlessDocuments(ctx context.Context, userID uuid.UUID, documentIDs []int, tokenEncryptionKey, appEnv string) {
	if len(documentIDs) == 0 {
		return
	}
	settings, err := srv.paperlessConfig(ctx, userID)
	if err != nil {
		slog.Error("tagPaperlessDocuments (config)", slog.String("error", err.Error()))
		return
	}
	if !paperlessConfigured(settings) || strings.TrimSpace(settings.PaperlessTag) == "" {
		return
	}
	client, err := paperlessClient(settings, appEnv, srv.logBodyLimit)
	if err != nil {
		slog.Error("tagPaperlessDocuments (client)", slog.String("error", err.Error()))
		return
	}
	token, err := paperlessToken(ctx, settings, tokenEncryptionKey)
	if err != nil {
		slog.Error("tagPaperlessDocuments (decrypt token)", slog.String("error", err.Error()))
		return
	}

	base := paperlessBase(settings)
	tagCtx, cancel := context.WithTimeout(ctx, tagPaperlessTimeout)
	defer cancel()

	var wg sync.WaitGroup
	sem := make(chan struct{}, tagPaperlessConcurrency)
	for _, id := range documentIDs {
		sem <- struct{}{}
		wg.Add(1)
		go func(docID int) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := addPaperlessTag(tagCtx, client, base, token, docID, settings.PaperlessTag); err != nil {
				slog.Error("tagging paperless document", slog.Int("document_id", docID), slog.String("error", err.Error()))
			}
		}(id)
	}
	wg.Wait()
}

// addPaperlessTag ensures the named tag exists in the user's Paperless-ngx
// instance and appends it to the given document, preserving its existing tags.
// The tag is created on first use (with the FinTrak brand colour) and looked up
// by name on subsequent imports.
func addPaperlessTag(ctx context.Context, client *http.Client, base, token string, documentID int, tagName string) error {
	tagName = strings.TrimSpace(tagName)
	if tagName == "" {
		return nil
	}

	// 1. Look up an existing tag by name (Paperless supports filtering by name).
	tagID := 0
	listReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+"/api/tags/?name="+url.QueryEscape(tagName), nil)
	if err != nil {
		return err
	}
	listReq.Header.Set("Authorization", "Token "+token)
	listResp, err := client.Do(listReq)
	if err != nil {
		return err
	}
	if listResp.StatusCode == http.StatusOK {
		var list struct {
			Results []struct {
				ID int `json:"id"`
			} `json:"results"`
		}
		listBody, _ := readAllLimited(listResp.Body, maxPaperlessResponse)
		if json.Unmarshal(listBody, &list) == nil && len(list.Results) > 0 {
			tagID = list.Results[0].ID
		}
	}
	listResp.Body.Close()

	// 2. Create the tag if it does not exist yet.
	if tagID == 0 {
		createReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
			base+"/api/tags/",
			bytes.NewReader(fmt.Appendf(nil, `{"name":%q,"color":"#06b6d4"}`, tagName)))
		if err != nil {
			return err
		}
		createReq.Header.Set("Authorization", "Token "+token)
		createReq.Header.Set("Content-Type", "application/json")
		createResp, err := client.Do(createReq)
		if err != nil {
			return err
		}
		createBody, _ := readAllLimited(createResp.Body, maxPaperlessResponse)
		createResp.Body.Close()
		if createResp.StatusCode >= 400 {
			return fmt.Errorf("paperless tag creation failed with status %d", createResp.StatusCode)
		}
		var created struct {
			ID int `json:"id"`
		}
		if json.Unmarshal(createBody, &created) != nil || created.ID == 0 {
			return fmt.Errorf("paperless tag creation returned no id")
		}
		tagID = created.ID
	}

	// 3. Load the document's current tags so we append rather than replace.
	docURL := base + "/api/documents/" + strconv.Itoa(documentID) + "/"
	docReq, err := http.NewRequestWithContext(ctx, http.MethodGet, docURL, nil)
	if err != nil {
		return err
	}
	docReq.Header.Set("Authorization", "Token "+token)
	docResp, err := client.Do(docReq)
	if err != nil {
		return err
	}
	var doc struct {
		Tags []int `json:"tags"`
	}
	docBody, _ := readAllLimited(docResp.Body, maxPaperlessResponse)
	docResp.Body.Close()
	if docResp.StatusCode >= 400 {
		return fmt.Errorf("paperless document fetch failed with status %d", docResp.StatusCode)
	}
	if json.Unmarshal(docBody, &doc) != nil {
		return fmt.Errorf("paperless document fetch returned invalid data")
	}
	for _, t := range doc.Tags {
		if t == tagID {
			return nil // already tagged
		}
	}
	doc.Tags = append(doc.Tags, tagID)

	// 4. PATCH the document with the merged tag set.
	merged, err := json.Marshal(map[string]any{"tags": doc.Tags})
	if err != nil {
		return err
	}
	patchReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, docURL, bytes.NewReader(merged))
	if err != nil {
		return err
	}
	patchReq.Header.Set("Authorization", "Token "+token)
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err := client.Do(patchReq)
	if err != nil {
		return err
	}
	defer patchResp.Body.Close()
	if patchResp.StatusCode >= 400 {
		return fmt.Errorf("paperless tag update failed with status %d", patchResp.StatusCode)
	}
	return nil
}
