package mcpserver

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fintrak/client/api"
	"github.com/fintrak/mcp/internal/readonly"
)

// Links and the suggestion scorers (backend/handlers/link.go, link_cycles.go).
//
// A link pairs two of the user's transactions: a transfer between their own
// accounts, a refund or cashback against an earlier purchase, or a bill
// payment. The suggestion tools are the read-only half of linking — they rank
// candidate pairs and change nothing.

// listLinksArgs narrows the link list.
type listLinksArgs struct {
	Type          string `json:"type,omitempty" jsonschema:"transfer, cashback, refund or bill_payment"`
	TransactionID string `json:"transactionId,omitempty" jsonschema:"only links touching this transaction id"`
}

// suggestionArgs is the paging shared by both suggestion endpoints.
type suggestionArgs struct {
	Page  int `json:"page,omitempty" jsonschema:"page number, default 1"`
	Limit int `json:"limit,omitempty" jsonschema:"page size, default 50, max 100"`
}

// linkCyclesArgs is the window for the cycle report.
type linkCyclesArgs struct {
	DateFrom  string `json:"dateFrom,omitempty" jsonschema:"inclusive start date, YYYY-MM-DD"`
	DateTo    string `json:"dateTo,omitempty" jsonschema:"inclusive end date, YYYY-MM-DD"`
	AccountID string `json:"accountId,omitempty" jsonschema:"restrict the flows to one account id"`
}

func linkTools() []Tool {
	return []Tool{
		{
			Name:  "list_links",
			Title: "List links",
			Description: "The user's links, newest first, optionally narrowed to one type or to the links touching one transaction. " +
				"Every link carries both of its transactions joined, so either side can be read without a second call.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/links"},
			install: installListLinks,
		},
		{
			Name:  "get_transfer_suggestions",
			Title: "Suggest transfer links",
			Description: "Debit/credit pairs on different accounts that look like a transfer, scored by amount match, date proximity and " +
				"transfer wording. Nothing is linked: present the suggestions for the user to confirm in the app.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/links/transfer-suggestions"},
			install: installTransferSuggestions,
		},
		{
			Name:  "get_cashback_suggestions",
			Title: "Suggest cashback links",
			Description: "Credit transactions that look like a cashback or refund, each paired with the prior debits on the same account " +
				"that likely funded it. Nothing is linked.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/links/cashback-suggestions"},
			install: installCashbackSuggestions,
		},
		{
			Name:  "get_link_cycles",
			Title: "Report account link cycles",
			Description: "The account-to-account flows the money-flow graph cannot draw because it has to stay acyclic: reciprocal pairs " +
				"netted into one edge, the back edges that closed a longer loop, and one-sided flows that look like a half-entered " +
				"transfer. Useful for explaining why a transfer pair is not visible as a flow.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/links/cycles"},
			install: installLinkCycles,
		},
	}
}

func installListLinks(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in listLinksArgs) (any, error) {
		return c.ListLinks(ctx, in.Type, in.TransactionID)
	})
}

func installTransferSuggestions(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in suggestionArgs) (any, error) {
		return c.TransferSuggestions(ctx, in.Page, in.Limit)
	})
}

func installCashbackSuggestions(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in suggestionArgs) (any, error) {
		return c.CashbackSuggestions(ctx, in.Page, in.Limit)
	})
}

func installLinkCycles(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in linkCyclesArgs) (any, error) {
		return c.LinkCycles(ctx, in.DateFrom, in.DateTo, in.AccountID)
	})
}
