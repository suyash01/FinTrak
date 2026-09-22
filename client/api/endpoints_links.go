package api

import "context"

// Links (backend/handlers/link.go, link_cycles.go).
//
// A link pairs two of the user's transactions: a transfer between two of their
// own accounts, a refund or cashback against an earlier purchase, or a bill
// payment. The type decides what the backend does beyond storing the pair —
// only "transfer" re-categorizes both transactions and swaps their payees to
// the counterpart account's linked payee, and only "transfer" is undone when
// the link is deleted. A transaction may belong to several links.

// ListLinks returns the user's links, newest first, optionally narrowed to one
// type ("transfer", "cashback", "refund" or "bill_payment") and/or to the links
// touching one transaction. The list is unpaginated and answers with a bare
// array. Every link carries both of its transactions joined in FromTxn/ToTxn
// (with account names), so a caller can render either side without a second
// fetch; those joined transactions are populated on reads only, never on the
// Link returned by CreateLink.
func (c *Client) ListLinks(ctx context.Context, linkType, txnID string) ([]Link, error) {
	r := get("/links").setQuery("type", linkType).setQuery("txnId", txnID)
	return do[[]Link](ctx, c, r)
}

// CreateLink links two transactions and returns the created link. The backend
// rejects an unknown Type, a link of a transaction to itself, a transaction the
// user does not own, and an exact duplicate (409); it answers 201 on success.
// For a "transfer" link both transactions are re-categorized as Transfer and
// their payees are set to the counterpart account's payee.
func (c *Client) CreateLink(ctx context.Context, req CreateLinkRequest) (Link, error) {
	return do[Link](ctx, c, post("/links").withJSON(req))
}

// BulkCreateLinks creates many links in one transaction and returns how many
// were actually created. Exact duplicates already stored (or repeated inside
// the batch) are skipped rather than failing the call, so the count may be
// lower than len(links). Any other invalid entry aborts the whole batch, and
// nothing is written.
func (c *Client) BulkCreateLinks(ctx context.Context, links []CreateLinkRequest) (int, error) {
	body := BulkCreateLinksRequest{Links: links}
	res, err := do[CreatedCountResult](ctx, c, post("/links/bulk").withJSON(body))
	return res.CreatedCount, err
}

// DeleteLink removes one link. Deleting a transfer also clears the
// transfer-derived category and payee from its two transactions, but only while
// no other link still references them; non-transfer links never touched
// category/payee, so removing them leaves the user's own categorization intact.
func (c *Client) DeleteLink(ctx context.Context, id string) error {
	_, err := do[MessageResult](ctx, c, del("/links/"+pathEscape(id)))
	return err
}

// BulkDeleteLinks removes many links at once and returns how many were deleted.
// An empty id list is not an error: the backend answers "nothing to delete"
// with a count of 0. As with DeleteLink, the transfer-derived category/payee is
// cleared from any transaction no remaining link references.
func (c *Client) BulkDeleteLinks(ctx context.Context, ids []string) (int, error) {
	body := BulkDeleteLinksRequest{IDs: ids}
	res, err := do[DeletedCountResult](ctx, c, post("/links/bulk-delete").withJSON(body))
	return res.DeletedCount, err
}

// applyLinkSuggestionPaging writes the page/limit parameters shared by both
// suggestion endpoints. A zero value leaves its parameter out entirely, so the
// server's defaults (page 1, limit 50) apply instead of being sent as 0, which
// the server would treat as invalid and reset anyway.
func applyLinkSuggestionPaging(r *request, page, limit int) *request {
	return r.setQueryInt("page", page).setQueryInt("limit", limit)
}

// TransferSuggestions proposes debit/credit pairs on different accounts that
// look like a transfer, scored by amount match, date proximity and
// transfer-related wording. Page defaults to 1 and limit to 50 server-side and
// is capped at 100; HasMore is true when a further page exists (the response
// carries no total).
func (c *Client) TransferSuggestions(ctx context.Context, page, limit int) (SuggestionPage, error) {
	r := applyLinkSuggestionPaging(get("/links/transfer-suggestions"), page, limit)
	return do[SuggestionPage](ctx, c, r)
}

// CashbackSuggestions proposes credit transactions whose description mentions a
// cashback/reward/refund, each paired with the prior debits on the same account
// that likely funded it; every suggestion carries a fixed score of 70. Paging
// works exactly as for TransferSuggestions.
func (c *Client) CashbackSuggestions(ctx context.Context, page, limit int) (SuggestionPage, error) {
	r := applyLinkSuggestionPaging(get("/links/cashback-suggestions"), page, limit)
	return do[SuggestionPage](ctx, c, r)
}

// LinkCycles reports the account-to-account link flows the Money Flow Sankey
// cannot draw, because the graph has to stay acyclic. Reciprocal pairs (kind
// "reciprocal") are netted into one edge and the back edges that close a longer
// loop (kind "cycle") are dropped; both are returned here with their
// participants, each leg's gross flow in the window, and the cycle's net — the
// smallest leg, i.e. the amount that actually circulates the whole loop.
// OneSidedFlows lists directed account flows with no flow in the opposite
// direction: a genuinely one-way bill payment or refund looks exactly like a
// half-entered transfer, which is why they are surfaced for review rather than
// corrected. An empty dateFrom/dateTo leaves the window unbounded and an empty
// accountID does not filter; a non-uuid accountID is rejected with 400.
func (c *Client) LinkCycles(ctx context.Context, dateFrom, dateTo, accountID string) (LinkCycleReport, error) {
	r := get("/links/cycles").
		setQuery("dateFrom", dateFrom).
		setQuery("dateTo", dateTo).
		setQuery("accountId", accountID)
	return do[LinkCycleReport](ctx, c, r)
}
