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
	"os"
	"sort"
	"sync"
	"testing"
	"time"

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

	require.Equal(t, money.FromFloat(5000), graph.TotalIncome)
	require.Equal(t, money.FromFloat(1500), graph.TotalExpense)

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
	require.Equal(t, money.FromFloat(5000), all.Days[0].Net)
	require.Equal(t, "2024-06-04", all.Days[1].Date)
	require.Equal(t, money.FromFloat(-1500), all.Days[1].Net)
	require.Equal(t, money.FromFloat(3500), all.Net)
	require.Equal(t, money.FromFloat(5000), all.MaxAbsNet)
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
			require.Equal(t, money.FromFloat(500), l.Value)
		}
	}
	require.True(t, found, "expected a checking -> savings account edge")

	var timeline models.MoneyFlowTimeline
	a.call(http.MethodGet, "/api/v1/dashboard/money-flow/timeline?dateFrom=2024-06-01&dateTo=2024-06-30", nil, http.StatusOK, &timeline)
	require.Len(t, timeline.Periods, 1)
	require.Equal(t, "2024-06", timeline.Periods[0].Key)
	require.Equal(t, money.FromFloat(500), timeline.Periods[0].Income)
	require.Equal(t, money.FromFloat(500), timeline.Periods[0].Expense)
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
	require.Equal(t, money.FromFloat(3000), cycle.Net)
	require.Equal(t, money.FromFloat(11000), cycle.Gross)
	require.Equal(t, money.FromFloat(3000), report.TotalCircular)

	require.Len(t, report.OneSidedFlows, 1)
	flow := report.OneSidedFlows[0]
	require.Equal(t, checking.ID.String(), flow.FromAccountID)
	require.Equal(t, savings.ID.String(), flow.ToAccountID)
	require.Equal(t, money.FromFloat(1500), flow.Total)
	require.Equal(t, 1, flow.Count)
	require.Len(t, flow.Types, 1)
	require.Equal(t, "transfer", flow.Types[0].Type)
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
