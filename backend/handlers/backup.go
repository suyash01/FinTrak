package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// maxBackupBytes bounds an uploaded backup bundle. A large personal finance
// history fits comfortably; the cap keeps a hostile body from exhausting
// memory, since the whole bundle is decoded before the restore transaction.
const maxBackupBytes = 256 << 20

// backupInsertChunk caps the rows per multi-row INSERT so a restore stays well
// under PostgreSQL's 65535-parameter limit.
const backupInsertChunk = 500

// maxBackupWarnings caps the warning list returned by an import so a corrupt
// bundle cannot produce an unbounded response.
const maxBackupWarnings = 100

// backupError is a business-rule failure that maps to a specific HTTP status
// (e.g. importing into a non-empty user, or a missing account type).
type backupError struct {
	status int
	msg    string
}

func (e *backupError) Error() string { return e.msg }

// ExportUserData downloads a complete JSON backup of everything the
// authenticated user owns. The account CSV export covers one account's
// transactions; this covers the user's whole graph (accounts, categories,
// payees, billing cycles, transactions, links, loan/recurring attachments,
// rules and non-secret settings) so it can be restored elsewhere.
func (srv *Server) ExportUserData(c *gin.Context) {
	b, err := buildUserBackup(c, srv.db, auth.GetUserID(c))
	if err != nil {
		slog.Error("ExportUserData", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="fintrak-backup-%s.json"`, time.Now().Format("2006-01-02")))
	c.JSON(http.StatusOK, b)
}

// ImportUserData restores a user backup bundle. Restores are all-or-nothing
// and only into a user that has no accounts yet: merging a foreign bundle into
// existing data is ambiguous (which account/category/payee is "the same"?), so
// the endpoint refuses rather than guessing. Every reference is remapped to a
// freshly minted ID.
func (srv *Server) ImportUserData(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBackupBytes)

	var bundle models.BackupBundle
	if err := json.NewDecoder(c.Request.Body).Decode(&bundle); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			validation.RespondError(c, "backup file is too large", http.StatusRequestEntityTooLarge)
			return
		}
		validation.RespondError(c, "invalid backup file", http.StatusBadRequest)
		return
	}

	if bundle.Format != models.BackupFormat {
		validation.RespondError(c, "not a FinTrak backup file", http.StatusBadRequest)
		return
	}
	if bundle.Version < 1 || bundle.Version > models.BackupVersion {
		validation.RespondError(c, fmt.Sprintf("unsupported backup version %d", bundle.Version), http.StatusBadRequest)
		return
	}

	var result models.BackupImportResult
	err := db.WithTx(c, srv.db, func(tx pgx.Tx) error {
		return restoreUserBackup(c, tx, auth.GetUserID(c), &bundle, &result)
	})
	if err != nil {
		var be *backupError
		if errors.As(err, &be) {
			validation.RespondError(c, be.msg, be.status)
			return
		}
		slog.Error("ImportUserData", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, result)
}

// exportUserRows runs a query and invokes scan for every row, closing the rows
// and surfacing iteration errors.
func exportUserRows(ctx context.Context, pool db.DBPool, query string, args []any, scan func(pgx.Rows) error) error {
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// buildUserBackup gathers the whole bundle in memory. The volume for a personal
// finance history is small enough that buffering simplifies error handling
// (any failure becomes a clean 500 before a byte of the response is written).
func buildUserBackup(ctx context.Context, pool db.DBPool, userID uuid.UUID) (*models.BackupBundle, error) {
	b := &models.BackupBundle{
		Format:               models.BackupFormat,
		Version:              models.BackupVersion,
		ExportedAt:           time.Now().UTC(),
		Accounts:             []models.BackupAccount{},
		CategoryGroups:       []models.BackupCategoryGroup{},
		Categories:           []models.BackupCategory{},
		Payees:               []models.BackupPayee{},
		BillingCycles:        []models.BackupBillingCycle{},
		Transactions:         []models.BackupTransaction{},
		Links:                []models.BackupLink{},
		LoanAttachments:      []models.BackupLoanAttachment{},
		RecurringSeries:      []models.BackupRecurringSeries{},
		RecurringTerms:       []models.BackupRecurringTerm{},
		RecurringAttachments: []models.BackupRecurringAttachment{},
		Rules:                []models.BackupRule{},
	}

	var s models.BackupSettings
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(paperless_url, ''), COALESCE(paperless_tag, ''), page_size FROM users WHERE id = $1`,
		userID,
	).Scan(&s.PaperlessURL, &s.PaperlessTag, &s.PageSize); err != nil {
		return nil, fmt.Errorf("export settings: %w", err)
	}
	b.Settings = &s

	if err := exportUserRows(ctx, pool,
		`SELECT id, name, account_type_id, COALESCE(bank, ''), COALESCE(currency, ''), COALESCE(color, ''), is_default, billing_day, closed, created_at, updated_at
		 FROM accounts WHERE user_id = $1 ORDER BY created_at`,
		[]any{userID}, func(rows pgx.Rows) error {
			var a models.BackupAccount
			if err := rows.Scan(&a.ID, &a.Name, &a.AccountTypeID, &a.Bank, &a.Currency, &a.Color, &a.IsDefault, &a.BillingDay, &a.Closed, &a.CreatedAt, &a.UpdatedAt); err != nil {
				return err
			}
			b.Accounts = append(b.Accounts, a)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("export accounts: %w", err)
	}

	if err := exportUserRows(ctx, pool,
		`SELECT id, name, COALESCE(icon, ''), COALESCE(color, ''), sort_order
		 FROM category_groups WHERE user_id = $1 ORDER BY sort_order, name`,
		[]any{userID}, func(rows pgx.Rows) error {
			var g models.BackupCategoryGroup
			if err := rows.Scan(&g.ID, &g.Name, &g.Icon, &g.Color, &g.SortOrder); err != nil {
				return err
			}
			b.CategoryGroups = append(b.CategoryGroups, g)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("export category groups: %w", err)
	}

	if err := exportUserRows(ctx, pool,
		`SELECT id, name, COALESCE(icon, ''), COALESCE(color, ''), group_id
		 FROM categories WHERE user_id = $1`,
		[]any{userID}, func(rows pgx.Rows) error {
			var cat models.BackupCategory
			if err := rows.Scan(&cat.ID, &cat.Name, &cat.Icon, &cat.Color, &cat.GroupID); err != nil {
				return err
			}
			b.Categories = append(b.Categories, cat)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("export categories: %w", err)
	}

	// Global categories the user references are included so the bundle is
	// self-contained; they carry global=true and are resolved by name on import.
	if err := exportUserRows(ctx, pool,
		`SELECT DISTINCT c.id, c.name, COALESCE(c.icon, ''), COALESCE(c.color, ''), c.group_id
		 FROM categories c
		 WHERE c.user_id IS NULL AND (
		     c.id IN (SELECT category_id FROM transactions WHERE user_id = $1 AND category_id IS NOT NULL)
		     OR c.id IN (SELECT category_id FROM rules WHERE user_id = $1)
		     OR c.id IN (SELECT category_id FROM recurring_series WHERE user_id = $1 AND category_id IS NOT NULL)
		 )`,
		[]any{userID}, func(rows pgx.Rows) error {
			var cat models.BackupCategory
			if err := rows.Scan(&cat.ID, &cat.Name, &cat.Icon, &cat.Color, &cat.GroupID); err != nil {
				return err
			}
			cat.Global = true
			b.Categories = append(b.Categories, cat)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("export global categories: %w", err)
	}

	if err := exportUserRows(ctx, pool,
		`SELECT id, name, account_id, created_at, updated_at FROM payees WHERE user_id = $1 ORDER BY created_at`,
		[]any{userID}, func(rows pgx.Rows) error {
			var p models.BackupPayee
			if err := rows.Scan(&p.ID, &p.Name, &p.AccountID, &p.CreatedAt, &p.UpdatedAt); err != nil {
				return err
			}
			b.Payees = append(b.Payees, p)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("export payees: %w", err)
	}

	if err := exportUserRows(ctx, pool,
		`SELECT id, account_id, start_date, end_date, COALESCE(label, ''), created_at
		 FROM billing_cycles WHERE user_id = $1 ORDER BY start_date`,
		[]any{userID}, func(rows pgx.Rows) error {
			var bc models.BackupBillingCycle
			var start, end time.Time
			if err := rows.Scan(&bc.ID, &bc.AccountID, &start, &end, &bc.Label, &bc.CreatedAt); err != nil {
				return err
			}
			bc.StartDate = start.Format("2006-01-02")
			bc.EndDate = end.Format("2006-01-02")
			b.BillingCycles = append(b.BillingCycles, bc)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("export billing cycles: %w", err)
	}

	if err := exportUserRows(ctx, pool,
		`SELECT id, account_id, date, description, amount, type, category_id, COALESCE(tags, '{}'), COALESCE(notes, ''), payee_id, billing_cycle_id, created_at, updated_at
		 FROM transactions WHERE user_id = $1 ORDER BY date, created_at`,
		[]any{userID}, func(rows pgx.Rows) error {
			var t models.BackupTransaction
			var date time.Time
			if err := rows.Scan(&t.ID, &t.AccountID, &date, &t.Description, &t.Amount, &t.Type, &t.CategoryID, &t.Tags, &t.Notes, &t.PayeeID, &t.BillingCycleID, &t.CreatedAt, &t.UpdatedAt); err != nil {
				return err
			}
			t.Date = date.Format("2006-01-02")
			if t.Tags == nil {
				t.Tags = []string{}
			}
			b.Transactions = append(b.Transactions, t)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("export transactions: %w", err)
	}

	if err := exportUserRows(ctx, pool,
		`SELECT id, type, from_txn_id, to_txn_id, COALESCE(notes, ''), created_at
		 FROM links WHERE user_id = $1 ORDER BY created_at`,
		[]any{userID}, func(rows pgx.Rows) error {
			var l models.BackupLink
			if err := rows.Scan(&l.ID, &l.Type, &l.FromTxnID, &l.ToTxnID, &l.Notes, &l.CreatedAt); err != nil {
				return err
			}
			b.Links = append(b.Links, l)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("export links: %w", err)
	}

	if err := exportUserRows(ctx, pool,
		`SELECT id, loan_account_id, transaction_id, created_at FROM loan_attachments WHERE user_id = $1 ORDER BY created_at`,
		[]any{userID}, func(rows pgx.Rows) error {
			var la models.BackupLoanAttachment
			if err := rows.Scan(&la.ID, &la.LoanAccountID, &la.TransactionID, &la.CreatedAt); err != nil {
				return err
			}
			b.LoanAttachments = append(b.LoanAttachments, la)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("export loan attachments: %w", err)
	}

	if err := exportUserRows(ctx, pool,
		`SELECT id, name, COALESCE(description, ''), type, frequency, interval, category_id, payee_id, active, COALESCE(notes, ''), created_at, updated_at
		 FROM recurring_series WHERE user_id = $1 ORDER BY created_at`,
		[]any{userID}, func(rows pgx.Rows) error {
			var series models.BackupRecurringSeries
			if err := rows.Scan(&series.ID, &series.Name, &series.Description, &series.Type, &series.Frequency, &series.Interval, &series.CategoryID, &series.PayeeID, &series.Active, &series.Notes, &series.CreatedAt, &series.UpdatedAt); err != nil {
				return err
			}
			b.RecurringSeries = append(b.RecurringSeries, series)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("export recurring series: %w", err)
	}

	if err := exportUserRows(ctx, pool,
		`SELECT id, series_id, start_date, end_date, amount, account_id, created_at
		 FROM recurring_series_terms WHERE user_id = $1 ORDER BY start_date`,
		[]any{userID}, func(rows pgx.Rows) error {
			var term models.BackupRecurringTerm
			var start time.Time
			var end *time.Time
			if err := rows.Scan(&term.ID, &term.SeriesID, &start, &end, &term.Amount, &term.AccountID, &term.CreatedAt); err != nil {
				return err
			}
			term.StartDate = start.Format("2006-01-02")
			term.EndDate = backupDatePtr(end)
			b.RecurringTerms = append(b.RecurringTerms, term)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("export recurring terms: %w", err)
	}

	if err := exportUserRows(ctx, pool,
		`SELECT id, series_id, transaction_id, created_at FROM recurring_attachments WHERE user_id = $1 ORDER BY created_at`,
		[]any{userID}, func(rows pgx.Rows) error {
			var ra models.BackupRecurringAttachment
			if err := rows.Scan(&ra.ID, &ra.SeriesID, &ra.TransactionID, &ra.CreatedAt); err != nil {
				return err
			}
			b.RecurringAttachments = append(b.RecurringAttachments, ra)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("export recurring attachments: %w", err)
	}

	if err := exportUserRows(ctx, pool,
		`SELECT id, pattern, COALESCE(match_type, 'contains'), category_id, payee_id, priority,
		        account_id, filter_category_id, filter_payee_id, min_amount, max_amount, txn_type,
		        date_from, date_to, is_linked, is_recurring, COALESCE(add_tags, '{}'), COALESCE(notes, '')
		 FROM rules WHERE user_id = $1 ORDER BY priority DESC`,
		[]any{userID}, func(rows pgx.Rows) error {
			var r models.BackupRule
			var dateFrom, dateTo *time.Time
			if err := rows.Scan(&r.ID, &r.Pattern, &r.MatchType, &r.CategoryID, &r.PayeeID, &r.Priority,
				&r.AccountID, &r.FilterCategoryID, &r.FilterPayeeID, &r.MinAmount, &r.MaxAmount, &r.TxnType,
				&dateFrom, &dateTo, &r.IsLinked, &r.IsRecurring, &r.AddTags, &r.Notes); err != nil {
				return err
			}
			r.DateFrom = backupDatePtr(dateFrom)
			r.DateTo = backupDatePtr(dateTo)
			b.Rules = append(b.Rules, r)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("export rules: %w", err)
	}

	return b, nil
}

// backupDatePtr formats an optional DATE for the bundle.
func backupDatePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format("2006-01-02")
	return &s
}

// restoreUserBackup inserts the bundle into tx under userID, remapping every
// reference. The bundle's own IDs are never reused: rows are inserted in
// dependency order with fresh UUIDs recorded in per-resource maps.
func restoreUserBackup(ctx context.Context, tx pgx.Tx, userID uuid.UUID, b *models.BackupBundle, res *models.BackupImportResult) error {
	var existing int
	if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM accounts WHERE user_id = $1", userID).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return &backupError{http.StatusConflict, "cannot import into an account that already has data; delete its accounts first"}
	}

	if err := checkBackupAccountTypes(ctx, tx, b); err != nil {
		return err
	}

	// Accounts.
	accountMap := map[uuid.UUID]uuid.UUID{}
	accountRows := make([][]any, 0, len(b.Accounts))
	defaultUsed := false
	for _, a := range b.Accounts {
		newID := uuid.New()
		accountMap[a.ID] = newID
		isDefault := a.IsDefault && !defaultUsed
		if isDefault {
			defaultUsed = true
		}
		accountRows = append(accountRows, []any{
			newID, userID, a.Name, a.AccountTypeID, a.Bank, a.Currency, a.Color,
			isDefault, a.BillingDay, a.Closed, nonZeroTime(a.CreatedAt), nonZeroTime(a.UpdatedAt),
		})
	}

	// Custom category groups, deduped by name against the target user's groups
	// so a fresh registration's defaults are not duplicated.
	groupMap := map[string]string{}
	groupRows := make([][]any, 0, len(b.CategoryGroups))
	for _, g := range b.CategoryGroups {
		existingID, found, err := findUserGroupID(ctx, tx, userID, g.Name)
		if err != nil {
			return err
		}
		if found {
			groupMap[g.ID] = existingID
			continue
		}
		newID := newBackupGroupID()
		groupMap[g.ID] = newID
		groupRows = append(groupRows, []any{newID, g.Name, g.Icon, g.Color, false, userID, g.SortOrder})
	}

	// Categories. Global rows are matched against the target instance's global
	// categories by name+group and fall back to a user-owned copy.
	categoryMap := map[uuid.UUID]uuid.UUID{}
	categoryRows := make([][]any, 0, len(b.Categories))
	for _, cat := range b.Categories {
		groupID := cat.GroupID
		if mapped, ok := groupMap[cat.GroupID]; ok {
			groupID = mapped
		}
		existingID, found, err := findCategoryID(ctx, tx, userID, cat, groupID)
		if err != nil {
			return err
		}
		if found {
			categoryMap[cat.ID] = existingID
			continue
		}
		newID := uuid.New()
		categoryMap[cat.ID] = newID
		categoryRows = append(categoryRows, []any{newID, userID, cat.Name, cat.Icon, cat.Color, groupID})
	}

	// Payees.
	payeeMap := map[uuid.UUID]uuid.UUID{}
	payeeRows := make([][]any, 0, len(b.Payees))
	for _, p := range b.Payees {
		newID := uuid.New()
		payeeMap[p.ID] = newID
		payeeRows = append(payeeRows, []any{
			newID, userID, p.Name, mapBackupUUID(accountMap, p.AccountID), nonZeroTime(p.CreatedAt), nonZeroTime(p.UpdatedAt),
		})
	}

	// Billing cycles.
	cycleMap := map[uuid.UUID]uuid.UUID{}
	cycleRows := make([][]any, 0, len(b.BillingCycles))
	for _, bc := range b.BillingCycles {
		accountID, ok := accountMap[bc.AccountID]
		if !ok {
			addBackupWarning(res, "skipped billing cycle: its account is not in the backup")
			continue
		}
		start, err := parseBackupDate(bc.StartDate)
		if err != nil {
			return err
		}
		end, err := parseBackupDate(bc.EndDate)
		if err != nil {
			return err
		}
		newID := uuid.New()
		cycleMap[bc.ID] = newID
		cycleRows = append(cycleRows, []any{newID, accountID, userID, start, end, bc.Label, nonZeroTime(bc.CreatedAt)})
	}

	// Transactions.
	txnMap := map[uuid.UUID]uuid.UUID{}
	txnRows := make([][]any, 0, len(b.Transactions))
	for _, t := range b.Transactions {
		accountID, ok := accountMap[t.AccountID]
		if !ok {
			addBackupWarning(res, "skipped transaction: its account is not in the backup")
			continue
		}
		date, err := parseBackupDate(t.Date)
		if err != nil {
			return err
		}
		tags := t.Tags
		if tags == nil {
			tags = []string{}
		}
		newID := uuid.New()
		txnMap[t.ID] = newID
		txnRows = append(txnRows, []any{
			newID, accountID, userID, date, t.Description, t.Amount, t.Type,
			mapBackupUUID(categoryMap, t.CategoryID), tags, t.Notes,
			mapBackupUUID(payeeMap, t.PayeeID), mapBackupUUID(cycleMap, t.BillingCycleID),
			nonZeroTime(t.CreatedAt), nonZeroTime(t.UpdatedAt),
		})
	}

	// Links.
	linkRows := make([][]any, 0, len(b.Links))
	for _, l := range b.Links {
		from, okFrom := txnMap[l.FromTxnID]
		to, okTo := txnMap[l.ToTxnID]
		if !okFrom || !okTo {
			addBackupWarning(res, "skipped link: one of its transactions is not in the backup")
			continue
		}
		linkRows = append(linkRows, []any{uuid.New(), userID, l.Type, from, to, l.Notes, nonZeroTime(l.CreatedAt)})
	}

	// Loan attachments.
	loanRows := make([][]any, 0, len(b.LoanAttachments))
	for _, la := range b.LoanAttachments {
		accountID, okAccount := accountMap[la.LoanAccountID]
		txnID, okTxn := txnMap[la.TransactionID]
		if !okAccount || !okTxn {
			addBackupWarning(res, "skipped loan attachment: its account or transaction is not in the backup")
			continue
		}
		loanRows = append(loanRows, []any{uuid.New(), userID, accountID, txnID, nonZeroTime(la.CreatedAt)})
	}

	// Recurring series. Amount/account/date range live on the series' terms.
	seriesMap := map[uuid.UUID]uuid.UUID{}
	seriesRows := make([][]any, 0, len(b.RecurringSeries))
	for _, s := range b.RecurringSeries {
		newID := uuid.New()
		seriesMap[s.ID] = newID
		seriesRows = append(seriesRows, []any{
			newID, userID, s.Name, s.Description, s.Type, s.Frequency, s.Interval,
			mapBackupUUID(categoryMap, s.CategoryID), mapBackupUUID(payeeMap, s.PayeeID),
			s.Active, s.Notes, nonZeroTime(s.CreatedAt), nonZeroTime(s.UpdatedAt),
		})
	}

	// Recurring terms.
	termRows := make([][]any, 0, len(b.RecurringTerms))
	for _, term := range b.RecurringTerms {
		seriesID, okSeries := seriesMap[term.SeriesID]
		accountID, okAccount := accountMap[term.AccountID]
		if !okSeries || !okAccount {
			addBackupWarning(res, "skipped recurring term: its series or account is not in the backup")
			continue
		}
		start, err := parseBackupDate(term.StartDate)
		if err != nil {
			return err
		}
		var end *time.Time
		if term.EndDate != nil {
			e, err := parseBackupDate(*term.EndDate)
			if err != nil {
				return err
			}
			end = &e
		}
		termRows = append(termRows, []any{uuid.New(), userID, seriesID, start, end, term.Amount, accountID, nonZeroTime(term.CreatedAt)})
	}

	// Recurring attachments.
	recurringRows := make([][]any, 0, len(b.RecurringAttachments))
	for _, ra := range b.RecurringAttachments {
		seriesID, okSeries := seriesMap[ra.SeriesID]
		txnID, okTxn := txnMap[ra.TransactionID]
		if !okSeries || !okTxn {
			addBackupWarning(res, "skipped recurring attachment: its series or transaction is not in the backup")
			continue
		}
		recurringRows = append(recurringRows, []any{uuid.New(), userID, seriesID, txnID, nonZeroTime(ra.CreatedAt)})
	}

	// Rules.
	ruleRows := make([][]any, 0, len(b.Rules))
	for _, r := range b.Rules {
		categoryID, ok := categoryMap[r.CategoryID]
		if !ok {
			addBackupWarning(res, "skipped rule: its category is not in the backup")
			continue
		}
		matchType := r.MatchType
		if matchType == "" {
			matchType = "contains"
		}
		// Conditions referencing a resource missing from the bundle degrade to
		// "condition unset" rather than dropping the whole rule, so a partial
		// bundle still restores the rule's core behavior.
		ruleRows = append(ruleRows, []any{
			uuid.New(), userID, r.Pattern, matchType, categoryID, mapBackupUUID(payeeMap, r.PayeeID), r.Priority,
			mapBackupUUID(accountMap, r.AccountID),
			mapBackupUUID(categoryMap, r.FilterCategoryID),
			mapBackupUUID(payeeMap, r.FilterPayeeID),
			r.MinAmount, r.MaxAmount, nullIfEmpty(r.TxnType),
			r.DateFrom, r.DateTo, r.IsLinked, r.IsRecurring, r.AddTags, r.Notes,
		})
	}

	// Insert in dependency order. Parents must exist before children so every
	// composite foreign key resolves.
	inserts := []struct {
		table   string
		columns []string
		rows    [][]any
		count   *int
	}{
		{"accounts", []string{"id", "user_id", "name", "account_type_id", "bank", "currency", "color", "is_default", "billing_day", "closed", "created_at", "updated_at"}, accountRows, &res.Accounts},
		{"category_groups", []string{"id", "name", "icon", "color", "is_base", "user_id", "sort_order"}, groupRows, &res.CategoryGroups},
		{"categories", []string{"id", "user_id", "name", "icon", "color", "group_id"}, categoryRows, &res.Categories},
		{"payees", []string{"id", "user_id", "name", "account_id", "created_at", "updated_at"}, payeeRows, &res.Payees},
		{"billing_cycles", []string{"id", "account_id", "user_id", "start_date", "end_date", "label", "created_at"}, cycleRows, &res.BillingCycles},
		{"transactions", []string{"id", "account_id", "user_id", "date", "description", "amount", "type", "category_id", "tags", "notes", "payee_id", "billing_cycle_id", "created_at", "updated_at"}, txnRows, &res.Transactions},
		{"links", []string{"id", "user_id", "type", "from_txn_id", "to_txn_id", "notes", "created_at"}, linkRows, &res.Links},
		{"loan_attachments", []string{"id", "user_id", "loan_account_id", "transaction_id", "created_at"}, loanRows, &res.LoanAttachments},
		{"recurring_series", []string{"id", "user_id", "name", "description", "type", "frequency", "interval", "category_id", "payee_id", "active", "notes", "created_at", "updated_at"}, seriesRows, &res.RecurringSeries},
		{"recurring_series_terms", []string{"id", "user_id", "series_id", "start_date", "end_date", "amount", "account_id", "created_at"}, termRows, &res.RecurringTerms},
		{"recurring_attachments", []string{"id", "user_id", "series_id", "transaction_id", "created_at"}, recurringRows, &res.RecurringAttachments},
		{"rules", []string{"id", "user_id", "pattern", "match_type", "category_id", "payee_id", "priority",
			"account_id", "filter_category_id", "filter_payee_id", "min_amount", "max_amount", "txn_type",
			"date_from", "date_to", "is_linked", "is_recurring", "add_tags", "notes"}, ruleRows, &res.Rules},
	}
	for _, ins := range inserts {
		if err := insertBackupRows(ctx, tx, ins.table, ins.columns, ins.rows); err != nil {
			return err
		}
		*ins.count = len(ins.rows)
	}

	// Non-secret settings.
	if b.Settings != nil {
		if _, err := tx.Exec(ctx,
			"UPDATE users SET paperless_url = $1, paperless_tag = $2, page_size = $3, updated_at = NOW() WHERE id = $4",
			b.Settings.PaperlessURL, b.Settings.PaperlessTag, b.Settings.PageSize, userID,
		); err != nil {
			return err
		}
	}

	return nil
}

// checkBackupAccountTypes fails the restore before any insert when the bundle
// references account types that don't exist on this server (e.g. an
// admin-created type from another instance).
func checkBackupAccountTypes(ctx context.Context, tx pgx.Tx, b *models.BackupBundle) error {
	typeIDs := map[string]struct{}{}
	for _, a := range b.Accounts {
		typeIDs[a.AccountTypeID] = struct{}{}
	}
	if len(typeIDs) == 0 {
		return nil
	}

	ids := make([]string, 0, len(typeIDs))
	for id := range typeIDs {
		ids = append(ids, id)
	}

	rows, err := tx.Query(ctx, "SELECT id FROM account_types WHERE id = ANY($1)", ids)
	if err != nil {
		return err
	}
	found := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		found[id] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var missing []string
	for id := range typeIDs {
		if _, ok := found[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return &backupError{
			http.StatusBadRequest,
			fmt.Sprintf("backup uses account types not available on this server: %s", strings.Join(missing, ", ")),
		}
	}
	return nil
}

// findUserGroupID looks up a custom group by name for the target user.
func findUserGroupID(ctx context.Context, tx pgx.Tx, userID uuid.UUID, name string) (string, bool, error) {
	var id string
	err := tx.QueryRow(ctx,
		"SELECT id FROM category_groups WHERE user_id = $1 AND name = $2 LIMIT 1", userID, name,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// findCategoryID resolves a bundle category to an existing target category:
// global categories match the target's global rows, user categories match the
// target user's own rows (which also absorbs the registration-seeded defaults).
func findCategoryID(ctx context.Context, tx pgx.Tx, userID uuid.UUID, cat models.BackupCategory, groupID string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	var err error
	if cat.Global {
		err = tx.QueryRow(ctx,
			"SELECT id FROM categories WHERE user_id IS NULL AND name = $1 AND group_id = $2 LIMIT 1",
			cat.Name, groupID,
		).Scan(&id)
	} else {
		err = tx.QueryRow(ctx,
			"SELECT id FROM categories WHERE user_id = $1 AND name = $2 AND group_id = $3 LIMIT 1",
			userID, cat.Name, groupID,
		).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

// insertBackupRows inserts rows with multi-row VALUES statements, chunked to
// stay under PostgreSQL's parameter limit.
func insertBackupRows(ctx context.Context, tx pgx.Tx, table string, columns []string, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	n := len(columns)
	for start := 0; start < len(rows); start += backupInsertChunk {
		end := start + backupInsertChunk
		if end > len(rows) {
			end = len(rows)
		}

		var sb strings.Builder
		sb.WriteString("INSERT INTO ")
		sb.WriteString(table)
		sb.WriteString(" (")
		sb.WriteString(strings.Join(columns, ", "))
		sb.WriteString(") VALUES ")

		args := make([]any, 0, (end-start)*n)
		for i := start; i < end; i++ {
			if i > start {
				sb.WriteString(", ")
			}
			sb.WriteByte('(')
			for j := 0; j < n; j++ {
				if j > 0 {
					sb.WriteString(", ")
				}
				fmt.Fprintf(&sb, "$%d", len(args)+1)
				args = append(args, rows[i][j])
			}
			sb.WriteByte(')')
		}

		if _, err := tx.Exec(ctx, sb.String(), args...); err != nil {
			return err
		}
	}
	return nil
}

// mapBackupUUID remaps an optional ID through the map, returning nil when the
// reference is absent or unresolved.
func mapBackupUUID(m map[uuid.UUID]uuid.UUID, id *uuid.UUID) *uuid.UUID {
	if id == nil {
		return nil
	}
	mapped, ok := m[*id]
	if !ok {
		return nil
	}
	return &mapped
}

// parseBackupDate parses an ISO "YYYY-MM-DD" date from a bundle.
func parseBackupDate(s string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, &backupError{http.StatusBadRequest, fmt.Sprintf("invalid date %q in backup", s)}
	}
	return t, nil
}

// nonZeroTime substitutes the current time for an omitted timestamp so a
// hand-written bundle still restores with sane audit columns.
func nonZeroTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t
}

// newBackupGroupID mints a slug group ID (the column is VARCHAR(50) and its PK
// is global, so a fresh ID avoids colliding with another user's custom group).
func newBackupGroupID() string {
	return "g" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

// addBackupWarning records a skipped row, capped so a corrupt bundle cannot
// produce an unbounded response.
func addBackupWarning(res *models.BackupImportResult, msg string) {
	if len(res.Warnings) >= maxBackupWarnings {
		return
	}
	res.Warnings = append(res.Warnings, msg)
}
