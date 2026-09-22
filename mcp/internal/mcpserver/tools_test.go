package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	"github.com/fintrak/client/api"
	"github.com/fintrak/mcp/internal/readonly"
)

// Placeholder ids stand in for the uuids the tools pass through; the stub API
// never parses them, it only records the request.
const (
	testAccountID  = "11111111-1111-1111-1111-111111111111"
	testCategoryID = "22222222-2222-2222-2222-222222222222"
	testSeriesID   = "33333333-3333-3333-3333-333333333333"
)

// sampleArgs is one minimal valid call per tool. TestArgumentTablesCoverEveryTool
// keeps the table in step with the registry, so a new tool cannot be added
// without describing how it is exercised.
var sampleArgs = map[string]map[string]any{
	"list_accounts":               {},
	"list_account_types":          {},
	"list_billing_cycles":         {"accountId": testAccountID},
	"get_loan_schedule":           {"accountId": testAccountID},
	"get_loan_payoff":             {"accountId": testAccountID, "date": "2026-01-31"},
	"list_groups":                 {},
	"list_categories":             {},
	"list_payees":                 {},
	"list_tags":                   {},
	"list_transactions":           {"accountId": testAccountID, "dateFrom": "2026-01-01", "limit": 5},
	"validate_transactions":       {"accountId": testAccountID, "transactions": []any{map[string]any{"date": "2026-01-02", "description": "Coffee", "amount": "12.50", "type": "debit"}}},
	"list_rules":                  {},
	"preview_rule":                {"pattern": "NETFLIX", "categoryId": testCategoryID},
	"list_links":                  {"type": "transfer"},
	"get_transfer_suggestions":    {"limit": 5},
	"get_cashback_suggestions":    {},
	"get_link_cycles":             {"accountId": testAccountID},
	"list_recurring":              {},
	"forecast_recurring":          {"id": testSeriesID, "count": 3},
	"get_recurring_suggestions":   {"id": testSeriesID},
	"list_recurring_transactions": {"id": testSeriesID},
	"list_recurring_terms":        {"id": testSeriesID},
	"get_dashboard_summary":       {"accountId": testAccountID, "groupBy": "billing_cycle", "cycles": 3},
	"get_money_flow":              {"limit": 5},
	"get_money_flow_timeline":     {},
	"get_cash_flow_calendar":      {"dateFrom": "2026-01-01", "dateTo": "2026-01-31"},
	"list_paperless_documents":    {"search": "receipt", "pageSize": 5},
}

// expectedQuery is what each tool must ask the API for, given the sample call
// above. Matching the route proves which operation a tool calls; only the query
// proves it calls it with the arguments the model supplied, which is where a
// date window fed into the wrong filter or a dropped paging parameter would
// land — both keep the route and change the answer. Every key the sample call
// sets is asserted, and anything the tool invents is a mismatch too.
//
// The table mirrors api/spec_parity_test.go's route cases for the same reason:
// the client pins its own methods, and this pins the tool-argument → client
// mapping on top of it.
var expectedQuery = map[string]url.Values{
	"list_accounts":               {},
	"list_account_types":          {},
	"list_billing_cycles":         {},
	"get_loan_schedule":           {},
	"get_loan_payoff":             {"date": {"2026-01-31"}},
	"list_groups":                 {},
	"list_categories":             {},
	"list_payees":                 {},
	"list_tags":                   {},
	"list_transactions":           {"accountId": {testAccountID}, "dateFrom": {"2026-01-01"}, "limit": {"5"}},
	"validate_transactions":       {},
	"list_rules":                  {},
	"preview_rule":                {},
	"list_links":                  {"type": {"transfer"}},
	"get_transfer_suggestions":    {"limit": {"5"}},
	"get_cashback_suggestions":    {},
	"get_link_cycles":             {"accountId": {testAccountID}},
	"list_recurring":              {},
	"forecast_recurring":          {"count": {"3"}},
	"get_recurring_suggestions":   {},
	"list_recurring_transactions": {},
	"list_recurring_terms":        {},
	"get_dashboard_summary":       {"accountId": {testAccountID}, "groupBy": {"billing_cycle"}, "cycles": {"3"}},
	"get_money_flow":              {"limit": {"5"}},
	"get_money_flow_timeline":     {},
	"get_cash_flow_calendar":      {"dateFrom": {"2026-01-01"}, "dateTo": {"2026-01-31"}},
	"list_paperless_documents":    {"pageSize": {"5"}, "search": {"receipt"}},
}

// readOnlyPosts are the POST operations the API documents as previews that
// write nothing. A tool may only use one of these, and the list is the whole
// reason a POST is admitted to a read-only surface — adding to it is a claim
// about the API, not a convenience.
var readOnlyPosts = map[string]bool{
	"/transactions/validate": true,
	"/rules/preview":         true,
}

// unexposedRoutes are the API's read-only operations that are deliberately not
// tools. The completeness check below fails on any other uncovered read, so a
// new endpoint forces a decision here instead of shipping as a silent gap.
var unexposedRoutes = map[string]string{
	"GET /health":                        "liveness probe, no ledger data",
	"GET /openapi.yaml":                  "the API document itself",
	"GET /auth/me":                       "session identity rather than ledger data",
	"GET /accounts/{id}/export":          "CSV stream; list_transactions covers the rows",
	"GET /transactions/export":           "CSV stream; list_transactions covers the rows",
	"GET /export":                        "whole-ledger JSON bundle, far too large for a model context",
	"GET /paperless/settings":            "carries the Paperless integration token",
	"GET /paperless/documents/{id}/file": "binary PDF stream",
	"GET /statements/extractors":         "parser service metadata",
	"GET /admin/catalog":                 "admin console surface, not the user's ledger",
}

// recorded is one request the stub API saw.
type recorded struct {
	method string
	path   string
	query  string
}

// stubAPI is a FinTrak API stand-in: it records every request and answers
// "null", which decodes into any response type, so a test can drive a tool
// without modelling the payload.
type stubAPI struct {
	mu   sync.Mutex
	seen []recorded
	body string
	srv  *httptest.Server
}

func newStubAPI(t *testing.T) *stubAPI {
	t.Helper()
	stub := &stubAPI{}
	stub.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		stub.seen = append(stub.seen, recorded{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery})
		body := stub.body
		stub.mu.Unlock()
		if body == "" {
			body = "null"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(stub.srv.Close)
	return stub
}

// respond makes the stub answer with body instead of "null", for the tests that
// need rows to look at.
func (s *stubAPI) respond(body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.body = body
}

// client returns a client for the stub with a session pre-installed, so the
// lazy sign-in stays out of the way of a tool-route assertion.
func (s *stubAPI) client(t *testing.T) *api.Client {
	t.Helper()
	c, err := api.New(s.srv.URL + "/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	c.SetTokens("access-token", "refresh-token")
	return c
}

func (s *stubAPI) requests() []recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recorded(nil), s.seen...)
}

// connect builds the MCP server over c and returns a client session talking to
// it, the same wiring an MCP client would use over stdio. The server signs in
// with no credentials, so tests that do not exercise the sign-in are unaffected
// by it.
func connect(t *testing.T, c *api.Client) *mcp.ClientSession {
	t.Helper()
	return connectWith(t, c, Credentials{})
}

// connectWith is connect with credentials configured.
func connectWith(t *testing.T, c *api.Client, creds Credentials) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	serverSession, err := New(c, creds).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connecting the server: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).
		Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connecting the client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// call runs one tool and returns its result, failing on a transport error.
func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: CallTool: %v", name, err)
	}
	return res
}

// resultText flattens a tool result's text content, which is where the SDK
// writes the JSON payload the model reads.
func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, content := range res.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

// errorText flattens a tool result that reported a failure.
func errorText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, content := range res.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
			b.WriteString(" ")
		}
	}
	return strings.TrimSpace(b.String())
}

// TestArgumentTablesCoverEveryTool keeps the registry and the test tables in
// step: a tool with no sample call is a tool nothing exercises, and a tool with
// no expected query is a tool whose arguments nothing checks.
func TestArgumentTablesCoverEveryTool(t *testing.T) {
	for _, tool := range Tools() {
		if _, ok := sampleArgs[tool.Name]; !ok {
			t.Errorf("tool %q has no sample call in sampleArgs", tool.Name)
		}
		if _, ok := expectedQuery[tool.Name]; !ok {
			t.Errorf("tool %q has no expected query in expectedQuery", tool.Name)
		}
	}
	names := map[string]bool{}
	for _, tool := range Tools() {
		names[tool.Name] = true
	}
	for name := range sampleArgs {
		if !names[name] {
			t.Errorf("sampleArgs names %q, which is not a tool", name)
		}
	}
	for name := range expectedQuery {
		if !names[name] {
			t.Errorf("expectedQuery names %q, which is not a tool", name)
		}
	}
}

// TestToolNamesAreUnique keeps a duplicate from silently replacing a tool.
func TestToolNamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range Tools() {
		if seen[tool.Name] {
			t.Errorf("duplicate tool name %q", tool.Name)
		}
		seen[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has no description", tool.Name)
		}
	}
}

// TestEveryToolHitsItsDeclaredRoute is the tool surface's proof of work: each
// tool is called through a real MCP session, over a client whose transport is
// the read-only guard, against a stub API that records what arrived. A tool
// that calls a different client method than it declares (or one the guard
// refuses) fails here, and so does one that reaches the right route with the
// wrong arguments: the recorded query is checked against expectedQuery.
func TestEveryToolHitsItsDeclaredRoute(t *testing.T) {
	for _, tool := range Tools() {
		t.Run(tool.Name, func(t *testing.T) {
			stub := newStubAPI(t)
			c := stub.client(t)
			c.SetTransport(NewGuard(c, http.DefaultTransport))
			session := connect(t, c)

			res := call(t, session, tool.Name, sampleArgs[tool.Name])
			if res.IsError {
				t.Fatalf("%s reported an error: %s", tool.Name, errorText(res))
			}

			requests := stub.requests()
			if len(requests) != 1 {
				t.Fatalf("%s made %d requests (%v), want exactly one", tool.Name, len(requests), requests)
			}
			got := requests[0]
			path := strings.TrimPrefix(got.path, "/api/v1")
			if !readonly.Match(tool.Route, got.method, path) {
				t.Errorf("%s called %s %s, want %s", tool.Name, got.method, path, tool.Route)
			}
			query, err := url.ParseQuery(got.query)
			if err != nil {
				t.Fatalf("%s sent an unparseable query %q: %v", tool.Name, got.query, err)
			}
			if want := expectedQuery[tool.Name]; !reflect.DeepEqual(query, want) {
				t.Errorf("%s asked for %v, want %v", tool.Name, query, want)
			}
		})
	}
}

// TestGuardRefusesWritesThroughTheClient is the enforcement half of the
// read-only promise: not a claim about the tool list, but the client actually
// being unable to make a write.
func TestGuardRefusesWritesThroughTheClient(t *testing.T) {
	writes := []struct {
		name string
		call func(context.Context, *api.Client) error
	}{
		{name: "delete a transaction", call: func(ctx context.Context, c *api.Client) error {
			return c.DeleteTransaction(ctx, testAccountID)
		}},
		{name: "bulk delete", call: func(ctx context.Context, c *api.Client) error {
			_, err := c.BulkDelete(ctx, []string{testAccountID})
			return err
		}},
		{name: "update a rule", call: func(ctx context.Context, c *api.Client) error {
			_, err := c.UpdateRule(ctx, testAccountID, api.UpdateRuleRequest{})
			return err
		}},
		{name: "log out", call: func(ctx context.Context, c *api.Client) error {
			return c.Logout(ctx)
		}},
	}

	for _, write := range writes {
		t.Run(write.name, func(t *testing.T) {
			stub := newStubAPI(t)
			c := stub.client(t)
			c.SetTransport(NewGuard(c, http.DefaultTransport))

			err := write.call(context.Background(), c)
			if !errors.Is(err, readonly.ErrNotAllowed) {
				t.Fatalf("error = %v, want a read-only refusal", err)
			}
			if requests := stub.requests(); len(requests) != 0 {
				t.Fatalf("the write reached the API: %v", requests)
			}
		})
	}
}

// TestGuardAdmitsTheSessionRoutes keeps the lazy sign-in working: the guard
// refuses everything the tools do not declare, so the login the server performs
// on its own behalf has to be on the allowlist explicitly.
func TestGuardAdmitsTheSessionRoutes(t *testing.T) {
	c, err := api.New("http://api.test/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	guard := NewGuard(c, http.DefaultTransport)

	for _, route := range sessionRoutes {
		if !guard.Allows(route.Method, route.Path) {
			t.Errorf("the guard refuses the session route %s", route)
		}
	}
}

// TestIDToolsRejectAnEmptyID keeps a missing id from being reported as a
// read-only refusal: an empty id reaches the client as /accounts//billing-cycles
// or /recurring//terms, whose empty path segment the guard rejects, so the model
// is told the server will not perform the read instead of that the argument is
// missing — and reports a broken server rather than asking for one.
func TestIDToolsRejectAnEmptyID(t *testing.T) {
	cases := []struct {
		tool    string
		args    map[string]any
		missing string
	}{
		{tool: "list_billing_cycles", args: map[string]any{"accountId": ""}, missing: "accountId is required"},
		{tool: "get_loan_schedule", args: map[string]any{"accountId": ""}, missing: "accountId is required"},
		{tool: "get_loan_payoff", args: map[string]any{"accountId": "", "date": "2025-01-01"}, missing: "accountId is required"},
		{tool: "forecast_recurring", args: map[string]any{"id": ""}, missing: "id is required"},
		{tool: "get_recurring_suggestions", args: map[string]any{"id": ""}, missing: "id is required"},
		{tool: "list_recurring_transactions", args: map[string]any{"id": ""}, missing: "id is required"},
		{tool: "list_recurring_terms", args: map[string]any{"id": ""}, missing: "id is required"},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			stub := newStubAPI(t)
			// The guard is installed, as in production, so a missing argument
			// would otherwise come back as the guard's refusal rather than as
			// the validation error under test.
			c := stub.client(t)
			c.SetTransport(NewGuard(c, http.DefaultTransport))
			session := connect(t, c)

			res := call(t, session, tc.tool, tc.args)
			if !res.IsError {
				t.Fatalf("%s with an empty id succeeded, want a validation error", tc.tool)
			}
			if text := errorText(res); !strings.Contains(text, tc.missing) {
				t.Errorf("%s reported %q, want it to say %q", tc.tool, text, tc.missing)
			}
			if requests := stub.requests(); len(requests) != 0 {
				t.Errorf("%s sent %v, want no request for an empty id", tc.tool, requests)
			}
		})
	}
}

// TestUnpagedToolsCapTheirResponse covers the two tools whose API route cannot
// page: they must hand the model a bounded page and say when the tail was
// dropped, so a large ledger cannot arrive as one multi-megabyte tool result.
// The limit is clamped, not trusted: a model cannot ask for an unbounded dump.
func TestUnpagedToolsCapTheirResponse(t *testing.T) {
	cases := []struct {
		name      string
		tool      string
		args      map[string]any
		body      string
		wantLimit int
		wantItems int
		truncated bool
	}{
		{
			name: "links are cut to the requested limit", tool: "list_links",
			args: map[string]any{"limit": 2}, body: `[{"id":"l1"},{"id":"l2"},{"id":"l3"}]`,
			wantLimit: 2, wantItems: 2, truncated: true,
		},
		{
			name: "a shorter link list is returned whole", tool: "list_links",
			args: map[string]any{}, body: `[{"id":"l1"},{"id":"l2"}]`,
			wantLimit: defaultListLimit, wantItems: 2,
		},
		{
			name: "a series' transactions are cut to the requested limit", tool: "list_recurring_transactions",
			args: map[string]any{"id": testSeriesID, "limit": 1}, body: `{"data":[{"id":"t1"},{"id":"t2"}]}`,
			wantLimit: 1, wantItems: 1, truncated: true,
		},
		{
			name: "the limit is clamped to the maximum", tool: "list_recurring_transactions",
			args: map[string]any{"id": testSeriesID, "limit": maxListLimit + 1000}, body: `{"data":[{"id":"t1"}]}`,
			wantLimit: maxListLimit, wantItems: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := newStubAPI(t)
			stub.respond(tc.body)
			c := stub.client(t)
			c.SetTransport(NewGuard(c, http.DefaultTransport))
			session := connect(t, c)

			res := call(t, session, tc.tool, tc.args)
			if res.IsError {
				t.Fatalf("%s reported an error: %s", tc.tool, errorText(res))
			}

			var page struct {
				Items     []map[string]any `json:"items"`
				Limit     int              `json:"limit"`
				Truncated bool             `json:"truncated"`
			}
			if err := json.Unmarshal([]byte(resultText(res)), &page); err != nil {
				t.Fatalf("%s returned %q, want the capped page: %v", tc.tool, resultText(res), err)
			}
			if len(page.Items) != tc.wantItems {
				t.Errorf("%s returned %d rows, want %d", tc.tool, len(page.Items), tc.wantItems)
			}
			if page.Limit != tc.wantLimit {
				t.Errorf("%s applied limit %d, want %d", tc.tool, page.Limit, tc.wantLimit)
			}
			if page.Truncated != tc.truncated {
				t.Errorf("%s reported truncated=%v, want %v", tc.tool, page.Truncated, tc.truncated)
			}
		})
	}
}

// spec is the part of backend/openapi.yaml these tests read.
type spec struct {
	Paths map[string]map[string]any `yaml:"paths"`
}

func loadSpec(t *testing.T) spec {
	t.Helper()
	data, err := os.ReadFile("../../../backend/openapi.yaml")
	if err != nil {
		t.Fatalf("reading the OpenAPI document: %v", err)
	}
	var s spec
	if err := yaml.Unmarshal(data, &s); err != nil {
		t.Fatalf("parsing the OpenAPI document: %v", err)
	}
	if len(s.Paths) == 0 {
		t.Fatal("the OpenAPI document has no paths")
	}
	return s
}

// TestSideEffectingRoutesAreDisclosed keeps the claim the server makes to the
// model honest. A few GETs in this API are not pure reads — they materialize an
// account's billing cycles, or re-seal a stored Paperless token — so a tool
// performing one must say so in the description the client shows the model, and
// a tool on a pure read must not claim a side effect (or the disclosure means
// nothing). The same declaration drives the tool's readOnlyHint, so a hint that
// says "read-only" about a call that writes fails here too.
//
// The claim itself lives in readonly.SideEffectingGETs, so the audit compares the
// two rather than trusting either.
func TestSideEffectingRoutesAreDisclosed(t *testing.T) {
	sideEffecting := make(map[string]bool, len(readonly.SideEffectingGETs))
	for _, route := range readonly.SideEffectingGETs {
		sideEffecting[route.String()] = true
	}

	performed := map[string]bool{}
	for _, tool := range Tools() {
		route := tool.Route.String()
		performed[route] = true
		switch want := sideEffecting[route]; {
		case want && tool.SideEffect == "":
			t.Errorf("%s performs %s, which is not a pure read, but declares no side effect", tool.Name, route)
		case !want && tool.SideEffect != "":
			t.Errorf("%s declares a side effect for %s, a pure read: add the route to readonly.SideEffectingGETs if that changed",
				tool.Name, route)
		}
	}
	for _, route := range readonly.SideEffectingGETs {
		if !performed[route.String()] {
			t.Errorf("readonly.SideEffectingGETs lists %s, which no tool performs: drop it or expose the tool", route)
		}
	}

	// The disclosure has to reach the client, not just the registry: the model
	// reads the description served over the protocol, and a client that
	// auto-approves read-only calls reads the hint.
	stub := newStubAPI(t)
	session := connect(t, stub.client(t))
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	descriptions := make(map[string]string, len(listed.Tools))
	annotations := make(map[string]*mcp.ToolAnnotations, len(listed.Tools))
	for _, tool := range listed.Tools {
		descriptions[tool.Name] = tool.Description
		annotations[tool.Name] = tool.Annotations
	}
	for _, tool := range Tools() {
		ann := annotations[tool.Name]
		if ann == nil {
			t.Errorf("%s: the tool is served without annotations", tool.Name)
			continue
		}
		// ReadOnlyHint is derived from the same declaration this test audits,
		// so a tool that writes is advertised as not-read-only: a client that
		// auto-approves read-only calls must not auto-approve these.
		if want := tool.SideEffect == ""; ann.ReadOnlyHint != want {
			t.Errorf("%s: readOnlyHint = %v, want %v (side effect: %q)", tool.Name, ann.ReadOnlyHint, want, tool.SideEffect)
		}
		if tool.SideEffect == "" {
			continue
		}
		if !strings.Contains(descriptions[tool.Name], tool.SideEffect) {
			t.Errorf("%s: the description sent to clients omits the declared side effect", tool.Name)
		}
	}
}

// TestToolRoutesAreDocumentedReadOnly checks every tool against the API
// document: the operation must exist, and it must be a GET or one of the
// preview POSTs. This is what "read-only" means for the tool surface — it is
// not a property of the Go code, it is a property of the operations the tools
// perform, so it is checked against the specification rather than the wire.
func TestToolRoutesAreDocumentedReadOnly(t *testing.T) {
	document := loadSpec(t)
	for _, tool := range Tools() {
		methods, ok := document.Paths[tool.Route.Path]
		if !ok {
			t.Errorf("%s declares %s, which the OpenAPI document does not define", tool.Name, tool.Route)
			continue
		}
		if _, ok := methods[strings.ToLower(tool.Route.Method)]; !ok {
			t.Errorf("%s declares %s, which the OpenAPI document does not define", tool.Name, tool.Route)
			continue
		}
		switch tool.Route.Method {
		case http.MethodGet:
		case http.MethodPost:
			if !readOnlyPosts[tool.Route.Path] {
				t.Errorf("%s declares POST %s, which is not on the read-only preview list", tool.Name, tool.Route.Path)
			}
		default:
			t.Errorf("%s declares %s, which is not a read", tool.Name, tool.Route)
		}
	}
}

// TestEveryReadOnlyOperationIsExposedOrExempted is the other direction: a
// read-only API operation that no tool claims has to be listed, with a reason,
// in unexposedRoutes. A new endpoint therefore fails this test until someone
// decides whether the model should see it.
func TestEveryReadOnlyOperationIsExposedOrExempted(t *testing.T) {
	document := loadSpec(t)

	claimed := map[string]bool{}
	for _, route := range ToolRoutes() {
		claimed[route.Method+" "+route.Path] = true
	}

	var missing []string
	for path, methods := range document.Paths {
		for method := range methods {
			upper := strings.ToUpper(method)
			readOnly := upper == http.MethodGet ||
				(upper == http.MethodPost && readOnlyPosts[path])
			if !readOnly {
				continue
			}
			key := upper + " " + path
			if claimed[key] || unexposedRoutes[key] != "" {
				continue
			}
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	for _, key := range missing {
		t.Errorf("%s is read-only and neither exposed as a tool nor listed in unexposedRoutes", key)
	}
}

// TestExemptionsAreStillReadOnly keeps unexposedRoutes honest: an entry must
// name a read-only operation the document defines, so the list cannot grow to
// silence the completeness check for a write.
func TestExemptionsAreStillReadOnly(t *testing.T) {
	document := loadSpec(t)
	for key := range unexposedRoutes {
		method, path, ok := strings.Cut(key, " ")
		if !ok {
			t.Errorf("malformed exemption %q", key)
			continue
		}
		methods, ok := document.Paths[path]
		if !ok {
			t.Errorf("exemption %q names a path the OpenAPI document does not define", key)
			continue
		}
		if _, ok := methods[strings.ToLower(method)]; !ok {
			t.Errorf("exemption %q names an operation the OpenAPI document does not define", key)
			continue
		}
		if method != http.MethodGet && !(method == http.MethodPost && readOnlyPosts[path]) {
			t.Errorf("exemption %q is not read-only, so it has no business being exempted", key)
		}
	}
}
