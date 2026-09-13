package handlers

import (
	"testing"

	"github.com/fintrak/backend/db"
	"github.com/pashagolub/pgxmock/v5"
)

// testParserURL is a sentinel parser base URL for handler tests that don't
// exercise the parser.
const testParserURL = "http://parser.test"

// newTestServer builds a Server over the given pool for tests. Handlers read
// their dependencies from the Server, so each test owns an isolated instance
// instead of swapping a package global.
func newTestServer(pool db.DBPool) *Server {
	return NewServer(pool, testParserURL, 0)
}

// newMockServer creates a mock pool and a Server over it, closing the mock when
// the test finishes.
func newMockServer(t *testing.T) (*Server, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mock.Close)
	return newTestServer(mock), mock
}
