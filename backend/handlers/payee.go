package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// GetPayees lists the user's payees alphabetically by name.
func (srv *Server) GetPayees(c *gin.Context) {
	rows, err := srv.db.Query(c, "SELECT id, name, account_id, created_at, updated_at FROM payees WHERE user_id = $1 ORDER BY name", auth.GetUserID(c))
	if err != nil {
		slog.Error("GetPayees", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	payees := []models.Payee{}
	for rows.Next() {
		var p models.Payee
		if err := rows.Scan(&p.ID, &p.Name, &p.AccountID, &p.CreatedAt, &p.UpdatedAt); err != nil {
			slog.Error("GetPayees scan", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		payees = append(payees, p)
	}
	if err := rows.Err(); err != nil {
		slog.Error("GetPayees rows", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, payees)
}

// CreatePayee inserts a payee, rejecting references to an account the user
// doesn't own and conflicting with an existing name (23505).
func (srv *Server) CreatePayee(c *gin.Context) {
	var req models.CreatePayeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	var p models.Payee
	err := srv.db.QueryRow(c,
		`INSERT INTO payees (user_id, name, account_id)
		 SELECT $1, $2, $3
		 WHERE ($3::uuid IS NULL OR EXISTS (SELECT 1 FROM accounts a WHERE a.id = $3 AND a.user_id = $1))
		 RETURNING id, name, account_id, created_at, updated_at`,
		auth.GetUserID(c), req.Name, req.AccountID,
	).Scan(&p.ID, &p.Name, &p.AccountID, &p.CreatedAt, &p.UpdatedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			validation.RespondError(c, "referenced account not found", http.StatusBadRequest)
			return
		}
		if respondPayeeUniqueViolation(c, err) {
			return
		}
		slog.Error("CreatePayee", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusCreated, p)
}

// payees carries two unique indexes and a 23505 from either of them must not be
// reported as the other: payees_user_name_uq (user_id, name) and
// payees_account_id_tenant_uq (user_id, account_id). Every account created
// through CreateAccount gets a linked payee row, so linking a payee to an
// account that already has one violates the *account* index — answering "a
// payee with this name already exists" there is untrue (renaming clears
// nothing) and leaves the caller with no way forward. respondPayeeUniqueViolation
// writes the response and reports whether it handled the error.
func respondPayeeUniqueViolation(c *gin.Context, err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	if pgErr.ConstraintName == "payees_account_id_tenant_uq" {
		validation.RespondError(c, "this account already has a linked payee", http.StatusConflict)
		return true
	}
	validation.RespondError(c, "a payee with this name already exists", http.StatusConflict)
	return true
}

// UpdatePayee renames a payee and/or re-links it to an account, enforcing
// ownership of both the payee and any referenced account.
func (srv *Server) UpdatePayee(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	var req models.CreatePayeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	var p models.Payee
	err = srv.db.QueryRow(c,
		`UPDATE payees SET name = $1, account_id = $2, updated_at = NOW()
		 WHERE id = $3 AND user_id = $4
		   AND ($2::uuid IS NULL OR EXISTS (SELECT 1 FROM accounts a WHERE a.id = $2 AND a.user_id = $4))
		 RETURNING id, name, account_id, created_at, updated_at`,
		req.Name, req.AccountID, id, auth.GetUserID(c),
	).Scan(&p.ID, &p.Name, &p.AccountID, &p.CreatedAt, &p.UpdatedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			validation.RespondError(c, "payee not found", http.StatusNotFound)
			return
		}
		if respondPayeeUniqueViolation(c, err) {
			return
		}
		slog.Error("UpdatePayee", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, p)
}

// DeletePayee removes a payee owned by the user.
func (srv *Server) DeletePayee(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)
	result, err := srv.db.Exec(c, "DELETE FROM payees WHERE id = $1 AND user_id = $2", id, userID)
	if err != nil {
		slog.Error("DeletePayee", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if result.RowsAffected() == 0 {
		validation.RespondError(c, "payee not found", http.StatusNotFound)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}
