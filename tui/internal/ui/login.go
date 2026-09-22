package ui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fintrak/client/api"
)

// LoginModel is the sign-in screen. It reuses the modal form so validation and
// key handling behave exactly like every other editor in the client.
//
// The TUI cannot read the browser's session cookie, so a terminal session
// authenticates with the same credentials the web UI accepts; the tokens come
// back as Set-Cookie headers and the client holds them (see internal/api).
type LoginModel struct {
	client   *api.Client
	mode     string
	login    *Form
	register *Form

	user       api.User
	err        error
	done       bool
	registered bool
}

// loginTag is the tag both forms submit under, so one handler covers them.
const loginTag = "login"

// NewLoginModel builds the sign-in screen in login mode.
func NewLoginModel(client *api.Client) *LoginModel {
	m := &LoginModel{client: client, mode: "login"}
	m.login = m.buildLoginForm()
	m.register = m.buildRegisterForm()
	return m
}

// buildLoginForm is the email/password form.
func (m *LoginModel) buildLoginForm() *Form {
	fields := []Field{
		{
			Label: "Email", Kind: FieldText, Width: 36,
			Validate: required("email"),
		},
		{
			Label: "Password", Kind: FieldPassword, Width: 36,
			Validate: required("password"),
		},
	}
	return NewForm(loginTag, "Sign in to FinTrak", fields, func(f *Form) tea.Cmd {
		email, password := f.Value("Email"), f.Value("Password")
		m.err = nil
		return load(loginTag, func(ctx context.Context) (api.User, error) {
			return m.client.Login(ctx, email, password)
		})
	})
}

// buildRegisterForm adds the operator bootstrap token, which the API requires
// only when the email is listed in ADMIN_EMAILS.
func (m *LoginModel) buildRegisterForm() *Form {
	fields := []Field{
		{
			Label: "Email", Kind: FieldText, Width: 36,
			Validate: required("email"),
		},
		{
			Label: "Password", Kind: FieldPassword, Width: 36,
			Validate: func(v string) error {
				if len(strings.TrimSpace(v)) < 12 {
					return errText("at least 12 characters")
				}
				if len(v) > 72 {
					return errText("at most 72 bytes")
				}
				return nil
			},
			Help: "at least 12 characters",
		},
		{
			Label: "Setup token", Kind: FieldText, Width: 36,
			Help: "only needed for an address listed in ADMIN_EMAILS",
		},
	}
	return NewForm(loginTag, "Create a FinTrak account", fields, func(f *Form) tea.Cmd {
		email, password, token := f.Value("Email"), f.Value("Password"), f.Value("Setup token")
		m.err = nil
		return load(loginTag, func(ctx context.Context) (api.User, error) {
			return m.client.Register(ctx, email, password, token)
		})
	})
}

// form returns the active form.
func (m *LoginModel) form() *Form {
	if m.mode == "register" {
		return m.register
	}
	return m.login
}

// Init focuses the email field through the form's own update path.
func (m *LoginModel) Init() tea.Cmd { return nil }

// Update handles keys and the login response.
func (m *LoginModel) Update(msg tea.Msg) tea.Cmd {
	if loadedUser, ok := msg.(loaded[api.User]); ok && loadedUser.tag == loginTag {
		if loadedUser.err != nil {
			m.err = loadedUser.err
			m.form().SetError(loadedUser.err)
			return nil
		}
		m.user = loadedUser.data
		m.registered = m.mode == "register"
		m.done = true
		return nil
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+r", "f2":
			if m.mode == "login" {
				m.mode = "register"
			} else {
				m.mode = "login"
			}
			m.err = nil
			return nil
		}
	}
	return m.form().Update(msg)
}

// Done reports whether authentication succeeded.
func (m *LoginModel) Done() bool { return m.done }

// Err returns the last failure, if any.
func (m *LoginModel) Err() error { return m.err }

// User returns the authenticated user.
func (m *LoginModel) User() api.User { return m.user }

// Registered reports whether the session came from a registration rather than a
// sign-in, so the status line can say "account created".
func (m *LoginModel) Registered() bool { return m.registered }

// Reset returns the screen to a clean sign-in state after a sign-out.
func (m *LoginModel) Reset() {
	m.login = m.buildLoginForm()
	m.register = m.buildRegisterForm()
	m.mode = "login"
	m.user, m.err, m.done, m.registered = api.User{}, nil, false, false
}

// View renders the sign-in card.
func (m *LoginModel) View(th Theme, width, height int) string {
	var footer string
	switch m.mode {
	case "register":
		footer = th.Subtle.Render("already have an account? ctrl+r to sign in")
	default:
		footer = th.Subtle.Render("no account? ctrl+r to register")
	}

	// The height used to be dropped here (the form was handed 0), which left the
	// two-field sign-in form showing a single field.
	formHeight := height - 2 // the blank line and the footer
	if formHeight < 1 {
		footer, formHeight = "", max(1, height)
	}
	body := m.form().View(th, width, formHeight)
	if footer == "" {
		return body
	}
	return body + "\n\n" + footer
}

// required builds a non-empty validator with a friendly message.
func required(name string) func(string) error {
	return func(v string) error {
		if strings.TrimSpace(v) == "" {
			return errText(name + " is required")
		}
		return nil
	}
}

// errText is a tiny error type so validators read cleanly.
type errText string

func (e errText) Error() string { return string(e) }
