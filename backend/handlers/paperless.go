package handlers

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
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
		"SELECT paperless_url, paperless_token FROM users WHERE id = $1",
		userID,
	).Scan(&s.PaperlessURL, &s.PaperlessToken)
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	c.JSON(http.StatusOK, settings)
}

// UpdatePaperlessSettings persists the user's Paperless-ngx settings against
// their user row.
func UpdatePaperlessSettings(c *gin.Context) {
	userID := auth.GetUserID(c)
	var req models.UpdateUserSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	req.PaperlessURL = strings.TrimSpace(req.PaperlessURL)
	req.PaperlessToken = strings.TrimSpace(req.PaperlessToken)

	_, err := db.Pool.Exec(c,
		"UPDATE users SET paperless_url = $1, paperless_token = $2 WHERE id = $3",
		req.PaperlessURL, req.PaperlessToken, userID,
	)
	if err != nil {
		log.Printf("Error in UpdatePaperlessSettings: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, models.UserSettings{PaperlessURL: req.PaperlessURL, PaperlessToken: req.PaperlessToken})
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
	Added         string `json:"added"`
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	if !paperlessConfigured(settings) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Paperless is not configured"})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}
		req.Header.Set("Authorization", "Token "+settings.PaperlessToken)

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Error in ListPaperlessDocuments (calling paperless): %v\n", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "Paperless is unavailable"})
			return
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			log.Printf("Error in ListPaperlessDocuments (read response): %v\n", readErr)
			c.JSON(http.StatusBadGateway, gin.H{"error": "Paperless returned an unreadable response"})
			return
		}

		if resp.StatusCode == http.StatusUnauthorized {
			c.JSON(http.StatusBadGateway, gin.H{"error": "Paperless rejected the API token"})
			return
		}
		if resp.StatusCode >= 500 {
			log.Printf("Error in ListPaperlessDocuments (status %d): %s\n", resp.StatusCode, string(body))
			c.JSON(http.StatusBadGateway, gin.H{"error": "Paperless failed to list documents"})
			return
		}

		var raw struct {
			Results []rawPaperlessDocument `json:"results"`
			Next    string                 `json:"next"`
			Error   string                 `json:"error"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			log.Printf("Error in ListPaperlessDocuments (unmarshal): %v\n", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "Paperless returned an invalid response"})
			return
		}
		if resp.StatusCode >= 400 || raw.Error != "" {
			msg := raw.Error
			if msg == "" {
				msg = "failed to list Paperless documents"
			}
			c.JSON(resp.StatusCode, gin.H{"error": msg})
			return
		}

		allRaw = append(allRaw, raw.Results...)
		current = raw.Next
	}

	maps := fetchNameMaps(c, base, settings.PaperlessToken)

	docs := make([]models.PaperlessDocument, 0, len(allRaw))
	for _, d := range allRaw {
		doc := models.PaperlessDocument{
			ID:    d.ID,
			Title: d.Title,
			Added: d.Added,
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	if !paperlessConfigured(settings) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Paperless is not configured"})
		return
	}

	docID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid document id"})
		return
	}

	base := strings.TrimRight(settings.PaperlessURL, "/")
	dlURL := base + "/api/documents/" + strconv.Itoa(docID) + "/download/"
	getReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, dlURL, nil)
	if err != nil {
		log.Printf("Error in GetPaperlessDocumentFile (build request): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	getReq.Header.Set("Authorization", "Token "+settings.PaperlessToken)

	client := &http.Client{Timeout: paperlessClientTimeout}
	resp, err := client.Do(getReq)
	if err != nil {
		log.Printf("Error in GetPaperlessDocumentFile (calling paperless): %v\n", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Paperless is unavailable"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "document not found"})
		return
	}
	if resp.StatusCode == http.StatusUnauthorized {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Paperless rejected the API token"})
		return
	}
	if resp.StatusCode >= 400 {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Paperless failed to download the document"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	if !paperlessConfigured(settings) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Paperless is not configured"})
		return
	}

	var req models.PaperlessImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	base := strings.TrimRight(settings.PaperlessURL, "/")
	dlURL := base + "/api/documents/" + strconv.Itoa(req.DocumentID) + "/download/"
	getReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, dlURL, nil)
	if err != nil {
		log.Printf("Error in ImportPaperlessDocument (build request): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	getReq.Header.Set("Authorization", "Token "+settings.PaperlessToken)

	client := &http.Client{Timeout: paperlessClientTimeout}
	resp, err := client.Do(getReq)
	if err != nil {
		log.Printf("Error in ImportPaperlessDocument (calling paperless): %v\n", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Paperless is unavailable"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "document not found"})
		return
	}
	if resp.StatusCode == http.StatusUnauthorized {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Paperless rejected the API token"})
		return
	}
	if resp.StatusCode >= 400 {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Paperless failed to download the document"})
		return
	}

	pdf, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error in ImportPaperlessDocument (read download): %v\n", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Paperless returned an unreadable file"})
		return
	}

	filename := "paperless-" + strconv.Itoa(req.DocumentID) + ".pdf"
	if req.Extractor == "" {
		req.Extractor = "sbi_cc"
	}

	result, status, errMsg, _ := forwardStatementToParser(c.Request.Context(), pdf, filename, req.Extractor, req.Password, req.DateFormat)
	if errMsg != "" {
		c.JSON(status, gin.H{"error": errMsg})
		return
	}
	c.JSON(http.StatusOK, result)
}
