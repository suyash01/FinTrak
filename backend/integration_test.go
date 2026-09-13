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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/fintrak/backend/config"
	"github.com/fintrak/backend/db"
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

func (a *apiClient) register(email string) {
	a.t.Helper()
	a.call(http.MethodPost, "/api/v1/auth/register", map[string]string{
		"email":    email,
		"password": "integration-pass-123",
	}, http.StatusCreated, nil)
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
