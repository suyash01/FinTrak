package api

import "context"

// Reference data: category groups, categories, the admin global catalog,
// payees, and tags (backend/handlers/category_group.go, category.go,
// admin_catalog.go, payee.go, tag.go).

// Groups.

// ListGroups returns the groups visible to the user: the immutable base/global
// groups first in canonical order, then the user's own custom groups.
func (c *Client) ListGroups(ctx context.Context) ([]CategoryGroup, error) {
	return do[[]CategoryGroup](ctx, c, get("/groups"))
}

// CreateGroup adds a user-owned custom group. ID is the caller-chosen primary
// key (a lowercase letter followed by lowercase letters, digits or underscores,
// at most 50 characters) and must not collide with an existing group. Base and
// global groups are never created here.
func (c *Client) CreateGroup(ctx context.Context, req CreateCategoryGroupRequest) (CategoryGroup, error) {
	return do[CategoryGroup](ctx, c, post("/groups").withJSON(req))
}

// UpdateGroup renames or restyles a user's own custom group. Base and global
// groups are immutable and answer 400; a group the user does not own answers
// 404.
func (c *Client) UpdateGroup(ctx context.Context, id string, req UpdateCategoryGroupRequest) (CategoryGroup, error) {
	return do[CategoryGroup](ctx, c, put("/groups/"+pathEscape(id)).withJSON(req))
}

// DeleteGroup removes a user's own custom group. Base and global groups are
// immutable, and a group that still has categories is refused, so the caller
// must move or delete those categories first.
func (c *Client) DeleteGroup(ctx context.Context, id string) error {
	_, err := do[MessageResult](ctx, c, del("/groups/"+pathEscape(id)))
	return err
}

// Categories.

// ListCategories returns every category visible to the user — their own plus
// the admin-created global ones — in group order (base groups first) and
// alphabetically within each group.
func (c *Client) ListCategories(ctx context.Context) ([]Category, error) {
	return do[[]Category](ctx, c, get("/categories"))
}

// CreateCategory adds a user-owned category. GroupID must name a base/global
// group or one of the user's own custom groups.
func (c *Client) CreateCategory(ctx context.Context, req CreateCategoryRequest) (Category, error) {
	return do[Category](ctx, c, post("/categories").withJSON(req))
}

// UpdateCategory edits a user's own category. Global categories are immutable
// through this route; an admin edits those with UpdateGlobalCategory.
func (c *Client) UpdateCategory(ctx context.Context, id string, req UpdateCategoryRequest) (Category, error) {
	return do[Category](ctx, c, put("/categories/"+pathEscape(id)).withJSON(req))
}

// DeleteCategory removes a user's own category. The delete and its cleanup run
// in one transaction: the result reports how many of the user's transactions
// were left uncategorized and how many auto-categorization rules pointing at
// the category were deleted along with it.
func (c *Client) DeleteCategory(ctx context.Context, id string) (DeleteCategoryResult, error) {
	return do[DeleteCategoryResult](ctx, c, del("/categories/"+pathEscape(id)))
}

// Admin catalog.

// AdminCatalog returns the shared global catalog for the admin console: the
// global groups with their category counts and the global categories with their
// transaction counts, ordered by group. Admin only.
func (c *Client) AdminCatalog(ctx context.Context) (AdminCatalog, error) {
	return do[AdminCatalog](ctx, c, get("/admin/catalog"))
}

// CreateGlobalGroup creates an admin-owned global group visible to every user.
// The id is a slug and must be unique; the seeded base groups themselves cannot
// be created here. Admin only.
func (c *Client) CreateGlobalGroup(ctx context.Context, req CreateCategoryGroupRequest) (CategoryGroup, error) {
	return do[CategoryGroup](ctx, c, post("/admin/groups").withJSON(req))
}

// CreateGlobalCategory creates a global category shared by every user. GroupID
// must name a global group (base or admin-created). Admin only.
func (c *Client) CreateGlobalCategory(ctx context.Context, req CreateCategoryRequest) (Category, error) {
	return do[Category](ctx, c, post("/admin/categories").withJSON(req))
}

// UpdateGlobalCategory edits an admin-created global category. Admin only.
func (c *Client) UpdateGlobalCategory(ctx context.Context, id string, req UpdateCategoryRequest) (Category, error) {
	return do[Category](ctx, c, put("/admin/categories/"+pathEscape(id)).withJSON(req))
}

// DeleteGlobalCategory removes a global category, clearing it across every
// user's transactions and deleting the rules that reference it; the result
// reports both counts. Admin only.
func (c *Client) DeleteGlobalCategory(ctx context.Context, id string) (DeleteCategoryResult, error) {
	return do[DeleteCategoryResult](ctx, c, del("/admin/categories/"+pathEscape(id)))
}

// Payees.

// ListPayees returns the user's payees, alphabetically by name.
func (c *Client) ListPayees(ctx context.Context) ([]Payee, error) {
	return do[[]Payee](ctx, c, get("/payees"))
}

// CreatePayee adds a payee. AccountID optionally links it to one of the user's
// accounts so transfers resolve to that account's payee; a name that is already
// taken answers 409.
func (c *Client) CreatePayee(ctx context.Context, req CreatePayeeRequest) (Payee, error) {
	return do[Payee](ctx, c, post("/payees").withJSON(req))
}

// UpdatePayee renames a payee and/or re-links it to an account. The handler
// binds the same struct as CreatePayee, so both fields are written: a nil
// AccountID unlinks the payee rather than leaving the link alone.
func (c *Client) UpdatePayee(ctx context.Context, id string, req CreatePayeeRequest) (Payee, error) {
	return do[Payee](ctx, c, put("/payees/"+pathEscape(id)).withJSON(req))
}

// DeletePayee removes a payee.
func (c *Client) DeletePayee(ctx context.Context, id string) error {
	_, err := do[MessageResult](ctx, c, del("/payees/"+pathEscape(id)))
	return err
}

// Tags.

// ListTags returns the user's tag vocabulary: every distinct tag with the
// number of transactions carrying it, most-used first. Tags live on
// transactions rather than in a table, so the name is the identity.
func (c *Client) ListTags(ctx context.Context) ([]TagCount, error) {
	res, err := do[DataList[TagCount]](ctx, c, get("/tags"))
	return res.Data, err
}

// RenameTag rewrites every occurrence of one tag to another across the user's
// transactions and reports how many transactions were updated, collapsing any
// duplicate the rename creates. The backend short-circuits and reports 0 when
// from and to are equal, and transactions on closed accounts are skipped
// (closed rows are immutable), so the count may be lower than the caller
// expects.
func (c *Client) RenameTag(ctx context.Context, from, to string) (int64, error) {
	res, err := do[UpdatedResult](ctx, c, post("/tags/rename").withJSON(RenameTagRequest{From: from, To: to}))
	return res.Updated, err
}
