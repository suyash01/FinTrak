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
	"github.com/jackc/pgx/v5"
)

// GetCategories lists the user's categories in a canonical grouped order
// (expense, income, transfer — matching the frontend's sectioned filter) and
// alphabetically by name within each group.
func GetCategories(c *gin.Context) {
	rows, err := db.Pool.Query(c, `SELECT id, name, icon, color, parent_id, type FROM categories
		 WHERE user_id = $1
		 ORDER BY CASE type WHEN 'expense' THEN 1 WHEN 'income' THEN 2 ELSE 3 END, name`, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error in GetCategories: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	categories := []models.Category{}
	for rows.Next() {
		var cat models.Category
		if err := rows.Scan(&cat.ID, &cat.Name, &cat.Icon, &cat.Color, &cat.ParentID, &cat.Type); err != nil {
			log.Printf("Error in GetCategories scan: %v\n", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		categories = append(categories, cat)
	}

	c.JSON(http.StatusOK, categories)
}

// CreateCategory inserts a user-scoped category, rejecting a ParentID that
// references a category the user doesn't own.
func CreateCategory(c *gin.Context) {
	var cat models.Category
	if err := c.ShouldBindJSON(&cat); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	err := db.Pool.QueryRow(c,
		`INSERT INTO categories (user_id, name, icon, color, parent_id, type)
		 SELECT $1, $2, $3, $4, $5, $6
		 WHERE $5 IS NULL OR EXISTS (SELECT 1 FROM categories p WHERE p.id = $5 AND p.user_id = $1)
		 RETURNING id, name, icon, color, parent_id, type`,
		auth.GetUserID(c), cat.Name, cat.Icon, cat.Color, cat.ParentID, cat.Type,
	).Scan(&cat.ID, &cat.Name, &cat.Icon, &cat.Color, &cat.ParentID, &cat.Type)

	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "referenced parent category not found", http.StatusBadRequest)
		return
	}
	if err != nil {
		log.Printf("Error in CreateCategory: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusCreated, cat)
}
