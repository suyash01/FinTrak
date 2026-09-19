package handlers

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
)

// maxTagLength bounds a single tag's length (in runes). Tags are free-text on
// transactions.tags, so the limit is enforced at every write edge (here and in
// the request validators) rather than by a column constraint.
const maxTagLength = 50

// maxTagsPerRequest bounds how many distinct tags a single bulk add/remove may
// carry, mirroring maxBulkBatch's role for transaction ids.
const maxTagsPerRequest = 100

// GetTags lists the user's tag vocabulary — every distinct tag with the number
// of transactions carrying it — ordered by usage. Tags are derived from
// transactions.tags (there is no tag table), so this is the canonical source
// for the tag filter, picker, and management UI.
func (srv *Server) GetTags(c *gin.Context) {
	rows, err := srv.db.Query(c,
		`SELECT tag, COUNT(*)::int AS count
		 FROM transactions t
		 CROSS JOIN LATERAL unnest(t.tags) AS tag
		 WHERE t.user_id = $1 AND tag <> ''
		 GROUP BY tag
		 ORDER BY COUNT(*) DESC, tag ASC`, auth.GetUserID(c))
	if err != nil {
		slog.Error("GetTags", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	tags := []models.TagCount{}
	for rows.Next() {
		var t models.TagCount
		if err := rows.Scan(&t.Name, &t.Count); err != nil {
			slog.Error("GetTags scan", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		tags = append(tags, t)
	}
	if err := rows.Err(); err != nil {
		slog.Error("GetTags rows", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": tags})
}

// BulkUpdateTags adds and/or removes tags on many transactions at once. The
// array expression rebuilds each row's tags from the union of its current tags
// and the additions, minus the removals, so a transaction that already carries
// a tag is unaffected by re-adding it. Additions are applied before removals.
func (srv *Server) BulkUpdateTags(c *gin.Context) {
	var req models.BulkUpdateTagsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	add, ok := normalizeTags(c, req.Add, "add")
	if !ok {
		return
	}
	remove, ok := normalizeTags(c, req.Remove, "remove")
	if !ok {
		return
	}
	if len(add) == 0 && len(remove) == 0 {
		validation.RespondError(c, "at least one tag to add or remove is required", http.StatusBadRequest)
		return
	}
	if len(req.TransactionIDs) > maxBulkBatch {
		validation.RespondError(c, "too many transactions in one request", http.StatusBadRequest)
		return
	}

	// Rebuild tags as: (existing ∪ add) \ remove, deduplicated and sorted. The
	// COALESCE keeps an empty result as '{}' rather than NULL.
	result, err := srv.db.Exec(c,
		`UPDATE transactions
		 SET tags = COALESCE((
		     SELECT array_agg(DISTINCT x ORDER BY x)
		     FROM unnest(tags || $2::text[]) AS x
		     WHERE x <> ALL($3::text[])
		 ), '{}')
		 WHERE user_id = $1 AND id = ANY($4::uuid[])`,
		auth.GetUserID(c), add, remove, req.TransactionIDs)
	if err != nil {
		slog.Error("BulkUpdateTags", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": result.RowsAffected()})
}

// RenameTag rewrites every occurrence of one tag to another across the user's
// transactions, collapsing duplicates a rename may create. This is the tag
// equivalent of renaming a managed entity, since tags have no id.
func (srv *Server) RenameTag(c *gin.Context) {
	var req models.RenameTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	from, ok := normalizeTags(c, []string{req.From}, "from")
	if !ok || len(from) == 0 {
		return
	}
	to, ok := normalizeTags(c, []string{req.To}, "to")
	if !ok || len(to) == 0 {
		return
	}
	if from[0] == to[0] {
		c.JSON(http.StatusOK, gin.H{"updated": 0})
		return
	}

	result, err := srv.db.Exec(c,
		`UPDATE transactions
		 SET tags = COALESCE((
		     SELECT array_agg(DISTINCT new_tag ORDER BY new_tag)
		     FROM (
		         SELECT CASE WHEN x = $2 THEN $3 ELSE x END AS new_tag
		         FROM unnest(tags) AS x
		     ) mapped
		 ), '{}')
		 WHERE user_id = $1 AND $2 = ANY(tags)`,
		auth.GetUserID(c), from[0], to[0])
	if err != nil {
		slog.Error("RenameTag", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": result.RowsAffected()})
}

// splitTagFilter parses the comma-separated `tags` query parameter into a
// de-duplicated, trimmed list. Tags are free text, but the filter uses a simple
// comma-separated grammar, so a tag containing a comma cannot be filtered on.
func splitTagFilter(raw string) []string {
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		tag := strings.TrimSpace(p)
		if tag == "" {
			continue
		}
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

// normalizeTags trims, drops blanks, rejects over-long tags, and deduplicates a
// tag list. On a validation failure it writes the error response and returns
// ok=false so the caller can return immediately.
func normalizeTags(c *gin.Context, in []string, field string) ([]string, bool) {
	if len(in) > maxTagsPerRequest {
		validation.RespondError(c, "too many tags in one request ("+field+")", http.StatusBadRequest)
		return nil, false
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			continue
		}
		if len([]rune(tag)) > maxTagLength {
			validation.RespondError(c, "tag exceeds the maximum length of 50 characters", http.StatusBadRequest)
			return nil, false
		}
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out, true
}
