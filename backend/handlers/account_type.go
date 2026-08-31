package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// builtInAccountTypeIDs are seeded by db.SeedAccountTypes and shared by every
// user; changing their balance semantics or deleting them would corrupt all
// accounts, so they are immutable even for admins.
var builtInAccountTypeIDs = map[string]bool{"bank": true, "credit_card": true, "loan": true}

// accountTypeIDPattern restricts custom type IDs to a safe lowercase slug.
var accountTypeIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,29}$`)

// rejectBuiltInAccountType forbids create/update/delete of seeded IDs.
func rejectBuiltInAccountType(c *gin.Context, id string) bool {
	if builtInAccountTypeIDs[id] {
		validation.RespondError(c, "account type is a built-in and cannot be changed", http.StatusForbidden)
		return true
	}
	return false
}

// GetAccountTypes lists all account types. The list is shared reference data
// (the same types apply to every user) and is safe for any authenticated user
// to read.
func GetAccountTypes(c *gin.Context) {
	rows, err := db.Pool.Query(c, "SELECT id, name, positive_txn_type FROM account_types ORDER BY name")
	if err != nil {
		slog.Error("GetAccountTypes", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	types := []models.AccountType{}
	for rows.Next() {
		var at models.AccountType
		if err := rows.Scan(&at.ID, &at.Name, &at.PositiveTxnType); err != nil {
			slog.Error("GetAccountTypes scan", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		types = append(types, at)
	}

	c.JSON(http.StatusOK, types)
}

// CreateAccountType adds a custom account type (admin only), enforcing the slug
// pattern, valid positiveTxnType, and that built-in IDs are not reused.
func CreateAccountType(c *gin.Context) {
	var req models.CreateAccountTypeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	if rejectBuiltInAccountType(c, req.ID) {
		return
	}
	if !accountTypeIDPattern.MatchString(req.ID) {
		validation.RespondError(c, "invalid id: must start with a letter and use only lowercase letters, digits, and underscores", http.StatusBadRequest)
		return
	}
	if req.PositiveTxnType != "credit" && req.PositiveTxnType != "debit" {
		validation.RespondError(c, "positiveTxnType must be 'credit' or 'debit'", http.StatusBadRequest)
		return
	}

	var at models.AccountType
	err := db.Pool.QueryRow(c,
		`INSERT INTO account_types (id, name, positive_txn_type) VALUES ($1, $2, $3)
		 RETURNING id, name, positive_txn_type`,
		req.ID, req.Name, req.PositiveTxnType,
	).Scan(&at.ID, &at.Name, &at.PositiveTxnType)

	if err != nil {
		slog.Error("CreateAccountType", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusCreated, at)
}

// UpdateAccountType edits a custom account type (admin only). Empty fields keep
// their current value; built-in types are immutable.
func UpdateAccountType(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	if rejectBuiltInAccountType(c, id) {
		return
	}

	var req models.UpdateAccountTypeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	if req.PositiveTxnType != "" && req.PositiveTxnType != "credit" && req.PositiveTxnType != "debit" {
		validation.RespondError(c, "positiveTxnType must be 'credit' or 'debit'", http.StatusBadRequest)
		return
	}

	var at models.AccountType
	err := db.Pool.QueryRow(c,
		`UPDATE account_types SET name = COALESCE(NULLIF($1, ''), name), 
		 positive_txn_type = COALESCE(NULLIF($2, ''), positive_txn_type)
		 WHERE id = $3
		 RETURNING id, name, positive_txn_type`,
		req.Name, req.PositiveTxnType, id,
	).Scan(&at.ID, &at.Name, &at.PositiveTxnType)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			validation.RespondError(c, "account type not found", http.StatusNotFound)
			return
		}
		slog.Error("UpdateAccountType", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, at)
}

// DeleteAccountType removes a custom account type (admin only) and refuses to
// delete built-in types or types still referenced by existing accounts.
func DeleteAccountType(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	if rejectBuiltInAccountType(c, id) {
		return
	}

	// Check if any accounts are using this type
	var count int
	if err := db.Pool.QueryRow(c, "SELECT COUNT(*) FROM accounts WHERE account_type_id = $1", id).Scan(&count); err != nil {
		slog.Error("DeleteAccountType (usage count)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if count > 0 {
		validation.RespondError(c, "cannot delete: account type is in use by existing accounts", http.StatusConflict)
		return
	}

	result, err := db.Pool.Exec(c, "DELETE FROM account_types WHERE id = $1", id)
	if err != nil {
		slog.Error("DeleteAccountType", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if result.RowsAffected() == 0 {
		validation.RespondError(c, "account type not found", http.StatusNotFound)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}
