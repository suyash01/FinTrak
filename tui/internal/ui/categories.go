package ui

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/fintrak/client/api"
)

func init() {
	registerScreen(40, func(ctx *Ctx) Screen { return NewCategories(ctx) })
}

// catPageRows is how far pgup/pgdown move the cursor in either pane.
const catPageRows = 20

// catPane selects which of the screen's two tables is on screen.
type catPane int

// Panes.
const (
	paneGroups catPane = iota
	paneCategories
)

// groupSlugRe mirrors the API's own rule for caller-chosen group ids. The
// server validates the slug again, but a value it would reject is worth
// catching in the form: a rejected create costs the round trip and the form.
var groupSlugRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,49}$`)

// validGroupSlug validates a user-chosen group id.
func validGroupSlug(value string) error {
	if !groupSlugRe.MatchString(strings.TrimSpace(value)) {
		return errText("lowercase letter first, then lowercase letters, digits or underscores (max 50)")
	}
	return nil
}

// Categories is the taxonomy editor: a groups pane and a categories pane over
// the shared reference cache, plus the admin-only tools for the global catalog
// every user sees.
//
// Both panes render from ctx.Ref instead of fetching their own lists: the App
// owns that cache and reloads it after any mutation that reports invalidate, so
// a rename made here or on another screen shows up without a second copy to
// keep in step. The rows are therefore derived from the cache on every render
// rather than snapshotted when the screen is first reached.
type Categories struct {
	ctx    *Ctx
	pane   catPane
	groups Table
	cats   Table

	// catalog remembers the last admin catalog report so deleting a global
	// category can quote the shared transaction count before the API reports
	// the real figures in its result.
	catalog api.AdminCatalog
	// pending is the category whose delete confirmation is waiting on the
	// impact probe.
	pending catDelete

	keys catKeys
}

// catDelete names the category a pending confirmation will remove.
type catDelete struct {
	id     string
	name   string
	global bool
}

// categoryImpact is how much history a category delete rewrites. It is measured
// before the prompt because the API reports the figures only after the call,
// and a confirmation that warns about uncategorizing transactions is worth far
// more when it says how many.
type categoryImpact struct {
	transactions int
	rules        int
}

// catKeys are the screen's bindings. The admin keys are only listed for an
// admin; every other key works in whichever pane is active.
type catKeys struct {
	Switch       key.Binding
	New          key.Binding
	Edit         key.Binding
	Delete       key.Binding
	Catalog      key.Binding
	NewGroup     key.Binding
	NewCategory  key.Binding
	EditGlobal   key.Binding
	DeleteGlobal key.Binding
}

func newCatKeys() catKeys {
	return catKeys{
		Switch:       key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "switch pane")),
		New:          key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new")),
		Edit:         key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
		Delete:       key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		Catalog:      key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "global catalog")),
		NewGroup:     key.NewBinding(key.WithKeys("N"), key.WithHelp("N", "new global group")),
		NewCategory:  key.NewBinding(key.WithKeys("C"), key.WithHelp("C", "new global category")),
		EditGlobal:   key.NewBinding(key.WithKeys("E"), key.WithHelp("E", "edit global category")),
		DeleteGlobal: key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "delete global category")),
	}
}

// NewCategories builds the taxonomy screen.
func NewCategories(ctx *Ctx) *Categories {
	s := &Categories{ctx: ctx, keys: newCatKeys()}
	s.groups.SetColumns(
		Column{Title: "Group"},
		Column{Title: "Id", Width: 24},
		Column{Title: "Scope", Width: 14},
		Column{Title: "Sort", Width: 5, Align: AlignRight},
	)
	s.cats.SetColumns(
		Column{Title: "Category"},
		Column{Title: "Group", Width: 20},
		Column{Title: "Scope", Width: 10},
	)
	return s
}

// Title implements Screen.
func (s *Categories) Title() string { return "Categories" }

// Keys implements Screen. The global-catalog keys are admin-only, and the two
// category-scoped ones are listed only while the categories pane is active,
// since that is the pane holding the row they act on.
func (s *Categories) Keys() []key.Binding {
	out := []key.Binding{s.keys.Switch, s.keys.New, s.keys.Edit, s.keys.Delete}
	if s.admin() {
		out = append(out, s.keys.Catalog, s.keys.NewGroup, s.keys.NewCategory)
		if s.pane == paneCategories {
			out = append(out, s.keys.EditGlobal, s.keys.DeleteGlobal)
		}
	}
	return out
}

// Refresh implements Screen. Nothing is fetched here: the lists live in the
// shared cache, so refreshing is rebuilding the rows from it.
func (s *Categories) Refresh() tea.Cmd {
	s.syncRows()
	return nil
}

// admin reports whether the signed-in user may touch the shared global catalog.
func (s *Categories) admin() bool { return s.ctx.User.Role == "admin" }

// syncRows rebuilds both tables from the shared cache, so a change the App
// fetched behind this screen's back can never be rendered stale.
func (s *Categories) syncRows() {
	s.groups.SetRows(groupRows(s.ctx.Ref.Groups))
	s.cats.SetRows(categoryRows(s.ctx.Ref.Categories, s.ctx.Ref))
}

// active returns the table the cursor keys move.
func (s *Categories) active() *Table {
	if s.pane == paneCategories {
		return &s.cats
	}
	return &s.groups
}

// switchPane flips between the two tables. Each keeps its own cursor, so a
// detour through the other pane does not lose the user's place.
func (s *Categories) switchPane() {
	if s.pane == paneGroups {
		s.pane = paneCategories
		return
	}
	s.pane = paneGroups
}

// currentGroup returns the selected group.
func (s *Categories) currentGroup() (api.CategoryGroup, bool) {
	index := s.groups.Cursor()
	if index < 0 || index >= len(s.ctx.Ref.Groups) {
		return api.CategoryGroup{}, false
	}
	return s.ctx.Ref.Groups[index], true
}

// currentCategory returns the selected category.
func (s *Categories) currentCategory() (api.Category, bool) {
	index := s.cats.Cursor()
	if index < 0 || index >= len(s.ctx.Ref.Categories) {
		return api.Category{}, false
	}
	return s.ctx.Ref.Categories[index], true
}

// Update implements Screen.
func (s *Categories) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case loaded[api.AdminCatalog]:
		if m.tag != "categories.catalog" {
			break
		}
		if m.err != nil {
			s.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		s.catalog = m.data
		s.ctx.Open(NewInfo("Global catalog", catalogReport(m.data)).
			WithFooter("N new global group · C new global category · E edit the selected global category · D delete it"))
		return nil

	case loaded[categoryImpact]:
		if m.tag != "categories.impact" {
			break
		}
		// A failed probe must not block the delete: the figures are a warning,
		// and the API reports the real ones in the result either way.
		s.openCategoryDeleteConfirm(m.data, m.err)
		return nil

	case done:
		if !strings.HasPrefix(m.tag, "categories.") {
			break
		}
		// The App is re-fetching the cache on invalidate; rebuild now for the
		// mutation that did not invalidate, and let the App's refresh settle
		// the rest.
		s.syncRows()
		return nil

	case tea.KeyMsg:
		return s.handleKey(m)
	}
	return nil
}

// handleKey routes a key press to the active pane.
func (s *Categories) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch {
	case keyMatches(s.keys.Switch, msg):
		s.switchPane()
		return nil
	case keyMatches(s.keys.New, msg):
		return s.openNew()
	case keyMatches(s.keys.Edit, msg):
		return s.openEdit()
	case keyMatches(s.keys.Delete, msg):
		return s.confirmDelete()
	case s.admin() && keyMatches(s.keys.Catalog, msg):
		return load("categories.catalog", func(ctx context.Context) (api.AdminCatalog, error) {
			return s.ctx.Client.AdminCatalog(ctx)
		})
	case s.admin() && keyMatches(s.keys.NewGroup, msg):
		s.openGlobalGroupForm()
		return nil
	case s.admin() && keyMatches(s.keys.NewCategory, msg):
		s.openGlobalCategoryForm()
		return nil
	case s.admin() && keyMatches(s.keys.EditGlobal, msg):
		s.openGlobalCategoryEdit()
		return nil
	case s.admin() && keyMatches(s.keys.DeleteGlobal, msg):
		s.confirmDeleteGlobal()
		return nil
	}

	switch msg.String() {
	case "up", "k":
		s.active().Move(-1)
	case "down", "j":
		s.active().Move(1)
	case "pgup", "ctrl+b":
		s.active().Page(-1, catPageRows)
	case "pgdown", "ctrl+f":
		s.active().Page(1, catPageRows)
	case "home":
		s.active().Home()
	case "end", "G":
		s.active().End()
	}
	return nil
}

// openNew opens the create form for the active pane.
func (s *Categories) openNew() tea.Cmd {
	if s.pane == paneGroups {
		s.openGroupForm()
		return nil
	}
	s.openCategoryForm()
	return nil
}

// openEdit opens the edit form for the active pane's cursor row. Base and
// global rows are refused here because the API keeps them immutable and only an
// admin may rewrite the shared catalog.
func (s *Categories) openEdit() tea.Cmd {
	if s.pane == paneGroups {
		group, ok := s.currentGroup()
		if !ok {
			return nil
		}
		if group.IsBase || group.IsGlobal {
			s.ctx.Notify(LevelError, "%q is a base/global group — the API keeps those immutable", group.Name)
			return nil
		}
		s.openGroupEdit(group)
		return nil
	}

	category, ok := s.currentCategory()
	if !ok {
		return nil
	}
	if category.IsGlobal {
		if s.admin() {
			s.openGlobalCategoryEdit()
			return nil
		}
		s.ctx.Notify(LevelError, "%q is a global category — it is read-only for you", category.Name)
		return nil
	}
	s.openCategoryEdit(category)
	return nil
}

// confirmDelete opens the confirmation for the active pane's cursor row.
func (s *Categories) confirmDelete() tea.Cmd {
	if s.pane == paneGroups {
		group, ok := s.currentGroup()
		if !ok {
			return nil
		}
		s.confirmGroupDelete(group)
		return nil
	}

	category, ok := s.currentCategory()
	if !ok {
		return nil
	}
	if category.IsGlobal {
		s.ctx.Notify(LevelError, "%q is a global category — deleting it is an admin action", category.Name)
		return nil
	}
	return s.probeCategoryImpact(category)
}

// openGroupForm creates a user-owned group. The id is caller-chosen and is the
// group's primary key, so it cannot be changed later.
func (s *Categories) openGroupForm() {
	fields := []Field{
		{Label: "Id", Kind: FieldText, Width: 24, Placeholder: "subscriptions", Validate: validGroupSlug, Help: "slug, the group's permanent id; a duplicate is rejected"},
		{Label: "Name", Kind: FieldText, Width: 32, Validate: required("name")},
		{Label: "Icon", Kind: FieldText, Width: 16, Help: "optional"},
		{Label: "Color", Kind: FieldText, Width: 16, Help: "optional, a trailing label colour"},
	}
	s.ctx.Open(NewForm("categories.group.create", "New group", fields, func(f *Form) tea.Cmd {
		req := api.CreateCategoryGroupRequest{
			ID:    strings.TrimSpace(f.Value("Id")),
			Name:  f.Value("Name"),
			Icon:  f.Value("Icon"),
			Color: f.Value("Color"),
		}
		return act("categories.group.create", "group "+req.ID+" created", true, func(ctx context.Context) error {
			_, err := s.ctx.Client.CreateGroup(ctx, req)
			return err
		})
	}))
}

// openGroupEdit renames or restyles a user-owned group.
func (s *Categories) openGroupEdit(group api.CategoryGroup) {
	fields := []Field{
		{Label: "Name", Kind: FieldText, Value: group.Name, Width: 32, Validate: required("name")},
		{Label: "Icon", Kind: FieldText, Value: group.Icon, Width: 16, Help: "optional; leaving it blank keeps the current icon"},
		{Label: "Color", Kind: FieldText, Value: group.Color, Width: 16, Help: "optional; leaving it blank keeps the current colour"},
	}
	s.ctx.Open(NewForm("categories.group.update", "Edit group "+group.ID, fields, func(f *Form) tea.Cmd {
		req := api.UpdateCategoryGroupRequest{Name: f.Value("Name"), Icon: f.Value("Icon"), Color: f.Value("Color")}
		return act("categories.group.update", "group "+group.ID+" updated", true, func(ctx context.Context) error {
			_, err := s.ctx.Client.UpdateGroup(ctx, group.ID, req)
			return err
		})
	}))
}

// confirmGroupDelete asks before removing a group. The API refuses a group that
// still holds categories or is base/global, and that refusal is surfaced as-is
// rather than pre-empted here, which is why the body names both conditions.
func (s *Categories) confirmGroupDelete(group api.CategoryGroup) {
	s.ctx.Open(NewConfirm("Delete group", fmt.Sprintf("Delete group %q?", group.Name), true, func() tea.Cmd {
		return act("categories.group.delete", "group "+group.ID+" deleted", true, func(ctx context.Context) error {
			return s.ctx.Client.DeleteGroup(ctx, group.ID)
		})
	}).WithDetail(
		"The API refuses the delete while the group still has categories — move or delete them first.",
		"Base and global groups are immutable and are never deleted here.",
	))
}

// openCategoryForm creates a category in a group the user may file under.
func (s *Categories) openCategoryForm() {
	groups := s.ctx.Ref.GroupOptions()
	if len(groups) == 0 {
		s.ctx.Notify(LevelError, "no category groups exist to file a category under")
		return
	}
	fields := []Field{
		{Label: "Name", Kind: FieldText, Width: 32, Validate: required("name")},
		SelectField("Group", groups[0].Value, groups, true),
		{Label: "Icon", Kind: FieldText, Width: 16, Help: "optional"},
		{Label: "Color", Kind: FieldText, Width: 16, Help: "optional, a trailing label colour"},
	}
	s.ctx.Open(NewForm("categories.category.create", "New category", fields, func(f *Form) tea.Cmd {
		req := api.CreateCategoryRequest{
			Name:    f.Value("Name"),
			Icon:    f.Value("Icon"),
			Color:   f.Value("Color"),
			GroupID: f.Value("Group"),
		}
		return act("categories.category.create", "category "+req.Name+" created", true, func(ctx context.Context) error {
			_, err := s.ctx.Client.CreateCategory(ctx, req)
			return err
		})
	}))
}

// openCategoryEdit edits a user-owned category, including moving it to another
// group.
func (s *Categories) openCategoryEdit(category api.Category) {
	fields := []Field{
		{Label: "Name", Kind: FieldText, Value: category.Name, Width: 32, Validate: required("name")},
		SelectField("Group", defaultTo(category.GroupID, firstGroupValue(s.ctx.Ref.GroupOptions())), s.ctx.Ref.GroupOptions(), true),
		{Label: "Icon", Kind: FieldText, Value: category.Icon, Width: 16, Help: "optional; leaving it blank keeps the current icon"},
		{Label: "Color", Kind: FieldText, Value: category.Color, Width: 16, Help: "optional; leaving it blank keeps the current colour"},
	}
	s.ctx.Open(NewForm("categories.category.update", "Edit category "+category.Name, fields, func(f *Form) tea.Cmd {
		req := api.UpdateCategoryRequest{
			Name:    f.Value("Name"),
			Icon:    f.Value("Icon"),
			Color:   f.Value("Color"),
			GroupID: f.Value("Group"),
		}
		return act("categories.category.update", "category "+category.Name+" updated", true, func(ctx context.Context) error {
			_, err := s.ctx.Client.UpdateCategory(ctx, category.ID, req)
			return err
		})
	}))
}

// probeCategoryImpact measures what deleting a category will clear, so the
// confirmation can quote real numbers. Both calls are reads over the same rows
// the API's delete touches: the category filter counts the user's transactions
// with it, and the rule list is the set the delete removes from.
func (s *Categories) probeCategoryImpact(category api.Category) tea.Cmd {
	s.pending = catDelete{id: category.ID, name: category.Name}
	return load("categories.impact", func(ctx context.Context) (categoryImpact, error) {
		page, err := s.ctx.Client.ListTransactions(ctx, api.TransactionFilter{CategoryID: category.ID, Limit: 1})
		if err != nil {
			return categoryImpact{}, err
		}
		rules, err := s.ctx.Client.ListRules(ctx)
		if err != nil {
			return categoryImpact{}, err
		}
		impact := categoryImpact{transactions: page.Total}
		for _, rule := range rules {
			if rule.CategoryID == category.ID {
				impact.rules++
			}
		}
		return impact, nil
	})
}

// openCategoryDeleteConfirm shows the prompt with the measured blast radius.
func (s *Categories) openCategoryDeleteConfirm(impact categoryImpact, probeErr error) {
	target := s.pending
	body := fmt.Sprintf("Delete category %q?", target.name)
	confirm := NewConfirm("Delete category", body, true, func() tea.Cmd {
		return s.deleteCategoryCmd(target)
	})
	if probeErr != nil {
		// The probe failed, so the warning stays general rather than claiming
		// nothing is affected.
		confirm = confirm.WithDetail("The API reports no figures for this one, but deleting a category hands its transactions back to Uncategorized.")
	} else {
		confirm = confirm.WithDetail(fmt.Sprintf("This also uncategorizes %s and removes %s.",
			pluralise(impact.transactions, "transaction", "transactions"),
			pluralise(impact.rules, "rule", "rules")))
	}
	s.ctx.Open(confirm)
}

// deleteCategoryCmd removes a category. act takes a fixed note, and how much the
// delete cleared is only known from its result, so the done message is built
// here: same contract, richer note.
func (s *Categories) deleteCategoryCmd(target catDelete) tea.Cmd {
	return func() tea.Msg {
		result, err := s.ctx.Client.DeleteCategory(context.Background(), target.id)
		if err != nil {
			return done{tag: "categories.category.delete", err: err}
		}
		return done{
			tag:        "categories.category.delete",
			note:       clearedNote(target.name, result),
			invalidate: true,
		}
	}
}

// clearedNote reports what removing a category cleared, straight from the API's
// result.
func clearedNote(name string, result api.DeleteCategoryResult) string {
	return fmt.Sprintf("deleted %s — uncategorized %s, removed %s",
		name,
		pluralise(result.ClearedTransactions, "transaction", "transactions"),
		pluralise(result.DeletedRules, "rule", "rules"))
}

// openGlobalGroupForm creates a group in the shared catalog, visible to every
// user. The id shares one namespace with the user-owned groups, so a collision
// is rejected by the API.
func (s *Categories) openGlobalGroupForm() {
	fields := []Field{
		{Label: "Id", Kind: FieldText, Width: 24, Placeholder: "housing", Validate: validGroupSlug, Help: "slug, the group's permanent id"},
		{Label: "Name", Kind: FieldText, Width: 32, Validate: required("name")},
		{Label: "Icon", Kind: FieldText, Width: 16, Help: "optional"},
		{Label: "Color", Kind: FieldText, Width: 16, Help: "optional"},
	}
	s.ctx.Open(NewForm("categories.global.group", "New global group", fields, func(f *Form) tea.Cmd {
		req := api.CreateCategoryGroupRequest{
			ID:    strings.TrimSpace(f.Value("Id")),
			Name:  f.Value("Name"),
			Icon:  f.Value("Icon"),
			Color: f.Value("Color"),
		}
		return act("categories.global.group", "global group "+req.ID+" created", true, func(ctx context.Context) error {
			_, err := s.ctx.Client.CreateGlobalGroup(ctx, req)
			return err
		})
	}))
}

// openGlobalCategoryForm creates a category shared by every user; it can only
// live in a global group.
func (s *Categories) openGlobalCategoryForm() {
	groups := s.globalGroupOptions()
	if len(groups) == 0 {
		s.ctx.Notify(LevelError, "no global group exists to file a global category under")
		return
	}
	fields := []Field{
		{Label: "Name", Kind: FieldText, Width: 32, Validate: required("name")},
		SelectField("Group", groups[0].Value, groups, true),
		{Label: "Icon", Kind: FieldText, Width: 16, Help: "optional"},
		{Label: "Color", Kind: FieldText, Width: 16, Help: "optional"},
	}
	s.ctx.Open(NewForm("categories.global.category.create", "New global category", fields, func(f *Form) tea.Cmd {
		req := api.CreateCategoryRequest{
			Name:    f.Value("Name"),
			Icon:    f.Value("Icon"),
			Color:   f.Value("Color"),
			GroupID: f.Value("Group"),
		}
		return act("categories.global.category.create", "global category "+req.Name+" created", true, func(ctx context.Context) error {
			_, err := s.ctx.Client.CreateGlobalCategory(ctx, req)
			return err
		})
	}))
}

// openGlobalCategoryEdit edits a global category in place.
func (s *Categories) openGlobalCategoryEdit() {
	category, ok := s.currentCategory()
	if !ok {
		return
	}
	if !category.IsGlobal {
		s.ctx.Notify(LevelError, "%q is your own category — use e to edit it", category.Name)
		return
	}
	groups := s.globalGroupOptions()
	fields := []Field{
		{Label: "Name", Kind: FieldText, Value: category.Name, Width: 32, Validate: required("name")},
		SelectField("Group", defaultTo(category.GroupID, firstGroupValue(groups)), groups, true),
		{Label: "Icon", Kind: FieldText, Value: category.Icon, Width: 16, Help: "optional; leaving it blank keeps the current icon"},
		{Label: "Color", Kind: FieldText, Value: category.Color, Width: 16, Help: "optional; leaving it blank keeps the current colour"},
	}
	s.ctx.Open(NewForm("categories.global.category.update", "Edit global category "+category.Name, fields, func(f *Form) tea.Cmd {
		req := api.UpdateCategoryRequest{
			Name:    f.Value("Name"),
			Icon:    f.Value("Icon"),
			Color:   f.Value("Color"),
			GroupID: f.Value("Group"),
		}
		return act("categories.global.category.update", "global category "+category.Name+" updated", true, func(ctx context.Context) error {
			_, err := s.ctx.Client.UpdateGlobalCategory(ctx, category.ID, req)
			return err
		})
	}))
}

// confirmDeleteGlobal asks before removing a category from the shared catalog,
// warning that the cleanup reaches beyond this user's data.
func (s *Categories) confirmDeleteGlobal() {
	category, ok := s.currentCategory()
	if !ok {
		return
	}
	if !category.IsGlobal {
		s.ctx.Notify(LevelError, "%q is your own category — use d to delete it", category.Name)
		return
	}
	detail := "This clears the category on every user's transactions and deletes the rules that reference it."
	if count, ok := s.catalogTransactionCount(category.ID); ok {
		detail = fmt.Sprintf("This clears the category on %s across every user and deletes the rules that reference it.",
			pluralise(count, "transaction", "transactions"))
	}
	s.ctx.Open(NewConfirm("Delete global category", fmt.Sprintf("Delete global category %q for every user?", category.Name), true, func() tea.Cmd {
		return s.deleteGlobalCategoryCmd(category)
	}).WithDetail(detail))
}

// deleteGlobalCategoryCmd removes a global category, folding the API's cleanup
// figures into the success note the same way the user-owned delete does.
func (s *Categories) deleteGlobalCategoryCmd(category api.Category) tea.Cmd {
	return func() tea.Msg {
		result, err := s.ctx.Client.DeleteGlobalCategory(context.Background(), category.ID)
		if err != nil {
			return done{tag: "categories.global.category.delete", err: err}
		}
		return done{
			tag:        "categories.global.category.delete",
			note:       clearedNote(category.Name, result),
			invalidate: true,
		}
	}
}

// catalogTransactionCount reports the shared catalog's transaction count for a
// global category, when the catalog has been fetched this session.
func (s *Categories) catalogTransactionCount(id string) (int, bool) {
	for _, entry := range s.catalog.Categories {
		if entry.ID == id {
			return entry.TransactionCount, true
		}
	}
	return 0, false
}

// globalGroupOptions lists the groups a global category may live in, which are
// the groups with no owner.
func (s *Categories) globalGroupOptions() []Option {
	out := make([]Option, 0, len(s.ctx.Ref.Groups))
	for _, group := range s.ctx.Ref.Groups {
		if !group.IsGlobal {
			continue
		}
		label := group.Name
		if group.IsBase {
			label += " · base"
		}
		out = append(out, Option{Value: group.ID, Label: label})
	}
	return out
}

// firstGroupValue is the value a select starts on when a row has no group.
func firstGroupValue(options []Option) string {
	if len(options) == 0 {
		return ""
	}
	return options[0].Value
}

// groupRows renders the groups pane.
func groupRows(groups []api.CategoryGroup) [][]Cell {
	rows := make([][]Cell, 0, len(groups))
	for _, group := range groups {
		name := Text(group.Name)
		if group.IsBase || group.IsGlobal {
			// Immutable rows read as de-emphasised, so the pane shows at a
			// glance what the user may actually edit.
			name = Muted(group.Name)
		}
		rows = append(rows, []Cell{
			name,
			Text(group.ID),
			Muted(groupScope(group)),
			Muted(fmt.Sprintf("%d", group.SortOrder)),
		})
	}
	return rows
}

// categoryRows renders the categories pane.
func categoryRows(categories []api.Category, ref *RefData) [][]Cell {
	rows := make([][]Cell, 0, len(categories))
	for _, category := range categories {
		rows = append(rows, []Cell{
			Text(category.Name),
			Text(defaultTo(category.GroupName, ref.GroupName(category.GroupID))),
			Muted(categoryScope(category)),
		})
	}
	return rows
}

// groupScope labels who owns a group.
func groupScope(group api.CategoryGroup) string {
	switch {
	case group.IsBase && group.IsGlobal:
		return "base · global"
	case group.IsBase:
		return "base"
	case group.IsGlobal:
		return "global"
	}
	return "custom"
}

// categoryScope labels who owns a category.
func categoryScope(category api.Category) string {
	if category.IsGlobal {
		return "global"
	}
	return "custom"
}

// catalogReport renders the admin catalog: the global groups with their
// category counts and the global categories with their transaction counts, so
// an admin can see what a shared entry is used by before changing it.
func catalogReport(catalog api.AdminCatalog) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Global groups (%d)\n", len(catalog.Groups)))
	if len(catalog.Groups) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, group := range catalog.Groups {
		scope := ""
		if group.IsBase {
			scope = "  base"
		}
		b.WriteString(fmt.Sprintf("  %-26s %-22s %-14s%s\n", group.Name, group.ID, pluralise(group.CategoryCount, "category", "categories"), scope))
	}

	b.WriteString(fmt.Sprintf("\nGlobal categories (%d)\n", len(catalog.Categories)))
	if len(catalog.Categories) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, category := range catalog.Categories {
		b.WriteString(fmt.Sprintf("  %-26s %-18s %s\n",
			category.Name,
			defaultTo(category.GroupName, category.GroupID),
			pluralise(category.TransactionCount, "transaction", "transactions")))
	}
	return b.String()
}

// View implements Screen.
func (s *Categories) View(width, height int) string {
	// The cache can be reloaded while this screen is not the visible one, so
	// the rows are rebuilt here rather than only on activation.
	s.syncRows()

	th := s.ctx.Theme
	body := s.active().View(th, width, listRows(height), s.emptyMessage())

	parts := []string{s.headerLine(width), body}
	if detail := s.detailLine(width); detail != "" {
		parts = append(parts, detail)
	}
	parts = append(parts, th.Subtle.Render(truncate(s.hintLine(), width)))
	return trimToBox(strings.Join(parts, "\n"), width, height)
}

// headerLine names the panes and the size of each list.
func (s *Categories) headerLine(width int) string {
	th := s.ctx.Theme
	summary := fmt.Sprintf("%s · %s",
		pluralise(len(s.ctx.Ref.Groups), "group", "groups"),
		pluralise(len(s.ctx.Ref.Categories), "category", "categories"))
	line := s.paneLabel(paneGroups) + " " + s.paneLabel(paneCategories) + "  " + th.Subtle.Render(summary)
	if s.admin() {
		line += "  " + th.WarnText.Render("admin")
	}
	return truncate(line, width)
}

// paneLabel renders one pane tab, marking the active one.
func (s *Categories) paneLabel(pane catPane) string {
	label := "Groups"
	if pane == paneCategories {
		label = "Categories"
	}
	if s.pane == pane {
		return s.ctx.Theme.Title.Render("▌" + label)
	}
	return s.ctx.Theme.Subtle.Render(" " + label)
}

// detailLine describes the cursor row of the active pane, including the facts
// the table columns have no room for.
func (s *Categories) detailLine(width int) string {
	th := s.ctx.Theme
	var parts []string
	if s.pane == paneGroups {
		group, ok := s.currentGroup()
		if !ok {
			return ""
		}
		parts = append(parts, "id "+group.ID, groupScope(group)+" group")
		parts = append(parts, pluralise(s.groupCategoryCount(group.ID), "category", "categories"))
		parts = append(parts, fmt.Sprintf("sort %d", group.SortOrder))
		if group.Icon != "" {
			parts = append(parts, "icon "+group.Icon)
		}
		if group.Color != "" {
			parts = append(parts, group.Color)
		}
	} else {
		category, ok := s.currentCategory()
		if !ok {
			return ""
		}
		parts = append(parts, "id "+category.ID, "group "+defaultTo(category.GroupName, s.ctx.Ref.GroupName(category.GroupID)))
		parts = append(parts, categoryScope(category)+" category")
		if category.Icon != "" {
			parts = append(parts, "icon "+category.Icon)
		}
		if category.Color != "" {
			parts = append(parts, category.Color)
		}
	}
	return th.Subtle.Render(truncate(strings.Join(parts, " · "), width))
}

// groupCategoryCount counts the categories filed under a group, so the pane can
// warn how much a group delete would have to move first.
func (s *Categories) groupCategoryCount(id string) int {
	count := 0
	for _, category := range s.ctx.Ref.Categories {
		if category.GroupID == id {
			count++
		}
	}
	return count
}

// hintLine spells out what the pane-sensitive keys will act on here.
func (s *Categories) hintLine() string {
	what := "group"
	if s.pane == paneCategories {
		what = "category"
	}
	hint := fmt.Sprintf("p switch pane · n new %s · e edit · d delete", what)
	if s.admin() {
		hint += " · A global catalog"
	}
	return hint
}

// emptyMessage explains an empty pane.
func (s *Categories) emptyMessage() string {
	if s.pane == paneGroups {
		return "no category groups"
	}
	return "no categories — n creates one"
}
