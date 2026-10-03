package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Account types: the small vocabulary that decides what a transaction *means*
// for a given account, and therefore how that account's balance is computed.
//
// Unlike categories and groups, account types are global and NOT owned by a
// user — there is no user_id column at all. Read every query in this file
// knowing that: nothing here is scoped, because nothing here can be. That is
// also why they are the one reference table whose delete is destructive rather
// than per-user: an account still pointing at a retired type would have no
// defined balance, so the guarded delete below refuses while any account uses it.
//
// The load-bearing field is `positive_txn_type`, and it is a convention rather
// than a rule: 'credit' or 'debit' saying which transaction type adds to the
// running balance for an account of this type. `bank` and `credit_card` are
// both 'credit' (money in is money in), while `loan` is 'debit' because a loan
// account's balance is the total repaid — see db.SeedAccountTypes, where that is
// spelled out, and account.go, which is where the expression is actually applied.
//
// Adding a type is therefore a way to change balance semantics for every
// account of that type, which is why the three seeded ids are immutable even for
// an admin.

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
func (srv *Server) GetAccountTypes(c *gin.Context) {
	rows, err := srv.db.Query(c, "SELECT id, name, positive_txn_type FROM account_types ORDER BY name")
	if err != nil {
		slog.Error("GetAccountTypes", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	types := []models.AccountType{}
	for rows.Next() {
		var at models.AccountType
		if err := rows.Scan(&at.ID, &at.Name, &at.PositiveTxnType); err != nil {
			slog.Error("GetAccountTypes scan", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		types = append(types, at)
	}
	if err := rows.Err(); err != nil {
		slog.Error("GetAccountTypes rows", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, types)
}

// CreateAccountType adds a custom account type (admin only), enforcing the slug
// pattern, valid positiveTxnType, and that built-in IDs are not reused.
func (srv *Server) CreateAccountType(c *gin.Context) {
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
	err := srv.db.QueryRow(c,
		`INSERT INTO account_types (id, name, positive_txn_type) VALUES ($1, $2, $3)
		 RETURNING id, name, positive_txn_type`,
		req.ID, req.Name, req.PositiveTxnType,
	).Scan(&at.ID, &at.Name, &at.PositiveTxnType)

	if err != nil {
		// account_types.id is the primary key and req.ID is user-chosen, so
		// re-using an existing id is a client mistake, not a server fault. The
		// sibling catalog creates (CreateGroup, CreateGlobalCategory) answer 409
		// for the same class, and the admin console can only explain a 409.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			validation.RespondError(c, "an account type with this id already exists", http.StatusConflict)
			return
		}
		slog.Error("CreateAccountType", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusCreated, at)
}

// UpdateAccountType edits a custom account type (admin only). Empty fields keep
// their current value; built-in types are immutable.
//
// `putPartial`, so positiveTxnType is only re-validated when non-empty and the
// COALESCE(NULLIF(...)) leaves the stored value alone otherwise. The asymmetry
// with the validation just above is deliberate: an empty string is a valid
// "leave alone" signal, so it must not be rejected as a bad value before the
// statement gets the chance to ignore it.
//
// Flipping positiveTxnType silently re-bases every account of that type: the
// balance expression in account.go reads this column, so the same transactions
// now sum the other way. Nothing here re-derives or warns — the row-level
// effect is the whole reason the three built-ins are frozen.
func (srv *Server) UpdateAccountType(c *gin.Context) {
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
	err := srv.db.QueryRow(c,
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
		slog.Error("UpdateAccountType", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, at)
}

// DeleteAccountType removes a custom account type (admin only) and refuses to
// delete built-in types or types still referenced by existing accounts.
func (srv *Server) DeleteAccountType(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	if rejectBuiltInAccountType(c, id) {
		return
	}

	// Check if any accounts are using this type. Global, not per-user: the
	// count spans every account on the instance, because the type is global and
	// deleting it would leave another user's account without a balance rule.
	// The race between this COUNT and the DELETE is why the schema's FK matters
	// as a backstop — a concurrent account created in the gap is caught there
	// rather than dangling.
	var count int
	if err := srv.db.QueryRow(c, "SELECT COUNT(*) FROM accounts WHERE account_type_id = $1", id).Scan(&count); err != nil {
		slog.Error("DeleteAccountType (usage count)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if count > 0 {
		validation.RespondError(c, "cannot delete: account type is in use by existing accounts", http.StatusConflict)
		return
	}

	result, err := srv.db.Exec(c, "DELETE FROM account_types WHERE id = $1", id)
	if err != nil {
		slog.Error("DeleteAccountType", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if result.RowsAffected() == 0 {
		validation.RespondError(c, "account type not found", http.StatusNotFound)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}
