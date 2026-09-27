//go:build integration

// Package main integration tests boot the real Gin router against a throwaway
// PostgreSQL container so SQL that pgxmock cannot validate (array bindings,
// constraints, migrations, correlated subqueries) is exercised end to end.
//
// Run with: go test -tags=integration ./...
package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/config"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestMain(m *testing.M) {
	os.Exit(runIntegration(m))
}

// runIntegration starts a Postgres container, applies migrations and seeders,
// wires the shared db.Pool, then runs the package's tests.
func runIntegration(m *testing.M) int {
	gin.SetMode(gin.TestMode)
	validation.Init()

	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("fintrak_test"),
		tcpostgres.WithUsername("fintrak"),
		tcpostgres.WithPassword("fintrak"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "start postgres container:", err)
		return 1
	}
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			fmt.Fprintln(os.Stderr, "terminate postgres container:", err)
		}
	}()

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintln(os.Stderr, "postgres connection string:", err)
		return 1
	}

	if err := db.Migrate(connStr); err != nil {
		fmt.Fprintln(os.Stderr, "run migrations:", err)
		return 1
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect pool:", err)
		return 1
	}
	defer pool.Close()

	db.Pool = pool
	db.SeedAccountTypes()
	db.SeedCategoryGroups()

	return m.Run()
}

// apiClient wraps an HTTP client that carries the session cookie across calls.
type apiClient struct {
	t      *testing.T
	client *http.Client
	base   string
}

func newAPIClient(t *testing.T) *apiClient {
	t.Helper()
	router := setupRouter(&config.Config{
		Port:               "0",
		AllowedOrigins:     []string{"*"},
		JWTSecret:          "integration-secret",
		ParserURL:          "http://parser.invalid",
		TokenEncryptionKey: "integration-token-key",
		Env:                "test",
		CookieSecure:       false,
	})

	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return &apiClient{t: t, client: &http.Client{Jar: jar, Timeout: 30 * time.Second}, base: ts.URL}
}

func (a *apiClient) request(method, path string, body any) (int, []byte) {
	a.t.Helper()
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		require.NoError(a.t, err)
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, a.base+path, reader)
	require.NoError(a.t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	require.NoError(a.t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(a.t, err)
	return resp.StatusCode, data
}

func (a *apiClient) call(method, path string, body any, wantStatus int, out any) {
	a.t.Helper()
	status, data := a.request(method, path, body)
	require.Equal(a.t, wantStatus, status, "unexpected status; body: %s", string(data))
	if out != nil {
		require.NoError(a.t, json.Unmarshal(data, out), "decoding %s %s: %s", method, path, string(data))
	}
}

// concurrentRequest issues a request without touching *testing.T, so it is
// safe to call from multiple goroutines (require/t.FailNow must run on the
// test goroutine).
func (a *apiClient) concurrentRequest(method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, a.base+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}

func (a *apiClient) register(email string) {
	a.t.Helper()
	a.call(http.MethodPost, "/api/v1/auth/register", map[string]string{
		"email":    email,
		"password": "integration-pass-123",
	}, http.StatusCreated, nil)
}

// rawRequest sends a request with an explicitly supplied refresh cookie over a
// jar-less client, so a test can replay a token the cookie jar has already
// replaced or cleared — exactly what a thief holding a copied cookie does.
func (a *apiClient) rawRequest(method, path, refreshToken string) (int, []byte) {
	a.t.Helper()
	req, err := http.NewRequest(method, a.base+path, nil)
	require.NoError(a.t, err)
	if refreshToken != "" {
		req.AddCookie(&http.Cookie{Name: auth.RefreshCookieName, Value: refreshToken})
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	require.NoError(a.t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(a.t, err)
	return resp.StatusCode, data
}

// refreshCookie returns the refresh cookie the client's jar currently holds, or
// "" when it holds none. The cookie is scoped to the auth endpoints, so the jar
// has to be asked about a URL inside that path.
func (a *apiClient) refreshCookie() string {
	a.t.Helper()
	u, err := url.Parse(a.base + "/api/v1/auth/refresh")
	require.NoError(a.t, err)
	for _, ck := range a.client.Jar.Cookies(u) {
		if ck.Name == auth.RefreshCookieName {
			return ck.Value
		}
	}
	return ""
}

func (a *apiClient) createAccount(name, accountType string, billingDay *int) models.Account {
	a.t.Helper()
	body := map[string]any{"name": name, "accountTypeId": accountType, "currency": "INR"}
	if billingDay != nil {
		body["billingDay"] = *billingDay
	}
	var acc models.Account
	a.call(http.MethodPost, "/api/v1/accounts", body, http.StatusCreated, &acc)
	require.NotEqual(a.t, uuid.Nil, acc.ID)
	return acc
}

// createAccountIn is createAccount with the currency chosen by the caller. The
// reporting endpoints key every amount off accounts.currency, so a test that
// exercises a second currency has to be able to name one.
func (a *apiClient) createAccountIn(name, accountType, currency string, billingDay *int) models.Account {
	a.t.Helper()
	body := map[string]any{"name": name, "accountTypeId": accountType, "currency": currency}
	if billingDay != nil {
		body["billingDay"] = *billingDay
	}
	var acc models.Account
	a.call(http.MethodPost, "/api/v1/accounts", body, http.StatusCreated, &acc)
	require.Equal(a.t, currency, acc.Currency, "the account must store the currency it was created with")
	require.NotEqual(a.t, uuid.Nil, acc.ID)
	return acc
}

// createCategory adds one user category under the given group, returning it.
// The category top-N tests need more categories than the seeded default set
// provides, and the seeded ones carry transactions of their own.
func (a *apiClient) createCategory(name, groupID string) models.Category {
	a.t.Helper()
	var cat models.Category
	a.call(http.MethodPost, "/api/v1/categories", models.CreateCategoryRequest{
		Name: name, GroupID: groupID,
	}, http.StatusCreated, &cat)
	require.NotEqual(a.t, uuid.Nil, cat.ID)
	return cat
}

// baseGroupID returns the id of a seeded base category group, which every
// user-owned category must reference.
func (a *apiClient) baseGroupID() string {
	a.t.Helper()
	var groups []models.CategoryGroup
	a.call(http.MethodGet, "/api/v1/groups", nil, http.StatusOK, &groups)
	for _, g := range groups {
		if g.IsBase && g.ID == "expense" {
			return g.ID
		}
	}
	a.t.Fatal("the seeded expense base group is missing")
	return ""
}

func (a *apiClient) categories() []models.Category {
	a.t.Helper()
	var cats []models.Category
	a.call(http.MethodGet, "/api/v1/categories", nil, http.StatusOK, &cats)
	return cats
}

// createTransaction returns the id of the created transaction. The endpoint
// responds with {"id": "..."}.
func (a *apiClient) createTransaction(accountID uuid.UUID, categoryID *uuid.UUID, date, desc string, amount float64, typ string) uuid.UUID {
	a.t.Helper()
	body := map[string]any{
		"accountId":   accountID,
		"date":        date,
		"description": desc,
		"amount":      amount,
		"type":        typ,
	}
	if categoryID != nil {
		body["categoryId"] = *categoryID
	}
	var out struct {
		ID uuid.UUID `json:"id"`
	}
	a.call(http.MethodPost, "/api/v1/transactions", body, http.StatusCreated, &out)
	require.NotEqual(a.t, uuid.Nil, out.ID)
	return out.ID
}

func (a *apiClient) transactions(accountID uuid.UUID) []models.Transaction {
	a.t.Helper()
	var out struct {
		Data  []models.Transaction `json:"data"`
		Total int                  `json:"total"`
	}
	a.call(http.MethodGet, "/api/v1/transactions?accountId="+accountID.String(), nil, http.StatusOK, &out)
	// Drop the synthetic running-balance summary rows; callers want real
	// transactions.
	real := make([]models.Transaction, 0, len(out.Data))
	for _, tx := range out.Data {
		if !tx.IsSummary {
			real = append(real, tx)
		}
	}
	return real
}

func categoryByName(t *testing.T, cats []models.Category, name string) models.Category {
	t.Helper()
	for _, c := range cats {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("category %q not found in seeded set", name)
	return models.Category{}
}

// scalarAmountFields are the JSON keys this change turned from a bare number
// into a currency-keyed object. They are listed rather than matched by suffix
// because two neighbouring fields that must stay numbers - totalAccounts and
// totalTransactions, both plain COUNT(*) - would collide with a suffix rule and
// teach the guard to pass.
var scalarAmountFields = map[string]bool{
	"total":         true,
	"totalIncome":   true,
	"totalExpense":  true,
	"totalNet":      true,
	"totalCircular": true,
	"income":        true,
	"expense":       true,
	"net":           true,
	"value":         true,
	"maxAbsNet":     true,
	"outstanding":   true,
	"gross":         true,
	"discarded":     true,
}

// requireNoScalarAmounts decodes a response and fails on any bare JSON value -
// a number above all - sitting under one of the keys above, wherever in the
// document it is. This is the assertion that outlives the rest: it does not know
// which endpoint is being walked or what a figure should be, only that a field
// this change made per-currency has stopped being a single number. A scalar
// added back at any of these names is the original bug returning, and this is
// the guard that says so without anyone having to remember to look.
//
// Values are allowed to be objects, arrays or absent; a key that is missing
// entirely is not a regression, and neither is a number the schema never
// converted (a transaction's own `amount`, a `count`).
func requireNoScalarAmounts(t *testing.T, path string, body []byte) {
	t.Helper()
	var doc any
	require.NoError(t, json.Unmarshal(body, &doc), "%s: %s", path, body)

	var violations []string
	var walk func(node any, at string)
	walk = func(node any, at string) {
		switch v := node.(type) {
		case map[string]any:
			for key, child := range v {
				here := at + "." + key
				if scalarAmountFields[key] {
					switch child.(type) {
					case map[string]any, []any:
					default:
						violations = append(violations, fmt.Sprintf("%s = %#v", here, child))
					}
				}
				walk(child, here)
			}
		case []any:
			for i, child := range v {
				walk(child, fmt.Sprintf("%s[%d]", at, i))
			}
		}
	}
	walk(doc, "$")
	sort.Strings(violations)
	require.Empty(t, violations, "%s still carries a scalar amount: %v", path, violations)
}

// insertAccountWithRawCurrency writes an account row whose accounts.currency is
// whatever the caller says, including NULL. The create endpoint cannot produce
// either state: it rewrites an empty currency to the default, and there is no
// path that stores NULL at all. So the only way to prove the reporting queries
// read both as the default currency - the premise the whole COALESCE(NULLIF(...))
// spelling rests on - is to write the rows directly.
func (a *apiClient) insertAccountWithRawCurrency(t *testing.T, userID uuid.UUID, name string, currency *string) models.Account {
	t.Helper()
	ctx := context.Background()
	var acc models.Account
	require.NoError(t, db.Pool.QueryRow(ctx,
		`INSERT INTO accounts (user_id, name, account_type_id, currency, color)
		 VALUES ($1, $2, 'bank', $3, '#06b6d4') RETURNING id, name, account_type_id`,
		userID, name, currency).Scan(&acc.ID, &acc.Name, &acc.AccountTypeID))
	return acc
}

func txnsByID(txns []models.Transaction) map[uuid.UUID]models.Transaction {
	m := make(map[uuid.UUID]models.Transaction, len(txns))
	for _, tx := range txns {
		m[tx.ID] = tx
	}
	return m
}

func TestIntegrationAuthAndSeededCategories(t *testing.T) {
	a := newAPIClient(t)
	a.register("alice@example.com")

	var me models.User
	a.call(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK, &me)
	require.Equal(t, "alice@example.com", me.Email)

	cats := a.categories()
	require.NotEmpty(t, cats, "registration should seed default categories")
	require.NotEmpty(t, categoryByName(t, cats, "Transfer"))
	require.NotEmpty(t, categoryByName(t, cats, "Groceries"))
}

// TestIntegrationCategoryCreationRunsAgainstPostgres covers the category create
// edge, which answers 500 for every user. Both create statements name the group
// id in the SELECT list and again in the EXISTS guard, so the server deduces
// the parameter as `text` in one place and `character varying` in the other and
// rejects the statement (42P08) before a row is ever inserted - so the whole
// feature is unreachable, not just an edge case. pgxmock cannot catch it: the
// mock matches a string and never asks the server to resolve parameter types.
// The guard's rejection path is asserted too, since a cast that made $5
// constant everywhere would turn an unknown group into a 500 FK violation
// instead of the 400 the client relies on.
func TestIntegrationCategoryCreationRunsAgainstPostgres(t *testing.T) {
	a := newAPIClient(t)
	a.register("creator@example.com")

	var groups []models.CategoryGroup
	a.call(http.MethodGet, "/api/v1/groups", nil, http.StatusOK, &groups)
	base := ""
	for _, g := range groups {
		if g.IsBase && g.ID == "expense" {
			base = g.ID
		}
	}
	require.NotEmpty(t, base, "the seeded base groups should be readable")

	var created models.Category
	a.call(http.MethodPost, "/api/v1/categories", models.CreateCategoryRequest{
		Name:    "Coffee",
		Icon:    "coffee",
		Color:   "#06b6d4",
		GroupID: base,
	}, http.StatusCreated, &created)
	require.NotEqual(t, uuid.Nil, created.ID)
	require.Equal(t, base, created.GroupID)

	// The new category is readable and user-owned.
	got := categoryByName(t, a.categories(), "Coffee")
	require.Equal(t, created.ID, got.ID)
	require.False(t, got.IsGlobal)

	// The EXISTS guard still rejects an unknown group as a 400, not a 500.
	a.call(http.MethodPost, "/api/v1/categories", models.CreateCategoryRequest{
		Name:    "Ghost",
		GroupID: "no-such-group",
	}, http.StatusBadRequest, nil)

	// Editing the new category takes the same group id through the same column.
	var updated models.Category
	a.call(http.MethodPut, "/api/v1/categories/"+created.ID.String(), models.UpdateCategoryRequest{
		Name:    "Coffee & Tea",
		GroupID: base,
	}, http.StatusOK, &updated)
	require.Equal(t, "Coffee & Tea", updated.Name)
}

// TestIntegrationGlobalCategoryCreationRunsAgainstPostgres is the admin half of
// the same defect: CreateGlobalCategory repeats the pattern with $4, so the
// global catalog was equally uncreatable.
func TestIntegrationGlobalCategoryCreationRunsAgainstPostgres(t *testing.T) {
	ctx := context.Background()

	a := newAPIClient(t)
	a.register("admin@example.com")

	var me models.User
	a.call(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK, &me)
	_, err := db.Pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id = $1`, me.ID)
	require.NoError(t, err)

	// The role travels in the access token, so the promotion only takes effect
	// once the user signs in again and the handler mints a session from the
	// role it reads on the users row.
	a.call(http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email":    "admin@example.com",
		"password": "integration-pass-123",
	}, http.StatusOK, nil)

	var created models.Category
	a.call(http.MethodPost, "/api/v1/admin/categories", models.CreateCategoryRequest{
		Name:    "Global Groceries",
		Icon:    "cart",
		Color:   "#22c55e",
		GroupID: "expense",
	}, http.StatusCreated, &created)
	require.NotEqual(t, uuid.Nil, created.ID)

	// A global category must land with a NULL user_id so it is shared.
	var userID *uuid.UUID
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT user_id FROM categories WHERE id = $1`, created.ID).Scan(&userID))
	require.Nil(t, userID, "a global category must not be owned by the admin")

	// The guard still rejects a group that is not global.
	a.call(http.MethodPost, "/api/v1/admin/categories", models.CreateCategoryRequest{
		Name:    "Bad Global",
		GroupID: "no-such-group",
	}, http.StatusBadRequest, nil)
}

// TestIntegrationRefreshSessionLifecycle drives rotation, reuse detection and
// logout revocation against real Postgres, where the refresh_tokens rows and
// the rotation transaction are actually exercised (pgxmock cannot validate the
// conditional update that detects reuse).
func TestIntegrationRefreshSessionLifecycle(t *testing.T) {
	ctx := context.Background()

	a := newAPIClient(t)
	a.register("rotate@example.com")

	var me models.User
	a.call(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK, &me)

	old := a.refreshCookie()
	require.NotEmpty(t, old, "registration must issue a refresh cookie")

	// Refresh rotates: the browser is handed a new cookie and the presented one
	// is spent.
	a.call(http.MethodPost, "/api/v1/auth/refresh", nil, http.StatusOK, nil)
	rotated := a.refreshCookie()
	require.NotEmpty(t, rotated)
	require.NotEqual(t, old, rotated, "refresh must rotate the refresh cookie")

	liveTokens := func(userID uuid.UUID) int {
		t.Helper()
		var n int
		require.NoError(t, db.Pool.QueryRow(ctx,
			`SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND revoked_at IS NULL`, userID).Scan(&n))
		return n
	}
	require.Equal(t, 1, liveTokens(me.ID))

	var replaced int
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND replaced_by IS NOT NULL`, me.ID).Scan(&replaced))
	require.Equal(t, 1, replaced, "rotation must record the successor")

	// Replaying the replaced token is reuse: the whole family is revoked, so
	// the successor the thief would present next dies with it.
	status, body := a.rawRequest(http.MethodPost, "/api/v1/auth/refresh", old)
	require.Equal(t, http.StatusUnauthorized, status, "body: %s", string(body))
	status, body = a.rawRequest(http.MethodPost, "/api/v1/auth/refresh", rotated)
	require.Equal(t, http.StatusUnauthorized, status, "the successor must die with the replayed token; body: %s", string(body))
	require.Equal(t, 0, liveTokens(me.ID))

	// Logout revokes the session's family, so a copied cookie cannot be resumed.
	other := newAPIClient(t)
	other.register("logout@example.com")
	copied := other.refreshCookie()
	require.NotEmpty(t, copied)

	other.call(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusOK, nil)
	require.Empty(t, other.refreshCookie(), "logout must clear the browser's cookie")

	status, body = other.rawRequest(http.MethodPost, "/api/v1/auth/refresh", copied)
	require.Equal(t, http.StatusUnauthorized, status, "a copied refresh token must not survive logout; body: %s", string(body))
}

func TestIntegrationImportDeduplicatesAgainstPostgres(t *testing.T) {
	a := newAPIClient(t)
	a.register("bob@example.com")
	acc := a.createAccount("Checking", "bank", nil)

	batch := []map[string]any{
		{"date": "2024-03-15", "description": "Coffee Shop", "amount": 250.50, "type": "debit"},
		{"date": "2024-03-16", "description": "Salary", "amount": 5000, "type": "credit"},
	}
	importBody := func(action string) map[string]any {
		return map[string]any{
			"accountId":       acc.ID,
			"duplicateAction": action,
			"transactions":    batch,
		}
	}

	var first struct {
		Imported   int `json:"imported"`
		Duplicates int `json:"duplicates"`
	}
	a.call(http.MethodPost, "/api/v1/transactions/import", importBody("skip"), http.StatusOK, &first)
	require.Equal(t, 2, first.Imported)
	require.Equal(t, 0, first.Duplicates)

	// Re-importing the identical batch is fully deduplicated. This exercises the
	// date-scoped snapshot query (date = ANY($3)) against real Postgres.
	var second struct {
		Imported   int `json:"imported"`
		Duplicates int `json:"duplicates"`
	}
	a.call(http.MethodPost, "/api/v1/transactions/import", importBody("skip"), http.StatusOK, &second)
	require.Equal(t, 0, second.Imported)
	require.Equal(t, 2, second.Duplicates)

	var validation models.ValidateTransactionsResponse
	a.call(http.MethodPost, "/api/v1/transactions/validate", map[string]any{
		"accountId":    acc.ID,
		"transactions": batch,
	}, http.StatusOK, &validation)
	require.Equal(t, 2, validation.ExistingCount)
	require.Equal(t, 0, validation.MissingCount)

	// "keep" bypasses dedupe and inserts duplicates.
	var kept struct {
		Imported int `json:"imported"`
	}
	a.call(http.MethodPost, "/api/v1/transactions/import", importBody("keep"), http.StatusOK, &kept)
	require.Equal(t, 2, kept.Imported)

	require.Len(t, a.transactions(acc.ID), 4)
}

// A create that repeats a clientKey is applied exactly once and the replay
// answers with the same id — the offline outbox's flush depends on it. Only a
// real server can prove the partial unique index and the replay lookup agree.
func TestIntegrationCreateIsIdempotentByClientKey(t *testing.T) {
	a := newAPIClient(t)
	a.register("idempotent@example.com")
	acc := a.createAccount("Checking", "bank", nil)

	body := map[string]any{
		"accountId":   acc.ID,
		"date":        "2024-06-10",
		"description": "Offline coffee",
		"amount":      12.5,
		"type":        "debit",
		"clientKey":   "offline-flush-1",
	}

	var first, replay struct {
		ID uuid.UUID `json:"id"`
	}
	a.call(http.MethodPost, "/api/v1/transactions", body, http.StatusCreated, &first)
	a.call(http.MethodPost, "/api/v1/transactions", body, http.StatusOK, &replay)
	require.Equal(t, first.ID, replay.ID)

	// The same payload under a new key is a genuinely new transaction.
	body["clientKey"] = "offline-flush-2"
	a.call(http.MethodPost, "/api/v1/transactions", body, http.StatusCreated, nil)

	require.Len(t, a.transactions(acc.ID), 2)
}

// TestIntegrationDeleteNonTransferLinkPreservesCategory guards SEC-C2: deleting
// a cashback link must not wipe the user's own category on either transaction.
func TestIntegrationDeleteNonTransferLinkPreservesCategory(t *testing.T) {
	a := newAPIClient(t)
	a.register("carol@example.com")
	acc := a.createAccount("Credit Card", "credit_card", nil)

	cats := a.categories()
	groceries := categoryByName(t, cats, "Groceries")
	shopping := categoryByName(t, cats, "Shopping")

	purchase := a.createTransaction(acc.ID, &groceries.ID, "2024-03-10", "Big Bazaar", 1500, "debit")
	payment := a.createTransaction(acc.ID, &shopping.ID, "2024-03-11", "Card payment", 1500, "credit")

	var link models.Link
	a.call(http.MethodPost, "/api/v1/links", map[string]any{
		"type":      "cashback",
		"fromTxnId": purchase,
		"toTxnId":   payment,
	}, http.StatusCreated, &link)

	a.call(http.MethodDelete, "/api/v1/links/"+link.ID.String(), nil, http.StatusOK, nil)

	byID := txnsByID(a.transactions(acc.ID))
	require.NotNil(t, byID[purchase].CategoryID)
	require.Equal(t, groceries.ID, *byID[purchase].CategoryID)
	require.NotNil(t, byID[payment].CategoryID)
	require.Equal(t, shopping.ID, *byID[payment].CategoryID)
}

// TestIntegrationDeleteTransferLinkClearsCategory verifies the complement of
// SEC-C2: deleting a transfer link does clear the transfer-derived category.
func TestIntegrationDeleteTransferLinkClearsCategory(t *testing.T) {
	a := newAPIClient(t)
	a.register("dave@example.com")
	src := a.createAccount("Checking", "bank", nil)
	dst := a.createAccount("Savings", "bank", nil)
	cats := a.categories()
	groceries := categoryByName(t, cats, "Groceries")
	salary := categoryByName(t, cats, "Salary")

	from := a.createTransaction(src.ID, &groceries.ID, "2024-03-01", "Transfer out", 500, "debit")
	to := a.createTransaction(dst.ID, &salary.ID, "2024-03-01", "Transfer in", 500, "credit")

	var link models.Link
	a.call(http.MethodPost, "/api/v1/links", map[string]any{
		"type":      "transfer",
		"fromTxnId": from,
		"toTxnId":   to,
	}, http.StatusCreated, &link)

	// The transfer link re-categorizes both legs under the seeded Transfer
	// category; deleting it clears that derived category.
	afterCreate := txnsByID(append(a.transactions(src.ID), a.transactions(dst.ID)...))
	require.NotNil(t, afterCreate[from].CategoryID)
	require.NotNil(t, afterCreate[to].CategoryID)

	a.call(http.MethodDelete, "/api/v1/links/"+link.ID.String(), nil, http.StatusOK, nil)

	afterDelete := txnsByID(append(a.transactions(src.ID), a.transactions(dst.ID)...))
	require.Nil(t, afterDelete[from].CategoryID)
	require.Nil(t, afterDelete[to].CategoryID)
}

func TestIntegrationBillingCyclesGeneratedForImport(t *testing.T) {
	a := newAPIClient(t)
	a.register("erin@example.com")
	billingDay := 15
	acc := a.createAccount("Credit Card", "credit_card", &billingDay)

	a.call(http.MethodPost, "/api/v1/transactions/import", map[string]any{
		"accountId":       acc.ID,
		"duplicateAction": "skip",
		"transactions": []map[string]any{
			{"date": time.Now().Format("2006-01-02"), "description": "Purchase", "amount": 1000, "type": "debit"},
		},
	}, http.StatusOK, nil)

	var out struct {
		Data []models.BillingCycle `json:"data"`
	}
	a.call(http.MethodGet, "/api/v1/accounts/"+acc.ID.String()+"/billing-cycles", nil, http.StatusOK, &out)
	require.NotEmpty(t, out.Data, "billing cycles should be generated on import")

	counted := 0
	for _, c := range out.Data {
		if c.TransactionCount > 0 {
			counted++
		}
	}
	require.Greater(t, counted, 0, "the imported transaction should be attached to a cycle")
}

// TestIntegrationCrossAccountBillingCycleRejected covers H-1: a transaction can
// never be attached to another account's billing cycle, on either create or
// PATCH. The composite FK plus the handler predicates enforce this in Postgres.
func TestIntegrationCrossAccountBillingCycleRejected(t *testing.T) {
	a := newAPIClient(t)
	a.register("frank@example.com")
	day := 15
	cardA := a.createAccount("Card A", "credit_card", &day)
	cardB := a.createAccount("Card B", "credit_card", &day)

	var cycles struct {
		Data []models.BillingCycle `json:"data"`
	}
	a.call(http.MethodGet, "/api/v1/accounts/"+cardA.ID.String()+"/billing-cycles", nil, http.StatusOK, &cycles)
	require.NotEmpty(t, cycles.Data, "account A should have generated cycles")
	cycleA := cycles.Data[0].ID

	// Creating a B transaction in A's cycle is rejected.
	status, data := a.request(http.MethodPost, "/api/v1/transactions", map[string]any{
		"accountId":      cardB.ID,
		"date":           "2024-03-10",
		"description":    "cross-account",
		"amount":         10,
		"type":           "debit",
		"billingCycleId": cycleA,
	})
	require.Equal(t, http.StatusBadRequest, status, "body: %s", string(data))

	// PATCHing an existing B transaction onto A's cycle is rejected too.
	bTxn := a.createTransaction(cardB.ID, nil, "2024-03-11", "cross-account patch", 10, "debit")
	a.call(http.MethodPatch, "/api/v1/transactions/"+bTxn.String(),
		map[string]any{"billingCycleId": cycleA}, http.StatusNotFound, nil)

	// The transaction is attached to one of B's own cycles (the date-based
	// default), never to A's cycle.
	txns := txnsByID(a.transactions(cardB.ID))
	if got := txns[bTxn]; got.BillingCycleID != nil {
		require.NotEqual(t, cycleA, *got.BillingCycleID, "transaction must not use account A's cycle")
	}
}

// TestIntegrationPatchToLoanAccountRejected covers H-2: PATCH cannot move an
// existing transaction onto a Loan / EMI account (the DB trigger backstops it).
func TestIntegrationPatchToLoanAccountRejected(t *testing.T) {
	a := newAPIClient(t)
	a.register("grace@example.com")
	bank := a.createAccount("Bank", "bank", nil)
	loan := a.createAccount("Car Loan", "loan", nil)

	txn := a.createTransaction(bank.ID, nil, "2024-03-01", "EMI", 1000, "debit")

	a.call(http.MethodPatch, "/api/v1/transactions/"+txn.String(),
		map[string]any{"accountId": loan.ID}, http.StatusNotFound, nil)

	// The transaction is unchanged and still on the bank account.
	bankTxns := a.transactions(bank.ID)
	require.Len(t, bankTxns, 1)
	require.Equal(t, bank.ID, bankTxns[0].AccountID)
	require.Empty(t, a.transactions(loan.ID))
}

// TestIntegrationConcurrentLinkCreationIsUnique covers M-1: two simultaneous
// identical link requests must yield exactly one link (the unique index makes
// the insert atomic, with the loser reporting a 409).
func TestIntegrationConcurrentLinkCreationIsUnique(t *testing.T) {
	a := newAPIClient(t)
	a.register("heidi@example.com")
	acc := a.createAccount("Card", "credit_card", nil)

	from := a.createTransaction(acc.ID, nil, "2024-03-01", "out", 100, "debit")
	to := a.createTransaction(acc.ID, nil, "2024-03-02", "in", 100, "credit")

	body := map[string]any{"type": "cashback", "fromTxnId": from, "toTxnId": to}
	statuses := make([]int, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i], _, errs[i] = a.concurrentRequest(http.MethodPost, "/api/v1/links", body)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "request %d", i)
	}
	require.Contains(t, statuses, http.StatusCreated)
	require.Contains(t, statuses, http.StatusConflict)

	var links []models.Link
	a.call(http.MethodGet, "/api/v1/links", nil, http.StatusOK, &links)
	require.Len(t, links, 1)
}

// TestIntegrationConcurrentSkipImportIsAtomic covers M-2: two simultaneous
// skip imports of the same batch must not double-insert (serialized per account
// by an advisory lock).
func TestIntegrationConcurrentSkipImportIsAtomic(t *testing.T) {
	a := newAPIClient(t)
	a.register("ivan@example.com")
	acc := a.createAccount("Checking", "bank", nil)

	body := map[string]any{
		"accountId":       acc.ID,
		"duplicateAction": "skip",
		"transactions": []map[string]any{
			{"date": "2024-04-01", "description": "One", "amount": 10, "type": "debit"},
			{"date": "2024-04-02", "description": "Two", "amount": 20, "type": "debit"},
		},
	}

	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = a.concurrentRequest(http.MethodPost, "/api/v1/transactions/import", body)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "request %d", i)
	}
	// Each row is stored exactly once despite the concurrent imports.
	require.Len(t, a.transactions(acc.ID), 2)
}

// TestIntegrationValidateAgreesWithSkipImport pins the contract the import
// preview dialog relies on: the duplicate count /transactions/validate reports
// is the count an import with duplicateAction=skip actually drops — including
// rows repeated inside the batch, which only the import path used to count.
func TestIntegrationValidateAgreesWithSkipImport(t *testing.T) {
	a := newAPIClient(t)
	a.register("validate-agreement@example.com")
	acc := a.createAccount("Checking", "bank", nil)

	coffee := map[string]any{"date": "2024-09-01", "description": "Coffee", "amount": 250.5, "type": "debit"}
	tea := map[string]any{"date": "2024-09-01", "description": "Tea", "amount": 50, "type": "debit"}
	batch := []map[string]any{coffee, coffee, tea}

	var preview models.ValidateTransactionsResponse
	a.call(http.MethodPost, "/api/v1/transactions/validate", map[string]any{
		"accountId":    acc.ID,
		"transactions": batch,
	}, http.StatusOK, &preview)
	require.Equal(t, 1, preview.ExistingCount, "the in-batch repeat is a duplicate")
	require.Equal(t, 2, preview.MissingCount)

	var imported struct {
		Imported   int `json:"imported"`
		Duplicates int `json:"duplicates"`
		Total      int `json:"total"`
	}
	a.call(http.MethodPost, "/api/v1/transactions/import", map[string]any{
		"accountId":       acc.ID,
		"duplicateAction": "skip",
		"transactions":    batch,
	}, http.StatusOK, &imported)

	require.Equal(t, preview.MissingCount, imported.Imported)
	require.Equal(t, preview.ExistingCount, imported.Duplicates)
	require.Len(t, a.transactions(acc.ID), preview.MissingCount)
}

// TestIntegrationMalformedFilterIDsAreRejected pins the filter validation
// against a real database. pgx cannot encode a Go string for a uuid parameter,
// so before the up-front checks these filters reached Postgres and answered 500
// — something pgxmock cannot demonstrate, because the mock never encodes.
func TestIntegrationMalformedFilterIDsAreRejected(t *testing.T) {
	a := newAPIClient(t)
	a.register("malformed-filters@example.com")
	acc := a.createAccount("Bank", "bank", nil)
	a.createTransaction(acc.ID, nil, "2024-08-01", "Coffee", 10, "debit")

	for _, path := range []string{
		"/api/v1/transactions?payeeId=not-a-uuid",
		"/api/v1/transactions?payeeId=none,not-a-uuid",
		"/api/v1/transactions?loanAccountId=not-a-uuid",
		"/api/v1/transactions?recurringId=not-a-uuid",
		"/api/v1/transactions/export?payeeId=not-a-uuid",
		"/api/v1/links?txnId=not-a-uuid",
		"/api/v1/links/cycles?dateFrom=oops",
	} {
		status, body := a.request(http.MethodGet, path, nil)
		require.Equal(t, http.StatusBadRequest, status, "%s -> %s", path, body)
	}
}

// TestIntegrationConcurrentRestoreIsSerialized covers the restore guard: the
// "user already has accounts" check is a plain read, so two simultaneous
// imports of the same bundle would both see an empty user and each insert a
// full copy. The user row's lock serializes them, so the loser waits, then sees
// the committed rows and answers the same 409 a sequential retry gets.
func TestIntegrationConcurrentRestoreIsSerialized(t *testing.T) {
	alice := newAPIClient(t)
	alice.register("restore-source@example.com")
	bank := alice.createAccount("Checking", "bank", nil)
	alice.createTransaction(bank.ID, nil, "2024-07-01", "One", 10, "debit")

	var bundle models.BackupBundle
	alice.call(http.MethodGet, "/api/v1/export", nil, http.StatusOK, &bundle)

	bob := newAPIClient(t)
	bob.register("restore-target@example.com")

	statuses := make([]int, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i], _, errs[i] = bob.concurrentRequest(http.MethodPost, "/api/v1/import", bundle)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "request %d", i)
	}
	require.Contains(t, statuses, http.StatusOK, "statuses: %v", statuses)
	require.Contains(t, statuses, http.StatusConflict, "statuses: %v", statuses)

	// The bundle was restored exactly once.
	var accounts []models.Account
	bob.call(http.MethodGet, "/api/v1/accounts", nil, http.StatusOK, &accounts)
	require.Len(t, accounts, 1)
}

// TestIntegrationUserBackupRoundTrip exports one user's whole graph and
// restores it into a fresh user, verifying that account/category/payee/billing
// cycle/transaction references survive the ID remapping and that a non-empty
// user is refused.
func TestIntegrationUserBackupRoundTrip(t *testing.T) {
	alice := newAPIClient(t)
	alice.register("alice-backup@example.com")

	bank := alice.createAccount("Checking", "bank", nil)
	day := 15
	card := alice.createAccount("Card", "credit_card", &day)
	loan := alice.createAccount("Car Loan", "loan", nil)

	cats := alice.categories()
	groceries := categoryByName(t, cats, "Groceries")
	salary := categoryByName(t, cats, "Salary")

	t1 := alice.createTransaction(bank.ID, &groceries.ID, "2024-05-01", "Groceries", 200, "debit")
	t2 := alice.createTransaction(bank.ID, &salary.ID, "2024-05-02", "Salary", 5000, "credit")
	t3 := alice.createTransaction(card.ID, nil, "2024-05-03", "EMI", 1000, "debit")

	alice.call(http.MethodPost, "/api/v1/links", map[string]any{
		"type": "cashback", "fromTxnId": t1, "toTxnId": t2,
	}, http.StatusCreated, nil)
	alice.call(http.MethodPost, "/api/v1/transactions/bulk-loan", map[string]any{
		"transactionIds": []uuid.UUID{t3}, "loanAccountId": loan.ID,
	}, http.StatusOK, nil)
	alice.call(http.MethodPost, "/api/v1/recurring", map[string]any{
		"name": "Rent", "type": "debit", "frequency": "monthly",
		"startDate": "2024-05-01", "accountId": bank.ID, "amount": 1500,
	}, http.StatusCreated, nil)
	alice.call(http.MethodPut, "/api/v1/accounts/"+loan.ID.String()+"/loan-schedule", map[string]any{
		"principal": 12000, "annualRateBps": 900, "tenureMonths": 24, "startDate": "2024-05-01",
	}, http.StatusOK, nil)

	var bundle models.BackupBundle
	alice.call(http.MethodGet, "/api/v1/export", nil, http.StatusOK, &bundle)
	require.Equal(t, models.BackupFormat, bundle.Format)
	require.Len(t, bundle.Accounts, 3)
	require.Len(t, bundle.Transactions, 3)
	require.Len(t, bundle.Links, 1)
	require.Len(t, bundle.LoanAttachments, 1)
	require.Len(t, bundle.LoanSchedules, 1)
	require.Len(t, bundle.RecurringSeries, 1)

	bob := newAPIClient(t)
	bob.register("bob-backup@example.com")
	categoriesBefore := len(bob.categories())

	var result models.BackupImportResult
	bob.call(http.MethodPost, "/api/v1/import", bundle, http.StatusOK, &result)
	require.Equal(t, 3, result.Accounts)
	require.Equal(t, 3, result.Transactions)
	require.Equal(t, 1, result.Links)
	require.Equal(t, 1, result.LoanAttachments)
	require.Equal(t, 1, result.LoanSchedules)
	require.Equal(t, 1, result.RecurringSeries)
	require.Equal(t, len(bundle.BillingCycles), result.BillingCycles)
	require.Empty(t, result.Warnings)

	var bobAccounts []models.Account
	bob.call(http.MethodGet, "/api/v1/accounts", nil, http.StatusOK, &bobAccounts)
	require.Len(t, bobAccounts, 3)

	// Matching by name+group reused every seeded default category instead of
	// duplicating it.
	require.Equal(t, categoriesBefore, len(bob.categories()))

	var bankID, cardID, loanID uuid.UUID
	for _, acc := range bobAccounts {
		switch acc.Name {
		case "Checking":
			bankID = acc.ID
		case "Card":
			cardID = acc.ID
		case "Car Loan":
			loanID = acc.ID
		}
	}
	require.NotEqual(t, uuid.Nil, bankID)
	require.NotEqual(t, uuid.Nil, cardID)
	require.NotEqual(t, uuid.Nil, loanID)
	require.Len(t, bob.transactions(bankID), 2)

	// The amortization schedule came across too, remapped onto bob's loan.
	var restored models.LoanScheduleDetail
	bob.call(http.MethodGet, "/api/v1/accounts/"+loanID.String()+"/loan-schedule", nil, http.StatusOK, &restored)
	require.NotNil(t, restored.Schedule)
	require.Equal(t, money.FromFloat(12000), restored.Schedule.Principal)
	require.Equal(t, 900, restored.Schedule.AnnualRateBps)
	require.Len(t, restored.Entries, 24)

	// The loan attachment was remapped onto bob's own loan account.
	cardTxns := bob.transactions(cardID)
	require.Len(t, cardTxns, 1)
	require.NotNil(t, cardTxns[0].LoanAccountID)

	var links []models.Link
	bob.call(http.MethodGet, "/api/v1/links", nil, http.StatusOK, &links)
	require.Len(t, links, 1)

	var recurring struct {
		Data []models.RecurringSeries `json:"data"`
	}
	bob.call(http.MethodGet, "/api/v1/recurring", nil, http.StatusOK, &recurring)
	require.Len(t, recurring.Data, 1)

	// A restore is refused once the user has accounts, and alice is non-empty.
	status, _ := bob.request(http.MethodPost, "/api/v1/import", bundle)
	require.Equal(t, http.StatusConflict, status)
	status, _ = alice.request(http.MethodPost, "/api/v1/import", bundle)
	require.Equal(t, http.StatusConflict, status)
}

// TestIntegrationRestoreOfACategoryInAnAbsentGroup covers a bundle whose
// category references a group the target instance does not have — what a bundle
// looks like when a category was filed under an admin-created global group that
// is missing on the target. The restore must not fail on
// categories_group_id_fkey; the category lands in a fallback group instead.
func TestIntegrationRestoreOfACategoryInAnAbsentGroup(t *testing.T) {
	alice := newAPIClient(t)
	alice.register("absent-group-source@example.com")

	bank := alice.createAccount("Checking", "bank", nil)
	groceries := categoryByName(t, alice.categories(), "Groceries")
	alice.createTransaction(bank.ID, &groceries.ID, "2024-05-01", "Groceries", 200, "debit")

	var bundle models.BackupBundle
	alice.call(http.MethodGet, "/api/v1/export", nil, http.StatusOK, &bundle)

	absent := uuid.New().String()
	rewritten := false
	for i := range bundle.Categories {
		if bundle.Categories[i].Name == "Groceries" {
			bundle.Categories[i].GroupID = absent
			rewritten = true
		}
	}
	require.True(t, rewritten, "the bundle must carry the Groceries category")

	bob := newAPIClient(t)
	bob.register("absent-group-target@example.com")

	var result models.BackupImportResult
	bob.call(http.MethodPost, "/api/v1/import", bundle, http.StatusOK, &result)
	require.Equal(t, 1, result.Categories)
	require.NotEmpty(t, result.Warnings, "substituting the group must be reported")

	bobCats := bob.categories()
	var imported *models.Category
	for i := range bobCats {
		if bobCats[i].Name == "Groceries" && bobCats[i].GroupName == "Imported" {
			imported = &bobCats[i]
		}
	}
	require.NotNil(t, imported, "the category must land in the fallback group")

	var bobAccounts []models.Account
	bob.call(http.MethodGet, "/api/v1/accounts", nil, http.StatusOK, &bobAccounts)
	require.Len(t, bobAccounts, 1)
	txns := bob.transactions(bobAccounts[0].ID)
	require.Len(t, txns, 1)
	require.NotNil(t, txns[0].CategoryID)
	require.Equal(t, imported.ID, *txns[0].CategoryID, "the transaction must reference the restored category")
}

// TestIntegrationMoneyFlowGraph exercises the money-flow aggregation against
// real PostgreSQL: the grouped queries, their GROUP BY clauses, and the
// link-summary join are all validated by the database, which pgxmock cannot do.
func TestIntegrationMoneyFlowGraph(t *testing.T) {
	a := newAPIClient(t)
	a.register("moneyflow@example.com")

	bank := a.createAccount("Main Bank", "bank", nil)
	cats := a.categories()
	salary := categoryByName(t, cats, "Salary")
	groceries := categoryByName(t, cats, "Groceries")

	a.createTransaction(bank.ID, &salary.ID, "2024-06-01", "June salary", 5000, "credit")
	a.createTransaction(bank.ID, &groceries.ID, "2024-06-02", "Big Bazaar", 1500, "debit")

	var graph models.MoneyFlowGraph
	a.call(http.MethodGet, "/api/v1/dashboard/money-flow?dateFrom=2024-06-01&dateTo=2024-06-30", nil, http.StatusOK, &graph)

	// Both accounts default to INR, so the window holds one key and the map is
	// that single figure; a second currency would have added a second key rather
	// than joined this one.
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(5000)}, graph.TotalIncome)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(1500)}, graph.TotalExpense)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(3500)}, graph.TotalNet)
	require.Equal(t, []string{"INR"}, graph.CurrencyScope.Currencies)

	kinds := map[string]int{}
	for _, n := range graph.Nodes {
		kinds[n.Kind]++
	}
	require.GreaterOrEqual(t, kinds["income"], 1)
	require.GreaterOrEqual(t, kinds["account"], 1)
	require.GreaterOrEqual(t, kinds["category"], 1)
	require.GreaterOrEqual(t, kinds["payee"], 1)
	require.NotEmpty(t, graph.Links)
}

// TestIntegrationCashFlowCalendar exercises the daily cash-flow aggregation
// against real PostgreSQL, including the DATE grouping and the billing-cycle /
// synthetic-summary overlays that pgxmock cannot validate.
func TestIntegrationCashFlowCalendar(t *testing.T) {
	a := newAPIClient(t)
	a.register("cashflow@example.com")

	billingDay := 5
	bank := a.createAccount("Main Bank", "bank", &billingDay)
	cats := a.categories()
	salary := categoryByName(t, cats, "Salary")
	groceries := categoryByName(t, cats, "Groceries")

	a.createTransaction(bank.ID, &salary.ID, "2024-06-03", "June salary", 5000, "credit")
	a.createTransaction(bank.ID, &groceries.ID, "2024-06-04", "Big Bazaar", 1500, "debit")

	// All accounts: just the daily aggregates, no overlays.
	var all models.CashFlowCalendar
	a.call(http.MethodGet, "/api/v1/dashboard/cash-flow-calendar?dateFrom=2024-06-01&dateTo=2024-06-30", nil, http.StatusOK, &all)
	require.Len(t, all.Days, 2)
	require.Equal(t, "2024-06-03", all.Days[0].Date)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(5000)}, all.Days[0].Net)
	require.Equal(t, "2024-06-04", all.Days[1].Date)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(-1500)}, all.Days[1].Net)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(3500)}, all.Net)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(5000)}, all.MaxAbsNet)
	require.Equal(t, []string{"INR"}, all.CurrencyScope.Currencies)
	require.Empty(t, all.Cycles)
	require.Empty(t, all.Markers)

	// Single billing-day account: cycle boundaries and summary markers overlay.
	var one models.CashFlowCalendar
	a.call(http.MethodGet,
		"/api/v1/dashboard/cash-flow-calendar?dateFrom=2024-06-01&dateTo=2024-06-30&accountId="+bank.ID.String(),
		nil, http.StatusOK, &one)
	require.Len(t, one.Days, 2)
	require.NotEmpty(t, one.Cycles)
	require.NotEmpty(t, one.Markers)
	for _, marker := range one.Markers {
		require.Contains(t, []string{"balance", "outstanding"}, marker.Kind)
	}
}

// TestIntegrationMoneyFlowAccountEdgesAndTimeline verifies the real-SQL
// behavior of the cross-account Sankey edges and the money-flow timeline, both
// of which rely on link/date joins that pgxmock cannot validate.
func TestIntegrationMoneyFlowAccountEdgesAndTimeline(t *testing.T) {
	a := newAPIClient(t)
	a.register("flowedges@example.com")

	src := a.createAccount("Checking", "bank", nil)
	dst := a.createAccount("Savings", "bank", nil)
	cats := a.categories()
	groceries := categoryByName(t, cats, "Groceries")
	salary := categoryByName(t, cats, "Salary")

	from := a.createTransaction(src.ID, &groceries.ID, "2024-06-01", "Transfer out", 500, "debit")
	to := a.createTransaction(dst.ID, &salary.ID, "2024-06-01", "Transfer in", 500, "credit")
	a.call(http.MethodPost, "/api/v1/links", map[string]any{
		"type":      "transfer",
		"fromTxnId": from,
		"toTxnId":   to,
	}, http.StatusCreated, nil)

	var graph models.MoneyFlowGraph
	a.call(http.MethodGet, "/api/v1/dashboard/money-flow?dateFrom=2024-06-01&dateTo=2024-06-30", nil, http.StatusOK, &graph)
	require.NotNil(t, findNode(graph.Nodes, "account:"+src.ID.String()))
	require.NotNil(t, findNode(graph.Nodes, "account:"+dst.ID.String()))
	found := false
	for _, l := range graph.Links {
		if l.Source == "account:"+src.ID.String() && l.Target == "account:"+dst.ID.String() {
			found = true
			require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(500)}, l.Value)
		}
	}
	require.True(t, found, "expected a checking -> savings account edge")

	var timeline models.MoneyFlowTimeline
	a.call(http.MethodGet, "/api/v1/dashboard/money-flow/timeline?dateFrom=2024-06-01&dateTo=2024-06-30", nil, http.StatusOK, &timeline)
	require.Len(t, timeline.Periods, 1)
	require.Equal(t, "2024-06", timeline.Periods[0].Key)
	// Per-currency: both accounts default to INR, so the month holds one key.
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(500)}, timeline.Periods[0].Income)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(500)}, timeline.Periods[0].Expense)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(0)}, timeline.Periods[0].Net)
	require.Equal(t, []string{"INR"}, timeline.CurrencyScope.Currencies)
}

// TestIntegrationMoneyFlowShowsCategorizedPayeeTransactions guards the
// user-facing scenario: after categorizing a transaction and assigning a payee,
// the money-flow graph reflects both the category and the payee node.
func TestIntegrationMoneyFlowShowsCategorizedPayeeTransactions(t *testing.T) {
	a := newAPIClient(t)
	a.register("categorized@example.com")
	acc := a.createAccount("Main Bank", "bank", nil)
	cats := a.categories()
	groceries := categoryByName(t, cats, "Groceries")

	var payee models.Payee
	a.call(http.MethodPost, "/api/v1/payees", map[string]any{"name": "Big Bazaar"}, http.StatusCreated, &payee)

	var created struct {
		ID uuid.UUID `json:"id"`
	}
	a.call(http.MethodPost, "/api/v1/transactions", map[string]any{
		"accountId":   acc.ID,
		"date":        "2024-06-10",
		"description": "Big Bazaar",
		"amount":      1200,
		"type":        "debit",
		"categoryId":  groceries.ID,
		"payeeId":     payee.ID,
	}, http.StatusCreated, &created)

	var graph models.MoneyFlowGraph
	a.call(http.MethodGet, "/api/v1/dashboard/money-flow?dateFrom=2024-06-01&dateTo=2024-06-30", nil, http.StatusOK, &graph)

	require.NotNil(t, findNode(graph.Nodes, "category:"+groceries.ID.String()), "category node missing")
	require.NotNil(t, findNode(graph.Nodes, "payee:"+payee.ID.String()), "payee node missing")
}

func findNode(nodes []models.MoneyFlowNode, id string) *models.MoneyFlowNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
	}
	return nil
}

// TestIntegrationSearchSpansNotesPayeeAndTags verifies against real Postgres
// that the shared free-text filter reaches every text field the list renders:
// description, notes, the payee's name, and tags (a correlated EXISTS over
// `unnest(tags)` — the part pgxmock cannot validate).
func TestIntegrationSearchSpansNotesPayeeAndTags(t *testing.T) {
	a := newAPIClient(t)
	a.register("searchall@example.com")
	acc := a.createAccount("Checking", "bank", nil)

	var payee models.Payee
	a.call(http.MethodPost, "/api/v1/payees", map[string]any{"name": "Blue Bottle Coffee"}, http.StatusCreated, &payee)

	create := func(body map[string]any) uuid.UUID {
		var out struct {
			ID uuid.UUID `json:"id"`
		}
		body["accountId"] = acc.ID
		a.call(http.MethodPost, "/api/v1/transactions", body, http.StatusCreated, &out)
		return out.ID
	}
	byDesc := create(map[string]any{
		"date": "2024-03-01", "description": "LATTE morning", "amount": 5, "type": "debit",
	})
	byNote := create(map[string]any{
		"date": "2024-03-02", "description": "POS 4471", "amount": 7, "type": "debit",
		"notes": "latte with a friend",
	})
	byTag := create(map[string]any{
		"date": "2024-03-03", "description": "POS 9930", "amount": 9, "type": "debit",
		"tags": []string{"LatteFund"},
	})
	byPayee := create(map[string]any{
		"date": "2024-03-04", "description": "POS 1111", "amount": 11, "type": "debit",
		"payeeId": payee.ID,
	})
	unrelated := create(map[string]any{
		"date": "2024-03-05", "description": "Rent", "amount": 100, "type": "debit",
	})

	search := func(q string) map[uuid.UUID]bool {
		var out struct {
			Data []models.Transaction `json:"data"`
		}
		a.call(http.MethodGet, "/api/v1/transactions?search="+q, nil, http.StatusOK, &out)
		found := map[uuid.UUID]bool{}
		for _, tx := range out.Data {
			found[tx.ID] = true
		}
		return found
	}

	hits := search("latte")
	// Case-insensitive across description, notes, and tags.
	require.True(t, hits[byDesc], "description should match")
	require.True(t, hits[byNote], "notes should match")
	require.True(t, hits[byTag], "tags should match")
	require.False(t, hits[byPayee], "only the payee's own name matches")
	require.False(t, hits[unrelated])

	require.True(t, search("blue%20bottle")[byPayee], "payee name should match")
	require.False(t, search("blue%20bottle")[byDesc])
}

// TestIntegrationLinkCycles verifies the circular-money report end to end: a
// reciprocal pair is reported as a cycle (with the Sankey netting it away), the
// residual one-way pair is reported as a one-sided flow, and a balanced pair is
// neither.
func TestIntegrationLinkCycles(t *testing.T) {
	a := newAPIClient(t)
	a.register("cycles@example.com")
	checking := a.createAccount("Checking", "bank", nil)
	card := a.createAccount("Card", "credit_card", nil)
	savings := a.createAccount("Savings", "bank", nil)

	link := func(from, to uuid.UUID) {
		a.call(http.MethodPost, "/api/v1/links", map[string]any{
			"type": "transfer", "fromTxnId": from, "toTxnId": to,
		}, http.StatusCreated, nil)
	}

	// Checking -> Card 8000 and Card -> Checking 3000: a reciprocal pair, which
	// the Sankey nets down to a single 5000 edge.
	out := a.createTransaction(checking.ID, nil, "2024-05-01", "Pay card", 8000, "debit")
	in := a.createTransaction(card.ID, nil, "2024-05-01", "Card paid", 8000, "credit")
	link(out, in)

	back := a.createTransaction(card.ID, nil, "2024-05-02", "Card refund out", 3000, "debit")
	backIn := a.createTransaction(checking.ID, nil, "2024-05-02", "Refund in", 3000, "credit")
	link(back, backIn)

	// A one-way pair: Checking -> Savings with no flow back.
	out2 := a.createTransaction(checking.ID, nil, "2024-05-03", "Move to savings", 1500, "debit")
	in2 := a.createTransaction(savings.ID, nil, "2024-05-03", "Savings in", 1500, "credit")
	link(out2, in2)

	var report models.LinkCycleReport
	a.call(http.MethodGet, "/api/v1/links/cycles?dateFrom=2024-05-01&dateTo=2024-05-31", nil, http.StatusOK, &report)

	require.Len(t, report.Cycles, 1)
	cycle := report.Cycles[0]
	require.Equal(t, "reciprocal", cycle.Kind)
	require.Len(t, cycle.Accounts, 2)
	require.Len(t, cycle.Legs, 2)
	// Every account here defaults to INR, so each amount is a one-key map and
	// that one key is the figure the scalar field used to carry.
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(3000)}, cycle.Net)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(11000)}, cycle.Gross)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(3000)}, report.TotalCircular)
	require.Equal(t, []string{"INR"}, report.CurrencyScope.Currencies)

	require.Len(t, report.OneSidedFlows, 1)
	flow := report.OneSidedFlows[0]
	require.Equal(t, checking.ID.String(), flow.FromAccountID)
	require.Equal(t, savings.ID.String(), flow.ToAccountID)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(1500)}, flow.Total)
	require.Equal(t, 1, flow.Count)
	require.Len(t, flow.Types, 1)
	require.Equal(t, "transfer", flow.Types[0].Type)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(1500)}, flow.Types[0].Total)
}

// TestIntegrationLoanSchedule walks idea #33 end to end: store the loan terms,
// attach EMI payments, and read back the amortization table with the principal/
// interest split and the progress derived from the attachments.
func TestIntegrationLoanSchedule(t *testing.T) {
	a := newAPIClient(t)
	a.register("amort@example.com")
	bank := a.createAccount("Bank", "bank", nil)
	loan := a.createAccount("Car Loan", "loan", nil)

	// An unconfigured loan answers with a null schedule rather than a 404.
	var empty models.LoanScheduleDetail
	a.call(http.MethodGet, "/api/v1/accounts/"+loan.ID.String()+"/loan-schedule", nil, http.StatusOK, &empty)
	require.Nil(t, empty.Schedule)
	require.Empty(t, empty.Entries)
	require.Equal(t, "Car Loan", empty.LoanAccountName)

	var saved models.LoanScheduleDetail
	a.call(http.MethodPut, "/api/v1/accounts/"+loan.ID.String()+"/loan-schedule", map[string]any{
		"principal":     1000,
		"annualRateBps": 1200,
		"tenureMonths":  12,
		"startDate":     "2024-04-01",
	}, http.StatusOK, &saved)

	require.NotNil(t, saved.Schedule)
	require.Equal(t, money.FromFloat(89), saved.EMI)
	require.Len(t, saved.Entries, 12)
	require.Equal(t, "2024-04-01", saved.Entries[0].DueDate.Format("2006-01-02"))
	require.Equal(t, money.FromFloat(10), saved.Entries[0].Interest)
	require.Equal(t, money.FromFloat(79), saved.Entries[0].Principal)
	require.Equal(t, money.FromFloat(1000), saved.OutstandingPrincipal)
	require.NotNil(t, saved.NextDueDate)

	// Attach two EMI payments (a debit) plus a refund (a credit).
	emi1 := a.createTransaction(bank.ID, nil, "2024-04-01", "EMI Apr", 88.85, "debit")
	emi2 := a.createTransaction(bank.ID, nil, "2024-05-01", "EMI May", 88.85, "debit")
	refund := a.createTransaction(bank.ID, nil, "2024-05-02", "EMI reversal", 10, "credit")
	a.call(http.MethodPost, "/api/v1/transactions/bulk-loan", map[string]any{
		"transactionIds": []uuid.UUID{emi1, emi2, refund},
		"loanAccountId":  loan.ID,
	}, http.StatusOK, nil)

	var progress models.LoanScheduleDetail
	a.call(http.MethodGet, "/api/v1/accounts/"+loan.ID.String()+"/loan-schedule", nil, http.StatusOK, &progress)
	require.Equal(t, 2, progress.PaidInstallments)
	require.True(t, progress.Entries[0].Paid)
	require.True(t, progress.Entries[1].Paid)
	require.False(t, progress.Entries[2].Paid)
	require.NotNil(t, progress.Entries[0].TransactionID)
	require.Equal(t, emi1, *progress.Entries[0].TransactionID)
	require.Equal(t, money.FromFloat(167.70), progress.PaidAmount) // 88.85*2 - 10
	require.Equal(t, money.FromFloat(19.21), progress.InterestPaid)
	require.Equal(t, money.FromFloat(158.79), progress.PrincipalPaid)
	require.Equal(t, money.FromFloat(841.21), progress.OutstandingPrincipal)
	require.False(t, progress.Completed)

	// The schedule survives a backup round trip.
	var bundle models.BackupBundle
	a.call(http.MethodGet, "/api/v1/export", nil, http.StatusOK, &bundle)
	require.Len(t, bundle.LoanSchedules, 1)
	require.Equal(t, money.FromFloat(1000), bundle.LoanSchedules[0].Principal)

	a.call(http.MethodDelete, "/api/v1/accounts/"+loan.ID.String()+"/loan-schedule", nil, http.StatusOK, nil)

	var removed models.LoanScheduleDetail
	a.call(http.MethodGet, "/api/v1/accounts/"+loan.ID.String()+"/loan-schedule", nil, http.StatusOK, &removed)
	require.Nil(t, removed.Schedule)
}

// TestIntegrationLoanFeeStubAndTransfer covers the three loan-account
// behaviors end to end against PostgreSQL: a processing fee recorded for
// reference without touching the table, a first period that is not a whole month
// billed with day-count interest, and a balance transfer that settles one loan
// while recasting the other (including starting the target's schedule when it
// had none).
func TestIntegrationLoanFeeStubAndTransfer(t *testing.T) {
	a := newAPIClient(t)
	a.register("loantransfer@example.com")
	bank := a.createAccount("Bank", "bank", nil)
	source := a.createAccount("Old Loan", "loan", nil)
	target := a.createAccount("New Loan", "loan", nil)

	// 1,000.00 sanctioned at 12% over 12 months, a 50.00 processing fee, and a
	// disbursal on 2024-02-20 against a first installment on 2024-04-05: a
	// broken 45-day period.
	var terms models.LoanScheduleDetail
	a.call(http.MethodPut, "/api/v1/accounts/"+source.ID.String()+"/loan-schedule", map[string]any{
		"principal":     1000,
		"processingFee": 50,
		"annualRateBps": 1200,
		"tenureMonths":  12,
		"startDate":     "2024-04-05",
		"disbursalDate": "2024-02-20",
	}, http.StatusOK, &terms)

	// The fee is stored for reference and never amortized: the table repays the
	// whole 1,000.00. The 45-day first period is charged 45/30 of a month's
	// interest and solved into the EMI, so all twelve installments stay level.
	require.Equal(t, money.FromFloat(50), terms.Schedule.ProcessingFee)
	require.Equal(t, money.FromFloat(1000), terms.OutstandingPrincipal)
	require.Equal(t, money.FromFloat(90), terms.EMI)
	require.Equal(t, money.FromFloat(15), terms.Entries[0].Interest) // 45 days prorated over a 30-day month
	require.Equal(t, money.FromFloat(9.25), terms.Entries[1].Interest)
	require.Equal(t, "2024-04-05", terms.Entries[0].DueDate.Format("2006-01-02"))
	require.False(t, terms.Entries[0].Recast)

	// Two installments paid leaves 844.25 of principal.
	emi1 := a.createTransaction(bank.ID, nil, "2024-04-05", "EMI Apr", 90, "debit")
	emi2 := a.createTransaction(bank.ID, nil, "2024-05-05", "EMI May", 90, "debit")
	a.call(http.MethodPost, "/api/v1/transactions/bulk-loan", map[string]any{
		"transactionIds": []uuid.UUID{emi1, emi2},
		"loanAccountId":  source.ID,
	}, http.StatusOK, nil)

	var paid models.LoanScheduleDetail
	a.call(http.MethodGet, "/api/v1/accounts/"+source.ID.String()+"/loan-schedule", nil, http.StatusOK, &paid)
	require.Equal(t, 2, paid.PaidInstallments)
	require.Equal(t, money.FromFloat(844.25), paid.OutstandingPrincipal)
	require.NotNil(t, paid.LastPaidDate)
	require.Equal(t, "2024-05-05", paid.LastPaidDate.Format("2006-01-02"))

	// The target has no terms yet, so the transfer starts its schedule from the
	// payoff that moves — which must be counted once, not twice. The payoff is
	// the 844.25 still owed plus the 27 days of interest since the May EMI.
	var moved models.LoanTransferResult
	a.call(http.MethodPost, "/api/v1/accounts/"+source.ID.String()+"/loan-transfer", map[string]any{
		"toLoanAccountId":     target.ID,
		"transferDate":        "2024-06-01",
		"targetAnnualRateBps": 900,
		"targetTenureMonths":  24,
		"targetStartDate":     "2024-07-01",
	}, http.StatusOK, &moved)

	require.Equal(t, money.FromFloat(851.85), moved.Transfer.Amount)
	require.Equal(t, money.FromFloat(844.25), moved.Transfer.Principal)
	require.Equal(t, money.FromFloat(7.60), moved.Transfer.AccruedInterest)
	require.Equal(t, target.ID, moved.Transfer.ToLoanAccountID)
	require.Equal(t, money.FromFloat(851.85), moved.Target.Schedule.Principal)
	require.Equal(t, money.FromFloat(851.85), moved.Target.OutstandingPrincipal)
	require.Equal(t, money.FromFloat(39), moved.Target.EMI)
	require.Len(t, moved.Target.Entries, 24)
	require.False(t, moved.Target.Entries[0].Recast)

	// The source is settled: its paid installments stand, the rest are void, and
	// nothing is outstanding.
	require.NotNil(t, moved.Source.SettledOn)
	require.True(t, moved.Source.Completed)
	require.Zero(t, moved.Source.OutstandingPrincipal)
	require.Nil(t, moved.Source.NextDueDate)
	require.True(t, moved.Source.Entries[1].Paid)
	require.False(t, moved.Source.Entries[1].Cancelled)
	require.True(t, moved.Source.Entries[2].Cancelled)
	require.Len(t, moved.Source.Transfers, 1)

	// The source has nothing left to move, so a second transfer is refused.
	a.call(http.MethodPost, "/api/v1/accounts/"+source.ID.String()+"/loan-transfer", map[string]any{
		"toLoanAccountId": target.ID,
		"transferDate":    "2024-07-01",
	}, http.StatusBadRequest, nil)

	// Undoing the transfer reverts both sides, including the schedule it opened.
	a.call(http.MethodDelete,
		"/api/v1/accounts/"+source.ID.String()+"/loan-transfer/"+moved.Transfer.ID.String(), nil, http.StatusOK, nil)

	var reverted models.LoanScheduleDetail
	a.call(http.MethodGet, "/api/v1/accounts/"+source.ID.String()+"/loan-schedule", nil, http.StatusOK, &reverted)
	require.Nil(t, reverted.SettledOn)
	require.False(t, reverted.Completed)
	require.Equal(t, money.FromFloat(844.25), reverted.OutstandingPrincipal)
	require.False(t, reverted.Entries[2].Cancelled)
	require.NotNil(t, reverted.NextDueDate)

	var targetAfter models.LoanScheduleDetail
	a.call(http.MethodGet, "/api/v1/accounts/"+target.ID.String()+"/loan-schedule", nil, http.StatusOK, &targetAfter)
	require.Nil(t, targetAfter.Schedule)

	// And the transfer row itself is gone.
	var bundle models.BackupBundle
	a.call(http.MethodGet, "/api/v1/export", nil, http.StatusOK, &bundle)
	require.Empty(t, bundle.LoanTransfers)
	require.Len(t, bundle.LoanSchedules, 1)
	require.Equal(t, money.FromFloat(50), bundle.LoanSchedules[0].ProcessingFee)
	require.Equal(t, "2024-02-20", bundle.LoanSchedules[0].DisbursalDate)
}

// TestIntegrationLoanTakeoverAndDisbursement covers the refinance shape end to
// end against PostgreSQL: a new loan takes the old loan's balance out of its own
// disbursement (leaving its amortization alone), and the disbursement is then
// reconciled against the bank credit that actually arrived.
func TestIntegrationLoanTakeoverAndDisbursement(t *testing.T) {
	a := newAPIClient(t)
	a.register("takeover@example.com")
	bank := a.createAccount("Bank", "bank", nil)
	oldLoan := a.createAccount("Old Loan", "loan", nil)
	newLoan := a.createAccount("New Loan", "loan", nil)

	// The old loan: 1,000.00 at 12% over 12 months.
	var oldTerms models.LoanScheduleDetail
	a.call(http.MethodPut, "/api/v1/accounts/"+oldLoan.ID.String()+"/loan-schedule", map[string]any{
		"principal": 1000, "annualRateBps": 1200, "tenureMonths": 12, "startDate": "2024-04-01",
	}, http.StatusOK, &oldTerms)
	require.Equal(t, money.FromFloat(89), oldTerms.EMI)

	// The new loan has its own principal: 3,000.00 at 9% over 24 months.
	var newTerms models.LoanScheduleDetail
	a.call(http.MethodPut, "/api/v1/accounts/"+newLoan.ID.String()+"/loan-schedule", map[string]any{
		"principal": 3000, "annualRateBps": 900, "tenureMonths": 24, "startDate": "2024-04-01",
	}, http.StatusOK, &newTerms)
	require.Equal(t, money.FromFloat(138), newTerms.EMI)
	require.NotNil(t, newTerms.Disbursement)
	require.Equal(t, money.FromFloat(3000), newTerms.Disbursement.Net)

	// Two EMIs paid on the old loan leaves 841.21.
	emi1 := a.createTransaction(bank.ID, nil, "2024-04-01", "EMI Apr", 89, "debit")
	emi2 := a.createTransaction(bank.ID, nil, "2024-05-01", "EMI May", 89, "debit")
	a.call(http.MethodPost, "/api/v1/transactions/bulk-loan", map[string]any{
		"transactionIds": []uuid.UUID{emi1, emi2},
		"loanAccountId":  oldLoan.ID,
	}, http.StatusOK, nil)

	// A payoff quote for the same date answers the same amount, because the
	// quote and the transfer share one computation.
	var quoted models.LoanPayoff
	a.call(http.MethodGet, "/api/v1/accounts/"+oldLoan.ID.String()+"/loan-payoff?date=2024-06-01", nil, http.StatusOK, &quoted)
	require.Equal(t, money.FromFloat(849.90), quoted.Payoff)
	require.Equal(t, money.FromFloat(841.21), quoted.OutstandingPrincipal)
	require.Equal(t, money.FromFloat(8.69), quoted.AccruedInterest)
	require.Equal(t, 31, quoted.Days)
	require.Equal(t, "2024-05-01", quoted.FromDate.Format("2006-01-02"))

	// The takeover settles the old loan out of the new one's disbursement.
	var moved models.LoanTransferResult
	a.call(http.MethodPost, "/api/v1/accounts/"+oldLoan.ID.String()+"/loan-transfer", map[string]any{
		"toLoanAccountId": newLoan.ID,
		"transferDate":    "2024-06-01",
		"mode":            "takeover",
	}, http.StatusOK, &moved)

	require.Equal(t, models.LoanTransferTakeover, moved.Transfer.Mode)
	// 841.21 of principal plus the 31 days of interest since the May EMI.
	require.Equal(t, money.FromFloat(849.90), moved.Transfer.Amount)
	require.Equal(t, money.FromFloat(841.21), moved.Transfer.Principal)
	require.Equal(t, money.FromFloat(8.69), moved.Transfer.AccruedInterest)
	// The old loan is settled exactly as before.
	require.NotNil(t, moved.Source.SettledOn)
	require.True(t, moved.Source.Completed)
	require.Zero(t, moved.Source.OutstandingPrincipal)
	// The new loan's own table is untouched.
	require.Equal(t, money.FromFloat(3000), moved.Target.Schedule.Principal)
	require.Equal(t, money.FromFloat(138), moved.Target.EMI)
	require.Equal(t, money.FromFloat(3000), moved.Target.OutstandingPrincipal)
	require.False(t, moved.Target.Entries[0].Recast)
	// But it released 841.21 less.
	require.NotNil(t, moved.Target.Disbursement)
	require.Equal(t, money.FromFloat(849.90), moved.Target.Disbursement.PaidOut)
	require.Equal(t, money.FromFloat(2150.10), moved.Target.Disbursement.Net)

	// The bank credit that actually arrived reconciles with it.
	credit := a.createTransaction(bank.ID, nil, "2024-06-02", "New Loan disbursement", 2150.10, "credit")
	var verified models.LoanScheduleDetail
	a.call(http.MethodPut, "/api/v1/accounts/"+newLoan.ID.String()+"/loan-disbursement",
		map[string]any{"transactionId": credit}, http.StatusOK, &verified)
	require.NotNil(t, verified.Disbursement)
	require.True(t, verified.Disbursement.Verified)
	require.Zero(t, verified.Disbursement.Difference)
	require.NotNil(t, verified.Disbursement.CreditTransactionID)
	require.Equal(t, credit, *verified.Disbursement.CreditTransactionID)

	// A credit that does not match is reported, not rejected.
	short := a.createTransaction(bank.ID, nil, "2024-06-03", "Short credit", 2000, "credit")
	var mismatch models.LoanScheduleDetail
	a.call(http.MethodPut, "/api/v1/accounts/"+newLoan.ID.String()+"/loan-disbursement",
		map[string]any{"transactionId": short}, http.StatusOK, &mismatch)
	require.False(t, mismatch.Disbursement.Verified)
	require.Equal(t, money.FromFloat(-150.10), mismatch.Disbursement.Difference)

	// The credit that released a loan is not a repayment: it cannot be attached
	// as an EMI payment.
	a.call(http.MethodPost, "/api/v1/transactions/bulk-loan", map[string]any{
		"transactionIds": []uuid.UUID{short},
		"loanAccountId":  newLoan.ID,
	}, http.StatusConflict, nil)

	// Undoing the takeover puts the disbursement back and un-settles the source,
	// leaving the new loan's own terms alone.
	a.call(http.MethodDelete,
		"/api/v1/accounts/"+oldLoan.ID.String()+"/loan-transfer/"+moved.Transfer.ID.String(), nil, http.StatusOK, nil)

	var reverted models.LoanScheduleDetail
	a.call(http.MethodGet, "/api/v1/accounts/"+newLoan.ID.String()+"/loan-schedule", nil, http.StatusOK, &reverted)
	require.NotNil(t, reverted.Schedule)
	require.Equal(t, money.FromFloat(3000), reverted.Schedule.Principal)
	require.Zero(t, reverted.Disbursement.PaidOut)
	require.Equal(t, money.FromFloat(3000), reverted.Disbursement.Net)
	// The linked credit now over-reports the disbursement, which is the point.
	require.False(t, reverted.Disbursement.Verified)
	require.Equal(t, money.FromFloat(-1000), reverted.Disbursement.Difference)

	var sourceAfter models.LoanScheduleDetail
	a.call(http.MethodGet, "/api/v1/accounts/"+oldLoan.ID.String()+"/loan-schedule", nil, http.StatusOK, &sourceAfter)
	require.Nil(t, sourceAfter.SettledOn)
	require.Equal(t, money.FromFloat(841.21), sourceAfter.OutstandingPrincipal)

	// Unlinking forgets the credit, and is idempotent.
	a.call(http.MethodDelete, "/api/v1/accounts/"+newLoan.ID.String()+"/loan-disbursement", nil, http.StatusOK, nil)
	var unlinked models.LoanScheduleDetail
	a.call(http.MethodGet, "/api/v1/accounts/"+newLoan.ID.String()+"/loan-schedule", nil, http.StatusOK, &unlinked)
	require.Nil(t, unlinked.Disbursement.CreditTransactionID)
	require.False(t, unlinked.Disbursement.Verified)

	var bundle models.BackupBundle
	a.call(http.MethodGet, "/api/v1/export", nil, http.StatusOK, &bundle)
	require.Len(t, bundle.LoanSchedules, 2)
	require.Empty(t, bundle.LoanTransfers)
	require.Empty(t, bundle.LoanDisbursements)
}

// TestIntegrationRuleApplyRunsAgainstPostgres covers R-1: appendRulePredicate
// double-formatted the pattern clause ("%!(EXTRA int=N)"), so /rules/apply and
// /rules/preview emitted SQL PostgreSQL rejects. pgxmock could not catch it —
// its regexp matcher is unanchored, so a prefix expectation passed on the
// malformed tail. Only a real server rejects the statement.
func TestIntegrationRuleApplyRunsAgainstPostgres(t *testing.T) {
	a := newAPIClient(t)
	a.register("rulesflow@example.com")

	open := a.createAccount("Rule Bank", "bank", nil)
	frozen := a.createAccount("Frozen Card", "credit_card", billingDayPtr(15))
	cat := categoryByName(t, a.categories(), "Food & Dining")

	openTxn := a.createTransaction(open.ID, nil, "2024-03-10", "Zomato dinner", 250, "debit")
	frozenTxn := a.createTransaction(frozen.ID, nil, "2024-03-11", "Zomato lunch", 300, "debit")

	// Closing with only `closed` (no billingDay) must not 500 — the optional
	// SET clauses each number their own placeholder.
	a.call(http.MethodPut, "/api/v1/accounts/"+frozen.ID.String(),
		map[string]any{"closed": true}, http.StatusOK, nil)

	rule := map[string]any{"pattern": "Zomato", "matchType": "contains", "categoryId": cat.ID}
	var created models.Rule
	a.call(http.MethodPost, "/api/v1/rules", rule, http.StatusCreated, &created)

	// UpdateRule builds the same untyped-NULL construct, so it must parse too.
	var updated models.Rule
	a.call(http.MethodPut, "/api/v1/rules/"+created.ID.String(), rule, http.StatusOK, &updated)
	require.Equal(t, created.ID, updated.ID)
	require.Equal(t, cat.ID, updated.CategoryID)

	// The preview and the apply must agree, and both must be valid SQL.
	var preview models.RulePreview
	a.call(http.MethodPost, "/api/v1/rules/preview", rule, http.StatusOK, &preview)
	require.Equal(t, 1, preview.Matched, "only the open account's transaction is eligible")

	var applied struct {
		Updated int `json:"updated"`
	}
	a.call(http.MethodPost, "/api/v1/rules/apply", nil, http.StatusOK, &applied)
	require.Equal(t, 1, applied.Updated)

	categorized := txnsByID(a.transactions(open.ID))[openTxn]
	require.NotNil(t, categorized.CategoryID)
	require.Equal(t, cat.ID, *categorized.CategoryID)

	frozenTxnAfter := txnsByID(a.transactions(frozen.ID))[frozenTxn]
	require.Nil(t, frozenTxnAfter.CategoryID, "a closed account's transactions stay uncategorized")
}

// TestIntegrationAccountMoveClearsBillingCycle covers R-2: moving a transaction
// to another account used to leave the previous account's cycle attached, which
// polluted that cycle's totals. The composite FK
// (user_id, account_id, billing_cycle_id) now also makes a stale cycle
// impossible to persist.
func TestIntegrationAccountMoveClearsBillingCycle(t *testing.T) {
	a := newAPIClient(t)
	a.register("cyclemove@example.com")

	from := a.createAccount("Move From", "credit_card", billingDayPtr(15))
	to := a.createAccount("Move To", "credit_card", billingDayPtr(15))

	txn := a.createTransaction(from.ID, nil, "2024-03-10", "moving purchase", 100, "debit")
	before := txnsByID(a.transactions(from.ID))[txn]
	require.NotNil(t, before.BillingCycleID, "a credit-card transaction attaches to a cycle")

	a.call(http.MethodPatch, "/api/v1/transactions/"+txn.String(),
		map[string]any{"accountId": to.ID}, http.StatusOK, nil)

	moved := txnsByID(a.transactions(to.ID))[txn]
	require.Equal(t, to.ID, moved.AccountID)
	require.Nil(t, moved.BillingCycleID, "the old account's cycle must be cleared")

	// The origin account's cycles no longer count the moved transaction.
	var cycles struct {
		Data []models.BillingCycle `json:"data"`
	}
	a.call(http.MethodGet, "/api/v1/accounts/"+from.ID.String()+"/billing-cycles", nil, http.StatusOK, &cycles)
	for _, c := range cycles.Data {
		require.Zero(t, c.TransactionCount, "cycle %s still counts a moved transaction", c.Label)
	}
}

// billingDayPtr returns a pointer to a billing day, for accounts that must
// generate billing cycles.
func billingDayPtr(day int) *int {
	return &day
}

// TestIntegrationRestoreClearsCrossAccountCycle covers the restore path: the
// composite FK (user_id, account_id, billing_cycle_id) rejects a bundle that
// pairs a transaction with another account's cycle — the corruption the
// pre-fix PATCH could produce — so the restore clears the reference and warns
// instead of failing the whole import.
func TestIntegrationRestoreClearsCrossAccountCycle(t *testing.T) {
	source := newAPIClient(t)
	source.register("bundle-source@example.com")
	day := 15
	cardA := source.createAccount("Bundle Card A", "credit_card", billingDayPtr(day))
	cardB := source.createAccount("Bundle Card B", "credit_card", billingDayPtr(day))
	source.createTransaction(cardA.ID, nil, "2024-03-10", "bundle purchase A", 50, "debit")
	source.createTransaction(cardB.ID, nil, "2024-03-11", "bundle purchase B", 60, "debit")

	var bundle models.BackupBundle
	source.call(http.MethodGet, "/api/v1/export", nil, http.StatusOK, &bundle)

	// Point B's transaction at one of A's cycles, as the pre-fix PATCH did.
	var cycleA *uuid.UUID
	for i, c := range bundle.BillingCycles {
		if c.AccountID == cardA.ID {
			cycleA = &bundle.BillingCycles[i].ID
			break
		}
	}
	require.NotNil(t, cycleA, "account A should have exported billing cycles")
	target := -1
	for i, tx := range bundle.Transactions {
		if tx.AccountID == cardB.ID {
			target = i
		}
	}
	require.NotEqual(t, -1, target)
	bundle.Transactions[target].BillingCycleID = cycleA

	// A fresh user restores it: the bad reference is dropped with a warning
	// rather than tripping the foreign key.
	targetClient := newAPIClient(t)
	targetClient.register("bundle-target@example.com")
	var res models.BackupImportResult
	targetClient.call(http.MethodPost, "/api/v1/import", bundle, http.StatusOK, &res)
	require.Contains(t, res.Warnings, "cleared billing cycle: it belongs to another account")

	var all struct {
		Data []models.Transaction `json:"data"`
	}
	targetClient.call(http.MethodGet, "/api/v1/transactions", nil, http.StatusOK, &all)
	restored := 0
	for _, tx := range all.Data {
		if tx.Description == "bundle purchase B" {
			restored++
			require.Nil(t, tx.BillingCycleID, "the cross-account cycle must be cleared")
		}
	}
	require.Equal(t, 1, restored, "the transaction should still be restored")
}

// idAscending returns the ids sorted ascending, which is the tiebreak the
// transaction list applies after the credit/debit split inside a day.
func idAscending(ids []uuid.UUID) []uuid.UUID {
	out := append([]uuid.UUID{}, ids...)
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i][:], out[j][:]) < 0 })
	return out
}

// realTransactionIDs returns the ids the list endpoint returned, in order.
func realTransactionIDs(t *testing.T, a *apiClient, accountID uuid.UUID) []uuid.UUID {
	t.Helper()
	txns := a.transactions(accountID)
	ids := make([]uuid.UUID, 0, len(txns))
	for _, tx := range txns {
		ids = append(ids, tx.ID)
	}
	return ids
}

// TestIntegrationTransactionOrderIsTotal covers the "the list reorders itself
// between fetches" bug: ORDER BY date alone is not a total order, so same-date
// rows — and every row of one bulk import, which shares a single created_at —
// came back in whatever order the planner picked. The tiebreakers make it
// total (credits before debits inside a day, then the id), which is also what
// keeps a running balance from dipping below what the day's income funded.
//
// The test inserts a day's debits *before* its credits so an
// insertion-ordered result cannot pass by accident, then walks the list with a
// two-row page size: an unstable order repeats or skips rows at the page
// boundaries.
func TestIntegrationTransactionOrderIsTotal(t *testing.T) {
	a := newAPIClient(t)
	a.register("ordering@example.com")
	acc := a.createAccount("Ordering", "bank", nil)

	const day = "2024-03-15"
	debits := make([]uuid.UUID, 0, 3)
	for i := 0; i < 3; i++ {
		debits = append(debits, a.createTransaction(acc.ID, nil, day, fmt.Sprintf("spend %d", i), float64(100+i), "debit"))
	}
	credits := make([]uuid.UUID, 0, 3)
	for i := 0; i < 3; i++ {
		credits = append(credits, a.createTransaction(acc.ID, nil, day, fmt.Sprintf("income %d", i), float64(200+i), "credit"))
	}
	older := a.createTransaction(acc.ID, nil, "2024-03-14", "older", 42, "debit")

	// Descending date, so the day leads: its credits first, then its debits,
	// each group in id order, and the previous day last.
	want := append(idAscending(credits), idAscending(debits)...)
	want = append(want, older)

	require.Equal(t, want, realTransactionIDs(t, a, acc.ID),
		"a date-sorted list must return credits before debits inside a day, then id order")

	// Ascending keeps the same inside-day rule: oldest first, credits still
	// before debits.
	asc := append([]uuid.UUID{older}, idAscending(credits)...)
	asc = append(asc, idAscending(debits)...)
	var ascRes struct {
		Data []models.Transaction `json:"data"`
	}
	a.call(http.MethodGet, "/api/v1/transactions?accountId="+acc.ID.String()+"&sortOrder=ASC", nil, http.StatusOK, &ascRes)
	ascGot := make([]uuid.UUID, 0, len(ascRes.Data))
	for _, tx := range ascRes.Data {
		if !tx.IsSummary {
			ascGot = append(ascGot, tx.ID)
		}
	}
	require.Equal(t, asc, ascGot, "ascending order keeps credits before debits inside a day")

	// Three walks of the same two-row pages must produce one identical
	// sequence: no row repeated, none skipped, no page boundary drifting.
	for walk := 0; walk < 3; walk++ {
		paged := []uuid.UUID{}
		for page := 1; ; page++ {
			path := fmt.Sprintf("/api/v1/transactions?accountId=%s&limit=2&page=%d", acc.ID, page)
			status, body := a.request(http.MethodGet, path, nil)
			require.Equal(t, http.StatusOK, status, "page %d: %s", page, body)
			var res struct {
				Data  []models.Transaction `json:"data"`
				Pages int                  `json:"pages"`
			}
			require.NoError(t, json.Unmarshal(body, &res), "page %d: %s", page, body)
			for _, tx := range res.Data {
				if !tx.IsSummary {
					paged = append(paged, tx.ID)
				}
			}
			if page >= res.Pages {
				break
			}
		}
		require.Equal(t, want, paged, "paged walk %d", walk)
	}

	// The CSV exports share the order, so a report's rows match the list.
	for _, path := range []string{
		fmt.Sprintf("/api/v1/accounts/%s/export", acc.ID),
		"/api/v1/transactions/export?accountId=" + acc.ID.String(),
	} {
		status, body := a.request(http.MethodGet, path, nil)
		require.Equal(t, http.StatusOK, status, "%s: %s", path, body)
		records, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
		require.NoError(t, err, path)
		require.Len(t, records, 8, "%s: header plus seven transactions", path)
		require.Equal(t, day, records[1][0], "%s must lead with the newest day", path)
		require.Equal(t, "credit", records[1][3], "%s must lead with a credit", path)
	}
}

// TestIntegrationAggregatesRefuseToCombineCurrencies is the guarantee this
// change exists for: a window over accounts in two currencies produces
// per-currency subtotals and a scope naming both accounts, and no figure
// anywhere in the response is a sum across them.
//
// Every SQL shape in this change set is reached from here, because they were
// only ever checked by pgxmock, which matches a query string and never executes
// one: the per-currency ROW_NUMBER category top-15, the currency-keyed GROUP BY
// the category/payee and calendar queries gained, the link queries' CASE-based
// currency column, and the shared scope query. A syntax error in any of them
// answers 500 and takes this test with it.
func TestIntegrationAggregatesRefuseToCombineCurrencies(t *testing.T) {
	a := newAPIClient(t)
	a.register("mixed@example.com")

	inr := a.createAccountIn("Rupee Bank", "bank", "INR", nil)
	usd := a.createAccountIn("Dollar Bank", "bank", "USD", nil)
	cats := a.categories()
	groceries := categoryByName(t, cats, "Groceries")
	salary := categoryByName(t, cats, "Salary")

	a.createTransaction(inr.ID, &salary.ID, "2024-06-01", "June salary", 5000, "credit")
	a.createTransaction(inr.ID, &groceries.ID, "2024-06-02", "Big Bazaar", 1500, "debit")
	// The same category spent in the other currency: a category is not a currency,
	// so the breakdown below has to carry two keys for one row rather than a sum.
	a.createTransaction(usd.ID, &groceries.ID, "2024-06-03", "Whole Foods", 200, "debit")

	const window = "dateFrom=2024-06-01&dateTo=2024-06-30"
	wantIncome := models.CurrencyAmounts{"INR": money.FromFloat(5000)}
	wantExpense := models.CurrencyAmounts{"INR": money.FromFloat(1500), "USD": money.FromFloat(200)}
	wantNet := models.CurrencyAmounts{"INR": money.FromFloat(3500), "USD": money.FromFloat(-200)}

	// All five bodies are fetched and walked before anything is asserted against
	// them. The walk is the shape guard, and it is the one that has to run even
	// when a figure below is wrong: a require that aborts first would let a
	// reintroduced scalar hide behind a mismatched total.
	bodies := map[string][]byte{}
	for _, path := range []string{
		"/api/v1/dashboard/summary?" + window,
		"/api/v1/dashboard/money-flow?" + window,
		"/api/v1/dashboard/money-flow/timeline?" + window,
		"/api/v1/dashboard/cash-flow-calendar?" + window,
		"/api/v1/links/cycles?" + window,
	} {
		status, body := a.request(http.MethodGet, path, nil)
		require.Equal(t, http.StatusOK, status, "%s -> %s", path, body)
		bodies[path] = body
		requireNoScalarAmounts(t, path, body)
	}

	// Dashboard summary.
	body := bodies["/api/v1/dashboard/summary?"+window]
	var summary models.DashboardSummary
	require.NoError(t, json.Unmarshal(body, &summary))
	require.Equal(t, wantIncome, summary.TotalIncome)
	require.Equal(t, wantExpense, summary.TotalExpense)
	require.Equal(t, wantNet, summary.TotalNet)
	require.Equal(t, []string{"INR", "USD"}, summary.CurrencyScope.Currencies)
	requireScopeAccounts(t, summary.CurrencyScope, map[uuid.UUID]scopeWant{
		inr.ID: {income: models.CurrencyAmounts{"INR": money.FromFloat(5000)}, expense: models.CurrencyAmounts{"INR": money.FromFloat(1500)}},
		usd.ID: {income: models.CurrencyAmounts{}, expense: models.CurrencyAmounts{"USD": money.FromFloat(200)}},
	})
	// The currency that only spent still has a negative net, and still has no
	// income key: Add skips a zero contribution rather than inventing one, which
	// is what makes "a missing key reads as zero" true rather than aspirational.
	require.NotContains(t, summary.TotalIncome, "USD")
	require.Negative(t, summary.TotalIncome.Sub(summary.TotalExpense)["USD"])
	require.Len(t, summary.CurrencyScope.Currencies, 2)

	// The per-currency top-15 folds both currencies into one category entry.
	require.Len(t, summary.ByCategory, 1)
	require.Equal(t, "Groceries", summary.ByCategory[0].CategoryName)
	require.Equal(t, models.CurrencyAmounts{
		"INR": money.FromFloat(1500), "USD": money.FromFloat(200),
	}, summary.ByCategory[0].Total)
	require.Equal(t, 2, summary.ByCategory[0].Count)
	require.Len(t, summary.IncomeByCategory, 1)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(5000)}, summary.IncomeByCategory[0].Total)
	require.Len(t, summary.MonthlyTrend, 1)
	require.Equal(t, wantExpense, summary.MonthlyTrend[0].Expense)

	// Money-flow graph. The category/payee queries gained the account's currency
	// in their GROUP BY, so a node fed by two currencies keeps them apart; the
	// account->category edge is emitted per currency rather than merged.
	var graph models.MoneyFlowGraph
	require.NoError(t, json.Unmarshal(bodies["/api/v1/dashboard/money-flow?"+window], &graph))
	require.Equal(t, wantIncome, graph.TotalIncome)
	require.Equal(t, wantExpense, graph.TotalExpense)
	require.Equal(t, wantNet, graph.TotalNet)
	require.Equal(t, []string{"INR", "USD"}, graph.CurrencyScope.Currencies)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(5000)},
		nodeTotal(t, graph.Nodes, "income:"+salary.ID.String()))
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(5000)},
		nodeTotal(t, graph.Nodes, "account:"+inr.ID.String()))
	require.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(200)},
		nodeTotal(t, graph.Nodes, "account:"+usd.ID.String()))
	require.Equal(t, models.CurrencyAmounts{
		"INR": money.FromFloat(1500), "USD": money.FromFloat(200),
	}, nodeTotal(t, graph.Nodes, "category:"+groceries.ID.String()))

	// Money-flow timeline.
	var timeline models.MoneyFlowTimeline
	require.NoError(t, json.Unmarshal(bodies["/api/v1/dashboard/money-flow/timeline?"+window], &timeline))
	require.Len(t, timeline.Periods, 1)
	require.Equal(t, wantIncome, timeline.Periods[0].Income)
	require.Equal(t, wantExpense, timeline.Periods[0].Expense)
	require.Equal(t, wantNet, timeline.Periods[0].Net)
	require.Equal(t, []string{"INR", "USD"}, timeline.CurrencyScope.Currencies)

	// Cash-flow calendar, including the heatmap's own scale: a per-currency
	// MaxAbsNet, because one denominator across two currencies flattens the
	// quieter account's real deficit into nothing.
	var calendar models.CashFlowCalendar
	require.NoError(t, json.Unmarshal(bodies["/api/v1/dashboard/cash-flow-calendar?"+window], &calendar))
	require.Len(t, calendar.Days, 3)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(5000)}, calendar.Days[0].Net)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(-1500)}, calendar.Days[1].Net)
	require.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(-200)}, calendar.Days[2].Net)
	require.Equal(t, wantNet, calendar.Net)
	require.Equal(t, models.CurrencyAmounts{
		"INR": money.FromFloat(5000), "USD": money.FromFloat(200),
	}, calendar.MaxAbsNet)

	// Link cycles. Nothing is linked here, so the report is empty rather than
	// zero-valued, and the money it *would* have summed is still named.
	var cycles models.LinkCycleReport
	require.NoError(t, json.Unmarshal(bodies["/api/v1/links/cycles?"+window], &cycles))
	require.Empty(t, cycles.Cycles)
	require.Equal(t, models.NewCurrencyAmounts(), cycles.TotalCircular)
	require.Equal(t, []string{"INR", "USD"}, cycles.CurrencyScope.Currencies)
}

// scopeWant is one account's expected contribution to a currencyScope: what it
// brought in and what it spent, each per currency.
type scopeWant struct {
	income, expense models.CurrencyAmounts
}

// requireScopeAccounts asserts the scope names exactly the expected accounts and
// that each reports the income and expense given. A scope is the only place a
// response explains where its per-currency figures came from, so an account that
// drops out of it — or one that appears with a blank currency — is a currency
// the response cannot account for. Comparing the whole map rather than counting
// entries is what makes a dropped account a failure.
func requireScopeAccounts(t *testing.T, scope models.CurrencyScope, want map[uuid.UUID]scopeWant) {
	t.Helper()
	got := map[uuid.UUID]scopeWant{}
	for _, acc := range scope.Accounts {
		got[acc.ID] = scopeWant{income: acc.Income, expense: acc.Expense}
	}
	require.Equal(t, want, got)
	for _, acc := range scope.Accounts {
		require.NotEmpty(t, acc.Currency, "account %q was named with a blank currency", acc.Name)
	}
}

func nodeTotal(t *testing.T, nodes []models.MoneyFlowNode, id string) models.CurrencyAmounts {
	t.Helper()
	n := findNode(nodes, id)
	require.NotNil(t, n, "node %q missing from the graph", id)
	return n.Total
}

// TestIntegrationNullAndEmptyCurrencyReadsAsINR covers accounts.currency being
// `VARCHAR(3) DEFAULT 'INR'` with no NOT NULL (migration 000001), so a row can
// genuinely hold NULL, and backup.go's restore writing COALESCE(currency, '')
// back verbatim, so it can genuinely hold the empty string too. Both must read as
// INR rather than becoming a "" key, which is the only way a consumer would ever
// see a blank currency, and both must be *found* by an explicit ?currency=INR.
//
// The second half is the part only execution can settle. The reporting queries
// each spell the same COALESCE(NULLIF(a.currency, ''), 'INR') three times — once
// projected, once grouped, once compared — and accounts.currency is nullable, so
// a predicate that dropped its NULLIF would exclude these accounts from a
// ?currency=INR report while the projection beside it still called them INR. A
// unit test matches that predicate as a string and a spelling check compares
// three strings; this asserts the behaviour the three spellings exist to produce.
func TestIntegrationNullAndEmptyCurrencyReadsAsINR(t *testing.T) {
	ctx := context.Background()
	a := newAPIClient(t)
	a.register("nullccy@example.com")

	var me models.User
	a.call(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK, &me)

	// Written directly: the create endpoint rewrites an empty currency to the
	// default and has no way to store NULL at all, so the API cannot reach either
	// state and neither can a test that goes through it.
	empty := ""
	nullAcc := a.insertAccountWithRawCurrency(t, me.ID, "Null Currency", nil)
	emptyAcc := a.insertAccountWithRawCurrency(t, me.ID, "Empty Currency", &empty)

	// The rows really do hold what the test claims, or the rest of it proves
	// nothing: a column default silently applied would leave this testing the
	// ordinary path.
	var gotNull, gotEmpty *string
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT currency FROM accounts WHERE id = $1`, nullAcc.ID).Scan(&gotNull))
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT currency FROM accounts WHERE id = $1`, emptyAcc.ID).Scan(&gotEmpty))
	require.Nil(t, gotNull, "the NULL account must really hold NULL")
	require.NotNil(t, gotEmpty)
	require.Equal(t, "", *gotEmpty, "the empty account must really hold the empty string")

	a.createTransaction(nullAcc.ID, nil, "2024-06-01", "From the null account", 300, "credit")
	a.createTransaction(emptyAcc.ID, nil, "2024-06-02", "From the empty account", 200, "debit")

	const window = "dateFrom=2024-06-01&dateTo=2024-06-30"
	wantIncome := models.CurrencyAmounts{"INR": money.FromFloat(300)}
	wantExpense := models.CurrencyAmounts{"INR": money.FromFloat(200)}

	// A filter naming a real code finds both, in the one currency they are.
	var inrCalendar models.CashFlowCalendar
	status, body := a.request(http.MethodGet,
		"/api/v1/dashboard/cash-flow-calendar?"+window+"&currency=INR", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &inrCalendar))
	require.Equal(t, []string{"INR"}, inrCalendar.CurrencyScope.Currencies)
	requireScopeAccounts(t, inrCalendar.CurrencyScope, map[uuid.UUID]scopeWant{
		nullAcc.ID:  {income: wantIncome, expense: models.CurrencyAmounts{}},
		emptyAcc.ID: {income: models.CurrencyAmounts{}, expense: wantExpense},
	})
	require.Equal(t, wantIncome, inrCalendar.TotalIncome)
	require.Equal(t, wantExpense, inrCalendar.TotalExpense)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(100)}, inrCalendar.Net)
	require.Len(t, inrCalendar.Days, 2)
	// A blank currency would reach a consumer as a "" key on every one of these.
	require.NotContains(t, string(body), `""`)

	// Filtering for a currency neither account holds excludes them, which is the
	// same predicate agreeing with the same projection rather than the other way
	// round: neither account is called USD anywhere in the first response, so
	// neither may turn up under a USD filter.
	var usdCalendar models.CashFlowCalendar
	status, body = a.request(http.MethodGet,
		"/api/v1/dashboard/cash-flow-calendar?"+window+"&currency=USD", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &usdCalendar))
	require.Empty(t, usdCalendar.CurrencyScope.Currencies)
	require.Empty(t, usdCalendar.CurrencyScope.Accounts)
	require.Empty(t, usdCalendar.Days)
	require.Equal(t, models.NewCurrencyAmounts(), usdCalendar.TotalIncome)
	require.Equal(t, models.NewCurrencyAmounts(), usdCalendar.TotalExpense)
	require.Equal(t, models.NewCurrencyAmounts(), usdCalendar.Net)
	require.Equal(t, models.NewCurrencyAmounts(), usdCalendar.MaxAbsNet)

	// Unfiltered, both are called INR — by the projection and the GROUP BY rather
	// than by the predicate.
	var summary models.DashboardSummary
	status, body = a.request(http.MethodGet, "/api/v1/dashboard/summary?"+window, nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &summary))
	require.Equal(t, []string{"INR"}, summary.CurrencyScope.Currencies)
	require.Equal(t, wantIncome, summary.TotalIncome)
	require.Equal(t, wantExpense, summary.TotalExpense)
	require.Equal(t, 2, summary.TotalTransactions)
}

// TestIntegrationCategoryTopFifteenIsPerCurrency runs the real window-function
// query, which pgxmock cannot: a category with no matching transaction must be
// absent from the breakdown rather than ranking first in an empty partition and
// displacing a real one, and each currency's 15 must be its own.
//
// The amounts make a ranking that ignored the partition obviously wrong rather
// than coincidentally right: every USD category spends more than every INR one,
// so a single global ranking would return fifteen USD rows and no INR row at all.
func TestIntegrationCategoryTopFifteenIsPerCurrency(t *testing.T) {
	a := newAPIClient(t)
	a.register("topfifteen@example.com")

	inr := a.createAccountIn("Rupee Bank", "bank", "INR", nil)
	usd := a.createAccountIn("Dollar Bank", "bank", "USD", nil)
	group := a.baseGroupID()

	// Twenty spenders per currency, with distinct totals so each one's rank is
	// fixed and the cut is observed rather than inferred.
	spent := map[string]map[uuid.UUID]bool{}
	for _, currency := range []string{"INR", "USD"} {
		spent[currency] = map[uuid.UUID]bool{}
		base := 1000
		acc := inr
		if currency == "USD" {
			base = 2000
			acc = usd
		}
		for i := range 20 {
			cat := a.createCategory(fmt.Sprintf("%s Spender %02d", currency, i), group)
			a.createTransaction(acc.ID, &cat.ID, "2024-06-01",
				fmt.Sprintf("%s spend %02d", currency, i), float64(base+i), "debit")
			spent[currency][cat.ID] = true
		}
	}
	// A category nobody spent in. It has to be absent, and the reason it is lies
	// in the query rather than in a filter here: the inner join to accounts means
	// it produces no row at all, so there is no empty partition to rank it in.
	empty := a.createCategory("Never Spent", group)

	var summary models.DashboardSummary
	status, body := a.request(http.MethodGet,
		"/api/v1/dashboard/summary?dateFrom=2024-06-01&dateTo=2024-06-30", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &summary))

	byID := map[string]models.CategorySpend{}
	for _, cs := range summary.ByCategory {
		byID[cs.CategoryID] = cs
	}
	require.NotContains(t, byID, empty.ID.String(), "a category with no transactions must not be ranked")
	require.Len(t, summary.ByCategory, 30, "15 per currency, and each of these is spent in exactly one")

	for _, currency := range []string{"INR", "USD"} {
		base := 1000
		if currency == "USD" {
			base = 2000
		}
		// Counted rather than enumerated by hand, so a currency that lost its
		// share of the cut shows up as a number instead of a missing name.
		var kept []models.CurrencyAmounts
		for id := range spent[currency] {
			cs, ok := byID[id.String()]
			if !ok {
				continue
			}
			require.Equal(t, []string{currency}, cs.Total.Currencies(),
				"a category spent in one currency must not be reported in another")
			kept = append(kept, cs.Total)
		}
		require.Len(t, kept, 15, "%s must keep its own 15, not a share of a global ranking", currency)

		// The 15th of each currency survives and the 16th does not, so the cut is
		// at 15 per partition rather than merely "at most 15 somewhere". Amounts
		// run base..base+19, so the 15 largest are base+19 down to base+5.
		cut := money.FromFloat(float64(base + 5))
		below := money.FromFloat(float64(base + 4))
		present, absent := 0, 0
		for _, total := range kept {
			switch total[currency] {
			case cut:
				present++
			case below:
				absent++
			}
		}
		require.Equal(t, 1, present, "the 15th largest %s category must survive", currency)
		require.Equal(t, 0, absent, "the 16th largest %s category must be cut", currency)
	}
}

// TestIntegrationBillingCycleDateRangesTileTheWindow guards the invariant the
// billing-cycle trend's date-range join is only safe under.
//
// The trend used to join transactions on t.billing_cycle_id, which is an index
// lookup and structurally counts each transaction once. It now joins on
// t.date BETWEEN bc.start_date AND bc.end_date, deliberately trading the index
// for the agreement between the stat cards, the count and the trend: a
// transaction whose cycle assignment is NULL or detached is in the totals beside
// the chart and used to be in no bar at all. That trade is only sound because
// cycleDates builds each cycle from the day after the previous billing date
// through this one, so the ranges tile with no overlap. If two cycles' ranges
// overlapped, a transaction on the shared boundary day would appear in two bars
// and the trend would double count money the totals counted once — precisely the
// class of silent disagreement this change set exists to remove.
//
// The dates are all near today on purpose: the cycles are generated from the
// earliest transaction forward, so a 2024 date would drag the window across two
// years of empty cycles and bury the boundary days the assertions are about.
func TestIntegrationBillingCycleDateRangesTileTheWindow(t *testing.T) {
	a := newAPIClient(t)
	a.register("tiling@example.com")

	card := a.createAccountIn("Tiling Card", "credit_card", "INR", billingDayPtr(15))

	// Three cycles' worth, straddling two boundaries. With a billing day of 15 the
	// cycle ending the 15th of a month runs from the 16th of the one before, so
	// each 15th is the last day of one cycle and each 16th the first day of the
	// next. If the ranges overlapped it would be the 15th — a day two cycles both
	// claim — that the trend double counted.
	prev := time.Now().AddDate(0, -1, 0)
	prevEnd := time.Date(prev.Year(), prev.Month(), 15, 0, 0, 0, 0, time.UTC)
	cur := time.Now()
	curEnd := time.Date(cur.Year(), cur.Month(), 15, 0, 0, 0, 0, time.UTC)
	nextEnd := time.Date(cur.Year(), cur.Month()+1, 15, 0, 0, 0, 0, time.UTC)

	onFirstBoundary := a.createTransaction(card.ID, nil, prevEnd.Format("2006-01-02"), "On the first billing day", 100, "debit")
	a.createTransaction(card.ID, nil, prevEnd.AddDate(0, 0, 1).Format("2006-01-02"), "The day after it", 200, "debit")
	a.createTransaction(card.ID, nil, curEnd.Format("2006-01-02"), "On the second billing day", 300, "debit")
	a.createTransaction(card.ID, nil, curEnd.AddDate(0, 0, 1).Format("2006-01-02"), "The day after that", 400, "debit")

	// The stored ranges tile: each starts the day after the previous one ends, so
	// no date belongs to two cycles and none belongs to none.
	var cycles struct {
		Data []models.BillingCycle `json:"data"`
	}
	a.call(http.MethodGet, "/api/v1/accounts/"+card.ID.String()+"/billing-cycles", nil, http.StatusOK, &cycles)
	require.GreaterOrEqual(t, len(cycles.Data), 3)
	for i := 1; i < len(cycles.Data); i++ {
		require.Equal(t, cycles.Data[i-1].EndDate.AddDate(0, 0, 1), cycles.Data[i].StartDate,
			"cycle %q starts %s, so it overlaps or leaves a gap against %q ending %s",
			cycles.Data[i].Label, cycles.Data[i].StartDate.Format("2006-01-02"),
			cycles.Data[i-1].Label, cycles.Data[i-1].EndDate.Format("2006-01-02"))
	}

	var summary models.DashboardSummary
	status, body := a.request(http.MethodGet,
		"/api/v1/dashboard/summary?groupBy=billing_cycle&accountId="+card.ID.String()+"&cycles=6", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &summary))
	require.NotEmpty(t, summary.CurrentCycle)
	require.Len(t, summary.BillingCycleTrend, len(cycles.Data))

	// Each boundary-day spend lands in exactly one bar, and the bars add up to the
	// total beside them. Together those two assertions are the double-count guard:
	// an overlap would leave a bar holding its own day's spend plus the previous
	// cycle's, and would push the sum above the total.
	byEnd := trendExpenseFor(t, summary)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(100)}, byEnd[prevEnd.Format("2006-01-02")])
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(500)}, byEnd[curEnd.Format("2006-01-02")],
		"the 16th after one boundary and the 15th of the next share a cycle, and only a shared cycle")
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(400)}, byEnd[nextEnd.Format("2006-01-02")])

	var trendExpense models.CurrencyAmounts
	for _, item := range summary.BillingCycleTrend {
		trendExpense = sumPerCurrency(trendExpense, item.Expense)
	}
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(1000)}, trendExpense,
		"the trend must account for exactly what the window totals do")
	require.Equal(t, summary.TotalExpense, trendExpense)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(1000)}, summary.TotalExpense)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(-1000)}, summary.TotalNet)
	require.Equal(t, 4, summary.TotalTransactions)

	// A transaction detached from its cycle by hand is still in the totals and
	// still in the bar. That is the reason the join is a date range and not the
	// cycle id, so the case belongs beside the invariant it justifies.
	a.call(http.MethodPatch, "/api/v1/transactions/"+onFirstBoundary.String(),
		map[string]any{"billingCycleId": nil}, http.StatusOK, nil)

	var after models.DashboardSummary
	status, body = a.request(http.MethodGet,
		"/api/v1/dashboard/summary?groupBy=billing_cycle&accountId="+card.ID.String()+"&cycles=6", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &after))
	require.Equal(t, summary.TotalExpense, after.TotalExpense,
		"the total is a date window, not a cycle attachment")
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(100)},
		trendExpenseFor(t, after)[prevEnd.Format("2006-01-02")],
		"a detached transaction is still inside its cycle's date range")
}

// trendExpenseFor indexes a billing-cycle trend by each bar's end date, which is
// the date a test needs to name the cycle a transaction belongs to.
func trendExpenseFor(t *testing.T, summary models.DashboardSummary) map[string]models.CurrencyAmounts {
	t.Helper()
	out := map[string]models.CurrencyAmounts{}
	for _, item := range summary.BillingCycleTrend {
		out[item.EndDate.Format("2006-01-02")] = item.Expense
	}
	return out
}

// sumPerCurrency folds one per-currency amount into another, key by key. It
// exists so a test can total a list of responses and compare the result with a
// server-computed figure: an addition across keys would be the very arithmetic
// CurrencyAmounts refuses, and a test must not perform it either.
func sumPerCurrency(into, add models.CurrencyAmounts) models.CurrencyAmounts {
	if into == nil {
		into = models.NewCurrencyAmounts()
	}
	for code, amount := range add {
		into[code] += amount
	}
	return into
}

// TestIntegrationCalendarOverlaysIgnoreTheCurrencyFilter executes the one
// documented exception in this change set, which until now had only been argued
// from the source: attachCashFlowOverlays takes no currency and keys
// cycles[].outstanding and markers[].amount by the selected account's own.
//
// So ?currency=USD against an INR account returns an empty report — no days, no
// window totals, an empty currencyScope — *alongside* that account's INR cycle
// outstanding and INR markers. The overlay is best-effort decoration read outside
// the snapshot and is one account's own figures, which is why it is exempt; the
// consequence is that a currency filter can be answered with a figure in the
// currency that was filtered out, and that claim is made in openapi.yaml, the
// README, AGENTS.md and three MCP tool descriptions. A test is the only thing
// that can confirm the docs and the handler still agree.
func TestIntegrationCalendarOverlaysIgnoreTheCurrencyFilter(t *testing.T) {
	a := newAPIClient(t)
	a.register("overlay@example.com")

	card := a.createAccountIn("Rupee Card", "credit_card", "INR", billingDayPtr(15))
	usd := a.createAccountIn("Dollar Bank", "bank", "USD", nil)
	a.createTransaction(card.ID, nil, "2024-06-15", "Card spend", 300, "debit")
	// The USD account is deliberately quiet in the window below, so the same
	// ?currency=USD request with no account named is the "currency in scope, no
	// money in it" case rather than a second way of saying the same thing.
	a.createTransaction(usd.ID, nil, "2025-01-10", "Dollar spend", 40, "debit")

	const window = "dateFrom=2024-06-01&dateTo=2024-06-30"

	// Unfiltered: the account's own overlays, the days, and the scope all agree.
	var plain models.CashFlowCalendar
	status, body := a.request(http.MethodGet,
		"/api/v1/dashboard/cash-flow-calendar?"+window+"&accountId="+card.ID.String(), nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &plain))
	require.Equal(t, []string{"INR"}, plain.CurrencyScope.Currencies)
	require.Len(t, plain.Days, 1)
	require.NotEmpty(t, plain.Cycles)
	require.NotEmpty(t, plain.Markers)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(300)}, plain.TotalExpense)
	for _, cycle := range plain.Cycles {
		// A cycle that closes at zero carries no key, so only the ones that closed
		// with a balance say which currency they are in.
		if len(cycle.Outstanding) > 0 {
			require.Equal(t, []string{"INR"}, cycle.Outstanding.Currencies(),
				"a cycle belongs to one account, so its outstanding is that account's money")
		}
	}
	for _, marker := range plain.Markers {
		require.Equal(t, []string{"INR"}, marker.Amount.Currencies())
	}

	// The exception. Everything the currency filter is responsible for is empty,
	// and the overlay is not: it is still the INR account's own figure.
	var filtered models.CashFlowCalendar
	status, body = a.request(http.MethodGet,
		"/api/v1/dashboard/cash-flow-calendar?"+window+"&currency=USD&accountId="+card.ID.String(), nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &filtered))

	require.Empty(t, filtered.Days, "a USD window over an INR account has no days")
	require.Empty(t, filtered.CurrencyScope.Currencies,
		"the scope must not claim a currency the filtered window has no money in")
	require.Empty(t, filtered.CurrencyScope.Accounts)
	require.Equal(t, models.NewCurrencyAmounts(), filtered.TotalIncome)
	require.Equal(t, models.NewCurrencyAmounts(), filtered.TotalExpense)
	require.Equal(t, models.NewCurrencyAmounts(), filtered.Net)
	require.Equal(t, models.NewCurrencyAmounts(), filtered.MaxAbsNet)

	// The overlay is the exception, and it is the named account's currency rather
	// than the requested one. Every marker after the empty window is INR.
	require.NotEmpty(t, filtered.Markers, "the overlay is not narrowed by ?currency=")
	for _, marker := range filtered.Markers {
		require.Equal(t, "INR", marker.Amount.Currencies()[0],
			"markers are keyed by the named account's own currency, which is the documented exception")
	}
	require.Equal(t, plain.Markers[0].Amount, filtered.Markers[0].Amount,
		"the same overlay, the same figure: nothing about it was narrowed")
	// Cycles with a zero running balance carry no key at all, which is the third
	// state rather than a blank one — and the response says so by omitting them.
	require.NotEmpty(t, filtered.Cycles)
	withMoney := 0
	for _, cycle := range filtered.Cycles {
		if len(cycle.Outstanding) > 0 {
			withMoney++
			require.Equal(t, []string{"INR"}, cycle.Outstanding.Currencies())
		}
	}
	require.Positive(t, withMoney, "at least one cycle closes with a real balance")

	// The same filter without an account is the "a currency in scope with no money
	// in it" case, and it is what produces a real {} on the wire rather than a
	// nil map. Add skips a zero contribution, so the scope names USD — the
	// account is in the window even though it is quiet in it — and every amount is
	// an empty object. Three MCP tool descriptions promise a model that an
	// amount with no keys reads as zero rather than as a failed call, and this is
	// the only place that promise can be observed.
	var quiet models.CashFlowCalendar
	status, body = a.request(http.MethodGet,
		"/api/v1/dashboard/cash-flow-calendar?"+window+"&currency=USD", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &quiet))
	require.Equal(t, []string{"USD"}, quiet.CurrencyScope.Currencies)
	require.Len(t, quiet.CurrencyScope.Accounts, 1, "a quiet account stays in the scope; that is what the LEFT JOIN is for")
	require.Equal(t, "USD", quiet.CurrencyScope.Accounts[0].Currency)
	require.Equal(t, models.NewCurrencyAmounts(), quiet.CurrencyScope.Accounts[0].Income)
	require.Equal(t, models.NewCurrencyAmounts(), quiet.CurrencyScope.Accounts[0].Expense)
	require.Empty(t, quiet.Days)

	// Asserted on the bytes, because {} and null are the same Go value to a
	// decoder and only one of them is the documented third state.
	for _, field := range []string{
		`"totalIncome":{}`, `"totalExpense":{}`, `"net":{}`, `"maxAbsNet":{}`,
	} {
		require.Contains(t, string(body), field,
			"an amount with no keys must serialize as {}, not null; body: %s", body)
	}
	require.NotContains(t, string(body), "null")
}

// TestIntegrationLinkCycleReportsTheValueCurrency runs the link queries' CASE
// currency column for real: a link's value is one of its two transactions'
// amounts, so the currency it is denominated in is the currency of the account
// that CASE picked, matched by the same CASE. The CASE is nested inside the
// COALESCE, which pgxmock could only check as a substring.
//
// The pair is reciprocal between an INR account and a USD one, which is the case
// the shape change exists for: the two legs are 100 INR one way and 40 USD the
// other, they cannot cancel, and the report has to say so in two keys rather than
// netting them or picking the smaller.
func TestIntegrationLinkCycleReportsTheValueCurrency(t *testing.T) {
	a := newAPIClient(t)
	a.register("cycleccy@example.com")

	inr := a.createAccountIn("Rupee Bank", "bank", "INR", nil)
	usd := a.createAccountIn("Dollar Bank", "bank", "USD", nil)

	link := func(from, to uuid.UUID) {
		a.call(http.MethodPost, "/api/v1/links", map[string]any{
			"type": "transfer", "fromTxnId": from, "toTxnId": to,
		}, http.StatusCreated, nil)
	}

	// Out of the INR account, so the value is denominated in INR.
	outINR := a.createTransaction(inr.ID, nil, "2024-05-01", "Rupees out", 100, "debit")
	inUSD := a.createTransaction(usd.ID, nil, "2024-05-01", "Dollars in", 100, "credit")
	// Out of the USD account, so the value is denominated in USD — the same CASE
	// on a different branch, and the two legs of one cycle in two currencies.
	outUSD := a.createTransaction(usd.ID, nil, "2024-05-02", "Dollars out", 40, "debit")
	inINR := a.createTransaction(inr.ID, nil, "2024-05-02", "Rupees in", 40, "credit")
	link(outINR, inUSD)
	link(outUSD, inINR)

	var report models.LinkCycleReport
	status, body := a.request(http.MethodGet,
		"/api/v1/links/cycles?dateFrom=2024-05-01&dateTo=2024-05-31", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &report))

	require.Len(t, report.Cycles, 1)
	cycle := report.Cycles[0]
	require.Equal(t, "reciprocal", cycle.Kind)
	require.Len(t, cycle.Legs, 2)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(100), "USD": money.FromFloat(40)},
		cycle.Gross, "each leg keeps the currency of the account its value came from")
	require.Equal(t, cycle.Gross, cycle.Net, "one leg per currency, so each currency's smallest leg is its only one")

	// Two keys is the signal that this is not a circulation figure, and nothing in
	// the payload may collapse it into one number.
	_, _, single := cycle.Net.Single()
	require.False(t, single, "a two-currency cycle has no single circulation figure")
	require.Len(t, cycle.Net, 2)
	require.Equal(t, cycle.Net, report.TotalCircular)
	require.Equal(t, []string{"INR", "USD"}, report.CurrencyScope.Currencies)
	requireScopeAccounts(t, report.CurrencyScope, map[uuid.UUID]scopeWant{
		inr.ID: {income: models.CurrencyAmounts{"INR": money.FromFloat(40)}, expense: models.CurrencyAmounts{"INR": money.FromFloat(100)}},
		usd.ID: {income: models.CurrencyAmounts{"USD": money.FromFloat(100)}, expense: models.CurrencyAmounts{"USD": money.FromFloat(40)}},
	})
	for _, leg := range cycle.Legs {
		require.Len(t, leg.Amount, 1, "a single leg is one direction, so it is one currency")
		require.Len(t, leg.Types, 1)
		require.Len(t, leg.Types[0].Total, 1)
	}
	require.Equal(t, []string{"INR", "USD"}, legCurrencies(cycle.Legs))
	requireNoScalarAmounts(t, "/links/cycles", body)

	// The Sankey cannot draw a cycle, and one currency cannot cancel another, so
	// the graph discloses what it removed rather than quietly losing it. The
	// per-type rollup underneath has to agree with the legs, per currency.
	var graph models.MoneyFlowGraph
	status, body = a.request(http.MethodGet,
		"/api/v1/dashboard/money-flow?dateFrom=2024-05-01&dateTo=2024-05-31", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &graph))
	require.Len(t, graph.SuppressedCycles, 1)
	require.Equal(t, "reciprocal", graph.SuppressedCycles[0].Kind)
	require.Len(t, graph.SuppressedCycles[0].Legs, 2)
	// Netting cancels each currency against its own reverse, so one leg survives as
	// a real edge and the other cannot cancel it and is dropped.
	require.Len(t, graph.SuppressedCycles[0].Legs, 2)
	var gross, discarded models.CurrencyAmounts
	for _, leg := range graph.SuppressedCycles[0].Legs {
		require.Len(t, leg.Gross, 1, "each leg is one direction, so it is one currency")
		gross = sumPerCurrency(gross, leg.Gross)
		discarded = sumPerCurrency(discarded, leg.Discarded)
	}
	require.Equal(t, cycle.Gross, gross, "the graph and the report name the same money, per currency")

	var accountEdges []models.MoneyFlowEdge
	for _, e := range graph.Links {
		if strings.HasPrefix(e.Source, "account:") && strings.HasPrefix(e.Target, "account:") {
			accountEdges = append(accountEdges, e)
		}
	}
	require.Len(t, accountEdges, 1)
	require.Len(t, accountEdges[0].Value, 1, "the surviving edge is one currency, never a sum of both")
	require.Len(t, discarded, 1, "exactly one currency could not be netted away")

	// The drawn currency and the discarded one partition the cycle's two
	// currencies: the same money, accounted for once each way, with nothing
	// reduced and nothing added. Which currency is drawn is the cycle-break
	// choosing a back edge and is not stable between two identical requests, so
	// it is deliberately not asserted; the partition is.
	accounted := map[string]bool{}
	for code, amount := range accountEdges[0].Value {
		accounted[code] = true
		require.Equal(t, cycle.Gross[code], amount, "the drawn edge is that currency's own leg, unreduced")
	}
	for code, amount := range discarded {
		require.False(t, accounted[code], "currency %s cannot be both drawn and discarded", code)
		require.Equal(t, cycle.Gross[code], amount, "the disclosure is that currency's own leg, whole")
		accounted[code] = true
	}
	require.Equal(t, cycle.Gross.Currencies(), sortedCodes(accounted),
		"every currency of the cycle is either drawn or disclosed as dropped")

	require.Len(t, graph.LinkSummary, 1)
	require.Equal(t, "transfer", graph.LinkSummary[0].Type)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(100), "USD": money.FromFloat(40)},
		graph.LinkSummary[0].Total, "the rollup is grouped by the currency of the value for the same reason")
	require.Equal(t, 2, graph.LinkSummary[0].Count)
	requireNoScalarAmounts(t, "/dashboard/money-flow", body)
}

// TestIntegrationCurrencyFilterNarrowsEveryReportingEndpoint walks the
// ?currency= predicate across all five reporting endpoints, because no amount
// check can cover it: a filter that reached only some of them would leave a
// response whose totals and breakdowns describe different sets of transactions,
// which is the silent-wrong-number case the predicate exists to prevent.
//
// The summary is asserted through both of its views, since they build their
// filter fragments separately.
func TestIntegrationCurrencyFilterNarrowsEveryReportingEndpoint(t *testing.T) {
	a := newAPIClient(t)
	a.register("narrowing@example.com")

	inr := a.createAccountIn("Rupee Bank", "bank", "INR", nil)
	usd := a.createAccountIn("Dollar Bank", "bank", "USD", nil)
	card := a.createAccountIn("Rupee Card", "credit_card", "INR", billingDayPtr(15))
	cats := a.categories()
	groceries := categoryByName(t, cats, "Groceries")
	salary := categoryByName(t, cats, "Salary")

	a.createTransaction(inr.ID, &salary.ID, "2024-06-01", "June salary", 5000, "credit")
	a.createTransaction(inr.ID, &groceries.ID, "2024-06-02", "Big Bazaar", 1500, "debit")
	a.createTransaction(usd.ID, &groceries.ID, "2024-06-03", "Whole Foods", 200, "debit")
	a.createTransaction(card.ID, nil, "2024-06-15", "Card spend", 300, "debit")

	const window = "dateFrom=2024-06-01&dateTo=2024-06-30"
	usdExpense := models.CurrencyAmounts{"USD": money.FromFloat(200)}

	// Dashboard summary, month view. Every transaction-backed section has to
	// narrow: the totals, the count, both category breakdowns, the trend and the
	// recent-transaction list, or the response describes two different windows.
	var summary models.DashboardSummary
	status, body := a.request(http.MethodGet, "/api/v1/dashboard/summary?"+window+"&currency=USD", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &summary))
	require.Equal(t, []string{"USD"}, summary.CurrencyScope.Currencies)
	require.Equal(t, models.NewCurrencyAmounts(), summary.TotalIncome)
	require.Equal(t, usdExpense, summary.TotalExpense)
	require.Equal(t, 1, summary.TotalTransactions, "the count is transaction-backed, so the filter reaches it too")
	require.Len(t, summary.ByCategory, 1)
	require.Equal(t, "Groceries", summary.ByCategory[0].CategoryName)
	require.Equal(t, usdExpense, summary.ByCategory[0].Total)
	require.Empty(t, summary.IncomeByCategory, "the INR salary must not survive a USD filter")
	require.Len(t, summary.MonthlyTrend, 1)
	require.Equal(t, usdExpense, summary.MonthlyTrend[0].Expense)
	require.Len(t, summary.RecentTransactions, 1)
	require.Equal(t, "Whole Foods", summary.RecentTransactions[0].Description)
	// totalAccounts is a plain COUNT(*) over the user's accounts and is documented
	// as not narrowed, so it stays 3 here. Pinned because the exception is stated
	// in the spec and a future "fix" that narrows it would change a documented
	// answer rather than repair a wrong one.
	require.Equal(t, 3, summary.TotalAccounts, "the account count is documented as not narrowed by ?currency=")
	requireNoScalarAmounts(t, "/dashboard/summary?currency=USD", body)

	// The same view, lower case. Case is folded rather than compared literally,
	// because the failure would be invisible: "usd" binding against 'USD' matches
	// nothing and reports zeros with no error anywhere.
	status, body = a.request(http.MethodGet, "/api/v1/dashboard/summary?"+window+"&currency=usd", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &summary))
	require.Equal(t, usdExpense, summary.TotalExpense)

	// A code that is not three letters is a 400, because ignoring it would be
	// the same silence in another shape.
	status, _ = a.request(http.MethodGet, "/api/v1/dashboard/summary?"+window+"&currency=us", nil)
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = a.request(http.MethodGet, "/api/v1/dashboard/summary?"+window+"&currency=us1", nil)
	require.Equal(t, http.StatusBadRequest, status)

	// Dashboard summary, billing-cycle view. It builds its own filter fragment, so
	// it is a separate statement from the month view's.
	var cycleSummary models.DashboardSummary
	status, body = a.request(http.MethodGet,
		"/api/v1/dashboard/summary?groupBy=billing_cycle&accountId="+card.ID.String()+"&currency=USD", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &cycleSummary))
	require.Equal(t, models.NewCurrencyAmounts(), cycleSummary.TotalExpense)
	require.Equal(t, 0, cycleSummary.TotalTransactions)
	require.Empty(t, cycleSummary.CurrencyScope.Currencies)

	// The four endpoints that reach their currency through flowFilter rather than
	// through the summary's own fragment. Two of them carry a documented partial
	// narrowing, asserted here so a change to either is a test failure rather than
	// a documentation drift.
	var graph models.MoneyFlowGraph
	status, body = a.request(http.MethodGet, "/api/v1/dashboard/money-flow?"+window+"&currency=USD", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &graph))
	require.Equal(t, []string{"USD"}, graph.CurrencyScope.Currencies)
	require.Equal(t, usdExpense, graph.TotalExpense)
	require.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(200)},
		nodeTotal(t, graph.Nodes, "account:"+usd.ID.String()))
	require.Nil(t, findNode(graph.Nodes, "account:"+inr.ID.String()),
		"an INR account is out of a USD window, so it is not drawn")

	var timeline models.MoneyFlowTimeline
	status, body = a.request(http.MethodGet, "/api/v1/dashboard/money-flow/timeline?"+window+"&currency=USD", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &timeline))
	require.Equal(t, []string{"USD"}, timeline.CurrencyScope.Currencies)
	require.Len(t, timeline.Periods, 1)
	require.Equal(t, usdExpense, timeline.Periods[0].Expense)

	var calendar models.CashFlowCalendar
	status, body = a.request(http.MethodGet, "/api/v1/dashboard/cash-flow-calendar?"+window+"&currency=USD", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &calendar))
	require.Equal(t, []string{"USD"}, calendar.CurrencyScope.Currencies)
	require.Len(t, calendar.Days, 1)
	require.Equal(t, "2024-06-03", calendar.Days[0].Date)
	// The one USD day only ever spent, so its net is the negative of its expense.
	require.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(-200)}, calendar.Days[0].Net)
	require.Equal(t, usdExpense, calendar.Days[0].Expense)

	var cycles models.LinkCycleReport
	status, body = a.request(http.MethodGet, "/api/v1/links/cycles?"+window+"&currency=USD", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &cycles))
	require.Equal(t, []string{"USD"}, cycles.CurrencyScope.Currencies)

	// The unfiltered INR window is the complement, and it is here so a predicate
	// that ignored the filter entirely could not pass by matching the INR side.
	var inrCalendar models.CashFlowCalendar
	status, body = a.request(http.MethodGet, "/api/v1/dashboard/cash-flow-calendar?"+window+"&currency=INR", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &inrCalendar))
	require.Equal(t, []string{"INR"}, inrCalendar.CurrencyScope.Currencies)
	require.Len(t, inrCalendar.CurrencyScope.Accounts, 2)
	require.Len(t, inrCalendar.Days, 3)
	require.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(1800)}, inrCalendar.TotalExpense)
}

// sortedCodes returns a code set in sorted order, so a test can compare it with
// CurrencyAmounts.Currencies without depending on map iteration order.
func sortedCodes(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for code := range set {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

// legCurrencies returns the currencies of a cycle's legs, deduplicated and
// sorted, so a test can state which currencies a cycle spans.
func legCurrencies(legs []models.LinkCycleLeg) []string {
	seen := map[string]bool{}
	for _, leg := range legs {
		for code := range leg.Amount {
			seen[code] = true
		}
	}
	out := make([]string, 0, len(seen))
	for code := range seen {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

// TestIntegrationDocumentedCurrencyFilterExceptions covers the three places
// ?currency= deliberately does not narrow everything, as one claim.
//
// The filter's default is to narrow the whole response, which is why the
// exception is worth stating three times over: a caller who reads "narrow the
// response" and then finds an un-narrowed figure has been misled, and each of
// these three is a *documented* answer rather than an accident. All three were
// previously argued from the source - the filter answered 500 on the summary, so
// the first of them could not even be observed - and a documentation review is
// not a test.
//
//	1. /dashboard/summary's totalAccounts is a plain COUNT(*) over the user's
//	   accounts and is not narrowed. AGENTS.md says so; openapi.yaml types the
//	   field and says nothing, which is a gap in the machine-readable contract
//	   noted in the report.
//	2. /dashboard/money-flow narrows its two link stages by the currency the
//	   link's *amount* is denominated in, not by either endpoint's account, so a
//	   link is dropped even when one endpoint holds the requested currency.
//	3. /dashboard/cash-flow-calendar's cycles and markers overlays are not
//	   narrowed at all.
func TestIntegrationDocumentedCurrencyFilterExceptions(t *testing.T) {
	a := newAPIClient(t)
	a.register("exceptions@example.com")

	inr := a.createAccountIn("Rupee Bank", "bank", "INR", nil)
	usd := a.createAccountIn("Dollar Bank", "bank", "USD", nil)
	card := a.createAccountIn("Rupee Card", "credit_card", "INR", billingDayPtr(15))
	cats := a.categories()
	groceries := categoryByName(t, cats, "Groceries")

	a.createTransaction(inr.ID, &groceries.ID, "2024-06-02", "Big Bazaar", 1500, "debit")
	a.createTransaction(usd.ID, &groceries.ID, "2024-06-03", "Whole Foods", 200, "debit")
	a.createTransaction(card.ID, nil, "2024-06-15", "Card spend", 300, "debit")

	// A link whose *amount* is denominated in USD: the debit leg is on the USD
	// account, so linkCurrencyColumn picks fa.currency and the value is USD even
	// though the other endpoint is the INR account holding the INR money.
	usdLeg := a.createTransaction(usd.ID, nil, "2024-06-04", "Dollars out", 60, "debit")
	inrLeg := a.createTransaction(inr.ID, nil, "2024-06-04", "Rupees in", 60, "credit")
	a.call(http.MethodPost, "/api/v1/links", map[string]any{
		"type": "transfer", "fromTxnId": usdLeg, "toTxnId": inrLeg,
	}, http.StatusCreated, nil)

	const window = "dateFrom=2024-06-01&dateTo=2024-06-30"

	// (1) totalAccounts is not narrowed. Asserted as a contrast against the same
	// request without the filter rather than as a bare constant, so it fails both
	// ways: if the count starts following the filter, and if it starts counting
	// something other than the user's accounts.
	var unfiltered, usdFiltered models.DashboardSummary
	status, body := a.request(http.MethodGet, "/api/v1/dashboard/summary?"+window, nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &unfiltered))
	status, body = a.request(http.MethodGet, "/api/v1/dashboard/summary?"+window+"&currency=USD", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &usdFiltered))

	require.Equal(t, 3, unfiltered.TotalAccounts, "three accounts, one of each kind")
	require.Equal(t, unfiltered.TotalAccounts, usdFiltered.TotalAccounts,
		"totalAccounts is a plain COUNT(*) over the user's accounts and ?currency= does not narrow it")
	// Everything beside it does narrow, which is what makes the exception a real
	// exception rather than the filter being inert.
	require.NotEqual(t, unfiltered.TotalTransactions, usdFiltered.TotalTransactions)
	// Five transactions in the window: two on the INR account, two on the USD one
	// (the spend and the link's debit leg), one on the card.
	require.Equal(t, 5, unfiltered.TotalTransactions)
	require.Equal(t, 2, usdFiltered.TotalTransactions)
	// The USD window holds the 200 spend plus the link's own 60 debit leg, which
	// is an ordinary transaction and counts here however it is linked.
	require.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(260)}, usdFiltered.TotalExpense)
	require.Equal(t, []string{"USD"}, usdFiltered.CurrencyScope.Currencies)

	// (2) A link is in scope by the currency of its amount, not by either
	// endpoint's account. The USD request must drop it, because its value is USD
	// and the USD account's own money is not what a USD filter picks it by - it
	// is the INR credit it is paired with that makes the trap: admitting the link
	// because *one* endpoint holds the currency would report its amount in the
	// account the value did not come from.
	var usdGraph, inrGraph models.MoneyFlowGraph
	status, body = a.request(http.MethodGet, "/api/v1/dashboard/money-flow?"+window+"&currency=USD", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &usdGraph))
	status, body = a.request(http.MethodGet, "/api/v1/dashboard/money-flow?"+window+"&currency=INR", nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &inrGraph))

	transferTotal := func(g models.MoneyFlowGraph) models.CurrencyAmounts {
		for _, s := range g.LinkSummary {
			if s.Type == "transfer" {
				return s.Total
			}
		}
		return models.NewCurrencyAmounts()
	}
	// Unfiltered, the link is valued in USD - the currency of the account the
	// amount came from, which is not the currency of the account it flows into.
	require.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(60)}, transferTotal(unfilteredGraph(t, a, window)))
	require.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(60)}, transferTotal(usdGraph),
		"a USD filter keeps a link whose amount is USD, even though its other endpoint is INR")
	require.Equal(t, models.NewCurrencyAmounts(), transferTotal(inrGraph),
		"an INR filter drops a link whose amount is USD, even though its other endpoint is INR")

	// (3) The calendar overlays are not narrowed. Proven here alongside the other
	// two so the three exceptions are one claim; the marker-by-marker reading is
	// in TestIntegrationCalendarOverlaysIgnoreTheCurrencyFilter.
	var cal models.CashFlowCalendar
	status, body = a.request(http.MethodGet,
		"/api/v1/dashboard/cash-flow-calendar?"+window+"&currency=USD&accountId="+card.ID.String(), nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &cal))
	require.Empty(t, cal.Days)
	require.Empty(t, cal.CurrencyScope.Currencies)
	require.NotEmpty(t, cal.Cycles)
	require.NotEmpty(t, cal.Markers)
	require.Equal(t, plainCalendarCycles(t, a, window, card.ID), cal.Cycles,
		"the overlay is identical to the unfiltered one: ?currency= did not narrow it")
}

// unfilteredGraph reads the money-flow graph with no currency filter, for the
// link-stage assertions that need the same window three ways.
func unfilteredGraph(t *testing.T, a *apiClient, window string) models.MoneyFlowGraph {
	t.Helper()
	var graph models.MoneyFlowGraph
	status, body := a.request(http.MethodGet, "/api/v1/dashboard/money-flow?"+window, nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &graph))
	return graph
}

// plainCalendarCycles reads the calendar with no currency filter and returns its
// cycles, which is what the USD request has to reproduce exactly to show the
// overlay was untouched.
func plainCalendarCycles(t *testing.T, a *apiClient, window string, accountID uuid.UUID) []models.CashFlowCalendarCycle {
	t.Helper()
	var cal models.CashFlowCalendar
	status, body := a.request(http.MethodGet,
		"/api/v1/dashboard/cash-flow-calendar?"+window+"&accountId="+accountID.String(), nil)
	require.Equal(t, http.StatusOK, status, "body: %s", body)
	require.NoError(t, json.Unmarshal(body, &cal))
	return cal.Cycles
}
