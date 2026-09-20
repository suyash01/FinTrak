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

	"github.com/fintrak/tui/internal/api"
)

// TestLoanScheduleTextShowsFeeTransfersAndEntryStates pins the loan schedule
// overlay's new surface: the processing fee recorded for reference, the
// transfers with the side this loan was on, and the state of an installment a
// transfer cancelled or recast.
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
			},
			{
				ID: "tr2", FromLoanAccountID: "acct-3", FromLoanAccountName: "Bike loan",
				ToLoanAccountID: "acct-1", Amount: "5000.00", TransferDate: date(2026, 3, 1),
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
			// Fill the target terms in, walking past the target account and the
			// transfer date (which defaults to today, a valid date).
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
