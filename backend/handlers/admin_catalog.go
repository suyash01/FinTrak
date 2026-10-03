package handlers

import (
	"log/slog"
	"net/http"

	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
)

// The admin console's read side: the shared global catalog, plus the usage
// counts an admin needs *before* deciding to edit or retire anything. It is the
// one place that sees the global catalog as a catalogue rather than as
// background to a per-user read.
//
// Both queries are scoped `WHERE user_id IS NULL` — the two-tenant rule from
// category.go. A user's own categories and groups are deliberately absent: this
// is the shared vocabulary, and an admin editing it must never be shown (or be
// able to reach) one user's personal rows. The write side lives in
// CreateGlobalCategory / UpdateGlobalCategory / DeleteGlobalCategory in
// category.go, which are scoped the same way for the same reason.
//
// Nothing here is admin-gated by a check in this function — that is the route
// group's job in main.go. Like every handler in the package it trusts
// auth.GetUserID(c) and the middleware above it.

// GetAdminCatalog returns the shared global catalog for the admin console: the
// global category groups and global categories, each with the usage counts that
// matter before an admin edits or retires it (how many categories sit in a
// group, how many transactions reference a category). Admin-only.
func (srv *Server) GetAdminCatalog(c *gin.Context) {
	catalog := models.AdminCatalog{
		Groups:     []models.AdminCatalogGroup{},
		Categories: []models.AdminCatalogCategory{},
	}

	// The usage counts are correlated subqueries rather than a second query,
	// so each count belongs to the row it is displayed beside. They are
	// intentionally NOT scoped to a user: `c.group_id = g.id` counts the
	// categories in a global group across all users, which is the number that
	// tells an admin whether retiring a global category has blast radius.
	// The `::int` casts are needed because COUNT returns bigint, which pgx
	// refuses to scan into the int32 the model declares.
	groupRows, err := srv.db.Query(c, `
		SELECT g.id, g.name, g.icon, g.color, g.is_base, g.user_id, g.sort_order,
		       (SELECT COUNT(*) FROM categories c WHERE c.group_id = g.id)::int
		FROM category_groups g
		WHERE g.user_id IS NULL
		ORDER BY g.sort_order, g.name`)
	if err != nil {
		slog.Error("GetAdminCatalog (groups)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer groupRows.Close()

	for groupRows.Next() {
		var g models.AdminCatalogGroup
		if err := groupRows.Scan(&g.ID, &g.Name, &g.Icon, &g.Color, &g.IsBase, &g.UserID, &g.SortOrder, &g.CategoryCount); err != nil {
			slog.Error("GetAdminCatalog group scan", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		g.IsGlobal = g.UserID == nil
		catalog.Groups = append(catalog.Groups, g)
	}
	if err := groupRows.Err(); err != nil {
		slog.Error("GetAdminCatalog group rows", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	catRows, err := srv.db.Query(c, `
		SELECT c.id, c.name, c.icon, c.color, c.group_id, g.name, g.is_base,
		       (SELECT COUNT(*) FROM transactions t WHERE t.category_id = c.id)::int
		FROM categories c
		JOIN category_groups g ON c.group_id = g.id
		WHERE c.user_id IS NULL
		ORDER BY g.sort_order, c.name`)
	if err != nil {
		slog.Error("GetAdminCatalog (categories)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer catRows.Close()

	for catRows.Next() {
		var cat models.AdminCatalogCategory
		if err := catRows.Scan(&cat.ID, &cat.Name, &cat.Icon, &cat.Color, &cat.GroupID, &cat.GroupName, &cat.GroupIsBase, &cat.TransactionCount); err != nil {
			slog.Error("GetAdminCatalog category scan", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		cat.IsGlobal = true
		catalog.Categories = append(catalog.Categories, cat)
	}
	if err := catRows.Err(); err != nil {
		slog.Error("GetAdminCatalog category rows", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, catalog)
}
