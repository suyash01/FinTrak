package handlers

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fintrak/backend/internal/crypto"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPaperlessQueryInt(t *testing.T) {
	queryCtx := func(rawURL string) *gin.Context {
		gin.SetMode(gin.TestMode)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, rawURL, nil)
		return c
	}

	assert.Equal(t, 25, paperlessQueryInt(queryCtx("/"), "page_size", 25, 1, 100))
	assert.Equal(t, 5, paperlessQueryInt(queryCtx("/?page_size=5"), "page_size", 25, 1, 100))
	assert.Equal(t, 25, paperlessQueryInt(queryCtx("/?page_size=abc"), "page_size", 25, 1, 100))
	assert.Equal(t, 25, paperlessQueryInt(queryCtx("/?page_size=0"), "page_size", 25, 1, 100))
	assert.Equal(t, 100, paperlessQueryInt(queryCtx("/?page_size=9999"), "page_size", 25, 1, 100))
	assert.Equal(t, 25, paperlessQueryInt(queryCtx("/?page_size=-3"), "page_size", 25, 1, 100))
}

func TestPaperlessOriginErrors(t *testing.T) {
	_, err := paperlessOrigin(models.UserSettings{PaperlessURL: "ftp://paperless.local"})
	assert.Error(t, err)

	_, err = paperlessOrigin(models.UserSettings{PaperlessURL: "http://"})
	assert.Error(t, err)

	o, err := paperlessOrigin(models.UserSettings{PaperlessURL: "http://paperless.local/"})
	require.NoError(t, err)
	assert.Equal(t, "http://paperless.local", o)
}

func TestIsDisallowedPaperlessIPNil(t *testing.T) {
	assert.True(t, isDisallowedPaperlessIP(nil, true))
}

func TestPaperlessTokenErrors(t *testing.T) {
	tok, err := paperlessToken(context.Background(), models.UserSettings{}, "key")
	require.NoError(t, err)
	assert.Equal(t, "", tok)

	_, err = paperlessToken(context.Background(), models.UserSettings{PaperlessToken: crypto.PrefixV2 + "!!!"}, "key")
	assert.Error(t, err)
}

func TestUpgradeLegacyToken(t *testing.T) {
	userID := testUserID()

	t.Run("plaintext is returned unchanged", func(t *testing.T) {
		prev := tokenEncryptionKey
		tokenEncryptionKey = "key"
		t.Cleanup(func() { tokenEncryptionKey = prev })

		srv, _ := setupPaperlessMock(t, "", "")
		assert.Equal(t, "plain-token", srv.upgradeLegacyToken(context.Background(), userID, "plain-token"))
	})

	t.Run("no configured key leaves the token untouched", func(t *testing.T) {
		prev := tokenEncryptionKey
		tokenEncryptionKey = ""
		t.Cleanup(func() { tokenEncryptionKey = prev })

		srv, _ := setupPaperlessMock(t, "", "")
		legacy := legacyV1Token(t, "secret", "some-key")
		assert.Equal(t, legacy, srv.upgradeLegacyToken(context.Background(), userID, legacy))
	})

	t.Run("decrypt failure leaves the token untouched", func(t *testing.T) {
		prev := tokenEncryptionKey
		tokenEncryptionKey = "wrong-key"
		t.Cleanup(func() { tokenEncryptionKey = prev })

		srv, _ := setupPaperlessMock(t, "", "")
		legacy := legacyV1Token(t, "secret", "other-key")
		assert.Equal(t, legacy, srv.upgradeLegacyToken(context.Background(), userID, legacy))
	})

	t.Run("persist failure returns the original token", func(t *testing.T) {
		prev := tokenEncryptionKey
		tokenEncryptionKey = "key"
		t.Cleanup(func() { tokenEncryptionKey = prev })

		srv, mock := setupPaperlessMock(t, "", "")
		legacy := legacyV1Token(t, "secret", tokenEncryptionKey)
		// The re-seal is a compare-and-swap on the value that was read, so the
		// update binds three arguments (new ciphertext, user id, expected value).
		mock.ExpectExec("UPDATE users SET paperless_token").
			WithArgs(pgxmock.AnyArg(), userID, legacy).
			WillReturnError(assert.AnError)

		assert.Equal(t, legacy, srv.upgradeLegacyToken(context.Background(), userID, legacy))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a row changed under us keeps its newer token", func(t *testing.T) {
		prev := tokenEncryptionKey
		tokenEncryptionKey = "key"
		t.Cleanup(func() { tokenEncryptionKey = prev })

		srv, mock := setupPaperlessMock(t, "", "")
		legacy := legacyV1Token(t, "secret", tokenEncryptionKey)
		// The CAS matched nothing: another read re-sealed first, or the user
		// saved a new token while this request was in flight.
		mock.ExpectExec("UPDATE users SET paperless_token").
			WithArgs(pgxmock.AnyArg(), userID, legacy).
			WillReturnResult(pgxmock.NewResult("UPDATE", 0))

		assert.Equal(t, legacy, srv.upgradeLegacyToken(context.Background(), userID, legacy))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestGetPaperlessSettingsConfigError(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")
	mock.ExpectQuery("SELECT paperless_url, paperless_token, paperless_tag, page_size FROM users").
		WithArgs(testUserID()).
		WillReturnError(assert.AnError)

	w := httptest.NewRecorder()
	newPaperlessTestRouter(srv).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/paperless/settings", nil))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPaperlessDialContext(t *testing.T) {
	t.Run("missing port is rejected", func(t *testing.T) {
		dial := paperlessDialContext("development")
		_, err := dial(context.Background(), "tcp", "noport")
		assert.Error(t, err)
	})

	t.Run("production rejects non-web ports before dialing", func(t *testing.T) {
		dial := paperlessDialContext("production")
		_, err := dial(context.Background(), "tcp", "paperless.example.com:8080")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not allowed")
	})
}

func TestRejectPaperlessRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	assert.False(t, rejectPaperlessRedirect(c, http.StatusOK))
	assert.True(t, rejectPaperlessRedirect(c, http.StatusFound))
	assert.Equal(t, http.StatusBadGateway, c.Writer.Status())
}

func TestIsDisallowedPaperlessIPRanges(t *testing.T) {
	// Multicast, link-local, IPv4-mapped private, and CGNAT boundaries.
	assert.True(t, isDisallowedPaperlessIP(net.ParseIP("224.0.0.1"), true))
	assert.True(t, isDisallowedPaperlessIP(net.ParseIP("fe80::1"), true))
	assert.True(t, isDisallowedPaperlessIP(net.ParseIP("100.127.255.255"), true))
	assert.False(t, isDisallowedPaperlessIP(net.ParseIP("100.128.0.1"), false))
}
