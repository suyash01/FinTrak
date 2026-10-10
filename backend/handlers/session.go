package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The user-facing session surface: what a person can see and end. Four routes,
// three shapes of question:
//
//	GET    /auth/sessions           which devices am I signed in on?
//	DELETE /auth/sessions/:id       end one of them
//	DELETE /auth/sessions           end every one except this one
//	DELETE /auth/sessions/current   end every one, this one included
//
// The last two are the same handler with a bool because they are the same
// action with a different intent: "that laptop is gone" and "I have been
// compromised" are not the same button, and collapsing them would force one of
// them to be the wrong answer.
//
// Revoking the caller's OWN session by id is allowed rather than refused
// (spec §6): "revoke this device" and "log me out here" are the same thing, and
// special-casing the current session out of its own route would leave the tab
// the user acted from continuing while the UI claims the session was revoked.

// callerSessionID returns the session the caller's own access token names, or
// uuid.Nil for a token minted before sessions existed (spec §4). It is the only
// place the caller's own session is known: no route carries it as a parameter,
// and no column stores it.
func callerSessionID(c *gin.Context) uuid.UUID {
	if claims := auth.GetClaims(c); claims != nil {
		return claims.SessionID
	}
	return uuid.Nil
}

// ListSessions returns the caller's own sessions, most recently seen first,
// flagging the one the request was made from.
//
// Expired sessions are listed rather than hidden: a session the user cannot see
// is one whose absence they cannot explain, and a row that vanishes on its own
// is exactly what makes a security panel untrustworthy. They are still
// revokable — only the bulk routes skip them.
//
// This is a pure read. last_seen is written on the refresh path alone
// (spec §5), because RequireAuth runs on every authenticated request and
// stamping it here would turn every authenticated GET into an UPDATE.
func (srv *Server) ListSessions(c *gin.Context) {
	rows, err := srv.db.Query(c,
		`SELECT id, user_agent, created_at, last_seen, expires_at FROM sessions WHERE user_id = $1 AND revoked_at IS NULL ORDER BY last_seen DESC`,
		auth.GetUserID(c),
	)
	if err != nil {
		slog.Error("ListSessions", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// Resolved once, outside the loop: the claim is the only thing that
	// distinguishes the caller's own device from the others, and a per-row
	// lookup of it would be the same answer every time.
	current := callerSessionID(c)

	sessions := []models.Session{}
	for rows.Next() {
		var s models.Session
		if err := rows.Scan(&s.ID, &s.UserAgent, &s.CreatedAt, &s.LastSeen, &s.ExpiresAt); err != nil {
			slog.Error("ListSessions scan", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		s.Current = s.ID == current
		sessions = append(sessions, s)
	}
	if err := rows.Err(); err != nil {
		slog.Error("ListSessions rows", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, sessions)
}

// RevokeSession ends one session: the caller's own rotation family and the row
// RequireAuth reads. Both halves, because neither is sufficient alone — the
// family alone leaves an already-issued access token good for up to a quarter
// of an hour, and the row alone leaves a refresh token that mints fresh tokens
// naming a session that no longer exists.
//
// The family is revoked FIRST and a failure to do so is reported rather than
// logged: at that point nothing has been written, so the caller can retry
// safely, whereas answering 200 over a live refresh family is the 401 loop
// described on RevokeSessions' helper — a client refreshing forever against a
// session every request refuses.
//
// Revoking an already-revoked session is a success, not a 404 or a 500: the
// state the caller asked for holds, and a retried tap must not read as a fault.
// It is therefore not the row count that decides the answer — an UPDATE scoped
// to the owner matches "already revoked" and "not yours" alike, and those
// deserve different answers, so ownership is read first.
func (srv *Server) RevokeSession(c *gin.Context) {
	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid session id", http.StatusBadRequest)
		return
	}

	var exists bool
	err = srv.db.QueryRow(c,
		`SELECT true FROM sessions WHERE id = $1 AND user_id = $2`,
		sessionID, auth.GetUserID(c),
	).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		// Not this user's session, or no such session. The two are deliberately
		// indistinguishable: the id is a UUID, so the answer confirms nothing
		// about anyone else's sessions either way.
		validation.RespondError(c, "session not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("RevokeSession (lookup)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// sessions.id IS refresh_tokens.family_id, so one value addresses both
	// halves.
	if err := srv.revokeRefreshFamily(c, sessionID); err != nil {
		slog.Error("RevokeSession: revoking refresh family", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// User-scoped: a `WHERE user_id = $1` alone would end every session the
	// user has, and no user_id at all would end other people's, both answering
	// 200. Rows affected is deliberately not inspected (see above).
	_, err = srv.db.Exec(c,
		`UPDATE sessions SET revoked_at = now() WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		sessionID, auth.GetUserID(c),
	)
	if err != nil {
		slog.Error("RevokeSession", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "session revoked"})
}

// RevokeOtherSessions ends every session except the one making the request —
// "sign out everywhere else". The caller stays signed in here, which is the
// entire difference from RevokeAllSessions.
func (srv *Server) RevokeOtherSessions(c *gin.Context) {
	srv.RevokeSessions(c, true)
}

// RevokeAllSessions ends every session including the caller's own — "I have
// been compromised". The response is still 200: the revocation is what the
// caller asked for, and their next request is answered by the session check
// RequireAuth already performs.
func (srv *Server) RevokeAllSessions(c *gin.Context) {
	srv.RevokeSessions(c, false)
}

// RevokeSessions is the shared body of the two bulk routes, parameterised by
// whether the caller's own session survives.
//
// The expires_at term is what separates "bulk revoke" from "list": an expired
// session is already dead — its refresh tokens are past their deadline and its
// access tokens are long expired — so revoking it would be bookkeeping about
// nothing. A single revoke by id deliberately does NOT carry that term: the
// session is still listed, and a user who wants it gone should be able to say
// so.
//
// Each affected family is revoked by id from the UPDATE's own RETURNING, so a
// family belonging to a session that was already revoked, expired, or someone
// else's is never touched. The row is gone by the time that runs, so a family
// revoke that fails here is logged rather than reported: the sessions ARE ended
// from the access token's side, the request cannot be undone by retrying, and
// the family dies with the refresh token it would otherwise be spent on.
func (srv *Server) RevokeSessions(c *gin.Context, exceptCurrent bool) {
	query := `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()`
	args := []any{auth.GetUserID(c)}
	if exceptCurrent {
		// The caller's own session is excluded by id, which is the only thing
		// that can spare it — so a caller whose token names NO session gets no
		// exclusion that matches. That is reachable: GenerateAccessToken mints
		// sid = uuid.Nil, only Refresh fills it in, and migration 000002
		// backfilled a live row for every family that predates the sessions
		// table. Such a user who has not refreshed since the deploy presents a
		// nil sid while owning a real, live session, and "sign out everywhere
		// else" then revokes it along with the rest.
		//
		// That is the right direction to fail. With no sid there is no way to
		// know which session is the caller's, and the two mistakes are not
		// equivalent: revoking a session the user asked to be rid of costs one
		// sign-in, while sparing one leaves a session alive on a route whose
		// whole purpose is to end them.
		query += ` AND id <> $2`
		args = append(args, callerSessionID(c))
	}
	query += ` RETURNING id`

	rows, err := srv.db.Query(c, query, args...)
	if err != nil {
		slog.Error("RevokeSessions", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	revoked := 0
	for rows.Next() {
		var familyID uuid.UUID
		if err := rows.Scan(&familyID); err != nil {
			slog.Error("RevokeSessions scan", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		revoked++
		if err := srv.revokeRefreshFamily(c, familyID); err != nil {
			slog.Error("RevokeSessions: revoking refresh family", slog.String("error", err.Error()))
		}
	}
	if err := rows.Err(); err != nil {
		slog.Error("RevokeSessions rows", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	slog.Info("sessions revoked", slog.Int("sessions", revoked))
	c.JSON(http.StatusOK, gin.H{"message": "sessions revoked"})
}
