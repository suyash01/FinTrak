package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// paperlessClientTimeout bounds calls to the user's Paperless-ngx instance.
const paperlessClientTimeout = 60 * time.Second

// paperlessConfig loads a user's Paperless-ngx settings from the users row.
func paperlessConfig(c *gin.Context, userID uuid.UUID) (models.UserSettings, error) {
	var s models.UserSettings
	err := db.Pool.QueryRow(c,
		"SELECT paperless_url, paperless_token, paperless_tag, page_size FROM users WHERE id = $1",
		userID,
	).Scan(&s.PaperlessURL, &s.PaperlessToken, &s.PaperlessTag, &s.PageSize)
	return s, err
}

// paperlessConfigured reports whether the user has set both required fields.
func paperlessConfigured(s models.UserSettings) bool {
	return strings.TrimSpace(s.PaperlessURL) != "" && strings.TrimSpace(s.PaperlessToken) != ""
}

// GetPaperlessSettings returns the current user's Paperless-ngx integration
// settings (URL + API token) so the Settings page can render them.
func GetPaperlessSettings(c *gin.Context) {
	userID := auth.GetUserID(c)
	settings, err := paperlessConfig(c, userID)
	if err != nil {
		log.Printf("Error in GetPaperlessSettings: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusOK, settings)
}

// UpdatePaperlessSettings persists the user's settings against their user row.
// Only the fields present in the request are updated, so saving one setting
// (e.g. the transactions page size) never clobbers the others.
func UpdatePaperlessSettings(c *gin.Context) {
	userID := auth.GetUserID(c)
	var req models.UpdateUserSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
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
		updates = append(updates, fmt.Sprintf("paperless_token = $%d", argIdx))
		args = append(args, strings.TrimSpace(*req.PaperlessToken))
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
		log.Printf("Error in UpdatePaperlessSettings: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Echo back only the fields that were just updated.
	updated := models.UserSettings{}
	if req.PaperlessURL != nil {
		updated.PaperlessURL = strings.TrimSpace(*req.PaperlessURL)
	}
	if req.PaperlessToken != nil {
		updated.PaperlessToken = strings.TrimSpace(*req.PaperlessToken)
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
func fetchNameMaps(c *gin.Context, base, token string) paperlessNameMaps {
	maps := paperlessNameMaps{
		correspondents: map[int]string{},
		documentTypes:  map[int]string{},
		tags:           map[int]string{},
	}
	client := &http.Client{Timeout: paperlessClientTimeout}
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
			log.Printf("Error fetching paperless %s: %v\n", path, err)
			return nil
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
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
// Paperless import UI can show available statements to pull. Requires the user
// to have configured both a URL and an API token.
func ListPaperlessDocuments(c *gin.Context) {
	settings, err := paperlessConfig(c, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error in ListPaperlessDocuments (config): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if !paperlessConfigured(settings) {
		validation.RespondError(c, "Paperless is not configured", http.StatusBadRequest)
		return
	}

	base := strings.TrimRight(settings.PaperlessURL, "/")
	docURL := base + "/api/documents/?page_size=100&ordering=-created"

	client := &http.Client{Timeout: paperlessClientTimeout}

	// Gather all documents by following the pagination `next` links Paperless
	// returns (page_size is capped at 100 server-side).
	allRaw := make([]rawPaperlessDocument, 0)
	current := docURL
	for current != "" {
		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, current, nil)
		if err != nil {
			log.Printf("Error in ListPaperlessDocuments (build request): %v\n", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		req.Header.Set("Authorization", "Token "+settings.PaperlessToken)

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Error in ListPaperlessDocuments (calling paperless): %v\n", err)
			validation.RespondError(c, "Paperless is unavailable", http.StatusBadGateway)
			return
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			log.Printf("Error in ListPaperlessDocuments (read response): %v\n", readErr)
			validation.RespondError(c, "Paperless returned an unreadable response", http.StatusBadGateway)
			return
		}

		if resp.StatusCode == http.StatusUnauthorized {
			validation.RespondError(c, "Paperless rejected the API token", http.StatusBadGateway)
			return
		}
		if resp.StatusCode >= 500 {
			log.Printf("Error in ListPaperlessDocuments (status %d): %s\n", resp.StatusCode, string(body))
			validation.RespondError(c, "Paperless failed to list documents", http.StatusBadGateway)
			return
		}

		var raw struct {
			Results []rawPaperlessDocument `json:"results"`
			Next    string                 `json:"next"`
			Error   string                 `json:"error"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			log.Printf("Error in ListPaperlessDocuments (unmarshal): %v\n", err)
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

		allRaw = append(allRaw, raw.Results...)
		current = raw.Next
	}

	maps := fetchNameMaps(c, base, settings.PaperlessToken)

	docs := make([]models.PaperlessDocument, 0, len(allRaw))
	for _, d := range allRaw {
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

	c.JSON(http.StatusOK, gin.H{"documents": docs})
}

// GetPaperlessDocumentFile proxies a document's original file from the user's
// Paperless-ngx instance so it can be viewed in the browser. It streams the
// bytes back with the upstream content type rather than parsing them.
func GetPaperlessDocumentFile(c *gin.Context) {
	settings, err := paperlessConfig(c, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error in GetPaperlessDocumentFile (config): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if !paperlessConfigured(settings) {
		validation.RespondError(c, "Paperless is not configured", http.StatusBadRequest)
		return
	}

	docID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid document id", http.StatusBadRequest)
		return
	}

	base := strings.TrimRight(settings.PaperlessURL, "/")
	dlURL := base + "/api/documents/" + strconv.Itoa(docID) + "/download/"
	getReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, dlURL, nil)
	if err != nil {
		log.Printf("Error in GetPaperlessDocumentFile (build request): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	getReq.Header.Set("Authorization", "Token "+settings.PaperlessToken)

	client := &http.Client{Timeout: paperlessClientTimeout}
	resp, err := client.Do(getReq)
	if err != nil {
		log.Printf("Error in GetPaperlessDocumentFile (calling paperless): %v\n", err)
		validation.RespondError(c, "Paperless is unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

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

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Content-Type", contentType)
	c.DataFromReader(resp.StatusCode, resp.ContentLength, contentType, resp.Body, nil)
}

// ImportPaperlessDocument downloads the original file for a document from the
// user's Paperless-ngx instance and feeds it through the existing statement
// parser, returning the same normalized result as the manual upload path so the
// frontend can preview and import it.
func ImportPaperlessDocument(c *gin.Context) {
	settings, err := paperlessConfig(c, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error in ImportPaperlessDocument (config): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if !paperlessConfigured(settings) {
		validation.RespondError(c, "Paperless is not configured", http.StatusBadRequest)
		return
	}

	var req models.PaperlessImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	base := strings.TrimRight(settings.PaperlessURL, "/")
	dlURL := base + "/api/documents/" + strconv.Itoa(req.DocumentID) + "/download/"
	getReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, dlURL, nil)
	if err != nil {
		log.Printf("Error in ImportPaperlessDocument (build request): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	getReq.Header.Set("Authorization", "Token "+settings.PaperlessToken)

	client := &http.Client{Timeout: paperlessClientTimeout}
	resp, err := client.Do(getReq)
	if err != nil {
		log.Printf("Error in ImportPaperlessDocument (calling paperless): %v\n", err)
		validation.RespondError(c, "Paperless is unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

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

	pdf, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error in ImportPaperlessDocument (read download): %v\n", err)
		validation.RespondError(c, "Paperless returned an unreadable file", http.StatusBadGateway)
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
// transactions have been successfully committed to the database, so the tag
// marks documents that were actually imported rather than merely parsed.
// Tagging is best-effort: a failure is logged but never fails the import.
func tagPaperlessDocuments(c *gin.Context, userID uuid.UUID, documentIDs []int) {
	if len(documentIDs) == 0 {
		return
	}
	settings, err := paperlessConfig(c, userID)
	if err != nil {
		log.Printf("Error in tagPaperlessDocuments (config): %v\n", err)
		return
	}
	if !paperlessConfigured(settings) || strings.TrimSpace(settings.PaperlessTag) == "" {
		return
	}

	base := strings.TrimRight(settings.PaperlessURL, "/")
	for _, id := range documentIDs {
		if err := addPaperlessTag(c, base, settings.PaperlessToken, id, settings.PaperlessTag); err != nil {
			log.Printf("Error tagging paperless document %d: %v\n", id, err)
		}
	}
}

// addPaperlessTag ensures the named tag exists in the user's Paperless-ngx
// instance and appends it to the given document, preserving its existing tags.
// The tag is created on first use (with the FinTrak brand colour) and looked up
// by name on subsequent imports.
func addPaperlessTag(c *gin.Context, base, token string, documentID int, tagName string) error {
	client := &http.Client{Timeout: paperlessClientTimeout}
	tagName = strings.TrimSpace(tagName)
	if tagName == "" {
		return nil
	}

	// 1. Look up an existing tag by name (Paperless supports filtering by name).
	tagID := 0
	listReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet,
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
		listBody, _ := io.ReadAll(listResp.Body)
		if json.Unmarshal(listBody, &list) == nil && len(list.Results) > 0 {
			tagID = list.Results[0].ID
		}
	}
	listResp.Body.Close()

	// 2. Create the tag if it does not exist yet.
	if tagID == 0 {
		createReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost,
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
		createBody, _ := io.ReadAll(createResp.Body)
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
	docReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, docURL, nil)
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
	docBody, _ := io.ReadAll(docResp.Body)
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
	patchReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPatch, docURL, bytes.NewReader(merged))
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
