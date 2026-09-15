package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GetLinks lists the user's links, optionally filtered by type and/or a
// transaction ID, newest first. Both linked transactions are joined in with
// their account names for display.
func (srv *Server) GetLinks(c *gin.Context) {
	linkType := c.Query("type")
	txnID := c.Query("txnId")

	query := `SELECT l.id, l.type, l.from_txn_id, l.to_txn_id, l.notes, l.created_at,
			  ft.date, ft.description, ft.amount, ft.type, fa.name,
			  tt.date, tt.description, tt.amount, tt.type, ta.name
			  FROM links l
			  JOIN transactions ft ON l.from_txn_id = ft.id AND ft.user_id = l.user_id
			  JOIN accounts fa ON ft.account_id = fa.id
			  JOIN transactions tt ON l.to_txn_id = tt.id AND tt.user_id = l.user_id
			  JOIN accounts ta ON tt.account_id = ta.id
			  WHERE l.user_id = $1`

	args := []interface{}{auth.GetUserID(c)}
	paramIdx := 2
	if linkType != "" {
		query += fmt.Sprintf(" AND l.type = $%d", paramIdx)
		args = append(args, linkType)
		paramIdx++
	}
	if txnID != "" {
		query += fmt.Sprintf(" AND (l.from_txn_id = $%d OR l.to_txn_id = $%d)", paramIdx, paramIdx+1)
		args = append(args, txnID, txnID)
	}
	query += " ORDER BY l.created_at DESC"

	rows, err := srv.db.Query(c, query, args...)
	if err != nil {
		slog.Error("GetLinks", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	links := []models.Link{}
	for rows.Next() {
		var l models.Link
		l.FromTxn = &models.Transaction{}
		l.ToTxn = &models.Transaction{}
		if err := rows.Scan(&l.ID, &l.Type, &l.FromTxnID, &l.ToTxnID, &l.Notes, &l.CreatedAt,
			&l.FromTxn.Date, &l.FromTxn.Description, &l.FromTxn.Amount, &l.FromTxn.Type, &l.FromTxn.AccountName,
			&l.ToTxn.Date, &l.ToTxn.Description, &l.ToTxn.Amount, &l.ToTxn.Type, &l.ToTxn.AccountName); err != nil {
			slog.Error("GetLinks scan", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		l.FromTxn.ID = l.FromTxnID
		l.ToTxn.ID = l.ToTxnID
		links = append(links, l)
	}
	if err := rows.Err(); err != nil {
		slog.Error("GetLinks rows", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, links)
}

// isValidLinkType reports whether t is one of the supported link types.
func isValidLinkType(t string) bool {
	return t == "transfer" || t == "cashback" || t == "refund" || t == "bill_payment"
}

// CreateLink links two transactions owned by the user, rejecting invalid types,
// missing transactions, and exact duplicates. For "transfer" links it also
// re-categorizes both transactions as "Transfer" and swaps their payees to the
// counterpart account's linked payee. Runs in a transaction.
func (srv *Server) CreateLink(c *gin.Context) {
	var req models.CreateLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	tx, err := srv.db.Begin(c)
	if err != nil {
		slog.Error("starting transaction in CreateLink", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(c)

	if !isValidLinkType(req.Type) {
		validation.RespondError(c, "invalid link type", http.StatusBadRequest)
		return
	}

	// A transaction cannot be linked to itself; the DB enforces this with a
	// CHECK constraint, but reject it here so the client gets a clear 400
	// instead of a constraint-violation 500.
	if req.FromTxnID == req.ToTxnID {
		validation.RespondError(c, "cannot link a transaction to itself", http.StatusBadRequest)
		return
	}

	// Verify both transactions belong to the requesting user before linking.
	var link models.Link
	userID := auth.GetUserID(c)
	var owned int
	if err := tx.QueryRow(c,
		"SELECT COUNT(*) FROM transactions WHERE id = ANY($1) AND user_id = $2",
		[]uuid.UUID{req.FromTxnID, req.ToTxnID}, userID,
	).Scan(&owned); err != nil {
		slog.Error("checking transaction ownership in CreateLink", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if owned != 2 {
		validation.RespondError(c, "one or both transactions not found", http.StatusNotFound)
		return
	}

	// Insert atomically: the unique index on (user_id, type, from_txn_id,
	// to_txn_id) makes concurrent duplicate requests safe (a plain
	// check-then-insert would let two requests both pass the check). A conflict
	// means this exact link already exists; the same transaction may still
	// appear in many links with different partners (one-to-many).
	err = tx.QueryRow(c,
		`INSERT INTO links (user_id, type, from_txn_id, to_txn_id, notes) VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (user_id, type, from_txn_id, to_txn_id) DO NOTHING
		 RETURNING id, type, from_txn_id, to_txn_id, notes, created_at`,
		userID, req.Type, req.FromTxnID, req.ToTxnID, req.Notes,
	).Scan(&link.ID, &link.Type, &link.FromTxnID, &link.ToTxnID, &link.Notes, &link.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "link already exists", http.StatusConflict)
		return
	}
	if err != nil {
		slog.Error("inserting link in CreateLink", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Auto-categorize and set payee if it's a transfer
	if req.Type == "transfer" {
		_, err = tx.Exec(c,
			`UPDATE transactions SET category_id = (SELECT id FROM categories WHERE name = 'Transfer' AND user_id = $3 LIMIT 1)
			 WHERE id IN ($1, $2) AND user_id = $3`,
			req.FromTxnID, req.ToTxnID, userID,
		)
		if err != nil {
			slog.Error("updating category for transfer in CreateLink", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}

		// Set Payee for both transactions
		// Debit txn payee = Credit account's linked payee
		// Credit txn payee = Debit account's linked payee
		_, err = tx.Exec(c, `
			UPDATE transactions t1
			SET payee_id = (SELECT p.id FROM payees p WHERE p.account_id = a2.id)
			FROM transactions t2
			JOIN accounts a2 ON t2.account_id = a2.id
			WHERE t1.id = $1 AND t2.id = $2 AND t1.user_id = $3`,
			req.FromTxnID, req.ToTxnID, userID,
		)
		if err != nil {
			slog.Error("updating payee for FromTxn in CreateLink", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}

		_, err = tx.Exec(c, `
			UPDATE transactions t1
			SET payee_id = (SELECT p.id FROM payees p WHERE p.account_id = a2.id)
			FROM transactions t2
			JOIN accounts a2 ON t2.account_id = a2.id
			WHERE t1.id = $1 AND t2.id = $2 AND t1.user_id = $3`,
			req.ToTxnID, req.FromTxnID, userID,
		)
		if err != nil {
			slog.Error("updating payee for ToTxn", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(c); err != nil {
		slog.Error("committing transaction in CreateLink", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusCreated, link)
}

// BulkCreateLinks creates many links in one transaction, validating each entry,
// skipping exact duplicates, and applying the same transfer re-categorization
// as CreateLink. Returns the number of links actually created.
func (srv *Server) BulkCreateLinks(c *gin.Context) {
	var req models.BulkCreateLinksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}
	if len(req.Links) > maxBulkBatch {
		validation.RespondError(c, fmt.Sprintf("too many links (max %d per request)", maxBulkBatch), http.StatusBadRequest)
		return
	}

	tx, err := srv.db.Begin(c)
	if err != nil {
		slog.Error("starting transaction in BulkCreateLinks", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(c)

	createdCount := 0
	userID := auth.GetUserID(c)
	for _, l := range req.Links {
		if !isValidLinkType(l.Type) {
			validation.RespondError(c, "invalid link type", http.StatusBadRequest)
			return
		}

		if l.FromTxnID == l.ToTxnID {
			validation.RespondError(c, "cannot link a transaction to itself", http.StatusBadRequest)
			return
		}

		// Verify both transactions belong to the requesting user before linking.
		var owned int
		if err := tx.QueryRow(c,
			"SELECT COUNT(*) FROM transactions WHERE id = ANY($1) AND user_id = $2",
			[]uuid.UUID{l.FromTxnID, l.ToTxnID}, userID,
		).Scan(&owned); err != nil {
			slog.Error("checking transaction ownership in BulkCreateLinks", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		if owned != 2 {
			validation.RespondError(c, "one or both transactions not found", http.StatusNotFound)
			return
		}

		// Insert atomically, skipping exact duplicates; a transaction may still
		// be linked to many partners. The unique index makes this safe under
		// concurrency (and against repeats within the same request).
		tag, err := tx.Exec(c,
			`INSERT INTO links (user_id, type, from_txn_id, to_txn_id, notes) VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (user_id, type, from_txn_id, to_txn_id) DO NOTHING`,
			userID, l.Type, l.FromTxnID, l.ToTxnID, l.Notes,
		)
		if err != nil {
			slog.Error("inserting link in BulkCreateLinks loop", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		if tag.RowsAffected() == 0 {
			continue
		}

		if l.Type == "transfer" {
			_, err = tx.Exec(c,
				`UPDATE transactions SET category_id = (SELECT id FROM categories WHERE name = 'Transfer' AND user_id = $3 LIMIT 1)
				 WHERE id IN ($1, $2) AND user_id = $3`,
				l.FromTxnID, l.ToTxnID, userID,
			)
			if err != nil {
				slog.Error("updating category for transfer in BulkCreateLinks", slog.String("error", err.Error()))
				validation.RespondError(c, "internal server error", http.StatusInternalServerError)
				return
			}

			// Set Payee for both transactions
			_, err = tx.Exec(c, `
				UPDATE transactions t1
				SET payee_id = (SELECT p.id FROM payees p WHERE p.account_id = a2.id)
				FROM transactions t2
				JOIN accounts a2 ON t2.account_id = a2.id
				WHERE t1.id = $1 AND t2.id = $2 AND t1.user_id = $3`,
				l.FromTxnID, l.ToTxnID, userID,
			)
			if err != nil {
				slog.Error("updating payee in BulkCreateLinks (from txn)", slog.String("error", err.Error()))
				validation.RespondError(c, "internal server error", http.StatusInternalServerError)
				return
			}

			_, err = tx.Exec(c, `
				UPDATE transactions t1
				SET payee_id = (SELECT p.id FROM payees p WHERE p.account_id = a2.id)
				FROM transactions t2
				JOIN accounts a2 ON t2.account_id = a2.id
				WHERE t1.id = $1 AND t2.id = $2 AND t1.user_id = $3`,
				l.ToTxnID, l.FromTxnID, userID,
			)
			if err != nil {
				slog.Error("updating payee in BulkCreateLinks (to txn)", slog.String("error", err.Error()))
				validation.RespondError(c, "internal server error", http.StatusInternalServerError)
				return
			}
		}
		createdCount++
	}

	if err := tx.Commit(c); err != nil {
		slog.Error("committing transaction in BulkCreateLinks", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"createdCount": createdCount})
}

// DeleteLink removes a link. If the link was a transfer, it also clears the
// transfer-derived category/payee from its two transactions — but only when no
// other link still references them. Non-transfer links (cashback, refund,
// bill_payment) never touched category/payee, so deleting them leaves the
// user's own categorization intact.
func (srv *Server) DeleteLink(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	tx, err := srv.db.Begin(c)
	if err != nil {
		slog.Error("starting transaction in DeleteLink", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(c)

	// Get associated transactions and the link type: only transfers mutate
	// category/payee, so only those are reset on delete.
	var linkType string
	var fromTxnID, toTxnID uuid.UUID
	err = tx.QueryRow(c, "SELECT type, from_txn_id, to_txn_id FROM links WHERE id = $1 AND user_id = $2", id, auth.GetUserID(c)).Scan(&linkType, &fromTxnID, &toTxnID)
	if err != nil {
		slog.Error("looking up link in DeleteLink", slog.String("error", err.Error()))
		validation.RespondError(c, "link not found", http.StatusNotFound)
		return
	}

	// Delete the link
	_, err = tx.Exec(c, "DELETE FROM links WHERE id = $1 AND user_id = $2", id, auth.GetUserID(c))
	if err != nil {
		slog.Error("deleting link in DeleteLink", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Clear category and payee for both transactions, but only for transfers
	// and only if no other link still references them (a txn may belong to
	// multiple links).
	if linkType == "transfer" {
		_, err = tx.Exec(c, `
			UPDATE transactions 
			SET category_id = NULL, payee_id = NULL 
			WHERE id = ANY($1) AND user_id = $2
			  AND NOT EXISTS (
			      SELECT 1 FROM links l2 
			      WHERE (l2.from_txn_id = transactions.id OR l2.to_txn_id = transactions.id)
			        AND l2.id != $3
			  )`,
			[]uuid.UUID{fromTxnID, toTxnID}, auth.GetUserID(c), id,
		)
		if err != nil {
			slog.Error("resetting transactions in DeleteLink", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(c); err != nil {
		slog.Error("committing transaction in DeleteLink", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// BulkDeleteLinks removes many links at once. For the transfer links among
// them, it resets the transfer-derived category/payee on any transaction no
// longer referenced by a remaining link. Non-transfer links are deleted without
// touching the user's own category/payee.
func (srv *Server) BulkDeleteLinks(c *gin.Context) {
	var req models.BulkDeleteLinksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}
	if len(req.IDs) > maxBulkBatch {
		validation.RespondError(c, fmt.Sprintf("too many link ids (max %d per request)", maxBulkBatch), http.StatusBadRequest)
		return
	}
	if len(req.IDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"message": "nothing to delete", "deletedCount": 0})
		return
	}

	tx, err := srv.db.Begin(c)
	if err != nil {
		slog.Error("starting transaction in BulkDeleteLinks", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(c)

	// Collect the transactions of the transfer links being deleted. Only
	// transfers mutate category/payee, so non-transfer links contribute no
	// transaction IDs to reset.
	rows, err := tx.Query(c, "SELECT type, from_txn_id, to_txn_id FROM links WHERE id = ANY($1) AND user_id = $2", req.IDs, auth.GetUserID(c))
	if err != nil {
		slog.Error("querying links in BulkDeleteLinks", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	txnIDs := []uuid.UUID{}
	for rows.Next() {
		var linkType string
		var fromID, toID uuid.UUID
		if err := rows.Scan(&linkType, &fromID, &toID); err != nil {
			slog.Error("scanning link row in BulkDeleteLinks", slog.String("error", err.Error()))
			continue
		}
		if linkType == "transfer" {
			txnIDs = append(txnIDs, fromID, toID)
		}
	}
	if err := rows.Err(); err != nil {
		slog.Error("iterating links in BulkDeleteLinks", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Delete links
	_, err = tx.Exec(c, "DELETE FROM links WHERE id = ANY($1) AND user_id = $2", req.IDs, auth.GetUserID(c))
	if err != nil {
		slog.Error("deleting links in BulkDeleteLinks", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Reset category and payee for all affected transactions that are no
	// longer referenced by any remaining link.
	if len(txnIDs) > 0 {
		_, err = tx.Exec(c, `
			UPDATE transactions SET category_id = NULL, payee_id = NULL 
			WHERE id = ANY($1) AND user_id = $2
			  AND NOT EXISTS (
			      SELECT 1 FROM links l2 
			      WHERE l2.from_txn_id = transactions.id OR l2.to_txn_id = transactions.id
			  )`,
			txnIDs, auth.GetUserID(c),
		)
		if err != nil {
			slog.Error("resetting transactions in BulkDeleteLinks", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(c); err != nil {
		slog.Error("committing transaction in BulkDeleteLinks", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted", "deletedCount": len(req.IDs)})
}

// Suggestion pagination. The suggestion endpoints expose the same page/limit
// contract as GetTransactions, but with a much lower page cap: every row is a
// scored pair and the LATERAL match expansion makes a large page costly. The
// handlers fetch one extra row (limit+1) to report hasMore without running a
// second count query across the LATERAL.
const (
	defaultSuggestionLimit = 50
	maxSuggestionLimit     = 100
)

// parseSuggestionPaging reads and clamps the page/limit query params shared by
// the transfer and cashback suggestion endpoints, mirroring GetTransactions.
// Invalid or out-of-range values fall back to the defaults so a crafted request
// cannot force an unbounded scan.
func parseSuggestionPaging(c *gin.Context) (page, limit, offset int) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	// Cap the page so (page-1)*limit can't overflow int into a negative offset.
	if page > maxPage {
		page = maxPage
	}

	limit, _ = strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(defaultSuggestionLimit)))
	if limit < 1 {
		limit = defaultSuggestionLimit
	}
	if limit > maxSuggestionLimit {
		limit = maxSuggestionLimit
	}

	offset = (page - 1) * limit
	return page, limit, offset
}

// GetTransferSuggestions proposes debit/credit pairs across different accounts
// that are likely transfers: same amount within ±3 days, not already linked.
// Each suggestion carries a confidence score. Results are paginated newest
// first; the per-debit match expansion stays capped at five credits.
func (srv *Server) GetTransferSuggestions(c *gin.Context) {
	page, limit, offset := parseSuggestionPaging(c)

	// Find debit transactions that might match credit transactions in other accounts
	// within ±3 days and same amounts. The outer ORDER BY ends with d.id so
	// paging is deterministic when several debits share a date.
	rows, err := srv.db.Query(c, `
		SELECT d.id, d.account_id, d.date, d.description, d.amount, d.type, da.name as d_account,
			   cr.id, cr.account_id, cr.date, cr.description, cr.amount, cr.type, ca.name as c_account
		FROM transactions d
		JOIN accounts da ON d.account_id = da.id
		CROSS JOIN LATERAL (
			SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type
			FROM transactions t
			WHERE t.account_id != d.account_id
			  AND t.user_id = d.user_id
			  AND t.type = 'credit'
			  AND t.amount = d.amount
			  AND t.date >= d.date - 3
			  AND t.date <= d.date + 3
			  AND NOT EXISTS (SELECT 1 FROM links WHERE (from_txn_id = d.id OR to_txn_id = d.id))
			  AND NOT EXISTS (SELECT 1 FROM links WHERE (from_txn_id = t.id OR to_txn_id = t.id))
			ORDER BY t.date DESC, t.id
			LIMIT 5
			) cr
		JOIN accounts ca ON cr.account_id = ca.id
		WHERE d.type = 'debit' AND d.user_id = $1
		ORDER BY d.date DESC, d.id
		LIMIT $2 OFFSET $3
	`, auth.GetUserID(c), limit+1, offset)
	if err != nil {
		slog.Error("GetTransferSuggestions", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	suggestions := []models.TransferSuggestion{}
	for rows.Next() {
		var s models.TransferSuggestion
		if err := rows.Scan(
			&s.DebitTxn.ID, &s.DebitTxn.AccountID, &s.DebitTxn.Date, &s.DebitTxn.Description, &s.DebitTxn.Amount, &s.DebitTxn.Type, &s.DebitTxn.AccountName,
			&s.CreditTxn.ID, &s.CreditTxn.AccountID, &s.CreditTxn.Date, &s.CreditTxn.Description, &s.CreditTxn.Amount, &s.CreditTxn.Type, &s.CreditTxn.AccountName,
		); err != nil {
			continue
		}

		s.Score = calculateTransferScore(s.DebitTxn, s.CreditTxn)
		suggestions = append(suggestions, s)
	}
	if err := rows.Err(); err != nil {
		slog.Error("link suggestions rows", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	hasMore := len(suggestions) > limit
	if hasMore {
		suggestions = suggestions[:limit]
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    suggestions,
		"page":    page,
		"limit":   limit,
		"hasMore": hasMore,
	})
}

// calculateTransferScore ranks a transfer suggestion from 0-100. It starts at
// 100 and subtracts for amount mismatch and date distance, then bumps the score
// (capped at 100) when the descriptions contain transfer-related keywords.
func calculateTransferScore(debitTxn, creditTxn models.Transaction) float64 {
	// Calculate score based on amount match and date proximity
	amountDiff := (debitTxn.Amount - creditTxn.Amount).Abs()
	dTime := debitTxn.Date
	cTime := creditTxn.Date
	daysDiff := math.Abs(float64(dTime.Sub(cTime).Hours() / 24))

	score := 100 - amountDiff.Float64()*10 - daysDiff*5
	if score < 0 {
		score = 0
	}

	// Check for common transfer keywords
	descLower := strings.ToLower(debitTxn.Description + " " + creditTxn.Description)
	transferKeywords := []string{"transfer", "neft", "rtgs", "imps", "upi", "fund transfer", "self"}
	for _, kw := range transferKeywords {
		if strings.Contains(descLower, kw) {
			score = math.Min(100, score+15)
			break
		}
	}
	return score
}

// GetCashbackSuggestions finds credit transactions whose description suggests a
// cashback/reward/refund and pairs each with up to three prior debits on the
// same account (within 90 days) as the likely originating purchase, excluding
// already-linked cashbacks. Results are paginated newest first.
func (srv *Server) GetCashbackSuggestions(c *gin.Context) {
	page, limit, offset := parseSuggestionPaging(c)

	rows, err := srv.db.Query(c, `
		SELECT cb.id, cb.account_id, cb.date, cb.description, cb.amount, cb.type, ca.name,
		       orig.id, orig.account_id, orig.date, orig.description, orig.amount, orig.type, oa.name
		FROM transactions cb
		JOIN accounts ca ON cb.account_id = ca.id
		CROSS JOIN LATERAL (
			SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type
			FROM transactions t
			WHERE t.account_id = cb.account_id
			  AND t.user_id = cb.user_id
			  AND t.type = 'debit'
			  AND t.date <= cb.date
			  AND t.date >= cb.date - 90
			  AND NOT EXISTS (SELECT 1 FROM links WHERE type = 'cashback' AND to_txn_id = cb.id)
			ORDER BY t.date DESC, t.id
			LIMIT 3
		) orig
		JOIN accounts oa ON orig.account_id = oa.id
		WHERE cb.type = 'credit'
		  AND cb.user_id = $1
		  AND (cb.description ILIKE '%cashback%'
		       OR cb.description ILIKE '%cash back%'
		       OR cb.description ILIKE '%reward%'
		       OR cb.description ILIKE '%refund%')
		  AND NOT EXISTS (SELECT 1 FROM links WHERE type = 'cashback' AND to_txn_id = cb.id)
		ORDER BY cb.date DESC, cb.id
		LIMIT $2 OFFSET $3
	`, auth.GetUserID(c), limit+1, offset)
	if err != nil {
		slog.Error("GetCashbackSuggestions", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	suggestions := []models.TransferSuggestion{}
	for rows.Next() {
		var s models.TransferSuggestion
		if err := rows.Scan(
			&s.CreditTxn.ID, &s.CreditTxn.AccountID, &s.CreditTxn.Date, &s.CreditTxn.Description, &s.CreditTxn.Amount, &s.CreditTxn.Type, &s.CreditTxn.AccountName,
			&s.DebitTxn.ID, &s.DebitTxn.AccountID, &s.DebitTxn.Date, &s.DebitTxn.Description, &s.DebitTxn.Amount, &s.DebitTxn.Type, &s.DebitTxn.AccountName,
		); err != nil {
			slog.Error("GetCashbackSuggestions scan", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		s.Score = 70
		suggestions = append(suggestions, s)
	}
	if err := rows.Err(); err != nil {
		slog.Error("link suggestions rows", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	hasMore := len(suggestions) > limit
	if hasMore {
		suggestions = suggestions[:limit]
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    suggestions,
		"page":    page,
		"limit":   limit,
		"hasMore": hasMore,
	})
}
