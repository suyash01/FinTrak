package api

import "context"

// Rules (backend/handlers/rule.go).

// ListRules returns the user's categorization rules, highest priority first,
// with the joined category/payee/account/filter names populated.
func (c *Client) ListRules(ctx context.Context) ([]Rule, error) {
	return do[[]Rule](ctx, c, get("/rules"))
}

// CreateRule creates a categorization rule and returns the stored rule. An
// empty MatchType defaults to "contains"; a rule may reference only accounts,
// categories and payees the user owns.
func (c *Client) CreateRule(ctx context.Context, req CreateRuleRequest) (Rule, error) {
	return do[Rule](ctx, c, post("/rules").withJSON(req))
}

// UpdateRule replaces a rule's fields and returns the stored rule. It reports
// 404 for a rule the user does not own and enforces ownership of any
// account/category/payee the rule references.
func (c *Client) UpdateRule(ctx context.Context, id string, req UpdateRuleRequest) (Rule, error) {
	return do[Rule](ctx, c, put("/rules/"+pathEscape(id)).withJSON(req))
}

// DeleteRule removes one of the user's rules.
func (c *Client) DeleteRule(ctx context.Context, id string) error {
	_, err := do[MessageResult](ctx, c, del("/rules/"+pathEscape(id)))
	return err
}

// ApplyRules re-categorizes the user's uncategorized transactions by their
// highest-priority matching rule, applying that rule's category, payee, tags
// and note exactly once per transaction; a transaction can be claimed by only
// one rule. Transactions on closed accounts are left untouched (they are
// immutable and may only be linked). It returns the number of transactions
// updated and applies atomically, so a mid-batch failure commits nothing.
func (c *Client) ApplyRules(ctx context.Context) (int64, error) {
	res, err := do[UpdatedResult](ctx, c, post("/rules/apply"))
	return res.Updated, err
}

// PreviewRule reports how many of the user's currently-uncategorized
// transactions a hypothetical rule would categorize, powering a live
// "N transactions match" hint for a new rule or a rule edit. It writes
// nothing. It builds the same predicate ApplyRules uses, so the preview and a
// real apply agree.
func (c *Client) PreviewRule(ctx context.Context, req CreateRuleRequest) (RulePreview, error) {
	return do[RulePreview](ctx, c, post("/rules/preview").withJSON(req))
}
