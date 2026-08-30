package handlers

import (
	"errors"
	"fmt"
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
		 WHERE EXISTS (SELECT 1 FROM categories c WHERE c.id = $4 AND (c.user_id = $1 OR c.user_id IS NULL))
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
		   AND EXISTS (SELECT 1 FROM categories c WHERE c.id = $3 AND (c.user_id = $7 OR c.user_id IS NULL))
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
// (category_id IS NULL), applying the first matching rule's category and payee.
// It runs as one set-based UPDATE per rule in descending priority order: the
// category_id IS NULL guard means a transaction is categorized by exactly one
// (the highest-priority matching) rule, and any failure rolls the whole apply
// back so a mid-batch error can never commit a silently partial result.
// Returns the number of transactions updated.
func ApplyRules(c *gin.Context) {
	userID := auth.GetUserID(c)

	// Get all rules
	rules, err := loadRules(c, userID)
	if err != nil {
		log.Printf("Error in ApplyRules (getting rules): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Use a transaction for batch updates
	tx, err := db.Pool.Begin(c)
	if err != nil {
		log.Printf("Error in ApplyRules (starting transaction): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(c)

	updated := 0
	for _, r := range rules {
		// Param layout: $1 user_id, $2 category_id, and the pattern is $3
		// (no payee) or $4 (payee at $3).
		args := []any{userID, r.CatID}
		payeeClause := ""
		paramIdx := 3
		if r.PayeeID != nil {
			payeeClause = ", payee_id = $3"
			args = append(args, r.PayeeID)
			paramIdx = 4
		}
		matchExpr, matchArg, ok := ruleMatchSQL(r.MatchType, r.Pattern, paramIdx)
		if !ok {
			// Match types matchRule does not implement (e.g. the legacy
			// 'regex' value) never fire there either — skip them here rather
			// than failing the whole batch.
			continue
		}
		args = append(args, matchArg)

		res, err := tx.Exec(c,
			fmt.Sprintf(`UPDATE transactions SET category_id = $2%s
			 WHERE user_id = $1 AND category_id IS NULL AND %s`, payeeClause, matchExpr),
			args...,
		)
		if err != nil {
			log.Printf("Error in ApplyRules (updating with rule %q): %v\n", r.Pattern, err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return // deferred rollback keeps the apply all-or-nothing
		}
		updated += int(res.RowsAffected())
	}

	if err := tx.Commit(c); err != nil {
		log.Printf("Error in ApplyRules (committing transaction): %v\n", err)
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
