package mcpserver

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fintrak/client/api"
	"github.com/fintrak/mcp/internal/readonly"
)

// The Paperless-ngx document list (backend/handlers/paperless.go).
//
// Only the list is exposed: it is how a receipt or statement is found in the
// user's own Paperless instance. The document file itself is a PDF stream, and
// the settings route carries the integration token, so neither is a tool.

// paperlessArgs filters the document list. The include and exclude lists carry
// names from the response's own lookup tables, not ids.
type paperlessArgs struct {
	Search           string   `json:"search,omitempty" jsonschema:"full-text search passed through to Paperless"`
	Page             int      `json:"page,omitempty" jsonschema:"page number, default 1"`
	PageSize         int      `json:"pageSize,omitempty" jsonschema:"page size, default 25, max 100"`
	CorrespondentInc []string `json:"correspondentInc,omitempty" jsonschema:"only documents from any of these correspondents"`
	CorrespondentExc []string `json:"correspondentExc,omitempty" jsonschema:"drop documents from any of these correspondents"`
	DocumentTypeInc  []string `json:"documentTypeInc,omitempty" jsonschema:"only documents of any of these types"`
	DocumentTypeExc  []string `json:"documentTypeExc,omitempty" jsonschema:"drop documents of any of these types"`
	TagInc           []string `json:"tagInc,omitempty" jsonschema:"only documents carrying any of these tags"`
	TagExc           []string `json:"tagExc,omitempty" jsonschema:"drop documents carrying any of these tags"`
}

func paperioTools() []Tool {
	return []Tool{
		{
			Name:  "list_paperless_documents",
			Title: "List Paperless documents",
			Description: "One page of documents from the user's Paperless-ngx instance, with the correspondent, document type and tag " +
				"names available for filtering. Answers 400 when the integration is not configured and 502 when Paperless is " +
				"unreachable or rejects the token.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/paperless/documents"},
			install: installListPaperlessDocuments,
		},
	}
}

func installListPaperlessDocuments(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in paperlessArgs) (any, error) {
		return c.ListPaperlessDocuments(ctx, api.PaperlessQuery{
			Page:             in.Page,
			PageSize:         in.PageSize,
			Search:           in.Search,
			CorrespondentInc: in.CorrespondentInc,
			CorrespondentExc: in.CorrespondentExc,
			DocumentTypeInc:  in.DocumentTypeInc,
			DocumentTypeExc:  in.DocumentTypeExc,
			TagInc:           in.TagInc,
			TagExc:           in.TagExc,
		})
	})
}
