package mcpserver

import (
	"context"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fintrak/client/api"
)

// methodCallTool is the MCP method a tool invocation arrives as. The SDK keeps
// the constant unexported, so the string is repeated here; it is fixed by the
// protocol.
const methodCallTool = "tools/call"

// Credentials are what the server signs in with.
type Credentials struct {
	Email    string
	Password string
}

// session signs the client in on demand.
//
// An MCP server is started by the client and then runs for as long as the user
// keeps it around, which makes an eager sign-in at startup the wrong shape: a
// backend that is briefly unreachable, or credentials the user is still fixing,
// would take the whole server down instead of failing one tool call. Instead
// the first tool call signs in, and the API client keeps the session alive from
// there — its own 401 replay trades the refresh token for a new access token.
// If the session ever ends for good (the client clears its tokens when a
// refresh is rejected), the next call signs in again, so a long-lived server
// recovers on its own.
//
// The sign-in belongs above the transport because the request has to be built
// after it: a transport sees a request that already attached the session cookie
// it had at the time, so signing in inside one would send the first call
// unauthenticated and pay for a 401 round trip.
type session struct {
	client   *api.Client
	email    string
	password string

	// mu serializes sign-ins so a burst of tool calls produces one login
	// rather than one per call.
	mu sync.Mutex
}

// ensure signs in when the client has no session and credentials are
// configured. It is a no-op once signed in, and for a server started with a
// session rather than credentials.
func (s *session) ensure(ctx context.Context) error {
	if s.email == "" || s.client.HasSession() {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Another call may have signed in while this one waited for the lock.
	if s.client.HasSession() {
		return nil
	}
	if _, err := s.client.Login(ctx, s.email, s.password); err != nil {
		return fmt.Errorf("signing in as %s: %w", s.email, err)
	}
	return nil
}

// sessionMiddleware signs the client in before a tool call, which is the one
// place every invocation passes through: a tool added later is covered without
// its handler knowing about sessions at all.
func sessionMiddleware(s *session) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodCallTool {
				return next(ctx, method, req)
			}
			if err := s.ensure(ctx); err != nil {
				// A tool error rather than a protocol error: the model should
				// see the sign-in failure and be able to report it.
				return &mcp.CallToolResult{
					IsError: true,
					Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
				}, nil
			}
			return next(ctx, method, req)
		}
	}
}
