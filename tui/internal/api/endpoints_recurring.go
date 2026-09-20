package api

import "context"

// Recurring series (backend/handlers/recurring.go).
//
// A series is a template for a repeating charge or income: it never creates
// transactions and never links one automatically. The user matches real
// transactions to it by hand (AttachRecurring) after the forecast and the
// suggestion list point out which ones fit.

// ListRecurring returns every series with its derived view: NextDueDate and
// MonthlyAmount (the amount normalized to a monthly figure) plus the joined
// account/category/payee names and AttachedCount.
func (c *Client) ListRecurring(ctx context.Context) ([]RecurringSeries, error) {
	res, err := do[DataList[RecurringSeries]](ctx, c, get("/recurring"))
	return res.Data, err
}

// CreateRecurring adds a series. It is a template only: it creates no
// transactions and links none. Supply either a single account/amount/start date
// or a full Ranges list of effective-dated entries.
func (c *Client) CreateRecurring(ctx context.Context, req CreateRecurringSeriesRequest) (RecurringSeries, error) {
	return do[RecurringSeries](ctx, c, post("/recurring").withJSON(req))
}

// UpdateRecurring applies a partial update and returns the reloaded series. The
// range handling is twofold: a non-empty Ranges replaces the whole range list;
// otherwise an Amount or AccountID change is recorded as a NEW range starting
// at EffectiveDate (default today), leaving earlier ranges in force. Omitted
// fields stay untouched.
func (c *Client) UpdateRecurring(ctx context.Context, id string, req UpdateRecurringSeriesRequest) (RecurringSeries, error) {
	return do[RecurringSeries](ctx, c, put("/recurring/"+pathEscape(id)).withJSON(req))
}

// DeleteRecurring removes a series. Its attachments and terms go with it; the
// transactions themselves are kept and merely unlinked.
func (c *Client) DeleteRecurring(ctx context.Context, id string) error {
	_, err := do[MessageResult](ctx, c, del("/recurring/"+pathEscape(id)))
	return err
}

// RecurringForecast projects the next occurrences of a series. count defaults
// to 12 and is capped at 60 server-side; zero leaves it to the default. Each
// item's Matched means an attached transaction already covers that occurrence.
func (c *Client) RecurringForecast(ctx context.Context, id string, count int) ([]RecurringForecastItem, error) {
	r := get("/recurring/"+pathEscape(id)+"/forecast").setQueryInt("count", count)
	res, err := do[DataList[RecurringForecastItem]](ctx, c, r)
	return res.Data, err
}

// RecurringSuggestions lists transactions the server believes satisfy an
// occurrence, best match first. limit defaults to 100 and is capped at 500;
// zero leaves it to the default. This is read-only: nothing is linked, the
// caller decides what to attach.
func (c *Client) RecurringSuggestions(ctx context.Context, id string, limit int) ([]RecurringSuggestion, error) {
	r := get("/recurring/"+pathEscape(id)+"/suggestions").setQueryInt("limit", limit)
	res, err := do[DataList[RecurringSuggestion]](ctx, c, r)
	return res.Data, err
}

// RecurringTransactions lists the transactions currently attached to a series.
func (c *Client) RecurringTransactions(ctx context.Context, id string) ([]Transaction, error) {
	res, err := do[DataList[Transaction]](ctx, c, get("/recurring/"+pathEscape(id)+"/transactions"))
	return res.Data, err
}

// ListRecurringTerms lists a series' effective-dated amount/account terms. A
// term covers [startDate, endDate) — the end date is exclusive, so adjacent
// terms may share a boundary and an omitted end is open-ended.
func (c *Client) ListRecurringTerms(ctx context.Context, id string) ([]RecurringSeriesTerm, error) {
	res, err := do[DataList[RecurringSeriesTerm]](ctx, c, get("/recurring/"+pathEscape(id)+"/terms"))
	return res.Data, err
}

// AddRecurringTerm records one date-ranged amount/account term. Terms of one
// series must not overlap (400 otherwise); gaps between them are allowed.
func (c *Client) AddRecurringTerm(ctx context.Context, id string, req CreateRecurringSeriesTermRequest) (RecurringSeriesTerm, error) {
	return do[RecurringSeriesTerm](ctx, c, put("/recurring/"+pathEscape(id)+"/terms").withJSON(req))
}

// UpdateRecurringTerm edits a term's range, amount, or account. Omitted fields
// keep their value; an empty EndDate clears the range end. The result must
// still not overlap the series' other terms.
func (c *Client) UpdateRecurringTerm(ctx context.Context, id, termID string, req UpdateRecurringSeriesTermRequest) (RecurringSeriesTerm, error) {
	return do[RecurringSeriesTerm](ctx, c, put("/recurring/"+pathEscape(id)+"/terms/"+pathEscape(termID)).withJSON(req))
}

// DeleteRecurringTerm removes one term, leaving a gap in the series' coverage
// (the series itself is untouched).
func (c *Client) DeleteRecurringTerm(ctx context.Context, id, termID string) error {
	_, err := do[MessageResult](ctx, c, del("/recurring/"+pathEscape(id)+"/terms/"+pathEscape(termID)))
	return err
}

// AttachRecurring links transactions to one series and returns how many were
// linked. A transaction may belong to at most one series, so an already-linked
// transaction yields a 409.
func (c *Client) AttachRecurring(ctx context.Context, seriesID string, transactionIDs []string) (int64, error) {
	body := RecurringAttachRequest{SeriesID: seriesID, TransactionIDs: transactionIDs}
	res, err := do[AttachedResult](ctx, c, post("/recurring/attach").withJSON(body))
	return res.Attached, err
}

// DetachRecurring unlinks transactions from whatever series they carry and
// returns how many were unlinked. A transaction that carried no series is
// simply not counted.
func (c *Client) DetachRecurring(ctx context.Context, transactionIDs []string) (int64, error) {
	body := RecurringDetachRequest{TransactionIDs: transactionIDs}
	res, err := do[DetachedResult](ctx, c, post("/recurring/detach").withJSON(body))
	return res.Detached, err
}
