package handlers

import (
	"log"
	"net/http"
	"strings"

	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func GetRules(c *gin.Context) {
	rows, err := db.Pool.Query(c,
		`SELECT r.id, r.pattern, r.match_type, r.category_id, r.payee_id, COALESCE(p.name, '') as payee, r.priority,
		 COALESCE(c.name, '') as category_name
		 FROM rules r
		 LEFT JOIN categories c ON r.category_id = c.id
		 LEFT JOIN payees p ON r.payee_id = p.id
		 ORDER BY r.priority DESC`)
	if err != nil {
		log.Printf("Error in GetRules: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer rows.Close()

	rules := []models.Rule{}
	for rows.Next() {
		var r models.Rule
		if err := rows.Scan(&r.ID, &r.Pattern, &r.MatchType, &r.CategoryID, &r.PayeeID, &r.Payee, &r.Priority, &r.CategoryName); err != nil {
			log.Printf("Error in GetRules scan: %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}
		rules = append(rules, r)
	}

	c.JSON(http.StatusOK, rules)
}

func CreateRule(c *gin.Context) {
	var req models.CreateRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.MatchType == "" {
		req.MatchType = "contains"
	}

	var rule models.Rule
	err := db.Pool.QueryRow(c,
		`INSERT INTO rules (pattern, match_type, category_id, payee_id, priority) VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, pattern, match_type, category_id, payee_id, priority`,
		req.Pattern, req.MatchType, req.CategoryID, req.PayeeID, req.Priority,
	).Scan(&rule.ID, &rule.Pattern, &rule.MatchType, &rule.CategoryID, &rule.PayeeID, &rule.Priority)

	if err != nil {
		log.Printf("Error in CreateRule: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusCreated, rule)
}

func DeleteRule(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	_, err = db.Pool.Exec(c, "DELETE FROM rules WHERE id = $1", id)
	if err != nil {
		log.Printf("Error in DeleteRule: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func UpdateRule(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req models.UpdateRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var rule models.Rule
	err = db.Pool.QueryRow(c,
		`UPDATE rules SET pattern = $1, match_type = $2, category_id = $3, payee_id = $4, priority = $5
		 WHERE id = $6 RETURNING id, pattern, match_type, category_id, payee_id, priority`,
		req.Pattern, req.MatchType, req.CategoryID, req.PayeeID, req.Priority, id,
	).Scan(&rule.ID, &rule.Pattern, &rule.MatchType, &rule.CategoryID, &rule.PayeeID, &rule.Priority)

	if err != nil {
		log.Printf("Error in UpdateRule: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, rule)
}

func ApplyRules(c *gin.Context) {
	// Get all rules
	rows, err := db.Pool.Query(c, "SELECT pattern, match_type, category_id, payee_id FROM rules ORDER BY priority DESC")
	if err != nil {
		log.Printf("Error in ApplyRules (getting rules): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer rows.Close()

	type ruleEntry struct {
		Pattern   string
		MatchType string
		CatID     uuid.UUID
		PayeeID   *uuid.UUID
	}
	rules := []ruleEntry{}
	for rows.Next() {
		var r ruleEntry
		if err := rows.Scan(&r.Pattern, &r.MatchType, &r.CatID, &r.PayeeID); err != nil {
			log.Printf("Error in ApplyRules rules scan: %v\n", err)
			continue
		}
		rules = append(rules, r)
	}

	// Find the "Uncategorized" category ID
	var uncategorizedID uuid.UUID
	err = db.Pool.QueryRow(c, "SELECT id FROM categories WHERE name = 'Uncategorized' LIMIT 1").Scan(&uncategorizedID)
	hasUncategorized := err == nil

	// Get uncategorized transactions (NULL or "Uncategorized" category)
	query := "SELECT id, description FROM transactions WHERE category_id IS NULL"
	args := []interface{}{}
	if hasUncategorized {
		query += " OR category_id = $1"
		args = append(args, uncategorizedID)
	}

	txnRows, err := db.Pool.Query(c, query, args...)
	if err != nil {
		log.Printf("Error in ApplyRules (getting transactions): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer txnRows.Close()

	// Use a transaction for batch updates
	tx, err := db.Pool.Begin(c)
	if err != nil {
		log.Printf("Error in ApplyRules (starting transaction): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
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
					updateQuery += ", payee_id = $2 WHERE id = $3"
					updateArgs = append(updateArgs, r.PayeeID, txnID)
				} else {
					updateQuery += " WHERE id = $2"
					updateArgs = append(updateArgs, txnID)
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": updated})
}

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
