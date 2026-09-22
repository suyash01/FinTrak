package mcpserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fintrak/client/api"
	"github.com/fintrak/mcp/internal/readonly"
)

// Categorization rules (backend/handlers/rule.go).

// previewRuleArgs describes a hypothetical rule. Only the condition fields are
// here: the rule's target category is required by the API but does not affect
// the count, and the effect fields (payee, priority, tags, note) change what an
// apply would write, not how many transactions it would touch.
type previewRuleArgs struct {
	Pattern    string `json:"pattern" jsonschema:"the text the rule matches against a transaction's description"`
	MatchType  string `json:"matchType,omitempty" jsonschema:"contains (default), equals, prefix or regex"`
	CategoryID string `json:"categoryId" jsonschema:"a category id from list_categories — required by the API, and the category an apply would set"`

	AccountID        string `json:"accountId,omitempty" jsonschema:"only match transactions on this account"`
	FilterCategoryID string `json:"filterCategoryId,omitempty" jsonschema:"only match transactions already in this category"`
	FilterPayeeID    string `json:"filterPayeeId,omitempty" jsonschema:"only match transactions with this payee"`
	MinAmount        string `json:"minAmount,omitempty" jsonschema:"only match amounts at or above this value, in major units"`
	MaxAmount        string `json:"maxAmount,omitempty" jsonschema:"only match amounts at or below this value, in major units"`
	TxnType          string `json:"txnType,omitempty" jsonschema:"\"debit\" or \"credit\""`
	DateFrom         string `json:"dateFrom,omitempty" jsonschema:"only match transactions on or after this date, YYYY-MM-DD"`
	DateTo           string `json:"dateTo,omitempty" jsonschema:"only match transactions on or before this date, YYYY-MM-DD"`
	IsLinked         *bool  `json:"isLinked,omitempty" jsonschema:"true to require an existing link, false to require none"`
	IsRecurring      *bool  `json:"isRecurring,omitempty" jsonschema:"true to require a recurring series, false to require none"`
}

func ruleTools() []Tool {
	return []Tool{
		{
			Name:  "list_rules",
			Title: "List rules",
			Description: "The user's categorization rules, highest priority first, with each rule's conditions and the category, " +
				"payee, account and filters it references. These rules decide how uncategorized transactions are filed.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/rules"},
			install: installListRules,
		},
		{
			Name:  "preview_rule",
			Title: "Preview a rule",
			Description: "How many of the user's currently-uncategorized transactions a hypothetical rule would categorize, using " +
				"exactly the predicate a real apply uses. It writes nothing: use it to answer \"would this rule have caught them?\" " +
				"before the user creates the rule in the app, which this server cannot do.",
			Route:   readonly.Route{Method: http.MethodPost, Path: "/rules/preview"},
			install: installPreviewRule,
		},
	}
}

func installListRules(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, _ noArgs) (any, error) {
		return c.ListRules(ctx)
	})
}

func installPreviewRule(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in previewRuleArgs) (any, error) {
		minAmount, err := parseAmount(in.MinAmount)
		if err != nil {
			return nil, err
		}
		maxAmount, err := parseAmount(in.MaxAmount)
		if err != nil {
			return nil, err
		}
		req := api.CreateRuleRequest{
			Pattern:     in.Pattern,
			MatchType:   in.MatchType,
			CategoryID:  in.CategoryID,
			TxnType:     in.TxnType,
			DateFrom:    in.DateFrom,
			DateTo:      in.DateTo,
			IsLinked:    in.IsLinked,
			IsRecurring: in.IsRecurring,
		}
		setIfPresent(&req.AccountID, in.AccountID)
		setIfPresent(&req.FilterCategoryID, in.FilterCategoryID)
		setIfPresent(&req.FilterPayeeID, in.FilterPayeeID)
		setIfPresent(&req.MinAmount, minAmount)
		setIfPresent(&req.MaxAmount, maxAmount)

		preview, err := c.PreviewRule(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("previewing the rule: %w", err)
		}
		return preview, nil
	})
}

// setIfPresent writes a pointer field only when the caller supplied a value,
// keeping the request's "absent" (null) distinct from its zero value — the API
// reads an explicit null as a condition to clear.
func setIfPresent[T comparable](dst **T, value T) {
	var zero T
	if value == zero {
		return
	}
	*dst = new(value)
}
