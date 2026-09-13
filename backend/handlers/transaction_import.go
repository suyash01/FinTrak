package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ImportTransactions batch-inserts a validated list of transactions for an
// account. It enforces payload bounds and ownership of the account, billing
// cycle, and any explicit payees, deduplicates rows when duplicateAction is
// "skip", applies categorization rules in memory, and commits everything in one
// transaction. Credit-card imports are attached to billing cycles, and source
// Paperless documents are tagged only after the commit succeeds.
func (srv *Server) ImportTransactions(c *gin.Context) {
	var req models.ImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	userID := auth.GetUserID(c)

	// Validate payload before touching the database.
	if len(req.Transactions) == 0 {
		validation.RespondError(c, "no transactions to import", http.StatusBadRequest)
		return
	}
	if len(req.Transactions) > maxImportBatch {
		validation.RespondError(c, fmt.Sprintf("too many transactions (max %d per import)", maxImportBatch), http.StatusBadRequest)
		return
	}
	action := req.DuplicateAction
	if action != "" && action != "skip" && action != "keep" {
		validation.RespondError(c, "duplicateAction must be 'skip' or 'keep'", http.StatusBadRequest)
		return
	}
	for i, t := range req.Transactions {
		if t.Type != "debit" && t.Type != "credit" {
			validation.RespondError(c, fmt.Sprintf("transaction %d has invalid type '%s' (must be 'debit' or 'credit')", i+1, t.Type), http.StatusBadRequest)
			return
		}
		if t.Amount <= 0 {
			validation.RespondError(c, fmt.Sprintf("transaction %d has invalid amount %v", i+1, t.Amount), http.StatusBadRequest)
			return
		}
		if _, err := time.Parse("2006-01-02", t.Date); err != nil {
			validation.RespondError(c, fmt.Sprintf("transaction %d has invalid date '%s' (expected YYYY-MM-DD)", i+1, t.Date), http.StatusBadRequest)
			return
		}
	}

	// The account must exist and belong to the authenticated user. Also fetch its
	// billing day (so imports can be attached to a billing cycle), its closed
	// flag, and its account type (loan accounts hold no transactions of their
	// own).
	var ownerID uuid.UUID
	var billingDay *int
	var closed bool
	var accountTypeID string
	err := srv.db.QueryRow(c,
		"SELECT user_id, billing_day, closed, account_type_id FROM accounts WHERE id = $1",
		req.AccountID).Scan(&ownerID, &billingDay, &closed, &accountTypeID)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("ImportTransactions (checking account)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if ownerID != userID {
		validation.RespondError(c, "forbidden", http.StatusForbidden)
		return
	}
	if closed {
		validation.RespondError(c, "account is closed — no transactions can be added", http.StatusConflict)
		return
	}
	if accountTypeID == loanAccountTypeID {
		validation.RespondError(c, "loan accounts cannot have transactions — link EMI payments from other accounts instead", http.StatusBadRequest)
		return
	}

	// Validate that any explicitly supplied billing cycle and payees belong to
	// this user, so a client can't import against another user's records.
	if req.BillingCycleID != nil {
		var owned bool
		err := srv.db.QueryRow(c,
			"SELECT EXISTS(SELECT 1 FROM billing_cycles bc WHERE bc.id = $1 AND bc.user_id = $2)",
			*req.BillingCycleID, userID).Scan(&owned)
		if err != nil {
			slog.Error("ImportTransactions (checking billing cycle)", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		if !owned {
			validation.RespondError(c, "billing cycle not found", http.StatusBadRequest)
			return
		}
	}

	// Collect the distinct explicitly supplied payees (rules-derived payees are
	// user-scoped already) and confirm every one belongs to this user.
	payeeIDs := make([]uuid.UUID, 0, len(req.Transactions))
	seen := map[uuid.UUID]bool{}
	for _, t := range req.Transactions {
		if t.PayeeID != nil && !seen[*t.PayeeID] {
			seen[*t.PayeeID] = true
			payeeIDs = append(payeeIDs, *t.PayeeID)
		}
	}
	if len(payeeIDs) > 0 {
		var owned int
		err := srv.db.QueryRow(c,
			"SELECT COUNT(*) FROM payees WHERE id = ANY($1) AND user_id = $2",
			payeeIDs, userID).Scan(&owned)
		if err != nil {
			slog.Error("ImportTransactions (checking payees)", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		if owned != len(payeeIDs) {
			validation.RespondError(c, "referenced payee not found", http.StatusBadRequest)
			return
		}
	}

	// Load rules once and match in memory to avoid N+1 queries.
	rules, err := srv.loadRules(c, userID)
	if err != nil {
		slog.Error("ImportTransactions (getting rules)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Run the whole import in a transaction so it is all-or-nothing.
	tx, err := srv.db.Begin(c)
	if err != nil {
		slog.Error("ImportTransactions (begin)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(c)

	// When the user asks to skip duplicates, load the existing transactions for
	// this account so we can compare against a consistent snapshot. The lookup is
	// scoped to the batch's dates so it never scans the account's whole history.
	existing := map[string]bool{}
	if action == "skip" {
		existing, err = loadExistingFingerprints(c, tx, req.AccountID, userID, req.Transactions)
		if err != nil {
			slog.Error("ImportTransactions (loading existing transactions)", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	// If the user chose to skip duplicates, drop the rows that match an existing
	// transaction or repeat earlier in the same batch.
	insertTxns := req.Transactions
	duplicates := 0
	if action == "skip" {
		insertTxns, duplicates = dedupeTransactions(req.Transactions, existing)
	}

	batch := &pgx.Batch{}
	imported := 0
	for _, t := range insertTxns {
		categoryID, payeeID := autoCategorize(rules, t.Description)

		// If a rule gives no payee, fall back to the payee matched during import.
		if payeeID == nil && t.PayeeID != nil {
			payeeID = t.PayeeID
		}

		batch.Queue(
			`INSERT INTO transactions (account_id, user_id, date, description, amount, type, category_id, payee_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			req.AccountID, userID, t.Date, t.Description, t.Amount, t.Type, categoryID, payeeID,
		)
		imported++
	}

	if imported > 0 {
		br := tx.SendBatch(c, batch)
		ids := make([]uuid.UUID, 0, imported)
		done := 0
		for done < imported {
			var id uuid.UUID
			if err := br.QueryRow().Scan(&id); err != nil {
				br.Close()
				slog.Error("ImportTransactions", slog.String("error", err.Error()))
				validation.RespondError(c, "internal server error", http.StatusInternalServerError)
				return
			}
			ids = append(ids, id)
			done++
		}
		br.Close()

		// Imports on accounts with a billing day: attach the new transactions
		// to their billing cycles. When the client chose an explicit cycle,
		// every imported transaction is attached to it (overriding the
		// date-based default); otherwise the suggested default (by transaction
		// date) applies. Runs inside the transaction so the assignment commits
		// atomically with the import.
		if billingDay != nil {
			if req.BillingCycleID != nil {
				if err := attachTransactionsToCycle(c, tx, *req.BillingCycleID, ids, userID); err != nil {
					slog.Error("ImportTransactions (set billing cycle)", slog.String("error", err.Error()))
					validation.RespondError(c, "internal server error", http.StatusInternalServerError)
					return
				}
			}
			// Cycles are only generated for accounts that have a billing day
			// set; the date-based default can't apply to accounts without one.
			if err := ensureBillingCycles(c, tx, userID, req.AccountID, *billingDay); err != nil {
				slog.Error("ImportTransactions (ensure billing cycles)", slog.String("error", err.Error()))
				validation.RespondError(c, "internal server error", http.StatusInternalServerError)
				return
			}
		}

		if err := tx.Commit(c); err != nil {
			slog.Error("ImportTransactions (commit)", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	// Tag the source Paperless documents only after the import has committed, so
	// the label reflects documents that were actually imported. Best-effort and
	// asynchronous: tagging costs up to four upstream HTTP round-trips per
	// document, so it must neither stall the response nor fail the import if
	// the user's Paperless instance is slow or unreachable. The detached
	// context outlives the request; tagPaperlessDocuments applies its own
	// overall timeout and bounded concurrency.
	if len(req.PaperlessDocumentIDs) > 0 {
		go srv.tagPaperlessDocuments(context.Background(), userID, req.PaperlessDocumentIDs, tokenEncryptionKey, appEnv)
	}

	c.JSON(http.StatusOK, gin.H{
		"imported":   imported,
		"duplicates": duplicates,
		"total":      len(req.Transactions),
	})
	slog.Info("import complete", slog.Int("imported", imported), slog.Int("duplicates", duplicates), slog.Int("total", len(req.Transactions)), slog.String("account_id", req.AccountID.String()))
}

// transactionFingerprint collapses a row into a stable value used for duplicate
// detection. Amounts are compared as integer cents so penny rounding and float
// noise don't produce false matches.
func transactionFingerprint(date string, amount money.Amount, typ, description string) string {
	cents := amount.Cents()
	return fmt.Sprintf("%s\x00%d\x00%s\x00%s", date, cents, typ, strings.ToLower(strings.TrimSpace(description)))
}

// transactionQueryer is the minimal query surface needed to load a
// duplicate-detection snapshot. Both *pgxpool.Pool (via db.DBPool) and pgx.Tx
// satisfy it.
type transactionQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// transactionDates returns the distinct dates present in a batch, sorted
// ascending. Duplicate detection is scoped to these dates because the
// fingerprint embeds the date, so rows on other dates can never match.
func transactionDates(txns []models.ImportTransaction) []time.Time {
	seen := map[string]time.Time{}
	for _, t := range txns {
		if _, ok := seen[t.Date]; ok {
			continue
		}
		if d, err := time.Parse("2006-01-02", t.Date); err == nil {
			seen[t.Date] = d
		}
	}
	dates := make([]time.Time, 0, len(seen))
	for _, d := range seen {
		dates = append(dates, d)
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })
	return dates
}

// loadExistingFingerprints builds the fingerprint set used for duplicate
// detection for the given account. It only loads transactions whose date is in
// the batch, so the work is bounded by the import window rather than the
// account's entire history.
func loadExistingFingerprints(ctx context.Context, q transactionQueryer, accountID, userID uuid.UUID, txns []models.ImportTransaction) (map[string]bool, error) {
	existing := map[string]bool{}
	dates := transactionDates(txns)
	if len(dates) == 0 {
		return existing, nil
	}

	rows, err := q.Query(ctx,
		"SELECT date, amount, type, description FROM transactions WHERE account_id = $1 AND user_id = $2 AND date = ANY($3)",
		accountID, userID, dates)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var d time.Time
		var amount money.Amount
		var typ, description string
		if err := rows.Scan(&d, &amount, &typ, &description); err != nil {
			return nil, err
		}
		existing[transactionFingerprint(d.Format("2006-01-02"), amount, typ, description)] = true
	}
	return existing, rows.Err()
}

// ValidateTransactions is a read-only check that reports which of the given
// candidate transactions already exist in the selected account. It reuses the
// same fingerprint matching as ImportTransactions (so the results agree with
// what an import with duplicateAction "skip" would drop) but writes nothing.
func (srv *Server) ValidateTransactions(c *gin.Context) {
	var req models.ValidateTransactionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	userID := auth.GetUserID(c)

	if len(req.Transactions) == 0 {
		validation.RespondError(c, "no transactions to validate", http.StatusBadRequest)
		return
	}
	if len(req.Transactions) > maxImportBatch {
		validation.RespondError(c, fmt.Sprintf("too many transactions (max %d per request)", maxImportBatch), http.StatusBadRequest)
		return
	}
	for i, t := range req.Transactions {
		if t.Type != "debit" && t.Type != "credit" {
			validation.RespondError(c, fmt.Sprintf("transaction %d has invalid type '%s' (must be 'debit' or 'credit')", i+1, t.Type), http.StatusBadRequest)
			return
		}
		if t.Amount <= 0 {
			validation.RespondError(c, fmt.Sprintf("transaction %d has invalid amount %v", i+1, t.Amount), http.StatusBadRequest)
			return
		}
		if _, err := time.Parse("2006-01-02", t.Date); err != nil {
			validation.RespondError(c, fmt.Sprintf("transaction %d has invalid date '%s' (expected YYYY-MM-DD)", i+1, t.Date), http.StatusBadRequest)
			return
		}
	}

	// The account must exist and belong to the authenticated user.
	var ownerID uuid.UUID
	err := srv.db.QueryRow(c,
		"SELECT user_id FROM accounts WHERE id = $1",
		req.AccountID).Scan(&ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("ValidateTransactions (checking account)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if ownerID != userID {
		validation.RespondError(c, "forbidden", http.StatusForbidden)
		return
	}

	// Load the account's existing transactions into a fingerprint set so each
	// candidate can be compared in memory. Scoped to the candidate dates so the
	// query stays bounded by the import window rather than the full history.
	existing, err := loadExistingFingerprints(c, srv.db, req.AccountID, userID, req.Transactions)
	if err != nil {
		slog.Error("ValidateTransactions (loading existing transactions)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	results := make([]models.ValidateTransactionResult, 0, len(req.Transactions))
	existingCount := 0
	for i, t := range req.Transactions {
		exists := existing[transactionFingerprint(t.Date, t.Amount, t.Type, t.Description)]
		if exists {
			existingCount++
		}
		results = append(results, models.ValidateTransactionResult{
			Index:       i,
			Exists:      exists,
			Date:        t.Date,
			Description: t.Description,
			Amount:      t.Amount,
			Type:        t.Type,
		})
	}

	c.JSON(http.StatusOK, models.ValidateTransactionsResponse{
		Total:         len(req.Transactions),
		ExistingCount: existingCount,
		MissingCount:  len(req.Transactions) - existingCount,
		Results:       results,
	})
}

// attachTransactionsToCycle attaches the given transaction IDs to a billing
// cycle. Used by credit-card imports when the client chose an explicit cycle so
// every imported transaction lands in it, overriding the date-based default.
func attachTransactionsToCycle(ctx context.Context, q cycleQueryer, cycleID uuid.UUID, ids []uuid.UUID, userID uuid.UUID) error {
	_, err := q.Exec(ctx,
		"UPDATE transactions SET billing_cycle_id = $1 WHERE id = ANY($2) AND user_id = $3",
		cycleID, ids, userID)
	return err
}

// dedupeTransactions keeps the first occurrence of every unique row, dropping
// rows that match an existing transaction or repeat earlier in the batch. It
// returns the rows to insert and the number of duplicates dropped.
func dedupeTransactions(txns []models.ImportTransaction, existing map[string]bool) ([]models.ImportTransaction, int) {
	seen := make(map[string]bool, len(txns))
	kept := make([]models.ImportTransaction, 0, len(txns))
	duplicates := 0
	for _, t := range txns {
		fp := transactionFingerprint(t.Date, t.Amount, t.Type, t.Description)
		if existing[fp] || seen[fp] {
			duplicates++
			continue
		}
		seen[fp] = true
		kept = append(kept, t)
	}
	return kept, duplicates
}

// autoCategorize returns the category (and optional payee) of the first rule
// matching the transaction description, or nil/nil when no rule matches. Rules
// are expected to be pre-sorted by descending priority.
func autoCategorize(rules []ruleEntry, description string) (*uuid.UUID, *uuid.UUID) {
	for _, r := range rules {
		if matchRule(description, r.Pattern, r.MatchType) {
			return &r.CatID, r.PayeeID
		}
	}
	return nil, nil
}
