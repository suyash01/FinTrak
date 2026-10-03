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

// Tags. There is no tag table: a tag is a string in `transactions.tags`, and the
// vocabulary is whatever those arrays contain. Every read here is therefore an
// aggregate over the ledger rather than a lookup, and the consequences ripple
// through the whole file:
//
//   - The vocabulary is free text, so it drifts. RenameTag is a history-wide
//     rewrite rather than a rename of one row, and the count it returns is how
//     the UI reports the blast radius.
//   - Tags are a set, not a list, so the bulk add/remove endpoints diff
//     (array_agg DISTINCT) instead of appending. Adding a tag twice must not
//     produce it twice.
//   - `transactions.tags` is never NULL by contract: write edges bind `{}` and
//     every read here wraps it in COALESCE(t.tags, '{}'). A NULL breaks
//     `unnest(tags || $n::text[])` outright, so a bulk add would silently store
//     nothing rather than fail — see AGENTS.md.
//
// Tag add/remove are whole-value operations even though they look like deltas,
// and that asymmetry with the rest of the package is deliberate: a bulk tag add
// is declared `patch`-shaped in the frontend's outbox but its payload is the
// tag list, not the row's current tags, so replaying it is idempotent.

// maxTagsPerRequest bounds how many distinct tags a single bulk add/remove may
// carry, mirroring maxBulkBatch's role for transaction ids.
const maxTagsPerRequest = 100

// GetTags lists the user's tag vocabulary — every distinct tag with the number
// of transactions carrying it — ordered by usage. Tags are derived from
// transactions.tags (there is no tag table), so this is the canonical source
// for the tag filter, picker, and management UI.
func (srv *Server) GetTags(c *gin.Context) {
	// The LATERAL unnest is what turns one row per transaction into one row per
	// (transaction, tag) pair, and it is why this needs no tag table. The
	// COALESCE inside it is the NULL guard the file header describes: without it
	// a NULL tags array would make the row vanish from the cross join instead of
	// contributing nothing, and the vocabulary would silently lose tags.
	//
	// `tag <> ''` drops the empty string, which the write edges never produce but
	// which an empty-array artifact or a hand-edited row could contain — and which
	// would otherwise show up in the picker as a blank option.
	//
	// The tiebreak on the name keeps the list stable between requests, so two
	// identically-used tags do not swap places on each refetch.
	rows, err := srv.db.Query(c,
		`SELECT tag, COUNT(*)::int AS count
		 FROM transactions t
		 CROSS JOIN LATERAL unnest(COALESCE(t.tags, '{}')) AS tag
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

	// Rebuild tags as: (existing ∪ add) \ remove, deduplicated and sorted.
	// COALESCE keeps a row that predates the '{}' column default (its tags are
	// SQL NULL) equivalent to an empty array, and keeps an empty result as '{}'
	// rather than NULL — without it, `tags || $2` is NULL, unnest yields no
	// rows, and the add silently stores nothing. Transactions on closed
	// accounts are skipped (immutable; linking only).
	result, err := srv.db.Exec(c,
		`UPDATE transactions
		 SET tags = COALESCE((
		     SELECT array_agg(DISTINCT x ORDER BY x)
		     FROM unnest(COALESCE(tags, '{}') || $2::text[]) AS x
		     WHERE x <> ALL($3::text[])
		 ), '{}')
		 WHERE user_id = $1 AND id = ANY($4::uuid[])
		   AND NOT EXISTS (SELECT 1 FROM accounts closed_acct WHERE closed_acct.id = transactions.account_id AND closed_acct.closed)`,
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
//
// Three details in the UPDATE below are each load-bearing:
//
//   - `unnest(tags)` is NOT wrapped in COALESCE here, unlike every read. That is
//     safe only because the WHERE requires `$2 = ANY(tags)`, which is false for
//     NULL — so a NULL row can never reach the SET. The file header's "never
//     NULL" contract is what makes the omission safe, and it is why a NULL
//     would be inert here rather than an error.
//   - The inner CASE maps each element to the new name and the outer
//     array_agg(DISTINCT ...) collapses the result. Without the DISTINCT a
//     transaction carrying both `from` and `to` would end up with `to` twice.
//   - ORDER BY inside the aggregate sorts each rewritten array. Not cosmetic:
//     it is what makes the result stable, so a tag array's order does not churn
//     between two identical renames.
//
// The `from == to` short-circuit returns 0 rather than running the statement,
// because the UPDATE would be a no-op that rewrites every matching row's array
// ordering and reports a large count for nothing.
func (srv *Server) RenameTag(c *gin.Context) {
	var req models.RenameTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	// normalizeTags trims and drops blanks, so a blank operand comes back as an
	// empty list with ok=true. Returning here would leave Gin to answer 200 with
	// an empty body — a blank tag accepted as a successful no-op. Every other
	// exit of this handler writes a response (400 from normalizeTags, 200 with
	// the count for the equal-name short-circuit), so the blank case must too.
	from, ok := normalizeTags(c, []string{req.From}, "from")
	if !ok {
		return
	}
	if len(from) == 0 {
		validation.RespondError(c, "from is required", http.StatusBadRequest)
		return
	}
	to, ok := normalizeTags(c, []string{req.To}, "to")
	if !ok {
		return
	}
	if len(to) == 0 {
		validation.RespondError(c, "to is required", http.StatusBadRequest)
		return
	}
	if from[0] == to[0] {
		c.JSON(http.StatusOK, gin.H{"updated": 0})
		return
	}

	// Transactions on closed accounts are skipped (immutable; linking only),
	// so a rename never rewrites a frozen row.
	result, err := srv.db.Exec(c,
		`UPDATE transactions
		 SET tags = COALESCE((
		     SELECT array_agg(DISTINCT new_tag ORDER BY new_tag)
		     FROM (
		         SELECT CASE WHEN x = $2 THEN $3 ELSE x END AS new_tag
		         FROM unnest(tags) AS x
		     ) mapped
		 ), '{}')
		 WHERE user_id = $1 AND $2 = ANY(tags)
		   AND NOT EXISTS (SELECT 1 FROM accounts closed_acct WHERE closed_acct.id = transactions.account_id AND closed_acct.closed)`,
		auth.GetUserID(c), from[0], to[0])
	if err != nil {
		slog.Error("RenameTag", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": result.RowsAffected()})
}

// splitCSVFilter parses a comma-separated list query parameter into a
// de-duplicated, trimmed list. Every list-valued filter parameter of the
// transaction list uses it (tags, accountId, categoryId, groupId, payeeId,
// loanAccountId), so a single value and a one-element list are the same
// grammar. Tags are free text, but the filter uses a simple comma-separated
// grammar, so a tag containing a comma cannot be filtered on.
func splitCSVFilter(raw string) []string {
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		item := strings.TrimSpace(p)
		if item == "" {
			continue
		}
		if _, dup := seen[item]; dup {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
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
