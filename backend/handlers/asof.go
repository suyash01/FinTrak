package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fintrak/backend/internal/validation"
)

// parseAsOf resolves the `asOf` parameter against the window the caller asked
// for. It writes a 400 and returns ok=false on a malformed or out-of-window
// value, or when dateFrom is strictly after the clamped asOf.
//
// `asOf` names an instant, not a day to exclude: it is the state of the ledger
// at the END of that day, so a transaction dated the asOf day itself is counted.
// That is why a reporting query built from the returned value filters
// `date <= asOf` and never `date < asOf`.
//
// The clamp never lets the window extend past the instant. A caller that asks
// for a window ending before their asOf has asked for a shorter window, and
// answering with the later instant would report as much as the window covers —
// silently answering a different question than the one asked. So the effective
// instant is pulled back to dateTo, and the dateFrom check that follows makes
// the now-empty window an explicit 400 rather than a bare figure.
//
// An absent parameter passes through as "" with ok=true, so every caller can
// treat "no asOf" and "an asOf equal to the window's end" the same way.
//
// dateFrom and dateTo are the caller's already-validated window, and both must
// be zero-padded YYYY-MM-DD: the two comparisons below are lexical, which is
// chronological for that shape and wrong for anything else. A caller that has
// not put its window through parseQueryDate is comparing a string to a date.
func parseAsOf(c *gin.Context, dateFrom, dateTo string) (asOf string, ok bool) {
	raw := c.Query("asOf")
	if raw == "" {
		return "", true
	}

	// parseQueryDate owns the wording of a malformed YYYY-MM-DD, so reusing it
	// here is what keeps `asOf=01/03/2026` from being reported differently from
	// every other date parameter depending on which handler saw it first.
	if _, passed := parseQueryDate(c, "asOf", raw); !passed {
		return "", false
	}

	// asOf is an instant in the ledger, so it sits in the same window a
	// transaction date does: 1900 is the floor the cycle generator needs, and
	// a year of slack covers a fast client clock.
	if _, msg := validation.CheckTransactionDate(raw, time.Now()); msg != "" {
		validation.RespondError(c, "asOf: "+msg, http.StatusBadRequest)
		return "", false
	}

	asOf = raw
	if dateTo != "" && dateTo < asOf {
		slog.Debug("asOf clamped to window end", slog.String("asOf", raw), slog.String("dateTo", dateTo))
		asOf = dateTo
	}

	// Strictly after, not >=: dateFrom == asOf is a single day, which is a
	// legitimate request and the reason this is a window and not a moment.
	if dateFrom != "" && dateFrom > asOf {
		validation.RespondError(c, fmt.Sprintf("dateFrom %s is after asOf %s", dateFrom, asOf), http.StatusBadRequest)
		return "", false
	}
	return asOf, true
}
