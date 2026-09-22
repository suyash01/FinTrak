package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fintrak/client/api"
)

// TestLoanScheduleTextShowsFeeTransfersAndEntryStates pins the loan schedule
// overlay's new surface: the processing fee recorded for reference, the
// transfers with the side this loan was on and the payoff breakdown each one
// moved, and the state of an installment a transfer cancelled or recast.
func TestLoanScheduleTextShowsFeeTransfersAndEntryStates(t *testing.T) {
	date := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	settled := date(2026, 2, 1)
	detail := api.LoanScheduleDetail{
		Schedule: &api.LoanSchedule{
			ID: "s1", LoanAccountID: "acct-1", Principal: "100000.00", ProcessingFee: "2000.00",
			AnnualRateBps: 950, TenureMonths: 12, StartDate: date(2026, 1, 5), DisbursalDate: date(2026, 1, 5),
		},
		EMI: "8500.00",
		Entries: []api.LoanScheduleEntry{
			{Number: 1, DueDate: date(2026, 2, 5), Amount: "8500.00", Principal: "7000.00", Interest: "1500.00", Balance: "91000.00"},
			{Number: 2, DueDate: date(2026, 3, 5), Amount: "8600.00", Principal: "7100.00", Interest: "1500.00", Balance: "83900.00", Recast: true, Paid: true},
			{Number: 3, DueDate: date(2026, 4, 5), Amount: "8600.00", Principal: "7100.00", Interest: "1500.00", Balance: "76800.00", Cancelled: true},
		},
		Transfers: []api.LoanPrincipalTransfer{
			{
				ID: "tr1", FromLoanAccountID: "acct-1", ToLoanAccountID: "acct-2",
				ToLoanAccountName: "Home loan", Amount: "91000.00", TransferDate: date(2026, 2, 1),
				Principal: "90000.00", AccruedInterest: "1000.00",
				Mode: "recast",
			},
			{
				ID: "tr2", FromLoanAccountID: "acct-3", FromLoanAccountName: "Bike loan",
				ToLoanAccountID: "acct-1", Amount: "5000.00", TransferDate: date(2026, 3, 1),
				Principal: "4900.00", AccruedInterest: "100.00",
				Mode: "takeover",
			},
		},
		SettledOn: &settled,
	}

	text := accountsLoanScheduleText(detail)
	for _, want := range []string{
		"Processing fee     2,000.00 (reference only)",
		"Settled on         2026-02-01",
		"out  2026-02-01  Home loan",
		"in   2026-03-01  Bike loan",
		"takeover",
		"(principal 90,000.00 · interest 1,000.00)",
		"(principal 4,900.00 · interest 100.00)",
		"recast · paid",
		"cancelled",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the schedule text is missing %q:\n%s", want, text)
		}
	}
}

// TestTransferFormSendsTargetTermsOnlyWithoutATargetSchedule covers the one
// decision the balance-transfer form makes on its own: a target that already has
// a schedule is recast and ignores the request's target terms, so the target's
// schedule is fetched and the terms are sent only when there is nothing to
// recast.
func TestTransferFormSendsTargetTermsOnlyWithoutATargetSchedule(t *testing.T) {
	withSchedule := `{"schedule":{"id":"s1","loanAccountId":"acct-2","principal":1000.00,"processingFee":0,` +
		`"annualRateBps":900,"tenureMonths":12,"startDate":"2026-01-05T00:00:00Z",` +
		`"createdAt":"2026-01-05T00:00:00Z","updatedAt":"2026-01-05T00:00:00Z"},"entries":[]}`
	withoutSchedule := `{"schedule":null,"entries":[]}`

	for _, tc := range []struct {
		name     string
		target   string
		wantSent bool
	}{
		{name: "target has a schedule", target: withSchedule},
		{name: "target has no schedule", target: withoutSchedule, wantSent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var fetched int32
			posted := make(chan map[string]any, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/acct-2/loan-schedule":
					atomic.AddInt32(&fetched, 1)
					fmt.Fprint(w, tc.target)
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/acct-1/loan-payoff":
					fmt.Fprint(w, payoffJSON)
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/accounts/acct-1/loan-transfer":
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decoding the transfer body: %v", err)
					}
					posted <- body
					fmt.Fprint(w, `{"transfer":{},"source":{"schedule":null,"entries":[]},"target":{"schedule":null,"entries":[]}}`)
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
			source := api.Account{ID: "acct-1", Name: "Car loan", AccountTypeID: "loan"}
			target := api.Account{ID: "acct-2", Name: "Home loan", AccountTypeID: "loan"}
			var modal Modal
			ctx := &Ctx{
				Client: client,
				Ref:    &RefData{Accounts: []api.Account{source, target}},
				Theme:  DefaultTheme(),
				Notify: func(Level, string, ...any) {},
				Open:   func(m Modal) { modal = m },
			}
			a := NewAccounts(ctx)
			a.accounts = ctx.Ref.Accounts
			a.applyRows()

			// `t` on a loan account opens the transfer form, whose target
			// defaults to the only other loan account.
			a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
			form, ok := modal.(*Form)
			if !ok {
				t.Fatalf("t did not open a form (got %T)", modal)
			}
			// Fill the target terms in, walking past the target account, the
			// mode (left on the automatic choice) and the transfer date (which
			// defaults to today, a valid date).
			form.Update(tea.KeyMsg{Type: tea.KeyTab})
			form.Update(tea.KeyMsg{Type: tea.KeyTab})
			form.Update(tea.KeyMsg{Type: tea.KeyTab})
			form.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("950")})
			form.Update(tea.KeyMsg{Type: tea.KeyTab})
			form.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("60")})
			form.Update(tea.KeyMsg{Type: tea.KeyTab})
			form.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2026-02-01")})

			run(t, a, form.Update(tea.KeyMsg{Type: tea.KeyCtrlS}))

			if got := atomic.LoadInt32(&fetched); got != 1 {
				t.Errorf("the target's schedule was fetched %d times, want 1", got)
			}
			var body map[string]any
			select {
			case body = <-posted:
			default:
				t.Fatal("no balance transfer was posted")
			}
			if body["toLoanAccountId"] != "acct-2" {
				t.Errorf("toLoanAccountId = %v, want acct-2", body["toLoanAccountId"])
			}
			if mode, sent := body["mode"]; sent {
				t.Errorf("mode = %v was sent, want it left off for the automatic choice", mode)
			}
			for key, want := range map[string]any{
				"targetAnnualRateBps": float64(950),
				"targetTenureMonths":  float64(60),
				"targetStartDate":     "2026-02-01",
			} {
				got, sent := body[key]
				if sent != tc.wantSent {
					t.Errorf("%s sent = %t, want %t (body %v)", key, sent, tc.wantSent, body)
					continue
				}
				if sent && got != want {
					t.Errorf("%s = %v, want %v", key, got, want)
				}
			}
		})
	}
}

// TestLoanScheduleTextShowsDisbursementReconciliation pins the disbursement
// breakdown and the three states the reconciliation can be in: no credit linked,
// a linked credit that matches the net, and one that differs from it.
func TestLoanScheduleTextShowsDisbursementReconciliation(t *testing.T) {
	schedule := &api.LoanSchedule{ID: "s1", LoanAccountID: "acct-1", Principal: "100000.00"}
	for _, tc := range []struct {
		name         string
		disbursement *api.LoanDisbursement
		want         []string
	}{
		{
			name: "no credit linked",
			disbursement: &api.LoanDisbursement{
				Sanctioned: "100000.00", ProcessingFee: "2000.00", PaidOut: "5000.00", Net: "93000.00",
			},
			want: []string{
				"Sanctioned         100,000.00",
				"Processing fee     2,000.00",
				"Paid out           5,000.00",
				"Net released       93,000.00",
				"Linked credit      none",
			},
		},
		{
			name: "linked and matching",
			disbursement: &api.LoanDisbursement{
				Sanctioned: "100000.00", Net: "93000.00",
				CreditTransactionID: "txn-9", CreditAmount: "93000.00", Verified: true,
			},
			want: []string{"Linked credit      93,000.00 — matches the net"},
		},
		{
			name: "linked and different",
			disbursement: &api.LoanDisbursement{
				Sanctioned: "100000.00", Net: "93000.00",
				CreditTransactionID: "txn-9", CreditAmount: "92000.00", Difference: "-1000.00",
			},
			want: []string{"Linked credit      92,000.00 — differs by -1,000.00"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := accountsLoanScheduleText(api.LoanScheduleDetail{Schedule: schedule, Disbursement: tc.disbursement})
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("the schedule text is missing %q:\n%s", want, text)
				}
			}
		})
	}
}

// loanScheduleJSON is a loan-schedule response with the given loan account and
// disbursement block; the tests vary the latter to drive the reconciliation.
func loanScheduleJSON(accountID, disbursement string) string {
	return `{"schedule":{"id":"s1","loanAccountId":"` + accountID + `","principal":100000.00,` +
		`"processingFee":2000.00,"annualRateBps":950,"tenureMonths":12,` +
		`"startDate":"2026-02-05T00:00:00Z","disbursalDate":"2026-01-05T00:00:00Z",` +
		`"createdAt":"2026-01-05T00:00:00Z","updatedAt":"2026-01-05T00:00:00Z"},` +
		`"disbursement":` + disbursement + `,"entries":[]}`
}

// payoffJSON is the quote the transfer tests have the source loan priced at: its
// outstanding principal plus the interest accrued from the last EMI payment,
// which is what a balance transfer settles.
const payoffJSON = `{"loanAccountName":"Car loan","asOf":"2026-02-01T00:00:00Z",` +
	`"fromDate":"2026-01-05T00:00:00Z","days":27,` +
	`"outstandingPrincipal":1956632.46,"accruedInterest":26292.25,"payoff":1982924.71}`

// testAccounts builds the screen against a stub client with the given accounts
// loaded and the first selected, which is the state the loan tests start from.
// The returned pointer holds the last modal the screen opened.
func testAccounts(t *testing.T, client *api.Client, accounts ...api.Account) (*Accounts, *Modal) {
	t.Helper()
	return testAccountsNotifying(t, client, func(Level, string, ...any) {}, accounts...)
}

// testAccountsNotifying is testAccounts with the status line handed to notify,
// for the tests that assert what the screen told the user rather than what it
// opened.
func testAccountsNotifying(t *testing.T, client *api.Client, notify func(Level, string, ...any), accounts ...api.Account) (*Accounts, *Modal) {
	t.Helper()
	var modal Modal
	ctx := &Ctx{
		Client: client,
		Ref:    &RefData{Accounts: accounts},
		Theme:  DefaultTheme(),
		Notify: notify,
		Open:   func(m Modal) { modal = m },
	}
	a := NewAccounts(ctx)
	a.accounts = ctx.Ref.Accounts
	a.applyRows()
	return a, &modal
}

// TestDisbursementCreditKeyLinksACandidate drives the whole link flow: `c` on a
// loan with a schedule but no credit fetches the schedule, searches for the
// credits that could be its disbursement by exact amount and by date window, and
// linking the chosen one sends its id and shows the refreshed reconciliation.
func TestDisbursementCreditKeyLinksACandidate(t *testing.T) {
	const (
		unlinked = `{"sanctioned":100000.00,"processingFee":2000.00,"paidOut":0,"net":98000.00,` +
			`"verified":false,"difference":0}`
		linked = `{"sanctioned":100000.00,"processingFee":2000.00,"paidOut":0,"net":98000.00,` +
			`"creditTransactionId":"txn-9","creditAmount":98000.00,"verified":true,"difference":0}`
		credit = `{"id":"txn-9","accountId":"bank-1","date":"2026-01-06T00:00:00Z",` +
			`"description":"Loan disbursement","amount":98000.00,"type":"credit","tags":[],"accountName":"Everyday"}`
	)
	queries := make(chan map[string]string, 2)
	posted := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/acct-1/loan-schedule":
			fmt.Fprint(w, loanScheduleJSON("acct-1", unlinked))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/transactions":
			query := map[string]string{}
			for key, values := range r.URL.Query() {
				query[key] = values[0]
			}
			queries <- query
			fmt.Fprintf(w, `{"data":[%s],"total":1,"page":1,"limit":100,"pages":1}`, credit)
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/accounts/acct-1/loan-disbursement":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding the link body: %v", err)
			}
			posted <- body
			fmt.Fprint(w, loanScheduleJSON("acct-1", linked))
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
	a, modal := testAccounts(t, client, api.Account{ID: "acct-1", Name: "Car loan", AccountTypeID: "loan"})

	// `c` fetches the schedule, then the candidates, and opens the picker.
	run(t, a, a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")}))
	picker, ok := (*modal).(*creditPicker)
	if !ok {
		t.Fatalf("c did not open a credit picker (got %T)", *modal)
	}

	// Two queries are issued: the exact net with no date bounds, and the window
	// around the disbursal date. The same credit answers both, so the picker
	// shows it once.
	byAmount, byWindow := <-queries, <-queries
	if byAmount["type"] != "credit" || byWindow["type"] != "credit" {
		t.Errorf("types = %q and %q, want both credit", byAmount["type"], byWindow["type"])
	}
	if byAmount["amount"] != "98000.00" {
		t.Errorf("amount = %q, want the net 98000.00", byAmount["amount"])
	}
	if byAmount["dateFrom"] != "" || byAmount["dateTo"] != "" {
		t.Errorf("the amount query carries date bounds %q..%q, want none",
			byAmount["dateFrom"], byAmount["dateTo"])
	}
	for name, query := range map[string]map[string]string{"amount": byAmount, "window": byWindow} {
		if query["limit"] != "100" {
			t.Errorf("the %s query asks for limit %q, want 100", name, query["limit"])
		}
		if value, sent := query["excludeAttached"]; sent {
			t.Errorf("the %s query sends excludeAttached=%q, want it left off so an attached credit is listed", name, value)
		}
	}
	// The schedule disbursed on 2026-01-05, so the window is ±45 days.
	if byWindow["dateFrom"] != "2025-11-21" || byWindow["dateTo"] != "2026-02-19" {
		t.Errorf("window = %s..%s, want 2025-11-21..2026-02-19",
			byWindow["dateFrom"], byWindow["dateTo"])
	}
	if byWindow["amount"] != "" {
		t.Errorf("the window query carries amount %q, want none", byWindow["amount"])
	}
	if !strings.Contains(picker.View(a.ctx.Theme, 100, 20), "Loan disbursement") {
		t.Errorf("the picker does not list the candidate:\n%s", picker.View(a.ctx.Theme, 100, 20))
	}

	// Choosing the candidate links it and shows the refreshed detail.
	run(t, a, picker.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	var body map[string]any
	select {
	case body = <-posted:
	default:
		t.Fatal("no disbursement credit was linked")
	}
	if body["transactionId"] != "txn-9" {
		t.Errorf("transactionId = %v, want txn-9", body["transactionId"])
	}
	info, ok := (*modal).(*InfoModal)
	if !ok {
		t.Fatalf("linking did not show the refreshed schedule (got %T)", *modal)
	}
	if !strings.Contains(info.Body, "matches the net") {
		t.Errorf("the refreshed schedule does not show the reconciliation:\n%s", info.Body)
	}
}

// TestDisbursementCreditKeyUnlinksALinkedCredit covers the other way the key
// acts: a loan already reconciled against a credit is not offered candidates but
// asked to unlink, and confirming deletes the link.
func TestDisbursementCreditKeyUnlinksALinkedCredit(t *testing.T) {
	const linked = `{"sanctioned":100000.00,"processingFee":2000.00,"paidOut":0,"net":98000.00,` +
		`"creditTransactionId":"txn-9","creditAmount":98000.00,"verified":true,"difference":0}`
	var unlinks int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/acct-1/loan-schedule":
			fmt.Fprint(w, loanScheduleJSON("acct-1", linked))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/accounts/acct-1/loan-disbursement":
			atomic.AddInt32(&unlinks, 1)
			fmt.Fprint(w, `{"deleted":1}`)
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
	a, modal := testAccounts(t, client, api.Account{ID: "acct-1", Name: "Car loan", AccountTypeID: "loan"})

	run(t, a, a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")}))
	confirm, ok := (*modal).(*Confirm)
	if !ok {
		t.Fatalf("c did not ask to unlink the linked credit (got %T)", *modal)
	}
	run(t, a, confirm.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	if got := atomic.LoadInt32(&unlinks); got != 1 {
		t.Errorf("unlink calls = %d, want 1", got)
	}
}

// TestDisbursementCreditCandidatesWidenTheWindowWithoutADisbursalDate covers the
// reported case: a loan with no disbursal date anchors on its first installment
// (2024-09-05), while the bank released the money in March. A window centred on
// the installment misses that credit, so the fallback reaches 240 days back —
// which is what puts the disbursement in the list at all.
func TestDisbursementCreditCandidatesWidenTheWindowWithoutADisbursalDate(t *testing.T) {
	const (
		schedule = `{"schedule":{"id":"s1","loanAccountId":"acct-1","principal":100000.00,` +
			`"processingFee":2000.00,"annualRateBps":950,"tenureMonths":12,` +
			`"startDate":"2024-09-05T00:00:00Z","createdAt":"2024-09-05T00:00:00Z",` +
			`"updatedAt":"2024-09-05T00:00:00Z"},` +
			`"disbursement":{"sanctioned":100000.00,"processingFee":2000.00,"paidOut":0,` +
			`"net":98000.00,"verified":false,"difference":0},"entries":[]}`
		credit = `{"id":"txn-9","accountId":"bank-1","date":"2024-03-01T00:00:00Z",` +
			`"description":"Loan disbursement","amount":98000.00,"type":"credit","tags":[],"accountName":"Everyday"}`
	)
	queries := make(chan map[string]string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/acct-1/loan-schedule":
			fmt.Fprint(w, schedule)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/transactions":
			query := map[string]string{}
			for key, values := range r.URL.Query() {
				query[key] = values[0]
			}
			queries <- query
			fmt.Fprintf(w, `{"data":[%s],"total":1,"page":1,"limit":100,"pages":1}`, credit)
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
	a, modal := testAccounts(t, client, api.Account{ID: "acct-1", Name: "Car loan", AccountTypeID: "loan"})

	run(t, a, a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")}))
	picker, ok := (*modal).(*creditPicker)
	if !ok {
		t.Fatalf("c did not open a credit picker (got %T)", *modal)
	}

	byAmount, byWindow := <-queries, <-queries
	if byAmount["amount"] != "98000.00" {
		t.Errorf("amount = %q, want the net 98000.00", byAmount["amount"])
	}
	if byWindow["dateFrom"] != "2024-01-09" || byWindow["dateTo"] != "2024-10-20" {
		t.Errorf("window = %s..%s, want 2024-01-09..2024-10-20: the first installment 2024-09-05 minus 240 days and plus 45",
			byWindow["dateFrom"], byWindow["dateTo"])
	}
	if view := picker.View(a.ctx.Theme, 120, 20); !strings.Contains(view, "2024-03-01") || !strings.Contains(view, "Loan disbursement") {
		t.Errorf("the credit the window must now cover is not listed:\n%s", view)
	}
}

// TestDisbursementCreditCandidatesMergeRankAndDropLoanAccounts covers how the two
// queries become one list: a credit both queries return appears once, a credit on
// a loan account is not a disbursement at all — money received is never held on
// one — and the rest are ordered by how close their amount is to the net, newest
// first among equals.
func TestDisbursementCreditCandidatesMergeRankAndDropLoanAccounts(t *testing.T) {
	const unlinked = `{"sanctioned":100000.00,"processingFee":2000.00,"paidOut":0,` +
		`"net":98000.00,"verified":false,"difference":0}`
	credit := func(id, accountID, description, amount, date, attached string) string {
		return fmt.Sprintf(`{"id":%q,"accountId":%q,"date":%q,"description":%q,`+
			`"amount":%s,"type":"credit","tags":[],"accountName":"Everyday"%s}`,
			id, accountID, date, description, amount, attached)
	}
	onLoan := `,"loanAccountId":"acct-3","loanAccountName":"Other loan"`
	// Only the exact net comes back from the amount query; the window is the
	// broader net, including the very credit the amount query already found.
	byAmount := credit("txn-b", "bank-1", "Disbursement B", "98000.00", "2026-01-06T00:00:00Z", "")
	byWindow := strings.Join([]string{
		credit("txn-b", "bank-1", "Disbursement B", "98000.00", "2026-01-06T00:00:00Z", ""),
		credit("txn-c", "bank-1", "Disbursement C", "98000.00", "2026-01-09T00:00:00Z", ""),
		credit("txn-a", "bank-1", "Disbursement A", "99000.00", "2026-01-20T00:00:00Z", ""),
		// A credit held on a loan account is not money released, whatever loan
		// the transaction points at.
		credit("txn-d", "acct-3", "Disbursement D", "98000.00", "2026-01-07T00:00:00Z", onLoan),
	}, ",")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/acct-1/loan-schedule":
			fmt.Fprint(w, loanScheduleJSON("acct-1", unlinked))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/transactions":
			data := byWindow
			if r.URL.Query().Get("amount") != "" {
				data = byAmount
			}
			fmt.Fprintf(w, `{"data":[%s],"total":1,"page":1,"limit":100,"pages":1}`, data)
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
	carLoan := api.Account{ID: "acct-1", Name: "Car loan", AccountTypeID: "loan"}
	otherLoan := api.Account{ID: "acct-3", Name: "Other loan", AccountTypeID: "loan"}
	a, modal := testAccounts(t, client, carLoan, otherLoan)
	a.ctx.Ref.AccountsByID = map[string]api.Account{carLoan.ID: carLoan, otherLoan.ID: otherLoan}

	run(t, a, a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")}))
	picker, ok := (*modal).(*creditPicker)
	if !ok {
		t.Fatalf("c did not open a credit picker (got %T)", *modal)
	}

	view := picker.View(a.ctx.Theme, 120, 30)
	newest := strings.Index(view, "Disbursement C")
	older := strings.Index(view, "Disbursement B")
	further := strings.Index(view, "Disbursement A")
	if newest < 0 || older < 0 || further < 0 {
		t.Fatalf("the candidates are not all listed:\n%s", view)
	}
	if !(newest < older && older < further) {
		t.Errorf("the candidates are out of order: C=%d B=%d A=%d, want the exact net first and the newest of those before the older one:\n%s",
			newest, older, further, view)
	}
	if strings.Contains(view, "Disbursement D") {
		t.Errorf("a credit on a loan account was offered as a disbursement:\n%s", view)
	}
}

// TestDisbursementCreditKeyShowsAnAttachedCreditWithoutLinkingIt covers the
// record that used to be invisible: a credit already attached to another loan —
// typically the very transaction the user filed as an EMI payment before this
// link existed — is listed and labelled, and enter on it reports the refusal the
// API would answer with, leaving the list open to pick one that can be linked.
func TestDisbursementCreditKeyShowsAnAttachedCreditWithoutLinkingIt(t *testing.T) {
	const (
		unlinked = `{"sanctioned":100000.00,"processingFee":2000.00,"paidOut":0,` +
			`"net":98000.00,"verified":false,"difference":0}`
		linked = `{"sanctioned":100000.00,"processingFee":2000.00,"paidOut":0,` +
			`"net":98000.00,"creditTransactionId":"txn-8","creditAmount":98000.00,"verified":true,"difference":0}`
		// Both carry the net, so the newer one ranks first — and that one is the
		// attached credit, which is the row the picker has to refuse.
		attached = `{"id":"txn-7","accountId":"bank-1","date":"2026-01-09T00:00:00Z",` +
			`"description":"EMI payment","amount":98000.00,"type":"credit","tags":[],"accountName":"Everyday",` +
			`"loanAccountId":"acct-2","loanAccountName":"Home loan"}`
		linkable = `{"id":"txn-8","accountId":"bank-1","date":"2026-01-06T00:00:00Z",` +
			`"description":"Loan disbursement","amount":98000.00,"type":"credit","tags":[],"accountName":"Everyday"}`
	)
	linkedTo := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/acct-1/loan-schedule":
			fmt.Fprint(w, loanScheduleJSON("acct-1", unlinked))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/transactions":
			fmt.Fprintf(w, `{"data":[%s],"total":2,"page":1,"limit":100,"pages":1}`, attached+","+linkable)
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/accounts/acct-1/loan-disbursement":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding the link body: %v", err)
			}
			linkedTo <- fmt.Sprint(body["transactionId"])
			fmt.Fprint(w, loanScheduleJSON("acct-1", linked))
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
	var notes []string
	a, modal := testAccountsNotifying(t, client, func(_ Level, format string, args ...any) {
		notes = append(notes, fmt.Sprintf(format, args...))
	},
		api.Account{ID: "acct-1", Name: "Car loan", AccountTypeID: "loan"},
		api.Account{ID: "acct-2", Name: "Home loan", AccountTypeID: "loan"})

	run(t, a, a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")}))
	picker, ok := (*modal).(*creditPicker)
	if !ok {
		t.Fatalf("c did not open a credit picker (got %T)", *modal)
	}
	if view := picker.View(a.ctx.Theme, 120, 20); !strings.Contains(view, "EMI payment") ||
		!strings.Contains(view, "already linked to Home loan") || !strings.Contains(view, "Loan disbursement") {
		t.Errorf("the attached credit is not shown and labelled beside the linkable one:\n%s", view)
	}

	// The attached row is highlighted first: enter must report the refusal and
	// keep the list open instead of posting a link.
	run(t, a, picker.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	select {
	case id := <-linkedTo:
		t.Fatalf("linked %s, want nothing: an attached credit must not be selectable", id)
	default:
	}
	if picker.Closed() {
		t.Error("the picker closed on a credit that cannot be linked, leaving no way to pick another")
	}
	if len(notes) == 0 || !strings.Contains(notes[len(notes)-1], "already attached to Home loan") {
		t.Errorf("choosing the attached credit was not explained on the status line: %v", notes)
	}

	// The linkable row below it still is selectable.
	run(t, a, picker.Update(tea.KeyMsg{Type: tea.KeyDown}))
	run(t, a, picker.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	select {
	case id := <-linkedTo:
		if id != "txn-8" {
			t.Errorf("linked %s, want txn-8", id)
		}
	default:
		t.Fatal("the linkable credit below the attached one could not be chosen")
	}
}

// TestDisbursementCreditKeyReportsAnEmptyResult covers the empty state: when
// neither query returns anything to offer, the picker is not opened with nothing
// in it — the user is told the two things that would produce a match.
func TestDisbursementCreditKeyReportsAnEmptyResult(t *testing.T) {
	const unlinked = `{"sanctioned":100000.00,"processingFee":2000.00,"paidOut":0,` +
		`"net":98000.00,"verified":false,"difference":0}`
	var queries int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/acct-1/loan-schedule":
			fmt.Fprint(w, loanScheduleJSON("acct-1", unlinked))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/transactions":
			atomic.AddInt32(&queries, 1)
			fmt.Fprint(w, `{"data":[],"total":0,"page":1,"limit":100,"pages":0}`)
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
	var notes []string
	a, modal := testAccountsNotifying(t, client, func(_ Level, format string, args ...any) {
		notes = append(notes, fmt.Sprintf(format, args...))
	}, api.Account{ID: "acct-1", Name: "Car loan", AccountTypeID: "loan"})

	run(t, a, a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")}))
	if got := atomic.LoadInt32(&queries); got != 2 {
		t.Errorf("candidate queries = %d, want 2 (the amount and the window)", got)
	}
	if *modal != nil {
		t.Errorf("c opened %T although neither query found a candidate", *modal)
	}
	if len(notes) == 0 {
		t.Fatal("the empty result was not reported")
	}
	note := notes[len(notes)-1]
	for _, want := range []string{"statement period", "disbursal date"} {
		if !strings.Contains(note, want) {
			t.Errorf("the empty result %q does not point at %q", note, want)
		}
	}
}

// TestTransferFormSendsTheChosenMode covers the mode select: takeover is sent as
// mode and suppresses the target terms, the automatic choice leaves mode off the
// wire so the API picks between recast and opens, and a mode that needs a target
// schedule is refused before anything is posted.
func TestTransferFormSendsTheChosenMode(t *testing.T) {
	withSchedule := loanScheduleJSON("acct-2", `{"sanctioned":100000.00,"net":98000.00,"verified":false,"difference":0}`)
	withoutSchedule := `{"schedule":null,"entries":[]}`

	for _, tc := range []struct {
		name     string
		target   string
		picks    int
		wantMode string
		wantPost bool
	}{
		{name: "automatic", target: withSchedule, wantPost: true},
		{name: "takeover", target: withSchedule, picks: 2, wantMode: "takeover", wantPost: true},
		{name: "takeover without a target schedule", target: withoutSchedule, picks: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			posted := make(chan map[string]any, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/acct-2/loan-schedule":
					fmt.Fprint(w, tc.target)
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/acct-1/loan-payoff":
					fmt.Fprint(w, payoffJSON)
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/accounts/acct-1/loan-transfer":
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decoding the transfer body: %v", err)
					}
					posted <- body
					fmt.Fprint(w, `{"transfer":{},"source":{"schedule":null,"entries":[]},"target":{"schedule":null,"entries":[]}}`)
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
			source := api.Account{ID: "acct-1", Name: "Car loan", AccountTypeID: "loan"}
			target := api.Account{ID: "acct-2", Name: "Home loan", AccountTypeID: "loan"}
			a, modal := testAccounts(t, client, source, target)

			a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
			form, ok := (*modal).(*Form)
			if !ok {
				t.Fatalf("t did not open a form (got %T)", *modal)
			}
			// Mode is the second field: the automatic choice is its empty value
			// and each right press steps to the next option.
			form.Update(tea.KeyMsg{Type: tea.KeyTab})
			for range tc.picks {
				form.Update(tea.KeyMsg{Type: tea.KeyRight})
			}
			run(t, a, form.Update(tea.KeyMsg{Type: tea.KeyCtrlS}))

			var body map[string]any
			select {
			case body = <-posted:
			default:
				if tc.wantPost {
					t.Fatal("no balance transfer was posted")
				}
				return
			}
			if !tc.wantPost {
				t.Fatal("a transfer was posted although the mode needs a target schedule")
			}
			mode, sent := body["mode"]
			if tc.wantMode == "" {
				if sent {
					t.Errorf("mode = %v was sent, want it left off for the automatic choice", mode)
				}
			} else if mode != tc.wantMode {
				t.Errorf("mode = %v, want %s", mode, tc.wantMode)
			}
			if _, sent := body["targetStartDate"]; sent {
				t.Errorf("targetStartDate was sent with a target that has a schedule: %v", body)
			}
		})
	}
}

// TestDisbursementCreditKeyWithoutASchedule covers the guard: a loan with no
// terms has no disbursement to reconcile, so the key reports that instead of
// listing credits (and must not dereference the absent schedule).
func TestDisbursementCreditKeyWithoutASchedule(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/acct-1/loan-schedule" {
			fmt.Fprint(w, `{"schedule":null,"entries":[]}`)
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	client, err := api.New(srv.URL + "/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	a, modal := testAccounts(t, client, api.Account{ID: "acct-1", Name: "Car loan", AccountTypeID: "loan"})

	run(t, a, a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")}))
	if *modal != nil {
		t.Errorf("c opened %T for a loan with no schedule", *modal)
	}
}

// TestTransferFormQuotesThePayoffBeforePosting covers the first step the transfer
// form now takes: the payoff the source is settled at is quoted for the entered
// transfer date, and only then is the transfer posted — the price is the
// outstanding principal plus the interest accrued from the last EMI payment, and
// only the API computes it. The breakdown the quote returned is what the success
// message reports, since the screen has no other way to know what moved.
func TestTransferFormQuotesThePayoffBeforePosting(t *testing.T) {
	quoted := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/acct-2/loan-schedule":
			fmt.Fprint(w, loanScheduleJSON("acct-2", `{"sanctioned":100000.00,"net":98000.00,"verified":false,"difference":0}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/acct-1/loan-payoff":
			quoted <- r.URL.Query().Get("date")
			fmt.Fprint(w, payoffJSON)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/accounts/acct-1/loan-transfer":
			select {
			case date := <-quoted:
				if date != "2026-02-01" {
					t.Errorf("the payoff was quoted for %q, want the entered transfer date 2026-02-01", date)
				}
			default:
				t.Error("the balance transfer was posted before the payoff was quoted")
			}
			fmt.Fprint(w, `{"transfer":{},"source":{"schedule":null,"entries":[]},"target":{"schedule":null,"entries":[]}}`)
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
	source := api.Account{ID: "acct-1", Name: "Car loan", AccountTypeID: "loan"}
	target := api.Account{ID: "acct-2", Name: "Home loan", AccountTypeID: "loan"}
	a, modal := testAccounts(t, client, source, target)

	a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	form, ok := (*modal).(*Form)
	if !ok {
		t.Fatalf("t did not open a form (got %T)", *modal)
	}
	// Walk to the transfer date, which starts at today, and replace it with a
	// date of the test's own so the quote proves it read the field.
	form.Update(tea.KeyMsg{Type: tea.KeyTab})
	form.Update(tea.KeyMsg{Type: tea.KeyTab})
	for range nowDate() {
		form.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	for _, r := range "2026-02-01" {
		form.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	cmd := form.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if cmd == nil {
		t.Fatal("submitting the transfer produced no command: the form did not validate")
	}
	settled, ok := cmd().(done)
	if !ok {
		t.Fatalf("submitting the transfer did not report a done message: %T", cmd())
	}
	if settled.err != nil {
		t.Fatalf("the transfer failed: %v", settled.err)
	}
	const want = "settled 1,982,924.71 (principal 1,956,632.46 + interest 26,292.25)"
	if settled.note != want {
		t.Errorf("the success message is %q, want %q", settled.note, want)
	}
	if !settled.invalidate {
		t.Error("the transfer did not mark the shared reference data stale")
	}
}

// TestTransferFormReportsAFailedQuoteAndDoesNotPost covers the other half of the
// new step: when the source cannot be quoted — it has no schedule, or a transfer
// already settled it — the API's error is reported and no transfer is posted,
// because there is no figure to settle at.
func TestTransferFormReportsAFailedQuoteAndDoesNotPost(t *testing.T) {
	posted := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/acct-2/loan-schedule":
			fmt.Fprint(w, loanScheduleJSON("acct-2", `{"sanctioned":100000.00,"net":98000.00,"verified":false,"difference":0}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/acct-1/loan-payoff":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"errors":[{"field":"date","message":"the loan has no schedule to settle"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/accounts/acct-1/loan-transfer":
			posted <- struct{}{}
			fmt.Fprint(w, `{"transfer":{},"source":{"schedule":null,"entries":[]},"target":{"schedule":null,"entries":[]}}`)
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
	source := api.Account{ID: "acct-1", Name: "Car loan", AccountTypeID: "loan"}
	target := api.Account{ID: "acct-2", Name: "Home loan", AccountTypeID: "loan"}
	a, modal := testAccounts(t, client, source, target)

	a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	form, ok := (*modal).(*Form)
	if !ok {
		t.Fatalf("t did not open a form (got %T)", *modal)
	}
	cmd := form.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if cmd == nil {
		t.Fatal("submitting the transfer produced no command: the form did not validate")
	}
	failed, ok := cmd().(done)
	if !ok {
		t.Fatalf("submitting the transfer did not report a done message: %T", cmd())
	}
	if failed.err == nil {
		t.Fatal("an unquotable loan was reported as settled")
	}
	if !strings.Contains(failed.err.Error(), "no schedule to settle") {
		t.Errorf("the failure reported %q, want the API's quote error", failed.err)
	}
	if failed.note != "" {
		t.Errorf("a failed transfer reported %q as settled", failed.note)
	}
	select {
	case <-posted:
		t.Error("the balance transfer was posted although the payoff could not be quoted")
	default:
	}
}

// TestDisbursementCreditLinksTheLoanItsCandidatesWereFetchedFor is a regression
// test: the picker's link read the screen's current loan when it was opened, so
// pressing `c` on a second loan while the first one's candidates were still in
// flight linked the first loan's bank credit to the second loan.
func TestDisbursementCreditLinksTheLoanItsCandidatesWereFetchedFor(t *testing.T) {
	const (
		unlinked = `{"sanctioned":100000.00,"processingFee":2000.00,"paidOut":0,"net":98000.00,` +
			`"verified":false,"difference":0}`
		credit = `{"id":"txn-9","accountId":"bank-1","date":"2026-01-06T00:00:00Z",` +
			`"description":"Loan disbursement","amount":98000.00,"type":"credit","tags":[],"accountName":"Everyday"}`
	)
	linked := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/accounts/") &&
			strings.HasSuffix(r.URL.Path, "/loan-schedule"):
			account := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/accounts/"), "/loan-schedule")
			fmt.Fprint(w, loanScheduleJSON(account, unlinked))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/transactions":
			fmt.Fprintf(w, `{"data":[%s],"total":1,"page":1,"limit":100,"pages":1}`, credit)
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/loan-disbursement"):
			linked <- r.URL.Path
			fmt.Fprint(w, loanScheduleJSON("acct-1", unlinked))
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
	a, modal := testAccounts(t, client,
		api.Account{ID: "acct-1", Name: "Car loan", AccountTypeID: "loan"},
		api.Account{ID: "acct-2", Name: "Bike loan", AccountTypeID: "loan"})

	// `c` on the car loan fetches its schedule, whose answer starts the search
	// for its candidate credits. That search is not delivered yet.
	schedule := a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if schedule == nil {
		t.Fatal("c did not start a schedule fetch")
	}
	candidates := a.Update(schedule())
	if candidates == nil {
		t.Fatal("the car loan's schedule did not start a candidate search")
	}

	// The user has meanwhile pressed `c` on the bike loan.
	a.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if next := a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")}); next == nil {
		t.Fatal("c on the bike loan did not start a schedule fetch")
	}

	// The car loan's candidates land: the picker is still the car loan's, and so
	// is the credit it links.
	run(t, a, candidates)
	picker, ok := (*modal).(*creditPicker)
	if !ok {
		t.Fatalf("the car loan's candidates did not open a credit picker (got %T)", *modal)
	}
	if view := picker.View(a.ctx.Theme, 120, 20); !strings.Contains(view, "Car loan") || strings.Contains(view, "Bike loan") {
		t.Errorf("the picker is not about the loan whose candidates were fetched:\n%s", view)
	}

	run(t, a, picker.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	select {
	case path := <-linked:
		if path != "/api/v1/accounts/acct-1/loan-disbursement" {
			t.Errorf("linked through %s, want /api/v1/accounts/acct-1/loan-disbursement", path)
		}
	default:
		t.Fatal("no disbursement credit was linked")
	}
}
