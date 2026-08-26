package handlers

import (
	"errors"
	"log"
	"net/http"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// GetCategories lists every category visible to the user: their own categories
// plus the global (admin-created) ones, in group order (base groups first, then
// custom groups) and alphabetically by name within each group.
func GetCategories(c *gin.Context) {
	rows, err := db.Pool.Query(c, `SELECT c.id, c.name, c.icon, c.color, c.group_id,
		       (c.user_id IS NULL) as is_global, g.name, g.is_base
		 FROM categories c
		 JOIN category_groups g ON c.group_id = g.id
		 WHERE c.user_id = $1 OR c.user_id IS NULL
		 ORDER BY g.sort_order, c.name`, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error in GetCategories: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	categories := []models.Category{}
	for rows.Next() {
		var cat models.Category
		if err := rows.Scan(&cat.ID, &cat.Name, &cat.Icon, &cat.Color, &cat.GroupID, &cat.IsGlobal, &cat.GroupName, &cat.GroupIsBase); err != nil {
			log.Printf("Error in GetCategories scan: %v\n", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		categories = append(categories, cat)
	}

	c.JSON(http.StatusOK, categories)
}

// CreateCategory inserts a user-scoped category, validating that the target
// group is usable by the user (a base/global group or one of their own custom
// groups).
func CreateCategory(c *gin.Context) {
	var req models.CreateCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	var cat models.Category
	err := db.Pool.QueryRow(c,
		`INSERT INTO categories (user_id, name, icon, color, group_id)
		 SELECT $1, $2, $3, $4, $5
		 WHERE EXISTS (SELECT 1 FROM category_groups g WHERE g.id = $5 AND (g.user_id IS NULL OR g.user_id = $1))
		 RETURNING id, name, icon, color, group_id`,
		auth.GetUserID(c), req.Name, req.Icon, req.Color, req.GroupID,
	).Scan(&cat.ID, &cat.Name, &cat.Icon, &cat.Color, &cat.GroupID)

	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "referenced group not found", http.StatusBadRequest)
		return
	}
	if err != nil {
		log.Printf("Error in CreateCategory: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusCreated, cat)
}

// UpdateCategory edits a user's own category. Global categories are immutable
// through this endpoint; an admin-only path handles those.
func UpdateCategory(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	var req models.UpdateCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	userID := auth.GetUserID(c)

	// Validate the new group (when provided) is usable by the user.
	if req.GroupID != "" {
		var ok bool
		err := db.Pool.QueryRow(c,
			`SELECT EXISTS (SELECT 1 FROM category_groups g WHERE g.id = $1 AND (g.user_id IS NULL OR g.user_id = $2))`,
			req.GroupID, userID,
		).Scan(&ok)
		if err != nil {
			log.Printf("Error in UpdateCategory (group check): %v\n", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		if !ok {
			validation.RespondError(c, "referenced group not found", http.StatusBadRequest)
			return
		}
	}

	var cat models.Category
	err = db.Pool.QueryRow(c,
		`UPDATE categories
		 SET name = COALESCE(NULLIF($2, ''), name),
		     icon = COALESCE(NULLIF($3, ''), icon),
		     color = COALESCE(NULLIF($4, ''), color),
		     group_id = COALESCE(NULLIF($5, ''), group_id)
		 WHERE id = $1 AND user_id = $6
		 RETURNING id, name, icon, color, group_id`,
		id, req.Name, req.Icon, req.Color, req.GroupID, userID,
	).Scan(&cat.ID, &cat.Name, &cat.Icon, &cat.Color, &cat.GroupID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			validation.RespondError(c, "category not found", http.StatusNotFound)
			return
		}
		log.Printf("Error in UpdateCategory: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, cat)
}

// DeleteCategory removes a user's own category. In the same transaction it
// clears the category on every transaction that referenced it and deletes any
// auto-categorization rules pointing at it, so nothing dangles. The response
// reports how many transactions/rules were affected so the UI can confirm the
// uncategorization warning.
func DeleteCategory(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)
	result := models.DeleteCategoryResult{}

	err = db.WithTx(c, func(tx pgx.Tx) error {
		cleared, err := tx.Exec(c, `UPDATE transactions SET category_id = NULL WHERE category_id = $1 AND user_id = $2`, id, userID)
		if err != nil {
			return err
		}
		result.ClearedTransactions = int(cleared.RowsAffected())

		rules, err := tx.Exec(c, `DELETE FROM rules WHERE category_id = $1 AND user_id = $2`, id, userID)
		if err != nil {
			return err
		}
		result.DeletedRules = int(rules.RowsAffected())

		deleted, err := tx.Exec(c, `DELETE FROM categories WHERE id = $1 AND user_id = $2`, id, userID)
		if err != nil {
			return err
		}
		if deleted.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			validation.RespondError(c, "category not found", http.StatusNotFound)
			return
		}
		log.Printf("Error in DeleteCategory: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, result)
}

// DeleteGlobalCategory removes an admin-created global category, clearing the
// category across every user's transactions and removing referencing rules.
func DeleteGlobalCategory(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	result := models.DeleteCategoryResult{}

	err = db.WithTx(c, func(tx pgx.Tx) error {
		cleared, err := tx.Exec(c, `UPDATE transactions SET category_id = NULL WHERE category_id = $1`, id)
		if err != nil {
			return err
		}
		result.ClearedTransactions = int(cleared.RowsAffected())

		rules, err := tx.Exec(c, `DELETE FROM rules WHERE category_id = $1`, id)
		if err != nil {
			return err
		}
		result.DeletedRules = int(rules.RowsAffected())

		deleted, err := tx.Exec(c, `DELETE FROM categories WHERE id = $1 AND user_id IS NULL`, id)
		if err != nil {
			return err
		}
		if deleted.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			validation.RespondError(c, "category not found", http.StatusNotFound)
			return
		}
		log.Printf("Error in DeleteGlobalCategory: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, result)
}

// CreateGlobalCategory creates an admin-owned, global category visible to every
// user. The target group must itself be a global group (base or admin-created).
func CreateGlobalCategory(c *gin.Context) {
	var req models.CreateCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	var cat models.Category
	err := db.Pool.QueryRow(c,
		`INSERT INTO categories (user_id, name, icon, color, group_id)
		 SELECT NULL, $1, $2, $3, $4
		 WHERE EXISTS (SELECT 1 FROM category_groups g WHERE g.id = $4 AND g.user_id IS NULL)
		 RETURNING id, name, icon, color, group_id`,
		req.Name, req.Icon, req.Color, req.GroupID,
	).Scan(&cat.ID, &cat.Name, &cat.Icon, &cat.Color, &cat.GroupID)

	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "referenced global group not found", http.StatusBadRequest)
		return
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			validation.RespondError(c, "a global category with this id already exists", http.StatusConflict)
			return
		}
		log.Printf("Error in CreateGlobalCategory: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusCreated, cat)
}

// UpdateGlobalCategory edits an admin-created global category.
func UpdateGlobalCategory(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	var req models.UpdateCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	if req.GroupID != "" {
		var ok bool
		err := db.Pool.QueryRow(c,
			`SELECT EXISTS (SELECT 1 FROM category_groups g WHERE g.id = $1 AND g.user_id IS NULL)`,
			req.GroupID,
		).Scan(&ok)
		if err != nil {
			log.Printf("Error in UpdateGlobalCategory (group check): %v\n", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		if !ok {
			validation.RespondError(c, "referenced global group not found", http.StatusBadRequest)
			return
		}
	}

	var cat models.Category
	err = db.Pool.QueryRow(c,
		`UPDATE categories
		 SET name = COALESCE(NULLIF($2, ''), name),
		     icon = COALESCE(NULLIF($3, ''), icon),
		     color = COALESCE(NULLIF($4, ''), color),
		     group_id = COALESCE(NULLIF($5, ''), group_id)
		 WHERE id = $1 AND user_id IS NULL
		 RETURNING id, name, icon, color, group_id`,
		id, req.Name, req.Icon, req.Color, req.GroupID,
	).Scan(&cat.ID, &cat.Name, &cat.Icon, &cat.Color, &cat.GroupID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			validation.RespondError(c, "global category not found", http.StatusNotFound)
			return
		}
		log.Printf("Error in UpdateGlobalCategory: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, cat)
}