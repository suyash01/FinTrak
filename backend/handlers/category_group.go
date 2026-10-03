package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Category groups — the level above a category. Groups and categories share the
// same two-tenant model (see the header of category.go): `user_id IS NULL` is a
// global row, and that NULL is what every predicate here keys off.
//
// The four base groups (income, expense, transfer, cashback) are inserted
// globally on every boot by db.SeedCategoryGroups, marked `is_base` so they can
// never be deleted, and given a fixed `sort_order`. That sort_order is the whole
// reason the list below orders by it: the user's own groups have no canonical
// position, so `is_base` groups lead in their seeded order and custom groups
// follow by their own.
//
// Note the asymmetry with categories: a group is addressed by a *slug* the user
// types (validated by groupIDSlugRe below) rather than by a uuid, because a group
// is shared vocabulary — "Expenses" is the same group for every user — while a
// user's personal category is their own row.

// GetGroups lists the groups visible to the user: the immutable base/global
// groups first (in canonical order), then the user's own custom groups.
func (srv *Server) GetGroups(c *gin.Context) {
	// The CASE in the ORDER BY is what puts global groups ahead of the user's
	// own regardless of their sort_order values, which are independent columns
	// from the base groups' seeded 1-4. Ordering by sort_order alone would
	// interleave them, and a user's group with sort_order 1 would precede Income.
	rows, err := srv.db.Query(c, `SELECT id, name, icon, color, is_base, user_id, sort_order
		 FROM category_groups
		 WHERE user_id IS NULL OR user_id = $1
		 ORDER BY CASE WHEN user_id IS NULL THEN 0 ELSE 1 END, sort_order, name`, auth.GetUserID(c))
	if err != nil {
		slog.Error("GetGroups", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	groups := []models.CategoryGroup{}
	for rows.Next() {
		var g models.CategoryGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Icon, &g.Color, &g.IsBase, &g.UserID, &g.SortOrder); err != nil {
			slog.Error("GetGroups scan", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		// IsGlobal is a derived response field, not a column: like a category's
		// is_global, it tells the client which rows its own routes may write.
		g.IsGlobal = g.UserID == nil
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		slog.Error("GetGroups rows", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, groups)
}

// groupIDSlugRe validates user-chosen category group ids: a lowercase letter,
// then lowercase letters/digits/underscores, up to 50 characters (the column
// is VARCHAR(50)). Mirrors the account-type id convention so every stored id
// is addressable via PUT/DELETE /groups/:id — a '/' or '?' in the id would
// otherwise create unroutable rows, and an overlong id would 500 on insert.
var groupIDSlugRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,49}$`)

// validCategoryGroupID reports whether a user-supplied group id is a usable slug.
func validCategoryGroupID(id string) bool {
	return groupIDSlugRe.MatchString(id)
}

// CreateGroup adds a user-owned custom group. Base/global groups are never
// created through this endpoint.
func (srv *Server) CreateGroup(c *gin.Context) {
	var req models.CreateCategoryGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}
	if !validCategoryGroupID(req.ID) {
		validation.RespondError(c, "group id must be a lowercase letter followed by lowercase letters, digits, or underscores (max 50 characters)", http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)

	var g models.CategoryGroup
	err := srv.db.QueryRow(c,
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
		slog.Error("CreateGroup", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	g.IsGlobal = false
	c.JSON(http.StatusCreated, g)
}

// UpdateGroup renames / restyles a user's own custom group. Base and global
// groups are immutable.
//
// Three predicates are AND-ed into the UPDATE's WHERE, and each refuses a
// different thing: `user_id = $5` keeps the write to the caller's own group,
// `is_base = FALSE` protects the four seeded groups even for an admin, and the
// id is the user's slug. Because they are one statement, all three produce the
// same pgx.ErrNoRows — which is why the 404 has to go back and re-query to tell
// "not yours" apart from "not there at all". Guessing would either leak the
// existence of another user's group or report a base group as missing.
//
// The COALESCE(NULLIF(...)) columns make this the `putPartial` shape: an empty
// string leaves that column alone and cannot clear it (see UpdateCategory for
// what that costs).
func (srv *Server) UpdateGroup(c *gin.Context) {
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
	err := srv.db.QueryRow(c,
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
			checkErr := srv.db.QueryRow(c,
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
		slog.Error("UpdateGroup", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	g.IsGlobal = false
	c.JSON(http.StatusOK, g)
}

// DeleteGroup removes a user's own custom group. A group that still has
// categories cannot be deleted — the user must move or delete them first.
//
// That refusal is the one place this resource refuses rather than cleans up.
// A category can be deleted and uncategorize its transactions (DeleteCategory
// does that, in a transaction, and reports the count); a group cannot, because
// the transactions would be left pointing at nothing with no category to fall
// back to. Blocking the delete keeps the fix in the user's hands — move the
// categories, or delete them first.
//
// The count is scoped to `user_id = $2`, so a global category sitting in the
// user's group would not block it; the DELETE below re-checks ownership of the
// group itself.
func (srv *Server) DeleteGroup(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)

	var count int
	err := srv.db.QueryRow(c,
		`SELECT COUNT(*) FROM categories WHERE group_id = $1 AND user_id = $2`, id, userID,
	).Scan(&count)
	if err != nil {
		slog.Error("DeleteGroup (count categories)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if count > 0 {
		validation.RespondError(c, "cannot delete a group that still has categories", http.StatusBadRequest)
		return
	}

	result, err := srv.db.Exec(c,
		`DELETE FROM category_groups WHERE id = $1 AND user_id = $2 AND is_base = FALSE`, id, userID)
	if err != nil {
		slog.Error("DeleteGroup", slog.String("error", err.Error()))
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
func (srv *Server) CreateGlobalGroup(c *gin.Context) {
	var req models.CreateCategoryGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}
	if !validCategoryGroupID(req.ID) {
		validation.RespondError(c, "group id must be a lowercase letter followed by lowercase letters, digits, or underscores (max 50 characters)", http.StatusBadRequest)
		return
	}

	var g models.CategoryGroup
	err := srv.db.QueryRow(c,
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
		slog.Error("CreateGlobalGroup", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	g.IsGlobal = true
	c.JSON(http.StatusCreated, g)
}
