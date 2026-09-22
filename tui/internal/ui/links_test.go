package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fintrak/client/api"
)

// TestSuggestionConfirmLinksTheRowOnScreenAfterAKindSwitch is a regression test:
// confirming built the link from the suggester the keys had just switched to
// while the rows on screen still came from the previous one, so a transfer pair
// was linked as a cashback — which by this screen's own documentation rewrites
// both transactions' categories and payees.
func TestSuggestionConfirmLinksTheRowOnScreenAfterAKindSwitch(t *testing.T) {
	const suggestion = `{"debitTxn":{"id":"txn-1","accountId":"acct-1","date":"2026-01-04T00:00:00Z",` +
		`"description":"Card payment","amount":500.00,"type":"debit","tags":[],"accountName":"Card"},` +
		`"creditTxn":{"id":"txn-2","accountId":"bank-1","date":"2026-01-05T00:00:00Z",` +
		`"description":"Card payment received","amount":500.00,"type":"credit","tags":[],"accountName":"Everyday"},` +
		`"score":90}`
	posted := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/links/transfer-suggestions":
			fmt.Fprintf(w, `{"data":[%s],"page":1,"limit":50,"hasMore":false}`, suggestion)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/links/cashback-suggestions":
			fmt.Fprint(w, `{"data":[],"page":1,"limit":50,"hasMore":false}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/links":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding the link body: %v", err)
			}
			posted <- body
			fmt.Fprint(w, `{}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			fmt.Fprint(w, `{}`)
		}
	}))
	defer srv.Close()

	client, err := api.New(srv.URL + "/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	l := NewLinks(&Ctx{
		Client: client,
		Ref:    &RefData{},
		Theme:  DefaultTheme(),
		Notify: func(Level, string, ...any) {},
		Open:   func(Modal) {},
	})
	l.pane = linkPaneSuggestions
	run(t, l, l.refreshPane())
	if len(l.sugg.Data) != 1 {
		t.Fatalf("the transfer page holds %d suggestion(s), want 1", len(l.sugg.Data))
	}

	// The user asks for the cashback suggester and confirms the row they are
	// still looking at — a transfer pair — before that reload lands.
	if cmd := l.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")}); cmd == nil {
		t.Fatal("C did not start the cashback reload")
	}
	if l.suggKind != "cashback" {
		t.Fatalf("suggKind = %q after the switch, want cashback", l.suggKind)
	}
	run(t, l, l.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}))

	select {
	case body := <-posted:
		if body["type"] != "transfer" {
			t.Errorf("the confirmed link has type %v, want transfer: that is the kind the row on screen belongs to",
				body["type"])
		}
		if body["fromTxnId"] != "txn-1" || body["toTxnId"] != "txn-2" {
			t.Errorf("the confirmed link is %v -> %v, want txn-1 -> txn-2", body["fromTxnId"], body["toTxnId"])
		}
	default:
		t.Fatal("no link was created")
	}
}
