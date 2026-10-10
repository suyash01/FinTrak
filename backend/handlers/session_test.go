package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The user-facing session surface: list the caller's own sessions, end one,
// end all the others, end everything.
//
// Two SQL shapes carry the whole authorization boundary here and are asserted
// ANCHORED (`^...$`) throughout. pgxmock's default matcher compiles a pattern as
// an unanchored regexp, so an expectation for "UPDATE sessions SET revoked_at"
// would happily match a statement with no user predicate at all — which is
// exactly the bug these tests exist to catch: revoking every session in the
// table, or another user's session, while still answering 200.

// newSessionTestRouter builds a router whose requests carry both halves of the
// authenticated identity RequireAuth publishes: the user id, and the claims
// naming the caller's own session (which is the only place that session is
// known — a revoke route has no path or query parameter for it).
func newSessionTestRouter(srv *Server, sessionID uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", testUserID())
		c.Set("authClaims", &auth.Claims{UserID: testUserID(), Role: "user", SessionID: sessionID})
		c.Next()
	})
	return r
}

// listSessionsSQL is the one SELECT the session list issues, anchored so the
// user predicate and the "not revoked" filter are both part of what is asserted.
const listSessionsSQL = `^SELECT id, user_agent, created_at, last_seen, expires_at FROM sessions ` +
	`WHERE user_id = \$1 AND revoked_at IS NULL ORDER BY last_seen DESC$`

// expectSessionOwnership registers the read that answers whether the named
// session is the caller's at all. It is a read rather than an inference from the
// update's row count because "already revoked" and "not yours" update the same
// number of rows and deserve different answers.
func expectSessionOwnership(mock pgxmock.PgxPoolIface, sessionID, userID uuid.UUID, found bool) {
	rows := pgxmock.NewRows([]string{"exists"})
	if found {
		rows.AddRow(true)
	}
	mock.ExpectQuery(`^SELECT true FROM sessions WHERE id = \$1 AND user_id = \$2$`).
		WithArgs(sessionID, userID).
		WillReturnRows(rows)
}

// expectFamilyRevoke registers the revocation of a session's whole rotation
// family, anchored: an unbounded match here would accept a statement that
// revokes every refresh token in the table.
func expectFamilyRevoke(mock pgxmock.PgxPoolIface, familyID uuid.UUID) {
	mock.ExpectExec(`^UPDATE refresh_tokens SET revoked_at = now\(\) WHERE family_id = \$1 AND revoked_at IS NULL$`).
		WithArgs(familyID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))
}

// expectRevokeOneSession registers the revocation of one row, anchored. The
// user predicate is inside the assertion: `WHERE user_id = $1` alone would
// revoke every session the user has, and `WHERE id = $1` alone would revoke
// anyone's — both of which still answer 200.
func expectRevokeOneSession(mock pgxmock.PgxPoolIface, sessionID, userID uuid.UUID, rows int64) {
	mock.ExpectExec(`^UPDATE sessions SET revoked_at = now\(\) WHERE id = \$1 AND user_id = \$2 AND revoked_at IS NULL$`).
		WithArgs(sessionID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", rows))
}

// sessionRows builds the SELECT's result set.
func sessionRows(entries ...models.Session) *pgxmock.Rows {
	rows := pgxmock.NewRows([]string{"id", "user_agent", "created_at", "last_seen", "expires_at"})
	for _, s := range entries {
		rows.AddRow(s.ID, s.UserAgent, s.CreatedAt, s.LastSeen, s.ExpiresAt)
	}
	return rows
}

func sessionFixture(id uuid.UUID, agent string) models.Session {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return models.Session{
		ID:        id,
		UserAgent: agent,
		CreatedAt: base,
		LastSeen:  base.Add(time.Hour),
		ExpiresAt: base.AddDate(0, 1, 0),
	}
}

func TestListSessionsReturnsTheCallersOwnSessionsFlaggingTheCurrent(t *testing.T) {
	srv, mock := newMockServer(t)
	current := uuid.New()
	other := uuid.New()
	r := newSessionTestRouter(srv, current)
	r.GET("/auth/sessions", srv.ListSessions)

	mock.ExpectQuery(listSessionsSQL).
		WithArgs(testUserID()).
		WillReturnRows(sessionRows(
			sessionFixture(current, "ThisDevice/1.0"),
			sessionFixture(other, "OtherDevice/2.0"),
		))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/sessions", nil))

	require.Equal(t, http.StatusOK, w.Code)
	var got []models.Session
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got, 2)

	byID := map[uuid.UUID]models.Session{}
	for _, s := range got {
		byID[s.ID] = s
	}
	// The caller's own row is flagged, and only that row: the token's sid is the
	// only place the caller's session is known, so getting this wrong either
	// hides the row the UI labels "This device" or labels a stranger's as it.
	assert.True(t, byID[current].Current, "the token's own session must be flagged current")
	assert.False(t, byID[other].Current, "another session must not be flagged current")

	// The list still shows everything needed to identify a device.
	assert.Equal(t, "ThisDevice/1.0", byID[current].UserAgent)
	assert.Equal(t, "OtherDevice/2.0", byID[other].UserAgent)
	assert.False(t, byID[other].ExpiresAt.IsZero())

	assert.NoError(t, mock.ExpectationsWereMet())
}

// The list is scoped in SQL, not by filtering rows afterwards: a query that did
// not bind the user id would return every session in the table.
func TestListSessionsDoesNotReturnAnotherUsersSessions(t *testing.T) {
	srv, mock := newMockServer(t)
	r := newSessionTestRouter(srv, uuid.New())
	r.GET("/auth/sessions", srv.ListSessions)

	// Anchored, and the only argument is the caller's id.
	mock.ExpectQuery(listSessionsSQL).
		WithArgs(testUserID()).
		WillReturnRows(sessionRows())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/sessions", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	var got []models.Session
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Empty(t, got, "an empty list is [], never null")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Revoking a session that is not the caller's is a 404, and the write never
// runs: this registers no ExpectExec, so an UPDATE issued anyway fails the test
// on the unexpected call rather than quietly revoking someone else's session.
func TestRevokeSessionIsScopedToTheCaller(t *testing.T) {
	srv, mock := newMockServer(t)
	foreign := uuid.New()
	r := newSessionTestRouter(srv, uuid.New())
	r.DELETE("/auth/sessions/:id", srv.RevokeSession)

	expectSessionOwnership(mock, foreign, testUserID(), false)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/auth/sessions/"+foreign.String(), nil))

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The UPDATE names the row AND its owner. A `WHERE user_id = $1` alone would
// revoke every session the user has and still answer 200; the assertion is on
// the exact statement and its exact arguments because nothing else here can
// tell those apart.
func TestRevokeSessionLeavesOtherSessionsAlone(t *testing.T) {
	srv, mock := newMockServer(t)
	target := uuid.New()
	r := newSessionTestRouter(srv, uuid.New())
	r.DELETE("/auth/sessions/:id", srv.RevokeSession)

	expectSessionOwnership(mock, target, testUserID(), true)
	expectFamilyRevoke(mock, target)
	expectRevokeOneSession(mock, target, testUserID(), 1)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/auth/sessions/"+target.String(), nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Revoking the caller's own session is the same action as revoking a device
// they do not recognise ("revoke this device" and "log me out here"), so it is
// allowed rather than special-cased out.
func TestRevokeSessionAcceptsTheCallersOwnSession(t *testing.T) {
	srv, mock := newMockServer(t)
	current := uuid.New()
	r := newSessionTestRouter(srv, current)
	r.DELETE("/auth/sessions/:id", srv.RevokeSession)

	expectSessionOwnership(mock, current, testUserID(), true)
	expectFamilyRevoke(mock, current)
	expectRevokeOneSession(mock, current, testUserID(), 1)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/auth/sessions/"+current.String(), nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Revoking twice is a success: 0 rows is what an already-revoked session looks
// like, and answering 500 would make a retried tap look like a server fault.
func TestRevokeSessionIsIdempotent(t *testing.T) {
	srv, mock := newMockServer(t)
	target := uuid.New()
	r := newSessionTestRouter(srv, uuid.New())
	r.DELETE("/auth/sessions/:id", srv.RevokeSession)

	expectSessionOwnership(mock, target, testUserID(), true)
	expectFamilyRevoke(mock, target)
	expectRevokeOneSession(mock, target, testUserID(), 0)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/auth/sessions/"+target.String(), nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A refresh token whose session was revoked must not keep rotating into fresh
// token pairs naming a dead session — the user would see an unexplained 401
// loop instead of a clean expiry. The family is therefore revoked first, and a
// failure to do so is reported rather than logged: it means the session was not
// actually ended.
func TestRevokeSessionRefusesToReportSuccessWhenTheFamilySurvives(t *testing.T) {
	srv, mock := newMockServer(t)
	target := uuid.New()
	r := newSessionTestRouter(srv, uuid.New())
	r.DELETE("/auth/sessions/:id", srv.RevokeSession)

	expectSessionOwnership(mock, target, testUserID(), true)
	mock.ExpectExec(`^UPDATE refresh_tokens SET revoked_at = now\(\) WHERE family_id = \$1 AND revoked_at IS NULL$`).
		WithArgs(target).
		WillReturnError(assert.AnError)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/auth/sessions/"+target.String(), nil))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet(), "the row must not be revoked when the family survives")
}

func TestRevokeSessionRejectsAMalformedID(t *testing.T) {
	srv, mock := newMockServer(t)
	r := newSessionTestRouter(srv, uuid.New())
	r.DELETE("/auth/sessions/:id", srv.RevokeSession)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/auth/sessions/not-a-uuid", nil))

	// A nil pool is the point: the parse must fail before anything is written.
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// "Sign out everywhere else" must not sign the caller out of the tab they
// clicked it in.
func TestRevokeOthersLeavesTheCallersSessionLive(t *testing.T) {
	srv, mock := newMockServer(t)
	current, stolen := uuid.New(), uuid.New()
	r := newSessionTestRouter(srv, current)
	r.DELETE("/auth/sessions", srv.RevokeOtherSessions)

	// Anchored: the exclusion is the whole point of this route, and its absence
	// is invisible in the response — both answer 200 with the same body.
	mock.ExpectQuery(`^UPDATE sessions SET revoked_at = now\(\) WHERE user_id = \$1 AND revoked_at IS NULL `+
		`AND expires_at > now\(\) AND id <> \$2 RETURNING id$`).
		WithArgs(testUserID(), current).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(stolen))
	// The row that was revoked must have its family revoked with it: sessions.id
	// IS refresh_tokens.family_id.
	expectFamilyRevoke(mock, stolen)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/auth/sessions", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// "I have been compromised" takes the caller's own session too — that is the
// difference between the two bulk routes, and collapsing them would make one of
// them the wrong answer.
func TestRevokeAllIncludesTheCallersSession(t *testing.T) {
	srv, mock := newMockServer(t)
	current := uuid.New()
	r := newSessionTestRouter(srv, current)
	r.DELETE("/auth/sessions/current", srv.RevokeAllSessions)

	mock.ExpectQuery(`^UPDATE sessions SET revoked_at = now\(\) WHERE user_id = \$1 AND revoked_at IS NULL ` +
		`AND expires_at > now\(\) RETURNING id$`).
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(current))
	expectFamilyRevoke(mock, current)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/auth/sessions/current", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// DELETE /auth/sessions/current is the "I have been compromised" action, and it
// has to reach the bulk handler: bound to the :id route instead it would fail
// the uuid parse and revoke nothing at all, while answering 400 — a user who
// believes they just signed out everywhere still signed in everywhere.
//
// gin resolves a static segment ahead of a parameter at the same level (and
// either registration order accepts both), so this is not a claim about
// registration order. It is a claim that the literal route is registered and
// reachable, which is what the unmet-expectation check below holds.
func TestCurrentIsServedByTheBulkRoute(t *testing.T) {
	srv, mock := newMockServer(t)
	r := newSessionTestRouter(srv, uuid.New())
	r.DELETE("/auth/sessions", srv.RevokeOtherSessions)
	r.DELETE("/auth/sessions/current", srv.RevokeAllSessions)
	r.DELETE("/auth/sessions/:id", srv.RevokeSession)

	mock.ExpectQuery(`^UPDATE sessions SET revoked_at = now\(\) WHERE user_id = \$1 AND revoked_at IS NULL ` +
		`AND expires_at > now\(\) RETURNING id$`).
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/auth/sessions/current", nil))

	assert.NotEqual(t, http.StatusBadRequest, w.Code, "the literal path reached the :id handler")
	assert.NoError(t, mock.ExpectationsWereMet(), "the bulk revoke, not the :id parse, handled the request")
}
