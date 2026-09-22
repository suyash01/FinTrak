// Package mcpserver exposes the FinTrak REST API to model clients as an MCP
// server.
//
// It is a pure client of the API the web frontend and the TUI already use: no
// backend route, model or migration is involved, and every tool is a thin
// translation of its arguments into one client call.
//
// # Read-only by construction
//
// Idea #59 describes an agent surface whose mutations are propose-only. This
// server implements the read half of that, and nothing else: it exposes the
// ledger, the derived aggregates and the API's read-only preview twins
// (POST /transactions/validate, POST /rules/preview), and no tool can write.
// The guarantee is enforced in two places rather than by convention:
//
//   - Every tool declares the API operation it performs (Tool.Route), and
//     tools_test.go checks each one against backend/openapi.yaml: a GET, or a
//     POST on the short list of documented previews.
//   - NewGuard installs a transport in front of the client that refuses any
//     request outside those routes (plus the session routes the client itself
//     uses), so a future tool that reaches for a write fails locally instead of
//     reaching the ledger.
//
// # Authentication
//
// There is no scoped-token endpoint yet (idea #56), so the server signs in with
// the same credentials the other clients use: the server signs in on the first
// tool call through a receiving middleware, and the API client keeps the
// session alive from there (its own 401 replay trades the refresh token for a
// new access token). Credentials are never part of the protocol, and a session
// that ends is re-established on the next call.
package mcpserver

import (
	"context"
	"net/http"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fintrak/client/api"
	"github.com/fintrak/mcp/internal/readonly"
)

// Version is the server version reported to clients. main sets it from the
// build's -ldflags value, the same way the TUI reports its own.
var Version = "dev"

// instructions is the server's guidance for the model. It states the read-only
// contract first, because the useful failure mode of an agent on a finance
// ledger is proposing a change this server cannot make.
const instructions = `FinTrak is a personal finance ledger. Every tool here is READ-ONLY: nothing can create, change or delete a transaction, account, category, payee, rule, link or recurring series.

Money is returned as decimal major units (for example "1250.50") and every figure is already computed by the server. Do not add amounts up yourself: ask the aggregate tools (get_dashboard_summary, list_billing_cycles, get_money_flow, get_cash_flow_calendar) when a total is what the user wants, and never invent a number that a tool did not return. Dates are YYYY-MM-DD.

Start with list_accounts, list_categories, list_groups, list_payees and list_tags: they provide the ids every other tool takes. list_transactions is the ledger itself and accepts the same filters as the app's transaction list.

The suggestion tools (validate_transactions, preview_rule, get_transfer_suggestions, get_cashback_suggestions, get_recurring_suggestions) compute what the app itself would propose and write nothing. Present their output as suggestions for the user to confirm in the app; this server cannot apply them.`

// Tool is one MCP tool: how it is described to a client, the API operation it
// performs, and how it is installed on a server.
//
// Route is declared next to the handler rather than derived from it, so the
// read-only audit (tools_test.go) can check the whole surface against the API
// specification without executing anything.
type Tool struct {
	Name        string
	Title       string
	Description string
	Route       readonly.Route

	install func(*mcp.Server, *api.Client, *mcp.Tool)
}

// Tools returns every tool the server exposes, in presentation order.
func Tools() []Tool {
	var all []Tool
	for _, group := range []func() []Tool{
		accountTools,
		referenceTools,
		transactionTools,
		ruleTools,
		linkTools,
		recurringTools,
		dashboardTools,
		paperioTools,
	} {
		all = append(all, group()...)
	}
	return all
}

// ToolRoutes returns the API operations the tools perform, in registry order.
func ToolRoutes() []readonly.Route {
	tools := Tools()
	routes := make([]readonly.Route, 0, len(tools))
	for _, t := range tools {
		routes = append(routes, t.Route)
	}
	return routes
}

// sessionRoutes are the auth operations the client performs on its own behalf:
// the lazy sign-in and the single-flight refresh a 401 triggers. They are part
// of the guard's allowlist because they carry their own credentials and write
// no ledger data, and they are deliberately not tools.
var sessionRoutes = []readonly.Route{
	{Method: http.MethodPost, Path: "/auth/login"},
	{Method: http.MethodPost, Path: "/auth/refresh"},
}

// NewGuard builds the read-only guard for a client: the transport admits the
// routes the tools declare plus the session routes, and refuses everything
// else. Install it with (*api.Client).SetTransport.
func NewGuard(c *api.Client, next http.RoundTripper) *readonly.Guard {
	base := ""
	if u, err := url.Parse(c.BaseURL()); err == nil {
		base = u.Path
	}
	routes := append(ToolRoutes(), sessionRoutes...)
	return readonly.New(next, base, routes)
}

// Register installs every tool on s, all annotated read-only.
func Register(s *mcp.Server, c *api.Client) {
	for _, t := range Tools() {
		tool := &mcp.Tool{
			Name:        t.Name,
			Title:       t.Title,
			Description: t.Description,
			Annotations: &mcp.ToolAnnotations{
				ReadOnlyHint:   true,
				IdempotentHint: true,
				OpenWorldHint:  new(false),
			},
		}
		t.install(s, c, tool)
	}
}

// New builds the MCP server: the FinTrak tool surface over c. creds are used to
// sign the client in on the first tool call; leave them empty when the caller
// has already installed a session.
func New(c *api.Client, creds Credentials) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "fintrak",
		Title:   "FinTrak",
		Version: Version,
	}, &mcp.ServerOptions{Instructions: instructions})
	Register(s, c)
	if creds.Email != "" {
		s.AddReceivingMiddleware(sessionMiddleware(&session{
			client:   c,
			email:    creds.Email,
			password: creds.Password,
		}))
	}
	return s
}

// addReadTool registers one read-only tool. Handlers stay thin: they translate
// the tool's arguments into a single client call and hand back its payload,
// which the SDK serializes as both structured content and JSON text. A failed
// call becomes a tool error carrying the API's own message.
func addReadTool[In any](s *mcp.Server, tool *mcp.Tool, call func(context.Context, In) (any, error)) {
	mcp.AddTool(s, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		out, err := call(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})
}

// noArgs is the input type of a tool that takes no arguments; the SDK turns it
// into an empty object schema.
type noArgs struct{}
