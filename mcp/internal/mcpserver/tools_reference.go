package mcpserver

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fintrak/client/api"
	"github.com/fintrak/mcp/internal/readonly"
)

// Reference data: category groups, categories, payees and tags
// (backend/handlers/category_group.go, category.go, payee.go, tag.go).
//
// These four lists are what makes the rest of the surface usable: every filter
// and every id an aggregate returns is resolved through them.

func referenceTools() []Tool {
	return []Tool{
		{
			Name:  "list_groups",
			Title: "List category groups",
			Description: "Category groups in canonical order: the shared base/global groups first, then the user's own. " +
				"A group id can be passed to list_transactions as groupId to match every category in it.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/groups"},
			install: installListGroups,
		},
		{
			Name:  "list_categories",
			Title: "List categories",
			Description: "Every category visible to the user (their own plus the shared global ones), in group order and " +
				"alphabetical within each group, with the group each belongs to.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/categories"},
			install: installListCategories,
		},
		{
			Name:  "list_payees",
			Title: "List payees",
			Description: "The user's payees alphabetically, each with the account it is linked to when it stands for a transfer " +
				"counterpart.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/payees"},
			install: installListPayees,
		},
		{
			Name:  "list_tags",
			Title: "List tags",
			Description: "The tag vocabulary in use, most-used first, each with the number of transactions carrying it. " +
				"Tags are free text on transactions rather than a table, so the name is the identity.",
			Route:   readonly.Route{Method: http.MethodGet, Path: "/tags"},
			install: installListTags,
		},
	}
}

func installListGroups(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, _ noArgs) (any, error) {
		return c.ListGroups(ctx)
	})
}

func installListCategories(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, _ noArgs) (any, error) {
		return c.ListCategories(ctx)
	})
}

func installListPayees(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, _ noArgs) (any, error) {
		return c.ListPayees(ctx)
	})
}

func installListTags(s *mcp.Server, c *api.Client, tool *mcp.Tool) {
	addReadTool(s, tool, func(ctx context.Context, _ noArgs) (any, error) {
		return c.ListTags(ctx)
	})
}
