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
	"github.com/jackc/pgx/v5/pgconn"
)

// GetGroups lists the groups visible to the user: the immutable base/global
// groups first (in canonical order), then the user's own custom groups.
func GetGroups(c *gin.Context) {
	rows, err := db.Pool.Query(c, `SELECT id, name, icon, color, is_base, user_id, sort_order
		 FROM category_groups
		 WHERE user_id IS NULL OR user_id = $1
		 ORDER BY CASE WHEN user_id IS NULL THEN 0 ELSE 1 END, sort_order, name`, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error in GetGroups: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	groups := []models.CategoryGroup{}
	for rows.Next() {
		var g models.CategoryGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Icon, &g.Color, &g.IsBase, &g.UserID, &g.SortOrder); err != nil {
			log.Printf("Error in GetGroups scan: %v\n", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		g.IsGlobal = g.UserID == nil
		groups = append(groups, g)
	}

	c.JSON(http.StatusOK, groups)
}

// CreateGroup adds a user-owned custom group. Base/global groups are never
// created through this endpoint.
func CreateGroup(c *gin.Context) {
	var req models.CreateCategoryGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	userID := auth.GetUserID(c)

	var g models.CategoryGroup
	err := db.Pool.QueryRow(c,
		`INSERT INTO category_groups (id, name, icon, color, is_base, user_id, sort_order)
		 VALUES ($1, $2, $3, $4, FALSE, $5,
		         (SELECT COALESCE(MAX(sort_order), 0) + 1 FROM category_groups WHERE user_id = $5))
		 RETURNING id, name, icon, color, is_base, user_id, sort_order`,
		req.ID, req.Name, req.Icon, req.Color, userID,
	).Scan(&g.ID, &g.Name, &g.Icon, &g.Color, &g.IsBase, &g.UserID, &g.SortOrder)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			validation.RespondError(c, "a group with this id already exists", http.StatusConflict)
			return
		}
		log.Printf("Error in CreateGroup: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	g.IsGlobal = false
	c.JSON(http.StatusCreated, g)
}

// UpdateGroup renames / restyles a user's own custom group. Base and global
// groups are immutable.
func UpdateGroup(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	var req models.UpdateCategoryGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	userID := auth.GetUserID(c)

	var g models.CategoryGroup
	err := db.Pool.QueryRow(c,
		`UPDATE category_groups
		 SET name = COALESCE(NULLIF($2, ''), name),
		     icon = COALESCE(NULLIF($3, ''), icon),
		     color = COALESCE(NULLIF($4, ''), color)
		 WHERE id = $1 AND user_id = $5 AND is_base = FALSE
		 RETURNING id, name, icon, color, is_base, user_id, sort_order`,
		id, req.Name, req.Icon, req.Color, userID,
	).Scan(&g.ID, &g.Name, &g.Icon, &g.Color, &g.IsBase, &g.UserID, &g.SortOrder)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Distinguish "you can't touch this group" from "doesn't exist".
			var exists bool
			checkErr := db.Pool.QueryRow(c,
				`SELECT EXISTS (SELECT 1 FROM category_groups WHERE id = $1 AND user_id IS NULL)`,
				id,
			).Scan(&exists)
			if checkErr == nil && exists {
				validation.RespondError(c, "base and global groups are immutable", http.StatusBadRequest)
				return
			}
			validation.RespondError(c, "group not found", http.StatusNotFound)
			return
		}
		log.Printf("Error in UpdateGroup: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	g.IsGlobal = false
	c.JSON(http.StatusOK, g)
}

// DeleteGroup removes a user's own custom group. A group that still has
// categories cannot be deleted — the user must move or delete them first.
func DeleteGroup(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)

	var count int
	err := db.Pool.QueryRow(c,
		`SELECT COUNT(*) FROM categories WHERE group_id = $1 AND user_id = $2`, id, userID,
	).Scan(&count)
	if err != nil {
		log.Printf("Error in DeleteGroup (count categories): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if count > 0 {
		validation.RespondError(c, "cannot delete a group that still has categories", http.StatusBadRequest)
		return
	}

	result, err := db.Pool.Exec(c,
		`DELETE FROM category_groups WHERE id = $1 AND user_id = $2 AND is_base = FALSE`, id, userID)
	if err != nil {
		log.Printf("Error in DeleteGroup: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if result.RowsAffected() == 0 {
		validation.RespondError(c, "group not found", http.StatusNotFound)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// CreateGlobalGroup creates an admin-owned, non-base global group. This lets an
// admin add groups that are visible to every user (base groups themselves are
// seeded and immutable).
func CreateGlobalGroup(c *gin.Context) {
	var req models.CreateCategoryGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	var g models.CategoryGroup
	err := db.Pool.QueryRow(c,
		`INSERT INTO category_groups (id, name, icon, color, is_base, user_id, sort_order)
		 VALUES ($1, $2, $3, $4, FALSE, NULL,
		         (SELECT COALESCE(MAX(sort_order), 0) + 1 FROM category_groups))
		 RETURNING id, name, icon, color, is_base, user_id, sort_order`,
		req.ID, req.Name, req.Icon, req.Color,
	).Scan(&g.ID, &g.Name, &g.Icon, &g.Color, &g.IsBase, &g.UserID, &g.SortOrder)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			validation.RespondError(c, "a group with this id already exists", http.StatusConflict)
			return
		}
		log.Printf("Error in CreateGlobalGroup: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	g.IsGlobal = true
	c.JSON(http.StatusCreated, g)
}