package handlers

import (
	"encoding/csv"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// errAccountNotFound is returned by account helpers when a delete/update
// targets an account that doesn't exist (or isn't owned by the user), so the
// caller can translate it to a 404.
var errAccountNotFound = errors.New("account not found")

// GetAccounts lists the authenticated user's accounts, newest first, each with
// a computed running balance based on its account type's positive_txn_type.
func GetAccounts(c *gin.Context) {
	userID := auth.GetUserID(c)
	query := `
		SELECT a.id, a.name, a.account_type_id, at.name as account_type_name, a.bank, a.currency, a.color, a.is_default, a.billing_day, a.created_at,
		COALESCE((
			SELECT SUM(CASE
				WHEN at.positive_txn_type = 'credit' THEN (CASE WHEN t.type = 'credit' THEN t.amount ELSE -t.amount END)
				WHEN at.positive_txn_type = 'debit' THEN (CASE WHEN t.type = 'debit' THEN t.amount ELSE -t.amount END)
				ELSE 0 END)
			FROM transactions t WHERE t.account_id = a.id AND t.user_id = $1
		), 0) as balance
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		WHERE a.user_id = $2
		ORDER BY a.created_at DESC`

	rows, err := db.Pool.Query(c, query, userID, userID)
	if err != nil {
		slog.Error("GetAccounts", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	accounts := []models.Account{}
	for rows.Next() {
		var a models.Account
		if err := rows.Scan(&a.ID, &a.Name, &a.AccountTypeID, &a.AccountTypeName, &a.Bank, &a.Currency, &a.Color, &a.IsDefault, &a.BillingDay, &a.CreatedAt, &a.Balance); err != nil {
			slog.Error("GetAccounts scan", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		accounts = append(accounts, a)
	}

	c.JSON(http.StatusOK, accounts)
}

// CreateAccount inserts a new account for the user, clears the default flag on
// any other account when the new one is the default, and keeps the payee table
// in sync by creating/updating a payee named after the account.
func CreateAccount(c *gin.Context) {
	var req models.CreateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	if req.Currency == "" {
		req.Currency = "INR"
	}
	if req.Color == "" {
		req.Color = "#06b6d4"
	}
	// Billing day is optional; when omitted it is stored as NULL (no billing
	// cycles, no summary rows).

	var account models.Account
	userID := auth.GetUserID(c)
	err := db.WithTx(c, func(tx pgx.Tx) error {
		// If this account is created as the default, clear the flag on any other
		// accounts belonging to the user so there is only one default.
		if req.IsDefault {
			if _, err := tx.Exec(c, "UPDATE accounts SET is_default = FALSE WHERE user_id = $1", userID); err != nil {
				return err
			}
		}

		err := tx.QueryRow(c,
			`INSERT INTO accounts (user_id, name, account_type_id, bank, currency, color, is_default, billing_day) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 RETURNING id, name, account_type_id, bank, currency, color, is_default, billing_day, created_at`,
			userID, req.Name, req.AccountTypeID, req.Bank, req.Currency, req.Color, req.IsDefault, req.BillingDay,
		).Scan(&account.ID, &account.Name, &account.AccountTypeID, &account.Bank, &account.Currency, &account.Color, &account.IsDefault, &account.BillingDay, &account.CreatedAt)

		if err != nil {
			return err
		}

		// Fetch the account type name for the response
		if err := tx.QueryRow(c, "SELECT name FROM account_types WHERE id = $1", account.AccountTypeID).Scan(&account.AccountTypeName); err != nil {
			return fmt.Errorf("failed to load account type name: %w", err)
		}

		// Synchronize with Payees: Create a payee for the new account
		_, err = tx.Exec(c,
			`INSERT INTO payees (user_id, name, account_id) VALUES ($1, $2, $3)
			 ON CONFLICT (account_id) WHERE account_id IS NOT NULL DO UPDATE SET name = EXCLUDED.name`,
			userID, account.Name, account.ID,
		)
		if err != nil {
			return fmt.Errorf("failed to create linked payee: %w", err)
		}

		return nil
	})

	if err != nil {
		// The account-linked payee upsert can collide with an existing payee
		// name owned by this user (payees_user_name_uq after migration 000002)
		// — surface that as a conflict, not a 500.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			validation.RespondError(c, "an account-linked payee with this name already exists. Choose a different account name or delete the existing payee.", http.StatusConflict)
			return
		}
		slog.Error("CreateAccount", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	account.Balance = 0 // New account has 0 balance
	c.JSON(http.StatusCreated, account)
}

// DeleteAccount removes an account owned by the user along with its
// account-linked payee, so no orphaned payees are left behind. Its
// transactions are deleted first (migration 000003 also cascades them — and
// their links — from the account row); the count is returned so the UI can
// tell the user what was removed. Previously the transactions were orphaned:
// invisible in listings (inner JOIN) yet still counted by dashboard totals.
func DeleteAccount(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)
	var transactionsDeleted int64
	err = db.WithTx(c, func(tx pgx.Tx) error {
		// Delete the account's transactions (user-scoped) so the response can
		// report the count; the FK cascade is schema-level insurance.
		res, err := tx.Exec(c, "DELETE FROM transactions WHERE account_id = $1 AND user_id = $2", id, userID)
		if err != nil {
			return err
		}
		transactionsDeleted = res.RowsAffected()

		// Remove the account-linked payee to avoid orphaned payees
		if _, err := tx.Exec(c, "DELETE FROM payees WHERE account_id = $1 AND user_id = $2", id, userID); err != nil {
			return err
		}
		result, err := tx.Exec(c, "DELETE FROM accounts WHERE id = $1 AND user_id = $2", id, userID)
		if err != nil {
			return err
		}
		if result.RowsAffected() == 0 {
			return errAccountNotFound
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errAccountNotFound) {
			validation.RespondError(c, "account not found", http.StatusNotFound)
			return
		}
		slog.Error("DeleteAccount", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted", "transactionsDeleted": transactionsDeleted})
}

// UpdateAccount edits an account's fields, preserves the current default flag
// when the request omits it (via *bool), keeps only one default account, and
// renames the account-linked payee to match.
func UpdateAccount(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	var req models.UpdateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	// Range-check an explicit billing day before touching the database; the
	// OptionalInt field replaces the old binding tag, which couldn't be used
	// on the absent/null-sensitive type.
	if req.BillingDay.Set() {
		if v := req.BillingDay.Value(); v != nil && (*v < 1 || *v > 31) {
			validation.RespondError(c, "billingDay must be between 1 and 31", http.StatusBadRequest)
			return
		}
	}

	var account models.Account
	userID := auth.GetUserID(c)
	err = db.WithTx(c, func(tx pgx.Tx) error {
		// If this account is being set as the default, clear the flag on any
		// other accounts belonging to the user so there is only one default.
		if req.IsDefault != nil && *req.IsDefault {
			if _, err := tx.Exec(c, "UPDATE accounts SET is_default = FALSE WHERE user_id = $1 AND id <> $2", userID, id); err != nil {
				return err
			}
		}

		// billing_day is only written when the request mentions it: an absent
		// key must leave the current value (and its derived billing cycles)
		// untouched, an explicit number sets it, and null clears it. The id and
		// user_id params stay at $7/$8 so the outer SELECT references hold; an
		// explicit billing day is appended as $9.
		setClauses := "name = $1, account_type_id = $2, bank = $3, currency = $4, color = $5, is_default = COALESCE($6, is_default)"
		args := []interface{}{req.Name, req.AccountTypeID, req.Bank, req.Currency, req.Color, req.IsDefault, id, userID}
		billingClause := ""
		if req.BillingDay.Set() {
			billingClause = ", billing_day = $9"
			args = append(args, req.BillingDay.Value())
		}

		err := tx.QueryRow(c,
			fmt.Sprintf(`WITH updated AS (
				UPDATE accounts SET %s%s, updated_at = NOW() 
				WHERE id = $7 AND user_id = $8 RETURNING id, name, account_type_id, bank, currency, color, is_default, billing_day, created_at
			)
			SELECT u.id, u.name, u.account_type_id, at.name as account_type_name, u.bank, u.currency, u.color, u.is_default, u.billing_day, u.created_at,
			COALESCE((
				SELECT SUM(CASE
					WHEN at.positive_txn_type = 'credit' THEN (CASE WHEN t.type = 'credit' THEN t.amount ELSE -t.amount END)
					WHEN at.positive_txn_type = 'debit' THEN (CASE WHEN t.type = 'debit' THEN t.amount ELSE -t.amount END)
					ELSE 0 END)
				FROM transactions t WHERE t.account_id = u.id AND t.user_id = $8
			), 0) as balance
			FROM updated u
			JOIN account_types at ON u.account_type_id = at.id`, setClauses, billingClause),
			args...,
		).Scan(&account.ID, &account.Name, &account.AccountTypeID, &account.AccountTypeName, &account.Bank, &account.Currency, &account.Color, &account.IsDefault, &account.BillingDay, &account.CreatedAt, &account.Balance)

		if err != nil {
			return err
		}

		// Synchronize with Payees: Update the corresponding payee name
		_, err = tx.Exec(c,
			`UPDATE payees SET name = $1 WHERE account_id = $2 AND user_id = $3`,
			account.Name, account.ID, userID,
		)
		if err != nil {
			return fmt.Errorf("failed to update linked payee: %w", err)
		}

		return nil
	})

	if err != nil {
		if err == pgx.ErrNoRows {
			validation.RespondError(c, "account not found", http.StatusNotFound)
			return
		}
		// Renaming the account-linked payee can collide with another payee of
		// the same name owned by this user (payees_user_name_uq) — surface it
		// as a conflict instead of a generic 500.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			validation.RespondError(c, "an account-linked payee with this name already exists. Rename the conflicting payee first.", http.StatusConflict)
			return
		}
		slog.Error("UpdateAccount", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, account)
}

// ExportAccount streams all transactions for an account as a CSV attachment,
// scoped to the authenticated user via an ownership EXISTS check.
func ExportAccount(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	rows, err := db.Pool.Query(c,
		`SELECT t.date, t.description, t.amount, t.type, t.tags, t.notes
		 FROM transactions t
		 WHERE t.account_id = $1
		 	 AND t.user_id = $2
		 ORDER BY t.date DESC`, id, auth.GetUserID(c))
	if err != nil {
		slog.Error("ExportAccount", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	c.Writer.Header().Set("Content-Type", "text/csv")
	c.Writer.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="account_%s_export.csv"`, id.String()[:8]))

	writer := csv.NewWriter(c.Writer)
	defer writer.Flush()

	// Write CSV Header
	if err := writer.Write([]string{"Date", "Description", "Amount", "Type", "Tags", "Notes"}); err != nil {
		slog.Error("writing CSV header", "error", err)
		return
	}

	for rows.Next() {
		var (
			date        time.Time
			description string
			amount      float64
			txnType     string
			tags        []string
			notes       string
		)
		if err := rows.Scan(&date, &description, &amount, &txnType, &tags, &notes); err != nil {
			slog.Error("scanning row in ExportAccount", "error", err)
			continue
		}

		record := []string{
			date.Format("2006-01-02"),
			description,
			fmt.Sprintf("%.2f", amount),
			txnType,
			strings.Join(tags, ";"),
			notes,
		}

		if err := writer.Write(record); err != nil {
			slog.Error("writing CSV record", "error", err)
			return
		}
	}
}
