package api

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"

	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// specPath is the backend's wire contract, relative to this package.
const specPath = "../../backend/openapi.yaml"

// The route table below is the TUI's declaration of API coverage: one entry per
// operation registered in backend/main.go, each holding a real call to the
// client method that performs it. Because the calls are compiled, a renamed or
// deleted method breaks the build; because the table is compared against
// backend/openapi.yaml (kept in lockstep with the router by the backend's own
// route-parity test), a new backend route fails the suite until the client
// claims it. TestEveryRouteHitsItsDocumentedPath then executes every entry
// against a stub server and asserts the method, path and query actually sent.

// Path parameters used by the table. The closures and the expected paths share
// them, so an entry cannot accidentally assert a different URL than it calls.
const (
	idAcct = "acct-1"
	idTxn  = "txn-1"
	idTerm = "term-1"
	idFile = "42"
)

// routeCase is one client call and the wire request it must produce.
type routeCase struct {
	name   string
	method string
	path   string // gin-style, e.g. /accounts/:id/billing-cycles
	call   func(ctx context.Context, c *Client) error
	body   string // canned response body

	// query holds parameters that must be present with exactly these values.
	query map[string]string
	// cookies are Set-Cookie values the response must carry (the refresh
	// endpoint only proves itself if it re-issues a token).
	cookies []*http.Cookie
	// params supplies the concrete path parameters this case's call passes.
	// Cases that leave it nil use defaultParams.
	params map[string]string
}

// defaultParams covers the cases whose call passes an account id (the common
// shape); a case overrides them when it targets another resource.
var defaultParams = map[string]string{"id": idAcct, "termId": idTerm}

// expand substitutes the case's path parameters into the expected path. The
// placeholder names match the ones in backend/openapi.yaml, so the same string
// serves both the spec comparison and the request assertion. A placeholder left
// unsubstituted is a test bug, not a pass.
func (r routeCase) expand(t *testing.T) string {
	t.Helper()
	params := r.params
	if params == nil {
		params = defaultParams
	}
	out := r.path
	for name, value := range params {
		out = strings.ReplaceAll(out, ":"+name, value)
	}
	if strings.Contains(out, ":") {
		t.Fatalf("route case %q leaves a path parameter unsubstituted: %s", r.name, out)
	}
	return out
}

func userJSON() string { return `{"id":"u1","email":"a@b.c","role":"admin"}` }

func txPageJSON() string {
	return `{"data":[],"total":0,"page":1,"limit":50,"pages":0}`
}

func routeCases() []routeCase {
	okMessage := `{"message":"ok"}`
	updated := `{"updated":2}`
	deleted := `{"deleted":2}`
	parseResult := `{"transactions":[],"summary":{},"pageCount":1,"transactionCount":0,"validationErrors":[]}`
	term := `{"id":"term-1","seriesId":"s1","amount":10,"accountId":"acct-1"}`
	group := `{"id":"g1","name":"G"}`
	category := `{"id":"c1","name":"C","groupId":"expense"}`
	account := `{"id":"acct-1","name":"Everyday","accountTypeId":"bank"}`
	payee := `{"id":"p1","name":"P"}`
	link := `{"id":"l1","type":"transfer","fromTxnId":"t1","toTxnId":"t2"}`
	rule := `{"id":"r1","pattern":"x","matchType":"contains","categoryId":"c1","priority":1,"addTags":[]}`
	rec := `{"id":"s1","name":"S"}`
	settings := `{"paperlessUrl":"","hasToken":false,"paperlessTag":"","pageSize":null}`
	sugg := `{"data":[],"page":1,"limit":50,"hasMore":false}`
	dataList := `{"data":[]}`
	schedule := `{"schedule":null,"lastPaidDate":"2026-01-05T00:00:00Z","entries":[]}`
	payoff := `{"loanAccountName":"Car loan","asOf":"2026-02-01T00:00:00Z","fromDate":"2026-01-05T00:00:00Z",` +
		`"days":27,"outstandingPrincipal":1956632.46,"accruedInterest":26292.25,"payoff":1982924.71}`

	return []routeCase{
		// System.
		{name: "health", method: "GET", path: "/health", body: `{"status":"ok"}`,
			call: func(ctx context.Context, c *Client) error { _, err := c.Health(ctx); return err }},
		{name: "openapi spec", method: "GET", path: "/openapi.yaml", body: "openapi: 3.0.3\n",
			call: func(ctx context.Context, c *Client) error { _, err := c.OpenAPISpec(ctx); return err }},

		// Auth.
		{name: "login", method: "POST", path: "/auth/login", body: `{"user":` + userJSON() + `}`,
			cookies: []*http.Cookie{{Name: AccessCookieName, Value: "a1"}, {Name: RefreshCookieName, Value: "r1"}},
			call:    func(ctx context.Context, c *Client) error { _, err := c.Login(ctx, "a@b.c", "pw"); return err }},
		{name: "register", method: "POST", path: "/auth/register", body: `{"user":` + userJSON() + `}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.Register(ctx, "a@b.c", "pw", "token")
				return err
			}},
		{name: "refresh", method: "POST", path: "/auth/refresh", body: `{"message":"token refreshed"}`,
			cookies: []*http.Cookie{{Name: AccessCookieName, Value: "a2"}},
			call:    func(ctx context.Context, c *Client) error { return c.RefreshSession(ctx) }},
		{name: "logout", method: "POST", path: "/auth/logout", body: `{"message":"logged out"}`,
			call: func(ctx context.Context, c *Client) error { return c.Logout(ctx) }},
		{name: "me", method: "GET", path: "/auth/me", body: userJSON(),
			call: func(ctx context.Context, c *Client) error { _, err := c.Me(ctx); return err }},

		// Accounts, account types, billing cycles, loan schedules.
		{name: "list accounts", method: "GET", path: "/accounts", body: `[]`,
			call: func(ctx context.Context, c *Client) error { _, err := c.ListAccounts(ctx); return err }},
		{name: "create account", method: "POST", path: "/accounts", body: account,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.CreateAccount(ctx, CreateAccountRequest{Name: "n", AccountTypeID: "bank"})
				return err
			}},
		{name: "update account", method: "PUT", path: "/accounts/:id", body: account,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.UpdateAccount(ctx, idAcct, UpdateAccountRequest{Name: "n"})
				return err
			}},
		{name: "delete account", method: "DELETE", path: "/accounts/:id", body: `{"message":"deleted","transactionsDeleted":3}`,
			call: func(ctx context.Context, c *Client) error { _, err := c.DeleteAccount(ctx, idAcct); return err }},
		{name: "account csv export", method: "GET", path: "/accounts/:id/export", body: "Date,Description\n",
			call: func(ctx context.Context, c *Client) error {
				_, err := c.ExportAccountCSV(ctx, idAcct, io.Discard)
				return err
			}},
		{name: "billing cycles", method: "GET", path: "/accounts/:id/billing-cycles", body: dataList,
			call: func(ctx context.Context, c *Client) error { _, err := c.ListBillingCycles(ctx, idAcct); return err }},
		{name: "loan schedule get", method: "GET", path: "/accounts/:id/loan-schedule", body: schedule,
			call: func(ctx context.Context, c *Client) error { _, err := c.LoanSchedule(ctx, idAcct); return err }},
		{name: "loan schedule put", method: "PUT", path: "/accounts/:id/loan-schedule", body: schedule,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.SetLoanSchedule(ctx, idAcct, LoanScheduleRequest{Principal: "1000.00", TenureMonths: 12, StartDate: "2026-01-01"})
				return err
			}},
		{name: "loan schedule delete", method: "DELETE", path: "/accounts/:id/loan-schedule", body: `{"deleted":1}`,
			call: func(ctx context.Context, c *Client) error { _, err := c.DeleteLoanSchedule(ctx, idAcct); return err }},
		{name: "loan payoff", method: "GET", path: "/accounts/:id/loan-payoff", body: payoff,
			query: map[string]string{"date": "2026-02-01"},
			call: func(ctx context.Context, c *Client) error {
				_, err := c.LoanPayoff(ctx, idAcct, "2026-02-01")
				return err
			}},
		{name: "loan balance transfer", method: "POST", path: "/accounts/:id/loan-transfer",
			body: `{"transfer":{"id":"tr1","fromLoanAccountId":"acct-1","toLoanAccountId":"acct-2","amount":1000.00,` +
				`"principal":900.00,"accruedInterest":100.00,` +
				`"transferDate":"2026-01-05T00:00:00Z","createdAt":"2026-01-05T00:00:00Z"},"source":` + schedule + `,"target":` + schedule + `}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.TransferLoanBalance(ctx, idAcct, LoanTransferRequest{
					ToLoanAccountID: "acct-2", TransferDate: "2026-01-05",
				})
				return err
			}},
		{name: "loan transfer delete", method: "DELETE", path: "/accounts/:id/loan-transfer/:transferId",
			params: map[string]string{"id": idAcct, "transferId": "tr1"}, body: `{"deleted":1}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.DeleteLoanTransfer(ctx, idAcct, "tr1")
				return err
			}},
		{name: "loan disbursement link", method: "PUT", path: "/accounts/:id/loan-disbursement", body: schedule,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.LinkLoanDisbursement(ctx, idAcct, idTxn)
				return err
			}},
		{name: "loan disbursement unlink", method: "DELETE", path: "/accounts/:id/loan-disbursement",
			body: `{"deleted":1}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.UnlinkLoanDisbursement(ctx, idAcct)
				return err
			}},
		{name: "list account types", method: "GET", path: "/account-types", body: `[]`,
			call: func(ctx context.Context, c *Client) error { _, err := c.ListAccountTypes(ctx); return err }},
		{name: "create account type", method: "POST", path: "/account-types", body: `{"id":"x","name":"X","positiveTxnType":"debit"}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.CreateAccountType(ctx, CreateAccountTypeRequest{ID: "x", Name: "X", PositiveTxnType: "debit"})
				return err
			}},
		{name: "update account type", method: "PUT", path: "/account-types/:id", params: map[string]string{"id": "x"}, body: `{"id":"x","name":"X","positiveTxnType":"debit"}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.UpdateAccountType(ctx, "x", UpdateAccountTypeRequest{Name: "X"})
				return err
			}},
		{name: "delete account type", method: "DELETE", path: "/account-types/:id", params: map[string]string{"id": "x"}, body: okMessage,
			call: func(ctx context.Context, c *Client) error { return c.DeleteAccountType(ctx, "x") }},

		// Reference data: groups, categories, admin catalog, payees, tags.
		{name: "list groups", method: "GET", path: "/groups", body: `[]`,
			call: func(ctx context.Context, c *Client) error { _, err := c.ListGroups(ctx); return err }},
		{name: "create group", method: "POST", path: "/groups", body: group,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.CreateGroup(ctx, CreateCategoryGroupRequest{ID: "g1", Name: "G"})
				return err
			}},
		{name: "update group", method: "PUT", path: "/groups/:id", params: map[string]string{"id": "g1"}, body: group,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.UpdateGroup(ctx, "g1", UpdateCategoryGroupRequest{Name: "G"})
				return err
			}},
		{name: "delete group", method: "DELETE", path: "/groups/:id", params: map[string]string{"id": "g1"}, body: okMessage,
			call: func(ctx context.Context, c *Client) error { return c.DeleteGroup(ctx, "g1") }},
		{name: "list categories", method: "GET", path: "/categories", body: `[]`,
			call: func(ctx context.Context, c *Client) error { _, err := c.ListCategories(ctx); return err }},
		{name: "create category", method: "POST", path: "/categories", body: category,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.CreateCategory(ctx, CreateCategoryRequest{Name: "C", GroupID: "expense"})
				return err
			}},
		{name: "update category", method: "PUT", path: "/categories/:id", params: map[string]string{"id": "c1"}, body: category,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.UpdateCategory(ctx, "c1", UpdateCategoryRequest{Name: "C"})
				return err
			}},
		{name: "delete category", method: "DELETE", path: "/categories/:id", params: map[string]string{"id": "c1"}, body: `{"clearedTransactions":0,"deletedRules":0}`,
			call: func(ctx context.Context, c *Client) error { _, err := c.DeleteCategory(ctx, "c1"); return err }},
		{name: "admin catalog", method: "GET", path: "/admin/catalog", body: `{"groups":[],"categories":[]}`,
			call: func(ctx context.Context, c *Client) error { _, err := c.AdminCatalog(ctx); return err }},
		{name: "admin create group", method: "POST", path: "/admin/groups", body: group,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.CreateGlobalGroup(ctx, CreateCategoryGroupRequest{ID: "g1", Name: "G"})
				return err
			}},
		{name: "admin create category", method: "POST", path: "/admin/categories", body: category,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.CreateGlobalCategory(ctx, CreateCategoryRequest{Name: "C", GroupID: "expense"})
				return err
			}},
		{name: "admin update category", method: "PUT", path: "/admin/categories/:id", params: map[string]string{"id": "c1"}, body: category,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.UpdateGlobalCategory(ctx, "c1", UpdateCategoryRequest{Name: "C"})
				return err
			}},
		{name: "admin delete category", method: "DELETE", path: "/admin/categories/:id", params: map[string]string{"id": "c1"}, body: `{"clearedTransactions":0,"deletedRules":0}`,
			call: func(ctx context.Context, c *Client) error { _, err := c.DeleteGlobalCategory(ctx, "c1"); return err }},
		{name: "list payees", method: "GET", path: "/payees", body: `[]`,
			call: func(ctx context.Context, c *Client) error { _, err := c.ListPayees(ctx); return err }},
		{name: "create payee", method: "POST", path: "/payees", body: payee,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.CreatePayee(ctx, CreatePayeeRequest{Name: "P"})
				return err
			}},
		{name: "update payee", method: "PUT", path: "/payees/:id", params: map[string]string{"id": "p1"}, body: payee,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.UpdatePayee(ctx, "p1", CreatePayeeRequest{Name: "P"})
				return err
			}},
		{name: "delete payee", method: "DELETE", path: "/payees/:id", params: map[string]string{"id": "p1"}, body: okMessage,
			call: func(ctx context.Context, c *Client) error { return c.DeletePayee(ctx, "p1") }},
		{name: "list tags", method: "GET", path: "/tags", body: dataList,
			call: func(ctx context.Context, c *Client) error { _, err := c.ListTags(ctx); return err }},
		{name: "rename tag", method: "POST", path: "/tags/rename", body: `{"updated":3}`,
			call: func(ctx context.Context, c *Client) error { _, err := c.RenameTag(ctx, "old", "new"); return err }},

		// Transactions.
		{name: "list transactions", method: "GET", path: "/transactions", body: txPageJSON(),
			query: map[string]string{"search": "coffee", "page": "2", "linked": "false"},
			call: func(ctx context.Context, c *Client) error {
				_, err := c.ListTransactions(ctx, TransactionFilter{
					Search: "coffee", Page: 2, Linked: new(false),
				})
				return err
			}},
		{name: "create transaction", method: "POST", path: "/transactions", body: `{"id":"txn-1"}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.CreateTransaction(ctx, CreateTransactionRequest{
					AccountID: idAcct, Date: "2026-01-01", Description: "d", Amount: "10.00", Type: "debit",
				})
				return err
			}},
		{name: "update transaction", method: "PATCH", path: "/transactions/:id", params: map[string]string{"id": idTxn}, body: `{"message":"updated"}`,
			call: func(ctx context.Context, c *Client) error {
				return c.UpdateTransaction(ctx, idTxn, UpdateTransactionRequest{CategoryID: UUIDNull()})
			}},
		{name: "delete transaction", method: "DELETE", path: "/transactions/:id", params: map[string]string{"id": idTxn}, body: `{"message":"deleted"}`,
			call: func(ctx context.Context, c *Client) error { return c.DeleteTransaction(ctx, idTxn) }},
		{name: "import transactions", method: "POST", path: "/transactions/import", body: `{"imported":1,"duplicates":0,"total":1}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.ImportTransactions(ctx, ImportRequest{AccountID: idAcct})
				return err
			}},
		{name: "validate transactions", method: "POST", path: "/transactions/validate", body: `{"total":0,"existingCount":0,"missingCount":0,"results":[]}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.ValidateTransactions(ctx, ValidateTransactionsRequest{AccountID: idAcct})
				return err
			}},
		{name: "bulk categorize", method: "POST", path: "/transactions/bulk-categorize", body: updated,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.BulkCategorize(ctx, []string{idTxn}, "c1")
				return err
			}},
		{name: "bulk payee", method: "POST", path: "/transactions/bulk-payee", body: updated,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.BulkPayee(ctx, []string{idTxn}, "p1")
				return err
			}},
		{name: "bulk billing cycle", method: "POST", path: "/transactions/bulk-billing-cycle", body: updated,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.BulkBillingCycle(ctx, []string{idTxn}, "bc1")
				return err
			}},
		{name: "bulk tags", method: "POST", path: "/transactions/bulk-tags", body: updated,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.BulkTags(ctx, []string{idTxn}, []string{"a"}, []string{"b"})
				return err
			}},
		{name: "bulk delete", method: "POST", path: "/transactions/bulk-delete", body: deleted,
			call: func(ctx context.Context, c *Client) error { _, err := c.BulkDelete(ctx, []string{idTxn}); return err }},
		{name: "bulk loan attach", method: "POST", path: "/transactions/bulk-loan", body: `{"attached":2}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.BulkAttachLoan(ctx, []string{idTxn}, idAcct)
				return err
			}},
		{name: "bulk loan detach", method: "POST", path: "/transactions/bulk-loan", body: `{"detached":2}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.BulkDetachLoan(ctx, []string{idTxn})
				return err
			}},
		{name: "transaction csv export", method: "GET", path: "/transactions/export", body: "Date,Description\n",
			query: map[string]string{"accountId": idAcct},
			call: func(ctx context.Context, c *Client) error {
				_, err := c.ExportTransactionsCSV(ctx, TransactionFilter{AccountID: idAcct}, io.Discard)
				return err
			}},

		// Rules.
		{name: "list rules", method: "GET", path: "/rules", body: `[]`,
			call: func(ctx context.Context, c *Client) error { _, err := c.ListRules(ctx); return err }},
		{name: "create rule", method: "POST", path: "/rules", body: rule,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.CreateRule(ctx, CreateRuleRequest{Pattern: "x", CategoryID: "c1"})
				return err
			}},
		{name: "update rule", method: "PUT", path: "/rules/:id", params: map[string]string{"id": "r1"}, body: rule,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.UpdateRule(ctx, "r1", UpdateRuleRequest{Pattern: "x", CategoryID: "c1"})
				return err
			}},
		{name: "delete rule", method: "DELETE", path: "/rules/:id", params: map[string]string{"id": "r1"}, body: okMessage,
			call: func(ctx context.Context, c *Client) error { return c.DeleteRule(ctx, "r1") }},
		{name: "apply rules", method: "POST", path: "/rules/apply", body: `{"updated":5}`,
			call: func(ctx context.Context, c *Client) error { _, err := c.ApplyRules(ctx); return err }},
		{name: "preview rule", method: "POST", path: "/rules/preview", body: `{"matched":3}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.PreviewRule(ctx, CreateRuleRequest{Pattern: "x", CategoryID: "c1"})
				return err
			}},

		// Links.
		{name: "list links", method: "GET", path: "/links", body: `[]`,
			query: map[string]string{"type": "transfer", "txnId": idTxn},
			call:  func(ctx context.Context, c *Client) error { _, err := c.ListLinks(ctx, "transfer", idTxn); return err }},
		{name: "create link", method: "POST", path: "/links", body: link,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.CreateLink(ctx, CreateLinkRequest{Type: "transfer", FromTxnID: "t1", ToTxnID: "t2"})
				return err
			}},
		{name: "bulk create links", method: "POST", path: "/links/bulk", body: `{"createdCount":1}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.BulkCreateLinks(ctx, []CreateLinkRequest{{Type: "transfer", FromTxnID: "t1", ToTxnID: "t2"}})
				return err
			}},
		{name: "delete link", method: "DELETE", path: "/links/:id", params: map[string]string{"id": "l1"}, body: okMessage,
			call: func(ctx context.Context, c *Client) error { return c.DeleteLink(ctx, "l1") }},
		{name: "bulk delete links", method: "POST", path: "/links/bulk-delete", body: `{"message":"deleted","deletedCount":2}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.BulkDeleteLinks(ctx, []string{"l1"})
				return err
			}},
		{name: "transfer suggestions", method: "GET", path: "/links/transfer-suggestions", body: sugg,
			query: map[string]string{"page": "1", "limit": "50"},
			call:  func(ctx context.Context, c *Client) error { _, err := c.TransferSuggestions(ctx, 1, 50); return err }},
		{name: "cashback suggestions", method: "GET", path: "/links/cashback-suggestions", body: sugg,
			query: map[string]string{"page": "1", "limit": "50"},
			call:  func(ctx context.Context, c *Client) error { _, err := c.CashbackSuggestions(ctx, 1, 50); return err }},
		{name: "link cycles", method: "GET", path: "/links/cycles", body: `{"cycles":[],"totalCircular":0,"oneSidedFlows":[]}`,
			query: map[string]string{"accountId": idAcct},
			call:  func(ctx context.Context, c *Client) error { _, err := c.LinkCycles(ctx, "", "", idAcct); return err }},

		// Recurring.
		{name: "list recurring", method: "GET", path: "/recurring", body: dataList,
			call: func(ctx context.Context, c *Client) error { _, err := c.ListRecurring(ctx); return err }},
		{name: "create recurring", method: "POST", path: "/recurring", body: rec,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.CreateRecurring(ctx, CreateRecurringSeriesRequest{Name: "S", Type: "debit", Frequency: "monthly"})
				return err
			}},
		{name: "update recurring", method: "PUT", path: "/recurring/:id", params: map[string]string{"id": "s1"}, body: rec,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.UpdateRecurring(ctx, "s1", UpdateRecurringSeriesRequest{Name: new("S")})
				return err
			}},
		{name: "delete recurring", method: "DELETE", path: "/recurring/:id", params: map[string]string{"id": "s1"}, body: okMessage,
			call: func(ctx context.Context, c *Client) error { return c.DeleteRecurring(ctx, "s1") }},
		{name: "recurring forecast", method: "GET", path: "/recurring/:id/forecast", params: map[string]string{"id": "s1"}, body: dataList,
			query: map[string]string{"count": "6"},
			call:  func(ctx context.Context, c *Client) error { _, err := c.RecurringForecast(ctx, "s1", 6); return err }},
		{name: "recurring suggestions", method: "GET", path: "/recurring/:id/suggestions", params: map[string]string{"id": "s1"}, body: dataList,
			query: map[string]string{"limit": "25"},
			call: func(ctx context.Context, c *Client) error {
				_, err := c.RecurringSuggestions(ctx, "s1", 25)
				return err
			}},
		{name: "recurring transactions", method: "GET", path: "/recurring/:id/transactions", params: map[string]string{"id": "s1"}, body: dataList,
			call: func(ctx context.Context, c *Client) error { _, err := c.RecurringTransactions(ctx, "s1"); return err }},
		{name: "list recurring terms", method: "GET", path: "/recurring/:id/terms", params: map[string]string{"id": "s1"}, body: dataList,
			call: func(ctx context.Context, c *Client) error { _, err := c.ListRecurringTerms(ctx, "s1"); return err }},
		{name: "add recurring term", method: "PUT", path: "/recurring/:id/terms", params: map[string]string{"id": "s1"}, body: term,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.AddRecurringTerm(ctx, "s1", CreateRecurringSeriesTermRequest{StartDate: "2026-01-01", Amount: "10.00", AccountID: idAcct})
				return err
			}},
		{name: "update recurring term", method: "PUT", path: "/recurring/:id/terms/:termId", params: map[string]string{"id": "s1", "termId": idTerm}, body: term,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.UpdateRecurringTerm(ctx, "s1", idTerm, UpdateRecurringSeriesTermRequest{Amount: new(Amount("12.00"))})
				return err
			}},
		{name: "delete recurring term", method: "DELETE", path: "/recurring/:id/terms/:termId", params: map[string]string{"id": "s1", "termId": idTerm}, body: okMessage,
			call: func(ctx context.Context, c *Client) error { return c.DeleteRecurringTerm(ctx, "s1", idTerm) }},
		{name: "attach recurring", method: "POST", path: "/recurring/attach", body: `{"attached":2}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.AttachRecurring(ctx, "s1", []string{idTxn})
				return err
			}},
		{name: "detach recurring", method: "POST", path: "/recurring/detach", body: `{"detached":2}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.DetachRecurring(ctx, []string{idTxn})
				return err
			}},

		// Dashboard.
		{name: "dashboard summary", method: "GET", path: "/dashboard/summary", body: `{}`,
			query: map[string]string{"accountId": idAcct, "groupBy": "billing_cycle", "cycles": "6"},
			call: func(ctx context.Context, c *Client) error {
				_, err := c.Summary(ctx, DashboardFilter{
					WindowFilter: WindowFilter{AccountID: idAcct}, GroupBy: "billing_cycle", Cycles: 6,
				})
				return err
			}},
		{name: "money flow", method: "GET", path: "/dashboard/money-flow", body: `{"nodes":[],"links":[],"linkSummary":[]}`,
			query: map[string]string{"limit": "15"},
			call: func(ctx context.Context, c *Client) error {
				_, err := c.MoneyFlow(ctx, MoneyFlowFilter{Limit: 15})
				return err
			}},
		{name: "money flow timeline", method: "GET", path: "/dashboard/money-flow/timeline", body: `{"groupBy":"month","periods":[]}`,
			query: map[string]string{"groupBy": "billing_cycle", "cycles": "12"},
			call: func(ctx context.Context, c *Client) error {
				_, err := c.MoneyFlowTimeline(ctx, TimelineFilter{GroupBy: "billing_cycle", Cycles: 12})
				return err
			}},
		{name: "cash flow calendar", method: "GET", path: "/dashboard/cash-flow-calendar", body: `{"days":[],"markers":[],"cycles":[]}`,
			query: map[string]string{"dateFrom": "2026-01-01"},
			call: func(ctx context.Context, c *Client) error {
				_, err := c.CashFlowCalendar(ctx, WindowFilter{DateFrom: "2026-01-01"})
				return err
			}},

		// Statements, Paperless, backup.
		{name: "parse statement", method: "POST", path: "/statements/parse", body: parseResult,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.ParseStatement(ctx, "statement.pdf", []byte("%PDF-1.4"), "", "sbi_cc", "")
				return err
			}},
		{name: "statement extractors", method: "GET", path: "/statements/extractors", body: `{"extractors":[]}`,
			call: func(ctx context.Context, c *Client) error { _, err := c.ListStatementExtractors(ctx); return err }},
		{name: "paperless settings get", method: "GET", path: "/paperless/settings", body: settings,
			call: func(ctx context.Context, c *Client) error { _, err := c.PaperlessSettings(ctx); return err }},
		{name: "paperless settings put", method: "PUT", path: "/paperless/settings", body: settings,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.UpdatePaperlessSettings(ctx, UpdateUserSettingsRequest{PaperlessURL: new("http://p")})
				return err
			}},
		{name: "paperless documents", method: "GET", path: "/paperless/documents", body: `{"documents":[],"page":1,"pageSize":25,"totalCount":0,"totalPages":0,"correspondents":[],"documentTypes":[],"tags":[]}`,
			query: map[string]string{"page": "2", "pageSize": "10", "search": "jan", "tagInc": "bills"},
			call: func(ctx context.Context, c *Client) error {
				_, err := c.ListPaperlessDocuments(ctx, PaperlessQuery{Page: 2, PageSize: 10, Search: "jan", TagInc: []string{"bills"}})
				return err
			}},
		{name: "paperless document file", method: "GET", path: "/paperless/documents/:id/file", params: map[string]string{"id": idFile}, body: "%PDF-1.4",
			call: func(ctx context.Context, c *Client) error {
				_, err := c.PaperlessDocumentFile(ctx, 42, io.Discard)
				return err
			}},
		{name: "paperless import", method: "POST", path: "/paperless/import", body: parseResult,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.ImportPaperlessDocument(ctx, PaperlessImportRequest{DocumentID: 42})
				return err
			}},
		{name: "backup export", method: "GET", path: "/export", body: `{"format":"fintrak.backup","version":1}`,
			call: func(ctx context.Context, c *Client) error { _, err := c.ExportBackup(ctx, io.Discard); return err }},
		{name: "backup import", method: "POST", path: "/import", body: `{"accounts":1,"transactions":2}`,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.ImportBackup(ctx, []byte(`{"format":"fintrak.backup","version":1}`))
				return err
			}},
	}
}

// TestRouteTableMatchesTheSpec keeps the client's declared coverage and the
// backend's documented surface in lockstep, in both directions.
func TestRouteTableMatchesTheSpec(t *testing.T) {
	spec := specRoutes(t)
	table := map[string]bool{}
	for _, tc := range routeCases() {
		table[tc.method+" "+tc.path] = true
	}

	for key := range table {
		if !spec[key] {
			t.Errorf("the client claims %s, which backend/openapi.yaml does not document", key)
		}
	}
	for key := range spec {
		if !table[key] {
			t.Errorf("backend/openapi.yaml documents %s, which the TUI client does not cover", key)
		}
	}
}

// TestEveryRouteHitsItsDocumentedPath executes every entry against a stub server
// and asserts the request it produced: method, path and any pinned query
// parameters. It is what makes the path strings inside the client methods -
// which reflection cannot reach - verifiable.
func TestEveryRouteHitsItsDocumentedPath(t *testing.T) {
	for _, tc := range routeCases() {
		t.Run(tc.name, func(t *testing.T) {
			var (
				gotMethod string
				gotPath   string
				gotQuery  map[string]string
				calls     int
			)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				gotMethod, gotPath = r.Method, r.URL.Path
				gotQuery = map[string]string{}
				for key, values := range r.URL.Query() {
					gotQuery[key] = values[0]
				}
				for _, ck := range tc.cookies {
					http.SetCookie(w, ck)
				}
				if strings.HasPrefix(tc.body, "{") || strings.HasPrefix(tc.body, "[") {
					w.Header().Set("Content-Type", "application/json")
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c, err := New(srv.URL + "/api/v1")
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			c.SetTokens("access-1", "refresh-1")

			if err := tc.call(context.Background(), c); err != nil {
				t.Fatalf("%s %s: %v", tc.method, tc.path, err)
			}
			if calls != 1 {
				t.Errorf("made %d requests, want exactly 1 (a 401 retry or a duplicate call is a bug)", calls)
			}
			if gotMethod != tc.method {
				t.Errorf("method = %s, want %s", gotMethod, tc.method)
			}
			if want := "/api/v1" + tc.expand(t); gotPath != want {
				t.Errorf("path = %s, want %s", gotPath, want)
			}
			// The whole query set is compared, not just the pinned keys: a
			// filter that was renamed on either side, or a parameter that
			// stopped being sent, has to fail here rather than silently
			// narrowing the result set.
			if len(gotQuery) != len(tc.query) {
				t.Errorf("query = %v, want exactly %v", gotQuery, tc.query)
			}
			for key, want := range tc.query {
				if got := gotQuery[key]; got != want {
					t.Errorf("query %s = %q, want %q", key, got, want)
				}
			}
		})
	}
}

// TestRouteTableHasNoDuplicateNames keeps subtest names unique, since the runner
// would silently collapse duplicates.
func TestRouteTableHasNoDuplicateNames(t *testing.T) {
	seen := map[string]bool{}
	for _, tc := range routeCases() {
		if seen[tc.name] {
			t.Errorf("duplicate route case name %q", tc.name)
		}
		seen[tc.name] = true
	}
}

// specRoutes parses backend/openapi.yaml into a set of "METHOD /path" keys,
// normalizing the spec's {param} syntax to the gin :param form the table uses.
func specRoutes(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("reading %s: %v", specPath, err)
	}

	var spec struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parsing %s: %v", specPath, err)
	}
	if len(spec.Paths) == 0 {
		t.Fatalf("%s has no paths", specPath)
	}

	verbs := map[string]bool{
		"get": true, "post": true, "put": true, "patch": true, "delete": true,
	}
	param := regexp.MustCompile(`\{([^}]+)\}`)

	routes := map[string]bool{}
	for path, operations := range spec.Paths {
		normalized := param.ReplaceAllString(path, ":$1")
		for verb := range operations {
			if !verbs[strings.ToLower(verb)] {
				continue
			}
			routes[strings.ToUpper(verb)+" "+normalized] = true
		}
	}
	return routes
}

// schemaAlias maps a client type to the components.schemas entry that documents
// its wire shape under a different name.
var schemaAlias = map[string]string{
	"StatementParseResult":        "ParseStatementResult",
	"TransactionPage":             "TransactionListResponse",
	"ImportResult":                "ImportResponse",
	"LinkLoanDisbursementRequest": "LoanDisbursementRequest",
	"MessageResult":               "MessageResponse",
	"CreateCategoryRequest":       "Category",
	"UpdateCategoryRequest":       "Category",
}

// schemaSubset lists the client types whose schema publishes more than the
// client carries, because the spec documents the request body with the response
// schema (POST /categories reuses Category, which also has the read-only id and
// group fields). Only the client's own tags are checked for these.
var schemaSubset = map[string]bool{
	"CreateCategoryRequest": true,
	"UpdateCategoryRequest": true,
}

// noSpecSchema lists the client types the spec documents inline in a path
// rather than as a components.schemas entry, with where. They have no property
// list to compare against, so they are exempt by name: a type that is in
// neither this map nor the spec fails the test, which is what stops a rename
// from quietly dropping it out of the comparison.
var noSpecSchema = map[string]string{
	"CategoryGroup":              "GET /groups documents the object inline",
	"CreateCategoryGroupRequest": "POST /groups documents the body inline",
	"UpdateCategoryGroupRequest": "PUT /groups/{id} documents the body inline",
	"AdminCatalog":               "GET /admin/catalog documents the object inline",
	"AdminCatalogGroup":          "GET /admin/catalog documents the object inline",
	"AdminCatalogCategory":       "GET /admin/catalog documents the object inline",
	"RulePreview":                "POST /rules/preview documents the object inline",
	"PaperlessDocumentsResponse": "GET /paperless/documents documents the object inline",
	"StatementExtractor":         "GET /statements/extractors documents the object inline",
	"ExtractorsResponse":         "GET /statements/extractors documents the object inline",
	"HealthResult":               "GET /health documents the object inline",
	"SuggestionPage":             "GET /links/transfer-suggestions documents the object inline",
	"DataList":                   "the list endpoints document each wrapper inline",

	// Client-side acknowledgements: every route below documents its response
	// inline as a one-key object rather than as a named schema.
	"UpdatedResult":            "the bulk routes document {updated} inline",
	"DeletedResult":            "the bulk delete routes document {deleted} inline",
	"AttachedResult":           "the bulk attach routes document {attached} inline",
	"DetachedResult":           "the bulk detach routes document {detached} inline",
	"CreatedCountResult":       "POST /links/bulk documents {createdCount} inline",
	"DeletedCountResult":       "POST /links/bulk-delete documents its object inline",
	"AccountDeleteResult":      "DELETE /accounts/{id} documents its object inline",
	"IDResult":                 "POST /transactions documents {id} inline",
	"DeleteCategoryResult":     "DELETE /categories/{id} documents its object inline",
	"DeleteLoanScheduleResult": "DELETE /accounts/{id}/loan-schedule documents its object inline",
}

// TestClientTypesMatchTheSpecSchemas closes the half of the drift claim the
// route table cannot reach: the table proves the client covers every route, but
// the bodies those routes return are decoded into the structs in types.go, and
// a mistyped or renamed json tag there decodes as an empty field with a green
// suite. Every struct declared in types.go is therefore compared against the
// components.schemas entry that documents its wire shape — the client must not
// carry a tag the spec does not publish, and, unless the type is a subset, the
// spec must not publish a property the client cannot decode. That comparison is
// what found BackupImportResult missing the loanTransfers and loanDisbursements
// counters.
//
// Type aliases are not compared: they carry the tags of their target, which is
// compared itself.
func TestClientTypesMatchTheSpecSchemas(t *testing.T) {
	schemas := specSchemas(t)
	tags := clientJSONTags(t)

	for name, clientTags := range tags {
		schema, ok := schemaForClientType(name)
		if !ok {
			if _, exempt := noSpecSchema[name]; !exempt {
				t.Errorf("client type %s is neither compared against a components.schemas entry nor listed in noSpecSchema; add it to one of them", name)
			}
			continue
		}
		props, ok := schemas[schema]
		if !ok {
			t.Errorf("client type %s is compared against components.schemas.%s, which the spec does not define", name, schema)
			continue
		}

		for _, tag := range clientTags {
			if !props[tag] {
				t.Errorf("%s: field tag %q is not a property of components.schemas.%s", name, tag, schema)
			}
		}
		if schemaSubset[name] {
			continue
		}
		for prop := range props {
			if !slices.Contains(clientTags, prop) {
				t.Errorf("%s: components.schemas.%s publishes %q, which the client cannot decode", name, schema, prop)
			}
		}
	}

	// A stale exemption hides a real comparison: if the type no longer exists,
	// the entry is dead weight that would let a rename through.
	for name := range noSpecSchema {
		if _, ok := tags[name]; !ok {
			t.Errorf("noSpecSchema still lists %s, which is no longer a struct in types.go", name)
		}
	}
}

// schemaForClientType resolves a client type to the components.schemas entry
// that documents it, preferring the identical name.
func schemaForClientType(name string) (string, bool) {
	if alias, ok := schemaAlias[name]; ok {
		return alias, true
	}
	if _, exempt := noSpecSchema[name]; exempt {
		return "", false
	}
	return name, true
}

// specSchemas parses backend/openapi.yaml into components.schemas reduced to
// the set of property names each entry publishes.
func specSchemas(t *testing.T) map[string]map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("reading %s: %v", specPath, err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]yaml.Node `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parsing %s: %v", specPath, err)
	}
	if len(spec.Components.Schemas) == 0 {
		t.Fatalf("%s has no components.schemas", specPath)
	}

	schemas := make(map[string]map[string]bool, len(spec.Components.Schemas))
	for name, schema := range spec.Components.Schemas {
		props := make(map[string]bool, len(schema.Properties))
		for prop := range schema.Properties {
			props[prop] = true
		}
		schemas[name] = props
	}
	return schemas
}

// clientJSONTags reads the json tag of every field of every struct declared in
// types.go, keyed by type name. Parsing the declaration rather than reflecting
// over values keeps it exhaustive: a type the tests never construct is covered
// too.
func clientJSONTags(t *testing.T) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "types.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing types.go: %v", err)
	}

	tags := map[string][]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			if _, seen := tags[ts.Name.Name]; !seen {
				tags[ts.Name.Name] = nil // an untagged struct still needs a decision
			}
			for _, field := range st.Fields.List {
				if field.Tag == nil {
					continue
				}
				tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`"))
				name := strings.Split(tag.Get("json"), ",")[0]
				if name == "" || name == "-" {
					continue
				}
				tags[ts.Name.Name] = append(tags[ts.Name.Name], name)
			}
		}
	}
	if len(tags) == 0 {
		t.Fatal("types.go declares no structs")
	}
	return tags
}
