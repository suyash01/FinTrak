package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Recurring series tracking. A recurring_series row is a user-defined
// expectation of a repeating charge/income. The backend only forecasts the
// schedule and suggests matching transactions — it never creates transactions
// from a series and never links transactions automatically; the user confirms
// every link explicitly (see AttachRecurring).
const (
	recurringFreqDaily   = "daily"
	recurringFreqWeekly  = "weekly"
	recurringFreqMonthly = "monthly"
	recurringFreqYearly  = "yearly"

	defaultForecastCount = 12
	maxForecastCount     = 60

	defaultRecurringSuggestionLimit = 100
	maxRecurringSuggestionLimit     = 500

	// maxRecurringSteps bounds occurrence generation so a pathological
	// frequency/interval/date combination can never spin forever.
	maxRecurringSteps = 20000

	// maxRecurringCandidates caps how many transactions are scored for a
	// series, which spans every range (not just the most recent). It is a
	// safety bound, far above any realistic subscription history.
	maxRecurringCandidates = 5000
)

// recurringMaxDaysOff bounds how far a candidate may fall from the nearest
// expected occurrence. It scales with the series period so a yearly series
// (whose occurrences are ~365 days apart) is not filtered out entirely.
func recurringMaxDaysOff(s models.RecurringSeries) float64 {
	period := 30.44 // monthly
	switch s.Frequency {
	case recurringFreqDaily:
		period = 1
	case recurringFreqWeekly:
		period = 7
	case recurringFreqMonthly:
		period = 30.44
	case recurringFreqYearly:
		period = 365.25
	}
	if s.Interval > 1 {
		period *= float64(s.Interval)
	}
	return math.Max(20, period/2)
}

// isValidRecurringFrequency reports whether f is a supported frequency.
func isValidRecurringFrequency(f string) bool {
	switch f {
	case recurringFreqDaily, recurringFreqWeekly, recurringFreqMonthly, recurringFreqYearly:
		return true
	}
	return false
}

// parseRecurringDate parses a YYYY-MM-DD date and normalizes it to midnight UTC.
func parseRecurringDate(s string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, errors.New("invalid date (expected YYYY-MM-DD)")
	}
	return dateOnly(t), nil
}

// parseRecurringEnd parses an optional term end date (exclusive), requiring it
// to be after start. An empty string means open-ended (nil).
func parseRecurringEnd(s string, start time.Time) (*time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	end, err := parseRecurringDate(s)
	if err != nil {
		return nil, err
	}
	if !end.After(dateOnly(start)) {
		return nil, errors.New("end date must be after start date")
	}
	e := dateOnly(end)
	return &e, nil
}

// addMonthsAnchored adds months to anchor while preserving its day-of-month,
// clamped to the target month's length. Anchoring on the original day (rather
// than repeatedly adding to the previous result) keeps a series that starts on
// the 31st from drifting to the 28th/30th after the first short month.
func addMonthsAnchored(anchor time.Time, months int) time.Time {
	y, m := anchor.Year(), anchor.Month()
	first := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC).AddDate(0, months, 0)
	day := anchor.Day()
	if last := daysInMonth(first.Year(), first.Month()); day > last {
		day = last
	}
	return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, time.UTC)
}

// recurringOccurrenceAt returns the i-th occurrence of a series, measured from
// its start date. i is zero-based.
func recurringOccurrenceAt(anchor time.Time, freq string, interval, i int) time.Time {
	if interval < 1 {
		interval = 1
	}
	switch freq {
	case recurringFreqDaily:
		return anchor.AddDate(0, 0, i*interval)
	case recurringFreqWeekly:
		return anchor.AddDate(0, 0, 7*i*interval)
	case recurringFreqMonthly:
		return addMonthsAnchored(anchor, i*interval)
	case recurringFreqYearly:
		return addMonthsAnchored(anchor, i*interval*12)
	}
	return anchor
}

// recurringUpcoming returns the first count occurrence dates on/after from,
// respecting the series' optional end date.
func recurringUpcoming(s models.RecurringSeries, from time.Time, count int) []time.Time {
	start := dateOnly(s.StartDate)
	from = dateOnly(from)
	out := []time.Time{}
	if count <= 0 {
		return out
	}
	var end *time.Time
	if s.EndDate != nil {
		e := dateOnly(*s.EndDate)
		end = &e
	}
	for i := 0; i < maxRecurringSteps && len(out) < count; i++ {
		occ := recurringOccurrenceAt(start, s.Frequency, s.Interval, i)
		// The series' end date is exclusive, like every term range it is
		// derived from: an occurrence falling exactly on it is outside the
		// series, and emitting it made nextDueDate and the forecast report a
		// "next due" date no range covers.
		if end != nil && !occ.Before(*end) {
			break
		}
		if occ.Before(from) {
			continue
		}
		out = append(out, occ)
	}
	return out
}

// nextRecurringOccurrence returns the next occurrence on/after from, or nil
// once the series has ended.
func nextRecurringOccurrence(s models.RecurringSeries, from time.Time) *time.Time {
	dates := recurringUpcoming(s, from, 1)
	if len(dates) == 0 {
		return nil
	}
	next := dates[0]
	return &next
}

// recurringMonthlyAmount normalizes a series to an estimated monthly amount in
// minor units using integer math (daily *365/12, weekly *52/12, monthly *1,
// yearly /12), scaled by the interval. It is a display-only estimate.
func recurringMonthlyAmount(s models.RecurringSeries) money.Amount {
	interval := s.Interval
	if interval < 1 {
		interval = 1
	}
	cents := s.Amount.Cents()
	switch s.Frequency {
	case recurringFreqDaily:
		return money.Amount(cents * 365 / (12 * int64(interval)))
	case recurringFreqWeekly:
		return money.Amount(cents * 52 / (12 * int64(interval)))
	case recurringFreqYearly:
		return money.Amount(cents / (12 * int64(interval)))
	default: // monthly
		return money.Amount(cents / int64(interval))
	}
}

// nearestRecurringOccurrence returns the occurrence closest to d and its
// distance in days. ok is false when the series has no occurrences (e.g. d
// falls before an ended series).
func nearestRecurringOccurrence(s models.RecurringSeries, d time.Time) (time.Time, float64, bool) {
	start := dateOnly(s.StartDate)
	d = dateOnly(d)
	var end *time.Time
	if s.EndDate != nil {
		e := dateOnly(*s.EndDate)
		end = &e
	}
	var best time.Time
	bestDiff := math.MaxFloat64
	found := false
	for i := 0; i < maxRecurringSteps; i++ {
		occ := recurringOccurrenceAt(start, s.Frequency, s.Interval, i)
		// Exclusive end, as in recurringUpcoming: an occurrence on the end date
		// is outside the series, so scoring a transaction against it would
		// invent an expected payment that never was.
		if end != nil && !occ.Before(*end) {
			break
		}
		diff := math.Abs(occ.Sub(d).Hours() / 24)
		if diff < bestDiff {
			bestDiff = diff
			best = occ
			found = true
		}
		// Occurrences are monotonic: once we pass d, the next one is only
		// farther away, so the closest has already been recorded.
		if occ.After(d) {
			break
		}
	}
	return best, bestDiff, found
}

// recurringDescriptionMatches reports whether any meaningful word of the
// series name appears in the transaction description.
func recurringDescriptionMatches(s models.RecurringSeries, desc string) bool {
	descLower := strings.ToLower(desc)
	for _, word := range strings.Fields(strings.ToLower(s.Name)) {
		if len(word) >= 3 && strings.Contains(descLower, word) {
			return true
		}
	}
	return false
}

// calculateRecurringScore ranks a candidate transaction against a series from
// 0-100: it starts at 100 and subtracts for distance from the nearest expected
// occurrence, then adds a small bonus for a matching description, category, or
// payee. Candidate amounts always match the series exactly (enforced by the
// suggestion query), so amount is not scored.
func calculateRecurringScore(s models.RecurringSeries, txn models.Transaction, daysOff float64) float64 {
	score := 100.0
	score -= daysOff * 8
	if recurringDescriptionMatches(s, txn.Description) {
		score += 15
	}
	if s.CategoryID != nil && txn.CategoryID != nil && *s.CategoryID == *txn.CategoryID {
		score += 10
	}
	if s.PayeeID != nil && txn.PayeeID != nil && *s.PayeeID == *txn.PayeeID {
		score += 10
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score
}

// clampQueryInt reads an integer query param, falling back to def when it is
// missing/invalid/below minVal and capping it at maxVal.
func clampQueryInt(c *gin.Context, name string, def, minVal, maxVal int) int {
	v, err := strconv.Atoi(c.Query(name))
	if err != nil || v < minVal {
		v = def
	}
	if v > maxVal {
		v = maxVal
	}
	return v
}

// recurringSeriesColumns is the SELECT/RETURNING column list for a series row
// (without the joined names), keeping the scan order in one place. The series'
// account, amount and date range are not columns: they are derived from its
// terms (see deriveRecurringSeries).
const recurringSeriesColumns = `id, name, description, type,
	frequency, interval, category_id, payee_id, active, notes, created_at`

// scanRecurringSeries scans the recurringSeriesColumns into s. The derived
// account/amount/date fields are left zero; call deriveRecurringSeries with the
// series' terms to populate them.
func scanRecurringSeries(row pgx.Row, s *models.RecurringSeries) error {
	return row.Scan(&s.ID, &s.Name, &s.Description, &s.Type,
		&s.Frequency, &s.Interval, &s.CategoryID, &s.PayeeID,
		&s.Active, &s.Notes, &s.CreatedAt)
}

// deriveRecurringSeries fills a series' derived fields from its terms (sorted
// oldest first): StartDate is the earliest term start, EndDate the latest term
// end (nil while open-ended), and AccountID/Amount the term in effect today
// (falling back to the nearest term). A term-less series is left untouched.
func deriveRecurringSeries(s *models.RecurringSeries, terms []models.RecurringSeriesTerm) {
	if len(terms) == 0 {
		return
	}
	s.StartDate = dateOnly(terms[0].StartDate)
	s.EndDate = terms[len(terms)-1].EndDate
	if cur := recurringTermAt(terms, time.Now()); cur != nil {
		s.AccountID = cur.AccountID
		s.Amount = cur.Amount
	}
}

// loadRecurringSeries fetches one series owned by userID. The caller maps
// pgx.ErrNoRows to a 404.
func (srv *Server) loadRecurringSeries(c *gin.Context, id, userID uuid.UUID) (models.RecurringSeries, error) {
	var s models.RecurringSeries
	err := scanRecurringSeries(srv.db.QueryRow(c,
		"SELECT "+recurringSeriesColumns+" FROM recurring_series WHERE id = $1 AND user_id = $2",
		id, userID), &s)
	return s, err
}

// recurringTermColumns is the SELECT column list for a term row (with the
// joined account name), keeping the scan order in one place.
const recurringTermColumns = `t.id, t.series_id, t.start_date, t.end_date, t.amount, t.account_id,
	COALESCE(a.name, ''), t.created_at`

// loadRecurringTerms returns a series' date-ranged terms, oldest first, with
// the linked account's name. Terms drive per-range amounts/accounts in the
// forecast and the range-based suggestion filter.
func (srv *Server) loadRecurringTerms(c *gin.Context, seriesID, userID uuid.UUID) ([]models.RecurringSeriesTerm, error) {
	rows, err := srv.db.Query(c,
		`SELECT `+recurringTermColumns+`
		 FROM recurring_series_terms t
		 JOIN accounts a ON a.id = t.account_id
		 WHERE t.series_id = $1 AND t.user_id = $2
		 ORDER BY t.start_date ASC`,
		seriesID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	terms := []models.RecurringSeriesTerm{}
	for rows.Next() {
		var t models.RecurringSeriesTerm
		if err := rows.Scan(&t.ID, &t.SeriesID, &t.StartDate, &t.EndDate, &t.Amount, &t.AccountID,
			&t.AccountName, &t.CreatedAt); err != nil {
			return nil, err
		}
		terms = append(terms, t)
	}
	return terms, rows.Err()
}

// recurringTermContaining returns the term whose [start, end) range contains d
// (end nil = open-ended), or nil when d falls outside every range.
func recurringTermContaining(terms []models.RecurringSeriesTerm, d time.Time) *models.RecurringSeriesTerm {
	d = dateOnly(d)
	for i := range terms {
		t := &terms[i]
		if dateOnly(t.StartDate).After(d) {
			break
		}
		if t.EndDate == nil || d.Before(dateOnly(*t.EndDate)) {
			return t
		}
	}
	return nil
}

// recurringTermAt returns the term covering d, or — when d falls in a gap — the
// most recent term before it, or the earliest term when d precedes them all.
// It is the display/forecast resolver; matching uses recurringTermContaining so
// transactions in a gap match nothing. Returns nil only when there are no terms.
func recurringTermAt(terms []models.RecurringSeriesTerm, d time.Time) *models.RecurringSeriesTerm {
	if len(terms) == 0 {
		return nil
	}
	d = dateOnly(d)
	var latestBefore *models.RecurringSeriesTerm
	for i := range terms {
		t := &terms[i]
		if dateOnly(t.StartDate).After(d) {
			break
		}
		latestBefore = t
		if t.EndDate == nil || d.Before(dateOnly(*t.EndDate)) {
			return t
		}
	}
	if latestBefore != nil {
		return latestBefore
	}
	return &terms[0]
}

// recurringTermsOverlap reports whether the [start, end) range (end nil =
// open-ended) overlaps any existing term other than excludeID. The end is
// exclusive, so adjacent ranges (one ending exactly where the next starts) do
// not overlap.
func recurringTermsOverlap(terms []models.RecurringSeriesTerm, start time.Time, end *time.Time, excludeID uuid.UUID) bool {
	start = dateOnly(start)
	for i := range terms {
		t := &terms[i]
		if t.ID == excludeID {
			continue
		}
		// The existing term ends at/before the new range starts.
		if t.EndDate != nil && !dateOnly(*t.EndDate).After(start) {
			continue
		}
		// The existing term starts at/after the new range ends.
		if end != nil && !dateOnly(t.StartDate).Before(dateOnly(*end)) {
			continue
		}
		return true
	}
	return false
}

// errRecurringAccountNotOwned signals a term referencing an account the user
// does not own (surfaced as a 400 rather than a constraint error).
var errRecurringAccountNotOwned = errors.New("referenced account not found")

// errRecurringRangeOverlap signals a new/edited term whose range overlaps an
// existing term of the same series.
var errRecurringRangeOverlap = errors.New("term date range overlaps an existing term")

// applyRecurringChange records an amount/account change effective from eff by
// editing the term ranges: the term covering eff is closed at eff (or updated
// in place when it starts exactly on eff), and a new term [eff, next start) is
// opened (open-ended when there is no later term).
func applyRecurringChange(c *gin.Context, tx pgx.Tx, seriesID, userID uuid.UUID, terms []models.RecurringSeriesTerm, eff time.Time, amount money.Amount, accountID uuid.UUID) error {
	eff = dateOnly(eff)

	var covering *models.RecurringSeriesTerm
	var next *models.RecurringSeriesTerm
	for i := range terms {
		t := &terms[i]
		if dateOnly(t.StartDate).After(eff) {
			next = t
			break
		}
		if t.EndDate == nil || eff.Before(dateOnly(*t.EndDate)) {
			covering = t
		}
	}

	// The change applies exactly where a term already starts: update in place.
	if covering != nil && dateOnly(covering.StartDate).Equal(eff) {
		res, err := tx.Exec(c,
			`UPDATE recurring_series_terms t SET amount = $1, account_id = $2
			 WHERE t.id = $3 AND t.user_id = $4
			   AND EXISTS (SELECT 1 FROM accounts a WHERE a.id = $2 AND a.user_id = $4)`,
			amount, accountID, covering.ID, userID)
		if err != nil {
			return err
		}
		if res.RowsAffected() == 0 {
			return errRecurringAccountNotOwned
		}
		return nil
	}
	// Close the covering term at the change (exclusive end).
	if covering != nil {
		if _, err := tx.Exec(c,
			`UPDATE recurring_series_terms SET end_date = $1 WHERE id = $2 AND user_id = $3`,
			eff, covering.ID, userID); err != nil {
			return err
		}
	}
	var end *time.Time
	if next != nil {
		e := dateOnly(next.StartDate)
		end = &e
	}
	res, err := tx.Exec(c,
		`INSERT INTO recurring_series_terms (series_id, user_id, start_date, end_date, amount, account_id)
		 SELECT $1, $2, $3, $4, $5, $6
		 WHERE EXISTS (SELECT 1 FROM accounts a WHERE a.id = $6 AND a.user_id = $2)`,
		seriesID, userID, eff, end, amount, accountID)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return errRecurringAccountNotOwned
	}
	return nil
}

// parsedRecurringRange is a validated range ready to persist.
type parsedRecurringRange struct {
	start     time.Time
	end       *time.Time
	amount    money.Amount
	accountID uuid.UUID
}

// parseSeriesRanges parses and validates a supplied range list: positive
// amounts, valid dates (end after start, exclusive), sorted by start, and
// non-overlapping. Gaps are allowed, so a subscription can be discontinued and
// resumed later; an entry's end may be omitted for an open-ended entry.
func parseSeriesRanges(reqs []models.RecurringSeriesRange) ([]parsedRecurringRange, error) {
	if len(reqs) == 0 {
		return nil, errors.New("at least one range is required")
	}
	out := make([]parsedRecurringRange, 0, len(reqs))
	for _, r := range reqs {
		if r.Amount <= 0 {
			return nil, errors.New("amount must be positive")
		}
		start, err := parseRecurringDate(r.StartDate)
		if err != nil {
			return nil, err
		}
		end, err := parseRecurringEnd(r.EndDate, start)
		if err != nil {
			return nil, err
		}
		out = append(out, parsedRecurringRange{start: start, end: end, amount: r.Amount, accountID: r.AccountID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start.Before(out[j].start) })
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if seriesRangesOverlap(out[i], out[j]) {
				return nil, errRecurringRangeOverlap
			}
		}
	}
	return out, nil
}

// seriesRangesOverlap reports whether two [start, end) ranges overlap. An
// omitted end is open-ended (and therefore overlaps any later range).
func seriesRangesOverlap(a, b parsedRecurringRange) bool {
	if a.end != nil && !b.start.Before(*a.end) {
		return false
	}
	if b.end != nil && !a.start.Before(*b.end) {
		return false
	}
	return true
}

// primaryRange returns the range covering today, or (falling back) the one with
// the latest start on/before today, or the earliest. The series' derived
// current account/amount is seeded from it.
func primaryRange(ranges []parsedRecurringRange, today time.Time) parsedRecurringRange {
	today = dateOnly(today)
	primary := ranges[0]
	for i := range ranges {
		if ranges[i].start.After(today) {
			break
		}
		primary = ranges[i]
		if ranges[i].end == nil || today.Before(*ranges[i].end) {
			return ranges[i]
		}
	}
	return primary
}

// GetRecurringSeries lists the user's recurring series with joined
// account/category/payee names and computed next-due, monthly-normalized cost,
// and attached transaction count.
func (srv *Server) GetRecurringSeries(c *gin.Context) {
	rows, err := srv.db.Query(c, `
		SELECT rs.id, rs.name, rs.description, rs.type,
		       rs.frequency, rs.interval,
		       rs.category_id, rs.payee_id, rs.active, rs.notes, rs.created_at,
		       et.account_id, et.amount, agg.start_date, agg.end_date,
		       a.name,
		       COALESCE(c.name, ''), COALESCE(c.icon, ''), COALESCE(c.color, ''),
		       COALESCE(p.name, ''),
		       (SELECT COUNT(*) FROM recurring_attachments ra
		        WHERE ra.user_id = rs.user_id AND ra.series_id = rs.id)
		FROM recurring_series rs
		JOIN LATERAL (
		    SELECT t.account_id, t.amount
		    FROM recurring_series_terms t
		    WHERE t.series_id = rs.id AND t.user_id = rs.user_id
		    ORDER BY (t.start_date <= CURRENT_DATE) DESC,
		             CASE WHEN t.start_date <= CURRENT_DATE THEN t.start_date END DESC,
		             CASE WHEN t.start_date > CURRENT_DATE THEN t.start_date END ASC
		    LIMIT 1
		) et ON TRUE
		JOIN LATERAL (
		    SELECT MIN(t.start_date) AS start_date,
		           CASE WHEN bool_or(t.end_date IS NULL) THEN NULL ELSE MAX(t.end_date) END AS end_date
		    FROM recurring_series_terms t
		    WHERE t.series_id = rs.id AND t.user_id = rs.user_id
		) agg ON TRUE
		JOIN accounts a ON a.id = et.account_id
		LEFT JOIN categories c ON rs.category_id = c.id
		LEFT JOIN payees p ON rs.payee_id = p.id
		WHERE rs.user_id = $1
		ORDER BY rs.active DESC, rs.name ASC`, auth.GetUserID(c))
	if err != nil {
		slog.Error("GetRecurringSeries", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	today := dateOnly(time.Now())
	series := []models.RecurringSeries{}
	for rows.Next() {
		var s models.RecurringSeries
		if err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.Type,
			&s.Frequency, &s.Interval, &s.CategoryID, &s.PayeeID,
			&s.Active, &s.Notes, &s.CreatedAt,
			&s.AccountID, &s.Amount, &s.StartDate, &s.EndDate,
			&s.AccountName, &s.CategoryName, &s.CategoryIcon,
			&s.CategoryColor, &s.Payee, &s.AttachedCount); err != nil {
			slog.Error("GetRecurringSeries scan", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		s.NextDueDate = nextRecurringOccurrence(s, today)
		s.MonthlyAmount = recurringMonthlyAmount(s)
		series = append(series, s)
	}
	if err := rows.Err(); err != nil {
		slog.Error("GetRecurringSeries rows", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": series})
}

// CreateRecurringSeries inserts a user-defined recurring series, enforcing
// ownership of the referenced account/category/payee.
func (srv *Server) CreateRecurringSeries(c *gin.Context) {
	var req models.CreateRecurringSeriesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}
	if req.Type != "debit" && req.Type != "credit" {
		validation.RespondError(c, "type must be 'debit' or 'credit'", http.StatusBadRequest)
		return
	}
	if !isValidRecurringFrequency(req.Frequency) {
		validation.RespondError(c, "frequency must be one of daily, weekly, monthly, yearly", http.StatusBadRequest)
		return
	}
	interval := req.Interval
	if interval <= 0 {
		interval = 1
	}
	if interval > 365 {
		validation.RespondError(c, "interval must be between 1 and 365", http.StatusBadRequest)
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}

	// The definition comes from the supplied Ranges list (auto-contiguous: each
	// range ends where the next begins, the last open-ended), or a single range
	// from StartDate + AccountID + Amount. The subscription's own period is
	// derived from the ranges.
	var err error
	var ranges []parsedRecurringRange
	if len(req.Ranges) > 0 {
		ranges, err = parseSeriesRanges(req.Ranges)
		if err != nil {
			validation.RespondError(c, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		if strings.TrimSpace(req.StartDate) == "" || req.AccountID == nil || req.Amount == nil {
			validation.RespondError(c, "startDate, accountId and amount (or a ranges list) are required", http.StatusBadRequest)
			return
		}
		if *req.Amount <= 0 {
			validation.RespondError(c, "amount must be positive", http.StatusBadRequest)
			return
		}
		start, serr := parseRecurringDate(req.StartDate)
		if serr != nil {
			validation.RespondError(c, serr.Error(), http.StatusBadRequest)
			return
		}
		end, eerr := parseRecurringEnd(req.EndDate, start)
		if eerr != nil {
			validation.RespondError(c, eerr.Error(), http.StatusBadRequest)
			return
		}
		ranges = []parsedRecurringRange{{start: start, end: end, amount: *req.Amount, accountID: *req.AccountID}}
	}
	// The subscription's period is derived: the earliest entry's start and the
	// latest entry's end (open-ended when that entry has no end). The "current"
	// account/amount shown on the series is the range in effect today.
	seriesStart := ranges[0].start
	seriesEnd := ranges[len(ranges)-1].end
	primary := primaryRange(ranges, dateOnly(time.Now()))

	userID := auth.GetUserID(c)
	var s models.RecurringSeries
	err = db.WithTx(c, srv.db, func(tx pgx.Tx) error {
		if err := scanRecurringSeries(tx.QueryRow(c, `
			INSERT INTO recurring_series (user_id, name, description, type, frequency, interval, category_id, payee_id, active, notes)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
			WHERE ($7::uuid IS NULL OR EXISTS (SELECT 1 FROM categories c WHERE c.id = $7 AND (c.user_id = $1 OR c.user_id IS NULL)))
			  AND ($8::uuid IS NULL OR EXISTS (SELECT 1 FROM payees p WHERE p.id = $8 AND p.user_id = $1))
			RETURNING `+recurringSeriesColumns,
			userID, req.Name, req.Description, req.Type, req.Frequency, interval,
			req.CategoryID, req.PayeeID, active, req.Notes,
		), &s); err != nil {
			return err
		}
		// Persist every range; each references an account the user must own.
		for _, r := range ranges {
			res, err := tx.Exec(c,
				`INSERT INTO recurring_series_terms (series_id, user_id, start_date, end_date, amount, account_id)
				 SELECT $1, $2, $3, $4, $5, $6
				 WHERE EXISTS (SELECT 1 FROM accounts a WHERE a.id = $6 AND a.user_id = $2)`,
				s.ID, userID, r.start, r.end, r.amount, r.accountID)
			if err != nil {
				return err
			}
			if res.RowsAffected() == 0 {
				return errRecurringAccountNotOwned
			}
		}
		return nil
	})
	if errors.Is(err, errRecurringAccountNotOwned) {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "referenced account, category, or payee not found", http.StatusBadRequest)
		return
	}
	if err != nil {
		slog.Error("CreateRecurringSeries", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	s.StartDate = seriesStart
	s.EndDate = seriesEnd
	s.AccountID = primary.accountID
	s.Amount = primary.amount
	s.NextDueDate = nextRecurringOccurrence(s, dateOnly(time.Now()))
	s.MonthlyAmount = recurringMonthlyAmount(s)
	c.JSON(http.StatusCreated, s)
}

// UpdateRecurringSeries applies a partial update. The existing row is loaded,
// the provided fields are merged in, the merged result is validated, and a
// single fixed UPDATE persists it (enforcing ownership of any referenced
// account/category/payee).
func (srv *Server) UpdateRecurringSeries(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}
	var req models.UpdateRecurringSeriesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	userID := auth.GetUserID(c)
	s, err := srv.loadRecurringSeries(c, id, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "recurring series not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("UpdateRecurringSeries (load)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	terms, err := srv.loadRecurringTerms(c, id, userID)
	if err != nil {
		slog.Error("UpdateRecurringSeries (load terms)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	deriveRecurringSeries(&s, terms)

	origAmount, origAccount := s.Amount, s.AccountID

	if req.AccountID != nil {
		s.AccountID = *req.AccountID
	}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			validation.RespondError(c, "name must not be empty", http.StatusBadRequest)
			return
		}
		s.Name = *req.Name
	}
	if req.Description != nil {
		s.Description = *req.Description
	}
	if req.Amount != nil {
		if *req.Amount <= 0 {
			validation.RespondError(c, "amount must be positive", http.StatusBadRequest)
			return
		}
		s.Amount = *req.Amount
	}
	if req.Type != nil {
		if *req.Type != "debit" && *req.Type != "credit" {
			validation.RespondError(c, "type must be 'debit' or 'credit'", http.StatusBadRequest)
			return
		}
		s.Type = *req.Type
	}
	if req.Frequency != nil {
		if !isValidRecurringFrequency(*req.Frequency) {
			validation.RespondError(c, "frequency must be one of daily, weekly, monthly, yearly", http.StatusBadRequest)
			return
		}
		s.Frequency = *req.Frequency
	}
	if req.Interval != nil {
		if *req.Interval < 1 || *req.Interval > 365 {
			validation.RespondError(c, "interval must be between 1 and 365", http.StatusBadRequest)
			return
		}
		s.Interval = *req.Interval
	}
	if req.CategoryID.Set() {
		s.CategoryID = req.CategoryID.Value()
	}
	if req.PayeeID.Set() {
		s.PayeeID = req.PayeeID.Value()
	}
	if req.Active != nil {
		s.Active = *req.Active
	}
	if req.Notes != nil {
		s.Notes = *req.Notes
	}

	// Either replace the whole range list (form edit) or record a single
	// amount/account change (lightweight edit).
	var newRanges []parsedRecurringRange
	if len(req.Ranges) > 0 {
		r, err := parseSeriesRanges(req.Ranges)
		if err != nil {
			validation.RespondError(c, err.Error(), http.StatusBadRequest)
			return
		}
		newRanges = r
	}
	valueChanged := len(newRanges) == 0 && (s.Amount != origAmount || s.AccountID != origAccount)

	eff := dateOnly(time.Now())
	explicitEff := false
	if req.EffectiveDate != nil && strings.TrimSpace(*req.EffectiveDate) != "" {
		e, err := parseRecurringDate(*req.EffectiveDate)
		if err != nil {
			validation.RespondError(c, err.Error(), http.StatusBadRequest)
			return
		}
		eff, explicitEff = e, true
	}
	if valueChanged && eff.Before(dateOnly(s.StartDate)) {
		if explicitEff {
			validation.RespondError(c, "effective date must not be before start date", http.StatusBadRequest)
			return
		}
		eff = dateOnly(s.StartDate)
	}

	err = db.WithTx(c, srv.db, func(tx pgx.Tx) error {
		switch {
		case len(newRanges) > 0:
			// Replace every range with the supplied list.
			if _, err := tx.Exec(c,
				`DELETE FROM recurring_series_terms WHERE series_id = $1 AND user_id = $2`,
				id, userID); err != nil {
				return err
			}
			for _, r := range newRanges {
				res, err := tx.Exec(c,
					`INSERT INTO recurring_series_terms (series_id, user_id, start_date, end_date, amount, account_id)
					 SELECT $1, $2, $3, $4, $5, $6
					 WHERE EXISTS (SELECT 1 FROM accounts a WHERE a.id = $6 AND a.user_id = $2)`,
					id, userID, r.start, r.end, r.amount, r.accountID)
				if err != nil {
					return err
				}
				if res.RowsAffected() == 0 {
					return errRecurringAccountNotOwned
				}
			}
		case valueChanged:
			if err := applyRecurringChange(c, tx, id, userID, terms, eff, s.Amount, s.AccountID); err != nil {
				return err
			}
		}
		return scanRecurringSeries(tx.QueryRow(c, `
			UPDATE recurring_series SET
				name = $1, description = $2, type = $3,
				frequency = $4, interval = $5,
				category_id = $6, payee_id = $7, active = $8, notes = $9, updated_at = NOW()
			WHERE id = $10 AND user_id = $11
			  AND ($6::uuid IS NULL OR EXISTS (SELECT 1 FROM categories c WHERE c.id = $6 AND (c.user_id = $11 OR c.user_id IS NULL)))
			  AND ($7::uuid IS NULL OR EXISTS (SELECT 1 FROM payees p WHERE p.id = $7 AND p.user_id = $11))
			RETURNING `+recurringSeriesColumns,
			s.Name, s.Description, s.Type, s.Frequency, s.Interval,
			s.CategoryID, s.PayeeID, s.Active, s.Notes, id, userID,
		), &s)
	})
	if errors.Is(err, errRecurringAccountNotOwned) {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "referenced category or payee not found", http.StatusBadRequest)
		return
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		validation.RespondError(c, errRecurringRangeOverlap.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		slog.Error("UpdateRecurringSeries (update)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	terms, err = srv.loadRecurringTerms(c, id, userID)
	if err != nil {
		slog.Error("UpdateRecurringSeries (reload terms)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	deriveRecurringSeries(&s, terms)
	s.NextDueDate = nextRecurringOccurrence(s, dateOnly(time.Now()))
	s.MonthlyAmount = recurringMonthlyAmount(s)
	c.JSON(http.StatusOK, s)
}

// DeleteRecurringSeries removes a series owned by the user; its attachments
// cascade away with it (the transactions themselves are untouched).
func (srv *Server) DeleteRecurringSeries(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}
	result, err := srv.db.Exec(c,
		"DELETE FROM recurring_series WHERE id = $1 AND user_id = $2", id, auth.GetUserID(c))
	if err != nil {
		slog.Error("DeleteRecurringSeries", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if result.RowsAffected() == 0 {
		validation.RespondError(c, "recurring series not found", http.StatusNotFound)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// GetRecurringTerms lists a series' effective-dated amount/account terms,
// oldest first.
func (srv *Server) GetRecurringTerms(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}
	userID := auth.GetUserID(c)
	if _, err := srv.loadRecurringSeries(c, id, userID); errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "recurring series not found", http.StatusNotFound)
		return
	} else if err != nil {
		slog.Error("GetRecurringTerms (load)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	terms, err := srv.loadRecurringTerms(c, id, userID)
	if err != nil {
		slog.Error("GetRecurringTerms", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": terms})
}

// CreateRecurringTerm records a date-ranged amount/account entry. Its range
// must not overlap an existing entry of the same series.
func (srv *Server) CreateRecurringTerm(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}
	var req models.CreateRecurringSeriesTermRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}
	if req.Amount <= 0 {
		validation.RespondError(c, "amount must be positive", http.StatusBadRequest)
		return
	}
	start, err := parseRecurringDate(req.StartDate)
	if err != nil {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}
	end, err := parseRecurringEnd(req.EndDate, start)
	if err != nil {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)
	if _, err := srv.loadRecurringSeries(c, id, userID); errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "recurring series not found", http.StatusNotFound)
		return
	} else if err != nil {
		slog.Error("CreateRecurringTerm (load)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	terms, err := srv.loadRecurringTerms(c, id, userID)
	if err != nil {
		slog.Error("CreateRecurringTerm (terms)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if recurringTermsOverlap(terms, start, end, uuid.Nil) {
		validation.RespondError(c, errRecurringRangeOverlap.Error(), http.StatusBadRequest)
		return
	}

	var term models.RecurringSeriesTerm
	err = db.WithTx(c, srv.db, func(tx pgx.Tx) error {
		// The referenced account must belong to the user; folding the check
		// into the insert means a cross-user account can never be used.
		if err := tx.QueryRow(c,
			`INSERT INTO recurring_series_terms (series_id, user_id, start_date, end_date, amount, account_id)
			 SELECT $1, $2, $3, $4, $5, $6
			 WHERE EXISTS (SELECT 1 FROM accounts a WHERE a.id = $6 AND a.user_id = $2)
			 RETURNING id, series_id, start_date, end_date, amount, account_id, created_at`,
			id, userID, start, end, req.Amount, req.AccountID,
		).Scan(&term.ID, &term.SeriesID, &term.StartDate, &term.EndDate, &term.Amount, &term.AccountID, &term.CreatedAt); err != nil {
			return err
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "referenced account not found", http.StatusBadRequest)
		return
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		validation.RespondError(c, errRecurringRangeOverlap.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		slog.Error("CreateRecurringTerm", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusCreated, term)
}

// UpdateRecurringTerm edits a term's date range, amount, or account. The edited
// range must not overlap another entry of the same series.
func (srv *Server) UpdateRecurringTerm(c *gin.Context) {
	seriesID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}
	termID, err := uuid.Parse(c.Param("termId"))
	if err != nil {
		validation.RespondError(c, "invalid term id", http.StatusBadRequest)
		return
	}
	var req models.UpdateRecurringSeriesTermRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	userID := auth.GetUserID(c)
	if _, err := srv.loadRecurringSeries(c, seriesID, userID); errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "recurring series not found", http.StatusNotFound)
		return
	} else if err != nil {
		slog.Error("UpdateRecurringTerm (load)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	terms, err := srv.loadRecurringTerms(c, seriesID, userID)
	if err != nil {
		slog.Error("UpdateRecurringTerm (terms)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	var current *models.RecurringSeriesTerm
	for i := range terms {
		if terms[i].ID == termID {
			current = &terms[i]
			break
		}
	}
	if current == nil {
		validation.RespondError(c, "term not found", http.StatusNotFound)
		return
	}

	merged := *current
	if req.StartDate != nil {
		start, err := parseRecurringDate(*req.StartDate)
		if err != nil {
			validation.RespondError(c, err.Error(), http.StatusBadRequest)
			return
		}
		merged.StartDate = start
	}
	if req.EndDate != nil {
		if strings.TrimSpace(*req.EndDate) == "" {
			merged.EndDate = nil
		} else {
			end, err := parseRecurringDate(*req.EndDate)
			if err != nil {
				validation.RespondError(c, err.Error(), http.StatusBadRequest)
				return
			}
			merged.EndDate = &end
		}
	}
	if req.Amount != nil {
		if *req.Amount <= 0 {
			validation.RespondError(c, "amount must be positive", http.StatusBadRequest)
			return
		}
		merged.Amount = *req.Amount
	}
	if req.AccountID != nil {
		merged.AccountID = *req.AccountID
	}
	if merged.EndDate != nil && !dateOnly(*merged.EndDate).After(dateOnly(merged.StartDate)) {
		validation.RespondError(c, "end date must be after start date", http.StatusBadRequest)
		return
	}
	if recurringTermsOverlap(terms, merged.StartDate, merged.EndDate, termID) {
		validation.RespondError(c, errRecurringRangeOverlap.Error(), http.StatusBadRequest)
		return
	}

	err = db.WithTx(c, srv.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(c,
			`UPDATE recurring_series_terms t SET start_date = $1, end_date = $2, amount = $3, account_id = $4
			 WHERE t.id = $5 AND t.series_id = $6 AND t.user_id = $7
			   AND EXISTS (SELECT 1 FROM accounts a WHERE a.id = $4 AND a.user_id = $7)
			 RETURNING t.id, t.series_id, t.start_date, t.end_date, t.amount, t.account_id, t.created_at`,
			merged.StartDate, merged.EndDate, merged.Amount, merged.AccountID, termID, seriesID, userID,
		).Scan(&merged.ID, &merged.SeriesID, &merged.StartDate, &merged.EndDate, &merged.Amount, &merged.AccountID, &merged.CreatedAt); err != nil {
			return err
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "referenced account not found", http.StatusBadRequest)
		return
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		validation.RespondError(c, errRecurringRangeOverlap.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		slog.Error("UpdateRecurringTerm", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusOK, merged)
}

// DeleteRecurringTerm removes a date range. Any term may be removed; deleting
// one leaves a gap that matches no transaction until it is replaced.
func (srv *Server) DeleteRecurringTerm(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}
	termID, err := uuid.Parse(c.Param("termId"))
	if err != nil {
		validation.RespondError(c, "invalid term id", http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)
	if _, err := srv.loadRecurringSeries(c, id, userID); errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "recurring series not found", http.StatusNotFound)
		return
	} else if err != nil {
		slog.Error("DeleteRecurringTerm (load)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	terms, err := srv.loadRecurringTerms(c, id, userID)
	if err != nil {
		slog.Error("DeleteRecurringTerm (terms)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	found := false
	for _, t := range terms {
		if t.ID == termID {
			found = true
			break
		}
	}
	if !found {
		validation.RespondError(c, "term not found", http.StatusNotFound)
		return
	}
	// A series is read entirely through its ranges: the list, the forecast and
	// the suggestion filter all inner-join them, so a series left without any
	// would disappear from GET /recurring while its attachments (and their
	// linked transactions) stayed behind, and by id it would degrade to
	// zero-amount occurrences. Refuse the delete that would do it, exactly as
	// creating a series without a range is refused.
	if len(terms) == 1 {
		validation.RespondError(c, "a series must keep at least one range", http.StatusBadRequest)
		return
	}

	err = db.WithTx(c, srv.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(c,
			`DELETE FROM recurring_series_terms WHERE id = $1 AND series_id = $2 AND user_id = $3`,
			termID, id, userID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		slog.Error("DeleteRecurringTerm", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// GetRecurringForecast projects the next occurrences of a series and marks
// which already have a matching attached transaction.
func (srv *Server) GetRecurringForecast(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}
	userID := auth.GetUserID(c)
	series, err := srv.loadRecurringSeries(c, id, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "recurring series not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("GetRecurringForecast (load)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	count := clampQueryInt(c, "count", defaultForecastCount, 1, maxForecastCount)

	// Attached transaction dates are used to mark occurrences matched.
	rows, err := srv.db.Query(c, `
		SELECT t.date
		FROM recurring_attachments ra
		JOIN transactions t ON t.id = ra.transaction_id
		WHERE ra.series_id = $1 AND ra.user_id = $2`, id, userID)
	if err != nil {
		slog.Error("GetRecurringForecast (attachments)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	attachedTxns := []recurringAttachedTxn{}
	for rows.Next() {
		var a recurringAttachedTxn
		if err := rows.Scan(&a.date); err != nil {
			rows.Close()
			slog.Error("GetRecurringForecast scan", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		attachedTxns = append(attachedTxns, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		slog.Error("GetRecurringForecast rows", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	terms, err := srv.loadRecurringTerms(c, id, userID)
	if err != nil {
		slog.Error("GetRecurringForecast (terms)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	deriveRecurringSeries(&series, terms)

	occurrences := recurringUpcoming(series, dateOnly(time.Now()), count)
	items := make([]models.RecurringForecastItem, 0, len(occurrences))
	for _, occ := range occurrences {
		// Each occurrence uses the amount in effect on its date, so a price
		// change only affects occurrences on/after the change.
		amount := series.Amount
		if t := recurringTermAt(terms, occ); t != nil {
			amount = t.Amount
		}
		items = append(items, models.RecurringForecastItem{
			Date:    occ,
			Amount:  amount,
			Type:    series.Type,
			Matched: recurringOccurrenceMatched(occ, attachedTxns, recurringMaxDaysOff(series)),
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

// recurringAttachedTxn is the minimal attached-transaction view used to mark
// forecast occurrences as matched.
type recurringAttachedTxn struct {
	date time.Time
}

// recurringOccurrenceMatched reports whether any attached transaction falls
// within windowDays of the occurrence. The window is the same tolerance the
// suggestion filter uses (recurringMaxDaysOff), so the API never proposes a
// link its own forecast would report as unmatched.
func recurringOccurrenceMatched(occ time.Time, attached []recurringAttachedTxn, windowDays float64) bool {
	for _, a := range attached {
		diffDays := math.Abs(dateOnly(a.date).Sub(dateOnly(occ)).Hours() / 24)
		if diffDays <= windowDays {
			return true
		}
	}
	return false
}

// GetRecurringSuggestions scores existing transactions that likely satisfy a
// series' occurrences and returns them highest-score first. Only transactions
// on the same account, of the same type, with an exactly matching amount, and
// near an expected occurrence are considered. Nothing is linked here — the
// caller confirms links via AttachRecurring.
func (srv *Server) GetRecurringSuggestions(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}
	userID := auth.GetUserID(c)
	series, err := srv.loadRecurringSeries(c, id, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "recurring series not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("GetRecurringSuggestions (load)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	limit := clampQueryInt(c, "limit", defaultRecurringSuggestionLimit, 1, maxRecurringSuggestionLimit)

	// Candidates must match the amount and account in effect on their date.
	// Prefilter on the union of every (account, amount) the series has used,
	// then enforce the exact per-date range below.
	terms, err := srv.loadRecurringTerms(c, id, userID)
	if err != nil {
		slog.Error("GetRecurringSuggestions (terms)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	deriveRecurringSeries(&series, terms)
	if len(terms) == 0 {
		// Defensive: a series always has at least its initial term.
		terms = []models.RecurringSeriesTerm{{Amount: series.Amount, AccountID: series.AccountID}}
	}
	accountSet := make(map[uuid.UUID]struct{}, len(terms))
	amountSet := make(map[int64]struct{}, len(terms))
	for _, t := range terms {
		accountSet[t.AccountID] = struct{}{}
		amountSet[t.Amount.Abs().Cents()] = struct{}{}
	}
	accountIDs := make([]uuid.UUID, 0, len(accountSet))
	for a := range accountSet {
		accountIDs = append(accountIDs, a)
	}
	amounts := make([]int64, 0, len(amountSet))
	for a := range amountSet {
		amounts = append(amounts, a)
	}

	// Span every range: from the earliest start to the latest (exclusive) end,
	// or unbounded when any range is open-ended. This is what lets older ranges
	// surface — the query is no longer limited to the most recent transactions.
	minStart := dateOnly(terms[0].StartDate)
	var maxEnd *time.Time
	openEnded := false
	for _, t := range terms {
		if s := dateOnly(t.StartDate); s.Before(minStart) {
			minStart = s
		}
		if t.EndDate == nil {
			openEnded = true
		} else {
			e := dateOnly(*t.EndDate)
			if maxEnd == nil || e.After(*maxEnd) {
				ec := e
				maxEnd = &ec
			}
		}
	}
	if openEnded {
		maxEnd = nil
	}

	rows, err := srv.db.Query(c, `
		SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type,
		       t.category_id, t.payee_id, COALESCE(a.name, '')
		FROM transactions t
		JOIN accounts a ON t.account_id = a.id
		WHERE t.user_id = $1
		  AND t.account_id = ANY($2::uuid[])
		  AND t.type = $3
		  AND t.amount = ANY($4::bigint[])
		  AND t.date >= $5
		  AND ($6::date IS NULL OR t.date < $6)
		  AND NOT EXISTS (SELECT 1 FROM recurring_attachments ra WHERE ra.transaction_id = t.id)
		ORDER BY `+txnOrderByDate(false)+`
		LIMIT $7`,
		userID, accountIDs, series.Type, amounts, minStart, maxEnd, maxRecurringCandidates)
	if err != nil {
		slog.Error("GetRecurringSuggestions", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type scoredSuggestion struct {
		suggestion models.RecurringSuggestion
		rangeID    uuid.UUID
	}
	scored := []scoredSuggestion{}
	for rows.Next() {
		var txn models.Transaction
		if err := rows.Scan(&txn.ID, &txn.AccountID, &txn.Date, &txn.Description, &txn.Amount,
			&txn.Type, &txn.CategoryID, &txn.PayeeID, &txn.AccountName); err != nil {
			slog.Error("GetRecurringSuggestions scan", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		// The transaction must fall inside a range and match its account and
		// amount, so each historical range suggests its own transactions.
		active := recurringTermContaining(terms, txn.Date)
		if active == nil || txn.AccountID != active.AccountID || txn.Amount != active.Amount {
			continue
		}
		occ, daysOff, ok := nearestRecurringOccurrence(series, txn.Date)
		if !ok || daysOff > recurringMaxDaysOff(series) {
			continue
		}
		scored = append(scored, scoredSuggestion{
			suggestion: models.RecurringSuggestion{
				Txn:            txn,
				Score:          calculateRecurringScore(series, txn, daysOff),
				OccurrenceDate: occ,
				DaysOff:        daysOff,
			},
			rangeID: active.ID,
		})
	}
	if err := rows.Err(); err != nil {
		slog.Error("GetRecurringSuggestions rows", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Rank by score, newest occurrence first on ties.
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].suggestion.Score != scored[j].suggestion.Score {
			return scored[i].suggestion.Score > scored[j].suggestion.Score
		}
		return scored[i].suggestion.OccurrenceDate.After(scored[j].suggestion.OccurrenceDate)
	})

	// Build the page: at most one candidate per expected occurrence, and — so
	// an older range can never be crowded out by a newer one — at least the
	// best candidate from every range that has one. Remaining slots fill by
	// score.
	suggestions := []models.RecurringSuggestion{}
	seenOccurrence := map[int64]bool{}
	seenRange := map[uuid.UUID]bool{}
	add := func(sc scoredSuggestion) {
		key := dateOnly(sc.suggestion.OccurrenceDate).Unix()
		if seenOccurrence[key] {
			return
		}
		seenOccurrence[key] = true
		seenRange[sc.rangeID] = true
		suggestions = append(suggestions, sc.suggestion)
	}
	for _, sc := range scored {
		if len(suggestions) >= limit {
			break
		}
		if !seenRange[sc.rangeID] {
			add(sc)
		}
	}
	for _, sc := range scored {
		if len(suggestions) >= limit {
			break
		}
		add(sc)
	}

	c.JSON(http.StatusOK, gin.H{"data": suggestions})
}

// GetRecurringTransactions lists the transactions attached to a series.
func (srv *Server) GetRecurringTransactions(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}
	userID := auth.GetUserID(c)
	if _, err := srv.loadRecurringSeries(c, id, userID); errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "recurring series not found", http.StatusNotFound)
		return
	} else if err != nil {
		slog.Error("GetRecurringTransactions (load)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	rows, err := srv.db.Query(c, `
		SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type, t.category_id,
		       t.tags, t.notes, t.payee_id, COALESCE(p.name, ''), t.created_at, a.name,
		       COALESCE(c.name, ''), COALESCE(c.icon, ''), COALESCE(c.color, ''),
		       t.billing_cycle_id, COALESCE(bc.label, '')
		FROM recurring_attachments ra
		JOIN transactions t ON t.id = ra.transaction_id
		JOIN accounts a ON t.account_id = a.id
		LEFT JOIN categories c ON t.category_id = c.id
		LEFT JOIN payees p ON t.payee_id = p.id
		LEFT JOIN billing_cycles bc ON t.billing_cycle_id = bc.id
		WHERE ra.series_id = $1 AND ra.user_id = $2
		ORDER BY `+txnOrderByDate(false), id, userID)
	if err != nil {
		slog.Error("GetRecurringTransactions", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	transactions := []models.Transaction{}
	for rows.Next() {
		var t models.Transaction
		if err := rows.Scan(&t.ID, &t.AccountID, &t.Date, &t.Description, &t.Amount, &t.Type,
			&t.CategoryID, &t.Tags, &t.Notes, &t.PayeeID, &t.Payee, &t.CreatedAt, &t.AccountName,
			&t.CategoryName, &t.CategoryIcon, &t.CategoryColor, &t.BillingCycleID, &t.BillingCycleLabel); err != nil {
			slog.Error("GetRecurringTransactions scan", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		transactions = append(transactions, t)
	}
	if err := rows.Err(); err != nil {
		slog.Error("GetRecurringTransactions rows", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": transactions})
}

// AttachRecurring links a batch of transactions to one recurring series. A
// transaction may belong to at most one series; already-attached rows produce a
// 409 so the user can detach first (mirrors the loan attachment semantics).
func (srv *Server) AttachRecurring(c *gin.Context) {
	var req models.RecurringAttachRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}
	if len(req.TransactionIDs) == 0 {
		validation.RespondError(c, "no transaction ids provided", http.StatusBadRequest)
		return
	}
	if len(req.TransactionIDs) > maxBulkBatch {
		validation.RespondError(c, fmt.Sprintf("too many transaction ids (max %d per request)", maxBulkBatch), http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)

	var seriesOwned bool
	if err := srv.db.QueryRow(c,
		"SELECT EXISTS(SELECT 1 FROM recurring_series WHERE id = $1 AND user_id = $2)",
		req.SeriesID, userID).Scan(&seriesOwned); err != nil {
		slog.Error("AttachRecurring (checking series)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if !seriesOwned {
		validation.RespondError(c, "recurring series not found", http.StatusNotFound)
		return
	}

	var owned int
	if err := srv.db.QueryRow(c,
		"SELECT COUNT(*) FROM transactions WHERE id = ANY($1) AND user_id = $2",
		req.TransactionIDs, userID).Scan(&owned); err != nil {
		slog.Error("AttachRecurring (checking transactions)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if owned != len(req.TransactionIDs) {
		validation.RespondError(c, "one or more transactions not found", http.StatusBadRequest)
		return
	}

	var already int
	if err := srv.db.QueryRow(c,
		"SELECT COUNT(*) FROM recurring_attachments WHERE transaction_id = ANY($1) AND user_id = $2",
		req.TransactionIDs, userID).Scan(&already); err != nil {
		slog.Error("AttachRecurring (checking existing attachments)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if already > 0 {
		validation.RespondError(c,
			fmt.Sprintf("%d of the selected transactions are already linked to a recurring series — detach them first", already),
			http.StatusConflict)
		return
	}

	res, err := srv.db.Exec(c,
		`INSERT INTO recurring_attachments (series_id, transaction_id, user_id)
		 SELECT $1, t, $3 FROM unnest($2::uuid[]) AS t`,
		req.SeriesID, req.TransactionIDs, userID)
	if err != nil {
		// Race guard: a concurrent attach can still hit the unique index.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			validation.RespondError(c, "one or more transactions are already linked to a recurring series", http.StatusConflict)
			return
		}
		slog.Error("AttachRecurring (insert)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"attached": res.RowsAffected()})
}

// DetachRecurring unlinks a batch of transactions from whatever recurring
// series they are attached to.
func (srv *Server) DetachRecurring(c *gin.Context) {
	var req models.RecurringDetachRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}
	if len(req.TransactionIDs) == 0 {
		validation.RespondError(c, "no transaction ids provided", http.StatusBadRequest)
		return
	}
	if len(req.TransactionIDs) > maxBulkBatch {
		validation.RespondError(c, fmt.Sprintf("too many transaction ids (max %d per request)", maxBulkBatch), http.StatusBadRequest)
		return
	}

	res, err := srv.db.Exec(c,
		"DELETE FROM recurring_attachments WHERE transaction_id = ANY($1) AND user_id = $2",
		req.TransactionIDs, auth.GetUserID(c))
	if err != nil {
		slog.Error("DetachRecurring", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusOK, gin.H{"detached": res.RowsAffected()})
}
