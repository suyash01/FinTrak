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
	"sync"
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

	var bundle models.BackupBundle
	alice.call(http.MethodGet, "/api/v1/export", nil, http.StatusOK, &bundle)
	require.Equal(t, models.BackupFormat, bundle.Format)
	require.Len(t, bundle.Accounts, 3)
	require.Len(t, bundle.Transactions, 3)
	require.Len(t, bundle.Links, 1)
	require.Len(t, bundle.LoanAttachments, 1)
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
	require.Equal(t, 1, result.RecurringSeries)
	require.Equal(t, len(bundle.BillingCycles), result.BillingCycles)
	require.Empty(t, result.Warnings)

	var bobAccounts []models.Account
	bob.call(http.MethodGet, "/api/v1/accounts", nil, http.StatusOK, &bobAccounts)
	require.Len(t, bobAccounts, 3)

	// Matching by name+group reused every seeded default category instead of
	// duplicating it.
	require.Equal(t, categoriesBefore, len(bob.categories()))

	var bankID, cardID uuid.UUID
	for _, acc := range bobAccounts {
		switch acc.Name {
		case "Checking":
			bankID = acc.ID
		case "Card":
			cardID = acc.ID
		}
	}
	require.NotEqual(t, uuid.Nil, bankID)
	require.NotEqual(t, uuid.Nil, cardID)
	require.Len(t, bob.transactions(bankID), 2)

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

