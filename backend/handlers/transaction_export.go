package handlers

import (
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/internal/validation"
	"github.com/gin-gonic/gin"
)

// maxExportRows caps how many transactions a single filtered export may stream,
// so an unfiltered export of a huge history can't pin a connection
// indefinitely. A filter that matches more than the cap is refused, not
// truncated: a partial CSV looks complete to whoever opens it, and a CSV body
// carries no trailer a browser can read, so the caller cannot be told.
const maxExportRows = 100000

// exportFrom is the FROM/JOIN fragment the export's SELECT and its match count
// share, so the count cannot disagree with the rows the export would stream.
const exportFrom = ` FROM transactions t
	          JOIN accounts a ON t.account_id = a.id
	          LEFT JOIN categories c ON t.category_id = c.id
	          LEFT JOIN category_groups g ON c.group_id = g.id
	          LEFT JOIN payees p ON t.payee_id = p.id`

// ExportTransactions streams the user's transactions as a CSV attachment,
// honoring the exact same filter grammar as GET /transactions (account,
// category/group, payee, tag, free-text over description/notes/payee/tags, date
// range, type, amount, linked, loan, recurring). Unlike the per-account export
// it is a report over whatever the caller has filtered to, and unlike the JSON
// backup it is flat and spreadsheet-friendly.
func (srv *Server) ExportTransactions(c *gin.Context) {
	f, _, ok := txnQueryFilter(c, auth.GetUserID(c))
	if !ok {
		return
	}

	// Count the matches first: the cap has to be applied before the first byte
	// of the body, and the count uses the same FROM and WHERE as the stream
	// below. (They are two statements, so a write landing between them could
	// still fill the LIMIT — that is logged rather than silently shipped.)
	var matching int
	if err := srv.db.QueryRow(c, "SELECT COUNT(*)"+exportFrom+f.where(), f.args...).Scan(&matching); err != nil {
		slog.Error("ExportTransactions count", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if matching > maxExportRows {
		validation.RespondError(c, fmt.Sprintf(
			"the filter matches %d transactions, more than the %d-row export limit; narrow the filters",
			matching, maxExportRows), http.StatusBadRequest)
		return
	}

	// The same shared predicate as the list, with no pagination, plus the
	// joined names so the CSV is readable without id lookups.
	query := `SELECT t.date, t.description, t.amount, t.type,
	                 COALESCE(a.name, '') AS account_name,
	                 COALESCE(c.name, '') AS category_name,
	                 COALESCE(g.name, '') AS group_name,
	                 COALESCE(p.name, '') AS payee,
	                 COALESCE(t.tags, '{}') AS tags, t.notes` +
		exportFrom +
		f.where() +
		// Same total order as the list, so the rows a given filter keeps are
		// always the same ones.
		fmt.Sprintf(" ORDER BY %s LIMIT %d", txnOrderByDate(false), maxExportRows)

	rows, err := srv.db.Query(c, query, f.args...)
	if err != nil {
		slog.Error("ExportTransactions", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	c.Writer.Header().Set("Content-Type", "text/csv")
	c.Writer.Header().Set("Content-Disposition", `attachment; filename="fintrak_transactions.csv"`)

	writer := csv.NewWriter(c.Writer)
	if err := writer.Write([]string{
		"Date", "Description", "Amount", "Type", "Account", "Category", "Group", "Payee", "Tags", "Notes",
	}); err != nil {
		slog.Error("writing CSV header", slog.String("error", err.Error()))
		return
	}

	for rows.Next() {
		var (
			date        time.Time
			description string
			amount      money.Amount
			txnType     string
			accountName string
			category    string
			group       string
			payee       string
			tags        []string
			notes       string
		)
		if err := rows.Scan(&date, &description, &amount, &txnType, &accountName, &category, &group, &payee, &tags, &notes); err != nil {
			// Flush and stop rather than silently emitting a complete-looking
			// but truncated file.
			writer.Flush()
			slog.Error("scanning row in ExportTransactions", slog.String("error", err.Error()))
			return
		}

		if err := writer.Write([]string{
			date.Format("2006-01-02"),
			description,
			amount.String(),
			txnType,
			accountName,
			category,
			group,
			payee,
			strings.Join(tags, ";"),
			notes,
		}); err != nil {
			writer.Flush()
			slog.Error("writing CSV record", slog.String("error", err.Error()))
			return
		}
	}
	if err := rows.Err(); err != nil {
		writer.Flush()
		slog.Error("iterating rows in ExportTransactions", slog.String("error", err.Error()))
		return
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		slog.Error("flushing CSV in ExportTransactions", slog.String("error", err.Error()))
	}
}
