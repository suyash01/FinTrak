package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fintrak/backend/internal/logger"
	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
)

// maxStatementUpload caps the size of a statement PDF we are willing to forward.
const maxStatementUpload = 20 * 1024 * 1024 // 20 MB

// maxParserResponse caps the statement-parser response body.
const maxParserResponse = 20 * 1024 * 1024 // 20 MB

// parseStatementResult is the normalized payload returned to the frontend after
// the backend has forwarded a statement PDF to the parser service.
type parseStatementResult struct {
	Transactions []models.ImportTransaction `json:"transactions"`
	Summary      map[string]string          `json:"summary"`
	PageCount    int                        `json:"pageCount"`
	TxnCount     int                        `json:"transactionCount"`
	// ValidationErrors carries the parser's per-page consistency warnings
	// (e.g. a rebuilt subtotal that didn't match the printed one) so the
	// preview can flag a drifted parse instead of presenting it as clean.
	ValidationErrors []string `json:"validationErrors"`
}

// rawParserTransaction is the shape returned by the statement-parser REST API.
type rawParserTransaction struct {
	Date        string  `json:"date"`
	Description string  `json:"description"`
	Amount      float64 `json:"amount"`
	Type        string  `json:"type"` // "Credit" | "Debit"
}

// rawParserResponse is the full envelope returned by the statement-parser
// service, including error and password-required signals.
type rawParserResponse struct {
	Transactions     []rawParserTransaction `json:"transactions"`
	Summary          map[string]string      `json:"summary"`
	PageCount        int                    `json:"page_count"`
	TransactionCount int                    `json:"transaction_count"`
	Error            string                 `json:"error"`
	PasswordRequired bool                   `json:"password_required"`
	// ValidationErrors is the parser's own JSON key (snake_case, unlike the
	// frontend-facing camelCase) and is absent when the extractor had nothing
	// to report.
	ValidationErrors []string `json:"validation_errors"`
}

// ParseStatement accepts an uploaded bank/credit-card statement PDF (plus an
// optional password), forwards it to the statement-parser service over HTTP, and
// returns the extracted transactions normalized to the app's import format so the
// frontend can preview and import them directly.
func (srv *Server) ParseStatement(c *gin.Context) {
	// Cap the request body before gin parses the multipart form: c.FormFile would
	// otherwise read (and spool) an unbounded upload before the size check runs.
	// The extra megabyte covers multipart boundaries and the form's text fields.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxStatementUpload+(1<<20))

	file, err := c.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			validation.RespondError(c, "file too large. Max upload size is 20 MB.", http.StatusRequestEntityTooLarge)
			return
		}
		validation.RespondError(c, "no file provided. Attach it as 'file'.", http.StatusBadRequest)
		return
	}

	if !strings.HasSuffix(strings.ToLower(file.Filename), ".pdf") {
		validation.RespondError(c, "only PDF files are supported.", http.StatusBadRequest)
		return
	}

	if file.Size > maxStatementUpload {
		validation.RespondError(c, "file too large. Max upload size is 20 MB.", http.StatusRequestEntityTooLarge)
		return
	}

	src, err := file.Open()
	if err != nil {
		slog.Error("ParseStatement (open upload)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer src.Close()

	pdf, err := readAllLimited(src, maxStatementUpload)
	if err != nil {
		validation.RespondError(c, "file too large. Max upload size is 20 MB.", http.StatusRequestEntityTooLarge)
		return
	}

	password := c.PostForm("password")
	extractor := c.PostForm("extractor")
	if extractor == "" {
		extractor = "sbi_cc"
	}
	dateFormat := c.PostForm("date_format")

	result, status, errMsg, _ := srv.forwardStatementToParser(c.Request.Context(), pdf, file.Filename, extractor, password, dateFormat)
	if errMsg != "" {
		validation.RespondError(c, errMsg, status)
		return
	}
	c.JSON(http.StatusOK, result)
}

// forwardStatementToParser sends the raw PDF bytes to the standalone
// statement-parser service and returns the normalized parse result. On failure
// it returns the HTTP status and message the caller should surface; on success
// status is 200 and errMsg is empty. Shared by both the manual upload path
// (ParseStatement) and the Paperless import path (ImportPaperlessDocument).
func (srv *Server) forwardStatementToParser(ctx context.Context, pdf []byte, filename, extractor, password, dateFormat string) (*parseStatementResult, int, string, bool) {
	// Bound concurrent parser work: fail fast with 429 when saturated rather
	// than queueing unbounded expensive parses. Callers may retry shortly.
	if srv.parseSem != nil {
		select {
		case srv.parseSem <- struct{}{}:
			defer func() { <-srv.parseSem }()
		case <-ctx.Done():
			return nil, http.StatusRequestTimeout, "request cancelled", false
		default:
			return nil, http.StatusTooManyRequests, "statement parser is busy; try again shortly", false
		}
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		slog.Error("forwarding statement (create form file)", slog.String("error", err.Error()))
		return nil, http.StatusInternalServerError, "internal server error", false
	}
	if _, err := part.Write(pdf); err != nil {
		slog.Error("forwarding statement (write pdf)", slog.String("error", err.Error()))
		return nil, http.StatusInternalServerError, "internal server error", false
	}

	if password != "" {
		_ = writer.WriteField("password", password)
	}
	if dateFormat != "" {
		_ = writer.WriteField("date_format", dateFormat)
	}
	writer.Close()

	// The extractor selector is passed as a query parameter, which is how the
	// parser service reads it (see app.py: request.args.get("extractor")).
	parserURL := strings.TrimRight(srv.parserURL, "/") + "/api/extract?format=json&extractor=" + url.QueryEscape(extractor)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parserURL, body)
	if err != nil {
		slog.Error("forwarding statement (build request)", slog.String("error", err.Error()))
		return nil, http.StatusInternalServerError, "internal server error", false
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{
		Timeout:   60 * time.Second,
		Transport: logger.LoggingRoundTripper(nil, slog.Default(), srv.logBodyLimit),
	}
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("forwarding statement (calling parser)", slog.String("error", err.Error()))
		return nil, http.StatusBadGateway, "statement parser is unavailable", false
	}
	defer resp.Body.Close()

	respBody, err := readAllLimited(resp.Body, maxParserResponse)
	if err != nil {
		slog.Error("forwarding statement (read parser response)", slog.String("error", err.Error()))
		return nil, http.StatusBadGateway, "statement parser returned an oversized response", false
	}

	if resp.StatusCode >= 500 {
		slog.Error("statement parser returned an error", slog.Int("status", resp.StatusCode), slog.String("response", string(respBody)))
		return nil, http.StatusBadGateway, "statement parser failed to process the file", false
	}

	var raw rawParserResponse
	if err := json.Unmarshal(respBody, &raw); err != nil {
		slog.Error("forwarding statement (unmarshal parser response)", slog.String("error", err.Error()))
		return nil, http.StatusBadGateway, "statement parser returned an invalid response", false
	}

	if raw.PasswordRequired || resp.StatusCode == http.StatusUnauthorized {
		return nil, http.StatusUnauthorized, "password required or incorrect", true
	}
	if resp.StatusCode >= 400 || raw.Error != "" {
		msg := raw.Error
		if msg == "" {
			msg = "failed to parse statement"
		}
		return nil, resp.StatusCode, msg, false
	}

	result := &parseStatementResult{
		Transactions: make([]models.ImportTransaction, 0, len(raw.Transactions)),
		Summary:      raw.Summary,
		PageCount:    raw.PageCount,
		TxnCount:     raw.TransactionCount,
	}
	// Always emit an array: the frontend renders the warnings as a list and
	// would have to null-check a missing/null field otherwise.
	result.ValidationErrors = raw.ValidationErrors
	if result.ValidationErrors == nil {
		result.ValidationErrors = []string{}
	}
	for _, t := range raw.Transactions {
		result.Transactions = append(result.Transactions, models.ImportTransaction{
			Date:        normalizeParserDate(t.Date, dateFormat),
			Description: t.Description,
			Amount:      money.FromFloat(t.Amount),
			Type:        normalizeParserType(t.Type),
		})
	}

	return result, http.StatusOK, "", false
}

// normalizeParserDate converts the parser's date string to the app's
// "YYYY-MM-DD" format used by the import pipeline. The dateFormat argument
// mirrors the frontend's date-format option (e.g. "DD/MM/YYYY"); when it is
// empty the common parser layouts are tried. Dates it can't parse are returned
// unchanged so the frontend can surface them.
func normalizeParserDate(d, dateFormat string) string {
	s := strings.TrimSpace(d)
	if s == "" {
		return s
	}

	layout := parserDateLayout(dateFormat)
	if layout != "" {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
		// Fall through to the auto-detect layouts below if the explicit
		// format doesn't match.
	}

	for _, l := range []string{"02 Jan 06", "02 Jan 2006", "02/01/2006", "01/02/2006", "2006-01-02", "02/01/06"} {
		if t, err := time.Parse(l, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return s
}

// parserDateLayout maps the frontend's date-format option values to Go time
// layouts. Unknown/auto values return "" so normalizeParserDate auto-detects.
func parserDateLayout(dateFormat string) string {
	switch dateFormat {
	case "DD/MM/YYYY":
		return "02/01/2006"
	case "MM/DD/YYYY":
		return "01/02/2006"
	case "DD/MM/YY":
		return "02/01/06"
	case "YYYY-MM-DD":
		return "2006-01-02"
	case "DD Mon YYYY":
		return "02 Jan 2006"
	case "DD Mon YY":
		return "02 Jan 06"
	default:
		return ""
	}
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
func (srv *Server) ListStatementExtractors(c *gin.Context) {
	url := strings.TrimRight(srv.parserURL, "/") + "/api/extractors"

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, url, nil)
	if err != nil {
		slog.Error("ListStatementExtractors (build request)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: logger.LoggingRoundTripper(nil, slog.Default(), srv.logBodyLimit),
	}
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("ListStatementExtractors (calling parser)", slog.String("error", err.Error()))
		validation.RespondError(c, "statement parser is unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, err := readAllLimited(resp.Body, maxParserResponse)
	if err != nil {
		slog.Error("ListStatementExtractors (read parser response)", slog.String("error", err.Error()))
		validation.RespondError(c, "statement parser returned an oversized response", http.StatusBadGateway)
		return
	}

	if resp.StatusCode >= 500 {
		slog.Error("statement parser failed to list extractors", slog.Int("status", resp.StatusCode), slog.String("response", string(respBody)))
		validation.RespondError(c, "statement parser failed to list extractors", http.StatusBadGateway)
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
		slog.Error("ListStatementExtractors (unmarshal parser response)", slog.String("error", err.Error()))
		validation.RespondError(c, "statement parser returned an invalid response", http.StatusBadGateway)
		return
	}
	if resp.StatusCode >= 400 || raw.Error != "" {
		msg := raw.Error
		if msg == "" {
			msg = "failed to list extractors"
		}
		validation.RespondError(c, msg, resp.StatusCode)
		return
	}

	c.JSON(http.StatusOK, gin.H{"extractors": raw.Extractors})
}
