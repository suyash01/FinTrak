package mcpserver

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fintrak/client/api"
	"github.com/fintrak/mcp/internal/readonly"
)

// Recurring series (backend/handlers/recurring.go).
//
// A series is a template for a repeating charge or income: it creates
// transactions itself never, and the user matches real transactions to it by
// hand. These tools expose the series, what it forecasts, and what the server
// thinks fits it.

// seriesArgs identifies one series.
type seriesArgs struct {
	ID string `json:"id" jsonschema:"the recurring series id, from list_recurring"`
}

// forecastArgs bounds a forecast.
type forecastArgs struct {
	ID    string `json:"id" jsonschema:"the recurring series id, from list_recurring"`
	Count int    `json:"count,omitempty" jsonschema:"how many occurrences to project, default 12, max 60"`
}

// recurringSuggestionArgs bounds the suggestion list.
type recurringSuggestionArgs struct {
	ID    string `json:"id" jsonschema:"the recurring series id, from list_recurring"`
	Limit int    `json:"limit,omitempty" jsonschema:"how many suggestions to return, default 100, max 500"`
}

func recurringTools() []Tool {
	return []Tool{
		{
			Name:  "list_recurring",
			Title: "List recurring series",
			Description: "Every recurring series with its derived view: next due date, the amount normalized to a monthly figure, " +
				"and the account, category and payee it belongs to.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/recurring"},
			install: installListRecurring,
		},
		{
			Name:  "forecast_recurring",
			Title: "Forecast a series",
			Description: "The next occurrences of one series, each flagged with whether an attached transaction already covers it. " +
				"This is the projection behind \"what is coming up?\".",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/recurring/{id}/forecast"},
			install: installForecastRecurring,
		},
		{
			Name:  "get_recurring_suggestions",
			Title: "Suggest matches for a series",
			Description: "Transactions the server believes satisfy an occurrence of the series, best match first. Read-only: nothing " +
				"is attached, the user confirms in the app.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/recurring/{id}/suggestions"},
			install: installRecurringSuggestions,
		},
		{
			Name:        "list_recurring_transactions",
			Title:       "List a series' transactions",
			Description: "The transactions currently attached to one recurring series.",
			Route:       readonly.Route{Method: http.MethodGet, Path: "/recurring/{id}/transactions"},
			install:     installRecurringTransactions,
		},
		{
			Name:  "list_recurring_terms",
			Title: "List a series' terms",
			Description: "A series' effective-dated amount/account terms, which is how a price change is recorded. Each term covers " +
				"[startDate, endDate), so the range a date falls in is the amount that applied then.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/recurring/{id}/terms"},
			install: installRecurringTerms,
		},
	}
}

func installListRecurring(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, _ noArgs) (any, error) {
		return c.ListRecurring(ctx)
	})
}

func installForecastRecurring(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in forecastArgs) (any, error) {
		return c.RecurringForecast(ctx, in.ID, in.Count)
	})
}

func installRecurringSuggestions(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in recurringSuggestionArgs) (any, error) {
		return c.RecurringSuggestions(ctx, in.ID, in.Limit)
	})
}

func installRecurringTransactions(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in seriesArgs) (any, error) {
		return c.RecurringTransactions(ctx, in.ID)
	})
}

func installRecurringTerms(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, in seriesArgs) (any, error) {
		return c.ListRecurringTerms(ctx, in.ID)
	})
}
