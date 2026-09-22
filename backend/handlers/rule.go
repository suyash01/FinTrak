package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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

// GetRules lists the user's categorization rules, highest priority first, with
// the joined category/payee/account/filter names.
func (srv *Server) GetRules(c *gin.Context) {
	rows, err := srv.db.Query(c,
		`SELECT r.id, r.pattern, r.match_type, r.category_id, r.payee_id, COALESCE(p.name, '') as payee, r.priority,
		 COALESCE(c.name, '') as category_name,
		 r.account_id, COALESCE(a.name, '') as account_name,
		 r.filter_category_id, COALESCE(fc.name, '') as filter_category_name,
		 r.filter_payee_id, COALESCE(fp.name, '') as filter_payee_name,
		 r.min_amount, r.max_amount, COALESCE(r.txn_type, ''),
		 r.date_from, r.date_to, r.is_linked, r.is_recurring,
		 COALESCE(r.add_tags, '{}'), COALESCE(r.notes, '')
		 FROM rules r
		 LEFT JOIN categories c ON r.category_id = c.id
		 LEFT JOIN payees p ON r.payee_id = p.id
		 LEFT JOIN accounts a ON r.account_id = a.id
		 LEFT JOIN categories fc ON r.filter_category_id = fc.id
		 LEFT JOIN payees fp ON r.filter_payee_id = fp.id
		 WHERE r.user_id = $1
		 ORDER BY r.priority DESC, r.id`, auth.GetUserID(c))
	if err != nil {
		slog.Error("GetRules", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	rules := []models.Rule{}
	for rows.Next() {
		var r models.Rule
		var dateFrom, dateTo *time.Time
		if err := rows.Scan(&r.ID, &r.Pattern, &r.MatchType, &r.CategoryID, &r.PayeeID, &r.Payee, &r.Priority,
			&r.CategoryName, &r.AccountID, &r.AccountName, &r.FilterCategoryID, &r.FilterCategoryName,
			&r.FilterPayeeID, &r.FilterPayeeName, &r.MinAmount, &r.MaxAmount, &r.TxnType,
			&dateFrom, &dateTo, &r.IsLinked, &r.IsRecurring, &r.AddTags, &r.Notes); err != nil {
			slog.Error("GetRules scan", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		r.DateFrom = formatDatePtr(dateFrom)
		r.DateTo = formatDatePtr(dateTo)
		rules = append(rules, r)
	}
	if err := rows.Err(); err != nil {
		slog.Error("GetRules rows", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, rules)
}

// formatDatePtr formats an optional DATE for JSON ("YYYY-MM-DD").
func formatDatePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format("2006-01-02")
	return &s
}

// normalizeRuleMatchType defaults an empty match type to "contains" and rejects
// an unknown one so a typo never silently creates a rule that can't fire.
func normalizeRuleMatchType(c *gin.Context, mt string) (string, bool) {
	if mt == "" {
		return "contains", true
	}
	switch mt {
	case "contains", "starts_with", "exact":
		return mt, true
	}
	validation.RespondError(c, "matchType must be 'contains', 'starts_with' or 'exact'", http.StatusBadRequest)
	return "", false
}

// validateRuleDates parses optional date bounds, writing a 400 and returning
// false when either is not YYYY-MM-DD.
func validateRuleDates(c *gin.Context, from, to string) (fromPtr, toPtr *string, ok bool) {
	if from != "" {
		if _, err := time.Parse("2006-01-02", from); err != nil {
			validation.RespondError(c, "invalid dateFrom (expected YYYY-MM-DD)", http.StatusBadRequest)
			return nil, nil, false
		}
		fromPtr = &from
	}
	if to != "" {
		if _, err := time.Parse("2006-01-02", to); err != nil {
			validation.RespondError(c, "invalid dateTo (expected YYYY-MM-DD)", http.StatusBadRequest)
			return nil, nil, false
		}
		toPtr = &to
	}
	return fromPtr, toPtr, true
}

// CreateRule inserts a categorization rule with optional conditions/actions,
// defaulting MatchType to "contains" and rejecting references to
// accounts/categories/payees the user doesn't own.
func (srv *Server) CreateRule(c *gin.Context) {
	var req models.CreateRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	matchType, ok := normalizeRuleMatchType(c, req.MatchType)
	if !ok {
		return
	}
	if req.TxnType != "" && req.TxnType != "debit" && req.TxnType != "credit" {
		validation.RespondError(c, "txnType must be 'debit' or 'credit'", http.StatusBadRequest)
		return
	}
	dateFrom, dateTo, ok := validateRuleDates(c, req.DateFrom, req.DateTo)
	if !ok {
		return
	}

	var rule models.Rule
	err := srv.db.QueryRow(c,
		// Every SELECT-list parameter is cast to its column type. Without the
		// casts the statement depends on the server inferring the types of
		// parameters that appear in no other typed context, and over the extended
		// protocol that inference fails ("could not determine data type of
		// parameter $5"), so creating a rule answered 500.
		`INSERT INTO rules (user_id, pattern, match_type, category_id, payee_id, priority,
		     account_id, filter_category_id, filter_payee_id, min_amount, max_amount, txn_type,
		     date_from, date_to, is_linked, is_recurring, add_tags, notes)
		 SELECT $1::uuid, $2::text, $3::text, $4::uuid, $5::uuid, $6::int, $7::uuid, $8::uuid, $9::uuid,
		        $10::bigint, $11::bigint, $12::text, $13::date, $14::date, $15::boolean, $16::boolean,
		        $17::text[], $18::text
		 WHERE EXISTS (SELECT 1 FROM categories c WHERE c.id = $4 AND (c.user_id = $1 OR c.user_id IS NULL))
		   AND ($5::uuid IS NULL OR EXISTS (SELECT 1 FROM payees p WHERE p.id = $5 AND p.user_id = $1))
		   AND ($7::uuid IS NULL OR EXISTS (SELECT 1 FROM accounts a WHERE a.id = $7 AND a.user_id = $1))
		   AND ($8::uuid IS NULL OR EXISTS (SELECT 1 FROM categories fc WHERE fc.id = $8 AND (fc.user_id = $1 OR fc.user_id IS NULL)))
		   AND ($9::uuid IS NULL OR EXISTS (SELECT 1 FROM payees fp WHERE fp.id = $9 AND fp.user_id = $1))
		 RETURNING id, pattern, match_type, category_id, payee_id, priority`,
		auth.GetUserID(c), req.Pattern, matchType, req.CategoryID, req.PayeeID, req.Priority,
		req.AccountID, req.FilterCategoryID, req.FilterPayeeID, req.MinAmount, req.MaxAmount, nullIfEmpty(req.TxnType),
		dateFrom, dateTo, req.IsLinked, req.IsRecurring, req.AddTags, req.Notes,
	).Scan(&rule.ID, &rule.Pattern, &rule.MatchType, &rule.CategoryID, &rule.PayeeID, &rule.Priority)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			validation.RespondError(c, "referenced category, payee, or account not found", http.StatusBadRequest)
			return
		}
		slog.Error("CreateRule", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	rule.AccountID = req.AccountID
	rule.FilterCategoryID = req.FilterCategoryID
	rule.FilterPayeeID = req.FilterPayeeID
	rule.MinAmount = req.MinAmount
	rule.MaxAmount = req.MaxAmount
	rule.TxnType = req.TxnType
	rule.DateFrom = dateFrom
	rule.DateTo = dateTo
	rule.IsLinked = req.IsLinked
	rule.IsRecurring = req.IsRecurring
	rule.AddTags = req.AddTags
	rule.Notes = req.Notes

	c.JSON(http.StatusCreated, rule)
}

// DeleteRule removes a rule owned by the user.
func (srv *Server) DeleteRule(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)
	result, err := srv.db.Exec(c, "DELETE FROM rules WHERE id = $1 AND user_id = $2", id, userID)
	if err != nil {
		slog.Error("DeleteRule", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if result.RowsAffected() == 0 {
		validation.RespondError(c, "rule not found", http.StatusNotFound)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// UpdateRule edits a rule's fields, enforcing ownership of any referenced
// account/category/payee and returning 404 when the rule isn't found.
func (srv *Server) UpdateRule(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	var req models.UpdateRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	// The create path gets this from the model's `binding:"required"`; the update
	// request has no binding tag, so without this check PUT /rules/:id could
	// blank the pattern and silently turn the rule into a catch-all — an empty
	// `contains` pattern matches every description, and the next apply (or every
	// new/imported transaction) would then be categorized by it.
	if req.Pattern == "" {
		validation.RespondError(c, "pattern is required", http.StatusBadRequest)
		return
	}

	matchType, ok := normalizeRuleMatchType(c, req.MatchType)
	if !ok {
		return
	}
	if req.TxnType != "" && req.TxnType != "debit" && req.TxnType != "credit" {
		validation.RespondError(c, "txnType must be 'debit' or 'credit'", http.StatusBadRequest)
		return
	}
	dateFrom, dateTo, ok := validateRuleDates(c, req.DateFrom, req.DateTo)
	if !ok {
		return
	}

	var rule models.Rule
	err = srv.db.QueryRow(c,
		`UPDATE rules SET pattern = $1, match_type = $2, category_id = $3, payee_id = $4, priority = $5,
		     account_id = $6, filter_category_id = $7, filter_payee_id = $8, min_amount = $9, max_amount = $10,
		     txn_type = $11, date_from = $12, date_to = $13, is_linked = $14, is_recurring = $15,
		     add_tags = $16, notes = $17
		 WHERE id = $18 AND user_id = $19
		   AND EXISTS (SELECT 1 FROM categories c WHERE c.id = $3 AND (c.user_id = $19 OR c.user_id IS NULL))
		   AND ($4::uuid IS NULL OR EXISTS (SELECT 1 FROM payees p WHERE p.id = $4 AND p.user_id = $19))
		   AND ($6::uuid IS NULL OR EXISTS (SELECT 1 FROM accounts a WHERE a.id = $6 AND a.user_id = $19))
		   AND ($7::uuid IS NULL OR EXISTS (SELECT 1 FROM categories fc WHERE fc.id = $7 AND (fc.user_id = $19 OR fc.user_id IS NULL)))
		   AND ($8::uuid IS NULL OR EXISTS (SELECT 1 FROM payees fp WHERE fp.id = $8 AND fp.user_id = $19))
		 RETURNING id, pattern, match_type, category_id, payee_id, priority`,
		req.Pattern, matchType, req.CategoryID, req.PayeeID, req.Priority,
		req.AccountID, req.FilterCategoryID, req.FilterPayeeID, req.MinAmount, req.MaxAmount,
		nullIfEmpty(req.TxnType), dateFrom, dateTo, req.IsLinked, req.IsRecurring,
		req.AddTags, req.Notes, id, auth.GetUserID(c),
	).Scan(&rule.ID, &rule.Pattern, &rule.MatchType, &rule.CategoryID, &rule.PayeeID, &rule.Priority)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			validation.RespondError(c, "rule not found", http.StatusNotFound)
			return
		}
		slog.Error("UpdateRule", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	rule.AccountID = req.AccountID
	rule.FilterCategoryID = req.FilterCategoryID
	rule.FilterPayeeID = req.FilterPayeeID
	rule.MinAmount = req.MinAmount
	rule.MaxAmount = req.MaxAmount
	rule.TxnType = req.TxnType
	rule.DateFrom = dateFrom
	rule.DateTo = dateTo
	rule.IsLinked = req.IsLinked
	rule.IsRecurring = req.IsRecurring
	rule.AddTags = req.AddTags
	rule.Notes = req.Notes

	c.JSON(http.StatusOK, rule)
}

// PreviewRule reports how many currently-uncategorized transactions a
// hypothetical rule (or rule edit) would categorize, without writing anything.
// It builds the same predicate ApplyRules uses, so the preview and a real apply
// agree by construction.
func (srv *Server) PreviewRule(c *gin.Context) {
	var req models.CreateRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	matchType, ok := normalizeRuleMatchType(c, req.MatchType)
	if !ok {
		return
	}
	if req.TxnType != "" && req.TxnType != "debit" && req.TxnType != "credit" {
		validation.RespondError(c, "txnType must be 'debit' or 'credit'", http.StatusBadRequest)
		return
	}
	dateFrom, dateTo, ok := validateRuleDates(c, req.DateFrom, req.DateTo)
	if !ok {
		return
	}

	entry := ruleEntryFromRequest(req, matchType, dateFrom, dateTo)
	f := newTxnFilter(auth.GetUserID(c))
	f.raw("category_id IS NULL")
	f.raw("NOT EXISTS (SELECT 1 FROM accounts closed_acct WHERE closed_acct.id = transactions.account_id AND closed_acct.closed)")
	if !appendRulePredicate(f, entry) {
		// An unrecognized match type never fires; report zero matches rather
		// than an error, matching ApplyRules' skip behavior.
		c.JSON(http.StatusOK, models.RulePreview{Matched: 0})
		return
	}

	// Render the WHERE fragment exactly as ApplyRules does — an unaliased
	// `transactions` scan and the same clause order — so the preview counts
	// precisely the rows a real apply would update.
	query := "SELECT COUNT(*) FROM transactions WHERE user_id = $1 AND " + strings.Join(f.clauses, " AND ")
	var matched int
	if err := srv.db.QueryRow(c, query, f.args...).Scan(&matched); err != nil {
		slog.Error("PreviewRule", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, models.RulePreview{Matched: matched})
}

// ApplyRules re-runs all of the user's rules against uncategorized transactions
// (category_id IS NULL), applying the first matching rule's category, payee,
// tags and note. It runs as one set-based UPDATE per rule in descending
// priority order: the category_id IS NULL guard means a transaction is
// categorized by exactly one (the highest-priority matching) rule, and any
// failure rolls the whole apply back so a mid-batch error can never commit a
// silently partial result. Transactions on closed accounts are skipped
// (immutable; linking only). Returns the number of transactions updated.
func (srv *Server) ApplyRules(c *gin.Context) {
	userID := auth.GetUserID(c)

	// Get all rules
	rules, err := srv.loadRules(c, userID)
	if err != nil {
		slog.Error("ApplyRules (getting rules)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Use a transaction for batch updates
	tx, err := srv.db.Begin(c)
	if err != nil {
		slog.Error("ApplyRules (starting transaction)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(c)

	updated := 0
	for _, r := range rules {
		// Param layout: $1 user_id, $2 category_id, and the rule's own
		// predicates (pattern + conditions) are appended from $3.
		args := []any{userID, r.CatID}
		setClauses := []string{"category_id = $2"}
		paramIdx := 3

		if r.PayeeID != nil {
			setClauses = append(setClauses, fmt.Sprintf("payee_id = $%d", paramIdx))
			args = append(args, *r.PayeeID)
			paramIdx++
		}
		if len(r.AddTags) > 0 {
			setClauses = append(setClauses, fmt.Sprintf(
				"tags = (SELECT COALESCE(array_agg(DISTINCT x ORDER BY x), '{}') FROM unnest(tags || $%d::text[]) AS x)", paramIdx))
			args = append(args, r.AddTags)
			paramIdx++
		}
		if r.Notes != "" {
			setClauses = append(setClauses, fmt.Sprintf(
				"notes = CASE WHEN notes = '' THEN $%d ELSE notes || E'\\n' || $%d END", paramIdx, paramIdx))
			args = append(args, r.Notes)
			paramIdx++
		}

		f := &txnFilter{args: args}
		f.raw("category_id IS NULL")
		f.raw("NOT EXISTS (SELECT 1 FROM accounts closed_acct WHERE closed_acct.id = transactions.account_id AND closed_acct.closed)")
		if !appendRulePredicate(f, r) {
			// Unrecognized match types never fire in matchRule either — skip
			// them here rather than failing the whole batch.
			continue
		}

		// Rebuild the args after appendRulePredicate appended condition args.
		// The SET placeholders above reference the first len(args) positions,
		// which appendRulePredicate must therefore start after.
		query := fmt.Sprintf("UPDATE transactions SET %s WHERE user_id = $1 AND %s",
			strings.Join(setClauses, ", "), strings.Join(f.clauses, " AND "))

		res, err := tx.Exec(c, query, f.args...)
		if err != nil {
			slog.Error("applying rule", slog.String("pattern", r.Pattern), slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return // deferred rollback keeps the apply all-or-nothing
		}
		updated += int(res.RowsAffected())
	}

	if err := tx.Commit(c); err != nil {
		slog.Error("ApplyRules (committing transaction)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": updated})
}

// ruleMatchSQL builds the SQL predicate (referencing the pattern as $paramIdx)
// and its bound argument for a rule's match type, mirroring matchRule's
// semantics: case-insensitive contains / starts_with / exact. The arg's % and _
// are escaped so LIKE behaves like a plain substring check ("100%" does not
// match "1000"). ok is false for match types matchRule never fires for.
func ruleMatchSQL(matchType, pattern string, paramIdx int) (expr string, arg string, ok bool) {
	switch matchType {
	case "contains":
		return fmt.Sprintf("LOWER(description) LIKE LOWER($%d) ESCAPE '\\'", paramIdx), "%" + escapeLikePattern(pattern) + "%", true
	case "starts_with":
		return fmt.Sprintf("LOWER(description) LIKE LOWER($%d) ESCAPE '\\'", paramIdx), escapeLikePattern(pattern) + "%", true
	case "exact":
		return fmt.Sprintf("LOWER(description) = LOWER($%d)", paramIdx), pattern, true
	}
	return "", "", false
}

// escapeLikePattern escapes LIKE wildcards and the escape character itself so a
// user-supplied pattern matches literally (same semantics as strings.Contains).
func escapeLikePattern(p string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(p)
}

// ruleEntry is the in-memory representation used for matching during
// transaction creation, imports, and ApplyRules. Its optional fields are the
// rule's conditions (all ANDed) and its extra actions.
type ruleEntry struct {
	Pattern   string
	MatchType string
	CatID     uuid.UUID
	PayeeID   *uuid.UUID

	AccountID        *uuid.UUID
	FilterCategoryID *uuid.UUID
	FilterPayeeID    *uuid.UUID
	MinAmount        *money.Amount
	MaxAmount        *money.Amount
	TxnType          string
	DateFrom         *string
	DateTo           *string
	IsLinked         *bool
	IsRecurring      *bool

	AddTags []string
	Notes   string
}

// ruleContext is the transaction state a rule is matched against. CategoryID
// and PayeeID are the transaction's current values (nil for a new/uncategorized
// transaction); IsLinked/IsRecurring are false at create time.
type ruleContext struct {
	Description string
	AccountID   uuid.UUID
	Amount      money.Amount
	Type        string
	Date        string
	CategoryID  *uuid.UUID
	PayeeID     *uuid.UUID
	IsLinked    bool
	IsRecurring bool
}

// ruleEntryFromRequest maps a create-rule request into a ruleEntry (used by the
// preview endpoint, which doesn't persist the rule).
func ruleEntryFromRequest(req models.CreateRuleRequest, matchType string, dateFrom, dateTo *string) ruleEntry {
	return ruleEntry{
		Pattern:          req.Pattern,
		MatchType:        matchType,
		CatID:            req.CategoryID,
		PayeeID:          req.PayeeID,
		AccountID:        req.AccountID,
		FilterCategoryID: req.FilterCategoryID,
		FilterPayeeID:    req.FilterPayeeID,
		MinAmount:        req.MinAmount,
		MaxAmount:        req.MaxAmount,
		TxnType:          req.TxnType,
		DateFrom:         dateFrom,
		DateTo:           dateTo,
		IsLinked:         req.IsLinked,
		IsRecurring:      req.IsRecurring,
		AddTags:          req.AddTags,
		Notes:            req.Notes,
	}
}

// appendRulePredicate appends a rule's full WHERE predicate (description match
// plus every set condition) to f. Columns are referenced unqualified so the
// same predicate works both for a plain `transactions` scan and for an
// `UPDATE transactions ...` (neither introduces an alias). It returns false for
// an unrecognized match type (which never fires) without appending anything.
func appendRulePredicate(f *txnFilter, r ruleEntry) bool {
	matchExpr, matchArg, ok := ruleMatchSQL(r.MatchType, r.Pattern, len(f.args)+1)
	if !ok {
		return false
	}
	// ruleMatchSQL already substituted the placeholder index into matchExpr, so
	// bind it directly: f.param would run the clause through fmt.Sprintf a
	// second time and append a "%!(EXTRA int=N)" artifact, which PostgreSQL
	// rejects as a syntax error (it broke every /rules/apply and /rules/preview
	// call that had a pattern).
	f.args = append(f.args, matchArg)
	f.clauses = append(f.clauses, matchExpr)

	if r.AccountID != nil {
		f.param("account_id = $%d", *r.AccountID)
	}
	if r.FilterCategoryID != nil {
		f.param("category_id = $%d", *r.FilterCategoryID)
	}
	if r.FilterPayeeID != nil {
		f.param("payee_id = $%d", *r.FilterPayeeID)
	}
	if r.MinAmount != nil {
		f.param("amount >= $%d", *r.MinAmount)
	}
	if r.MaxAmount != nil {
		f.param("amount <= $%d", *r.MaxAmount)
	}
	if r.TxnType != "" {
		f.param("type = $%d", r.TxnType)
	}
	if r.DateFrom != nil {
		f.param("date >= $%d", *r.DateFrom)
	}
	if r.DateTo != nil {
		f.param("date <= $%d", *r.DateTo)
	}
	if r.IsLinked != nil {
		if *r.IsLinked {
			f.raw("EXISTS (SELECT 1 FROM links WHERE from_txn_id = transactions.id OR to_txn_id = transactions.id)")
		} else {
			f.raw("NOT EXISTS (SELECT 1 FROM links WHERE from_txn_id = transactions.id OR to_txn_id = transactions.id)")
		}
	}
	if r.IsRecurring != nil {
		if *r.IsRecurring {
			f.raw("EXISTS (SELECT 1 FROM recurring_attachments ra WHERE ra.transaction_id = transactions.id)")
		} else {
			f.raw("NOT EXISTS (SELECT 1 FROM recurring_attachments ra WHERE ra.transaction_id = transactions.id)")
		}
	}
	return true
}

// loadRules fetches the user's rules (with conditions/actions) ordered by
// descending priority. Priority defaults to 0, so ties are the norm rather than
// the exception; the id breaks them so the rule that auto-categorizes a new or
// imported transaction is the same on every run instead of whatever order the
// planner happened to return. It is the only stable key the rules table has.
func (srv *Server) loadRules(c *gin.Context, userID uuid.UUID) ([]ruleEntry, error) {
	rows, err := srv.db.Query(c,
		`SELECT pattern, match_type, category_id, payee_id,
		        account_id, filter_category_id, filter_payee_id, min_amount, max_amount, COALESCE(txn_type, ''),
		        date_from, date_to, is_linked, is_recurring, COALESCE(add_tags, '{}'), COALESCE(notes, '')
		 FROM rules WHERE user_id = $1 ORDER BY priority DESC, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rules := []ruleEntry{}
	for rows.Next() {
		var r ruleEntry
		var dateFrom, dateTo *time.Time
		if err := rows.Scan(&r.Pattern, &r.MatchType, &r.CatID, &r.PayeeID,
			&r.AccountID, &r.FilterCategoryID, &r.FilterPayeeID, &r.MinAmount, &r.MaxAmount, &r.TxnType,
			&dateFrom, &dateTo, &r.IsLinked, &r.IsRecurring, &r.AddTags, &r.Notes); err != nil {
			return nil, err
		}
		r.DateFrom = formatDatePtr(dateFrom)
		r.DateTo = formatDatePtr(dateTo)
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// matchRule reports whether a transaction description matches a rule pattern
// for the given match type ("contains", "starts_with", or "exact"). Matching is
// case-insensitive.
func matchRule(desc, pattern, matchType string) bool {
	descLower := strings.ToLower(desc)
	patternLower := strings.ToLower(pattern)

	switch matchType {
	case "contains":
		return strings.Contains(descLower, patternLower)
	case "starts_with":
		return strings.HasPrefix(descLower, patternLower)
	case "exact":
		return descLower == patternLower
	}
	return false
}

// ruleMatches reports whether a transaction context satisfies a rule's
// description pattern and every set condition. A rule with no conditions
// reduces to matchRule.
func ruleMatches(r ruleEntry, ctx ruleContext) bool {
	if !matchRule(ctx.Description, r.Pattern, r.MatchType) {
		return false
	}
	if r.AccountID != nil && *r.AccountID != ctx.AccountID {
		return false
	}
	if r.FilterCategoryID != nil {
		if ctx.CategoryID == nil || *ctx.CategoryID != *r.FilterCategoryID {
			return false
		}
	}
	if r.FilterPayeeID != nil {
		if ctx.PayeeID == nil || *ctx.PayeeID != *r.FilterPayeeID {
			return false
		}
	}
	if r.MinAmount != nil && ctx.Amount < *r.MinAmount {
		return false
	}
	if r.MaxAmount != nil && ctx.Amount > *r.MaxAmount {
		return false
	}
	if r.TxnType != "" && ctx.Type != r.TxnType {
		return false
	}
	if r.DateFrom != nil && ctx.Date < *r.DateFrom {
		return false
	}
	if r.DateTo != nil && ctx.Date > *r.DateTo {
		return false
	}
	if r.IsLinked != nil && *r.IsLinked != ctx.IsLinked {
		return false
	}
	if r.IsRecurring != nil && *r.IsRecurring != ctx.IsRecurring {
		return false
	}
	return true
}

// nullIfEmpty maps "" to a nil so an unset string column is stored as NULL.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// unionTags returns base plus additions, de-duplicated and preserving order
// (base first). Used to apply a rule's AddTags to a transaction's own tags.
func unionTags(base, additions []string) []string {
	if len(additions) == 0 {
		return base
	}
	seen := make(map[string]struct{}, len(base)+len(additions))
	out := make([]string, 0, len(base)+len(additions))
	for _, t := range base {
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	for _, t := range additions {
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// appendNote appends a rule's note to a transaction's existing notes (or
// returns the note when there are none), matching ApplyRules' newline join.
func appendNote(existing, note string) string {
	if note == "" {
		return existing
	}
	if existing == "" {
		return note
	}
	return existing + "\n" + note
}
