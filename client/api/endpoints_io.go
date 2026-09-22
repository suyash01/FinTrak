package api

import (
	"context"
	"io"
	"strconv"
)

// Statement parsing, the Paperless-ngx integration, backup export/restore, and
// the served OpenAPI document (backend/handlers/statement.go, paperless.go,
// backup.go, backend/openapi.go).

// ParseStatement uploads a statement PDF and returns the parser's normalized
// rows for preview. Password unlocks an encrypted PDF, Extractor selects the
// parser (see ListStatementExtractors; "" defaults to the server's default
// extractor) and dateFormat hints the parser's date layout ("" auto-detects).
//
// It is the one multipart endpoint: the bytes go as the "file" part and the
// other fields as form values. The call is allowed parseTimeout (90s) rather
// than the 60s JSON default, because the handler queues the upload behind its
// four-way parse semaphore and then waits for the parser service. Errors a
// caller must handle: 400 when no file is attached or it is not a PDF, 413
// above 20 MB, 422 when the PDF is password-protected (retry with password) or
// nothing could be extracted (a scanned image-only file, or a mismatched
// extractor), 429 when the parser is busy (retry shortly), 408 when the request
// was cancelled, and 502 when the parser service is unavailable. A non-empty
// ValidationErrors in the result means the parser's own reconciliation failed
// and the rows are suspect.
func (c *Client) ParseStatement(ctx context.Context, filename string, pdf []byte, password, extractor, dateFormat string) (StatementParseResult, error) {
	r, err := uploadRequest("/statements/parse", map[string]string{
		"password":    password,
		"extractor":   extractor,
		"date_format": dateFormat,
	}, &multipartFile{field: "file", filename: filename, content: pdf})
	if err != nil {
		return StatementParseResult{}, err
	}
	return do[StatementParseResult](ctx, c, r.withTimeout(parseTimeout))
}

// ListStatementExtractors returns the extractor registry of the parser service.
// Each Name is a value ParseStatement and ImportPaperlessDocument accept as
// extractor; DisplayName is the label for a picker.
func (c *Client) ListStatementExtractors(ctx context.Context) ([]StatementExtractor, error) {
	res, err := do[ExtractorsResponse](ctx, c, get("/statements/extractors"))
	return res.Extractors, err
}

// PaperlessSettings returns the Paperless-ngx integration settings. The stored
// API token is never returned: HasToken tells the UI whether one is configured
// so it can render a masked field, and an empty PageSize means the server's
// default applies.
func (c *Client) PaperlessSettings(ctx context.Context) (PaperlessSettingsResponse, error) {
	return do[PaperlessSettingsResponse](ctx, c, get("/paperless/settings"))
}

// UpdatePaperlessSettings persists a partial settings update: nil pointers
// leave a field alone, and PageSize distinguishes "not provided" from an
// explicit null, which clears the stored page size.
//
// The response echoes back only the fields present in req and leaves the rest
// zeroed ("" / false / nil) — it is not the merged settings. Callers that need
// the full state must refetch with PaperlessSettings or merge the response into
// the values they already hold.
func (c *Client) UpdatePaperlessSettings(ctx context.Context, req UpdateUserSettingsRequest) (PaperlessSettingsResponse, error) {
	return do[PaperlessSettingsResponse](ctx, c, put("/paperless/settings").withJSON(req))
}

// PaperlessQuery filters the Paperless-ngx document list. The six include and
// exclude lists carry names from the lookup tables the response returns, not
// ids, and each is repeatable on the wire: within one list the values are
// OR-ed, and an exclude list removes whatever the matching include list allows.
// The list filters are the import screen's dropdowns; a zero value asks for the
// first page with the server's default page size and no filtering.
type PaperlessQuery struct {
	Page     int
	PageSize int
	Search   string

	CorrespondentInc []string
	CorrespondentExc []string
	DocumentTypeInc  []string
	DocumentTypeExc  []string
	TagInc           []string
	TagExc           []string
}

// apply writes the query parameters. Page and PageSize are sent only when
// positive, so the server's defaults (page 1, page size 25) apply; the server
// clamps page size to 100.
func (q PaperlessQuery) apply(r *request) *request {
	r = r.setQueryInt("page", q.Page).
		setQueryInt("pageSize", q.PageSize).
		setQuery("search", q.Search)
	// The include/exclude filters repeat their key rather than joining on
	// commas: the backend reads them with QueryArray and resolves each name to
	// a Paperless id.
	r.addQueryList("correspondentInc", q.CorrespondentInc)
	r.addQueryList("correspondentExc", q.CorrespondentExc)
	r.addQueryList("documentTypeInc", q.DocumentTypeInc)
	r.addQueryList("documentTypeExc", q.DocumentTypeExc)
	r.addQueryList("tagInc", q.TagInc)
	r.addQueryList("tagExc", q.TagExc)
	return r
}

// ListPaperlessDocuments returns one page of documents from the user's
// Paperless-ngx instance, filtered server-side by Paperless. Besides the page,
// the response carries the correspondent, document type and tag names so the
// import screen can build its filter dropdowns from one call instead of
// fetching every document.
//
// It answers 400 when the integration is not configured (URL and token), and
// 502 when Paperless is unreachable, redirects to another origin, rejects the
// token, or returns an unreadable body.
func (c *Client) ListPaperlessDocuments(ctx context.Context, q PaperlessQuery) (PaperlessDocumentsResponse, error) {
	return do[PaperlessDocumentsResponse](ctx, c, q.apply(get("/paperless/documents")))
}

// PaperlessDocumentFile streams a document's original file from the user's
// Paperless-ngx instance into w and returns the suggested filename. The bytes
// are always the upstream PDF: the response content type is pinned by the
// backend (the user-supplied Paperless URL is not trusted to set it), so a
// caller cannot rely on it to detect the format. Answers 404 for an unknown
// document and 413 when the file exceeds the 20 MB cap.
func (c *Client) PaperlessDocumentFile(ctx context.Context, id int, w io.Writer) (string, error) {
	return c.download(ctx, get("/paperless/documents/"+strconv.Itoa(id)+"/file"), w)
}

// ImportPaperlessDocument pulls one Paperless document, parses it and returns
// the normalized rows for preview. It only parses — nothing is persisted and
// nothing is tagged yet. The caller previews the result and then commits it
// through ImportTransactions with the document id in PaperlessDocumentIDs, so
// the configured paperlessTag is applied to the document after the import has
// actually written the rows.
//
// A document whose PDF is password-protected fails with 422 (retry with
// Password set) — deliberately not 401, which means the session expired; 400
// means the integration is not configured or the document id is missing, 404
// that Paperless has no such document, and 502 that Paperless is unreachable or
// rejected the token.
func (c *Client) ImportPaperlessDocument(ctx context.Context, req PaperlessImportRequest) (StatementParseResult, error) {
	return do[StatementParseResult](ctx, c, post("/paperless/import").withJSON(req))
}

// ExportBackup streams the user's whole graph (accounts, categories, payees,
// billing cycles, transactions, links, loan/recurring attachments, rules and
// non-secret settings) as a JSON bundle into w and returns the server's
// suggested filename (fintrak-backup-<YYYY-MM-DD>.json).
//
// Note the content-type quirk: the bundle is JSON (application/json) but is
// sent with Content-Disposition: attachment, because the same route serves the
// "save to disk" flow in a browser. The API token is not part of the bundle.
func (c *Client) ExportBackup(ctx context.Context, w io.Writer) (string, error) {
	return c.download(ctx, get("/export"), w)
}

// ImportBackup restores a bundle previously written by ExportBackup, reporting
// how many rows of each resource were created. The body is the bundle file read
// from disk and is forwarded verbatim, so bundle must be valid JSON.
//
// The call is allowed importTimeout (320s) rather than the 60s JSON default:
// a restore decodes a bundle the route accepts up to 256 MB of and replays it
// row by row inside one transaction, so the upload plus the replay can outlast
// the default. Expiring early would report a failure for a transaction the
// server is still committing, and the retry is refused with 409, so the
// deadline has to cover the whole restore rather than the first byte.
//
// The whole restore is one all-or-nothing transaction: on any error nothing is
// written, and rows that reference a resource the bundle did not carry are
// skipped and listed in Warnings rather than failing the restore. Every row is
// inserted under a freshly minted id with its references remapped, so a bundle
// never collides with existing data — but the route refuses with 409 when the
// user already has accounts, because merging a foreign bundle into existing
// data is ambiguous. It answers 413 above the 256 MB cap and 400 when the body
// is not a FinTrak bundle (wrong format key or unsupported version).
func (c *Client) ImportBackup(ctx context.Context, bundle []byte) (BackupImportResult, error) {
	return do[BackupImportResult](ctx, c, post("/import").withRawJSON(bundle).withTimeout(importTimeout))
}

// OpenAPISpec fetches the API's OpenAPI document. It is YAML, not JSON, so it
// is returned as raw bytes for embedding or writing to a file rather than being
// decoded; the route is public and needs no session.
func (c *Client) OpenAPISpec(ctx context.Context) ([]byte, error) {
	return c.downloadBytes(ctx, get("/openapi.yaml"))
}
