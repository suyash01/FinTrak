package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/fintrak/tui/internal/api"
)

func init() {
	registerScreen(60, func(ctx *Ctx) Screen { return NewTags(ctx) })
}

// Message tags. The App forwards every message to every screen, so a screen
// must ignore anything whose tag lacks its prefix.
const (
	tagsListTag   = "tags.list"
	tagsRenameTag = "tags.rename"
)

// tagBarWidth is the usage bar's width in cells. The bar column is sized to
// match, so the table never has to truncate a cell that already carries styling
// — a cut escape sequence would leak the bar's colour into the rest of the row.
const tagBarWidth = 20

// Tags is the tag vocabulary screen. Tags are not a table on the API: the
// vocabulary is derived from transactions.tags, so the name is the identity and
// there is no id, no create and no delete. Every entry is therefore read-only
// metadata plus one pervasive edit — a rename that rewrites every transaction
// carrying the name — which is why this screen is a list of usage counts rather
// than an editable catalog.
type Tags struct {
	ctx   *Ctx
	table Table

	// all is the full vocabulary as the API ordered it (most used first); rows
	// is what the name filter currently shows.
	all  []api.TagCount
	filt string

	// filtBefore is the filter that was applied when the prompt opened, so esc
	// can abandon an edit instead of leaving a half-typed filter behind.
	filtBefore string
	searching  bool
	search     textinput.Model

	// rename carries the answer of the rename in flight back to the event loop.
	rename *renameOutcome

	// bodyHeight is the table area from the last render, which is what paging
	// needs. The layout owns the real height, so the table cannot know it.
	bodyHeight int

	keys tagKeys
}

// tagKeys are the screen's bindings.
type tagKeys struct {
	Search key.Binding
	Rename key.Binding
	Usage  key.Binding
}

func newTagKeys() tagKeys {
	return tagKeys{
		Search: key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter names")),
		Rename: key.NewBinding(key.WithKeys("e", "R"), key.WithHelp("e", "rename everywhere")),
		Usage:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "usage")),
	}
}

// NewTags builds the tag vocabulary screen.
func NewTags(ctx *Ctx) *Tags {
	t := &Tags{ctx: ctx, keys: newTagKeys()}
	t.table.SetColumns(
		Column{Title: "Tag"},
		Column{Title: "Used", Width: 6, Align: AlignRight},
		Column{Title: "Share of most used", Width: tagBarWidth},
	)
	return t
}

// Title implements Screen.
func (t *Tags) Title() string { return "Tags" }

// Keys implements Screen.
func (t *Tags) Keys() []key.Binding {
	return []key.Binding{t.keys.Usage, t.keys.Rename, t.keys.Search}
}

// Refresh implements Screen.
func (t *Tags) Refresh() tea.Cmd { return t.reload() }

// reload fetches the vocabulary. Only the API can count usage — it counts over
// every transaction, including the ones no filter would have loaded — so the
// screen never derives a count from its own rows.
func (t *Tags) reload() tea.Cmd {
	return load(tagsListTag, func(ctx context.Context) ([]api.TagCount, error) {
		return t.ctx.Client.ListTags(ctx)
	})
}

// Update implements Screen.
func (t *Tags) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case loaded[[]api.TagCount]:
		if m.tag != tagsListTag {
			break
		}
		if m.err != nil {
			t.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		t.all = m.data
		t.applyRows()
		return nil

	case done:
		if !strings.HasPrefix(m.tag, "tags.") {
			break
		}
		if m.err != nil {
			// A failed rename is reported inside the open form and never
			// reaches here; anything else leaves the vocabulary unchanged, so
			// there is nothing worth reloading.
			return nil
		}
		if m.tag == tagsRenameTag && t.rename != nil {
			t.reportRename(t.rename)
		}
		return t.reload()

	case tea.KeyMsg:
		return t.handleKey(m)
	}
	return nil
}

// handleKey routes a key press.
func (t *Tags) handleKey(msg tea.KeyMsg) tea.Cmd {
	if t.searching {
		switch msg.String() {
		case "enter":
			// The filter is applied as it is typed, so enter only leaves the
			// prompt.
			t.searching = false
			return nil
		case "esc":
			t.filt = t.filtBefore
			t.searching = false
			t.applyRows()
			return nil
		}
		var cmd tea.Cmd
		t.search, cmd = t.search.Update(msg)
		t.filt = t.search.Value()
		t.applyRows()
		return cmd
	}

	switch {
	case keyMatches(t.keys.Search, msg):
		return t.startSearch()
	case keyMatches(t.keys.Rename, msg):
		if tag, ok := t.current(); ok {
			t.openRenameForm(tag.Name)
		}
		return nil
	case keyMatches(t.keys.Usage, msg):
		if tag, ok := t.current(); ok {
			t.ctx.Open(NewInfo("Tag "+tag.Name, tagUsage(tag, t.maxCount())).
				WithFooter("e rename everywhere · esc close"))
		}
		return nil
	}

	switch msg.String() {
	case "up", "k":
		t.table.Move(-1)
	case "down", "j":
		t.table.Move(1)
	case "pgup", "ctrl+b":
		t.table.Page(-1, VisibleRows(t.bodyHeight))
	case "pgdown", "ctrl+f":
		t.table.Page(1, VisibleRows(t.bodyHeight))
	case "home":
		t.table.Home()
	case "end", "G":
		t.table.End()
	}
	return nil
}

// startSearch opens the inline name filter. The vocabulary is small and fully
// fetched, so filtering is client-side — the API has no tag query to page.
func (t *Tags) startSearch() tea.Cmd {
	t.filtBefore = t.filt
	t.searching = true
	t.search = textinput.New()
	t.search.Placeholder = "filter tag names"
	t.search.Width = 32
	t.search.SetValue(t.filt)
	t.search.Focus()
	return textinput.Blink
}

// applyRows rebuilds the table from the filtered vocabulary.
func (t *Tags) applyRows() {
	largest := t.maxCount()
	th := t.ctx.Theme
	rows := make([][]Cell, 0, len(t.all))
	for _, tag := range t.visible() {
		rows = append(rows, []Cell{
			Text(tag.Name),
			Text(strconv.Itoa(tag.Count)),
			Text(bar(th, countRatio(tag.Count, largest), tagBarWidth)),
		})
	}
	t.table.SetRows(rows)
}

// visible applies the name filter. Matching is case-insensitive because the
// names are typed by hand: a user looking for "groceries" should still find the
// entry spelled "Groceries".
func (t *Tags) visible() []api.TagCount {
	if t.filt == "" {
		return t.all
	}
	needle := strings.ToLower(t.filt)
	out := make([]api.TagCount, 0, len(t.all))
	for _, tag := range t.all {
		if strings.Contains(strings.ToLower(tag.Name), needle) {
			out = append(out, tag)
		}
	}
	return out
}

// maxCount is the largest usage count in the vocabulary.
func (t *Tags) maxCount() int {
	largest := 0
	for _, tag := range t.all {
		if tag.Count > largest {
			largest = tag.Count
		}
	}
	return largest
}

// countRatio is ratioOf's counterpart for plain counts: a tag's usage relative to
// the most-used tag. It cannot go through ratioOf, which takes an api.Amount —
// usage counts are ints, and no money is involved.
func countRatio(count, largest int) float64 {
	if largest <= 0 {
		return 0
	}
	return float64(count) / float64(largest)
}

// current returns the cursor row's tag. The name is the identity, so the entry
// itself is enough to act on; nothing needs an id.
func (t *Tags) current() (api.TagCount, bool) {
	rows := t.visible()
	i := t.table.Cursor()
	if i < 0 || i >= len(rows) {
		return api.TagCount{}, false
	}
	return rows[i], true
}

// renameOutcome carries the API's answer back to the event loop. The mutation
// runs on its own goroutine, so the count is written into this value and read
// only once the matching done message arrives — the message channel is what
// orders the two. Only one rename can be in flight, because the form stays open
// and the modal swallows every key until that done message lands.
type renameOutcome struct {
	from    string
	to      string
	rewrote int64
}

// openRenameForm rewrites one tag everywhere it appears. This is the tag
// equivalent of editing a row: there is nothing to update on the tag itself, so
// the work is a bulk rewrite of transactions, and the count the API returns is
// the only honest report of what happened.
func (t *Tags) openRenameForm(tag string) {
	fields := []Field{
		{Label: "From", Kind: FieldText, Value: tag, Width: 30, Validate: tagName,
			Help: "the tag to rewrite, spelled as it appears on transactions"},
		{Label: "To", Kind: FieldText, Placeholder: "new name", Width: 30, Validate: tagName,
			Help: "the replacement name — occurrences are merged, never duplicated"},
	}
	t.ctx.Open(NewForm(tagsRenameTag, "Rename tag everywhere", fields, func(f *Form) tea.Cmd {
		out := &renameOutcome{
			from: strings.TrimSpace(f.Value("From")),
			to:   strings.TrimSpace(f.Value("To")),
		}
		t.rename = out
		// The note is empty on purpose: the number of rewritten transactions is
		// only known once the API answers, and reportRename fills it in from the
		// done message.
		return act(tagsRenameTag, "", true, func(ctx context.Context) error {
			n, err := t.ctx.Client.RenameTag(ctx, out.from, out.to)
			out.rewrote = n
			return err
		})
	}))
}

// reportRename says what the rename actually did. The count is the API's, not the
// vocabulary's: transactions on closed accounts are skipped (closed rows are
// immutable), so fewer rows can be rewritten than the usage count suggests, and
// the endpoint short-circuits to 0 without touching anything when the two names
// are equal.
func (t *Tags) reportRename(out *renameOutcome) {
	switch {
	case out.from == out.to:
		t.ctx.Notify(LevelInfo, "%q already has that name — nothing to rewrite", out.from)
	case out.rewrote == 0:
		t.ctx.Notify(LevelInfo, "no transaction could be rewritten to %q", out.to)
	default:
		t.ctx.Notify(LevelSuccess, "renamed %q to %q in %s",
			out.from, out.to, pluralise(int(out.rewrote), "transaction", ""))
	}
}

// tagName validates a tag name locally against the server's rules: a tag is
// trimmed free text of at most 50 characters. The blank check is not only
// courtesy — the endpoint answers a blank name with no body at all, which the
// client would surface as a parse failure rather than a message.
func tagName(value string) error {
	tag := strings.TrimSpace(value)
	if tag == "" {
		return errText("a tag name is required")
	}
	if len([]rune(tag)) > 50 {
		return errText("a tag name is at most 50 characters")
	}
	return nil
}

// tagUsage renders the usage overlay: what the count means, why the name is the
// only handle on a tag, and the places a tag can actually change.
func tagUsage(tag api.TagCount, largest int) string {
	var b strings.Builder
	if largest <= 0 {
		largest = tag.Count
	}
	fmt.Fprintf(&b, "Used by       %s\n", pluralise(tag.Count, "transaction", ""))
	fmt.Fprintf(&b, "Share         %.0f%% of the most-used tag (%s)\n",
		countRatio(tag.Count, largest)*100, pluralise(largest, "transaction", ""))

	b.WriteString("\nTags are free text stored on each transaction (transactions.tags). ")
	b.WriteString("There is no tag table and no tag id: the name is the identity, so this ")
	b.WriteString("list is derived from the transactions and an entry disappears as soon as ")
	b.WriteString("the last one stops using it. Nothing here can be created or deleted directly.\n")

	b.WriteString("\nWhat changes a tag\n")
	b.WriteString("  Transactions  select rows and press t to add or remove tags in bulk\n")
	b.WriteString("  A single row  the Tags field in a transaction's edit form\n")
	b.WriteString("  Rules         a rule action applies a tag to every matching transaction\n")
	b.WriteString("  Rename (e)    rewrites every transaction carrying this name at once\n")

	b.WriteString("\nA rename skips transactions on closed accounts, which are immutable, so it ")
	b.WriteString("can rewrite fewer rows than the count above.\n")
	return b.String()
}

// View implements Screen.
func (t *Tags) View(width, height int) string {
	header := t.headerLine(width)
	if t.searching {
		header = t.ctx.Theme.Title.Render("/") + t.search.View()
	}
	t.bodyHeight = height - 4
	body := t.table.View(t.ctx.Theme, width, t.bodyHeight, t.emptyMessage())
	parts := []string{header, body, ""}
	if detail := t.detailLine(width); detail != "" {
		parts = append(parts, detail)
	}
	parts = append(parts, "enter usage · e rename · / filter · esc clear filter")
	return trimToBox(strings.Join(parts, "\n"), width, height)
}

// headerLine summarises the vocabulary and the active name filter, and repeats
// what the table cannot say: these names are derived, not stored.
func (t *Tags) headerLine(width int) string {
	th := t.ctx.Theme
	summary := pluralise(len(t.all), "tag", "") + " in the vocabulary"
	if t.filt != "" {
		summary += fmt.Sprintf(" · %d shown", t.table.Len())
	}
	line := th.Title.Render(summary)
	hint := "free text on transactions — the name is the identity"
	if t.filt != "" {
		hint = "filter " + strconv.Quote(t.filt) + " · " + hint
	}
	return line + "  " + th.Subtle.Render(truncate(hint, max(10, width-len(summary)-4)))
}

// detailLine describes the cursor row with the API's count, not the bar's
// rounded proportions.
func (t *Tags) detailLine(width int) string {
	tag, ok := t.current()
	if !ok {
		return ""
	}
	line := fmt.Sprintf("%s · %s", tag.Name, pluralise(tag.Count, "transaction", ""))
	if largest := t.maxCount(); largest > tag.Count {
		line += fmt.Sprintf(" · %.0f%% of the most-used tag", countRatio(tag.Count, largest)*100)
	}
	return t.ctx.Theme.Subtle.Render(truncate(line, width))
}

// emptyMessage separates "nobody has tagged anything" from "the filter matches
// nothing", which are different problems with different fixes.
func (t *Tags) emptyMessage() string {
	if t.filt != "" {
		return "no tag name matches " + strconv.Quote(t.filt) + " — esc clears the filter"
	}
	return "no tags yet — tags are free text, so this list fills up as transactions are tagged"
}
