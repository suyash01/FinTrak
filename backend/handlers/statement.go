package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
)

// statementParserURL is the base URL of the standalone statement-parser service.
// It is wired up from configuration in setupRouter so the backend stays decoupled
// from the parser; the two only talk over HTTP.
var statementParserURL = "http://localhost:5000"

// SetStatementParserURL updates the base URL used to reach the standalone
// statement-parser service. Called once at startup from configuration.
func SetStatementParserURL(url string) {
	if url != "" {
		statementParserURL = url
	}
}

// maxStatementUpload caps the size of a statement PDF we are willing to forward.
const maxStatementUpload = 20 * 1024 * 1024 // 20 MB

// parseStatementResult is the normalized payload returned to the frontend after
// the backend has forwarded a statement PDF to the parser service.
type parseStatementResult struct {
	Transactions []models.ImportTransaction `json:"transactions"`
	Summary      map[string]string          `json:"summary"`
	PageCount    int                        `json:"pageCount"`
	TxnCount     int                        `json:"transactionCount"`
}

// rawParserTransaction is the shape returned by the statement-parser REST API.
type rawParserTransaction struct {
	Date        string  `json:"date"`
	Description string  `json:"description"`
	Amount      float64 `json:"amount"`
	Type        string  `json:"type"` // "Credit" | "Debit"
}

type rawParserResponse struct {
	Transactions     []rawParserTransaction `json:"transactions"`
	Summary          map[string]string      `json:"summary"`
	PageCount        int                    `json:"page_count"`
	TransactionCount int                    `json:"transaction_count"`
	Error            string                 `json:"error"`
	PasswordRequired bool                   `json:"password_required"`
}

// ParseStatement accepts an uploaded bank/credit-card statement PDF (plus an
// optional password), forwards it to the statement-parser service over HTTP, and
// returns the extracted transactions normalized to the app's import format so the
// frontend can preview and import them directly.
func ParseStatement(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no file provided. Attach it as 'file'."})
		return
	}

	if !strings.HasSuffix(strings.ToLower(file.Filename), ".pdf") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only PDF files are supported."})
		return
	}

	if file.Size > maxStatementUpload {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file too large. Max upload size is 20 MB."})
		return
	}

	password := c.PostForm("password")
	extractor := c.PostForm("extractor")
	if extractor == "" {
		extractor = "sbi_cc"
	}

	// Forward the uploaded PDF to the parser service as a multipart request.
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", file.Filename)
	if err != nil {
		log.Printf("Error in ParseStatement (create form file): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	src, err := file.Open()
	if err != nil {
		log.Printf("Error in ParseStatement (open upload): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	if _, err := io.Copy(part, src); err != nil {
		src.Close()
		log.Printf("Error in ParseStatement (copy upload): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	src.Close()

	if password != "" {
		_ = writer.WriteField("password", password)
	}
	writer.Close()

	// The extractor selector is passed as a query parameter, which is how the
	// parser service reads it (see app.py: request.args.get("extractor")).
	url := strings.TrimRight(statementParserURL, "/") + "/api/extract?format=json&extractor=" + url.QueryEscape(extractor)
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, url, body)
	if err != nil {
		log.Printf("Error in ParseStatement (build request): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error in ParseStatement (calling parser): %v\n", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "statement parser is unavailable"})
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error in ParseStatement (read parser response): %v\n", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "statement parser returned an unreadable response"})
		return
	}

	if resp.StatusCode >= 500 {
		log.Printf("Error in ParseStatement (parser error status %d): %s\n", resp.StatusCode, string(respBody))
		c.JSON(http.StatusBadGateway, gin.H{"error": "statement parser failed to process the file"})
		return
	}

	var raw rawParserResponse
	if err := json.Unmarshal(respBody, &raw); err != nil {
		log.Printf("Error in ParseStatement (unmarshal parser response): %v\n", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "statement parser returned an invalid response"})
		return
	}

	if raw.PasswordRequired || resp.StatusCode == http.StatusUnauthorized {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "password required or incorrect", "passwordRequired": true})
		return
	}
	if resp.StatusCode >= 400 || raw.Error != "" {
		msg := raw.Error
		if msg == "" {
			msg = "failed to parse statement"
		}
		c.JSON(resp.StatusCode, gin.H{"error": msg})
		return
	}

	result := parseStatementResult{
		Transactions: make([]models.ImportTransaction, 0, len(raw.Transactions)),
		Summary:      raw.Summary,
		PageCount:    raw.PageCount,
		TxnCount:     raw.TransactionCount,
	}
	for _, t := range raw.Transactions {
		result.Transactions = append(result.Transactions, models.ImportTransaction{
			Date:        normalizeParserDate(t.Date),
			Description: t.Description,
			Amount:      t.Amount,
			Type:        normalizeParserType(t.Type),
		})
	}

	c.JSON(http.StatusOK, result)
}

// normalizeParserDate converts the parser's "DD Mon YY" date format to the app's
// "YYYY-MM-DD" format used by the import pipeline. Dates it can't parse are
// returned unchanged so the frontend can surface them.
func normalizeParserDate(d string) string {
	s := strings.TrimSpace(d)
	if s == "" {
		return s
	}
	t, err := time.Parse("02 Jan 06", s)
	if err == nil {
		return t.Format("2006-01-02")
	}
	t, err = time.Parse("02 Jan 2006", s)
	if err == nil {
		return t.Format("2006-01-02")
	}
	return s
}

// normalizeParserType maps the parser's "Credit"/"Debit" values to the app's
// lowercase "credit"/"debit" convention.
func normalizeParserType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "credit":
		return "credit"
	case "debit":
		return "debit"
	default:
		return "debit"
	}
}

// ListStatementExtractors proxies the parser service's extractor registry so the
// frontend can render a dropdown of available statement parsers.
func ListStatementExtractors(c *gin.Context) {
	url := strings.TrimRight(statementParserURL, "/") + "/api/extractors"

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, url, nil)
	if err != nil {
		log.Printf("Error in ListStatementExtractors (build request): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error in ListStatementExtractors (calling parser): %v\n", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "statement parser is unavailable"})
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error in ListStatementExtractors (read parser response): %v\n", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "statement parser returned an unreadable response"})
		return
	}

	if resp.StatusCode >= 500 {
		log.Printf("Error in ListStatementExtractors (parser error status %d): %s\n", resp.StatusCode, string(respBody))
		c.JSON(http.StatusBadGateway, gin.H{"error": "statement parser failed to list extractors"})
		return
	}

	var raw struct {
		Extractors []struct {
			Name        string `json:"name"`
			DisplayName string `json:"display_name"`
		} `json:"extractors"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &raw); err != nil {
		log.Printf("Error in ListStatementExtractors (unmarshal parser response): %v\n", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "statement parser returned an invalid response"})
		return
	}
	if resp.StatusCode >= 400 || raw.Error != "" {
		msg := raw.Error
		if msg == "" {
			msg = "failed to list extractors"
		}
		c.JSON(resp.StatusCode, gin.H{"error": msg})
		return
	}

	c.JSON(http.StatusOK, gin.H{"extractors": raw.Extractors})
}