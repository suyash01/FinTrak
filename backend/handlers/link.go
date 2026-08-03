package handlers

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func GetLinks(c *gin.Context) {
	linkType := c.Query("type")

	query := `SELECT l.id, l.type, l.from_txn_id, l.to_txn_id, l.notes, l.created_at,
			  ft.date, ft.description, ft.amount, ft.type, fa.name,
			  tt.date, tt.description, tt.amount, tt.type, ta.name
			  FROM links l
			  JOIN transactions ft ON l.from_txn_id = ft.id
			  JOIN accounts fa ON ft.account_id = fa.id
			  JOIN transactions tt ON l.to_txn_id = tt.id
			  JOIN accounts ta ON tt.account_id = ta.id
			  WHERE l.user_id = $1`

	args := []interface{}{auth.GetUserID(c)}
	if linkType != "" {
		query += " AND l.type = $2"
		args = append(args, linkType)
	}
	query += " ORDER BY l.created_at DESC"

	rows, err := db.Pool.Query(c, query, args...)
	if err != nil {
		log.Printf("Error in GetLinks: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
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
			log.Printf("Error in GetLinks scan: %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}
		l.FromTxn.ID = l.FromTxnID
		l.ToTxn.ID = l.ToTxnID
		links = append(links, l)
	}

	c.JSON(http.StatusOK, links)
}

func CreateLink(c *gin.Context) {
	var req models.CreateLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tx, err := db.Pool.Begin(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer tx.Rollback(c)

	var link models.Link
	userID := auth.GetUserID(c)
	err = tx.QueryRow(c,
		`INSERT INTO links (user_id, type, from_txn_id, to_txn_id, notes) VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, type, from_txn_id, to_txn_id, notes, created_at`,
		userID, req.Type, req.FromTxnID, req.ToTxnID, req.Notes,
	).Scan(&link.ID, &link.Type, &link.FromTxnID, &link.ToTxnID, &link.Notes, &link.CreatedAt)

	if err != nil {
		log.Printf("Error inserting link in CreateLink: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}

		// Set Payee for both transactions
		// Debit txn payee = Credit account name
		// Credit txn payee = Debit account name
		_, err = tx.Exec(c, `
			UPDATE transactions t1
			SET payee = a2.name
			FROM transactions t2
			JOIN accounts a2 ON t2.account_id = a2.id
			WHERE t1.id = $1 AND t2.id = $2 AND t1.user_id = $3`,
			req.FromTxnID, req.ToTxnID, userID,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}

		_, err = tx.Exec(c, `
			UPDATE transactions t1
			SET payee = a2.name
			FROM transactions t2
			JOIN accounts a2 ON t2.account_id = a2.id
			WHERE t1.id = $1 AND t2.id = $2 AND t1.user_id = $3`,
			req.ToTxnID, req.FromTxnID, userID,
		)
		if err != nil {
			log.Printf("Error updating payee for ToTxn: %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}
	}

	if err := tx.Commit(c); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusCreated, link)
}

func BulkCreateLinks(c *gin.Context) {
	var req models.BulkCreateLinksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tx, err := db.Pool.Begin(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer tx.Rollback(c)

	createdCount := 0
	userID := auth.GetUserID(c)
	for _, l := range req.Links {
		_, err = tx.Exec(c,
			`INSERT INTO links (user_id, type, from_txn_id, to_txn_id, notes) VALUES ($1, $2, $3, $4, $5)`,
			userID, l.Type, l.FromTxnID, l.ToTxnID, l.Notes,
		)
		if err != nil {
			log.Printf("Error inserting link in BulkCreateLinks loop: %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}

		if l.Type == "transfer" {
			_, err = tx.Exec(c,
				`UPDATE transactions SET category_id = (SELECT id FROM categories WHERE name = 'Transfer' AND user_id = $3 LIMIT 1)
				 WHERE id IN ($1, $2) AND user_id = $3`,
				l.FromTxnID, l.ToTxnID, userID,
			)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
				return
			}

			// Set Payee for both transactions
			_, err = tx.Exec(c, `
				UPDATE transactions t1
				SET payee = a2.name
				FROM transactions t2
				JOIN accounts a2 ON t2.account_id = a2.id
				WHERE t1.id = $1 AND t2.id = $2 AND t1.user_id = $3`,
				l.FromTxnID, l.ToTxnID, userID,
			)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
				return
			}

			_, err = tx.Exec(c, `
				UPDATE transactions t1
				SET payee = a2.name
				FROM transactions t2
				JOIN accounts a2 ON t2.account_id = a2.id
				WHERE t1.id = $1 AND t2.id = $2 AND t1.user_id = $3`,
				l.ToTxnID, l.FromTxnID, userID,
			)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
				return
			}
		}
		createdCount++
	}

	if err := tx.Commit(c); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"createdCount": createdCount})
}

func DeleteLink(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	tx, err := db.Pool.Begin(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer tx.Rollback(c)

	// Get associated transactions
	var fromTxnID, toTxnID uuid.UUID
	err = tx.QueryRow(c, "SELECT from_txn_id, to_txn_id FROM links WHERE id = $1 AND user_id = $2", id, auth.GetUserID(c)).Scan(&fromTxnID, &toTxnID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "link not found"})
		return
	}

	// Delete the link
	_, err = tx.Exec(c, "DELETE FROM links WHERE id = $1 AND user_id = $2", id, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error deleting link in DeleteLink: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	// Clear category and payee for both transactions
	_, err = tx.Exec(c, `
		UPDATE transactions 
		SET category_id = NULL, payee = '' 
		WHERE id IN ($1, $2)`,
		fromTxnID, toTxnID,
	)
	if err != nil {
		log.Printf("Error resetting transactions in DeleteLink: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	if err := tx.Commit(c); err != nil {
		log.Printf("Error committing transaction in DeleteLink: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func BulkDeleteLinks(c *gin.Context) {
	var req models.BulkDeleteLinksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.IDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"message": "nothing to delete", "deletedCount": 0})
		return
	}

	tx, err := db.Pool.Begin(c)
	if err != nil {
		log.Printf("Error starting transaction in BulkDeleteLinks: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer tx.Rollback(c)

	// Get all associated transaction IDs before deleting links
	rows, err := tx.Query(c, "SELECT from_txn_id, to_txn_id FROM links WHERE id = ANY($1) AND user_id = $2", req.IDs, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error querying links in BulkDeleteLinks: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer rows.Close()

	txnIDs := []uuid.UUID{}
	for rows.Next() {
		var fromID, toID uuid.UUID
		if err := rows.Scan(&fromID, &toID); err == nil {
			txnIDs = append(txnIDs, fromID, toID)
		}
	}

	// Delete links
	_, err = tx.Exec(c, "DELETE FROM links WHERE id = ANY($1) AND user_id = $2", req.IDs, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error deleting links in BulkDeleteLinks: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	// Reset category and payee for all affected transactions
	if len(txnIDs) > 0 {
		_, err = tx.Exec(c, "UPDATE transactions SET category_id = NULL, payee = '' WHERE id = ANY($1)", txnIDs)
		if err != nil {
			log.Printf("Error resetting transactions in BulkDeleteLinks: %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}
	}

	if err := tx.Commit(c); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted", "deletedCount": len(req.IDs)})
}

func GetTransferSuggestions(c *gin.Context) {
	// Find debit transactions that might match credit transactions in other accounts
	// within ±3 days and same amounts
	rows, err := db.Pool.Query(c, `
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
			  AND ABS(t.date - d.date) <= 3
			  AND NOT EXISTS (SELECT 1 FROM links WHERE (from_txn_id = d.id OR to_txn_id = d.id))
		) cr
		JOIN accounts ca ON cr.account_id = ca.id
		WHERE d.type = 'debit' AND d.user_id = $1
		ORDER BY d.date DESC
		LIMIT 50
	`, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error in GetTransferSuggestions: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
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

	c.JSON(http.StatusOK, suggestions)
}

func calculateTransferScore(debitTxn, creditTxn models.Transaction) float64 {
	// Calculate score based on amount match and date proximity
	amountDiff := math.Abs(debitTxn.Amount - creditTxn.Amount)
	dTime := debitTxn.Date
	cTime := creditTxn.Date
	daysDiff := math.Abs(float64(dTime.Sub(cTime).Hours() / 24))

	score := 100 - amountDiff*10 - daysDiff*5
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

func GetCashbackSuggestions(c *gin.Context) {
	rows, err := db.Pool.Query(c, `
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
			ORDER BY t.date DESC
			LIMIT 3
		) orig
		JOIN accounts oa ON orig.account_id = oa.id
		WHERE cb.type = 'credit'
		  AND cb.user_id = $1
		  AND (LOWER(cb.description) LIKE '%cashback%'
		       OR LOWER(cb.description) LIKE '%cash back%'
		       OR LOWER(cb.description) LIKE '%reward%'
		       OR LOWER(cb.description) LIKE '%refund%')
		  AND NOT EXISTS (SELECT 1 FROM links WHERE type = 'cashback' AND to_txn_id = cb.id)
		ORDER BY cb.date DESC
		LIMIT 50
	`, auth.GetUserID(c))
	if err != nil {
		// Fallback: if the complex query fails, return empty
		fmt.Printf("Cashback query error: %v\n", err)
		c.JSON(http.StatusOK, []models.TransferSuggestion{})
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
			continue
		}
		s.Score = 70
		suggestions = append(suggestions, s)
	}

	c.JSON(http.StatusOK, suggestions)
}
