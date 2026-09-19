package handlers

import (
	"log/slog"
	"net/http"

	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
)

// GetAdminCatalog returns the shared global catalog for the admin console: the
// global category groups and global categories, each with the usage counts that
// matter before an admin edits or retires it (how many categories sit in a
// group, how many transactions reference a category). Admin-only.
func (srv *Server) GetAdminCatalog(c *gin.Context) {
	catalog := models.AdminCatalog{
		Groups:     []models.AdminCatalogGroup{},
		Categories: []models.AdminCatalogCategory{},
	}

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
