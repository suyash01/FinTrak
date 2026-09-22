package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/fintrak/client/api"
)

func init() {
	registerScreen(70, func(ctx *Ctx) Screen { return NewRules(ctx) })
}

// ruleMatchTypes are the match types the API accepts. The handler rejects an
// unknown value with a 400 and never fires a rule whose match type it does not
// recognize, so the form offers exactly this set and nothing else.
var ruleMatchTypes = []Option{
	{Value: "contains", Label: "contains"},
	{Value: "starts_with", Label: "starts_with"},
	{Value: "exact", Label: "exact"},
}

// Rules manages the categorization rules: a priority-ordered list with inline
// create/edit/delete, a dry run that counts what the cursor rule would catch, and
// the batch apply. Two API behaviours shape the screen: the list is already
// ordered by descending priority (that order is the precedence, so the screen
// never re-sorts it), and every rule only ever touches transactions that are
// still uncategorized and sit on an open account.
type Rules struct {
	ctx   *Ctx
	table Table

	rules []api.Rule
	// applied counts what the last apply updated. The apply closure runs off the
	// event loop, so it records the count before its done message is delivered,
	// which is the only way to report a number that is unknown until the call
	// returns (act carries a fixed note).
	applied int64

	keys ruleKeys
}

// ruleKeys are the screen's bindings.
type ruleKeys struct {
	New     key.Binding
	Edit    key.Binding
	Delete  key.Binding
	Preview key.Binding
	Apply   key.Binding
}

func newRuleKeys() ruleKeys {
	return ruleKeys{
		New:     key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new")),
		Edit:    key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
		Delete:  key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		Preview: key.NewBinding(key.WithKeys("p", "enter"), key.WithHelp("p", "preview matches")),
		Apply:   key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "apply all rules")),
	}
}

// NewRules builds the rules screen.
func NewRules(ctx *Ctx) *Rules {
	r := &Rules{ctx: ctx, keys: newRuleKeys()}
	r.table.SetColumns(
		Column{Title: "Prio", Width: 5, Align: AlignRight},
		Column{Title: "Match", Width: 12},
		Column{Title: "Pattern"},
		Column{Title: "Category", Width: 18},
		Column{Title: "Payee", Width: 16},
		Column{Title: "Conditions"},
	)
	return r
}

// Title implements Screen.
func (r *Rules) Title() string { return "Rules" }

// Keys implements Screen.
func (r *Rules) Keys() []key.Binding {
	return []key.Binding{r.keys.New, r.keys.Edit, r.keys.Delete, r.keys.Preview, r.keys.Apply}
}

// CapturesText implements Screen: this screen has no inline text input.
func (r *Rules) CapturesText() bool { return false }

// Refresh implements Screen.
func (r *Rules) Refresh() tea.Cmd { return r.reload() }

// reload fetches the rules, which the API returns highest priority first.
func (r *Rules) reload() tea.Cmd {
	return load("rules.list", func(ctx context.Context) ([]api.Rule, error) {
		return r.ctx.Client.ListRules(ctx)
	})
}

// Update implements Screen.
func (r *Rules) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case loaded[[]api.Rule]:
		if m.tag != "rules.list" {
			break
		}
		if m.err != nil {
			r.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		r.rules = m.data
		r.applyRows()
		return nil

	case loaded[rulePreviewResult]:
		if m.tag != "rules.preview" {
			break
		}
		if m.err != nil {
			r.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		r.ctx.Open(NewInfo("Rule preview", wrapText(rulePreviewReport(m.data), 56)).
			WithFooter("Read-only: a preview writes nothing, and it builds the same predicate an apply uses."))
		return nil

	case done:
		if !strings.HasPrefix(m.tag, "rules.") {
			break
		}
		if m.err != nil {
			// The App already reported the failure; a rejected save keeps its
			// form open with the error inside it.
			return nil
		}
		if m.tag == "rules.apply" {
			r.ctx.Open(NewInfo("Apply rules", wrapText(ruleApplyReport(r.applied), 56)).
				WithFooter("Only uncategorized transactions are touched."))
		}
		return r.reload()

	case tea.KeyMsg:
		return r.handleKey(m)
	}
	return nil
}

// handleKey routes a key press.
func (r *Rules) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch {
	case keyMatches(r.keys.New, msg):
		r.openRuleForm(nil)
		return nil
	case keyMatches(r.keys.Edit, msg):
		if rule, ok := r.current(); ok {
			r.openRuleForm(&rule)
		}
		return nil
	case keyMatches(r.keys.Delete, msg):
		r.confirmDelete()
		return nil
	case keyMatches(r.keys.Preview, msg):
		return r.previewCurrent()
	case keyMatches(r.keys.Apply, msg):
		r.confirmApply()
		return nil
	}

	switch msg.String() {
	case "up", "k":
		r.table.Move(-1)
	case "down", "j":
		r.table.Move(1)
	case "pgup", "ctrl+b":
		r.table.Page(-1, 20)
	case "pgdown", "ctrl+f":
		r.table.Page(1, 20)
	case "home", "g":
		r.table.Home()
	case "end", "G":
		r.table.End()
	}
	return nil
}

// applyRows rebuilds the table from the fetched rules. The joined category and
// payee names the list endpoint returns are preferred over a cache lookup, which
// keeps a rule readable even when its payee is filtered out of the reference
// data.
func (r *Rules) applyRows() {
	ref := r.ctx.Ref
	rows := make([][]Cell, 0, len(r.rules))
	for _, rule := range r.rules {
		conditions := Muted("—")
		if summary := ruleConditionSummary(rule, ref); summary != "" {
			conditions = Text(summary)
		}
		rows = append(rows, []Cell{
			Text(strconv.Itoa(rule.Priority)),
			Muted(defaultTo(rule.MatchType, "contains")),
			Text(rule.Pattern),
			Text(defaultTo(rule.CategoryName, ref.CategoryName(rule.CategoryID))),
			Muted(defaultTo(rule.Payee, ref.PayeeName(derefID(rule.PayeeID)))),
			conditions,
		})
	}
	r.table.SetRows(rows)
}

// current returns the selected rule.
func (r *Rules) current() (api.Rule, bool) {
	index := r.table.Cursor()
	if index < 0 || index >= len(r.rules) {
		return api.Rule{}, false
	}
	return r.rules[index], true
}

// openRuleForm opens the create or edit form. A nil rule creates; both paths use
// the same field set because the API's update body is an alias of the create
// body, so an edit reloads the whole rule rather than patching single columns.
func (r *Rules) openRuleForm(rule *api.Rule) {
	ref := r.ctx.Ref
	creating := rule == nil
	title := "New rule"
	current := api.Rule{}
	if rule != nil {
		title = "Edit rule"
		current = *rule
	}

	categories := ref.CategoryOptions()
	if len(categories) == 0 {
		r.ctx.Notify(LevelError, "create a category first — every rule files transactions under one")
		return
	}

	matchType := Field{
		Label:   "Match type",
		Kind:    FieldSelect,
		Value:   defaultTo(current.MatchType, "contains"),
		Options: ruleMatchTypes,
		Help:    "how the pattern is compared to the description",
	}
	account := SelectField("Account", derefID(current.AccountID), ref.AccountOptions(), false)
	account.Help = "only match transactions on this account"
	filterCategory := SelectField("Txn category", derefID(current.FilterCategoryID), categories, false)
	filterCategory.Help = "only match transactions already filed under this category"
	filterPayee := SelectField("Txn payee", derefID(current.FilterPayeeID), ref.PayeeOptions(), false)
	filterPayee.Help = "only match transactions already carrying this payee"
	txnType := SelectField("Txn type", current.TxnType, []Option{
		{Value: "debit", Label: "debit"},
		{Value: "credit", Label: "credit"},
	}, false)
	txnType.ClearLabel = "any"
	linked := SelectField("Linked", linkedState(current.IsLinked), ruleTriState("linked", "not linked"), false)
	linked.ClearLabel = "any"
	linked.Help = "only match transactions that are (or are not) linked to another"
	recurring := SelectField("Recurring", linkedState(current.IsRecurring), ruleTriState("recurring", "not recurring"), false)
	recurring.ClearLabel = "any"
	recurring.Help = "only match transactions attached to a recurring series"

	fields := []Field{
		{Label: "Pattern", Kind: FieldText, Value: current.Pattern, Width: 40, Validate: required("pattern"), Help: "compared to the transaction description"},
		matchType,
		SelectField("Category", current.CategoryID, categories, true),
		{Label: "Priority", Kind: FieldText, Value: strconv.Itoa(current.Priority), Width: 6, Validate: positiveInt, Help: "the highest matching priority wins"},
		SelectField("Payee", derefID(current.PayeeID), ref.PayeeOptions(), false),
		{Label: "Tags to add", Kind: FieldText, Value: strings.Join(current.AddTags, ","), Width: 30, Help: "comma separated, added to the transaction"},
		{Label: "Notes", Kind: FieldText, Value: current.Notes, Width: 40, Help: "appended to the transaction's notes"},
		account,
		filterCategory,
		filterPayee,
		AmountField("Min amount", ruleAmountValue(current.MinAmount)),
		AmountField("Max amount", ruleAmountValue(current.MaxAmount)),
		txnType,
		{Label: "Date from", Kind: FieldText, Value: derefID(current.DateFrom), Width: 14, Validate: optionalDate},
		{Label: "Date to", Kind: FieldText, Value: derefID(current.DateTo), Width: 14, Validate: optionalDate},
		linked,
		recurring,
	}

	r.ctx.Open(NewForm("rules.save", title, fields, func(f *Form) tea.Cmd {
		values, err := ruleFormValuesFrom(f)
		if err != nil {
			f.SetError(err)
			return nil
		}
		if creating {
			return act("rules.save", "rule created", false, func(ctx context.Context) error {
				_, err := r.ctx.Client.CreateRule(ctx, values.request())
				return err
			})
		}
		id := current.ID
		return act("rules.save", "rule updated", false, func(ctx context.Context) error {
			// UpdateRuleRequest is an alias of CreateRuleRequest, so the same
			// body serves both calls.
			_, err := r.ctx.Client.UpdateRule(ctx, id, values.request())
			return err
		})
	}))
}

// ruleFormValues carries the form's contents across the async boundary, so the
// request is built from a snapshot rather than from live input buffers.
type ruleFormValues struct {
	pattern          string
	matchType        string
	categoryID       string
	priority         int
	payeeID          string
	accountID        string
	filterCategoryID string
	filterPayeeID    string
	minAmount        *api.Amount
	maxAmount        *api.Amount
	txnType          string
	dateFrom         string
	dateTo           string
	isLinked         *bool
	isRecurring      *bool
	addTags          []string
	notes            string
}

// ruleFormValuesFrom reads and validates the form.
func ruleFormValuesFrom(f *Form) (ruleFormValues, error) {
	minAmount, err := ruleOptionalAmount(f.Value("Min amount"))
	if err != nil {
		return ruleFormValues{}, err
	}
	maxAmount, err := ruleOptionalAmount(f.Value("Max amount"))
	if err != nil {
		return ruleFormValues{}, err
	}
	return ruleFormValues{
		pattern:          strings.TrimSpace(f.Value("Pattern")),
		matchType:        f.Value("Match type"),
		categoryID:       f.Value("Category"),
		priority:         f.IntValue("Priority"),
		payeeID:          f.Value("Payee"),
		accountID:        f.Value("Account"),
		filterCategoryID: f.Value("Txn category"),
		filterPayeeID:    f.Value("Txn payee"),
		minAmount:        minAmount,
		maxAmount:        maxAmount,
		txnType:          f.Value("Txn type"),
		dateFrom:         strings.TrimSpace(f.Value("Date from")),
		dateTo:           strings.TrimSpace(f.Value("Date to")),
		isLinked:         parseLinked(f.Value("Linked")),
		isRecurring:      parseLinked(f.Value("Recurring")),
		addTags:          splitList(f.Value("Tags to add")),
		notes:            strings.TrimSpace(f.Value("Notes")),
	}, nil
}

// request builds the body shared by create and update. An empty optional field
// is sent as an explicit null: that is what clears the column on an update and
// what leaves the condition unset on a create.
func (v ruleFormValues) request() api.CreateRuleRequest {
	return api.CreateRuleRequest{
		Pattern:          v.pattern,
		MatchType:        v.matchType,
		CategoryID:       v.categoryID,
		Priority:         v.priority,
		PayeeID:          ruleOptionalString(v.payeeID),
		AccountID:        ruleOptionalString(v.accountID),
		FilterCategoryID: ruleOptionalString(v.filterCategoryID),
		FilterPayeeID:    ruleOptionalString(v.filterPayeeID),
		MinAmount:        v.minAmount,
		MaxAmount:        v.maxAmount,
		TxnType:          v.txnType,
		DateFrom:         v.dateFrom,
		DateTo:           v.dateTo,
		IsLinked:         v.isLinked,
		IsRecurring:      v.isRecurring,
		AddTags:          v.addTags,
		Notes:            v.notes,
	}
}

// confirmDelete deletes the cursor rule.
func (r *Rules) confirmDelete() {
	rule, ok := r.current()
	if !ok {
		r.ctx.Notify(LevelError, "no rule selected")
		return
	}
	r.ctx.Open(NewConfirm("Delete rule", fmt.Sprintf("Delete the %s rule %q?", defaultTo(rule.MatchType, "contains"), rule.Pattern), true, func() tea.Cmd {
		return act("rules.delete", "rule deleted", false, func(ctx context.Context) error {
			return r.ctx.Client.DeleteRule(ctx, rule.ID)
		})
	}).WithDetail(
		"Transactions this rule already categorized keep their category.",
		"Apply rules again to re-file anything it had matched.",
	))
}

// rulePreviewResult pairs a matched count with the rule it belongs to: the
// request is asynchronous, so the cursor may have moved on by the time the count
// arrives and the report still has to name the right rule.
type rulePreviewResult struct {
	label   string
	matched int
}

// previewCurrent dry-runs the cursor rule. The API evaluates the same predicate
// the apply builds, so the count is exactly what an apply would change — and it
// writes nothing.
func (r *Rules) previewCurrent() tea.Cmd {
	rule, ok := r.current()
	if !ok {
		r.ctx.Notify(LevelError, "no rule selected")
		return nil
	}
	req := ruleRequestFor(rule)
	label := fmt.Sprintf("%s %q", defaultTo(rule.MatchType, "contains"), rule.Pattern)
	return load("rules.preview", func(ctx context.Context) (rulePreviewResult, error) {
		preview, err := r.ctx.Client.PreviewRule(ctx, req)
		if err != nil {
			return rulePreviewResult{}, err
		}
		return rulePreviewResult{label: label, matched: preview.Matched}, nil
	})
}

// ruleRequestFor rebuilds a create body from a stored rule, which is what the
// preview endpoint needs in order to reuse the predicate an apply would build.
func ruleRequestFor(rule api.Rule) api.CreateRuleRequest {
	return api.CreateRuleRequest{
		Pattern:          rule.Pattern,
		MatchType:        rule.MatchType,
		CategoryID:       rule.CategoryID,
		Priority:         rule.Priority,
		PayeeID:          rule.PayeeID,
		AccountID:        rule.AccountID,
		FilterCategoryID: rule.FilterCategoryID,
		FilterPayeeID:    rule.FilterPayeeID,
		MinAmount:        rule.MinAmount,
		MaxAmount:        rule.MaxAmount,
		TxnType:          rule.TxnType,
		DateFrom:         derefID(rule.DateFrom),
		DateTo:           derefID(rule.DateTo),
		IsLinked:         rule.IsLinked,
		IsRecurring:      rule.IsRecurring,
		AddTags:          rule.AddTags,
		Notes:            rule.Notes,
	}
}

// confirmApply runs every rule over the user's uncategorized transactions.
func (r *Rules) confirmApply() {
	if len(r.rules) == 0 {
		r.ctx.Notify(LevelError, "there are no rules to apply")
		return
	}
	r.ctx.Open(NewConfirm("Apply rules",
		fmt.Sprintf("Apply all %s?", pluralise(len(r.rules), "rule", "rules")),
		false, func() tea.Cmd {
			return act("rules.apply", "rules applied", false, func(ctx context.Context) error {
				updated, err := r.ctx.Client.ApplyRules(ctx)
				r.applied = updated
				return err
			})
		}).WithDetail(
		"Only the user's uncategorized transactions are touched.",
		"The highest-priority matching rule wins each transaction.",
		"Transactions on closed accounts are skipped.",
	))
}

// rulePreviewReport states what a dry run found.
func rulePreviewReport(result rulePreviewResult) string {
	return fmt.Sprintf("%s would match rule %s.\n\n"+
		"Only uncategorized transactions are counted, and transactions on closed "+
		"accounts are excluded.",
		pluralise(result.matched, "uncategorized transaction", "uncategorized transactions"),
		result.label)
}

// ruleApplyReport describes what an apply did, in the terms the rules engine
// actually works in: the category_id IS NULL guard means a transaction is
// categorized by exactly one (the highest-priority matching) rule, and closed
// accounts are immutable so their transactions are never re-filed.
func ruleApplyReport(updated int64) string {
	return fmt.Sprintf("%s updated.\n\n"+
		"Rules ran against your uncategorized transactions only. Every rule is "+
		"considered in priority order, and the highest-priority match wins: once a "+
		"rule has categorized a transaction, a lower-priority rule can no longer "+
		"touch it.\n\n"+
		"Transactions on closed accounts are skipped — a closed account is "+
		"immutable and may only be linked.",
		pluralise(int(updated), "transaction", "transactions"))
}

// ruleConditionSummary renders a rule's optional conditions on one line. The
// description match is already its own column, so only the extra ANDed
// conditions appear here; an empty summary means the rule fires on the pattern
// alone.
func ruleConditionSummary(rule api.Rule, ref *RefData) string {
	var bits []string
	switch {
	case rule.MinAmount != nil && rule.MaxAmount != nil:
		bits = append(bits, "amount "+rule.MinAmount.Display()+"…"+rule.MaxAmount.Display())
	case rule.MinAmount != nil:
		bits = append(bits, "amount ≥ "+rule.MinAmount.Display())
	case rule.MaxAmount != nil:
		bits = append(bits, "amount ≤ "+rule.MaxAmount.Display())
	}
	if rule.TxnType != "" {
		bits = append(bits, "type="+rule.TxnType)
	}
	if rule.AccountID != nil {
		bits = append(bits, "account="+defaultTo(rule.AccountName, ref.AccountName(*rule.AccountID)))
	}
	if rule.FilterCategoryID != nil {
		bits = append(bits, "category="+defaultTo(rule.FilterCatName, ref.CategoryName(*rule.FilterCategoryID)))
	}
	if rule.FilterPayeeID != nil {
		bits = append(bits, "payee="+defaultTo(rule.FilterPayeeName, ref.PayeeName(*rule.FilterPayeeID)))
	}
	if rule.DateFrom != nil || rule.DateTo != nil {
		bits = append(bits, "dates "+defaultTo(derefID(rule.DateFrom), "…")+"…"+defaultTo(derefID(rule.DateTo), "…"))
	}
	if rule.IsLinked != nil {
		bits = append(bits, ruleTriStateLabel("linked", "not linked", *rule.IsLinked))
	}
	if rule.IsRecurring != nil {
		bits = append(bits, ruleTriStateLabel("recurring", "not recurring", *rule.IsRecurring))
	}
	return strings.Join(bits, " · ")
}

// ruleTriState builds the two options of a tri-state condition select. The
// select's clear entry leaves the condition unset, which the API reads as "do
// not filter on this column".
func ruleTriState(yes, no string) []Option {
	return []Option{{Value: "yes", Label: yes}, {Value: "no", Label: no}}
}

// ruleTriStateLabel renders a set tri-state condition.
func ruleTriStateLabel(yes, no string, value bool) string {
	if value {
		return yes
	}
	return no
}

// ruleOptionalString maps a blank form value to the API's explicit null.
func ruleOptionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

// ruleOptionalAmount parses an optional amount condition, leaving it unset when
// the field is blank so the condition is omitted rather than compared to zero.
func ruleOptionalAmount(value string) (*api.Amount, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	amount, err := api.ParseAmount(value)
	if err != nil {
		return nil, err
	}
	return &amount, nil
}

// ruleAmountValue renders an optional amount as a form value.
func ruleAmountValue(amount *api.Amount) string {
	if amount == nil {
		return ""
	}
	return amount.String()
}

// View implements Screen.
func (r *Rules) View(width, height int) string {
	body := r.table.View(r.ctx.Theme, width, listRows(height), "no rules yet — press n to add one")
	parts := []string{r.headerLine(width), body, ""}
	if detail := r.detailLine(width); detail != "" {
		parts = append(parts, detail)
	}
	parts = append(parts, "n new · e edit · d delete · p preview · A apply rules")
	return trimToBox(strings.Join(parts, "\n"), width, height)
}

// headerLine explains the evaluation order of the list.
func (r *Rules) headerLine(width int) string {
	summary := pluralise(len(r.rules), "rule", "rules") + " · highest priority wins · applied to uncategorized transactions"
	return r.ctx.Theme.Title.Render(truncate(summary, width))
}

// detailLine describes the cursor rule's actions beyond the category it files
// transactions under.
func (r *Rules) detailLine(width int) string {
	rule, ok := r.current()
	if !ok {
		return ""
	}
	parts := []string{"id " + truncateID(rule.ID), "priority " + strconv.Itoa(rule.Priority)}
	if rule.PayeeID != nil {
		parts = append(parts, "sets payee "+defaultTo(rule.Payee, r.ctx.Ref.PayeeName(*rule.PayeeID)))
	}
	if len(rule.AddTags) > 0 {
		parts = append(parts, "adds tags "+strings.Join(rule.AddTags, ","))
	}
	if rule.Notes != "" {
		parts = append(parts, "notes "+rule.Notes)
	}
	return r.ctx.Theme.Subtle.Render(truncate(strings.Join(parts, " · "), width))
}
