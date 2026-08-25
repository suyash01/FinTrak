package handlers

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GetRules lists the user's categorization rules, highest priority first, with
// the joined category name and payee name.
func GetRules(c *gin.Context) {
	rows, err := db.Pool.Query(c,
		`SELECT r.id, r.pattern, r.match_type, r.category_id, r.payee_id, COALESCE(p.name, '') as payee, r.priority,
		 COALESCE(c.name, '') as category_name
		 FROM rules r
		 LEFT JOIN categories c ON r.category_id = c.id
		 LEFT JOIN payees p ON r.payee_id = p.id
		 WHERE r.user_id = $1
		 ORDER BY r.priority DESC`, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error in GetRules: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	rules := []models.Rule{}
	for rows.Next() {
		var r models.Rule
		if err := rows.Scan(&r.ID, &r.Pattern, &r.MatchType, &r.CategoryID, &r.PayeeID, &r.Payee, &r.Priority, &r.CategoryName); err != nil {
			log.Printf("Error in GetRules scan: %v\n", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		rules = append(rules, r)
	}

	c.JSON(http.StatusOK, rules)
}

// CreateRule inserts a categorization rule, defaulting MatchType to "contains"
// and rejecting references to categories/payees the user doesn't own.
func CreateRule(c *gin.Context) {
	var req models.CreateRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	if req.MatchType == "" {
		req.MatchType = "contains"
	}

	var rule models.Rule
	err := db.Pool.QueryRow(c,
		`INSERT INTO rules (user_id, pattern, match_type, category_id, payee_id, priority)
		 SELECT $1, $2, $3, $4, $5, $6
		 WHERE EXISTS (SELECT 1 FROM categories c WHERE c.id = $4 AND c.user_id = $1)
		   AND ($5 IS NULL OR EXISTS (SELECT 1 FROM payees p WHERE p.id = $5 AND p.user_id = $1))
		 RETURNING id, pattern, match_type, category_id, payee_id, priority`,
		auth.GetUserID(c), req.Pattern, req.MatchType, req.CategoryID, req.PayeeID, req.Priority,
	).Scan(&rule.ID, &rule.Pattern, &rule.MatchType, &rule.CategoryID, &rule.PayeeID, &rule.Priority)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			validation.RespondError(c, "referenced category or payee not found", http.StatusBadRequest)
			return
		}
		log.Printf("Error in CreateRule: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusCreated, rule)
}

// DeleteRule removes a rule owned by the user.
func DeleteRule(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)
	result, err := db.Pool.Exec(c, "DELETE FROM rules WHERE id = $1 AND user_id = $2", id, userID)
	if err != nil {
		log.Printf("Error in DeleteRule: %v\n", err)
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
// category/payee and returning 404 when the rule isn't found.
func UpdateRule(c *gin.Context) {
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

	var rule models.Rule
	err = db.Pool.QueryRow(c,
		`UPDATE rules SET pattern = $1, match_type = $2, category_id = $3, payee_id = $4, priority = $5
		 WHERE id = $6 AND user_id = $7
		   AND EXISTS (SELECT 1 FROM categories c WHERE c.id = $3 AND c.user_id = $7)
		   AND ($4 IS NULL OR EXISTS (SELECT 1 FROM payees p WHERE p.id = $4 AND p.user_id = $7))
		 RETURNING id, pattern, match_type, category_id, payee_id, priority`,
		req.Pattern, req.MatchType, req.CategoryID, req.PayeeID, req.Priority, id, auth.GetUserID(c),
	).Scan(&rule.ID, &rule.Pattern, &rule.MatchType, &rule.CategoryID, &rule.PayeeID, &rule.Priority)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			validation.RespondError(c, "rule not found", http.StatusNotFound)
			return
		}
		log.Printf("Error in UpdateRule: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, rule)
}

// ApplyRules re-runs all of the user's rules against uncategorized transactions
// (category_id IS NULL or the "Uncategorized" category), applying the first
// matching rule's category and payee. Returns the number of transactions
// updated.
func ApplyRules(c *gin.Context) {
	userID := auth.GetUserID(c)

	// Get all rules
	rules, err := loadRules(c, userID)
	if err != nil {
		log.Printf("Error in ApplyRules (getting rules): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Find the "Uncategorized" category ID
	var uncategorizedID uuid.UUID
	err = db.Pool.QueryRow(c, "SELECT id FROM categories WHERE name = 'Uncategorized' AND user_id = $1 LIMIT 1", userID).Scan(&uncategorizedID)
	hasUncategorized := err == nil

	// Get uncategorized transactions (NULL or "Uncategorized" category)
	query := "SELECT id, description FROM transactions WHERE category_id IS NULL AND user_id = $1"
	args := []interface{}{userID}
	if hasUncategorized {
		query += " OR (category_id = $2 AND user_id = $1)"
		args = append(args, uncategorizedID)
	}

	txnRows, err := db.Pool.Query(c, query, args...)
	if err != nil {
		log.Printf("Error in ApplyRules (getting transactions): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer txnRows.Close()

	// Use a transaction for batch updates
	tx, err := db.Pool.Begin(c)
	if err != nil {
		log.Printf("Error in ApplyRules (starting transaction): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(c)

	updated := 0
	for txnRows.Next() {
		var txnID uuid.UUID
		var desc string
		if err := txnRows.Scan(&txnID, &desc); err != nil {
			log.Printf("Error in ApplyRules transaction scan: %v\n", err)
			continue
		}

		for _, r := range rules {
			if matchRule(desc, r.Pattern, r.MatchType) {
				updateQuery := "UPDATE transactions SET category_id = $1"
				updateArgs := []interface{}{r.CatID}
				if r.PayeeID != nil {
					updateQuery += ", payee_id = $2 WHERE id = $3 AND user_id = $4"
					updateArgs = append(updateArgs, r.PayeeID, txnID, userID)
				} else {
					updateQuery += " WHERE id = $2 AND user_id = $3"
					updateArgs = append(updateArgs, txnID, userID)
				}
				_, err := tx.Exec(c, updateQuery, updateArgs...)
				if err != nil {
					log.Printf("Error in ApplyRules (updating transaction %v with rule %v): %v\n", txnID, r.Pattern, err)
					continue
				}
				updated++
				break
			}
		}
	}

	if err := tx.Commit(c); err != nil {
		log.Printf("Error in ApplyRules (committing transaction): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": updated})
}

// ruleEntry is the minimal rule representation used for in-memory matching
// during transaction creation, imports, and ApplyRules.
type ruleEntry struct {
	Pattern   string
	MatchType string
	CatID     uuid.UUID
	PayeeID   *uuid.UUID
}

// loadRules fetches the user's rules ordered by descending priority.
func loadRules(c *gin.Context, userID uuid.UUID) ([]ruleEntry, error) {
	rows, err := db.Pool.Query(c,
		"SELECT pattern, match_type, category_id, payee_id FROM rules WHERE user_id = $1 ORDER BY priority DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rules := []ruleEntry{}
	for rows.Next() {
		var r ruleEntry
		if err := rows.Scan(&r.Pattern, &r.MatchType, &r.CatID, &r.PayeeID); err != nil {
			return nil, err
		}
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
