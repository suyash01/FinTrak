package ui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/fintrak/client/api"
)

func init() {
	registerScreen(100, func(ctx *Ctx) Screen { return NewMoneyFlow(ctx) })
}

// The flow limits mirror the server's own: the node cap is 12 unless it is
// raised and 30 at most, and the billing-cycle framing spans 12 cycles unless it
// is raised and 60 at most. Knowing them here lets a form reject a value the
// server would only clamp silently, which would leave the "Other" node
// disagreeing with the number the user typed.
const (
	mfDefaultLimit  = 12
	mfMaxLimit      = 30
	mfDefaultCycles = 12
	mfMaxCycles     = 60
)

// Timeline groupings. The billing-cycle view frames the strip around an
// account's statement periods instead of calendar months.
const (
	mfGroupMonth        = "month"
	mfGroupBillingCycle = "billing_cycle"
)

// Rendering budgets: how much of a short terminal each section keeps before the
// graph starts losing rows.
const (
	mfMinGraphLines     = 6
	mfMaxTraceLines     = 5
	mfMaxTimelineLines  = 7
	mfPanelMinWidth     = 78
	mfPanelWidth        = 32
	mfTimelineLabelWide = 10
)

// mfStages is the graph's left-to-right order: the money sources, the accounts
// it lands in, the categories it is spent on, and the payees it reaches. The
// server returns the same staging, so the headings follow it rather than the
// order the nodes happen to arrive in.
var mfStages = []struct {
	kind  string
	label string
}{
	{kind: "income", label: "INCOME"},
	{kind: "account", label: "ACCOUNTS"},
	{kind: "category", label: "CATEGORIES"},
	{kind: "payee", label: "PAYEES"},
}

// MoneyFlow is the money-flow screen: the income → accounts → categories →
// payees graph drawn as one proportional row per node, the edge trace of the
// selected node, the cross-account link rollup the graph deliberately does not
// draw, and the timeline strip that scrubs the graph down to a single period.
type MoneyFlow struct {
	ctx   *Ctx
	table Table

	graph    api.MoneyFlowGraph
	timeline api.MoneyFlowTimeline

	// nodes is the graph's node list flattened into stage order. names maps a
	// node id back to its label, because the edges reference ids only, and
	// rowNode maps a table row to an index in nodes (-1 for a stage heading,
	// which the cursor steps over).
	nodes   []api.MoneyFlowNode
	names   map[string]string
	rowNode []int

	// window is the user's own filter, shared by the graph and the timeline.
	window api.WindowFilter
	// scrub is the timeline period the graph is currently narrowed to. It is
	// kept apart from window so the strip keeps offering every period while the
	// user moves from one to the next.
	scrub *api.WindowFilter

	limit   int
	groupBy string
	cycles  int

	// timelineFocus routes the arrow keys and `enter` to the strip instead of the
	// graph, which is what lets one key scrub what another key traces.
	timelineFocus bool
	period        int
	periodChosen  bool
	offset        int

	// rowWidth is the width the node rows were last built for. They carry a bar,
	// so they are rebuilt whenever the graph's column changes size.
	rowWidth  int
	rowsDirty bool
	loaded    bool

	keys mfKeys
}

// mfKeys are the screen's bindings.
type mfKeys struct {
	Filter   key.Binding
	Limit    key.Binding
	Timeline key.Binding
	GroupBy  key.Binding
	Cycles   key.Binding
	Trace    key.Binding
	Clear    key.Binding
}

func newMFKeys() mfKeys {
	return mfKeys{
		Filter:   key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "window filter")),
		Limit:    key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "node limit")),
		Timeline: key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "timeline focus")),
		GroupBy:  key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "month/cycle")),
		Cycles:   key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "cycles")),
		Trace:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "trace/scrub")),
		Clear:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back/clear scrub")),
	}
}

// NewMoneyFlow builds the money-flow screen.
func NewMoneyFlow(ctx *Ctx) *MoneyFlow {
	return &MoneyFlow{
		ctx:     ctx,
		names:   map[string]string{},
		groupBy: mfGroupMonth,
		cycles:  mfDefaultCycles,
		keys:    newMFKeys(),
	}
}

// Title implements Screen.
func (m *MoneyFlow) Title() string { return "Money Flow" }

// Keys implements Screen.
func (m *MoneyFlow) Keys() []key.Binding {
	if m.timelineFocus {
		return []key.Binding{m.keys.Timeline, m.keys.GroupBy, m.keys.Cycles, m.keys.Trace, m.keys.Clear}
	}
	return []key.Binding{m.keys.Filter, m.keys.Limit, m.keys.Timeline, m.keys.Trace, m.keys.Clear}
}

// Refresh implements Screen. The graph and the strip are independent requests,
// so they are issued together.
func (m *MoneyFlow) Refresh() tea.Cmd {
	return tea.Batch(m.loadGraph(), m.loadTimeline())
}

// graphFilter is the graph request: the user's window, overridden by a scrubbed
// period when one is active, plus the node cap (zero leaves the server default).
func (m *MoneyFlow) graphFilter() api.MoneyFlowFilter {
	window := m.window
	if m.scrub != nil {
		window.DateFrom, window.DateTo = m.scrub.DateFrom, m.scrub.DateTo
	}
	return api.MoneyFlowFilter{WindowFilter: window, Limit: m.limit}
}

// timelineFilter is the strip request. Cycles is only sent for the billing-cycle
// framing, which is the only mode that reads it.
func (m *MoneyFlow) timelineFilter() api.TimelineFilter {
	filter := api.TimelineFilter{WindowFilter: m.window, GroupBy: m.groupBy}
	if m.groupBy == mfGroupBillingCycle {
		filter.Cycles = m.cycles
	}
	return filter
}

// loadGraph fetches the Sankey graph for the current window.
func (m *MoneyFlow) loadGraph() tea.Cmd {
	filter := m.graphFilter()
	return load("moneyflow.graph", func(ctx context.Context) (api.MoneyFlowGraph, error) {
		return m.ctx.Client.MoneyFlow(ctx, filter)
	})
}

// loadTimeline fetches the period strip for the user's window, which the scrub
// deliberately does not narrow.
func (m *MoneyFlow) loadTimeline() tea.Cmd {
	filter := m.timelineFilter()
	return load("moneyflow.timeline", func(ctx context.Context) (api.MoneyFlowTimeline, error) {
		return m.ctx.Client.MoneyFlowTimeline(ctx, filter)
	})
}

// Update implements Screen.
func (m *MoneyFlow) Update(msg tea.Msg) tea.Cmd {
	switch v := msg.(type) {
	case loaded[api.MoneyFlowGraph]:
		if v.tag != "moneyflow.graph" {
			break
		}
		if v.err != nil {
			m.ctx.Notify(LevelError, "%s", v.err)
			return nil
		}
		m.graph = v.data
		m.loaded = true
		m.flatten()
		m.rowsDirty = true
		m.syncRows()
		return nil

	case loaded[api.MoneyFlowTimeline]:
		if v.tag != "moneyflow.timeline" {
			break
		}
		if v.err != nil {
			m.ctx.Notify(LevelError, "%s", v.err)
			return nil
		}
		m.timeline = v.data
		if v.data.GroupBy != "" {
			// The server echoes the framing it actually used, which is the truth
			// to display even if the request asked for something else.
			m.groupBy = v.data.GroupBy
		}
		m.clampPeriod()
		return nil

	case done:
		// Nothing here mutates, so a done carrying our prefix could only come
		// from a modal that grew a write; refreshing both views is the same
		// answer the mutating screens give.
		if !strings.HasPrefix(v.tag, "moneyflow.") {
			break
		}
		if v.err == nil {
			return m.Refresh()
		}
		return nil

	case tea.KeyMsg:
		return m.handleKey(v)
	}
	return nil
}

// handleKey routes a key press, first to the timeline when it holds focus and
// otherwise to the graph.
func (m *MoneyFlow) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch {
	case keyMatches(m.keys.Trace, msg):
		if m.timelineFocus {
			return m.scrubToPeriod()
		}
		if node, ok := m.currentNode(); ok {
			m.ctx.Open(NewInfo("Flow trace · "+node.Name, m.traceBody(node)))
		}
		return nil
	case keyMatches(m.keys.Clear, msg):
		return m.clear()
	case keyMatches(m.keys.Limit, msg):
		m.openLimitForm()
		return nil
	case keyMatches(m.keys.Cycles, msg):
		m.openCyclesForm()
		return nil
	case keyMatches(m.keys.Timeline, msg):
		return m.toggleTimeline()
	case keyMatches(m.keys.GroupBy, msg):
		return m.toggleGroupBy()
	case keyMatches(m.keys.Filter, msg):
		m.openWindowForm()
		return nil
	}

	if m.timelineFocus {
		m.movePeriod(msg)
		return nil
	}
	m.moveNode(msg)
	return nil
}

// toggleTimeline moves the cursor between the graph and the strip. Focusing an
// empty strip would hide every key, so it reports that instead.
func (m *MoneyFlow) toggleTimeline() tea.Cmd {
	if m.timelineFocus {
		m.timelineFocus = false
		return nil
	}
	if len(m.timeline.Periods) == 0 {
		m.ctx.Notify(LevelError, "no timeline periods in this window")
		return nil
	}
	m.timelineFocus = true
	return nil
}

// clear handles esc: it leaves the strip first, then drops a scrubbed period,
// which is what hands the graph back to the user's own window.
func (m *MoneyFlow) clear() tea.Cmd {
	if m.timelineFocus {
		m.timelineFocus = false
		return nil
	}
	if m.scrub == nil {
		return nil
	}
	m.scrub = nil
	m.ctx.Notify(LevelInfo, "graph window restored")
	return m.loadGraph()
}

// scrubToPeriod narrows the graph to the selected period's inclusive bounds,
// which is exactly what those bounds exist for. The timeline itself keeps
// running on the user's window, so the other periods stay selectable.
func (m *MoneyFlow) scrubToPeriod() tea.Cmd {
	if m.period < 0 || m.period >= len(m.timeline.Periods) {
		return nil
	}
	period := m.timeline.Periods[m.period]
	if period.StartDate == "" || period.EndDate == "" {
		m.ctx.Notify(LevelError, "that period has no bounds to scrub to")
		return nil
	}
	m.scrub = &api.WindowFilter{DateFrom: period.StartDate, DateTo: period.EndDate}
	m.ctx.Notify(LevelInfo, "graph scrubbed to %s", period.Label)
	return m.loadGraph()
}

// toggleGroupBy flips the strip between calendar months and the account's
// statement periods. The cycle framing needs one account that has a billing day,
// so the same condition the server rejects with a 400 is checked here first.
func (m *MoneyFlow) toggleGroupBy() tea.Cmd {
	if m.groupBy == mfGroupBillingCycle {
		m.groupBy = mfGroupMonth
		m.ctx.Notify(LevelInfo, "timeline grouped by calendar month")
		return m.loadTimeline()
	}
	if !m.billingCycleReady() {
		m.ctx.Notify(LevelError, "billing cycles need one account with a billing day — set it in f")
		return nil
	}
	m.groupBy = mfGroupBillingCycle
	m.ctx.Notify(LevelInfo, "timeline grouped by billing cycle (%d cycles)", m.cycles)
	return m.loadTimeline()
}

// billingCycleReady reports whether the window holds a single account with a
// billing day, which is what the cycle timeline requires.
func (m *MoneyFlow) billingCycleReady() bool {
	if m.window.AccountID == "" {
		return false
	}
	account, ok := m.ctx.Ref.Account(m.window.AccountID)
	return ok && account.BillingDay != nil
}

// movePeriod walks the strip's cursor.
func (m *MoneyFlow) movePeriod(msg tea.KeyMsg) {
	count := len(m.timeline.Periods)
	if count == 0 {
		return
	}
	switch msg.String() {
	case "up", "k", "left":
		m.period--
	case "down", "j", "right":
		m.period++
	case "home", "g":
		m.period = 0
	case "end", "G":
		m.period = count - 1
	case "pgup", "ctrl+b":
		m.period -= 5
	case "pgdown", "ctrl+f":
		m.period += 5
	default:
		return
	}
	m.period = min(max(m.period, 0), count-1)
	m.periodChosen = true
}

// moveNode walks the graph cursor. The stage headings are skipped, so every step
// lands on a node the trace can act on.
func (m *MoneyFlow) moveNode(msg tea.KeyMsg) {
	switch msg.String() {
	case "up", "k":
		m.step(-1)
	case "down", "j":
		m.step(1)
	case "home", "g":
		m.seek(1, 0)
	case "end", "G":
		m.seek(-1, len(m.rowNode)-1)
	case "pgup", "ctrl+b":
		for i := 0; i < 10; i++ {
			m.step(-1)
		}
	case "pgdown", "ctrl+f":
		for i := 0; i < 10; i++ {
			m.step(1)
		}
	}
}

// step moves the cursor by delta selectable rows, stopping at the ends, so a
// heading can never take the selection.
func (m *MoneyFlow) step(delta int) {
	index := m.table.Cursor()
	for range m.rowNode {
		index += delta
		if index < 0 || index >= len(m.rowNode) {
			return
		}
		if m.rowNode[index] >= 0 {
			m.table.SetCursor(index)
			return
		}
	}
}

// seek moves the cursor from `start` in direction `delta` until a node row is
// found, which is how home and end land on a node rather than a heading.
func (m *MoneyFlow) seek(delta, start int) {
	index := min(max(start, 0), len(m.rowNode)-1)
	for ; index >= 0 && index < len(m.rowNode); index += delta {
		if m.rowNode[index] >= 0 {
			m.table.SetCursor(index)
			return
		}
	}
}

// currentNode returns the node under the cursor.
func (m *MoneyFlow) currentNode() (api.MoneyFlowNode, bool) {
	index := m.table.Cursor()
	if index < 0 || index >= len(m.rowNode) {
		return api.MoneyFlowNode{}, false
	}
	node := m.rowNode[index]
	if node < 0 || node >= len(m.nodes) {
		return api.MoneyFlowNode{}, false
	}
	return m.nodes[node], true
}

// flatten rebuilds the stage-ordered node list and the id→name map. The server
// already stages its nodes, but re-sorting by volume keeps the rows stable when
// a stage is capped and its "Other" node appears at the end.
func (m *MoneyFlow) flatten() {
	m.names = make(map[string]string, len(m.graph.Nodes))
	for _, node := range m.graph.Nodes {
		m.names[node.ID] = node.Name
	}

	m.nodes = make([]api.MoneyFlowNode, 0, len(m.graph.Nodes))
	for _, stage := range mfStages {
		group := make([]api.MoneyFlowNode, 0, 8)
		for _, node := range m.graph.Nodes {
			if node.Kind == stage.kind {
				group = append(group, node)
			}
		}
		sort.SliceStable(group, func(i, j int) bool {
			vi, vj := group[i].Total.Float64(), group[j].Total.Float64()
			if vi != vj {
				return vi > vj
			}
			return group[i].Name < group[j].Name
		})
		m.nodes = append(m.nodes, group...)
	}
}

// anchorCursor keeps the selection on a node. A reload rebuilds the row list, so
// the row under the cursor may now be a heading or past the end.
func (m *MoneyFlow) anchorCursor() {
	if len(m.rowNode) == 0 {
		return
	}
	index := min(max(m.table.Cursor(), 0), len(m.rowNode)-1)
	if m.rowNode[index] >= 0 {
		m.table.SetCursor(index)
		return
	}
	m.seek(1, index)
	m.seek(-1, index)
}

// clampPeriod keeps the strip's selection valid. A new window re-anchors on the
// most recent period, which is the one a user usually wants to scrub to.
func (m *MoneyFlow) clampPeriod() {
	if len(m.timeline.Periods) == 0 {
		m.period, m.offset, m.periodChosen = 0, 0, false
		return
	}
	if !m.periodChosen {
		m.period, m.periodChosen = len(m.timeline.Periods)-1, true
		return
	}
	m.period = min(max(m.period, 0), len(m.timeline.Periods)-1)
}

// stageMax is the largest node total in a stage, the denominator every bar in
// that stage is drawn against so the bars compare within their own stage.
func (m *MoneyFlow) stageMax(kind string) float64 {
	max := 0.0
	for _, node := range m.nodes {
		if node.Kind != kind {
			continue
		}
		if value := node.Total.Float64(); value > max {
			max = value
		}
	}
	return max
}

// name resolves a node id for an edge line.
func (m *MoneyFlow) name(id string) string {
	if label, ok := m.names[id]; ok {
		return label
	}
	return id
}

// edgesAround splits the graph's edges into those leaving and those arriving at
// a node, each ordered by volume so the biggest flow reads first.
func (m *MoneyFlow) edgesAround(id string) (out, in []api.MoneyFlowEdge) {
	for _, edge := range m.graph.Links {
		switch {
		case edge.Source == id:
			out = append(out, edge)
		case edge.Target == id:
			in = append(in, edge)
		}
	}
	mfSortEdges(out)
	mfSortEdges(in)
	return out, in
}

// effectiveLimit is the cap the request actually carries: the server's own
// default when the user has not raised it.
func (m *MoneyFlow) effectiveLimit() int {
	if m.limit <= 0 {
		return mfDefaultLimit
	}
	return m.limit
}

// isCollapsed reports whether a node is a stage's rolled-up remainder. The
// server names those ids "<kind>:other", and the modal explains what that means
// because the name alone does not say why the tail disappeared.
func (m *MoneyFlow) isCollapsed(node api.MoneyFlowNode) bool {
	return strings.HasSuffix(node.ID, ":other")
}

// traceLines renders the selected node's edges as "from → to value" lines. The
// graph carries ids only, so the labels come from the node map.
func (m *MoneyFlow) traceLines(width int) []string {
	node, ok := m.currentNode()
	if !ok {
		return nil
	}
	th := m.ctx.Theme
	lines := []string{th.PanelTitle.Render("Trace · " + node.Name + " · " + node.Kind)}
	out, in := m.edgesAround(node.ID)
	for _, edge := range out {
		lines = append(lines, m.edgeLine("out", edge, width))
	}
	for _, edge := range in {
		lines = append(lines, m.edgeLine("in ", edge, width))
	}
	if len(out)+len(in) == 0 {
		lines = append(lines, th.Subtle.Render("  no edge touches this node in this window"))
	}
	return lines
}

// edgeLine renders one edge, keeping the value visible: it is the payload of the
// line, so the names give way first. Amounts are ASCII, so len is their width.
func (m *MoneyFlow) edgeLine(direction string, edge api.MoneyFlowEdge, width int) string {
	value := edge.Value.Display()
	line := "  " + direction + "  " + m.name(edge.Source) + " → " + m.name(edge.Target)
	return truncate(line, max(8, width-len(value)-2)) + "  " + value
}

// traceBody is the modal view of one node: what it is, how large it is, and
// every edge touching it. It is the terminal equivalent of clicking a node on
// the web page's Sankey.
func (m *MoneyFlow) traceBody(node api.MoneyFlowNode) string {
	th := m.ctx.Theme
	var b strings.Builder
	fmt.Fprintf(&b, "Name   %s\n", node.Name)
	fmt.Fprintf(&b, "Kind   %s\n", node.Kind)
	if node.Group != "" {
		fmt.Fprintf(&b, "Group  %s\n", m.ctx.Ref.GroupName(node.Group))
	}
	fmt.Fprintf(&b, "Total  %s\n", node.Total.Display())
	fmt.Fprintf(&b, "Share  %s of the largest %s node\n", bar(th, ratioOf(node.Total, m.stageMax(node.Kind)), 24), node.Kind)
	if node.Color != "" {
		fmt.Fprintf(&b, "Color  %s\n", node.Color)
	}
	fmt.Fprintf(&b, "Id     %s\n", node.ID)

	if m.isCollapsed(node) {
		fmt.Fprintf(&b, "\nThis node is the tail of the %s stage rolled up at the current limit of %d, so its edges are the sum of every node below the cap (l raises it, up to %d).\n",
			node.Kind, m.effectiveLimit(), mfMaxLimit)
	}
	if node.Kind == "account" {
		b.WriteString("\nThe flows through an account include the cross-account links the graph nets in pairs and drops when they would close a cycle, so this trace can be narrower than the raw link data. The per-type rollup is in the link panel.\n")
	}

	writeSection := func(title string, edges []api.MoneyFlowEdge) {
		fmt.Fprintf(&b, "\n%s (%s)\n", title, pluralise(len(edges), "edge", "edges"))
		if len(edges) == 0 {
			b.WriteString("  none\n")
			return
		}
		for _, edge := range edges {
			fmt.Fprintf(&b, "  %s → %s  %s\n", m.name(edge.Source), m.name(edge.Target), edge.Value.Display())
		}
	}
	out, in := m.edgesAround(node.ID)
	writeSection("Outbound", out)
	writeSection("Inbound", in)
	return b.String()
}

// openLimitForm edits the node cap. A value equal to the server's default is
// stored as "unset" so the request stays minimal and keeps tracking the server's
// own default if it ever changes.
func (m *MoneyFlow) openLimitForm() {
	fields := []Field{{
		Label:    "Stage limit",
		Kind:     FieldText,
		Value:    strconv.Itoa(m.effectiveLimit()),
		Width:    6,
		Validate: mfLimitValidate,
		Help:     fmt.Sprintf("1-%d; caps income, categories and payees, the tail collapses into Other", mfMaxLimit),
	}}
	m.ctx.Open(NewForm("moneyflow.limit", "Flow node limit", fields, func(f *Form) tea.Cmd {
		value := f.IntValue("Stage limit")
		if value == mfDefaultLimit {
			m.limit = 0
		} else {
			m.limit = value
		}
		f.Close()
		m.ctx.Notify(LevelInfo, "node limit %d", m.effectiveLimit())
		return m.loadGraph()
	}))
}

// openCyclesForm edits how many statement periods the cycle timeline spans.
func (m *MoneyFlow) openCyclesForm() {
	fields := []Field{{
		Label:    "Cycles",
		Kind:     FieldText,
		Value:    strconv.Itoa(m.cycles),
		Width:    6,
		Validate: mfCyclesValidate,
		Help:     fmt.Sprintf("1-%d; only the billing-cycle timeline reads it", mfMaxCycles),
	}}
	m.ctx.Open(NewForm("moneyflow.cycles", "Billing cycles", fields, func(f *Form) tea.Cmd {
		m.cycles = f.IntValue("Cycles")
		f.Close()
		if m.groupBy != mfGroupBillingCycle {
			m.ctx.Notify(LevelInfo, "cycles set to %d — press b for the billing-cycle view", m.cycles)
			return nil
		}
		m.ctx.Notify(LevelInfo, "cycles set to %d", m.cycles)
		return m.loadTimeline()
	}))
}

// openWindowForm edits the shared window. The date bounds are inclusive, the
// same contract the timeline periods and the API use.
func (m *MoneyFlow) openWindowForm() {
	window := m.window
	fields := []Field{
		{Label: "Date from", Kind: FieldText, Value: window.DateFrom, Width: 14, Validate: optionalDate, Help: "YYYY-MM-DD, inclusive; empty means open ended"},
		{Label: "Date to", Kind: FieldText, Value: window.DateTo, Width: 14, Validate: optionalDate},
		SelectField("Account", window.AccountID, m.ctx.Ref.AccountOptions(), false),
	}
	m.ctx.Open(NewForm("moneyflow.window", "Money flow window", fields, func(f *Form) tea.Cmd {
		m.window = api.WindowFilter{
			DateFrom:  f.Value("Date from"),
			DateTo:    f.Value("Date to"),
			AccountID: f.Value("Account"),
		}
		// The strip's period list changes with the window, so the selection is
		// re-anchored on the newest period instead of pointing at a stale index.
		m.period, m.offset, m.periodChosen = 0, 0, false
		m.scrub = nil
		f.Close()

		message := "window applied"
		if m.groupBy == mfGroupBillingCycle && !m.billingCycleReady() {
			// A cycle view without a billing-day account is a 400, and the strip
			// would be left empty behind an error the user cannot act on.
			m.groupBy = mfGroupMonth
			message = "no billing day on that account — timeline back to months"
		}
		m.ctx.Notify(LevelInfo, "%s", message)
		return tea.Batch(m.loadGraph(), m.loadTimeline())
	}))
}

// mfLimitValidate keeps the node cap inside the server's own range, because a
// larger value would be clamped silently and the "Other" node would then not
// match the cap the user asked for.
func mfLimitValidate(value string) error {
	return mfRangeValidate(value, 1, mfMaxLimit)
}

// mfCyclesValidate keeps the cycle count inside the server's own range.
func mfCyclesValidate(value string) error {
	return mfRangeValidate(value, 1, mfMaxCycles)
}

// mfRangeValidate validates a mandatory integer inside an inclusive range.
func mfRangeValidate(value string, low, high int) error {
	if err := requiredInt(value); err != nil {
		return err
	}
	number, _ := strconv.Atoi(strings.TrimSpace(value))
	if number < low || number > high {
		return errText(fmt.Sprintf("expected %d-%d", low, high))
	}
	return nil
}

// syncRows rebuilds the table when the data or the box it is rendered into has
// changed since the last build.
func (m *MoneyFlow) syncRows() {
	if !m.rowsDirty || m.rowWidth <= 0 {
		return
	}
	m.applyRows(m.rowWidth)
	m.rowsDirty = false
}

// applyRows rebuilds the table for one content width: the stage headings, the
// node rows with their proportional bars, and the columns that still fit. The
// bar width is derived from the box, so the rows are rebuilt whenever either the
// data or the width changes.
func (m *MoneyFlow) applyRows(width int) {
	th := m.ctx.Theme
	plan := mfPlanFor(width)
	m.table.SetColumns(plan.columns()...)

	rows := make([][]Cell, 0, len(m.nodes)+len(mfStages))
	rowNode := make([]int, 0, len(m.nodes)+len(mfStages))
	for start := 0; start < len(m.nodes); {
		kind := m.nodes[start].Kind
		end := start
		for end < len(m.nodes) && m.nodes[end].Kind == kind {
			end++
		}

		// The heading label sits in the wide node column: the table pads every
		// column to its own width, so a heading cannot span the row.
		heading := make([]Cell, plan.count())
		for i := range heading {
			heading[i] = Muted("")
		}
		heading[0] = Text(th.Header.Render(fmt.Sprintf("▸ %s (%d)", mfStageLabel(kind), end-start)))
		rows = append(rows, heading)
		rowNode = append(rowNode, -1)

		stageMax := m.stageMax(kind)
		for i := start; i < end; i++ {
			rows = append(rows, m.nodeRow(plan, m.nodes[i], stageMax))
			rowNode = append(rowNode, i)
		}
		start = end
	}

	m.rowNode = rowNode
	m.table.SetRows(rows)
	m.anchorCursor()
}

// nodeRow renders one node: its name, its group when there is room, its total,
// and a bar proportional to the largest node in its stage.
func (m *MoneyFlow) nodeRow(plan mfPlan, node api.MoneyFlowNode, stageMax float64) []Cell {
	row := []Cell{Text("  " + node.Name)}
	if plan.group > 0 {
		group := ""
		if node.Group != "" {
			group = m.ctx.Ref.GroupName(node.Group)
		}
		row = append(row, Muted(group))
	}
	if plan.total > 0 {
		row = append(row, Cell{Text: node.Total.Display(), Role: RoleMoney})
	}
	if plan.bar > 0 {
		row = append(row, Cell{Text: bar(m.ctx.Theme, ratioOf(node.Total, stageMax), plan.bar)})
	}
	return row
}

// mfStageLabel names a stage for its heading.
func mfStageLabel(kind string) string {
	for _, stage := range mfStages {
		if stage.kind == kind {
			return stage.label
		}
	}
	return strings.ToUpper(kind)
}

// mfPlan is the column geometry that fits one content width. The group column
// and the bar give way before the node names do, because a clipped name makes
// the graph unreadable while a shorter bar only makes it coarser.
type mfPlan struct {
	group int
	total int
	bar   int
}

// mfPlanFor resolves the column widths for a content width.
func mfPlanFor(width int) mfPlan {
	plan := mfPlan{total: 14}
	if width >= 60 {
		plan.group = 14
	}
	plan.bar = min(max((width-plan.group-plan.total-4)/2, 8), 22)

	for plan.bar > 6 && width-plan.group-plan.total-plan.bar-3 < 12 {
		plan.bar--
	}
	if width-plan.group-plan.total-plan.bar-3 < 12 {
		plan.group = 0
	}
	if width-plan.total-plan.bar-3 < 10 {
		plan.total = 0
	}
	if width-plan.total-plan.bar-3 < 10 {
		plan.bar = 0
	}
	return plan
}

// columns lists the columns of a plan; the node column stays flexible so it
// absorbs whatever the fixed columns leave.
func (p mfPlan) columns() []Column {
	columns := []Column{{Title: "Node"}}
	if p.group > 0 {
		columns = append(columns, Column{Title: "Group", Width: p.group})
	}
	if p.total > 0 {
		columns = append(columns, Column{Title: "Total", Width: p.total, Align: AlignRight})
	}
	if p.bar > 0 {
		columns = append(columns, Column{Title: "Share", Width: p.bar})
	}
	return columns
}

// count is how many cells a row of this plan carries, so a heading row can be
// padded to the same shape as a node row.
func (p mfPlan) count() int {
	return len(p.columns())
}

// headerLines summarises the window the graph covers and the totals the server
// reported. The graph response carries income and expense but no net, and the
// client never does arithmetic on money, so no net is shown here.
func (m *MoneyFlow) headerLines(width int) []string {
	th := m.ctx.Theme
	title := th.Title.Render("Money flow")
	title += th.Subtle.Render(" · ") + th.Positive.Render("in "+m.graph.TotalIncome.Display())
	title += th.Subtle.Render(" · ") + th.Negative.Render("out "+m.graph.TotalExpense.Display())
	title += th.Subtle.Render(fmt.Sprintf(" · %s, %s",
		pluralise(len(m.graph.Nodes), "node", "nodes"),
		pluralise(len(m.graph.Links), "edge", "edges")))

	context := []string{mfWindowLabel(m.window)}
	if m.window.AccountID != "" {
		context = append([]string{"account " + m.ctx.Ref.AccountName(m.window.AccountID)}, context...)
	}
	limit := fmt.Sprintf("limit %d", m.effectiveLimit())
	if m.limit <= 0 {
		limit += " (default)"
	}
	context = append(context, limit, "group "+m.groupLabel())
	if m.groupBy == mfGroupBillingCycle {
		context = append(context, fmt.Sprintf("cycles %d", m.cycles))
	}

	lines := []string{
		truncate(title, width),
		th.Subtle.Render(truncate(strings.Join(context, " · "), width)),
	}
	if m.scrub != nil {
		lines = append(lines, th.WarnText.Render(truncate(fmt.Sprintf(
			"scrubbed to %s…%s (esc clears)", m.scrub.DateFrom, m.scrub.DateTo), width)))
	}
	// A narrow box has no room for the side panel, so the rollup still has to be
	// stated here or the netted-away flows would silently disappear.
	if width < mfPanelMinWidth && len(m.graph.LinkSummary) > 0 {
		lines = append(lines, th.Subtle.Render(truncate("links (netted, not drawn): "+m.linkSummaryLine(), width)))
	}
	return lines
}

// mfWindowLabel describes the window in words, so an unbounded window does not
// render as a row of ellipses.
func mfWindowLabel(window api.WindowFilter) string {
	switch {
	case window.DateFrom != "" && window.DateTo != "":
		return fmt.Sprintf("window %s…%s", window.DateFrom, window.DateTo)
	case window.DateFrom != "":
		return "window from " + window.DateFrom
	case window.DateTo != "":
		return "window until " + window.DateTo
	default:
		return "window all time"
	}
}

// groupLabel names the timeline's framing.
func (m *MoneyFlow) groupLabel() string {
	if m.groupBy == mfGroupBillingCycle {
		return "billing cycle"
	}
	return "calendar month"
}

// linkSummaryLine folds the rollup into one line for a narrow box.
func (m *MoneyFlow) linkSummaryLine() string {
	parts := make([]string, 0, len(m.graph.LinkSummary))
	for _, summary := range m.graph.LinkSummary {
		parts = append(parts, fmt.Sprintf("%s %d %s", summary.Type, summary.Count, summary.Total.Display()))
	}
	return strings.Join(parts, " · ")
}

// linkPanel is the cross-account link rollup. The graph nets reciprocal pairs and
// drops back edges to stay acyclic, so those flows are deliberately absent from
// the node rows; the panel says so and gives the per-type counts instead.
func (m *MoneyFlow) linkPanel(width, height int) string {
	th := m.ctx.Theme
	contentWidth := max(10, width-4) // the panel frame adds a border and padding
	lines := []string{th.PanelTitle.Render("Cross-account links")}
	for _, line := range strings.Split(wrapText("netted in pairs or dropped when they would close a cycle, so they are not drawn as edges", contentWidth), "\n") {
		lines = append(lines, th.Subtle.Render(line))
	}
	lines = append(lines, "")
	if len(m.graph.LinkSummary) == 0 {
		lines = append(lines, th.Subtle.Render("none in this window"))
	}
	for _, summary := range m.graph.LinkSummary {
		labelWidth := max(6, contentWidth-17)
		lines = append(lines, pad(truncate(summary.Type, labelWidth), labelWidth)+
			" "+mfPadLeft(strconv.Itoa(summary.Count), 3)+
			" "+mfPadLeft(summary.Total.Display(), 12))
	}

	// Clip the content to the rows the column has; the frame then closes around
	// what is left rather than stretching to the full height.
	if allowed := max(1, height-2); len(lines) > allowed {
		lines = append(lines[:allowed-1], th.Subtle.Render("…"))
	}
	for i := range lines {
		lines[i] = truncate(lines[i], contentWidth)
	}
	return trimToBox(th.Panel.Render(strings.Join(lines, "\n")), width, height)
}

// mfPadLeft right-aligns a value in a fixed number of cells, so the counts and
// totals line up in the side panel.
func mfPadLeft(value string, width int) string {
	if current := ansi.StringWidth(value); current < width {
		return strings.Repeat(" ", width-current) + value
	}
	return value
}

// timelineView renders the period strip: one line per period with its label, a
// bar split into income and expense, and the period's own net. The net comes
// from the API — the client never subtracts money — and the bars are scaled
// against the busiest period in the strip so the periods compare at a glance.
func (m *MoneyFlow) timelineView(width, height int) string {
	th := m.ctx.Theme
	periods := m.timeline.Periods
	visible := max(0, height-1)
	m.offset = clampOffset(m.offset, m.period, visible, len(periods))

	title := fmt.Sprintf("Timeline · %s · %s", m.groupLabel(), pluralise(len(periods), "period", "periods"))
	if len(periods) > visible {
		title += fmt.Sprintf(" · showing %d-%d", m.offset+1, min(m.offset+visible, len(periods)))
	}
	if m.timelineFocus {
		title = th.PanelTitle.Render("▸ " + title)
	} else {
		title = th.Header.Render(title)
	}

	busiest := 0.0
	for _, period := range periods {
		if total := period.Income.Float64() + period.Expense.Float64(); total > busiest {
			busiest = total
		}
	}

	barWidth := min(max(width/3, 8), 20)
	full := width >= 66+barWidth

	lines := []string{title}
	for i := m.offset; i < len(periods) && len(lines) < height; i++ {
		lines = append(lines, m.periodLine(periods[i], i, barWidth, busiest, full))
	}
	return strings.Join(lines, "\n")
}

// periodLine renders one timeline period. A narrow box keeps only the label, the
// bar and the net, because dropping the two component totals is honest while
// letting them push the net off the edge is not.
func (m *MoneyFlow) periodLine(period api.MoneyFlowTimelinePeriod, index, barWidth int, busiest float64, full bool) string {
	th := m.ctx.Theme
	cursor := "  "
	if m.timelineFocus && index == m.period {
		cursor = th.Title.Render("▸ ")
	}
	label := pad(truncate(defaultTo(period.Label, period.Key), mfTimelineLabelWide), mfTimelineLabelWide)
	if m.scrub != nil && m.scrub.DateFrom == period.StartDate {
		label += th.WarnText.Render(" scrubbed")
	}

	split := mfSplitBar(th, period.Income, period.Expense, busiest, barWidth)
	net := mfNet(period.Net)
	netStyle := th.Positive
	if period.Net.IsNegative() {
		netStyle = th.Negative
	}

	if full {
		return cursor + label + " " + split + "  " +
			th.Positive.Render("in "+period.Income.Display()) + "  " +
			th.Negative.Render("out "+period.Expense.Display()) + "  " +
			netStyle.Render("net "+net)
	}
	return cursor + label + " " + split + "  " + netStyle.Render(net)
}

// mfNet renders a period's net with an explicit sign. The API sends the net
// already signed, so signedAmount would be wrong here: it is built for credit
// transactions, whose stored amount carries no sign of its own.
func mfNet(amount api.Amount) string {
	text := amount.Display()
	if amount.IsZero() || amount.IsNegative() {
		return text
	}
	return "+" + text
}

// mfSplitBar draws one period's income and expense as a single bar scaled
// against the busiest period, so the periods are comparable. Both segments come
// from display-only float conversions of the amounts the server sent.
func mfSplitBar(th Theme, income, expense api.Amount, busiest float64, width int) string {
	if width <= 0 {
		return ""
	}
	inWidth := mfSegment(income, busiest, width)
	expenseWidth := mfSegment(expense, busiest, width)
	if inWidth+expenseWidth > width {
		expenseWidth = width - inWidth
	}
	return th.Positive.Render(strings.Repeat("█", inWidth)) +
		th.Negative.Render(strings.Repeat("▓", expenseWidth)) +
		th.BarEmpty.Render(strings.Repeat("░", width-inWidth-expenseWidth))
}

// mfSegment sizes one part of a split bar, keeping a nonzero amount visible even
// when it rounds down to nothing.
func mfSegment(amount api.Amount, busiest float64, width int) int {
	if amount.IsZero() {
		return 0
	}
	return min(max(int(ratioOf(amount, busiest)*float64(width)+0.5), 1), width)
}

// mfSortEdges orders edges by volume, biggest first.
func mfSortEdges(edges []api.MoneyFlowEdge) {
	sort.SliceStable(edges, func(i, j int) bool {
		return edges[i].Value.Float64() > edges[j].Value.Float64()
	})
}

// mfClip keeps the first limit lines, replacing the last with a marker so it is
// clear that more detail exists behind the key that shows it.
func mfClip(lines []string, limit int, more string) string {
	if limit <= 0 || len(lines) == 0 {
		return ""
	}
	if len(lines) <= limit || limit == 1 {
		return strings.Join(lines[:min(limit, len(lines))], "\n")
	}
	return strings.Join(append(append([]string{}, lines[:limit-1]...), more), "\n")
}

// emptyMessage distinguishes "nothing loaded yet" from "this window has no flow".
func (m *MoneyFlow) emptyMessage() string {
	if !m.loaded {
		return "loading the flow graph…"
	}
	return "no flows in this window — f widens it, l raises the node limit"
}

// hintLine is the screen's own key reminder, in the same voice as the ledger's.
func (m *MoneyFlow) hintLine() string {
	if m.timelineFocus {
		return m.ctx.Theme.Subtle.Render("↑/↓ period · enter scrub graph · b month/cycle · c cycles · esc back")
	}
	return m.ctx.Theme.Subtle.Render("f window · l limit · t timeline · enter trace · esc clear scrub")
}

// View implements Screen.
func (m *MoneyFlow) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	th := m.ctx.Theme
	head := m.headerLines(width)
	avail := max(1, height-len(head)-1) // the hint line is always drawn

	// The side panel exists only when the box can afford a graph beside it.
	panelWidth, gutter, graphWidth := 0, 0, width
	if width >= mfPanelMinWidth && avail >= mfMinGraphLines+2 {
		panelWidth, gutter = mfPanelWidth, 2
		graphWidth = width - panelWidth - gutter
	}
	// The rows carry a bar, so they are built for the column the graph actually
	// gets, which the side panel narrows.
	if graphWidth != m.rowWidth {
		m.rowWidth, m.rowsDirty = graphWidth, true
	}
	m.syncRows()

	trace := m.traceLines(width)
	traceHeight := min(len(trace), mfMaxTraceLines)
	timelineHeight := 0
	if periods := len(m.timeline.Periods); periods > 0 {
		timelineHeight = min(periods+1, mfMaxTimelineLines)
	}

	// The graph is the screen, so the preview and then the strip give way first
	// when the box is short.
	graphHeight := avail - traceHeight - timelineHeight
	for graphHeight < mfMinGraphLines && traceHeight > 1 {
		traceHeight--
		graphHeight++
	}
	for graphHeight < mfMinGraphLines && timelineHeight > 3 {
		timelineHeight--
		graphHeight++
	}
	graphHeight = max(1, graphHeight)

	block := m.table.View(th, graphWidth, graphHeight, m.emptyMessage())
	if panelWidth > 0 {
		// The table fills its box exactly, so the gutter has to be added here or
		// the panel's border would sit against the bars.
		block = joinHorizontal(block, graphWidth+gutter-1, m.linkPanel(panelWidth, graphHeight))
	}

	parts := append([]string{}, head...)
	parts = append(parts, block)
	if traceHeight > 0 {
		parts = append(parts, mfClip(trace, traceHeight, th.Subtle.Render("… more edges — enter for the full trace")))
	}
	if timelineHeight > 0 {
		parts = append(parts, m.timelineView(width, timelineHeight))
	}
	parts = append(parts, m.hintLine())
	return trimToBox(strings.Join(parts, "\n"), width, height)
}
