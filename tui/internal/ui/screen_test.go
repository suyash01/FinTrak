package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fintrak/client/api"
)

// refStub serves the eight endpoints the reference load hits, so a test can fail
// one of them and see what that does. bodies overrides a path's response, and a
// path listed in fail is answered with a 500.
func refStub(t *testing.T, fail map[string]bool, bodies map[string]string) *api.Client {
	t.Helper()
	// Two of the eight wrap their list in a DataList envelope; the rest are bare
	// arrays and /auth/me is a bare object.
	answers := map[string]string{
		"/auth/me":       `{"id":"u1","email":"a@b.test","name":"A"}`,
		"/accounts":      `[{"id":"a1","name":"Everyday","accountTypeId":"bank","currency":"INR"}]`,
		"/account-types": `[{"id":"bank","name":"Bank"}]`,
		"/groups":        `[{"id":"g1","name":"Food"}]`,
		"/categories":    `[{"id":"c1","name":"Rent","groupId":"g1"}]`,
		"/payees":        `[{"id":"p1","name":"Corner Store"}]`,
		"/tags":          `{"data":[{"name":"food","count":3}]}`,
		"/recurring":     `{"data":[{"id":"r1","name":"Netflix"}]}`,
	}
	for path, body := range bodies {
		answers[path] = body
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		if fail[path] {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"boom"}`)
			return
		}
		body, ok := answers[path]
		if !ok {
			t.Errorf("the reference load asked for an unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)

	client, err := api.New(srv.URL + "/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	return client
}

// TestFetchRefDataLoadsEveryTable is the baseline: all eight resolve and the
// lookup maps are built.
func TestFetchRefDataLoadsEveryTable(t *testing.T) {
	ref, err := fetchRefData(context.Background(), refStub(t, nil, nil))
	if err != nil {
		t.Fatalf("fetchRefData: %v", err)
	}
	if len(ref.Accounts) != 1 || len(ref.Categories) != 1 || len(ref.Payees) != 1 {
		t.Errorf("reference data is short: %d accounts, %d categories, %d payees",
			len(ref.Accounts), len(ref.Categories), len(ref.Payees))
	}
	if len(ref.Recurring) != 1 || ref.Recurring[0].Name != "Netflix" {
		t.Errorf("recurring series = %+v, want the one the stub served", ref.Recurring)
	}
	if ref.RecurringErr != nil {
		t.Errorf("RecurringErr = %v, want nil when every table loaded", ref.RecurringErr)
	}
	if !ref.Loaded() {
		t.Error("Loaded() is false after a successful load, so no screen would be created")
	}
}

// TestAFailedSeriesLoadDoesNotFailTheSession is the whole reason recurring series
// go through a second error sink. Every screen is created after this load, so an
// all-or-nothing reference load means one flaky endpoint stops the TUI opening at
// all. A filter that cannot narrow by series is a much smaller loss - and the
// failure is recorded rather than swallowed, so the picker can say so.
func TestAFailedSeriesLoadDoesNotFailTheSession(t *testing.T) {
	ref, err := fetchRefData(context.Background(), refStub(t, map[string]bool{"/recurring": true}, nil))
	if err != nil {
		t.Fatalf("a failed recurring load failed the whole session: %v", err)
	}
	if ref.RecurringErr == nil {
		t.Error("RecurringErr is nil after the series load failed, so the reason is lost")
	}
	if len(ref.Recurring) != 0 {
		t.Errorf("recurring series = %+v, want none", ref.Recurring)
	}
	// The rest of the load must be intact, or the session is degraded for
	// nothing.
	if !ref.Loaded() || len(ref.Accounts) != 1 {
		t.Errorf("the other tables did not survive a series failure: loaded=%v accounts=%d",
			ref.Loaded(), len(ref.Accounts))
	}
}

// TestAFailedRequiredTableStillFailsTheSession is the other half, and the one that
// stops the optional sink being over-applied. Accounts and categories are not
// optional: without them there are no screens to show and no names to resolve.
func TestAFailedRequiredTableStillFailsTheSession(t *testing.T) {
	for _, path := range []string{"/auth/me", "/accounts", "/categories", "/payees"} {
		t.Run(path, func(t *testing.T) {
			if _, err := fetchRefData(context.Background(), refStub(t, map[string]bool{path: true}, nil)); err == nil {
				t.Errorf("a failed %s load succeeded; the reference load has gone optional", path)
			}
		})
	}
}

// TestTheSeriesFieldExplainsAnEmptyList is what makes the optional slot honest
// rather than merely non-fatal. Zero options is ambiguous - a user with no series
// and a user whose series failed to load look identical - so the field has to say
// which, and the two messages must not be the same string.
func TestTheSeriesFieldExplainsAnEmptyList(t *testing.T) {
	none := (&RefData{}).RecurringSeriesField("")
	if len(none.Options) != 0 {
		t.Errorf("a RefData with no series offered %d options", len(none.Options))
	}
	if !strings.Contains(none.Help, "no recurring series") {
		t.Errorf("the empty list is unexplained: help = %q", none.Help)
	}

	failed := (&RefData{RecurringErr: errors.New("502 bad gateway")}).RecurringSeriesField("")
	if !strings.Contains(failed.Help, "could not load") {
		t.Errorf("a failed load is not reported in the help line: %q", failed.Help)
	}
	// The reason has to reach the user, not just the fact of it: "it did not
	// load" with no indication of why is barely better than silence.
	if !strings.Contains(failed.Help, "502") {
		t.Errorf("the help line drops the cause: %q", failed.Help)
	}
	if failed.Help == none.Help {
		t.Error("a failed load and a user with no series are reported identically")
	}
}

// TestTheSeriesFieldOffersTheLoadedSeries is the positive case, and it also pins
// that a loaded picker carries no help - the help line is for explaining an
// absence, not for narrating a present list.
func TestTheSeriesFieldOffersTheLoadedSeries(t *testing.T) {
	ref := &RefData{Recurring: []api.RecurringSeries{
		{ID: "r1", Name: "Netflix"},
		{ID: "r2", Name: "Gym"},
	}}
	field := ref.RecurringSeriesField("")
	if len(field.Options) != 2 {
		t.Fatalf("options = %d, want 2", len(field.Options))
	}
	if field.Options[0].Value != "r1" || field.Options[0].Label != "Netflix" {
		t.Errorf("the first option is %+v, want the Netflix series", field.Options[0])
	}
	if field.Help != "" {
		t.Errorf("a loaded picker still carries help text: %q", field.Help)
	}
	// And it is pre-selected from the filter, so re-opening the form shows the
	// series already in force rather than resetting it.
	chosen := ref.RecurringSeriesField("r2")
	if chosen.Value != "r2" {
		t.Errorf("the field opens on %q, want the filter's r2", chosen.Value)
	}
}
