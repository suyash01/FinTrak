package handlers

import "github.com/fintrak/backend/db"

// maxConcurrentParses bounds how many statement-parser forwards run at once.
// Parsing an attacker-controlled PDF is expensive (decompression + text
// extraction), so a burst — e.g. a multi-document Paperless batch — must not
// be able to overload the parser. This is the authoritative server-side limit;
// the frontend's client pool is only a courtesy.
const maxConcurrentParses = 4

// Server carries the dependencies shared by the HTTP handlers. Handlers are
// methods on it so the database pool, the statement-parser URL, and the
// outbound log body limit are explicit arguments to construction rather than
// package-level globals: main wires the real values once, and tests construct
// their own Server with a mock pool, which keeps tests isolated (no shared
// mutable state).
type Server struct {
	db           db.DBPool
	parserURL    string
	logBodyLimit int
	// parseSem limits concurrent parser forwards (see maxConcurrentParses).
	parseSem chan struct{}
}

// NewServer builds a Server over the given database pool, statement-parser base
// URL, and outbound log body limit (bytes; <= 0 disables truncation). parserURL
// may be empty, in which case the parser endpoints report a configuration error.
func NewServer(pool db.DBPool, parserURL string, logBodyLimit int) *Server {
	return &Server{
		db:           pool,
		parserURL:    parserURL,
		logBodyLimit: logBodyLimit,
		parseSem:     make(chan struct{}, maxConcurrentParses),
	}
}
