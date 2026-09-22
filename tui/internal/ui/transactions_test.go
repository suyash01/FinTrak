package ui

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fintrak/client/api"
)

// run executes a command and feeds its message back to the screen, so a test can
// drive the real load/mutate cycle the App would drive.
func run(t *testing.T, s Screen, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if next := s.Update(msg); next != nil {
		if follow := next(); follow != nil {
			s.Update(follow)
		}
	}
}

// TestTransactionsReloadsAfterAWrite is a regression test for the reported bug:
// editing a transaction left the list showing its old values, because the
// screen's done handler cleared the selection and never refetched. The stub
// server returns one row before the write and two after, so only a real reload
// can produce two.
func TestTransactionsReloadsAfterAWrite(t *testing.T) {
	var gets int32
	row := func(id, description string) string {
		return fmt.Sprintf(`{"id":%q,"accountId":"acct-1","date":"2026-09-01T00:00:00Z",`+
			`"description":%q,"amount":10.5,"type":"debit","tags":[]}`, id, description)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/transactions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		n := atomic.AddInt32(&gets, 1)
		body := "[" + row("t1", "one") + "]"
		if n > 1 {
			body = "[" + row("t1", "one") + "," + row("t2", "two") + "]"
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":%s,"total":%d,"page":1,"limit":50,"pages":1}`, body, n)
	}))
	defer srv.Close()

	client, err := api.New(srv.URL + "/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ctx := &Ctx{Client: client, Ref: &RefData{}, Theme: DefaultTheme(), Notify: func(Level, string, ...any) {}}
	screen := NewTransactions(ctx)

	run(t, screen, screen.Refresh())
	if len(screen.rows) != 1 {
		t.Fatalf("initial rows = %d, want 1", len(screen.rows))
	}

	run(t, screen, screen.Update(done{tag: "txn.save", note: "transaction updated"}))

	if len(screen.rows) != 2 {
		t.Errorf("after a successful write the list holds %d rows, want 2 — it did not reload", len(screen.rows))
	}
}

// TestTransactionsKeepsTheListOnAFailedWrite makes sure a rejected save does not
// discard what the screen already had.
func TestTransactionsKeepsTheListOnAFailedWrite(t *testing.T) {
	var gets int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&gets, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"t1","accountId":"a","date":"2026-09-01T00:00:00Z","description":"one","amount":1,"type":"debit","tags":[]}],"total":1,"page":1,"limit":50,"pages":1}`)
	}))
	defer srv.Close()

	client, err := api.New(srv.URL + "/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ctx := &Ctx{Client: client, Ref: &RefData{}, Theme: DefaultTheme(), Notify: func(Level, string, ...any) {}}
	screen := NewTransactions(ctx)
	run(t, screen, screen.Refresh())

	before := atomic.LoadInt32(&gets)
	run(t, screen, screen.Update(done{tag: "txn.save", err: errors.New("amount: must be positive")}))

	if len(screen.rows) != 1 {
		t.Errorf("a failed write changed the list: %d rows", len(screen.rows))
	}
	if atomic.LoadInt32(&gets) != before {
		t.Error("a failed write should not refetch")
	}
}

// TestTransactionsDropsASupersededPage is a regression test for a race: the list
// load carried only its tag, so two overlapping loads were indistinguishable and
// whichever answered last won — including an older page overwriting the newer one
// that had already landed. The header, the rows and the next page request then
// described different queries.
func TestTransactionsDropsASupersededPage(t *testing.T) {
	var gets int32
	row := func(id, description string) string {
		return fmt.Sprintf(`{"id":%q,"accountId":"acct-1","date":"2026-09-01T00:00:00Z",`+
			`"description":%q,"amount":10.5,"type":"debit","tags":[]}`, id, description)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The first request is answered with two rows and the second with one, so
		// the test can tell which response the screen ends up showing: they are
		// executed out of order, the newer request answering first.
		n := atomic.AddInt32(&gets, 1)
		body, total := "["+row("t1", "one")+","+row("t2", "two")+"]", 2
		if n > 1 {
			body, total = "["+row("t3", "three")+"]", 1
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":%s,"total":%d,"page":1,"limit":50,"pages":1}`, body, total)
	}))
	defer srv.Close()

	client, err := api.New(srv.URL + "/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ctx := &Ctx{Client: client, Ref: &RefData{}, Theme: DefaultTheme(), Notify: func(Level, string, ...any) {}}
	screen := NewTransactions(ctx)

	superseded := screen.Refresh()
	current := screen.Refresh()

	// The newer request answers first...
	run(t, screen, current)
	if len(screen.rows) != 2 {
		t.Fatalf("rows = %d, want the 2 the newer request asked for", len(screen.rows))
	}
	// ...and the request it superseded must not overwrite it when it lands late.
	run(t, screen, superseded)
	if len(screen.rows) != 2 {
		t.Errorf("a superseded page overwrote the current one: %d rows, want 2", len(screen.rows))
	}
	if screen.info.Total != 2 {
		t.Errorf("the paging was taken from a superseded page: total = %d, want 2", screen.info.Total)
	}
}
