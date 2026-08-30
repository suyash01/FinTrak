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
	"github.com/fintrak/backend/db"
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
// paperlessToken so read-only paths never need it.
func paperlessConfig(ctx context.Context, userID uuid.UUID) (models.UserSettings, error) {
	var s models.UserSettings
	err := db.Pool.QueryRow(ctx,
		"SELECT paperless_url, paperless_token, paperless_tag, page_size FROM users WHERE id = $1",
		userID,
	).Scan(&s.PaperlessURL, &s.PaperlessToken, &s.PaperlessTag, &s.PageSize)
	return s, err
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

// paperlessClient builds an HTTP client for the user's Paperless-ngx instance
// that refuses to follow redirects leaving the configured origin.
func paperlessClient(s models.UserSettings) (*http.Client, error) {
	origin, err := paperlessOrigin(s)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout:   paperlessClientTimeout,
		Transport: logger.LoggingRoundTripper(nil, slog.Default()),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme+"://"+req.URL.Host != origin {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}, nil
}

// validatePaperlessHost enforces SSRF boundaries in production: HTTPS is
// required and the configured host must not resolve to a loopback, private,
// link-local, multicast, or unspecified address.
func validatePaperlessHost(ctx context.Context, s models.UserSettings, appEnv string) error {
	if appEnv != "production" {
		return nil
	}
	u, err := url.Parse(paperlessBase(s))
	if err != nil {
		return err
	}
	if u.Scheme != "https" {
		return errors.New("paperless URL must use https in production")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil {
		return fmt.Errorf("could not resolve paperless host: %w", err)
	}
	for _, ip := range ips {
		if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() ||
			ip.IP.IsLinkLocalMulticast() || ip.IP.IsMulticast() || ip.IP.IsUnspecified() {
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
func GetPaperlessSettings(c *gin.Context) {
	userID := auth.GetUserID(c)
	settings, err := paperlessConfig(c, userID)
	if err != nil {
		slog.Error("GetPaperlessSettings", "error", err)
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
func UpdatePaperlessSettings(c *gin.Context) {
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
			enc, err := crypto.Encrypt(token, c.GetString("tokenEncryptionKey"))
			if err != nil {
				slog.Error("UpdatePaperlessSettings (encrypt token)", "error", err)
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
	if _, err := db.Pool.Exec(c, query, args...); err != nil {
		slog.Error("UpdatePaperlessSettings", "error", err)
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

// fetchNameMaps pulls the correspondent/document_type/tag lookup tables from
// Paperless and returns them as ID->name maps. Individual failures are logged
// but not fatal — the document list still renders, just without names.
func fetchNameMaps(c *gin.Context, client *http.Client, base, token string) paperlessNameMaps {
	maps := paperlessNameMaps{
		correspondents: map[int]string{},
		documentTypes:  map[int]string{},
		tags:           map[int]string{},
	}
	fetch := func(path string) []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} {
		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, base+path, nil)
		if err != nil {
			return nil
		}
		req.Header.Set("Authorization", "Token "+token)
		resp, err := client.Do(req)
		if err != nil {
			slog.Error("fetching paperless resource", "path", path, "error", err)
			return nil
		}
		defer resp.Body.Close()
		body, err := readAllLimited(resp.Body, maxPaperlessResponse)
		if err != nil {
			return nil
		}
		var list struct {
			Results []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"results"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			return nil
		}
		return list.Results
	}

	for _, r := range fetch("/api/correspondents/?page_size=1000") {
		maps.correspondents[r.ID] = r.Name
	}
	for _, r := range fetch("/api/document_types/?page_size=1000") {
		maps.documentTypes[r.ID] = r.Name
	}
	for _, r := range fetch("/api/tags/?page_size=1000") {
		maps.tags[r.ID] = r.Name
	}
	return maps
}

// ListPaperlessDocuments proxies the user's Paperless-ngx document list so the
// Paperless import UI can show available statements to pull. Filters (search
// text, correspondents, document types, tags) and pagination are forwarded to
// Paperless so it does the filtering and returns a single page — the backend
// never pulls the full document set and filters in memory. Requires the user
// to have configured both a URL and an API token.
func ListPaperlessDocuments(c *gin.Context) {
	settings, err := paperlessConfig(c, auth.GetUserID(c))
	if err != nil {
		slog.Error("ListPaperlessDocuments (config)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if !paperlessConfigured(settings) {
		validation.RespondError(c, "Paperless is not configured", http.StatusBadRequest)
		return
	}
	if err := validatePaperlessHost(c.Request.Context(), settings, c.GetString("appEnv")); err != nil {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}
	client, err := paperlessClient(settings)
	if err != nil {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}
	token, err := paperlessToken(c, settings, c.GetString("tokenEncryptionKey"))
	if err != nil {
		slog.Error("ListPaperlessDocuments (decrypt token)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	base := paperlessBase(settings)

	// The lookup tables are fetched once and reused to (a) resolve the
	// name-based filters the UI sends back into Paperless IDs, (b) humanize the
	// document list, and (c) populate the filter dropdown options in the
	// response.
	maps := fetchNameMaps(c, client, base, token)

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
	// `title_search` is Paperless's current (Tantivy) title-only simple search;
	// `title__icontains` is the legacy ORM filter that older instances use and
	// that newer ones still honor. Sending both keeps title-only filtering
	// working across Paperless versions (unknown params are ignored). `text`
	// would match the OCR'd content too, so it is deliberately not used.
	if q := strings.TrimSpace(c.Query("search")); q != "" {
		qs.Set("title_search", q)
		qs.Set("title__icontains", q)
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
		slog.Error("ListPaperlessDocuments (build request)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Authorization", "Token "+token)

	resp, err := client.Do(req)
	if err != nil {
		slog.Error("ListPaperlessDocuments (calling paperless)", "error", err)
		validation.RespondError(c, "Paperless is unavailable", http.StatusBadGateway)
		return
	}
	body, readErr := readAllLimited(resp.Body, maxPaperlessResponse)
	resp.Body.Close()
	if readErr != nil {
		slog.Error("ListPaperlessDocuments (read response)", "error", readErr)
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
		slog.Error("listing paperless documents", "status", resp.StatusCode, "response", string(body))
		validation.RespondError(c, "Paperless failed to list documents", http.StatusBadGateway)
		return
	}

	var raw struct {
		Results []rawPaperlessDocument `json:"results"`
		Count   int                    `json:"count"`
		Error   string                 `json:"error"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		slog.Error("ListPaperlessDocuments (unmarshal)", "error", err)
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
func GetPaperlessDocumentFile(c *gin.Context) {
	settings, err := paperlessConfig(c, auth.GetUserID(c))
	if err != nil {
		slog.Error("GetPaperlessDocumentFile (config)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if !paperlessConfigured(settings) {
		validation.RespondError(c, "Paperless is not configured", http.StatusBadRequest)
		return
	}
	if err := validatePaperlessHost(c.Request.Context(), settings, c.GetString("appEnv")); err != nil {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}
	client, err := paperlessClient(settings)
	if err != nil {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}
	token, err := paperlessToken(c, settings, c.GetString("tokenEncryptionKey"))
	if err != nil {
		slog.Error("GetPaperlessDocumentFile (decrypt token)", "error", err)
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
		slog.Error("GetPaperlessDocumentFile (build request)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	getReq.Header.Set("Authorization", "Token "+token)

	resp, err := client.Do(getReq)
	if err != nil {
		slog.Error("GetPaperlessDocumentFile (calling paperless)", "error", err)
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
		slog.Error("GetPaperlessDocumentFile (read download)", "error", err)
		validation.RespondError(c, "Paperless document is too large", http.StatusRequestEntityTooLarge)
		return
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Data(http.StatusOK, contentType, data)
}

// ImportPaperlessDocument downloads the original file for a document from the
// user's Paperless-ngx instance and feeds it through the existing statement
// parser, returning the same normalized result as the manual upload path so the
// frontend can preview and import it.
func ImportPaperlessDocument(c *gin.Context) {
	settings, err := paperlessConfig(c, auth.GetUserID(c))
	if err != nil {
		slog.Error("ImportPaperlessDocument (config)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if !paperlessConfigured(settings) {
		validation.RespondError(c, "Paperless is not configured", http.StatusBadRequest)
		return
	}
	if err := validatePaperlessHost(c.Request.Context(), settings, c.GetString("appEnv")); err != nil {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}
	client, err := paperlessClient(settings)
	if err != nil {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}
	token, err := paperlessToken(c, settings, c.GetString("tokenEncryptionKey"))
	if err != nil {
		slog.Error("ImportPaperlessDocument (decrypt token)", "error", err)
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
		slog.Error("ImportPaperlessDocument (build request)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	getReq.Header.Set("Authorization", "Token "+token)

	resp, err := client.Do(getReq)
	if err != nil {
		slog.Error("ImportPaperlessDocument (calling paperless)", "error", err)
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
		slog.Error("ImportPaperlessDocument (read download)", "error", err)
		validation.RespondError(c, "Paperless document is too large", http.StatusRequestEntityTooLarge)
		return
	}

	filename := "paperless-" + strconv.Itoa(req.DocumentID) + ".pdf"
	if req.Extractor == "" {
		req.Extractor = "sbi_cc"
	}

	result, status, errMsg, _ := forwardStatementToParser(c.Request.Context(), pdf, filename, req.Extractor, req.Password, req.DateFormat)
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
func tagPaperlessDocuments(ctx context.Context, userID uuid.UUID, documentIDs []int, tokenEncryptionKey string) {
	if len(documentIDs) == 0 {
		return
	}
	settings, err := paperlessConfig(ctx, userID)
	if err != nil {
		slog.Error("tagPaperlessDocuments (config)", "error", err)
		return
	}
	if !paperlessConfigured(settings) || strings.TrimSpace(settings.PaperlessTag) == "" {
		return
	}
	client, err := paperlessClient(settings)
	if err != nil {
		slog.Error("tagPaperlessDocuments (client)", "error", err)
		return
	}
	token, err := paperlessToken(ctx, settings, tokenEncryptionKey)
	if err != nil {
		slog.Error("tagPaperlessDocuments (decrypt token)", "error", err)
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
				slog.Error("tagging paperless document", "document_id", docID, "error", err)
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
